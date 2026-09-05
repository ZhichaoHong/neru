//go:build linux

package linux

import "testing"

// TestUinputScrollRemainder_WholeSendLeavesNothing is the Hyprland regression:
// a default 50 px step is one 30 px notch, and the 20 px of rounding left over
// must not go out again on the virtual pointer as a second notch.
func TestUinputScrollRemainder_WholeSendLeavesNothing(t *testing.T) {
	const scale = 30

	tests := []struct {
		name      string
		delta     int
		total     int
		remaining int
		want      int
	}{
		{name: "every notch sent, rounding left over", delta: -50, total: 1, remaining: 0, want: 0},
		{name: "every notch sent, exact", delta: 60, total: 2, remaining: 0, want: 0},
		{name: "nothing sent keeps the whole delta", delta: -50, total: 1, remaining: 1, want: -50},
		{name: "half sent keeps the unsent half", delta: 120, total: 4, remaining: 2, want: 60},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := uinputScrollRemainder(testCase.delta, testCase.total, testCase.remaining, scale)
			if got != testCase.want {
				t.Fatalf("uinputScrollRemainder(%d, %d, %d) = %d, want %d",
					testCase.delta, testCase.total, testCase.remaining, got, testCase.want)
			}
		})
	}
}

// TestWlrootsScrollNotch_StepAndDiscreteAgree pins that the pixel step and
// the discrete count a virtual-pointer notch carries point the same way, so
// clients reading axis and clients reading axis_value120 scroll in the same
// direction. Both axes negate the caller's delta, so one table covers them.
func TestWlrootsScrollNotch_StepAndDiscreteAgree(t *testing.T) {
	const step = 30

	tests := []struct {
		name     string
		delta    int
		wantStep int
		wantDisc int
	}{
		{name: "scroll down or right", delta: -50, wantStep: step, wantDisc: 1},
		{name: "scroll up or left", delta: 50, wantStep: -step, wantDisc: -1},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			gotStep, gotDisc := wlrootsScrollNotch(testCase.delta, step)
			if gotStep != testCase.wantStep || gotDisc != testCase.wantDisc {
				t.Fatalf("wlrootsScrollNotch(%d) = (%d, %d), want (%d, %d)",
					testCase.delta,
					gotStep, gotDisc,
					testCase.wantStep, testCase.wantDisc)
			}
		})
	}
}
