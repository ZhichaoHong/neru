package vision

import (
	"image"
	"image/color"
	"image/draw"
	"runtime"
	"slices"
	"testing"

	"github.com/y3owk1n/neru/internal/domain/element"
)

// TestScaleRectKeepsATargetsSize is the arithmetic the DPI correction rests on.
//
// Truncating each edge instead of rounding shaves up to a pixel off the right and
// bottom of every target, which on a row of 16-pixel toolbar icons is a tenth of
// the thing being clicked. Rounding both edges keeps the size and moves the box.
func TestScaleRectKeepsATargetsSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		rect   image.Rectangle
		factor float64
		want   image.Rectangle
	}{
		{
			name:   "factor of one is the identity",
			rect:   image.Rect(10, 20, 60, 44),
			factor: 1,
			want:   image.Rect(10, 20, 60, 44),
		},
		{
			name:   "150 percent display",
			rect:   image.Rect(10, 10, 20, 30),
			factor: 1.5,
			want:   image.Rect(15, 15, 30, 45),
		},
		{
			name:   "both edges round rather than truncate",
			rect:   image.Rect(3, 3, 4, 4),
			factor: 1.5,
			want:   image.Rect(5, 5, 6, 6),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := scaleRect(testCase.rect, testCase.factor)
			if got != testCase.want {
				t.Errorf("scaleRect(%v, %v) = %v, want %v",
					testCase.rect, testCase.factor, got, testCase.want)
			}

			if want := testCase.want.Dx() * testCase.want.Dy(); got.Dx()*got.Dy() != want {
				t.Errorf("scaled area %d, want %d", got.Dx()*got.Dy(), want)
			}
		})
	}
}

// TestContourElementsPlacesTargetsInsideTheRegion is the end-to-end shape of the
// contour path, on a frame with known boxes in it rather than a real screen.
//
// What is pinned is the contract every adapter depends on: the detector's
// frame-relative rectangles come back placed on the global desktop, clipped to the
// region asked for, clickable, and marked vision-only so the hybrid merge can tell
// them from tree elements. The exact count is the detector's business and is not
// asserted - only that boxes drawn in the frame produce targets.
func TestContourElementsPlacesTargetsInsideTheRegion(t *testing.T) {
	t.Parallel()

	region := image.Rect(300, 200, 500, 350)
	frame := frameWithBoxes(region.Dx(), region.Dy())

	elements, err := contourElements(frame, region.Min, region, 1, 1)
	if err != nil {
		t.Fatalf("contourElements failed: %v", err)
	}

	if len(elements) == 0 {
		t.Fatal("no targets found in a frame with three drawn boxes")
	}

	for _, elem := range elements {
		bounds := elem.Bounds()
		if !bounds.In(region) {
			t.Errorf("target %v is outside the region %v; its hint and its click would land there",
				bounds, region)
		}

		if !elem.IsClickable() {
			t.Errorf("target %v is not clickable, so nothing would hint it", bounds)
		}

		if !elem.IsVisionOnly() {
			t.Errorf("target %v is not marked vision-only", bounds)
		}
	}
}

// TestContourElementsCorrectsForADisplayScale covers the units bug this path
// exists to avoid: the detector answers in logical pixels while the region and the
// origin are physical, so on a 150% display every target lands at two thirds of
// where it belongs.
//
// Both calls detect at the same scale, so the detector sees an identical frame and
// returns identical rectangles. Only frameScale differs - 1.5 says the frame is
// already in the region's units, 1 says it is physical pixels - so any difference
// between the two results is the correction and nothing else.
func TestContourElementsCorrectsForADisplayScale(t *testing.T) {
	t.Parallel()

	// Wide enough that scaling by 1.5 clips nothing, which would otherwise look
	// like a correction that lost targets.
	region := image.Rect(0, 0, 4000, 3000)
	frame := frameWithBoxes(300, 220)

	uncorrected, err := contourElements(frame, region.Min, region, 1.5, 1.5)
	if err != nil {
		t.Fatalf("contourElements failed: %v", err)
	}

	corrected, err := contourElements(frame, region.Min, region, 1.5, 1)
	if err != nil {
		t.Fatalf("contourElements failed: %v", err)
	}

	if len(uncorrected) == 0 {
		t.Fatal("no targets found in a frame with three drawn boxes")
	}

	if len(corrected) != len(uncorrected) {
		t.Fatalf("the correction changed the target count from %d to %d",
			len(uncorrected), len(corrected))
	}

	for i, elem := range corrected {
		want := scaleRect(uncorrected[i].Bounds(), 1.5)
		if got := elem.Bounds(); got != want {
			t.Errorf("target %d landed at %v, want %v", i, got, want)
		}
	}
}

// TestContourElementsAnswersInThePlatformsRoleVocabulary is the regression this
// path was shipped with: the detector found 138 targets on a real Windows window
// and the overlay showed none of them, because the role was the AX name "AXButton"
// while the filter held UIA's "Button".
//
// The expectation comes from the role vocabulary rather than a copy of the native
// name, so it stays true on whichever platform runs it and fails if the two tables
// ever drift apart.
func TestContourElementsAnswersInThePlatformsRoleVocabulary(t *testing.T) {
	t.Parallel()

	region := image.Rect(0, 0, 400, 300)

	elements, err := contourElements(frameWithBoxes(region.Dx(), region.Dy()), region.Min, region, 1, 1)
	if err != nil {
		t.Fatalf("contourElements failed: %v", err)
	}

	if len(elements) == 0 {
		t.Fatal("no targets found in a frame with three drawn boxes")
	}

	native := element.ResolveRolesForCurrentPlatform([]string{string(element.SemanticButton)}).Native
	if len(native) == 0 {
		t.Skipf("%s has no accessibility vocabulary, so it has no vision adapter to filter for", runtime.GOOS)
	}

	for _, elem := range elements {
		if !slices.Contains(native, string(elem.Role())) {
			t.Fatalf("role %q is not one of %v, so clickable_roles would filter every contour hint away",
				elem.Role(), native)
		}
	}
}

// frameWithBoxes draws three outlined boxes on a white background: something with
// edges in it, which is all the detector reads. Sizes are comfortably inside the
// detector's own limits at both scales the tests above use.
func frameWithBoxes(width, height int) *image.RGBA {
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	for i := range 3 {
		box := image.Rect(20, 20+i*40, 120, 55+i*40)
		outline(frame, box)
	}

	return frame
}

// outline strokes a 2px black border, which survives the blur that precedes edge
// detection where a 1px one is close to being averaged away.
func outline(frame *image.RGBA, box image.Rectangle) {
	black := image.NewUniform(color.Black)

	edges := []image.Rectangle{
		image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+2),
		image.Rect(box.Min.X, box.Max.Y-2, box.Max.X, box.Max.Y),
		image.Rect(box.Min.X, box.Min.Y, box.Min.X+2, box.Max.Y),
		image.Rect(box.Max.X-2, box.Min.Y, box.Max.X, box.Max.Y),
	}

	for _, edge := range edges {
		draw.Draw(frame, edge, black, image.Point{}, draw.Src)
	}
}
