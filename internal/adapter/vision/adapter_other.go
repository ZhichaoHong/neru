//go:build !darwin && !linux && !windows

package vision

import (
	"context"
	"image"

	"github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/derrors"
	"github.com/y3owk1n/neru/internal/domain/element"
)

// DetectElements reports not-supported on the platforms with no vision
// implementation at all. Nothing neru releases builds through this file today:
// darwin, linux and windows each have a real adapter, so this is what a freebsd
// or openbsd build would compile.
func (a *Adapter) DetectElements(
	_ context.Context,
	_ image.Rectangle,
	_ config.HintsVisionConfig,
	_ bool,
) ([]*element.Element, error) {
	return nil, derrors.New(
		derrors.CodeNotSupported,
		"vision element detection is not implemented on this platform",
	)
}

// DetectContours reports not-supported. The detector itself is platform-neutral
// Go and would run here happily; what is missing is the capture underneath it, so
// the error names that rather than the strategy's algorithm.
func (a *Adapter) DetectContours(
	_ context.Context,
	_ image.Rectangle,
) ([]*element.Element, error) {
	return nil, derrors.New(
		derrors.CodeNotSupported,
		"the contour strategy needs screen capture, which is not implemented on this platform",
	)
}

// CaptureScreen reports not-supported: there is no capture backend on this
// platform.
func (a *Adapter) CaptureScreen(_ context.Context) (*image.RGBA, error) {
	return nil, derrors.New(
		derrors.CodeNotSupported,
		"screen capture is not implemented on this platform",
	)
}

// Health reports not-supported, which is how the hint pipeline learns the
// vision strategy is unavailable here.
func (a *Adapter) Health(_ context.Context) error {
	return derrors.New(
		derrors.CodeNotSupported,
		"the vision strategy is not implemented on this platform",
	)
}
