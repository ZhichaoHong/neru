package recursivegrid

import (
	"image"
	"math"
	"testing"

	"github.com/y3owk1n/neru/internal/adapter/overlay/render/badge"
	"github.com/y3owk1n/neru/internal/config"
)

// labelFontSizeTolerance is how far a fitted size may land from the expected
// one: the fit divides, so a size that is whole on paper comes back a few bits
// off it.
const labelFontSizeTolerance = 1e-9

type mockThemeProvider struct {
	darkMode bool
}

func (m *mockThemeProvider) IsDarkMode() bool {
	return m.darkMode
}

// TestBuildStyle_ResolvesThemeColors pins that each color comes from the
// configured value for the active theme.
//
// The style is one type on every platform, so this runs in every job rather
// than only where a particular backend is built.
func TestBuildStyle_ResolvesThemeColors(t *testing.T) {
	cfg := config.DefaultConfig().RecursiveGrid

	tests := []struct {
		name            string
		dark            bool
		highlight       string
		labelBackground string
		previewText     string
	}{
		{
			name:            "light theme",
			dark:            false,
			highlight:       config.RecursiveGridHighlightColorLight,
			labelBackground: config.RecursiveGridLabelBackgroundColorLight,
			previewText:     config.RecursiveGridSubKeyPreviewTextColorLight,
		},
		{
			name:            "dark theme",
			dark:            true,
			highlight:       config.RecursiveGridHighlightColorDark,
			labelBackground: config.RecursiveGridLabelBackgroundColorDark,
			previewText:     config.RecursiveGridSubKeyPreviewTextColorDark,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			style := BuildStyle(cfg, &mockThemeProvider{darkMode: testCase.dark})

			if got := style.HighlightColor(); got != testCase.highlight {
				t.Errorf("HighlightColor() = %q, want %q", got, testCase.highlight)
			}

			if got := style.LabelBackgroundColor(); got != testCase.labelBackground {
				t.Errorf("LabelBackgroundColor() = %q, want %q", got, testCase.labelBackground)
			}

			if got := style.SubKeyPreviewTextColor(); got != testCase.previewText {
				t.Errorf("SubKeyPreviewTextColor() = %q, want %q", got, testCase.previewText)
			}
		})
	}
}

// TestBuildStyle_CarriesTheToggles pins that the boolean options come from the
// configuration rather than from a hard-coded default.
//
// It sets them explicitly rather than reading DefaultConfig, whose values for
// these two differ by platform.
func TestBuildStyle_CarriesTheToggles(t *testing.T) {
	for _, want := range []bool{true, false} {
		cfg := config.DefaultConfig().RecursiveGrid
		cfg.UI.LabelBackground = want
		cfg.UI.SubKeyPreview = want

		style := BuildStyle(cfg, &mockThemeProvider{})

		if got := style.LabelBackground(); got != want {
			t.Errorf("LabelBackground() = %v, want %v", got, want)
		}

		if got := style.SubKeyPreview(); got != want {
			t.Errorf("SubKeyPreview() = %v, want %v", got, want)
		}
	}
}

