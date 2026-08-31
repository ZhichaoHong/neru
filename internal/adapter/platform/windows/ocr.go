//go:build windows

package windows

import (
	"errors"
	"fmt"
	"image"
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/y3owk1n/neru/internal/derrors"
)

// Text recognition through Windows.Media.Ocr, driven by raw vtable calls because
// CGO is disabled for every Windows build.
//
// The names here mirror the Linux ones - OCRHealth, RecognizeText, OCRWord,
// OCRParams, OCRStats - so the two vision adapters read alike. Two differences
// are real and declared rather than papered over:
//
//   - No confidence score. Windows.Media.Ocr reports none, at any level, so
//     every OCRWord comes back with Confidence 0. hints.vision.minimum_confidence
//     is therefore unsupported on Windows, not defaulted.
//   - No language option, for the same reason Linux pins one: the engine takes
//     whatever OCR data the user installed, and the recognizer language it
//     settled on is logged so a substitution stays visible.
//
// Recognized text is screen content. It is never logged, never written to disk,
// and never held past the recognition that produced it.

// IIDs, from windows.media.ocr.idl, windows.graphics.imaging.idl,
// windows.foundation.idl, MemoryBuffer.h and windows.globalization.idl in
// Windows Kits\10\Include\10.0.26100.0.
//
// No parameterized IID appears, and none is needed: every generic the path reads
// - IVectorView<OcrLine>, IVectorView<OcrWord>, IAsyncOperation<OcrResult>,
// IReference<double> - arrives as an [out] [retval] already typed as that
// interface, so it is called directly rather than QueryInterface'd for.
var (
	iidIOcrEngineStatics       = mustIID("{5BFFA85A-3384-3540-9940-699120D428A8}")
	iidISoftwareBitmapFactory  = mustIID("{C99FEB69-2D62-4D47-A6B3-4FDB6A07FDF8}")
	iidISoftwareBitmap         = mustIID("{689E0708-7EEF-483F-963F-DA938818E073}")
	iidIMemoryBuffer           = mustIID("{FBC4DD2A-245B-11E4-AF98-689423260CF8}")
	iidIMemoryBufferByteAccess = mustIID("{5B0D3235-4DBA-4D44-865E-8F1D0E4FD04D}")
	iidIAsyncInfo              = mustIID("{00000036-0000-0000-C000-000000000046}")
	iidILanguageFactory        = mustIID("{9B0252AC-0C27-44F8-B792-9793FB66C63E}")
)

const (
	classOcrEngine      = "Windows.Media.Ocr.OcrEngine"
	classSoftwareBitmap = "Windows.Graphics.Imaging.SoftwareBitmap"
	classLanguage       = "Windows.Globalization.Language"
)

// BitmapPixelFormat, BitmapAlphaMode and BitmapBufferAccessMode values.
// BitmapBufferAccessMode is Read=0, ReadWrite=1, Write=2 - not the order the
// names suggest, which is worth stating because reading it wrong yields a
// buffer that silently discards the writes.
const (
	pixelFormatRgba8 = 30
	alphaStraight    = 1
	accessReadWrite  = 1
)

// AsyncStatus.
const (
	asyncStarted = iota
	asyncCompleted
	asyncCanceled
	asyncError
)

// asyncPollInterval is how often the recognition's status is read. A sleep, not
// a spin: the OCR thread has nothing else to do, and one recognition takes two
// orders of magnitude longer than this.
const asyncPollInterval = 1500 * time.Microsecond

// OCRWord is one run of recognized text, in the coordinate space of the image it
// was read from - origin (0, 0) at the top-left of that buffer, not of the
// screen.
//
// Text is screen content and carries the same rules the capture buffer does: it
// is never logged, never written to disk, and never held past the detection that
// asked for it.
type OCRWord struct {
	Text   string
	Bounds image.Rectangle
	// Confidence is 0..1 to match the Linux and macOS backends, and is always 0
	// here: Windows.Media.Ocr exposes no confidence on OcrWord, OcrLine or
	// OcrResult. Callers must not gate on it - see the adapter's header comment.
	Confidence float64
}

// OCRStats describes the work one recognition did, so a caller can log it.
// Durations and counts only - nothing here derives from what was on the screen.
type OCRStats struct {
	// Recognition is how long the engine spent on the frame, which is the
	// number that separates "gave up on the deadline" from "failed instantly".
	Recognition time.Duration
}

