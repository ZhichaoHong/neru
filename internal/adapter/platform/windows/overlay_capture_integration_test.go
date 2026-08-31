//go:build integration && windows

package windows_test

import (
	"image"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
)

// Whether WDA_EXCLUDEFROMCAPTURE keeps an overlay out of a GDI screen grab, which
// is the question the capture backend depends on: the Windows vision path
// BitBlts the display DC, and an overlay that survives that grab is OCRed as UI
// text and the feature reads its own hint labels back.
//
// Documented behavior is that the affinity applies to every capture path, not
// only to Windows.Graphics.Capture. That is worth verifying rather than trusting,
// because the failure mode is silent and only visible in the OCR results.
//
// Run on an interactive desktop session:
//
//	go test -tags=integration -run Capture ./internal/adapter/platform/windows/
var (
	gdi32Capture  = windows.NewLazySystemDLL("gdi32.dll")
	user32Capture = windows.NewLazySystemDLL("user32.dll")

	procCaptureGetDC               = user32Capture.NewProc("GetDC")
	procCaptureReleaseDC           = user32Capture.NewProc("ReleaseDC")
	procCaptureCreateCompatibleDC  = gdi32Capture.NewProc("CreateCompatibleDC")
	procCaptureCreateCompatibleBmp = gdi32Capture.NewProc("CreateCompatibleBitmap")
	procCaptureSelectObject        = gdi32Capture.NewProc("SelectObject")
	procCaptureBitBlt              = gdi32Capture.NewProc("BitBlt")
	procCaptureGetDIBits           = gdi32Capture.NewProc("GetDIBits")
	procCaptureDeleteObject        = gdi32Capture.NewProc("DeleteObject")
	procCaptureDeleteDC            = gdi32Capture.NewProc("DeleteDC")
)

const (
	srcCopy    = 0x00CC0020
	captureBLT = 0x40000000

	biRGB             = 0
	dibRGBColors      = 0
	captureBitCount   = 32
	captureBytesPerPx = 4

	// probeColor is opaque so the layered window presents it unblended, which
	// makes "the overlay is in this grab" a pixel equality rather than a guess.
	probeColor = 0xFFFF00FF

	// compositeDelay lets DWM present the layered window before it is grabbed.
	// UpdateLayeredWindow returns before the frame is on screen.
	compositeDelay = 300 * time.Millisecond

	// presentThreshold is the share of the grabbed rect that must carry the probe
	// color for the overlay to count as captured. Well above noise, well below
	// 100%: the window is opaque over the whole rect, so a real capture is near
	// total and an exclusion is near zero.
	presentThreshold = 0.5
)

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

func TestCaptureExclusionHoldsAgainstBitBlt(t *testing.T) {
	// Not parallel: it reads the screen, so another test drawing an overlay would
	// land in the same grab.
	probe := image.Rect(200, 200, 600, 500)

	overlay, err := winplatform.NewOverlayWindowAt(
		probe.Min.X, probe.Min.Y, probe.Dx(), probe.Dy(),
	)
	skipIfOverlayUnavailable(t, err)

	if err != nil {
		t.Fatalf("NewOverlayWindowAt: %v", err)
	}

	defer overlay.Destroy()

	overlay.Clear()
	overlay.FillRect(image.Rect(0, 0, probe.Dx(), probe.Dy()), probeColor)

	err = overlay.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}

	overlay.Show()
	time.Sleep(compositeDelay)

	// The control comes first. Without it a passing exclusion proves nothing: a
	// grab that never sees the overlay in the first place would pass too.
	for _, useCaptureBLT := range []bool{false, true} {
		share := probeShareOfScreenRect(t, probe, useCaptureBLT)
		if share < presentThreshold {
			t.Fatalf(
				"a capturable overlay is only %.0f%% of the grab (CAPTUREBLT=%v); "+
					"the grab cannot see the overlay, so this test cannot judge the exclusion",
				share*100, useCaptureBLT,
			)
		}
	}

	err = overlay.SetExcludedFromCapture(true)
	if err != nil {
		t.Fatalf("SetExcludedFromCapture(true): %v", err)
	}

	time.Sleep(compositeDelay)

	for _, useCaptureBLT := range []bool{false, true} {
		share := probeShareOfScreenRect(t, probe, useCaptureBLT)
		if share >= presentThreshold {
			t.Errorf(
				"an excluded overlay is still %.0f%% of the grab (CAPTUREBLT=%v); "+
					"WDA_EXCLUDEFROMCAPTURE does not hold for BitBlt and the capture "+
					"backend needs a different mechanism",
				share*100, useCaptureBLT,
			)
		}
	}

	// Clearing it has to work too, or a user who turns the option off stays hidden
	// until the overlay is recreated.
	err = overlay.SetExcludedFromCapture(false)
	if err != nil {
		t.Fatalf("SetExcludedFromCapture(false): %v", err)
	}

	time.Sleep(compositeDelay)

	share := probeShareOfScreenRect(t, probe, false)
	if share < presentThreshold {
		t.Errorf(
			"after clearing the exclusion the overlay is only %.0f%% of the grab; "+
				"WDA_NONE did not restore it",
			share*100,
		)
	}
}

