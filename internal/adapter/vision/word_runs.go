package vision

import (
	"image"
	"slices"
	"strings"
)

// wordGapRatio is how far apart two recognized words may sit and still be read as
// one label, as a fraction of the height of the line of text they sit on.
//
// A ratio rather than a pixel count, because that is the only form that survives a
// scale factor: the same dialog at 200% doubles every gap and every glyph height,
// and only their ratio holds still.
//
// Measured on a live Win32 property sheet at 150%, with the labels known, gaps
// inside one label ran 0.25 to 0.50 of the line height and gaps between adjacent
// controls 0.72 to 6.7. Other surfaces put the tightest control gap at 0.58, so
// the usable margin is 0.50 to 0.58 and this sits in it.
//
// It is deliberately nearer the low end. The two ways to be wrong are not
// symmetric: splitting a label costs a redundant hint, while merging two controls
// costs reachability, because a hint acts at its element's center and the center of
// a merged rect can land between them. Eroding the control margin to make prose
// prettier is the wrong trade, so a wide word space - a monospace terminal's is
// about 0.56 of its line height - fragments rather than risk it. `--split-word`
// remains the escape hatch in both directions.
const wordGapRatio = 0.55

// wordOverlapRatio is how much of the shorter rect's height must sit inside the
// taller one's vertical span for the two to belong to one line of text.
//
// Generous, because a word with no ascender or descender is genuinely shorter than
// its neighbour: "core" against "Config" differs by the cap height, so demanding
// near-equal spans would refuse to group ordinary prose onto one line. Stacked rows
// of text share nothing at all - on the property sheet above, consecutive rows'
// ink spans were adjacent but never overlapped - so the loose threshold does not
// let a column collapse into a row.
const wordOverlapRatio = 0.5

// mergeWordRuns joins recognized words that read as one label into one region.
//
// Vision backends segment text, not controls, and both granularities they offer are
// wrong in one direction. A line rect merges distinct controls: a property sheet's
// five-tab strip arrives as one 488px rect, and since a hint acts at its element's
// center, four of those tabs end up with no hint at all. Word rects never do that,
// but they split every two-word label into two hints and every paragraph into a
// dozen.
//
// So the pipeline asks for words and rebuilds the labels here. Words are grouped
// into lines of text, then each line is swept left to right: a gap narrow enough to
// be a word space continues the run, and a gap wide enough to be a control's
// padding ends it. Prose comes back whole, "System Protection" comes back whole,
// and "Remote" beside it stays its own hint.
//
// The gap is measured against the whole line's height rather than against the two
// words being compared, because a word's ink height depends on which glyphs it
// happens to contain. "use" is 12px tall and "a" is 9px on the same 16px line, and
// judging their 6px gap against 9px calls a word space a control boundary. The line
// is the same height for every pair on it, which is the only stable denominator
// available from word rects alone.
//
// Non-text regions pass through untouched. A rectangle detection is not text, has
// no baseline to share and no glyph height to measure a gap against.
//
// Order is not preserved: text regions come back grouped by line. Nothing
// downstream depends on the order - MergeRegions sorts by score and the hint
// labeller sorts by position.
func mergeWordRuns(regions []DetectedRegion) []DetectedRegion {
	if len(regions) < 2 {
		return regions
	}

	words := make([]DetectedRegion, 0, len(regions))
	merged := make([]DetectedRegion, 0, len(regions))

	for _, region := range regions {
		if !region.IsText || region.Bounds.Empty() {
			merged = append(merged, region)

			continue
		}

		words = append(words, region)
	}

	for _, line := range groupTextLines(words) {
		merged = append(merged, splitLineIntoRuns(line)...)
	}

	return merged
}

// groupTextLines clusters word rects into the lines of text they sit on.
//
// A word joins the first line whose ink span it shares, and that line's span grows
// to include it - so a line accumulates from whichever of its words is read first,
// rather than depending on one of them being representative.
func groupTextLines(words []DetectedRegion) [][]DetectedRegion {
	lines := make([][]DetectedRegion, 0, len(words))
	spans := make([]image.Rectangle, 0, len(words))

	for _, word := range words {
		joined := false

		for i := range lines {
			if !sameTextLine(spans[i], word.Bounds) {
				continue
			}

			lines[i] = append(lines[i], word)
			spans[i] = spans[i].Union(word.Bounds)
			joined = true

			break
		}

		if !joined {
			lines = append(lines, []DetectedRegion{word})
			spans = append(spans, word.Bounds)
		}
	}

	return lines
}

// splitLineIntoRuns cuts one line of text into the labels it reads as.
func splitLineIntoRuns(line []DetectedRegion) []DetectedRegion {
	slices.SortStableFunc(line, func(a, b DetectedRegion) int {
		return a.Bounds.Min.X - b.Bounds.Min.X
	})

	span := line[0].Bounds
	for _, word := range line {
		span = span.Union(word.Bounds)
	}

	limit := wordGapRatio * float64(span.Dy())

	runs := make([]DetectedRegion, 0, len(line))
	run := []DetectedRegion{line[0]}

	for _, word := range line[1:] {
		// A negative gap is an overlap rather than a space. It happens where OCR reads
		// a glyph twice at a word boundary, and those two rects are certainly one
		// label.
		gap := word.Bounds.Min.X - run[len(run)-1].Bounds.Max.X

		if float64(gap) <= limit {
			run = append(run, word)

			continue
		}

		runs = append(runs, joinRun(run))
		run = []DetectedRegion{word}
	}

	return append(runs, joinRun(run))
}

// sameTextLine reports whether two rects sit on one line of text.
func sameTextLine(first, second image.Rectangle) bool {
	overlap := min(first.Max.Y, second.Max.Y) - max(first.Min.Y, second.Min.Y)
	if overlap <= 0 {
		return false
	}

	shorter := min(first.Dy(), second.Dy())
	if shorter <= 0 {
		return false
	}

	return float64(overlap) >= wordOverlapRatio*float64(shorter)
}

// joinRun collapses a run of words, already in reading order, into the one region
// they read as.
//
// The label is the words joined by single spaces, which is what the engine's own
// line text would have been for that stretch. Score is the highest in the run: the
// backends that report one report per word, and a run is as confident as its most
// confident word.
func joinRun(run []DetectedRegion) DetectedRegion {
	if len(run) == 1 {
		return run[0]
	}

	joined := run[0]
	labels := make([]string, 0, len(run))

	for _, word := range run {
		joined.Bounds = joined.Bounds.Union(word.Bounds)
		joined.Score = max(joined.Score, word.Score)

		if word.Label != "" {
			labels = append(labels, word.Label)
		}
	}

	joined.Label = strings.Join(labels, " ")

	return joined
}
