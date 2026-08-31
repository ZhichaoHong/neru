//go:build windows

package windows

import (
	"errors"
	"strings"
	"testing"
)

// What is testable here without an interactive desktop: which affinity constant a
// request maps to, and that a refusal is reported rather than swallowed. The
// affinity actually holding against a capture is a desktop-session question and
// lives with the integration tests.

func TestApplyDisplayAffinityReportsARefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		exclude      bool
		wantAffinity string
	}{
		{
			name:         "excluding asks for WDA_EXCLUDEFROMCAPTURE",
			exclude:      true,
			wantAffinity: "0x11",
		},
		{
			// WDA_MONITOR (0x1) is never asked for: it blanks the window in a
			// capture instead of omitting it.
			name:         "not excluding asks for WDA_NONE",
			exclude:      false,
			wantAffinity: "0x0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// A zero handle is not a window, so the call fails whatever the build
			// is. That is the point: a bool return nobody reads is how a user who
			// asked to be hidden from a screen share silently is not.
			err := applyDisplayAffinity(0, test.exclude)
			if err == nil {
				t.Fatal("applyDisplayAffinity(0, ...) = nil, want an error for an invalid window")
			}

			if !strings.Contains(err.Error(), test.wantAffinity) {
				t.Errorf(
					"applyDisplayAffinity(0, %v) error = %q, want it to name affinity %s",
					test.exclude,
					err,
					test.wantAffinity,
				)
			}
		})
	}
}

func TestOverlayWindowRemembersCaptureExclusionWithoutAnHWND(t *testing.T) {
	t.Parallel()

	// No HWND yet, which is the state every recreation path passes through.
	// createHWNDLocked replays this flag, so remembering it is the whole contract.
	overlay := &OverlayWindow{}

	if overlay.ExcludedFromCapture() {
		t.Error("a fresh overlay window is excluded from capture, want capturable")
	}

	err := overlay.SetExcludedFromCapture(true)
	if err != nil {
		t.Fatalf("SetExcludedFromCapture(true) with no HWND = %v, want nil", err)
	}

	if !overlay.ExcludedFromCapture() {
		t.Error("SetExcludedFromCapture(true) was not remembered for the next HWND")
	}

	err = overlay.SetExcludedFromCapture(false)
	if err != nil {
		t.Fatalf("SetExcludedFromCapture(false) with no HWND = %v, want nil", err)
	}

	if overlay.ExcludedFromCapture() {
		t.Error("SetExcludedFromCapture(false) was not remembered for the next HWND")
	}
}

func TestNilOverlayWindowRefusesCaptureExclusion(t *testing.T) {
	t.Parallel()

	var overlay *OverlayWindow

	err := overlay.SetExcludedFromCapture(true)
	if !errors.Is(err, errOverlayNil) {
		t.Errorf("SetExcludedFromCapture on a nil overlay = %v, want errOverlayNil", err)
	}

	if overlay.ExcludedFromCapture() {
		t.Error("a nil overlay reports itself excluded from capture")
	}
}
