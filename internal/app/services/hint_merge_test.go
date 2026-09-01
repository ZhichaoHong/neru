package services

import (
	"image"
	"maps"
	"slices"
	"testing"

	"github.com/y3owk1n/neru/internal/domain/element"
)

// The dedupe rule is the part of hybrid that will regress, so every branch of it
// has a case here, named for the screen it describes rather than the geometry.

func treeElement(t *testing.T, id string, bounds image.Rectangle) *element.Element {
	t.Helper()

	built, err := element.NewElement(element.ID(id), bounds, "AXButton")
	if err != nil {
		t.Fatalf("building tree element %q: %v", id, err)
	}

	return built
}

func visionElement(t *testing.T, id string, bounds image.Rectangle) *element.Element {
	t.Helper()

	built, err := element.NewElement(
		element.ID(id),
		bounds,
		"AXStaticText",
		element.WithVisionOnly(),
	)
	if err != nil {
		t.Fatalf("building vision element %q: %v", id, err)
	}

	return built
}

func idsOf(elements []*element.Element) []string {
	ids := make([]string, 0, len(elements))

	for _, kept := range elements {
		ids = append(ids, string(kept.ID()))
	}

	return ids
}

func TestMergeVisionWithTree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// tree and vision are merged in that order, which is also the order the
		// service appends them in.
		tree   map[string]image.Rectangle
		vision map[string]image.Rectangle
		want   []string
	}{
		{
			// The common case: a button's label is recognized inside the button.
			name:   "a recognized label inside a button is dropped",
			tree:   map[string]image.Rectangle{"button": image.Rect(100, 100, 220, 132)},
			vision: map[string]image.Rectangle{"label": image.Rect(136, 109, 184, 123)},
			want:   []string{"button"},
		},
		{
			// Containment alone would keep this: OCR read one antialiased pixel
			// past the tree element's bounds on every side.
			name:   "a label a pixel wider than its button is dropped",
			tree:   map[string]image.Rectangle{"button": image.Rect(100, 100, 160, 130)},
			vision: map[string]image.Rectangle{"label": image.Rect(99, 99, 161, 131)},
			want:   []string{"button"},
		},
		{
			// 40x20 inside 100x100: fully covered, so coverage is 1. The point of
			// the case is that union-based IOU would not answer it - 0.08 is
			// nowhere near any usable threshold - which is why the rule measures
			// coverage of the OCR rect instead.
			name:   "a small label in a large tree element is dropped",
			tree:   map[string]image.Rectangle{"panel": image.Rect(0, 0, 100, 100)},
			vision: map[string]image.Rectangle{"label": image.Rect(10, 10, 50, 30)},
			want:   []string{"panel"},
		},
		{
			// Measured off a File Explorer navigation pane, and the case that
			// forced the rule from containment to coverage. OCR reads the row's
			// text with the expander chevron attached, so the region starts 76px
			// left of the row it belongs to: not contained, and an IOU of 0.28
			// against a row three times the text's height. It was surviving as a
			// second label on a row that already had one.
			name: "a row's text read with its expander chevron is dropped",
			tree: map[string]image.Rectangle{
				"row": image.Rect(-1444, 984, -1289, 1048),
			},
			vision: map[string]image.Rectangle{
				"row_text": image.Rect(-1520, 1009, -1292, 1030),
			},
			want: []string{"row"},
		},
		{
			// A quarter of each rectangle's area overlaps, which is an IOU of
			// 1/7. Two controls next to each other, not one control read twice.
			name:   "a partly overlapping region is kept",
			tree:   map[string]image.Rectangle{"button": image.Rect(0, 0, 100, 100)},
			vision: map[string]image.Rectangle{"text": image.Rect(50, 50, 150, 150)},
			want:   []string{"button", "text"},
		},
		{
			name:   "a region nowhere near the tree is kept",
			tree:   map[string]image.Rectangle{"button": image.Rect(0, 0, 100, 100)},
			vision: map[string]image.Rectangle{"text": image.Rect(400, 400, 500, 440)},
			want:   []string{"button", "text"},
		},
		{
			// A Win32 tab strip: OCR reads the whole strip as one wide line.
			// Keeping it would put a label across the middle of the strip on top
			// of the per-tab labels.
			name: "a region spanning two tree elements is dropped",
			tree: map[string]image.Rectangle{
				"tab_one": image.Rect(0, 0, 60, 24),
				"tab_two": image.Rect(70, 0, 130, 24),
			},
			vision: map[string]image.Rectangle{"strip": image.Rect(0, 0, 488, 24)},
			want:   []string{"tab_one", "tab_two"},
		},
		{
			// The same strip measured off a Windows Terminal window. It encloses
			// nothing at all - the tab items are 48px high and the line OCR read
			// off them is 18px - so neither containment nor whole-rectangle
			// spanning sees it. Coverage answers it here (0.70 of the strip sits
			// on the first tab); the synthetic case above is the one that still
			// needs spanning.
			name: "a tab strip taller than the text OCR read off it is dropped",
			tree: map[string]image.Rectangle{
				"1_tab_one":   image.Rect(21, 64, 393, 112),
				"2_close_one": image.Rect(331, 70, 379, 106),
				"3_tab_two":   image.Rect(387, 64, 747, 112),
				"4_close_two": image.Rect(691, 70, 739, 106),
			},
			vision: map[string]image.Rectangle{"strip": image.Rect(80, 81, 527, 99)},
			want:   []string{"1_tab_one", "2_close_one", "3_tab_two", "4_close_two"},
		},
		{
			// The reason the rule says two and not one. A paragraph of text with
			// a single stray tree element inside it is exactly what hybrid was
			// added to surface.
			name: "a region spanning one tree element is kept",
			tree: map[string]image.Rectangle{"stray": image.Rect(10, 10, 40, 24)},
			vision: map[string]image.Rectangle{
				"paragraph": image.Rect(0, 0, 600, 400),
			},
			want: []string{"stray", "paragraph"},
		},
		{
			// The vision-only platforms, and the case where the tree walk failed.
			// Nothing to compare against, so nothing is dropped.
			name:   "every region survives an empty tree",
			tree:   nil,
			vision: map[string]image.Rectangle{"text": image.Rect(0, 0, 80, 20)},
			want:   []string{"text"},
		},
		{
			name:   "a tree with no recognized text is returned unchanged",
			tree:   map[string]image.Rectangle{"button": image.Rect(0, 0, 80, 20)},
			vision: nil,
			want:   []string{"button"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var elements []*element.Element

			// Sorted so a map's iteration order cannot reorder the result and
			// make the assertion flaky.
			for _, id := range slices.Sorted(maps.Keys(test.tree)) {
				elements = append(elements, treeElement(t, id, test.tree[id]))
			}

			for _, id := range slices.Sorted(maps.Keys(test.vision)) {
				elements = append(elements, visionElement(t, id, test.vision[id]))
			}

			got := idsOf(mergeVisionWithTree(elements))

			if !slices.Equal(got, test.want) {
				t.Errorf("merged to %v, want %v", got, test.want)
			}
		})
	}
}

