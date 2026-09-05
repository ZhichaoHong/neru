//go:build integration && windows && amd64

package windows

import "testing"

// The GDI surface is a deliberate fallback, so a wrong interface ID or vtable
// slot in the DirectComposition path costs throughput without failing a test:
// the overlay still draws. This pins the one thing that must hold — once the
// composition device is up, the surface built on it comes up too.
func TestDCompSurfaceComesUpWhenTheDeviceDoes(t *testing.T) {
	if !dcompAvailable() {
		t.Skipf("DirectComposition unavailable in this session: %v", errDComp)
	}

	overlay, err := NewOverlayWindow()
	if err != nil {
		t.Fatalf("NewOverlayWindow: %v", err)
	}

	defer overlay.Destroy()

	if backend := overlay.Backend(); backend != "direct2d" {
		t.Fatalf("backend = %q, want direct2d", backend)
	}
}
