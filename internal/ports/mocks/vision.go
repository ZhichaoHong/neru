package mocks

import (
	"context"
	"image"
	"sync"

	"github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/derrors"
	"github.com/y3owk1n/neru/internal/domain/element"
	"github.com/y3owk1n/neru/internal/ports"
)

// MockVisionPort is a mock implementation of ports.VisionPort.
//
// Its zero value behaves like a platform with no vision backend at all:
// CodeNotSupported from every method. Set the Func fields to model a working one.
type MockVisionPort struct {
	HealthFunc         func(context.Context) error
	DetectElementsFunc func(
		context.Context,
		image.Rectangle,
		config.HintsVisionConfig,
		bool,
	) ([]*element.Element, error)
	DetectContoursFunc func(context.Context, image.Rectangle) ([]*element.Element, error)
	CaptureScreenFunc  func(context.Context) (*image.RGBA, error)

	mu       sync.Mutex
	detectN  int
	contourN int
	captureN int
}

// Health implements ports.VisionPort.
func (m *MockVisionPort) Health(ctx context.Context) error {
	if m.HealthFunc != nil {
		return m.HealthFunc(ctx)
	}

	return derrors.New(derrors.CodeNotSupported, "vision framework is only available on macOS")
}

// DetectElements implements ports.VisionPort.
func (m *MockVisionPort) DetectElements(
	ctx context.Context,
	screenBounds image.Rectangle,
	cfg config.HintsVisionConfig,
	splitWord bool,
) ([]*element.Element, error) {
	m.mu.Lock()
	m.detectN++
	m.mu.Unlock()

	if m.DetectElementsFunc != nil {
		return m.DetectElementsFunc(ctx, screenBounds, cfg, splitWord)
	}

	return nil, derrors.New(derrors.CodeNotSupported, "vision detection is only supported on macOS")
}

// DetectContours implements ports.VisionPort.
func (m *MockVisionPort) DetectContours(
	ctx context.Context,
	screenBounds image.Rectangle,
) ([]*element.Element, error) {
	m.mu.Lock()
	m.contourN++
	m.mu.Unlock()

	if m.DetectContoursFunc != nil {
		return m.DetectContoursFunc(ctx, screenBounds)
	}

	return nil, derrors.New(
		derrors.CodeNotSupported,
		"the contour strategy needs screen capture, which is not implemented on this platform",
	)
}

// CaptureScreen implements ports.VisionPort.
func (m *MockVisionPort) CaptureScreen(ctx context.Context) (*image.RGBA, error) {
	m.mu.Lock()
	m.captureN++
	m.mu.Unlock()

	if m.CaptureScreenFunc != nil {
		return m.CaptureScreenFunc(ctx)
	}

	return nil, derrors.New(derrors.CodeNotSupported, "screen capture is only supported on macOS")
}

// DetectCallCount reports how many times DetectElements was called.
func (m *MockVisionPort) DetectCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.detectN
}

// ContourCallCount reports how many times DetectContours was called.
func (m *MockVisionPort) ContourCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.contourN
}

// CaptureCallCount reports how many times CaptureScreen was called.
func (m *MockVisionPort) CaptureCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.captureN
}

// Ensure MockVisionPort implements ports.VisionPort.
var _ ports.VisionPort = (*MockVisionPort)(nil)