// TestStyle_ShowLabelIn pins the label autohide threshold every backend draws
// by: label_autohide_multiplier x minLabelFontSize x the monitor scale, compared
// against both cell dimensions, with a non-positive multiplier meaning "always
// show".
//
// The floor and not the configured font size, because LabelFontSizeIn shrinks a
// label to its cell: the configured size says nothing about whether a label will
// fit, so measuring it hid labels the fit had already made fit.
//
// The Cairo and GDI backends both call this, so the cases run in every job
// rather than only where a particular backend is built.
func TestStyle_ShowLabelIn(t *testing.T) {
	tests := []struct {
		name       string
		cell       image.Rectangle
		fontSize   int
		multiplier float64
		scale      float64
		want       bool
	}{
		{
			name:       "zero multiplier always shows",
			cell:       image.Rect(0, 0, 1, 1),
			fontSize:   100,
			multiplier: 0,
			scale:      1,
			want:       true,
		},
		{
			name:       "negative multiplier always shows",
			cell:       image.Rect(0, 0, 1, 1),
			fontSize:   100,
			multiplier: -2,
			scale:      1,
			want:       true,
		},
		{
			name:       "cell clears the threshold on both axes",
			cell:       image.Rect(0, 0, 30, 30),
			fontSize:   10,
			multiplier: 2, // threshold 12
			scale:      1,
			want:       true,
		},
		{
			name:       "cell exactly on the threshold shows",
			cell:       image.Rect(0, 0, 12, 12),
			fontSize:   10,
			multiplier: 2, // threshold 12
			scale:      1,
			want:       true,
		},
		{
			name:       "cell below the threshold hides",
			cell:       image.Rect(0, 0, 8, 8),
			fontSize:   20,
			multiplier: 2, // threshold 12
			scale:      1,
			want:       false,
		},
		{
			name:       "narrow cell hides even when tall enough",
			cell:       image.Rect(0, 0, 11, 100),
			fontSize:   10,
			multiplier: 2, // threshold 12
			scale:      1,
			want:       false,
		},
		{
			name:       "short cell hides even when wide enough",
			cell:       image.Rect(0, 0, 100, 11),
			fontSize:   10,
			multiplier: 2, // threshold 12
			scale:      1,
			want:       false,
		},
		{
			name:       "offset cell is measured by its size, not its position",
			cell:       image.Rect(500, 700, 530, 730),
			fontSize:   10,
			multiplier: 2, // threshold 12
			scale:      1,
			want:       true,
		},
		{
			// The report that moved the anchor to the floor: a 5x5 grid two
			// levels deep on a 4K screen, whose cells an 18 pt label clears on
			// neither axis even though it draws at 8 pt there and fills them.
			name:       "a configured size the cell cannot hold does not hide the label",
			cell:       image.Rect(0, 0, 30, 17),
			fontSize:   18,
			multiplier: 1.5, // threshold 9, not 27
			scale:      1,
			want:       true,
		},
		{
			name:       "the scale raises the threshold, since the cell arrives scaled",
			cell:       image.Rect(0, 0, 10, 10),
			fontSize:   10,
			multiplier: 1.5, // threshold 18
			scale:      2,
			want:       false,
		},
		{
			name:       "the same cell clears the unscaled threshold",
			cell:       image.Rect(0, 0, 10, 10),
			fontSize:   10,
			multiplier: 1.5, // threshold 9
			scale:      1,
			want:       true,
		},
		{
			name:       "a non-positive scale is read as 1",
			cell:       image.Rect(0, 0, 10, 10),
			fontSize:   10,
			multiplier: 1.5, // threshold 9
			scale:      0,
			want:       true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			style := NewStyle(StyleOptions{
				FontSize:                testCase.fontSize,
				LabelAutohideMultiplier: testCase.multiplier,
			})

			got := style.ShowLabelIn(testCase.cell, testCase.scale)
			if got != testCase.want {
				t.Errorf(
					"ShowLabelIn(%v, %v) = %v, want %v",
					testCase.cell, testCase.scale, got, testCase.want,
				)
			}
		})
	}
}

