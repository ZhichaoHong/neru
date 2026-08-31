//go:build windows

package windows

import (
	"context"
	"image"
	"unsafe"

	"github.com/y3owk1n/neru/internal/derrors"
)

// Screen capture through GDI: BitBlt the display into a memory bitmap, then
// GetDIBits it into a Go buffer.
//
// GDI rather than Windows.Graphics.Capture, even though the OCR half is already
// WinRT: WGC needs a DirectX device and a frame pool to read one still frame,
// and on the Windows versions that predate the 2021 "no yellow border" flag it
// draws a capture border over the screen it is reading. BitBlt is synchronous,
// needs no device, and is what the overlay-exclusion integration test verified
// WDA_EXCLUDEFROMCAPTURE holds against.
//
// The captured pixels are screen content. They are never logged, never written
// to disk, and never held past the detection that asked for them.

var (
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

const (
	// srcCopy is SRCCOPY. CAPTUREBLT is deliberately absent: it pulls layered
	// windows into the grab, which is what neru's own overlays are, and it also
	// makes the whole screen flicker on some drivers.
	srcCopy = 0x00CC0020

	biRGB        = 0
	dibRGBColors = 0
)

// bitmapInfoHeader is BITMAPINFOHEADER. overlay.go carries a V4 header because
// it needs the color masks for per-pixel alpha; a plain read-back does not.
type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// CaptureScreenRegion grabs one rectangle of the virtual desktop.
//
// region is in virtual-desktop physical pixels, so a monitor left of or above
// the primary has a negative origin and that is legal. The returned image is
// based at (0, 0) regardless: the caller keeps region and adds region.Min back
// to whatever coordinates it reads out of the frame. No DPI conversion happens
// anywhere in here, because BitBlt works in the same physical pixels the rest of
// the Windows adapter reports.
//
// An empty region means the whole virtual desktop.
func CaptureScreenRegion(ctx context.Context, region image.Rectangle) (*image.RGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if region.Empty() {
		bounds, err := virtualDesktopBounds()
		if err != nil {
			return nil, err
		}

		region = bounds
	}

	region = region.Canon()

	width, height := region.Dx(), region.Dy()
	if width <= 0 || height <= 0 {
		return nil, derrors.Newf(
			derrors.CodeInvalidInput,
			"cannot capture the empty region %v",
			region,
		)
	}

	screenDC, _, err := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, derrors.Wrap(
			err,
			derrors.CodeActionFailed,
			"the screen could not be opened for capture",
		)
	}

	defer procReleaseDC.Call(0, screenDC)

	memDC, _, err := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, derrors.Wrap(
			err,
			derrors.CodeActionFailed,
			"a memory device context for the capture could not be created",
		)
	}

	defer procDeleteDC.Call(memDC)

	// Sized from the requested region, never from GetDeviceCaps: the caps of a
	// display DC describe the primary monitor no matter which DC they are read
	// from, so a secondary monitor would be captured at the wrong size.
	bitmap, _, err := procCreateCompatibleBitmap.Call(
		screenDC,
		uintptr(width),
		uintptr(height),
	)
	if bitmap == 0 {
		return nil, derrors.Wrapf(
			err,
			derrors.CodeActionFailed,
			"a %dx%d capture bitmap could not be created",
			width, height,
		)
	}

	defer procDeleteObject.Call(bitmap)

	previous, _, _ := procSelectObject.Call(memDC, bitmap)
	if previous != 0 {
		defer procSelectObject.Call(memDC, previous)
	}

	// A negative origin sign-extends into uintptr, and BitBlt reads the low 32
	// bits back as a signed int, so a monitor left of or above the primary needs
	// no masking here. Worth saying because it reads like a bug.
	blitted, _, err := procBitBlt.Call(
		memDC, 0, 0, uintptr(width), uintptr(height),
		screenDC, uintptr(region.Min.X), uintptr(region.Min.Y),
		srcCopy,
	)

	if err := win32Bool(blitted, err); err != nil {
		return nil, derrors.Wrapf(
			err,
			derrors.CodeActionFailed,
			"the screen region %v could not be copied",
			region,
		)
	}

	header := bitmapInfoHeader{
		Size:  uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width: int32(width),
		// Negative height asks for a top-down DIB. A positive one is bottom-up,
		// which would hand OCR a vertically mirrored frame - and OCR would still
		// find text in it, so the bug would surface as hints in the wrong place
		// rather than as no hints at all.
		Height:      int32(-height),
		Planes:      1,
		BitCount:    dibBitCount,
		Compression: biRGB,
	}

	pixels := make([]byte, width*height*bytesPerPixel)

	scanned, _, err := procGetDIBits.Call(
		memDC,
		bitmap,
		0,
		uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&header)),
		dibRGBColors,
	)
	if scanned == 0 {
		return nil, derrors.Wrap(
			err,
			derrors.CodeActionFailed,
			"the captured frame could not be read back from the graphics device",
		)
	}

	if int(scanned) != height {
		return nil, derrors.Newf(
			derrors.CodeActionFailed,
			"the capture read back %d of %d scanlines",
			int(scanned), height,
		)
	}

	bgraToRGBA(pixels)

	return &image.RGBA{
		Pix:    pixels,
		Stride: width * bytesPerPixel,
		Rect:   image.Rect(0, 0, width, height),
	}, nil
}

// bgraToRGBA swaps the red and blue channels in place and forces alpha opaque.
//
// GDI always hands back BGRX for BI_RGB at 32 bpp and the X byte is undefined,
// so alpha is written rather than trusted - an *image.RGBA with garbage alpha
// composites to nothing.
//
// The swap costs about 8 ms on a 4K frame and cannot be avoided by asking the
// OCR engine for Bgra8 instead: CaptureScreenRegion's contract is an
// *image.RGBA, VisionPort.CaptureScreen hands that same buffer to callers that
// are not OCR, and a platform that quietly returned BGRA in an RGBA would be a
// lie that only shows up as inverted colors in whatever reads it next.
func bgraToRGBA(pixels []byte) {
	for i := 0; i+bytesPerPixel <= len(pixels); i += bytesPerPixel {
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
		pixels[i+3] = 0xFF
	}
}

// virtualDesktopBounds is the union of every monitor, which is the rectangle
// BitBlt treats as the whole screen.
//
// The monitors are enumerated rather than read from GetSystemMetrics because
// enumeration is what the rest of this adapter already uses, so a capture of
// "the screen" covers exactly the monitors neru is willing to draw hints on.
func virtualDesktopBounds() (image.Rectangle, error) {
	monitors, err := enumerateMonitors()
	if err != nil {
		return image.Rectangle{}, err
	}

	var bounds image.Rectangle

	for _, monitor := range monitors {
		bounds = bounds.Union(monitor.bounds)
	}

	if bounds.Empty() {
		return image.Rectangle{}, derrors.New(
			derrors.CodeActionFailed,
			"the monitors reported no area to capture",
		)
	}

	return bounds, nil
}
