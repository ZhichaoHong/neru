//go:build integration && windows

package windows_test

import (
	"context"
	"image"
	"sync"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
)

// The invisible resize border is DPI-dependent - 9 physical pixels at 150%
// scaling, 11 at 200% - so this pins the relationship between the two rects and
// never a measurement. Run on WIN-VM with:
// go test -tags=integration -run FocusedWindowBounds ./internal/adapter/platform/windows/...

const (
	wsThickFrame  = 0x00040000
	wsMaximizeBox = 0x00010000

	// perMonitorAwareV2 is DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2, passed as a
	// pseudo-handle rather than a pointer.
	perMonitorAwareV2 = ^uintptr(3)
)

var (
	// GWL_STYLE is negative, so it cannot be a constant converted to uintptr.
	gwlStyle = int32(-16)

	user32Frame = windows.NewLazySystemDLL("user32.dll")

	procFrameGetWindowRect        = user32Frame.NewProc("GetWindowRect")
	procFrameGetWindowLongW       = user32Frame.NewProc("GetWindowLongW")
	procSetProcessDpiAwarenessCtx = user32Frame.NewProc("SetProcessDpiAwarenessContext")
)

// frameTestDPIOnce matches the DPI awareness neru's manifest declares. A test
// binary links no manifest, so without this the process is DPI-unaware:
// GetWindowRect comes back virtualized while DwmGetWindowAttribute does not, and
// the two rects differ by the monitor's scale factor rather than by a border.
// Failing is fine, it means the context was already set.
var frameTestDPIOnce = sync.OnceFunc(func() {
	if procSetProcessDpiAwarenessCtx.Find() != nil {
		return
	}

	procSetProcessDpiAwarenessCtx.Call(perMonitorAwareV2) //nolint:errcheck
})

func TestFocusedWindowBoundsExcludesTheInvisibleBorder(t *testing.T) {
	t.Parallel()
	frameTestDPIOnce()

	handle, ok := winplatform.ForegroundWindowHandle()
	if !ok {
		t.Skip("skipping: no foreground window in this session")
	}

	adapter := winplatform.NewSystemAdapter()

	visible, found, err := adapter.FocusedWindowBounds(context.Background())
	skipIfHeadlessSession(t, err)

	if err != nil {
		t.Fatalf("FocusedWindowBounds: %v", err)
	}

	if !found {
		t.Skip("skipping: foreground window is not visible")
	}

	outer := windowRect(t, handle)

	// FocusedWindowBounds clips to the desktop, so a window hanging off the outer
	// edge of the outermost monitor would fail the inset comparison below for a
	// reason that has nothing to do with the border.
	if !outer.In(desktopBounds(t, adapter)) {
		t.Skip("skipping: foreground window extends past the desktop")
	}

	if !visible.In(outer) {
		t.Fatalf("visible frame %v is not inside GetWindowRect %v", visible, outer)
	}

	left := visible.Min.X - outer.Min.X
	right := outer.Max.X - visible.Max.X
	bottom := outer.Max.Y - visible.Max.Y

	// Windows keeps the same border width on both vertical edges, so an
	// asymmetric inset means the two rects describe different windows or the
	// window moved mid-test.
	if left != right {
		t.Errorf("left inset %d != right inset %d (outer %v, visible %v)",
			left, right, outer, visible)
	}

	if !isResizable(t, handle) {
		t.Logf("foreground window is not resizable; insets were %d/%d/%d", left, right, bottom)

		return
	}

	// A resizable window is the case this exists for: GetWindowRect pads it and
	// DWM does not. Zero here means the DWM call fell back to the outer rect.
	if left <= 0 || bottom <= 0 {
		t.Errorf(
			"resizable window reported no border: left=%d bottom=%d (outer %v, visible %v)",
			left, bottom, outer, visible,
		)
	}
}

// desktopBounds is the rectangle every monitor fits inside, built from the same
// enumeration the adapter clips against.
func desktopBounds(t *testing.T, adapter *winplatform.SystemAdapter) image.Rectangle {
	t.Helper()

	ctx := context.Background()

	names, err := adapter.ScreenNames(ctx)
	if err != nil {
		t.Fatalf("ScreenNames: %v", err)
	}

	var desktop image.Rectangle

	for _, name := range names {
		bounds, found, err := adapter.ScreenBoundsByName(ctx, name)
		if err != nil {
			t.Fatalf("ScreenBoundsByName(%q): %v", name, err)
		}

		if found {
			desktop = desktop.Union(bounds)
		}
	}

	if desktop.Empty() {
		t.Fatal("no monitors reported bounds")
	}

	return desktop
}

func windowRect(t *testing.T, handle uintptr) image.Rectangle {
	t.Helper()

	var rect windows.Rect

	ret, _, err := procFrameGetWindowRect.Call(handle, uintptr(unsafe.Pointer(&rect)))
	if ret == 0 {
		t.Fatalf("GetWindowRect: %v", err)
	}

	return image.Rect(int(rect.Left), int(rect.Top), int(rect.Right), int(rect.Bottom))
}

func isResizable(t *testing.T, handle uintptr) bool {
	t.Helper()

	style, _, _ := procFrameGetWindowLongW.Call(handle, uintptr(gwlStyle))

	return style&wsThickFrame != 0 && style&wsMaximizeBox != 0
}
