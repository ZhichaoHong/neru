package contour

import "image"

// This file is the one place the fork deliberately differs from upstream's
// contour package. Everything else here is upstream's code verbatim; MergeRuns
// is ours and has no counterpart there.
//
// The reason is that Detect boxes connected edge blobs, and at typical UI DPI an
// inter-word gap is wider than the dilation kernel, so a two-word label such as
// "Create account" comes back as two rectangles and two hints. The OCR path
// already solves the same problem with its word-run merging; contour had
// nothing. Measured on real frames, merging cut a dense article from 733
// rectangles to 94 and a full-screen RDP session from 442 to 189.

const (
	// runOverlapPercent is how much of the shorter rectangle's height must
	// overlap the other's before the two can be on the same line.
	runOverlapPercent = 60
	// runHeightRatio caps how different two heights may be. Text of one size
	// merges; a caption next to a heading does not.
	runHeightRatio = 2
	// runGapPercent is the horizontal gap allowed between neighbours, as a
	// percentage of text height, which approximates a space at that size.
	runGapPercent = 60
	// runMinGap keeps small text merging at all, where 60% of the height
	// rounds down to nearly nothing.
	runMinGap = 6
)

// MergeRuns joins rectangles that sit on the same line of text into one, then
// reapplies Detect's own size ceilings to the merged boxes. It takes and
// returns logical-pixel rectangles, so it belongs immediately after Detect and
// before Elements.
//
// Reapplying maxTargetWidth is what makes this more than cosmetic: a label
// merges into a label-sized box and survives, while a line of prose merges into
// an over-wide box and falls out. Two consequences are accepted deliberately. A
// link inside a paragraph is lost, because it merges into its line and the line
// is dropped - contour exists for RDP and canvas surfaces, and axtree covers
// prose links in windows that expose them. And prose in a narrow column still
// survives one box per line, because such a line is under the ceiling; a
// terminal is the worst case seen, where merging removed only a third.
func MergeRuns(rects []image.Rectangle) []image.Rectangle {
	parent := make([]int, len(rects))
	for i := range parent {
		parent[i] = i
	}

	find := func(i int) int {
		root := i
		for parent[root] != root {
			root = parent[root]
		}

		for parent[i] != root {
			parent[i], i = root, parent[i]
		}

		return root
	}

	for i := range rects {
		for j := i + 1; j < len(rects); j++ {
			if !sameRun(rects[i], rects[j]) {
				continue
			}

			if ri, rj := find(i), find(j); ri != rj {
				parent[rj] = ri
			}
		}
	}

	// Keep the first rectangle of each run in place, so the output stays in
	// Detect's component order and hint labels do not shuffle between frames.
	boxes := make(map[int]image.Rectangle, len(rects))
	order := make([]int, 0, len(rects))

	for i, rect := range rects {
		root := find(i)
		if existing, ok := boxes[root]; ok {
			boxes[root] = existing.Union(rect)

			continue
		}

		boxes[root] = rect
		order = append(order, root)
	}

	merged := make([]image.Rectangle, 0, len(order))

	for _, root := range order {
		box := boxes[root]
		if box.Dx() >= maxTargetWidth || box.Dy() >= maxTargetHeight {
			continue
		}

		merged = append(merged, box)
	}

	return merged
}

// sameRun reports whether two rectangles read as neighbouring words of one
// label: overlapping vertically, of comparable height, and close enough
// horizontally to be separated by a space rather than by layout.
func sameRun(a, b image.Rectangle) bool {
	overlapY := min(a.Max.Y, b.Max.Y) - max(a.Min.Y, b.Min.Y)
	shorter := min(a.Dy(), b.Dy())

	//nolint:mnd // percentages of the shorter height, held as integer arithmetic
	if shorter <= 0 || overlapY*100 < shorter*runOverlapPercent {
		return false
	}

	if abs(a.Dy()-b.Dy())*runHeightRatio > max(a.Dy(), b.Dy()) {
		return false
	}

	gap := max(a.Min.X, b.Min.X) - min(a.Max.X, b.Max.X)

	//nolint:mnd // percentages of the shorter height, held as integer arithmetic
	return gap <= max(runMinGap, shorter*runGapPercent/100)
}
