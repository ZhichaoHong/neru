//go:build windows

package windows

import "image"

// Caption-button geometry, found by asking a window what sits under a point.
//
// The search is here, apart from the messaging in caption.go, because it is the
// part with the arithmetic: a probe function is all it needs, so it can be
// exercised against a synthetic window layout instead of a real one.

// hitCode is a WM_NCHITTEST result. Only the three the caption buttons answer
// with are named; everything else is compared, never interpreted.
type hitCode int32

const (
	htMinButton hitCode = 8
	htMaxButton hitCode = 9
	htClose     hitCode = 20
)

// captionProbe reports what a window says sits under a screen point. ok is
// false when the window did not answer - a timeout, or a spent budget - and
// that ends the whole search: a half-probed caption is worse than none, because
// a badge drawn on a guess still invites a click.
type captionProbe func(point image.Point) (code hitCode, ok bool)

// Logical pixels at 96 dpi, scaled by the monitor's factor before use.
const (
	// captionScanDepth is how far down from the top of the window the search
	// looks. A Windows 11 caption is 32 logical pixels; Electron frames draw
	// theirs taller, and Teams' is 48.
	captionScanDepth = 56

	// captionScanStep is the sampling interval. It has to be smaller than the
	// narrowest button worth finding - the system's are 45 wide - so every
	// button gets several samples and none can fall between two probes.
	captionScanStep = 8

	// captionScanSpan is how far in from the window's edge the search reaches.
	// Three buttons occupy 135 logical pixels; the rest is room for a frame
	// inset and for apps that pad theirs.
	captionScanSpan = 240

	// A run smaller or larger than these is not a caption button, whatever the
	// window claims. The lower bound rejects a stray pixel, the upper one a
	// window that answers a button code across its whole frame.
	captionButtonMinSide = 8
	captionButtonMaxSide = 200
)

// captionProbeLines is how many horizontal lines the search samples inside the
// caption band. Three, at even depths, because the band is taller than the
// buttons in every frame that draws its own: a single line down the middle
// misses a button drawn flush to the top, and one at the top hits the resize
// border instead.
const captionProbeLines = 3

// captionKinds are the hit-test codes worth a hint, in the order the caption
// draws them. Help is left out: the code exists, but no shipping frame draws
// the button, and a hint for something invisible is a wasted label.
var captionKinds = []struct {
	code hitCode
	kind CaptionButtonKind
}{
	{htMinButton, CaptionMinimize},
	{htMaxButton, CaptionMaximize},
	{htClose, CaptionClose},
}

// scanCaptionButtons finds the caption buttons of the window the probe speaks
// for, within area, in the same coordinates the probe takes.
//
// scale is the monitor's physical pixels per logical unit, which every
// dimension below is expressed in: a step fixed in physical pixels would
// oversample a 100% monitor and, at 300%, walk straight over a button.
func scanCaptionButtons(area image.Rectangle, scale float64, probe captionProbe) []CaptionButton {
	if probe == nil || area.Empty() {
		return nil
	}

	step := max(1, scaleLogical(captionScanStep, scale))
	depth := min(scaleLogical(captionScanDepth, scale), area.Dy())
	span := min(scaleLogical(captionScanSpan, scale), area.Dx())

	seeds, ok := seedCaptionButtons(area, span, depth, step, probe)
	if !ok || len(seeds) == 0 {
		return nil
	}

	buttons := make([]CaptionButton, 0, len(seeds))

	for _, candidate := range captionKinds {
		seed, found := seeds[candidate.code]
		if !found {
			continue
		}

		bounds, ok := growCaptionButton(seed, candidate.code, area, step, probe)
		if !ok {
			return nil
		}

		if !plausibleCaptionButton(bounds, scale) {
			continue
		}

		buttons = append(buttons, CaptionButton{Kind: candidate.kind, Bounds: bounds})
	}

	return buttons
}

// seedCaptionButtons samples the caption band for one point inside each button.
//
// The right edge is searched first and the left only if the right yielded
// nothing, which is both the common case and the cheap one: a right-to-left
// layout moves the buttons to the left, and nothing else does.
func seedCaptionButtons(
	area image.Rectangle,
	span int,
	depth int,
	step int,
	probe captionProbe,
) (map[hitCode]image.Point, bool) {
	lines := captionScanRows(area.Min.Y, depth)

	for _, fromRight := range []bool{true, false} {
		seeds := make(map[hitCode]image.Point, len(captionKinds))

		for _, y := range lines {
			for offset := 0; offset < span; offset += step {
				x := area.Min.X + offset
				if fromRight {
					x = area.Max.X - 1 - offset
				}

				code, ok := probe(image.Pt(x, y))
				if !ok {
					return nil, false
				}

				if _, wanted := captionKindOf(code); !wanted {
					continue
				}

				if _, seen := seeds[code]; !seen {
					seeds[code] = image.Pt(x, y)
				}
			}

			if len(seeds) == len(captionKinds) {
				return seeds, true
			}
		}

		if len(seeds) > 0 {
			return seeds, true
		}
	}

	return nil, true
}

