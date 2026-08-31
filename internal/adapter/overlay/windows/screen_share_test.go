//go:build windows

package windows

import "testing"

// What this backend decides about hide_in_screen_share on its own: that the
// choice is remembered, so a window created or rebuilt after the toggle inherits
// it rather than appearing in a capture until the next toggle. Whether the
// affinity holds against a real capture is a desktop-session question and is
// verified by hand; reaching a real HWND from here means creating a layered
// window, the same reason there is no draw test in this package.

func TestSetSharingTypeRemembersTheChoiceForWindowsCreatedLater(t *testing.T) {
	t.Parallel()

	// No windows exist yet, which is the state a manager is in between modes and
	// the state that made the affinity easy to lose: the toggle arrives on its own
	// goroutine from AppState, long before the badge windows are created lazily.
	m := &Manager{}

	m.SetSharingType(true)

	if !m.hideInScreenShare {
		t.Error("SetSharingType(true) was not remembered, so a later window would be capturable")
	}

	m.SetSharingType(false)

	if m.hideInScreenShare {
		t.Error("SetSharingType(false) was not remembered, so a later window would stay excluded")
	}
}

func TestWinOverlayRemembersCaptureExclusionWithoutAWindow(t *testing.T) {
	t.Parallel()

	// recreateWindow builds a new platform window rather than reviving the old
	// one, so the platform-level flag cannot survive that path; this one does.
	overlay := &winOverlay{}

	err := overlay.SetExcludedFromCapture(true)
	if err != nil {
		t.Fatalf("SetExcludedFromCapture(true) with no window = %v, want nil", err)
	}

	if !overlay.excludeFromCapture {
		t.Error("SetExcludedFromCapture(true) was not remembered across a window recreation")
	}

	err = overlay.SetExcludedFromCapture(false)
	if err != nil {
		t.Fatalf("SetExcludedFromCapture(false) with no window = %v, want nil", err)
	}

	if overlay.excludeFromCapture {
		t.Error("SetExcludedFromCapture(false) was not remembered across a window recreation")
	}
}

func TestNilWinOverlayAcceptsCaptureExclusion(t *testing.T) {
	t.Parallel()

	// newWinOverlay returns nil when the window cannot be created, and
	// SetSharingType calls straight through without a guard, so a nil surface has
	// to be answerable rather than fatal.
	var overlay *winOverlay

	err := overlay.SetExcludedFromCapture(true)
	if err != nil {
		t.Errorf("SetExcludedFromCapture on a nil surface = %v, want nil", err)
	}
}
