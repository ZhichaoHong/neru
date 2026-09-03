//go:build windows

package windows

import (
	"image"
	"testing"

	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
)

// TestHorizontalScrollSteps pins the pixel-to-increment mapping, which is the
// only part of the ScrollPattern path that can be exercised without a live
// provider under the cursor.
func TestHorizontalScrollSteps(t *testing.T) {
	t.Parallel()

	const notch = winplatform.ScrollPixelsPerNotch

	tests := []struct {
		name       string
		deltaX     int
		wantAmount int
		wantSteps  int
	}{
		{
			// The shared convention is macOS's: a positive delta scrolls left,
			// and left is toward the start of the content, which UIA calls a
			// decrement.
			name:       "a positive delta decrements",
			deltaX:     notch,
			wantAmount: scrollAmountSmallDecrement,
			wantSteps:  1,
		},
		{
			name:       "a negative delta increments",
			deltaX:     -notch,
			wantAmount: scrollAmountSmallIncrement,
			wantSteps:  1,
		},
		{
			// scroll_step ships at 50 pixels, which is one notch and change. The
			// remainder is dropped rather than rounded up, the same way the wheel
			// path's integer division drops it.
			name:       "the shipped scroll_step is one increment",
			deltaX:     -50,
			wantAmount: scrollAmountSmallIncrement,
			wantSteps:  1,
		},
		{
			name:       "a delta under one notch still moves",
			deltaX:     -1,
			wantAmount: scrollAmountSmallIncrement,
			wantSteps:  1,
		},
		{
			name:       "a multi-notch delta travels that many increments",
			deltaX:     notch * 7,
			wantAmount: scrollAmountSmallDecrement,
			wantSteps:  7,
		},
		{
			// A configured scroll_step this large would otherwise be 33333
			// cross-process COM calls for one keypress.
			name:       "an unreasonable delta is capped",
			deltaX:     -1000000,
			wantAmount: scrollAmountSmallIncrement,
			wantSteps:  maxScrollSteps,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			amount, steps := horizontalScrollSteps(test.deltaX)
			if amount != test.wantAmount || steps != test.wantSteps {
				t.Errorf(
					"horizontalScrollSteps(%d) = (%d, %d), want (%d, %d)",
					test.deltaX, amount, steps, test.wantAmount, test.wantSteps,
				)
			}
		})
	}
}

// TestPackPoint pins the layout ElementFromPoint reads its POINT out of. A
// wrong-endian pack lands the hit test on a different element, or off-screen,
// which looks like "horizontal scroll does nothing" rather than like a bug here.
func TestPackPoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		x    int
		y    int
	}{
		{"origin", 0, 0},
		{"a point on the primary monitor", 900, 700},
		{"a point on a monitor left of the primary one", -1920, 540},
		{"a point above the primary monitor", 400, -1080},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			packed := packPoint(image.Pt(test.x, test.y))

			gotX := int(int32(uint32(packed)))
			gotY := int(int32(uint32(packed >> 32)))

			if gotX != test.x || gotY != test.y {
				t.Errorf("packPoint(%d, %d) unpacks as (%d, %d)", test.x, test.y, gotX, gotY)
			}
		})
	}
}