// captionScanRows returns the y coordinates to sample, spread evenly through
// the band and never on its first or last row: the top row of a window is its
// resize border, which answers with a border code rather than a button.
func captionScanRows(top int, depth int) []int {
	rows := make([]int, 0, captionProbeLines)

	for i := 1; i <= captionProbeLines; i++ {
		rows = append(rows, top+depth*i/(captionProbeLines+1))
	}

	return rows
}

// growCaptionButton expands a point inside a button into the button's extent,
// by walking outwards in steps until the answer changes and then bisecting the
// last step. Adjacent buttons bound each other, because the walk stops at the
// first point that answers with a different code.
func growCaptionButton(
	seed image.Point,
	code hitCode,
	area image.Rectangle,
	step int,
	probe captionProbe,
) (image.Rectangle, bool) {
	left, ok := captionRunEnd(func(offset int) image.Point {
		return image.Pt(seed.X-offset, seed.Y)
	}, code, seed.X-area.Min.X, step, probe)
	if !ok {
		return image.Rectangle{}, false
	}

	right, ok := captionRunEnd(func(offset int) image.Point {
		return image.Pt(seed.X+offset, seed.Y)
	}, code, area.Max.X-1-seed.X, step, probe)
	if !ok {
		return image.Rectangle{}, false
	}

	top, ok := captionRunEnd(func(offset int) image.Point {
		return image.Pt(seed.X, seed.Y-offset)
	}, code, seed.Y-area.Min.Y, step, probe)
	if !ok {
		return image.Rectangle{}, false
	}

	bottom, ok := captionRunEnd(func(offset int) image.Point {
		return image.Pt(seed.X, seed.Y+offset)
	}, code, area.Max.Y-1-seed.Y, step, probe)
	if !ok {
		return image.Rectangle{}, false
	}

	return image.Rect(
		seed.X-left,
		seed.Y-top,
		seed.X+right+1,
		seed.Y+bottom+1,
	), true
}

// captionRunEnd returns the largest offset along one direction that still
// answers with code, bounded by maxOffset. at maps an offset to the point to
// probe, which is what lets one function walk all four directions.
func captionRunEnd(
	at func(offset int) image.Point,
	code hitCode,
	maxOffset int,
	step int,
	probe captionProbe,
) (int, bool) {
	if maxOffset <= 0 {
		return 0, true
	}

	inside := 0
	outside := 0

	for offset := min(step, maxOffset); ; offset = min(offset+step, maxOffset) {
		got, ok := probe(at(offset))
		if !ok {
			return 0, false
		}

		if got != code {
			outside = offset

			break
		}

		inside = offset

		if offset == maxOffset {
			return maxOffset, true
		}
	}

	// The edge is between the last point that answered with code and the first
	// that did not, and the step is coarse enough that the difference shows: a
	// badge sized from `inside` alone would sit up to a step short of the
	// button it labels.
	for outside-inside > 1 {
		middle := inside + (outside-inside)/2

		got, ok := probe(at(middle))
		if !ok {
			return 0, false
		}

		if got == code {
			inside = middle
		} else {
			outside = middle
		}
	}

	return inside, true
}

// plausibleCaptionButton rejects a run that no caption button could be. A
// window is free to answer a button code anywhere, including across its entire
// frame, and a hint over half a window would be worse than the missing one it
// replaces.
func plausibleCaptionButton(bounds image.Rectangle, scale float64) bool {
	if bounds.Empty() {
		return false
	}

	minSide := scaleLogical(captionButtonMinSide, scale)
	maxSide := scaleLogical(captionButtonMaxSide, scale)

	return bounds.Dx() >= minSide && bounds.Dy() >= minSide &&
		bounds.Dx() <= maxSide && bounds.Dy() <= maxSide
}

// captionKindOf reports which caption button a hit-test code names.
func captionKindOf(code hitCode) (CaptionButtonKind, bool) {
	for _, candidate := range captionKinds {
		if candidate.code == code {
			return candidate.kind, true
		}
	}

	return 0, false
}

// scaleLogical converts a dimension written in logical pixels into physical
// ones, never returning less than one pixel.
func scaleLogical(logical int, scale float64) int {
	if scale <= 0 {
		scale = 1
	}

	return max(1, int(float64(logical)*scale+0.5))
}
