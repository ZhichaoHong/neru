package contour

import (
	"fmt"
	"image"

	"github.com/y3owk1n/neru/internal/domain/element"
)

// Elements turns detector rectangles, which are logical pixels relative to the
// captured frame's top-left corner, into clickable vision-only elements in
// global coordinates. origin is where the frame sits on the global desktop.
// Rectangles are clipped to region, so a frame wider than the region asked
// for (macOS captures the whole display) yields nothing outside it, and a
// target straddling the edge cannot put its hint, and its click, outside.
//
// role is the caller's, and it is the one deviation from upstream in this file.
// Upstream hardcodes element.RoleButton, which is the AX name "AXButton"; the
// hint pipeline compares an element's role against clickable_roles already
// resolved to the running platform's vocabulary, so on Windows that list holds
// "Button" and on Linux "push button" and a hardcoded AX name matches neither -
// every contour rectangle is detected and then filtered away. The OCR
// classifier answers in the native vocabulary for the same reason
// (internal/adapter/vision/classifier_roles.go).
func Elements(
	origin image.Point,
	region image.Rectangle,
	rects []image.Rectangle,
	role element.Role,
) []*element.Element {
	elements := make([]*element.Element, 0, len(rects))

	for _, rect := range rects {
		bounds := rect.Add(origin).Intersect(region)
		if bounds.Empty() {
			continue
		}

		elementID := element.ID(
			fmt.Sprintf(
				"contour-%d-%d-%d-%d",
				bounds.Min.X,
				bounds.Min.Y,
				bounds.Dx(),
				bounds.Dy(),
			),
		)

		elem, err := element.NewElement(
			elementID,
			bounds,
			role,
			element.WithClickable(true),
			element.WithVisionOnly(),
		)
		if err != nil {
			continue
		}

		elements = append(elements, elem)
	}

	return elements
}
