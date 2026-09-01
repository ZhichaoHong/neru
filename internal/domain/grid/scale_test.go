package grid_test

import (
	"image"
	"math"
	"sort"
	"testing"

	"github.com/y3owk1n/neru/internal/adapter/logger"
	"github.com/y3owk1n/neru/internal/domain/grid"
)

// coordinates is the grid's plan in the form a test can compare: two grids with
// the same columns and rows label their cells identically, whatever pixel
// rectangles those cells cover.
func coordinates(g *grid.Grid) []string {
	cells := g.AllCells()
	out := make([]string, 0, len(cells))

	for _, cell := range cells {
		out = append(out, cell.Coordinate())
	}

	sort.Strings(out)

	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

// TestNewGridAtScale_PlansByLogicalExtent is the point of the scale parameter: a
// monitor at 150% has to plan the grid its logical extent asks for, not the
// denser one its pixel count would.
func TestNewGridAtScale_PlansByLogicalExtent(t *testing.T) {
	t.Parallel()

	log := logger.Get()

	tests := []struct {
		name    string
		bounds  image.Rectangle
		scale   float64
		logical image.Rectangle
	}{
		{
			name:    "4K at 150%",
			bounds:  image.Rect(0, 0, 3840, 2160),
			scale:   1.5,
			logical: image.Rect(0, 0, 2560, 1440),
		},
		{
			name:    "4K at 200%",
			bounds:  image.Rect(0, 0, 3840, 2160),
			scale:   2.0,
			logical: image.Rect(0, 0, 1920, 1080),
		},
		{
			name:    "1440p at 125%",
			bounds:  image.Rect(0, 0, 2560, 1440),
			scale:   1.25,
			logical: image.Rect(0, 0, 2048, 1152),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			scaled := grid.NewGridAtScale(allLetters, "", "", testCase.bounds, testCase.scale, log)
			unscaled := grid.NewGridAtScale(allLetters, "", "", testCase.logical, 1, log)

			if !equalStrings(coordinates(scaled), coordinates(unscaled)) {
				t.Errorf(
					"%v at %v planned %d cells, %v at 1 planned %d",
					testCase.bounds.Size(), testCase.scale, len(scaled.AllCells()),
					testCase.logical.Size(), len(unscaled.AllCells()),
				)
			}

			// The cells are still cut from the physical bounds, so the grid
			// covers the same glass either way.
			if got := scaled.Bounds(); got != testCase.bounds {
				t.Errorf("bounds = %v, want %v", got, testCase.bounds)
			}
		})
	}
}

// TestNewGridAtScale_ScaleOneIsUnchanged pins the pre-parameter behavior: the
// two-argument constructor and scale 1 have to plan the same grid, or every
// platform that reports logical coordinates changed underneath.
func TestNewGridAtScale_ScaleOneIsUnchanged(t *testing.T) {
	t.Parallel()

	log := logger.Get()

	for _, bounds := range []image.Rectangle{
		image.Rect(0, 0, 1920, 1080),
		image.Rect(0, 0, 3840, 2160),
		image.Rect(0, 0, 100, 100),
	} {
		legacy := grid.NewGridWithLabels(allLetters, "", "", bounds, log)
		explicit := grid.NewGridAtScale(allLetters, "", "", bounds, 1, log)

		if !equalStrings(coordinates(legacy), coordinates(explicit)) {
			t.Errorf(
				"%v: NewGridWithLabels planned %d cells, scale 1 planned %d",
				bounds.Size(), len(legacy.AllCells()), len(explicit.AllCells()),
			)
		}
	}
}

// TestNewGridAtScale_UnusableScaleFallsBackToOne covers what a platform hands
// over when it cannot answer. Below 1 is not a coordinate space this
// understands, and 0 would divide by zero.
func TestNewGridAtScale_UnusableScaleFallsBackToOne(t *testing.T) {
	t.Parallel()

	log := logger.Get()
	bounds := image.Rect(0, 0, 1920, 1080)
	want := coordinates(grid.NewGridAtScale(allLetters, "", "", bounds, 1, log))

	for _, scale := range []float64{0, -1, 0.5, math.NaN()} {
		got := grid.NewGridAtScale(allLetters, "", "", bounds, scale, log)

		if got.Scale() != 1 {
			t.Errorf("scale %v reported as %v, want 1", scale, got.Scale())
		}

		if !equalStrings(coordinates(got), want) {
			t.Errorf("scale %v planned a different grid than scale 1", scale)
		}
	}
}

// TestGrid_ScaleIsRemembered is what the config-reload rebuild path reads: it
// has the old grid but not the port that answered for the scale.
func TestGrid_ScaleIsRemembered(t *testing.T) {
	t.Parallel()

	log := logger.Get()

	scaled := grid.NewGridAtScale(
		allLetters, "", "", image.Rect(0, 0, 3840, 2160), 1.5, log,
	)
	if got := scaled.Scale(); got != 1.5 {
		t.Errorf("Scale() = %v, want 1.5", got)
	}

	if got := grid.NewGrid(allLetters, image.Rect(0, 0, 1920, 1080), log).Scale(); got != 1 {
		t.Errorf("unscaled constructor Scale() = %v, want 1", got)
	}
}
