//go:build windows

package windows

import (
	"image"
	"runtime"
	"unsafe"

	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
)

// Horizontal scrolling through the UI Automation ScrollPattern.
//
// MOUSEEVENTF_HWHEEL arrives at the target as WM_MOUSEHWHEEL, which a window has
// to handle. File Explorer's item view does not: a WM_MOUSEHWHEEL posted
// directly to it moves it no further than an injected one does, and it does not
// honor the shift-plus-vertical-wheel convention either. So scroll_left and
// scroll_right reached a control that scrolls fine vertically and refused to
// move sideways.
//
// The same control advertises a ScrollPattern whose Scroll method does move it.
// That is the provider's own declared way to scroll, so it is the route taken
// whenever one is advertised; the wheel remains the route for everything else.
//
// The vertical axis stays on the wheel unconditionally. Every target handles
// WM_MOUSEWHEEL, and the wheel carries the caller's pixel delta rather than the
// line-and-page granularity a ScrollPattern quantizes to.

// iidIUIAutomationScrollPattern identifies the interface GetCurrentPattern's
// IUnknown has to be narrowed to before Scroll can be called on it.
var iidIUIAutomationScrollPattern = guidMust("{88F4D42A-E881-459D-A77C-73BBBB7E02DC}")

const (
	// UIA_ScrollPatternId.
	uiaScrollPatternID = 10004

	// ScrollAmount. A small decrement moves the viewport one line toward the
	// start of the content, which on the horizontal axis is left.
	scrollAmountSmallDecrement = 1
	scrollAmountNoAmount       = 2
	scrollAmountSmallIncrement = 4
)

// Vtable slot indices, continuing the set in automation.go. IUnknown occupies
// 0,1,2, and these match the public UIAutomationClient IDL.
const (
	vtQueryInterface = 0

	// IUIAutomation.
	vtElementFromPoint     = 7
	vtGetControlViewWalker = 14

	// IUIAutomationElement.
	vtGetCurrentPattern = 16

	// IUIAutomationTreeWalker.
	vtWalkerGetParent = 3

	// IUIAutomationScrollPattern.
	vtScrollScroll                    = 3
	vtScrollCurrentHorizontallyScroll = 9
)

const (
	// maxScrollAncestorWalk bounds the climb from the element under the cursor
	// to the one that scrolls. Explorer's item view is one step up; a text node
	// in a Chromium page is about seven. The cap is what stops a provider with a
	// cyclic or absurdly deep tree from turning one keypress into an unbounded
	// walk.
	maxScrollAncestorWalk = 24

	// maxScrollSteps bounds the repeat loop. Every step is a cross-process COM
	// call, so an unbounded one would stall the action on an unreasonable
	// scroll_step: the shipped default of 50 pixels is one step, but nothing
	// stops a configuration asking for 100000.
	maxScrollSteps = 200
)

// A POINT is passed to ElementFromPoint by value. Two LONGs fit one 64-bit
// register, which is what packPoint relies on; on a 32-bit target they would be
// two separate stack arguments and the packing would be wrong.
var _ [8 - unsafe.Sizeof(uintptr(0))]byte

// scrollHorizontallyViaPattern scrolls whatever advertises a horizontal
// ScrollPattern under the given point by deltaX pixels, following the shared
// convention that a positive delta scrolls left.
//
// It reports whether the scroll was handled. False means nothing under the
// cursor advertises horizontal scrolling, or the provider refused the first
// step, and the caller still owes the target a wheel event.
func scrollHorizontallyViaPattern(point image.Point, deltaX int) bool {
	if deltaX == 0 {
		return false
	}

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	hresult, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)

	// Same bargain as enumerateClickableElements: balance initialization only
	// when this call owns it, and leave an apartment somebody else opened alone.
	if uint32(hresult) == hresultSOK || uint32(hresult) == hresultSFalse {
		defer func() { discardCall(procCoUninitialize.Call()) }()
	}

	automation := createAutomation()
	if automation == nil {
		return false
	}
	defer comCall(automation, vtRelease)

	pattern := horizontalScrollPatternAt(automation, point)
	if pattern == nil {
		return false
	}
	defer comCall(pattern, vtRelease)

	amount, steps := horizontalScrollSteps(deltaX)

	return repeatScroll(pattern, amount, steps)
}

// horizontalScrollSteps converts a caller's pixel delta into the ScrollAmount to
// repeat and how many times to repeat it.
//
// A small increment is one line, which is what a wheel notch approximates, so
// the pixel delta divides into increments the same way the wheel path divides it
// into notches. Anything under one notch still travels one increment: a binding
// that asks to scroll must move something.
func horizontalScrollSteps(deltaX int) (int, int) {
	amount := scrollAmountSmallIncrement
	pixels := -deltaX

	if deltaX > 0 {
		amount = scrollAmountSmallDecrement
		pixels = deltaX
	}

	steps := pixels / winplatform.ScrollPixelsPerNotch
	if steps == 0 {
		steps = 1
	}

	return amount, min(steps, maxScrollSteps)
}