// OCRParams is what one recognition needs beyond the pixels.
type OCRParams struct {
	// WordLevel asks for per-word boxes instead of per-line ones, which is what
	// `neru hints --split-word` means.
	WordLevel bool
	// TimeoutMS bounds the recognition. Zero or less means no deadline.
	TimeoutMS int
}

// ocrRuntime owns everything the OCR path allocates for the process: the COM
// thread, the activation factories, and the engine.
//
// Holding them is what keeps recognition cheap - resolving the factories costs
// roughly 14 ms, which is a third of a whole recognition to pay again per call.
// The mutex serializes callers, and running the vtable calls on comThread is
// what makes that serialization sufficient: RecognizeAsync is single-flight per
// engine, and an overlapping call does not fail at call time. It hands back a
// live operation that later lands in Status=Error with ErrorCode=E_ABORT, which
// would read as a flaky engine rather than as a race.
type ocrRuntime struct {
	mu     sync.Mutex
	thread *comThread

	statics    unsafe.Pointer // IOcrEngineStatics
	bitmapFac  unsafe.Pointer // ISoftwareBitmapFactory
	langFac    unsafe.Pointer // ILanguageFactory
	maxDim     uint32
	initErr    error
	initedOnce bool

	engine     unsafe.Pointer // IOcrEngine, nil until first use or after a timeout
	engineLang string
}

var ocr ocrRuntime

// ensureStatics resolves the activation factories once. The failure is cached:
// a machine with no OCR data does not acquire it between two hint invocations,
// and retrying the factory resolution on every call would spend 14 ms to learn
// the same thing.
func (r *ocrRuntime) ensureStatics() error {
	if r.initedOnce {
		return r.initErr
	}

	r.initedOnce = true
	r.initErr = r.openStatics()

	if r.initErr != nil {
		r.closeStatics()
	}

	return r.initErr
}

func (r *ocrRuntime) openStatics() error {
	thread, err := newCOMThread()
	if err != nil {
		return derrors.Wrap(
			err,
			derrors.CodeNotSupported,
			"the vision hint strategy could not join a COM apartment",
		)
	}

	r.thread = thread

	var inner error

	r.thread.run(func() {
		if r.statics, inner = activationFactory(classOcrEngine, iidIOcrEngineStatics); inner != nil {
			return
		}

		if r.bitmapFac, inner = activationFactory(
			classSoftwareBitmap,
			iidISoftwareBitmapFactory,
		); inner != nil {
			return
		}

		if r.langFac, inner = activationFactory(classLanguage, iidILanguageFactory); inner != nil {
			return
		}

		inner = comVCall("IOcrEngineStatics.get_MaxImageDimension", r.statics, 6,
			uintptr(unsafe.Pointer(&r.maxDim)))
	})

	if inner != nil {
		return derrors.Wrap(
			inner,
			derrors.CodeNotSupported,
			"the vision hint strategy needs the Windows.Media.Ocr runtime classes, "+
				"which this machine did not provide",
		)
	}

	return nil
}

func (r *ocrRuntime) closeStatics() {
	if r.thread == nil {
		return
	}

	r.thread.run(func() {
		comRelease(r.engine)
		comRelease(r.langFac)
		comRelease(r.bitmapFac)
		comRelease(r.statics)

		r.engine, r.langFac, r.bitmapFac, r.statics = nil, nil, nil, nil
	})
}

// availableLanguages lists the languages this machine has OCR data for. It is
// the engine-free health probe: static, and an empty list is the honest "no OCR
// data installed" signal rather than an error to interpret.
//
// Must run on the COM thread.
func (r *ocrRuntime) availableLanguages() ([]string, error) {
	var view unsafe.Pointer

	err := comVCall("IOcrEngineStatics.get_AvailableRecognizerLanguages", r.statics, 7,
		uintptr(unsafe.Pointer(&view)))
	if err != nil {
		return nil, err
	}

	languages := vectorView{ptr: view, name: "IVectorView<Language>"}
	defer languages.release()

	count, err := languages.size()
	if err != nil {
		return nil, err
	}

	tags := make([]string, 0, count)

	for i := uint32(0); i < count; i++ {
		language, err := languages.at(i)
		if err != nil {
			return nil, err
		}

		tag, err := readHStringOut("ILanguage.get_LanguageTag", language, 6)

		comRelease(language)

		if err != nil {
			return nil, err
		}

		tags = append(tags, tag)
	}

	return tags, nil
}

