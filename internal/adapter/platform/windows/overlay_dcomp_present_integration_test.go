//go:build integration && windows && amd64

package windows

import (
	"testing"
	"unsafe"
)

// Test-only Direct2D constants for reading a target bitmap back on the CPU.
const (
	d2dBitmapOptionsCPURead = 0x4
	d2dMapOptionsRead       = 1

	vtblD2DBitmapCopyFromBitmap = 8
	vtblD2DBitmap1Map           = 14
	vtblD2DBitmap1Unmap         = 15
)

type d2dMappedRect struct {
	Pitch uint32
	Bits  *byte
}

// The canvas-to-back-buffer copy has to replace what the back buffer holds. A
// flip-model swapchain hands back a buffer that still carries an older frame,
// so a copy that blends instead of replacing leaves that frame on screen:
// activating hints drew the recursive grid underneath them, and either mode
// showed the other. Nothing here is visible to a unit test, so pin the one
// property the copy must have.
func TestPresentCopyReplacesTheDestination(t *testing.T) {
	if !dcompAvailable() {
		t.Skipf("DirectComposition unavailable in this session: %v", errDComp)
	}

	overlay, err := NewOverlayWindow()
	if err != nil {
		t.Fatalf("NewOverlayWindow: %v", err)
	}

	defer overlay.Destroy()

	surface, ok := overlay.surface.(*dcompSurface)
	if !ok {
		t.Fatalf("surface is %T, want *dcompSurface", overlay.surface)
	}

	const side = 8

	// Stands in for the back buffer, holding an opaque frame.
	backBuf := newReadableBitmap(t, surface, side, d2dBitmapOptionsTarget)
	defer backBuf.release()

	readback := newReadableBitmap(t, surface, side,
		d2dBitmapOptionsCannotDraw|d2dBitmapOptionsCPURead)
	defer readback.release()

	clearBitmap(t, surface, backBuf, d2dColorF{R: 1, A: 1})
	clearBitmap(t, surface, surface.canvas, d2dColorF{})

	surface.context.call(vtblD2DDCSetTarget, objArg(backBuf))
	surface.context.call(vtblD2DRTBeginDraw)
	surface.context.call(vtblD2DDCDrawImage,
		objArg(surface.canvas), 0, 0, 0, d2dCompositeModeSourceCopy)

	err = surface.context.hresult("ID2D1RenderTarget::EndDraw", vtblD2DRTEndDraw, 0, 0)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	surface.context.call(vtblD2DDCSetTarget, 0)

	pixel := firstPixel(t, readback, backBuf)
	if pixel[3] != 0 {
		t.Fatalf("pixel = %v after copying an empty canvas over an opaque frame, "+
			"want fully transparent: the copy blended with the destination "+
			"instead of replacing it", pixel)
	}
}

func newReadableBitmap(t *testing.T, surface *dcompSurface, side int, options uint32) comObject {
	t.Helper()

	props := d2dBitmapProperties1{
		Format:    dxgiFormatB8G8R8A8UNorm,
		AlphaMode: d2dAlphaModePremultiplied,
		DpiX:      d2dDefaultDPI,
		DpiY:      d2dDefaultDPI,
		Options:   options,
	}

	var bitmap comObject

	err := surface.context.hresult(
		"ID2D1DeviceContext::CreateBitmap",
		vtblD2DDCCreateBitmap,
		packedSizeU(side, side),
		0,
		0,
		ptrArg(unsafe.Pointer(&props)),
		outArg(&bitmap),
	)
	if err != nil {
		t.Fatalf("CreateBitmap(options=%#x): %v", options, err)
	}

	return bitmap
}

func clearBitmap(t *testing.T, surface *dcompSurface, target comObject, color d2dColorF) {
	t.Helper()

	surface.context.call(vtblD2DDCSetTarget, objArg(target))
	surface.context.call(vtblD2DRTBeginDraw)
	surface.context.call(vtblD2DRTClear, ptrArg(unsafe.Pointer(&color)))

	err := surface.context.hresult("ID2D1RenderTarget::EndDraw", vtblD2DRTEndDraw, 0, 0)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
}

// firstPixel copies source onto a CPU-readable bitmap and returns its top-left
// pixel as BGRA.
func firstPixel(t *testing.T, readback, source comObject) [4]byte {
	t.Helper()

	err := readback.hresult(
		"ID2D1Bitmap::CopyFromBitmap", vtblD2DBitmapCopyFromBitmap,
		0, objArg(source), 0)
	if err != nil {
		t.Fatalf("CopyFromBitmap: %v", err)
	}

	var mapped d2dMappedRect

	err = readback.hresult(
		"ID2D1Bitmap1::Map", vtblD2DBitmap1Map,
		d2dMapOptionsRead, ptrArg(unsafe.Pointer(&mapped)))
	if err != nil {
		t.Fatalf("Map: %v", err)
	}

	defer readback.call(vtblD2DBitmap1Unmap)

	if mapped.Bits == nil {
		t.Fatal("Map returned no bits")
	}

	return [4]byte(unsafe.Slice(mapped.Bits, 4))
}
