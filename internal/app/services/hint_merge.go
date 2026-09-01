package services

import (
	"image"

	"github.com/y3owk1n/neru/internal/domain/element"
)

// treeWinsCoverage is how much of an OCR region has to sit inside one tree
// element for that element to have already answered for it.
//
// Measured against the OCR region's own area rather than the two rectangles'
// union, which is the whole point. A control is much bigger than the text printed
// in it - an Explorer tree row is 64px high around a 21px line - so union-based
// IOU scores that pair at 0.28 even when the text is entirely within the row, and
// no threshold low enough to catch it is high enough to be meaningful. Coverage
// asks the question that matters: is most of what OCR read explained by this one
// control?
//
// A constant rather than an option, and deliberately not
// hints.vision.merge_iou_threshold: that one tunes OCR-against-OCR suppression
// inside the vision adapter, so sharing it would mean a user tuning how words
// merge silently changes how the tree deduplicates. A constant can become an
// option on evidence; the reverse breaks configs.
const treeWinsCoverage = 0.5

// mergeVisionWithTree drops the OCR elements the accessibility tree has already
// answered for, and returns the tree elements untouched.
//
// Asymmetric on purpose. This is not non-maximum suppression: a tree element is
// never dropped, however much OCR text sits on top of it, because it carries a
// real role and a real title. Those are what --filter-role and hint search read,
// and the OCR alternative is StaticText holding a possibly garbled copy of the
// same words. Both are equally clickable - a hint acts at a point, never through
// an accessibility handle - so clickability is not the reason.
//
// Two ways a tree element claims an OCR one:
//
//   - It covers more than treeWinsCoverage of the OCR rect. The common case by
//     far: a button's label is recognized inside the button. Stated as coverage
//     rather than containment because OCR routinely reads a pixel past a control's
//     bounds, or swallows an adjacent glyph - an Explorer tree row's text arrives
//     with the expander chevron attached, 76px left of where the row starts.
//   - The OCR rect spans two or more tree elements. The same rule read backwards:
//     a Win32 tab strip arrives from OCR as one wide line spanning every tab, and
//     keeping it would put a label across the middle of the strip on top of the
//     per-tab labels. Two or more rather than any, because "any" would drop a
//     paragraph of text for one stray tree element inside it - which is exactly
//     the text hybrid exists to add.
//
// The tree set here is already role-filtered, because the filter is applied inside
// the accessibility adapter. So an OCR word sitting inside a tree element the role
// filter excluded survives. That reading is defensible - the user asked for
// buttons, and OCR's guess at button-ness is what is left - but nobody chose it
// deliberately.
func mergeVisionWithTree(elements []*element.Element) []*element.Element {
	var tree, vision []*element.Element

	for _, candidate := range elements {
		if candidate.IsVisionOnly() {
			vision = append(vision, candidate)

			continue
		}

		tree = append(tree, candidate)
	}

	if len(tree) == 0 || len(vision) == 0 {
		return elements
	}

	// Tree elements first and in their original order, so hint labels stay stable
	// against a screen where only the OCR half moved.
	merged := make([]*element.Element, 0, len(elements))
	merged = append(merged, tree...)

	for _, detected := range vision {
		if treeAnswersFor(detected.Bounds(), tree) {
			continue
		}

		merged = append(merged, detected)
	}

	return merged
}

// treeAnswersFor reports whether the tree already covers a detected region.
//
// Every rectangle here is non-empty: element.NewElement refuses empty bounds, so
// both sets came through that check. That matters twice - coveredFraction divides
// by the detected region's area, and image.Rectangle.In is true for an empty
// rectangle against anything, which would make an empty region look enclosed by
// every tree element it was compared with.
func treeAnswersFor(detected image.Rectangle, tree []*element.Element) bool {
	var spanned int

	for _, known := range tree {
		bounds := known.Bounds()

		if coveredFraction(detected, bounds) > treeWinsCoverage {
			return true
		}

		if center(bounds).In(detected) {
			spanned++

			if spanned >= 2 {
				return true
			}
		}
	}

	return false
}

// center is the midpoint of a rectangle as a 1x1 rectangle, so In can test it.
//
// Spanning is measured against centers rather than whole rectangles because a
// control is taller than the text printed inside it: Windows Terminal's tab items
// are 48px high and the line OCR reads off them is 18px, so no tab is ever fully
// inside the strip's OCR rect. Centers are what actually distinguish
// "this region covers several controls" from "this region sits beside them".
func center(rect image.Rectangle) image.Rectangle {
	mid := image.Pt((rect.Min.X+rect.Max.X)/2, (rect.Min.Y+rect.Max.Y)/2)

	return image.Rectangle{Min: mid, Max: mid.Add(image.Pt(1, 1))}
}

// coveredFraction is how much of detected lies inside cover, as a fraction of
// detected's own area. One when cover encloses detected, zero when they do not
// touch. Asymmetric in its arguments, unlike IOU.
func coveredFraction(detected, cover image.Rectangle) float64 {
	intersection := detected.Intersect(cover)
	if intersection.Empty() {
		return 0
	}

	return float64(area(intersection)) / float64(area(detected))
}

func area(rect image.Rectangle) int {
	return rect.Dx() * rect.Dy()
}
