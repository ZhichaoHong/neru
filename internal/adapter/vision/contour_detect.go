package vision

import (
	"image"
	"math"

	"github.com/y3owk1n/neru/internal/adapter/vision/contour"
	"github.com/y3owk1n/neru/internal/domain/element"
)

// contourElements is the whole of the contour strategy after the frame is in
// hand: detect, merge same-line runs, put the rectangles back in the caller's
// coordinate space, and hand them over as elements. Every platform's adapter
// runs this same chain, because the detector is platform-neutral Go and only the
// capture underneath it differs.
//
// It deliberately does not go through newRegionClassifier and
// elementsFromRegions the way the OCR path does. A contour rectangle carries no
// text and therefore no Score, and heuristics.go falls back to a non-clickable
// Generic role when Score is zero - so routing contour through the classifier
// would produce hints that cannot be clicked. contour.Elements builds clickable
// vision-only buttons directly instead.
//
// The two scales are different questions and the units only work out if both are
// answered:
//
//   - scale is the display's physical pixels per logical pixel. The detector's
//     size thresholds are logical-pixel numbers, so this is what tells it that a
//     42-pixel button on a 150% display is a 28-logical-pixel button.
//   - frameScale is how many frame pixels make up one unit of the coordinate
//     space region is expressed in. It is 1 where the window rect and the capture
//     are both physical pixels, and the display's backing scale where the window
//     rect is in points and the capture is in pixels.
//
// Detect returns logical pixels, so the rectangles are multiplied by
// scale/frameScale to land back in region's space. Where the window rect is
// already logical those two cancel and the factor is 1, which is the case the
// arithmetic below has to leave untouched.
//
// The frame is screen content and stays here: nothing below logs it, derives log
// text from it, or keeps it past the call.
func contourElements(
	frame *image.RGBA,
	origin image.Point,
	region image.Rectangle,
	scale float64,
	frameScale float64,
) ([]*element.Element, error) {
	rects, err := contour.Detect(frame, scale)
	if err != nil {
		return nil, err
	}

	rects = contour.MergeRuns(rects)

	if frameScale > 0 && scale != frameScale {
		factor := scale / frameScale
		for i, rect := range rects {
			rects[i] = scaleRect(rect, factor)
		}
	}

	// The role is the running platform's own name for a button, not the semantic
	// one: ports.ElementFilter compares it against clickable_roles resolved to that
	// same vocabulary. Button rather than Generic because a contour rectangle says
	// nothing about what it is, and Generic ("Custom" on UIA, "unknown" on AT-SPI)
	// is not in anybody's clickable_roles - a strategy whose every hint is filtered
	// out by the default config is not a strategy.
	role := element.Role(currentClassifierRoles().Button)

	return contour.Elements(origin, region, rects, role), nil
}

// scaleRect multiplies a rectangle about the frame's origin, rounding each edge
// so a target keeps its size rather than losing a pixel off each side.
func scaleRect(rect image.Rectangle, factor float64) image.Rectangle {
	return image.Rect(
		int(math.Round(float64(rect.Min.X)*factor)),
		int(math.Round(float64(rect.Min.Y)*factor)),
		int(math.Round(float64(rect.Max.X)*factor)),
		int(math.Round(float64(rect.Max.Y)*factor)),
	)
}
