//go:build windows

package windows

import (
	"image"
	"testing"
)

// A drag is carried by the moves between the press and the release, so the path
// they take is what these tests pin: it has to arrive exactly, and it has to be
// movement rather than one jump.

func TestDragPathEndsOnTheDestination(t *testing.T) {
	tests := []struct {
		name string
		from image.Point
		to   image.Point
	}{
		{name: "across the desktop", from: image.Pt(0, 0), to: image.Pt(3840, 1200)},
		{name: "a few pixels", from: image.Pt(500, 500), to: image.Pt(504, 498)},
		{name: "backwards", from: image.Pt(900, 900), to: image.Pt(100, 200)},
		{name: "nowhere at all", from: image.Pt(700, 300), to: image.Pt(700, 300)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := dragPath(test.from, test.to)

			if len(path) < dragStepCountMin {
				t.Fatalf("path has %d steps, want at least %d", len(path), dragStepCountMin)
			}

			if len(path) > dragStepCountMax {
				t.Fatalf("path has %d steps, want at most %d", len(path), dragStepCountMax)
			}

			if last := path[len(path)-1]; last != test.to {
				t.Errorf("path ends at %v, want the destination %v", last, test.to)
			}
		})
	}
}

// TestDragPathStepsScaleWithDistance covers the reason the count is not fixed: a
// drag across the desktop needs the steps, and a nudge does not.
func TestDragPathStepsScaleWithDistance(t *testing.T) {
	near := len(dragPath(image.Pt(0, 0), image.Pt(10, 0)))
	far := len(dragPath(image.Pt(0, 0), image.Pt(3000, 0)))

	if near != dragStepCountMin {
		t.Errorf("a 10px drag walks %d steps, want the minimum %d", near, dragStepCountMin)
	}

	if far != dragStepCountMax {
		t.Errorf("a 3000px drag walks %d steps, want the maximum %d", far, dragStepCountMax)
	}
}
