package vision

import (
	"image"
	"slices"
	"testing"
)

func textRegion(label string, bounds image.Rectangle) DetectedRegion {
	return DetectedRegion{Bounds: bounds, Label: label, IsText: true}
}

func TestMergeWordRunsJoinsWordsASpaceApart(t *testing.T) {
	t.Parallel()

	// The measured case: "Computer Name" 6px apart on an 18px line, then 15px of tab
	// padding before "Hardware".
	regions := []DetectedRegion{
		textRegion("Computer", image.Rect(20, 62, 100, 80)),
		textRegion("Name", image.Rect(106, 62, 150, 80)),
		textRegion("Hardware", image.Rect(165, 62, 240, 80)),
	}

	merged := mergeWordRuns(regions)

	if len(merged) != 2 {
		t.Fatalf("mergeWordRuns produced %d regions, want 2: %v", len(merged), boundsOf(merged))
	}

	label := labelFor(t, merged, image.Rect(20, 62, 150, 80))
	if label != "Computer Name" {
		t.Errorf("merged label = %q, want %q", label, "Computer Name")
	}

	labelFor(t, merged, image.Rect(165, 62, 240, 80))
}

func TestMergeWordRunsDecidesTheSameWayAtTwiceTheScale(t *testing.T) {
	t.Parallel()

	// Every rect and gap of the previous test doubled. A pixel threshold would decide
	// this differently; a ratio must not.
	regions := []DetectedRegion{
		textRegion("Computer", image.Rect(40, 124, 200, 160)),
		textRegion("Name", image.Rect(212, 124, 300, 160)),
		textRegion("Hardware", image.Rect(330, 124, 480, 160)),
	}

	merged := mergeWordRuns(regions)

	if len(merged) != 2 {
		t.Fatalf("mergeWordRuns produced %d regions, want 2: %v", len(merged), boundsOf(merged))
	}

	labelFor(t, merged, image.Rect(40, 124, 300, 160))
}

// The System Properties tab strip, transcribed from a live 150% capture: seven
// words, gaps 6, 15, 13, 14, 7, 15 on an 18px line. Line recognition returns the
// whole strip as one 488px rect, which leaves four of the five tabs with no
// reachable hint. Word recognition returns seven hints for five tabs. Neither is
// what the user sees.
func TestMergeWordRunsRebuildsTheMeasuredTabStrip(t *testing.T) {
	t.Parallel()

	regions := []DetectedRegion{
		textRegion("Computer", image.Rect(0, 366, 80, 384)),
		textRegion("Name", image.Rect(86, 366, 130, 382)),
		textRegion("Hardware", image.Rect(145, 368, 215, 380)),
		textRegion("Advanced", image.Rect(228, 368, 300, 380)),
		textRegion("System", image.Rect(314, 366, 370, 382)),
		textRegion("Protection", image.Rect(377, 368, 445, 380)),
		textRegion("Remote", image.Rect(460, 368, 510, 380)),
	}

	merged := mergeWordRuns(regions)

	want := []string{"Computer Name", "Hardware", "Advanced", "System Protection", "Remote"}

	got := make([]string, 0, len(merged))
	for _, region := range merged {
		got = append(got, region.Label)
	}

	slices.Sort(got)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("tab strip read as %q, want %q", got, want)
	}
}

// A word's ink height depends on its glyphs, so the gap must be judged against the
// line rather than against the pair. Measured live: "use" is 12px tall and "a" is
// 9px on the same 16px line, and their 6px gap is a word space either way.
func TestMergeWordRunsJoinsAShortWordOntoItsLine(t *testing.T) {
	t.Parallel()

	regions := []DetectedRegion{
		textRegion("following", image.Rect(0, 0, 70, 16)),
		textRegion("use", image.Rect(76, 4, 106, 16)),
		textRegion("a", image.Rect(112, 7, 122, 16)),
	}

	merged := mergeWordRuns(regions)

	if len(merged) != 1 {
		t.Fatalf("mergeWordRuns produced %d regions, want 1: %v", len(merged), boundsOf(merged))
	}

	if merged[0].Label != "following use a" {
		t.Errorf("run label = %q, want %q", merged[0].Label, "following use a")
	}
}