// repeatScroll issues the same horizontal scroll increment steps times.
//
// The count is deliberately not verified against HorizontalScrollPercent between
// steps, which looks like the obvious way to stop once the viewport runs out of
// content. A provider may serve the scroll asynchronously, and Chromium does:
// the percent read straight after a Scroll still reports where the viewport was,
// so treating that as the edge truncates a ten-increment delta to one. Running
// out of content is left to the provider, which either refuses the step or
// no-ops it.
//
// Chromium also coalesces increments issued back to back - ten asked for arrive
// as six - so a delta much larger than one increment travels less far there than
// the wheel would have carried it. The shipped scroll_step is one increment,
// where there is nothing to coalesce.
func repeatScroll(pattern unsafe.Pointer, amount int, steps int) bool {
	for step := range steps {
		hresult := comCall(
			pattern,
			vtScrollScroll,
			uintptr(amount),
			uintptr(scrollAmountNoAmount),
		)
		if failed(hresult) {
			// A refusal on the first step means the pattern was advertised but
			// cannot serve this direction, so the caller should still try the
			// wheel. A refusal later means scrolling already happened, and the
			// most likely reason for it is that the viewport ran out of content.
			return step > 0
		}
	}

	return true
}

// horizontalScrollPatternAt returns the ScrollPattern of the nearest element at
// or above the given point that reports itself horizontally scrollable, or nil
// when there is none. The caller owns the returned interface.
//
// The walk is upward because the element under the cursor is a leaf - a list
// item, a run of text - while the thing that scrolls is one of its ancestors.
// It stops at the first horizontally scrollable one, which is the innermost, and
// therefore the one a wheel event would have reached.
func horizontalScrollPatternAt(automation unsafe.Pointer, point image.Point) unsafe.Pointer {
	var walker unsafe.Pointer

	hresult := comCall(automation, vtGetControlViewWalker, uintptr(unsafe.Pointer(&walker)))
	if failed(hresult) || walker == nil {
		return nil
	}
	defer comCall(walker, vtRelease)

	var current unsafe.Pointer

	hresult = comCall(
		automation,
		vtElementFromPoint,
		packPoint(point),
		uintptr(unsafe.Pointer(&current)),
	)
	if failed(hresult) || current == nil {
		return nil
	}

	for range maxScrollAncestorWalk {
		pattern := horizontalScrollPattern(current)
		if pattern != nil {
			comCall(current, vtRelease)

			return pattern
		}

		var parent unsafe.Pointer

		if failed(
			comCall(walker, vtWalkerGetParent, uintptr(current), uintptr(unsafe.Pointer(&parent))),
		) {
			parent = nil
		}

		comCall(current, vtRelease)

		current = parent
		if current == nil {
			return nil
		}
	}

	comCall(current, vtRelease)

	return nil
}

// horizontalScrollPattern returns the element's ScrollPattern when it has one
// and reports horizontal scrolling, and nil otherwise. The caller owns the
// returned interface.
//
// The scrollable check belongs here rather than at the call site because a
// pattern that cannot scroll horizontally must not stop the upward walk: a
// Chromium document advertises a ScrollPattern on every page and reports
// HorizontallyScrollable false on the ones with nothing to scroll to.
func horizontalScrollPattern(element unsafe.Pointer) unsafe.Pointer {
	var unknown unsafe.Pointer

	// A supported-pattern query succeeds with a null object when the element
	// does not implement the pattern, so the null is the answer, not a failure.
	hresult := comCall(
		element,
		vtGetCurrentPattern,
		uiaScrollPatternID,
		uintptr(unsafe.Pointer(&unknown)),
	)
	if failed(hresult) || unknown == nil {
		return nil
	}

	var pattern unsafe.Pointer

	hresult = comCall(
		unknown,
		vtQueryInterface,
		uintptr(unsafe.Pointer(&iidIUIAutomationScrollPattern)),
		uintptr(unsafe.Pointer(&pattern)),
	)

	comCall(unknown, vtRelease)

	if failed(hresult) || pattern == nil {
		return nil
	}

	var scrollable int32

	hresult = comCall(
		pattern,
		vtScrollCurrentHorizontallyScroll,
		uintptr(unsafe.Pointer(&scrollable)),
	)
	if failed(hresult) || scrollable == 0 {
		comCall(pattern, vtRelease)

		return nil
	}

	return pattern
}

// packPoint lays a Win32 POINT out as the single 64-bit argument it is passed as.
func packPoint(point image.Point) uintptr {
	return uintptr(uint32(int32(point.X))) | uintptr(uint32(int32(point.Y)))<<32
}