// errNoLanguageData is what a machine with the runtime classes but no installed
// OCR model reports. It names the exact place a user fixes it, because "OCR is
// unavailable" is not actionable and this is the failure most users will hit.
var errNoLanguageData = derrors.New(
	derrors.CodeNotSupported,
	"the vision hint strategy needs Windows OCR language data, and none is installed; "+
		"add a language under Settings > Time & language > Language & region > "+
		"Add a language, with the optional Language pack included",
)

// ensureEngine creates the engine if there is not one already.
//
// Must run on the COM thread.
func (r *ocrRuntime) ensureEngine() error {
	if r.engine != nil {
		return nil
	}

	var engine unsafe.Pointer

	err := comVCall("IOcrEngineStatics.TryCreateFromUserProfileLanguages", r.statics, 10,
		uintptr(unsafe.Pointer(&engine)))
	if err != nil {
		return derrors.Wrap(
			err,
			derrors.CodeNotSupported,
			"the Windows OCR engine could not be created",
		)
	}

	if engine == nil {
		// S_OK with a null engine means no language in the user's profile
		// resolved to installed OCR data. Fall back to the first language that
		// is installed rather than to a hardcoded en-US: a machine with only
		// ja-JP data can still read Latin script, and guessing en-US would
		// report "unavailable" on a machine that works.
		languages, err := r.availableLanguages()
		if err != nil {
			return derrors.Wrap(
				err,
				derrors.CodeNotSupported,
				"the installed Windows OCR languages could not be listed",
			)
		}

		if len(languages) == 0 {
			return errNoLanguageData
		}

		if engine, err = r.engineForLanguage(languages[0]); err != nil {
			return err
		}
	}

	r.engine = engine
	r.engineLang, _ = r.recognizerLanguage()

	return nil
}

// engineForLanguage builds an engine for one BCP-47 tag.
//
// Must run on the COM thread.
func (r *ocrRuntime) engineForLanguage(tag string) (unsafe.Pointer, error) {
	name, err := newHString(tag)
	if err != nil {
		return nil, err
	}

	defer name.free()

	var language unsafe.Pointer

	if err := comVCall("ILanguageFactory.CreateLanguage", r.langFac, 6,
		uintptr(name), uintptr(unsafe.Pointer(&language))); err != nil {
		return nil, derrors.Wrapf(
			err,
			derrors.CodeNotSupported,
			"Windows rejected the OCR language %q",
			tag,
		)
	}

	defer comRelease(language)

	var engine unsafe.Pointer

	if err := comVCall("IOcrEngineStatics.TryCreateFromLanguage", r.statics, 9,
		uintptr(language), uintptr(unsafe.Pointer(&engine))); err != nil {
		return nil, derrors.Wrapf(
			err,
			derrors.CodeNotSupported,
			"the Windows OCR engine could not be created for %q",
			tag,
		)
	}

	if engine == nil {
		return nil, errNoLanguageData
	}

	return engine, nil
}

// recognizerLanguage reads which language the engine actually settled on, via
// IOcrEngine.get_RecognizerLanguage (slot 7) then ILanguage.get_LanguageTag
// (slot 6). The adapter logs it so a substitution stays visible.
//
// Must run on the COM thread.
func (r *ocrRuntime) recognizerLanguage() (string, error) {
	var language unsafe.Pointer

	err := comVCall("IOcrEngine.get_RecognizerLanguage", r.engine, 7,
		uintptr(unsafe.Pointer(&language)))
	if err != nil {
		return "", err
	}

	defer comRelease(language)

	return readHStringOut("ILanguage.get_LanguageTag", language, 6)
}

// discardEngine drops the engine so the next call builds a fresh one.
//
// A recognition that ran past its deadline is canceled, but Cancel returns
// immediately while the engine keeps working for roughly 231 ms - so this
// instance would fail the next call with E_ABORT. A fresh engine costs about
// 24 ms and works immediately, which is why there is no backoff here.
//
// Must run on the COM thread.
func (r *ocrRuntime) discardEngine() {
	comRelease(r.engine)

	r.engine = nil
	r.engineLang = ""
}