func TestMergeWordRunsJoinsAWholeRunInOnePass(t *testing.T) {
	t.Parallel()

	regions := []DetectedRegion{
		textRegion("one", image.Rect(0, 0, 30, 16)),
		textRegion("two", image.Rect(36, 0, 66, 16)),
		textRegion("three", image.Rect(72, 0, 120, 16)),
		textRegion("four", image.Rect(126, 0, 160, 16)),
	}

	merged := mergeWordRuns(regions)

	if len(merged) != 1 {
		t.Fatalf("mergeWordRuns produced %d regions, want 1: %v", len(merged), boundsOf(merged))
	}

	if merged[0].Label != "one two three four" {
		t.Errorf("run label = %q, want the words in reading order", merged[0].Label)
	}
}

func TestMergeWordRunsKeepsSeparateLinesApart(t *testing.T) {
	t.Parallel()

	// Stacked rows, horizontally identical. Only the vertical span tells them apart.
	regions := []DetectedRegion{
		textRegion("upper", image.Rect(0, 0, 60, 16)),
		textRegion("lower", image.Rect(0, 24, 60, 40)),
	}

	merged := mergeWordRuns(regions)

	if len(merged) != 2 {
		t.Fatalf("two rows merged into %d region(s): %v", len(merged), boundsOf(merged))
	}
}

func TestMergeWordRunsJoinsWordsOfUnequalHeight(t *testing.T) {
	t.Parallel()

	// "core" has no ascender, so its rect is shorter than "Config" beside it. Refusing
	// to merge on that difference would fragment ordinary prose.
	regions := []DetectedRegion{
		textRegion("Config", image.Rect(0, 0, 50, 18)),
		textRegion("core", image.Rect(56, 4, 90, 18)),
	}

	merged := mergeWordRuns(regions)

	if len(merged) != 1 {
		t.Fatalf("mergeWordRuns produced %d regions, want 1: %v", len(merged), boundsOf(merged))
	}
}

func TestMergeWordRunsLeavesNonTextRegionsAlone(t *testing.T) {
	t.Parallel()

	rectangle := DetectedRegion{Bounds: image.Rect(0, 0, 60, 18), Score: 0.9}

	regions := []DetectedRegion{
		rectangle,
		textRegion("label", image.Rect(64, 0, 110, 18)),
	}

	merged := mergeWordRuns(regions)

	if len(merged) != 2 {
		t.Fatalf("a rectangle merged with text: got %d regions %v", len(merged), boundsOf(merged))
	}

	for _, region := range merged {
		if !region.IsText && region.Bounds != rectangle.Bounds {
			t.Errorf("rectangle bounds = %v, want %v unchanged", region.Bounds, rectangle.Bounds)
		}
	}
}

func TestMergeWordRunsKeepsTheHighestScoreInTheRun(t *testing.T) {
	t.Parallel()

	regions := []DetectedRegion{
		{Bounds: image.Rect(0, 0, 30, 16), Label: "low", Score: 0.4, IsText: true},
		{Bounds: image.Rect(36, 0, 66, 16), Label: "high", Score: 0.9, IsText: true},
	}

	merged := mergeWordRuns(regions)

	if len(merged) != 1 {
		t.Fatalf("mergeWordRuns produced %d regions, want 1", len(merged))
	}

	if merged[0].Score != 0.9 {
		t.Errorf("run score = %v, want the highest word's 0.9", merged[0].Score)
	}
}

func boundsOf(regions []DetectedRegion) []image.Rectangle {
	out := make([]image.Rectangle, 0, len(regions))

	for _, region := range regions {
		out = append(out, region.Bounds)
	}

	return out
}

func labelFor(t *testing.T, regions []DetectedRegion, bounds image.Rectangle) string {
	t.Helper()

	for _, region := range regions {
		if region.Bounds == bounds {
			return region.Label
		}
	}

	t.Fatalf("no region at %v; got %v", bounds, boundsOf(regions))

	return ""
}
