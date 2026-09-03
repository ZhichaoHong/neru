package contour_test

import (
	"image"
	"testing"

	"github.com/y3owk1n/neru/internal/adapter/vision/contour"
	"github.com/y3owk1n/neru/internal/domain/element"
)

func TestElements_ClipsToRegionAndDropsOutside(t *testing.T) {
	t.Parallel()

	origin := image.Pt(1920, 0)
	region := image.Rect(2000, 100, 2800, 700)
	rects := []image.Rectangle{
		image.Rect(100, 150, 200, 190), // inside once offset
		image.Rect(10, 10, 50, 30),     // outside region
		image.Rect(40, 300, 120, 340),  // straddles the region's left edge
	}

	got := contour.Elements(origin, region, rects, element.RoleButton)
	if len(got) != 2 {
		t.Fatalf("len(Elements) = %d, want 2", len(got))
	}

	want := image.Rect(2020, 150, 2120, 190)
	if got[0].Bounds() != want {
		t.Errorf("Bounds() = %v, want %v", got[0].Bounds(), want)
	}

	clipped := image.Rect(2000, 300, 2040, 340)
	if got[1].Bounds() != clipped {
		t.Errorf("straddling Bounds() = %v, want clipped to %v", got[1].Bounds(), clipped)
	}

	if !got[0].IsClickable() || !got[0].IsVisionOnly() {
		t.Error("element must be clickable and vision-only")
	}
}

func TestElements_CarriesTheRoleItWasGiven(t *testing.T) {
	t.Parallel()

	region := image.Rect(0, 0, 100, 100)
	rects := []image.Rectangle{image.Rect(10, 10, 40, 30)}

	// A native name from another platform's vocabulary, so a hardcoded AX role
	// would fail this rather than pass it by coincidence.
	got := contour.Elements(image.Point{}, region, rects, element.Role("push button"))
	if len(got) != 1 {
		t.Fatalf("len(Elements) = %d, want 1", len(got))
	}

	if want := element.Role("push button"); got[0].Role() != want {
		t.Errorf("Role() = %q, want %q", got[0].Role(), want)
	}
}