// OCRHealth reports whether text recognition can run on this machine.
//
// It resolves the runtime classes and creates the engine, which is the same
// work the first recognition would do - so a machine that passes here does not
// then fail on the frame. It deliberately does not capture the screen: this is
// the probe `neru doctor` runs, and a capability check has no business reading
// what is on the display.
func OCRHealth() error {
	ocr.mu.Lock()
	defer ocr.mu.Unlock()

	if err := ocr.ensureStatics(); err != nil {
		return err
	}

	var err error

	ocr.thread.run(func() {
		err = ocr.ensureEngine()
	})

	return err
}

// OCRLanguage reports the language the engine settled on, or an empty string
// before the first successful recognition. It exists so the adapter can log a
// language substitution; it is not a config value.
func OCRLanguage() string {
	ocr.mu.Lock()
	defer ocr.mu.Unlock()

	return ocr.engineLang
}

// RecognizeText reads text out of one captured frame.
//
// The returned bounds are in img's own coordinate space, so the caller adds the
// captured region's origin back. Confidence is always 0; see OCRWord.
func RecognizeText(img *image.RGBA, params OCRParams) ([]OCRWord, OCRStats, error) {
	var stats OCRStats

	if img == nil || img.Bounds().Empty() {
		return nil, stats, derrors.New(
			derrors.CodeActionFailed,
			"the captured frame is empty, so there is nothing to recognize",
		)
	}

	ocr.mu.Lock()
	defer ocr.mu.Unlock()

	if err := ocr.ensureStatics(); err != nil {
		return nil, stats, err
	}

	bounds := img.Bounds()
	if err := ocr.checkImageDimension(bounds); err != nil {
		return nil, stats, err
	}

	var (
		words []OCRWord
		err   error
	)

	ocr.thread.run(func() {
		if err = ocr.ensureEngine(); err != nil {
			return
		}

		var bitmap unsafe.Pointer

		if bitmap, err = ocr.newBitmap(img); err != nil {
			return
		}

		defer func() {
			comClose(bitmap)
			comRelease(bitmap)
		}()

		words, stats, err = ocr.recognize(bitmap, params)
	})

	return words, stats, err
}

// checkImageDimension refuses a frame the engine would reject with a bare
// E_INVALIDARG, so the error names the limit and the option that fixes it.
func (r *ocrRuntime) checkImageDimension(bounds image.Rectangle) error {
	if r.maxDim == 0 {
		return nil
	}

	limit := int(r.maxDim)
	if bounds.Dx() <= limit && bounds.Dy() <= limit {
		return nil
	}

	return derrors.Newf(
		derrors.CodeActionFailed,
		"the captured frame is %dx%d, past the %d-pixel limit the Windows OCR engine "+
			"accepts; narrow what is being read with hints.vision.rectangles or by "+
			"hinting a single window",
		bounds.Dx(), bounds.Dy(), limit,
	)
}

// newBitmap builds the SoftwareBitmap the engine reads, writing pixels straight
// into the bitmap's own memory through LockBuffer. That is one full-frame copy;
// the documented CreateCopyFromBuffer route costs two.
//
// Must run on the COM thread.
func (r *ocrRuntime) newBitmap(img *image.RGBA) (unsafe.Pointer, error) {
	pix, width, height := tightPixels(img)

	var bitmap unsafe.Pointer

	err := comVCall("ISoftwareBitmapFactory.CreateWithAlpha", r.bitmapFac, 7,
		pixelFormatRgba8, uintptr(width), uintptr(height), alphaStraight,
		uintptr(unsafe.Pointer(&bitmap)))
	if err != nil {
		return nil, derrors.Wrap(
			err,
			derrors.CodeInternal,
			"the captured frame could not be handed to the Windows OCR engine",
		)
	}

	dst, stride, unlock, err := lockBitmapPixels(bitmap)
	if err != nil {
		comRelease(bitmap)

		return nil, derrors.Wrap(
			err,
			derrors.CodeInternal,
			"the OCR bitmap's pixel buffer could not be locked",
		)
	}

	defer unlock()

	// The stride WinRT chose is read, never assumed: it is free to pad rows, and
	// a mismatch copied as one block would shear the frame.
	rowLen := width * 4
	if stride == rowLen {
		copy(dst, pix)
	} else {
		for y := 0; y < height; y++ {
			copy(dst[y*stride:y*stride+rowLen], pix[y*rowLen:(y+1)*rowLen])
		}
	}

	runtime.KeepAlive(pix)

	return bitmap, nil
}

