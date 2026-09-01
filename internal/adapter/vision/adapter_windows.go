//go:build windows

package vision

import (
	"context"
	"image"
	"time"

	"go.uber.org/zap"

	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
	"github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/derrors"
	"github.com/y3owk1n/neru/internal/domain/element"
)

// Windows answers this port with two native pieces, both cgo-free because every
// Windows build in the justfile sets CGO_ENABLED=0: screen capture through GDI
// (BitBlt into a memory bitmap, GetDIBits back out) and recognition through
// Windows.Media.Ocr, driven by raw WinRT vtable calls.
//
// It answers the *text* half of the strategy only, the same as Linux. macOS runs
// three Vision requests - text recognition, rectangle detection and saliency -
// and an OCR engine answers the first. hints.vision.detect_rectangles and its
// four rectangle_* companions stay macOS-only rather than being met with a
// contour-detection library, so Windows `vision` is text-only and says so
// (docs/adr/0013-parity-is-measured-in-words-not-subsystems.md).
//
// One option more than Linux is exempt: hints.vision.minimum_confidence.
// Windows.Media.Ocr reports no per-word confidence anywhere in its API - not on
// OcrWord, not on OcrLine, not on OcrResult - so there is no number to threshold
// and the option is declared unsupported rather than met with a made-up score.
// That is why the call below passes a literal 0 where adapter_linux.go passes
// cfg.MinimumConfidence: reading the option would let a user set it, see no
// change, and conclude the filter ran.
//
// The same missing score is what makes hints.vision.button_min_confidence a
// footgun here rather than an exemption. It is honored, because the classifier
// reads it, but every word scores 0, so any value above 0 suppresses Button
// classification entirely. config_windows.go defaults it to 0 for exactly that
// reason.
//
// Privacy runs through every line below. Recognized text is screen content: it
// reaches an element's title and search text, where the hint pipeline needs it,
// and nowhere else. Nothing here logs it, counts characters of it into a message,
// or writes it anywhere; the debug lines carry durations and counts.

// DetectElements captures screenBounds and returns the text found in it as
// hintable elements.
//
// screenBounds is where the caller wants hints - normally the focused window - in
// virtual-desktop physical pixels, top-left origin, and it is honored rather than
// widened: full-desktop OCR takes seconds, one window is tens of milliseconds to
// a few hundred. It is also what places the results, so an empty rectangle is
// refused rather than read as "the whole screen": the frame would come back with
// no way to say where its top-left was.
func (a *Adapter) DetectElements(
	ctx context.Context,
	screenBounds image.Rectangle,
	cfg config.HintsVisionConfig,
	splitWord bool,
) ([]*element.Element, error) {
	select {
	case <-ctx.Done():
		return nil, derrors.Wrap(ctx.Err(), derrors.CodeContextCanceled, "operation canceled")
	default:
	}

	region := screenBounds.Canon()
	if region.Empty() {
		return nil, derrors.Newf(
			derrors.CodeActionFailed,
			"vision detection needs a region to read; %v is empty",
			screenBounds,
		)
	}

	if !cfg.DetectText {
		// Text is the whole of what this backend detects, so with
		// hints.vision.detect_text off there is nothing left to run. Capturing
		// the screen to find nothing in it would be a plain waste.
		a.logger.Debug("Vision detection skipped: text detection is disabled")

		return nil, nil
	}

	// The engine is checked before the screen is read, not after. A machine with
	// no language pack installed would otherwise have its focused window captured
	// on every activation only to be told there is nothing to read it with -
	// paying for a frame, and taking one, to answer a question about which
	// Windows features are installed. After the first call this is a cached
	// handle.
	engineErr := winplatform.OCRHealth()
	if engineErr != nil {
		return nil, engineErr
	}

	img, err := a.captureRegion(ctx, region)
	if err != nil {
		return nil, err
	}

	// Word level always, whatever the activation asked for. Per-line rects merge
	// distinct controls - a five-tab strip is one 488px rect, and a hint acts at its
	// element's center, so four tabs get no hint - and mergeWordRuns rebuilds the
	// labels more carefully than the engine's line segmentation does. splitWord
	// decides whether that merge runs, not what the engine is asked for.
	words, stats, err := winplatform.RecognizeText(img, winplatform.OCRParams{
		WordLevel: true,
		TimeoutMS: cfg.RequestTimeoutMS,
	})
	if err != nil {
		// A failed recognition is logged with what it cost and what it was given,
		// because those are the two numbers that say which failure it was: a frame
		// the engine gave up on immediately reads nothing like one that ran to the
		// budget, and the budget is the option a user can turn. Dimensions and
		// durations describe the work, never its content.
		a.logger.Error("Vision detection failed",
			zap.Duration("recognition", stats.Recognition),
			zap.Int("budget_ms", cfg.RequestTimeoutMS),
			zap.Int("frame_width", img.Rect.Dx()),
			zap.Int("frame_height", img.Rect.Dy()),
			zap.Error(err),
		)

		return nil, err
	}

	regions := regionsFromWords(
		toRecognizedWords(words),
		region,
		img.Rect,
		// Not cfg.MinimumConfidence: see the exemption at the top of this file.
		// Every Windows word carries a confidence of 0, so any threshold above 0
		// would discard every word, and passing the option through would make the
		// filter look live when it cannot be.
		0,
	)

	if !splitWord {
		regions = mergeWordRuns(regions)
	}

	merged := MergeRegions(regions, cfg.MergeIOUThreshold)

	classifier := newRegionClassifier(cfg)

	elements, skipped := elementsFromRegions(merged, &classifier)

	// recognizer_language is the BCP-47 tag the engine settled on, which is not
	// always the one asked for: a machine whose display language has no OCR pack
	// gets an engine for whichever installed language does. That substitution is
	// invisible otherwise, and it is the first thing to check when recognition
	// quality is bad on a non-English desktop. A language tag is not screen
	// content.
	a.logger.Debug("Vision detection complete",
		zap.String("recognizer_language", winplatform.OCRLanguage()),
		zap.Duration("recognition", stats.Recognition),
		zap.Int("frame_width", img.Rect.Dx()),
		zap.Int("frame_height", img.Rect.Dy()),
		zap.Int("raw_words", len(words)),
		zap.Int("merged_elements", len(elements)),
		zap.Int("skipped_regions", skipped),
		zap.Bool("word_level", splitWord),
	)

	return elements, nil
}

