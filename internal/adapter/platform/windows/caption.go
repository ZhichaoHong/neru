//go:build windows

package windows

import (
	"image"
	"math"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Caption buttons for windows that draw their own frame.
//
// A window with the system frame publishes minimize, maximize and close through
// UIA, and hints find them there. A window that draws its own does not have to:
// Chromium-based frames (Teams, Explorer's newer views) paint the glyphs into
// their client area and expose nothing behind them, so hints have three buttons
// to show and no elements to attach them to.
//
// What every such window does still answer is WM_NCHITTEST, because that is how
// Windows itself decides what the mouse is over: the answer carries both the
// button's identity and, since the message takes a screen point, its geometry.
// Sampling the caption for those answers reconstructs what UIA left out.
//
// Two alternatives came first and neither survived a look at real windows:
//
//   - WM_GETTITLEBARINFOEX returns the three rects directly, but a Chromium
//     frame fills them in its own DIP space rather than screen pixels. On a 200%
//     monitor the rects came back at half scale plus an offset, and there is no
//     way to tell a wrong rect from a right one without already knowing the
//     answer.
//   - The MSAA titlebar object (OBJID_TITLEBAR) names its children correctly and
//     reports accLocation as 0,0 0x0 for all of them. Names without geometry
//     cannot place a hint.

// CaptionButtonKind is which of a window's caption buttons an element is.
type CaptionButtonKind int

// The three buttons a standard caption carries, in the order the frame lays
// them out left to right.
const (
	CaptionMinimize CaptionButtonKind = iota
	CaptionMaximize
	CaptionClose
)

// String is the label a hint shows for the button, matching the names the system
// frame publishes through UIA so the two paths read alike.
func (k CaptionButtonKind) String() string {
	switch k {
	case CaptionMinimize:
		return "Minimize"
	case CaptionMaximize:
		return "Maximize"
	case CaptionClose:
		return "Close"
	default:
		return "Caption"
	}
}

// CaptionButton is one caption button, with bounds in physical screen pixels -
// the same space windowFrameBounds answers in, and the space hints place badges
// in.
type CaptionButton struct {
	Kind   CaptionButtonKind
	Bounds image.Rectangle
}

const (
	// lparamHighWordShift is where LPARAM keeps its second 16-bit coordinate.
	lparamHighWordShift = 16

	// smtoAbortIfHung returns immediately when the target thread is already
	// known to be hung, instead of waiting out the timeout. Without it a single
	// wedged application would cost the whole budget below.
	smtoAbortIfHung = 0x0002

	// captionProbeTimeoutMS bounds one message. WM_NCHITTEST is handled inline
	// by DefWindowProc or by a few lines of application code, so a window that
	// has not answered in this long is not going to.
	captionProbeTimeoutMS = 50

	// captionProbeBudget bounds the whole search. A probe costs about 16
	// microseconds against a responsive window, so this is far more than any
	// real caption needs; it exists for the window that answers slowly rather
	// than not at all, where the per-message timeout alone would let a few
	// hundred probes stall a keystroke.
	captionProbeBudget = 60 * time.Millisecond

	// captionMaxProbes bounds the search by count as well, since a fast window
	// can burn a lot of probes inside the time budget without getting closer to
	// an answer.
	captionMaxProbes = 2000

	// captionMaxCandidates bounds how many descendant windows are asked. A frame
	// that draws its own caption uses one child for it, or none; a window with
	// dozens of children overlapping its caption band is not one of these, and
	// asking them all would cost more than the feature is worth.
	captionMaxCandidates = 32
)

var (
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
	procEnumChildWindows    = user32.NewProc("EnumChildWindows")
	procIsIconic            = user32.NewProc("IsIconic")
)

// minimized reports whether a window is minimized, in which case its caption is
// not on screen and nothing there is worth a hint.
func minimized(hwnd windows.HWND) bool {
	ret, _, _ := procIsIconic.Call(uintptr(hwnd))

	return ret != 0
}

// CaptionButtons reports the caption buttons of a top-level window, or nothing
// when the window does not answer for any - which includes every window whose
// caption the system draws, since those never see the message.
//
// Callers are expected to treat the result as a supplement to what the
// accessibility tree already found, not a replacement: a window may both publish
// its buttons and answer here.
func CaptionButtons(hwnd uintptr) []CaptionButton {
	handle := windows.HWND(hwnd)
	if hwnd == 0 || !windows.IsWindow(handle) || minimized(handle) {
		return nil
	}

	frame, err := windowFrameBounds(handle)
	if err != nil || frame.Empty() || !packableRect(frame) {
		return nil
	}

	// The scale of the monitor the caption is on, not the one the window's
	// center is on: a window straddling two monitors of different scaling draws
	// its caption at the top monitor's scale.
	scale := ScreenScaleAt(image.Pt(frame.Min.X+frame.Dx()/2, frame.Min.Y))
	band := captionBand(frame, scale)

	deadline := time.Now().Add(captionProbeBudget)
	probes := 0

	for _, candidate := range captionCandidates(handle, band) {
		area := candidateArea(candidate, handle, band)
		if area.Empty() {
			continue
		}

		probe := func(point image.Point) (hitCode, bool) {
			if probes >= captionMaxProbes || time.Now().After(deadline) {
				return 0, false
			}

			probes++

			return hitTestWindow(candidate, point)
		}

		if buttons := scanCaptionButtons(area, scale, probe); len(buttons) > 0 {
			return buttons
		}

		if probes >= captionMaxProbes || time.Now().After(deadline) {
			return nil
		}
	}

	return nil
}

// captionBand is the strip of the frame a caption button could be in. It bounds
// both which descendants are worth asking and how deep into each one the search
// looks.
func captionBand(frame image.Rectangle, scale float64) image.Rectangle {
	depth := min(scaleLogical(captionScanDepth, scale), frame.Dy())

	return image.Rect(frame.Min.X, frame.Min.Y, frame.Max.X, frame.Min.Y+depth)
}

// candidateArea is where in a candidate window to search: the caption band for
// the top-level window, and a descendant's own extent clipped to that band.
//
// Clipping a descendant matters for the same reason the descendant walk does. A
// caption child narrower than the frame - a Chromium frame that leaves its tab
// strip to another child - would otherwise have the search start at the frame's
// right edge and spend its whole span outside the only window that can answer.
func candidateArea(hwnd windows.HWND, topLevel windows.HWND, band image.Rectangle) image.Rectangle {
	if hwnd == 0 {
		return image.Rectangle{}
	}

	// The top-level window is asked over the whole band, which is already derived
	// from its frame.
	area := band

	if hwnd != topLevel {
		bounds, ok := windowBounds(hwnd)
		if !ok {
			return image.Rectangle{}
		}

		area = band.Intersect(bounds)
	}

	if !packableRect(area) {
		return image.Rectangle{}
	}

	return area
}

// captionCandidates are the windows worth asking, the top-level one first.
//
// Where the answer comes from is not a property of the window's class or style,
// so there is nothing to filter on ahead of time. Notepad and Edge answer on the
// top-level window; Teams and Explorer answer only on the child that draws the
// caption, and refuse on their top-level. Both orders have to work.
func captionCandidates(hwnd windows.HWND, band image.Rectangle) []windows.HWND {
	candidates := make([]windows.HWND, 0, captionMaxCandidates+1)
	candidates = append(candidates, hwnd)

	for _, child := range childWindows(hwnd) {
		if len(candidates) > captionMaxCandidates {
			break
		}

		if !windows.IsWindowVisible(child) {
			continue
		}

		bounds, ok := windowBounds(child)
		if !ok || !bounds.Overlaps(band) {
			continue
		}

		candidates = append(candidates, child)
	}

	return candidates
}

// windowBounds is a window's outer rect. Unlike windowFrameBounds it does not
// consult DWM, which has nothing to say about a child window.
func windowBounds(hwnd windows.HWND) (image.Rectangle, bool) {
	var rect windows.Rect

	ret, _, err := procGetWindowRect.Call(
		uintptr(hwnd),
		uintptr(unsafe.Pointer(&rect)),
	)
	if win32Bool(ret, err) != nil {
		return image.Rectangle{}, false
	}

	return rectToImage(rect), true
}

// hitTestWindow asks a window what sits under a screen point. The bool is false
// when the window did not answer, which the search treats as fatal rather than as
// "nothing there".
func hitTestWindow(hwnd windows.HWND, point image.Point) (hitCode, bool) {
	packed, ok := packScreenPoint(point)
	if !ok {
		return 0, false
	}

	var result uintptr

	ret, _, _ := procSendMessageTimeoutW.Call(
		uintptr(hwnd),
		wmNCHitTest,
		0,
		packed,
		smtoAbortIfHung,
		captionProbeTimeoutMS,
		uintptr(unsafe.Pointer(&result)),
	)
	if ret == 0 {
		return 0, false
	}

	return hitCode(int32(result)), true
}

// packScreenPoint packs a screen point into WM_NCHITTEST's LPARAM, which carries
// the two coordinates as signed 16-bit words. A desktop spanning more than 32767
// pixels, or one whose left monitor starts below -32768, cannot be addressed this
// way at all, so such a point is refused rather than wrapped into a point
// somewhere else entirely.
func packScreenPoint(point image.Point) (uintptr, bool) {
	if !packableCoordinate(point.X) || !packableCoordinate(point.Y) {
		return 0, false
	}

	low := uint32(uint16(int16(point.X)))
	high := uint32(uint16(int16(point.Y))) << lparamHighWordShift

	return uintptr(low | high), true
}

func packableCoordinate(value int) bool {
	return value >= math.MinInt16 && value <= math.MaxInt16
}

func packableRect(rect image.Rectangle) bool {
	return packableCoordinate(rect.Min.X) && packableCoordinate(rect.Min.Y) &&
		packableCoordinate(rect.Max.X) && packableCoordinate(rect.Max.Y)
}

var (
	// childEnumMu, childEnumTarget and childEnumProcPtr follow the same shape as
	// the monitor enumeration above, for the same two reasons: Go never frees a
	// callback slot, so the callback is allocated once for the process and
	// cannot capture per-call state, and routing that state through the lParam
	// instead would mean a uintptr-to-pointer conversion.
	//
	// EnumChildWindows calls back synchronously on the calling goroutine, so the
	// lock held across the call is what makes one shared collector safe.
	childEnumMu     sync.Mutex
	childEnumTarget *[]windows.HWND

	childEnumProcPtr = sync.OnceValue(func() uintptr {
		return syscall.NewCallback(collectChildWindow)
	})
)

// collectChildWindow is the EnumChildWindowsProc. Returning 1 continues the
// enumeration.
func collectChildWindow(hwnd uintptr, _ uintptr) uintptr {
	if childEnumTarget == nil {
		return 1
	}

	*childEnumTarget = append(*childEnumTarget, windows.HWND(hwnd))

	return 1
}

// childWindows returns every descendant of a window, at any depth - which is
// what EnumChildWindows walks, and what this needs: a Chromium frame nests its
// caption child inside a host child.
func childWindows(hwnd windows.HWND) []windows.HWND {
	children := make([]windows.HWND, 0, captionMaxCandidates)

	childEnumMu.Lock()

	defer func() {
		childEnumTarget = nil

		childEnumMu.Unlock()
	}()

	childEnumTarget = &children

	// The return value is not checked: EnumChildWindows returns zero both when
	// the window has no children and when the callback stopped the walk, and
	// neither is an error. Whatever was collected is the answer.
	//nolint:dogsled // a syscall's three results, none of them an answer here
	_, _, _ = procEnumChildWindows.Call(uintptr(hwnd), childEnumProcPtr(), 0)

	return children
}
