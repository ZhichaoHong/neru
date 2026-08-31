//go:build integration && windows

package windows_test

import (
	"context"
	"image"
	"testing"

	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
)

// The coordinate contract of a GDI capture, which the vision adapter depends on
// twice over: the caller keeps the region it asked for and adds region.Min back
// to every rectangle OCR reports, and the frame itself must be top-down.
//
// Both are checked with one comparison. A wide capture from the primary
// monitor's origin is the reference; a smaller one taken at an offset inside it
// has to match the reference's sub-rectangle pixel for pixel. An offset dropped
// on the way to BitBlt fails this, and so does a bottom-up DIB: the mirrored
// crop carries content from the other end of the frame.
//
// Run on an interactive desktop session:
//
//	go test -tags=integration -run Capture ./internal/adapter/platform/windows/
const (
	referenceWidth  = 320
	referenceHeight = 240

	offsetX = 80
	offsetY = 60

	regionWidth  = 200
	regionHeight = 150

	// matchThreshold allows for what moved on screen between the two grabs - a
	// caret, a clock, a cursor. An offset or orientation bug is nowhere near
	// this close.
	matchThreshold = 0.99
)

func TestCaptureScreenRegionPlacesTheFrameWhereItWasAskedFor(t *testing.T) {
	ctx := context.Background()

	reference, err := winplatform.CaptureScreenRegion(
		ctx,
		image.Rect(0, 0, referenceWidth, referenceHeight),
	)
	if err != nil {
		t.Fatalf("capturing the reference region: %v", err)
	}

	region, err := winplatform.CaptureScreenRegion(
		ctx,
		image.Rect(offsetX, offsetY, offsetX+regionWidth, offsetY+regionHeight),
	)
	if err != nil {
		t.Fatalf("capturing the offset region: %v", err)
	}

	want := image.Rect(0, 0, regionWidth, regionHeight)
	if region.Rect != want {
		t.Errorf(
			"a capture at (%d, %d) came back as %v, want %v: the frame is based at "+
				"its own origin and the caller places it",
			offsetX, offsetY, region.Rect, want,
		)
	}

	if colorsIn(reference) < 2 {
		t.Skip("the reference region is a single flat color, so nothing here can be told apart")
	}

	var matched int

	for y := range regionHeight {
		for x := range regionWidth {
			if region.RGBAAt(x, y) == reference.RGBAAt(x+offsetX, y+offsetY) {
				matched++
			}
		}
	}

	total := regionWidth * regionHeight
	if share := float64(matched) / float64(total); share < matchThreshold {
		t.Errorf(
			"only %.1f%% of the offset capture matches the same rectangle of the "+
				"reference; the region origin or the DIB orientation is wrong",
			share*100,
		)
	}
}

func colorsIn(img *image.RGBA) int {
	seen := make(map[uint32]struct{})

	bounds := img.Bounds()

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := img.RGBAAt(x, y)
			seen[uint32(pixel.R)<<16|uint32(pixel.G)<<8|uint32(pixel.B)] = struct{}{}

			if len(seen) > 1 {
				return len(seen)
			}
		}
	}

	return len(seen)
}
