package contour_test

import (
	"image"
	"testing"

	"github.com/y3owk1n/neru/internal/adapter/vision/contour"
)

func TestMergeRuns_JoinsWordsOfOneLabel(t *testing.T) {
	t.Parallel()

	// "Create" and "account" on one line, a space apart.
	rects := []image.Rectangle{
		image.Rect(100, 40, 160, 56),
		image.Rect(166, 40, 240, 56),
	}

	got := contour.MergeRuns(rects)

	if len(got) != 1 {
		t.Fatalf("want 1 merged rect, got %d: %v", len(got), got)
	}

	if want := image.Rect(100, 40, 240, 56); got[0] != want {
		t.Errorf("merged box = %v, want %v", got[0], want)
	}
}

func TestMergeRuns_MergingIsTransitive(t *testing.T) {
	t.Parallel()

	rects := []image.Rectangle{
		image.Rect(0, 0, 30, 16),
		image.Rect(36, 0, 66, 16),
		image.Rect(72, 0, 102, 16),
	}

	got := contour.MergeRuns(rects)

	if len(got) != 1 {
		t.Fatalf("want 1 merged rect, got %d: %v", len(got), got)
	}

	if want := image.Rect(0, 0, 102, 16); got[0] != want {
		t.Errorf("merged box = %v, want %v", got[0], want)
	}
}

func TestMergeRuns_KeepsSeparateTargetsApart(t *testing.T) {
	t.Parallel()

	tests := map[string][]image.Rectangle{
		"different lines": {
			image.Rect(0, 0, 40, 16),
			image.Rect(0, 40, 40, 56),
		},
		"gap too wide for a space": {
			image.Rect(0, 0, 40, 16),
			image.Rect(90, 0, 130, 16),
		},
		"heights too different": {
			image.Rect(0, 0, 40, 12),
			image.Rect(46, 0, 86, 40),
		},
	}

	for name, rects := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := contour.MergeRuns(rects); len(got) != 2 {
				t.Errorf("want both rects kept, got %d: %v", len(got), got)
			}
		})
	}
}

func TestMergeRuns_DropsRunsPastDetectCeilings(t *testing.T) {
	t.Parallel()

	// Seven words of prose across a 700-logical-wide line. Each word survives
	// Detect on its own; the merged line is wider than maxTargetWidth.
	var rects []image.Rectangle
	for x := 0; x < 700; x += 100 {
		rects = append(rects, image.Rect(x, 0, x+94, 16))
	}

	if got := contour.MergeRuns(rects); len(got) != 0 {
		t.Errorf("want the over-wide run dropped, got %d: %v", len(got), got)
	}
}

func TestMergeRuns_LeavesLoneRectsUntouched(t *testing.T) {
	t.Parallel()

	rects := []image.Rectangle{
		image.Rect(10, 10, 34, 34),
		image.Rect(200, 300, 224, 324),
	}

	got := contour.MergeRuns(rects)

	if len(got) != len(rects) {
		t.Fatalf("want %d rects, got %d: %v", len(rects), len(got), got)
	}

	for i := range rects {
		if got[i] != rects[i] {
			t.Errorf("rect %d = %v, want %v", i, got[i], rects[i])
		}
	}
}

func TestMergeRuns_Empty(t *testing.T) {
	t.Parallel()

	if got := contour.MergeRuns(nil); len(got) != 0 {
		t.Errorf("want no rects, got %v", got)
	}
}