// recognize runs one recognition to completion. The caller owns bitmap.
//
// Must run on the COM thread.
func (r *ocrRuntime) recognize(bitmap unsafe.Pointer, params OCRParams) ([]OCRWord, OCRStats, error) {
	var stats OCRStats

	started := time.Now()

	var operation unsafe.Pointer

	err := comVCall("IOcrEngine.RecognizeAsync", r.engine, 6,
		uintptr(bitmap), uintptr(unsafe.Pointer(&operation)))
	if err != nil {
		r.discardEngine()

		return nil, stats, recognitionError(err)
	}

	if operation == nil {
		return nil, stats, derrors.New(
			derrors.CodeInternal,
			"the Windows OCR engine accepted the frame but returned no operation",
		)
	}

	defer comRelease(operation)

	info, err := comQueryInterface(operation, iidIAsyncInfo, "IAsyncInfo")
	if err != nil {
		return nil, stats, derrors.Wrap(
			err,
			derrors.CodeInternal,
			"the Windows OCR operation could not be awaited",
		)
	}

	defer comRelease(info)

	status, err := awaitTerminal(info, timeoutOf(params))

	stats.Recognition = time.Since(started)

	if err != nil {
		_ = comVCall("IAsyncInfo.Cancel", info, 9)
		_ = comVCall("IAsyncInfo.Close", info, 10)

		r.discardEngine()

		return nil, stats, err
	}

	switch status {
	case asyncCompleted:
	case asyncError:
		var code uint32

		_ = comVCall("IAsyncInfo.get_ErrorCode", info, 8, uintptr(unsafe.Pointer(&code)))
		_ = comVCall("IAsyncInfo.Close", info, 10)

		r.discardEngine()

		return nil, stats, recognitionError(&comError{
			hr:  code,
			ctx: "RecognizeAsync failed",
		})
	case asyncCanceled:
		_ = comVCall("IAsyncInfo.Close", info, 10)

		return nil, stats, derrors.New(
			derrors.CodeActionFailed,
			"text recognition was canceled before it finished",
		)
	default:
		return nil, stats, derrors.Newf(
			derrors.CodeInternal,
			"text recognition ended in unexpected status %d",
			status,
		)
	}

	var result unsafe.Pointer

	if err := comVCall("IAsyncOperation.GetResults", operation, 8,
		uintptr(unsafe.Pointer(&result))); err != nil {
		return nil, stats, recognitionError(err)
	}

	if result == nil {
		return nil, stats, derrors.New(
			derrors.CodeInternal,
			"the Windows OCR engine finished but returned no result",
		)
	}

	defer comRelease(result)

	words, err := extractWords(result, params.WordLevel)

	// Close last. get_Status and GetResults both return E_ILLEGAL_METHOD_CALL
	// once the operation is closed, so closing earlier would break the read
	// above rather than tidy up after it.
	_ = comVCall("IAsyncInfo.Close", info, 10)

	if err != nil {
		return nil, stats, derrors.Wrap(
			err,
			derrors.CodeInternal,
			"the Windows OCR result could not be read",
		)
	}

	return words, stats, nil
}

// timeoutOf turns the configured budget into a duration. Zero or less means no
// deadline, which matches hints.vision.request_timeout_ms on the other
// platforms.
func timeoutOf(params OCRParams) time.Duration {
	if params.TimeoutMS <= 0 {
		return 0
	}

	return time.Duration(params.TimeoutMS) * time.Millisecond
}

// recognitionError maps a COM failure onto the shared error vocabulary.
//
// Everything here is CodeActionFailed rather than CodeNotSupported: the engine
// exists and was created, so one frame failed, and a caller should retry or
// narrow the frame rather than conclude the platform cannot do OCR.
func recognitionError(err error) error {
	if isCOMError(err, hrEAbort) {
		return derrors.Wrap(
			err,
			derrors.CodeActionFailed,
			"the Windows OCR engine was still busy with an earlier frame; try again",
		)
	}

	if isCOMError(err, hrEInvalidArg) {
		return derrors.Wrap(
			err,
			derrors.CodeActionFailed,
			"the captured frame is not a shape the Windows OCR engine can read",
		)
	}

	return derrors.Wrap(
		err,
		derrors.CodeActionFailed,
		"the Windows OCR engine could not read the captured frame",
	)
}