// TestStyle_LabelFontSizeIn pins the size every backend draws a cell's label at:
// the configured size until the label stops fitting the cell, then whatever does
// fit, and never below the floor.
//
// The case that started this: a 5x5 recursive grid two levels deep on a 1080p
// screen leaves cells around 15x8, and a 10 pt label drawn in one of those is
// clipped. Fitted, it comes back at about 6 pt and reads.
func TestStyle_LabelFontSizeIn(t *testing.T) {
	tests := []struct {
		name     string
		label    string
		cell     image.Rectangle
		fontSize int
		scale    float64
		want     float64
	}{
		{
			name:     "a cell with room keeps the configured size",
			label:    "A",
			cell:     image.Rect(0, 0, 400, 400),
			fontSize: 10,
			scale:    1,
			want:     10,
		},
		{
			name:     "a short cell shrinks the label",
			label:    "A",
			cell:     image.Rect(0, 0, 400, 11),
			fontSize: 10,
			scale:    1,
			want:     11 / 1.4,
		},
		{
			name:     "a narrow cell shrinks the label",
			label:    "A",
			cell:     image.Rect(0, 0, 5, 400),
			fontSize: 10,
			scale:    1,
			want:     5 / 0.7,
		},
		{
			name:     "the deepest layer of a 5x5 grid on a 1080p screen",
			label:    "A",
			cell:     image.Rect(0, 0, 15, 8),
			fontSize: 10,
			scale:    1,
			// 8 / 1.4 is 5.7, under the floor.
			want: minLabelFontSize,
		},
		{
			name:     "a cell too small for the floor gets the floor",
			label:    "A",
			cell:     image.Rect(0, 0, 3, 3),
			fontSize: 10,
			scale:    1,
			want:     minLabelFontSize,
		},
		{
			name:     "a scaled backend measures its device pixels as fewer points",
			label:    "A",
			cell:     image.Rect(0, 0, 400, 22),
			fontSize: 100,
			scale:    2,
			want:     22 / 1.4 / 2,
		},
		{
			name:     "an offset cell is measured by its size, not its position",
			label:    "A",
			cell:     image.Rect(500, 700, 515, 708),
			fontSize: 10,
			scale:    1,
			// 8 / 1.4 is 5.7, under the floor.
			want: minLabelFontSize,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			style := NewStyle(StyleOptions{FontSize: testCase.fontSize})

			got := style.LabelFontSizeIn(testCase.label, testCase.cell, testCase.scale)
			if math.Abs(got-testCase.want) > labelFontSizeTolerance {
				t.Errorf(
					"LabelFontSizeIn(%q, %v, %v) = %v, want %v",
					testCase.label, testCase.cell, testCase.scale, got, testCase.want,
				)
			}
		})
	}
}

// TestStyle_LabelFontSizeInNeverExceedsTheConfiguredSize pins the direction the
// fit runs in. font_size is what the user asked for, and the fit exists to give
// that up when a cell cannot hold it, never to hand back something larger
// because a cell had room to spare.
func TestStyle_LabelFontSizeInNeverExceedsTheConfiguredSize(t *testing.T) {
	style := NewStyle(StyleOptions{FontSize: 10})

	for width := 1; width <= 200; width++ {
		for height := 1; height <= 200; height++ {
			cell := image.Rect(0, 0, width, height)

			if got := style.LabelFontSizeIn("A", cell, 1); got > style.LabelFontSize() {
				t.Fatalf(
					"LabelFontSizeIn(\"A\", %v, 1) = %v, larger than the configured %v",
					cell, got, style.LabelFontSize(),
				)
			}
		}
	}
}

// TestStyle_ARGBAccessorsMatchTheHexValues pins the conversion the Cairo and GDI
// backends rely on: the packed form of a color must be the packed form of the
// hex string beside it.
func TestStyle_ARGBAccessorsMatchTheHexValues(t *testing.T) {
	style := BuildStyle(config.DefaultConfig().RecursiveGrid, &mockThemeProvider{})

	pairs := []struct {
		name string
		hex  string
		argb uint32
	}{
		{"line", style.LineColor(), style.LineColorARGB()},
		{"highlight", style.HighlightColor(), style.HighlightColorARGB()},
		{"text", style.TextColor(), style.TextColorARGB()},
		{"labelBackground", style.LabelBackgroundColor(), style.LabelBackgroundColorARGB()},
		{"previewText", style.SubKeyPreviewTextColor(), style.SubKeyPreviewTextColorARGB()},
	}

	for _, pair := range pairs {
		if want := badge.ParseHexARGB(pair.hex); pair.argb != want {
			t.Errorf("%s ARGB = %#08x, want %#08x (from %q)", pair.name, pair.argb, want, pair.hex)
		}
	}
}
