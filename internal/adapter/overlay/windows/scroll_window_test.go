//go:build windows

package windows

import (
	"testing"

	"github.com/y3owk1n/neru/internal/adapter/overlay/manager"
)

// Whether scroll mode leaves the shared full-screen window up.
//
// It must not. Scroll draws nothing on that surface here - every indicator is a
// window of its own - and an empty full-screen overlay is not harmless: wheel
// routing picks its target the way WindowFromPoint does, ignoring the
// WM_NCHITTEST answer that makes the overlay click-through, so the overlay
// became the wheel target and the event was dropped. Vertical scroll did nothing
// in scroll mode while horizontal kept working, because horizontal goes through
// UI Automation rather than the wheel.
//
// suppressDraw stands in for "Hide was called": it is the flag Hide sets and
// Show clears, and reaching a real HWND from a unit test means creating a
// window, which is why nothing else in this package draws.
//
// Getting the window back for the next mode is the adapter's Show call, which
// runs before the switch; TestAdapterShowFrame_BringsTheWindowUpBeforeSwitchingMode
// pins that order.
func TestScrollModeTakesTheSharedWindowDown(t *testing.T) {
	t.Parallel()

	m := &Manager{win: &winOverlay{}}

	m.SwitchTo(manager.ModeHints)

	if m.win.suppressDraw {
		t.Fatal("hints mode hid the shared window it draws its labels on")
	}

	m.SwitchTo(manager.ModeScroll)

	if !m.win.suppressDraw {
		t.Error("scroll mode left the empty shared full-screen window up, " +
			"which swallows wheel events and breaks vertical scrolling")
	}
}
