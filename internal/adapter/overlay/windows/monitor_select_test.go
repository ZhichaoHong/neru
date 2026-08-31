//go:build windows

package windows

import (
	"image"
	"testing"

	"github.com/y3owk1n/neru/internal/adapter/overlay/manager"
)

// What this backend decides about monitor_select on its own: the size of the
// window the panels go into. The panel geometry inside it is shared with the
// Linux backend and tested beside it (render/monitorselect).
//
// There is no draw test here, for the same reason there is none for hints:
// reaching DrawMonitorSelect's body means creating a real layered window.

func TestMonitorSelectSpan(t *testing.T) {
	t.Parallel()

	primary := image.Rect(0, 0, 1920, 1080)

	tests := []struct {
		name    string
		targets []manager.MonitorSelectTarget
		want    image.Rectangle
	}{
		{
			name: "no targets span nothing",
			want: image.Rectangle{},
		},
		{
			name:    "one target spans itself",
			targets: []manager.MonitorSelectTarget{{Bounds: primary}},
			want:    primary,
		},
		{
			// A monitor left of the primary one has a negative X, which is the
			// case the drawing code translates rects out of.
			name: "a monitor left of the primary one moves the origin negative",
			targets: []manager.MonitorSelectTarget{
				{Bounds: primary},
				{Bounds: image.Rect(-1920, 0, 0, 1080)},
			},
			want: image.Rect(-1920, 0, 1920, 1080),
		},
		{
			name: "a monitor above the primary one moves the origin negative",
			targets: []manager.MonitorSelectTarget{
				{Bounds: primary},
				{Bounds: image.Rect(0, -1080, 1920, 0)},
			},
			want: image.Rect(0, -1080, 1920, 1080),
		},
		{
			// Union with the zero Rectangle would answer with the zero
			// Rectangle's own corner, stretching the window to the origin for a
			// monitor nobody can draw on.
			name: "an empty target does not stretch the span to the origin",
			targets: []manager.MonitorSelectTarget{
				{Bounds: image.Rect(1920, 0, 3840, 1080)},
				{Bounds: image.Rectangle{}},
			},
			want: image.Rect(1920, 0, 3840, 1080),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := monitorSelectSpan(test.targets)
			if got != test.want {
				t.Errorf("monitorSelectSpan() = %v, want %v", got, test.want)
			}
		})
	}
}
