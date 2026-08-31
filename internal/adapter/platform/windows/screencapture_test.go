//go:build windows

package windows

import (
	"context"
	"image"
	"testing"
)

func TestBgraToRGBASwapsRedAndBlueAndForcesAlpha(t *testing.T) {
	t.Parallel()

	// Two pixels, each with a distinct blue and red so a no-op swap is visible,
	// and an alpha byte GDI left undefined.
	pixels := []byte{
		0x10, 0x20, 0x30, 0x00,
		0x40, 0x50, 0x60, 0x7F,
	}

	bgraToRGBA(pixels)

	want := []byte{
		0x30, 0x20, 0x10, 0xFF,
		0x60, 0x50, 0x40, 0xFF,
	}

	for i := range want {
		if pixels[i] != want[i] {
			t.Fatalf("bgraToRGBA produced %v, want %v", pixels, want)
		}
	}
}

// TestBgraToRGBALeavesAPartialPixelAlone pins the loop bound. A buffer that is
// not a whole number of pixels cannot come out of GetDIBits, but the swizzle
// walks it four bytes at a time and reading past the end would panic in the
// capture path rather than in a test.
func TestBgraToRGBALeavesAPartialPixelAlone(t *testing.T) {
	t.Parallel()

	pixels := []byte{0x10, 0x20, 0x30, 0x00, 0xAA, 0xBB}

	bgraToRGBA(pixels)

	if pixels[4] != 0xAA || pixels[5] != 0xBB {
		t.Errorf("the trailing partial pixel became %v, want it untouched", pixels[4:])
	}
}

func TestCaptureScreenRegionHonorsACanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	img, err := CaptureScreenRegion(ctx, image.Rect(0, 0, 8, 8))
	if err == nil {
		t.Fatal("CaptureScreenRegion on a canceled context returned no error")
	}

	if img != nil {
		t.Error("CaptureScreenRegion returned a frame for a canceled context")
	}
}