// awaitTerminal polls IAsyncInfo.get_Status (slot 7) until it leaves Started.
//
// Polling rather than a completion handler is deliberate: a handler would mean
// implementing a COM delegate - a hand-built vtable Windows calls back into on
// its own thread pool - and the pure-Go cost of that is far higher than a
// 1.5 ms sleep on a thread that has nothing else to do.
func awaitTerminal(info unsafe.Pointer, timeout time.Duration) (uint32, error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	for {
		var status uint32

		err := comVCall("IAsyncInfo.get_Status", info, 7, uintptr(unsafe.Pointer(&status)))
		if err != nil {
			return 0, derrors.Wrap(
				err,
				derrors.CodeInternal,
				"the Windows OCR operation's status could not be read",
			)
		}

		if status != asyncStarted {
			return status, nil
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			return 0, derrors.Newf(
				derrors.CodeActionFailed,
				"text recognition ran past hints.vision.request_timeout_ms (%s); raise it, "+
					"or narrow what is being read by hinting a smaller window",
				timeout,
			)
		}

		time.Sleep(asyncPollInterval)
	}
}

// ocrRect is Windows.Foundation.Rect: four float32 in image coordinates.
type ocrRect struct {
	X, Y, W, H float32
}

func (r ocrRect) toImage() image.Rectangle {
	// Round outward. The engine reports fractional edges, and a box rounded
	// inward can crop the glyph it describes - which matters because these
	// rectangles become the clickable target.
	return image.Rect(
		int(math.Floor(float64(r.X))),
		int(math.Floor(float64(r.Y))),
		int(math.Ceil(float64(r.X+r.W))),
		int(math.Ceil(float64(r.Y+r.H))),
	)
}

// extractWords walks the result: IOcrResult (6 get_Lines, 7 get_TextAngle,
// 8 get_Text), IOcrLine (6 get_Words, 7 get_Text), IOcrWord
// (6 get_BoundingRect, 7 get_Text).
//
// wordLevel picks which level becomes an OCRWord. A line has no BoundingRect of
// its own, so a line box is the union of its words' - which is also why an empty
// line is dropped rather than reported at the origin.
//
// Must run on the COM thread.
func extractWords(result unsafe.Pointer, wordLevel bool) ([]OCRWord, error) {
	var linesView unsafe.Pointer

	if err := comVCall("IOcrResult.get_Lines", result, 6,
		uintptr(unsafe.Pointer(&linesView))); err != nil {
		return nil, err
	}

	lines := vectorView{ptr: linesView, name: "IVectorView<OcrLine>"}
	defer lines.release()

	count, err := lines.size()
	if err != nil {
		return nil, err
	}

	var out []OCRWord

	for i := uint32(0); i < count; i++ {
		line, err := lines.at(i)
		if err != nil {
			return nil, err
		}

		words, err := extractLine(line, wordLevel)

		comRelease(line)

		if err != nil {
			return nil, err
		}

		out = append(out, words...)
	}

	return out, nil
}

// extractLine returns one line's words, or the line itself as a single entry
// when per-word boxes were not asked for.
//
// Must run on the COM thread.
func extractLine(line unsafe.Pointer, wordLevel bool) ([]OCRWord, error) {
	var wordsView unsafe.Pointer

	if err := comVCall("IOcrLine.get_Words", line, 6,
		uintptr(unsafe.Pointer(&wordsView))); err != nil {
		return nil, err
	}

	words := vectorView{ptr: wordsView, name: "IVectorView<OcrWord>"}
	defer words.release()

	count, err := words.size()
	if err != nil {
		return nil, err
	}

	out := make([]OCRWord, 0, count)
	lineBounds := image.Rectangle{}

	for i := uint32(0); i < count; i++ {
		word, err := words.at(i)
		if err != nil {
			return nil, err
		}

		text, err := readHStringOut("IOcrWord.get_Text", word, 7)
		if err != nil {
			comRelease(word)

			return nil, err
		}

		var box ocrRect

		if err := comVCall("IOcrWord.get_BoundingRect", word, 6,
			uintptr(unsafe.Pointer(&box))); err != nil {
			comRelease(word)

			return nil, err
		}

		comRelease(word)

		bounds := box.toImage()
		lineBounds = lineBounds.Union(bounds)

		if wordLevel {
			out = append(out, OCRWord{Text: text, Bounds: bounds})
		}
	}

	if wordLevel {
		return out, nil
	}

	if lineBounds.Empty() {
		return nil, nil
	}

	text, err := readHStringOut("IOcrLine.get_Text", line, 7)
	if err != nil {
		return nil, err
	}

	return []OCRWord{{Text: text, Bounds: lineBounds}}, nil
}

