//go:build windows && (amd64 || arm64)

package windows

import (
	"slices"
	"testing"
	"unsafe"
)

func TestSendInputStructLayout(t *testing.T) {
	t.Parallel()

	if got := unsafe.Sizeof(input{}); got != 40 {
		t.Fatalf("sizeof(input) = %d, want 40", got)
	}

	if got := unsafe.Sizeof(mouseInput{}); got != 32 {
		t.Fatalf("sizeof(mouseInput) = %d, want 32", got)
	}

	if got := unsafe.Offsetof(input{}.mi); got != 8 {
		t.Fatalf("offsetof(input.mi) = %d, want 8", got)
	}

	// The keyboard arm of the union carries its own padding to reach the same
	// 40 bytes cbSize demands, so it needs the same offset check: a wVk landing
	// anywhere but byte 8 is a SendInput that silently posts the wrong key.
	if got := unsafe.Sizeof(keyInput{}); got != 40 {
		t.Fatalf("sizeof(keyInput) = %d, want 40", got)
	}

	if got := unsafe.Sizeof(keyboardInput{}); got != 24 {
		t.Fatalf("sizeof(keyboardInput) = %d, want 24", got)
	}

	if got := unsafe.Offsetof(keyInput{}.ki); got != 8 {
		t.Fatalf("offsetof(keyInput.ki) = %d, want 8", got)
	}
}

// notches converts a signed wheel count into the two's-complement mouseData
// SendInput reads, the same way wheelEvents does for a whole notch's worth of
// pixels.
func notches(count int) uint32 {
	return uint32(int32(count) * wheelDelta)
}

// pixels converts a signed pixel delta into mouseData the same way
// wheelEvents does: ScrollPixelsPerNotch pixels are one notch.
func pixels(count int) uint32 {
	return uint32(int32(count) * wheelUnitsPerPixel)
}

// TestWheelEvents_NegatesHorizontalDelta pins the sign convention across the
// SendInput seam: Neru's positive deltaX means left everywhere, while
// MOUSEEVENTF_HWHEEL reads positive as right, so scroll_left must arrive as
// a negative HWHEEL notch and scroll_right as a positive one.
func TestWheelEvents_NegatesHorizontalDelta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		deltaX int
		deltaY int
		want   []wheelEvent
	}{
		{name: "no movement sends nothing"},
		{
			name:   "a notch's worth of pixels up is one positive wheel notch",
			deltaY: ScrollPixelsPerNotch,
			want:   []wheelEvent{{flags: mouseeventfWheel, data: notches(1)}},
		},
		{
			name:   "a pixel is a fraction of a notch, not a notch",
			deltaY: 1,
			want:   []wheelEvent{{flags: mouseeventfWheel, data: pixels(1)}},
		},
		{
			name:   "scroll left is a negative hwheel notch",
			deltaX: ScrollPixelsPerNotch,
			want:   []wheelEvent{{flags: mouseeventfHWheel, data: notches(-1)}},
		},
		{
			name:   "scroll right is a positive hwheel notch",
			deltaX: -2 * ScrollPixelsPerNotch,
			want:   []wheelEvent{{flags: mouseeventfHWheel, data: notches(2)}},
		},
		{
			name:   "both axes send vertical first",
			deltaX: -ScrollPixelsPerNotch,
			deltaY: -ScrollPixelsPerNotch,
			want: []wheelEvent{
				{flags: mouseeventfWheel, data: notches(-1)},
				{flags: mouseeventfHWheel, data: notches(1)},
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := wheelEvents(testCase.deltaX, testCase.deltaY)
			if !slices.Equal(got, testCase.want) {
				t.Fatalf(
					"wheelEvents(%d, %d) = %+v, want %+v",
					testCase.deltaX,
					testCase.deltaY,
					got,
					testCase.want,
				)
			}
		})
	}
}

// TestCapWheelPixels_KeepsTheDeltaInsideOneEvent is the go_bottom regression.
// The units a caller's delta converts to reach the target as the signed short
// in WM_MOUSEWHEEL's high word, so an uncapped scroll_step_full of 1000000
// pixels arrived as its own low 16 bits: 4000000 units read as 2304, a scroll
// of 19 notches upward where a whole document was asked for.
func TestCapWheelPixels_KeepsTheDeltaInsideOneEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		delta int
		want  int
	}{
		{name: "a scroll_step is untouched", delta: 50, want: 50},
		{
			name:  "the largest expressible delta is untouched",
			delta: maxWheelPixels,
			want:  maxWheelPixels,
		},
		{name: "scroll_step_full is capped", delta: 1000000, want: maxWheelPixels},
		{name: "a capped delta keeps its direction", delta: -1000000, want: -maxWheelPixels},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := capWheelPixels(testCase.delta)
			if got != testCase.want {
				t.Fatalf("capWheelPixels(%d) = %d, want %d", testCase.delta, got, testCase.want)
			}

			units := int32(got) * wheelUnitsPerPixel
			if int32(int16(units)) != units {
				t.Fatalf(
					"%d pixels are %d units, which does not survive a signed short",
					got,
					units,
				)
			}
		})
	}
}