// probeShareOfScreenRect grabs one screen rectangle with BitBlt and reports the
// fraction of its pixels carrying probeColor.
func probeShareOfScreenRect(t *testing.T, rect image.Rectangle, useCaptureBLT bool) float64 {
	t.Helper()

	pixels := grabScreenRect(t, rect, useCaptureBLT)

	const lowByte = 0xFF

	want := [3]byte{
		byte(probeColor & lowByte),         // blue
		byte((probeColor >> 8) & lowByte),  // green
		byte((probeColor >> 16) & lowByte), // red
	}

	matched := 0

	for offset := 0; offset+captureBytesPerPx <= len(pixels); offset += captureBytesPerPx {
		// GetDIBits hands back BGRA, so the comparison is written in that order
		// rather than swizzled first: this test judges pixels, it does not produce
		// an image.
		if pixels[offset] == want[0] &&
			pixels[offset+1] == want[1] &&
			pixels[offset+2] == want[2] {
			matched++
		}
	}

	total := rect.Dx() * rect.Dy()
	if total == 0 {
		t.Fatal("probe rect is empty")
	}

	return float64(matched) / float64(total)
}

// grabScreenRect BitBlts one rectangle of the display DC into a 32-bit top-down
// DIB and returns its BGRA bytes.
func grabScreenRect(t *testing.T, rect image.Rectangle, useCaptureBLT bool) []byte {
	t.Helper()

	screenDC, _, err := procCaptureGetDC.Call(0)
	if screenDC == 0 {
		t.Fatalf("GetDC(0): %v", err)
	}

	defer procCaptureReleaseDC.Call(0, screenDC)

	memDC, _, err := procCaptureCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		t.Fatalf("CreateCompatibleDC: %v", err)
	}

	defer procCaptureDeleteDC.Call(memDC)

	width, height := rect.Dx(), rect.Dy()

	bitmap, _, err := procCaptureCreateCompatibleBmp.Call(
		screenDC, uintptr(width), uintptr(height),
	)
	if bitmap == 0 {
		t.Fatalf("CreateCompatibleBitmap: %v", err)
	}

	defer procCaptureDeleteObject.Call(bitmap)

	previous, _, _ := procCaptureSelectObject.Call(memDC, bitmap)
	defer procCaptureSelectObject.Call(memDC, previous)

	flags := uintptr(srcCopy)
	if useCaptureBLT {
		// CAPTUREBLT is the flag that pulls layered windows into a grab, so it is
		// the case most likely to defeat the exclusion.
		flags |= captureBLT
	}

	ret, _, err := procCaptureBitBlt.Call(
		memDC, 0, 0, uintptr(width), uintptr(height),
		screenDC, uintptr(rect.Min.X), uintptr(rect.Min.Y),
		flags,
	)
	if ret == 0 {
		t.Fatalf("BitBlt: %v", err)
	}

	header := bitmapInfoHeader{
		Size:  uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width: int32(width),
		// Negative height asks for a top-down DIB. A positive one is bottom-up,
		// which would not change the count here but would make every future reader
		// of this code wonder which way it went.
		Height:      int32(-height),
		Planes:      1,
		BitCount:    captureBitCount,
		Compression: biRGB,
	}

	pixels := make([]byte, width*height*captureBytesPerPx)

	scanned, _, err := procCaptureGetDIBits.Call(
		memDC,
		bitmap,
		0,
		uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&header)),
		dibRGBColors,
	)
	if scanned == 0 {
		t.Fatalf("GetDIBits: %v", err)
	}

	return pixels
}