// lockBitmapPixels returns a writable view of a SoftwareBitmap's own pixels,
// plus the stride it chose and the teardown that releases the lock.
//
// ISoftwareBitmap.LockBuffer is slot 15. The BitmapBuffer it hands back must be
// closed and released before the bitmap reaches RecognizeAsync, or the engine
// reads a bitmap someone else still holds locked.
//
// Must run on the COM thread.
func lockBitmapPixels(bitmap unsafe.Pointer) (pixels []byte, stride int, unlock func(), err error) {
	software, err := comQueryInterface(bitmap, iidISoftwareBitmap, "ISoftwareBitmap")
	if err != nil {
		return nil, 0, nil, err
	}

	defer comRelease(software)

	var buffer unsafe.Pointer

	if err := comVCall("ISoftwareBitmap.LockBuffer", software, 15,
		accessReadWrite, uintptr(unsafe.Pointer(&buffer))); err != nil {
		return nil, 0, nil, err
	}

	releaseBuffer := func() {
		comClose(buffer)
		comRelease(buffer)
	}

	// IBitmapBuffer.GetPlaneDescription is slot 7; GetPlaneCount is 6.
	var plane struct {
		StartIndex, Width, Height, Stride int32
	}

	if err := comVCall("IBitmapBuffer.GetPlaneDescription", buffer, 7,
		0, uintptr(unsafe.Pointer(&plane))); err != nil {
		releaseBuffer()

		return nil, 0, nil, err
	}

	memory, err := comQueryInterface(buffer, iidIMemoryBuffer, "IMemoryBuffer")
	if err != nil {
		releaseBuffer()

		return nil, 0, nil, err
	}

	defer comRelease(memory)

	var reference unsafe.Pointer

	if err := comVCall("IMemoryBuffer.CreateReference", memory, 6,
		uintptr(unsafe.Pointer(&reference))); err != nil {
		releaseBuffer()

		return nil, 0, nil, err
	}

	releaseAll := func() {
		comClose(reference)
		comRelease(reference)
		releaseBuffer()
	}

	access, err := comQueryInterface(reference, iidIMemoryBufferByteAccess,
		"IMemoryBufferByteAccess")
	if err != nil {
		releaseAll()

		return nil, 0, nil, err
	}

	defer comRelease(access)

	// IMemoryBufferByteAccess derives from IUnknown, not IInspectable, so
	// GetBuffer is slot 3.
	var (
		base     unsafe.Pointer
		capacity uint32
	)

	if err := comVCall("IMemoryBufferByteAccess.GetBuffer", access, 3,
		uintptr(unsafe.Pointer(&base)), uintptr(unsafe.Pointer(&capacity))); err != nil {
		releaseAll()

		return nil, 0, nil, err
	}

	if base == nil || capacity == 0 {
		releaseAll()

		return nil, 0, nil, errors.New("the OCR bitmap reported an empty pixel buffer")
	}

	if int(plane.StartIndex) > int(capacity) {
		releaseAll()

		return nil, 0, nil, fmt.Errorf(
			"the OCR bitmap's plane starts at %d in a %d-byte buffer",
			plane.StartIndex, capacity,
		)
	}

	view := unsafe.Slice((*byte)(unsafe.Pointer(base)), capacity)[plane.StartIndex:]

	return view, int(plane.Stride), releaseAll, nil
}

// tightPixels returns row-packed RGBA bytes and the frame's dimensions. A
// SubImage carries the parent's stride, so its rows have to be repacked.
func tightPixels(img *image.RGBA) (pix []byte, width, height int) {
	bounds := img.Bounds()
	width, height = bounds.Dx(), bounds.Dy()
	rowLen := width * 4

	if img.Stride == rowLen && len(img.Pix) == rowLen*height {
		return img.Pix, width, height
	}

	packed := make([]byte, rowLen*height)

	for y := 0; y < height; y++ {
		copy(packed[y*rowLen:(y+1)*rowLen], img.Pix[y*img.Stride:y*img.Stride+rowLen])
	}

	return packed, width, height
}