func namedElement(
	t *testing.T,
	id, role, title string,
	bounds image.Rectangle,
) *element.Element {
	t.Helper()

	built, err := element.NewElement(
		element.ID(id),
		bounds,
		element.Role(role),
		element.WithTitle(title),
	)
	if err != nil {
		t.Fatalf("building element %q: %v", id, err)
	}

	return built
}

func TestDropDuplicateTreeElements(t *testing.T) {
	t.Parallel()

	type spec struct {
		id     string
		role   string
		title  string
		bounds image.Rectangle
	}

	tests := []struct {
		name     string
		elements []spec
		want     []string
	}{
		{
			// New Outlook, physical pixels off a 200% monitor. The host publishes
			// the three caption buttons and the WebView2 content that paints them
			// publishes them again, 12px lower.
			name: "a caption button published by two providers is hinted once",
			elements: []spec{
				{"host-min", "AXButton", "Minimize", image.Rect(-300, 118, -204, 214)},
				{"host-max", "AXButton", "Maximize", image.Rect(-204, 118, -108, 214)},
				{"host-close", "AXButton", "Close", image.Rect(-108, 118, -12, 214)},
				{"web-min", "AXButton", "Minimize", image.Rect(-300, 106, -204, 202)},
				{"web-max", "AXButton", "Maximize", image.Rect(-204, 106, -108, 202)},
				{"web-close", "AXButton", "Close", image.Rect(-108, 106, -12, 202)},
			},
			want: []string{"host-min", "host-max", "host-close"},
		},
		{
			// The three caption buttons of any ordinary window: same role, same
			// kind of name, side by side. Nothing here may be dropped.
			name: "buttons beside each other all survive",
			elements: []spec{
				{"min", "AXButton", "Minimize", image.Rect(0, 0, 48, 32)},
				{"max", "AXButton", "Maximize", image.Rect(48, 0, 96, 32)},
				{"close", "AXButton", "Close", image.Rect(96, 0, 144, 32)},
			},
			want: []string{"min", "max", "close"},
		},
		{
			// Two toolbar buttons named the same in different places - a "More"
			// overflow in each of two panes.
			name: "the same name in two places survives",
			elements: []spec{
				{"left", "AXButton", "More", image.Rect(0, 0, 32, 32)},
				{"right", "AXButton", "More", image.Rect(900, 0, 932, 32)},
			},
			want: []string{"left", "right"},
		},
		{
			// A row and the button inside it can carry the same name. The row's
			// center is outside the button, so the pair is not a duplicate.
			name: "a wrapper around a control survives",
			elements: []spec{
				{"row", "AXButton", "Inbox", image.Rect(0, 0, 400, 40)},
				{"icon", "AXButton", "Inbox", image.Rect(8, 8, 40, 32)},
			},
			want: []string{"row", "icon"},
		},
		{
			// Unnamed elements are where Win32 trees legitimately stack, so the
			// rule stays out of them even when the bounds coincide exactly.
			name: "unnamed elements on one spot survive",
			elements: []spec{
				{"pane", "AXGroup", "", image.Rect(0, 0, 200, 40)},
				{"custom", "AXGroup", "", image.Rect(0, 0, 200, 40)},
			},
			want: []string{"pane", "custom"},
		},
		{
			// Same place, same name, different roles: an editable combo box whose
			// text field carries the field's name.
			name: "a different role on one spot survives",
			elements: []spec{
				{"combo", "AXComboBox", "Search", image.Rect(0, 0, 200, 40)},
				{"field", "AXTextField", "Search", image.Rect(0, 0, 200, 40)},
			},
			want: []string{"combo", "field"},
		},
		{
			// Three providers over one control collapse to the first, not to two.
			name: "a third copy collapses into the same survivor",
			elements: []spec{
				{"first", "AXButton", "Close", image.Rect(0, 0, 96, 96)},
				{"second", "AXButton", "Close", image.Rect(0, 12, 96, 108)},
				{"third", "AXButton", "Close", image.Rect(4, 6, 100, 102)},
			},
			want: []string{"first"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			elements := make([]*element.Element, 0, len(test.elements))
			for _, built := range test.elements {
				elements = append(
					elements,
					namedElement(t, built.id, built.role, built.title, built.bounds),
				)
			}

			got := idsOf(dropDuplicateTreeElements(elements))

			if !slices.Equal(got, test.want) {
				t.Errorf("kept %v, want %v", got, test.want)
			}
		})
	}
}

// TestMergeVisionWithTreeNeverDropsATreeElement is the asymmetry stated as its
// own test. Whatever OCR reads over a tree element, the tree element survives -
// it is the one carrying a real role and title, which --filter-role and hint
// search read.
func TestMergeVisionWithTreeNeverDropsATreeElement(t *testing.T) {
	t.Parallel()

	bounds := image.Rect(0, 0, 200, 40)

	merged := mergeVisionWithTree([]*element.Element{
		treeElement(t, "button", bounds),
		visionElement(t, "identical", bounds),
	})

	if got := idsOf(merged); !slices.Equal(got, []string{"button"}) {
		t.Errorf("merged to %v, want the tree element alone", got)
	}
}