// toRecognizedWords adapts the platform bridge's word type to the shared one the
// region mapping is written against, so that mapping stays testable on a host
// with no Windows build.
//
// Confidence is carried across rather than dropped, even though it is always 0
// here: the shared type has the field, and a converter that quietly substituted
// something else would hide the exemption this file documents.
func toRecognizedWords(words []winplatform.OCRWord) []recognizedWord {
	converted := make([]recognizedWord, 0, len(words))

	for _, word := range words {
		converted = append(converted, recognizedWord{
			Text:       word.Text,
			Bounds:     word.Bounds,
			Confidence: word.Confidence,
		})
	}

	return converted
}

// CaptureScreen returns the pixels currently on the active screen, which on
// Windows means the whole virtual desktop - the union of every monitor, not the
// primary one.
func (a *Adapter) CaptureScreen(ctx context.Context) (*image.RGBA, error) {
	return a.captureRegion(ctx, image.Rectangle{})
}

// Health reports whether the vision strategy can run on this machine.
//
// One check, and it is the recognition half. Unlike Linux, there is no capture
// precondition worth verifying: GDI ships with every Windows session that has a
// desktop, and a GetDC that succeeds says nothing a real capture would not - so
// the only honest capture probe is a capture, and a health check that read the
// user's screen to find out whether it could read the screen would be the wrong
// trade.
//
// The recognition half is where the per-machine failure lives, and it is one a
// user can fix. Windows ships Windows.Media.Ocr with the OS, but it recognizes
// nothing without language data, and that arrives with an installed language pack
// rather than with the API. The error names the Settings page.
//
// Asking also warms the engine: the activation factories and the recognizer are
// cached for the process, so a later detection finds them ready. This is the
// method HintService.Health calls, which is how a Windows user learns about
// missing language data at all - the notification path that would otherwise carry
// it goes through ShowNotification, which is stubbed on Windows, so `neru doctor`
// is the surface.
func (a *Adapter) Health(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return derrors.Wrap(ctx.Err(), derrors.CodeContextCanceled, "operation canceled")
	default:
	}

	return winplatform.OCRHealth()
}

// captureRegion captures region - virtual-desktop physical pixels, top-left
// origin, Y down - through GDI. An empty rectangle means the whole virtual
// desktop, which is what CaptureScreen asks for.
//
// The region exists so a caller constrained to the focused window pays for the
// focused window: reading a 4K display back to examine one window is the
// difference between usable and not.
//
// Nothing derived from the returned image is logged. The debug line below carries
// a duration and the frame's dimensions, which describe the capture rather than
// its contents.
func (a *Adapter) captureRegion(ctx context.Context, region image.Rectangle) (*image.RGBA, error) {
	select {
	case <-ctx.Done():
		return nil, derrors.Wrap(ctx.Err(), derrors.CodeContextCanceled, "operation canceled")
	default:
	}

	started := time.Now()

	img, err := winplatform.CaptureScreenRegion(ctx, region)
	if err != nil {
		return nil, err
	}

	a.logger.Debug("Captured screen region",
		zap.Duration("duration", time.Since(started)),
		zap.Int("width", img.Rect.Dx()),
		zap.Int("height", img.Rect.Dy()),
	)

	return img, nil
}
