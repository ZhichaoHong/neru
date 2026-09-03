//go:build darwin

package vision

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Vision -framework CoreGraphics -framework Foundation
#include "../platform/darwin/vision.h"
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"image"
	"unsafe"

	"go.uber.org/zap"

	_ "github.com/y3owk1n/neru/internal/adapter/platform/darwin" // ensure darwin CGo .m files are compiled
	"github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/derrors"
	"github.com/y3owk1n/neru/internal/domain/element"
)

const bytesPerPixel = 4

// DetectElements captures a screenshot of the main display and runs Vision
// Framework detection (text recognition + rectangle detection). Results are
// merged via non-maximum suppression and classified by the heuristic classifier.
func (a *Adapter) DetectElements(
	ctx context.Context,
	screenBounds image.Rectangle,
	cfg config.HintsVisionConfig,
	splitWord bool,
) ([]*element.Element, error) {
	select {
	case <-ctx.Done():
		return nil, derrors.Wrap(ctx.Err(), derrors.CodeContextCanceled, "operation canceled")
	default:
	}

	// Convert screen bounds to CGRect
	cgRect := C.CGRect{
		origin: C.CGPoint{x: C.double(screenBounds.Min.X), y: C.double(screenBounds.Min.Y)},
		size:   C.CGSize{width: C.double(screenBounds.Dx()), height: C.double(screenBounds.Dy())},
	}

	var detectText C.int
	if cfg.DetectText {
		detectText = 1
	}

	var detectRectangles C.int
	if cfg.DetectRectangles {
		detectRectangles = 1
	}

	// Word level always: mergeWordRuns rebuilds labels from words more carefully
	// than Vision's line segmentation does, and splitWord decides whether that merge
	// runs rather than what the framework is asked for. See its comment for why
	// per-line rects cost reachability.
	wordLevel := C.int(1)

	cCfg := C.NeruVisionConfig{
		detectText:             detectText,
		detectRectangles:       detectRectangles,
		requestTimeoutMS:       C.int(cfg.RequestTimeoutMS),
		rectangleMaxCandidates: C.int(cfg.RectangleMaxCandidates),
		rectangleMinSize:       C.double(cfg.RectangleMinSize),
		rectangleMinAspect:     C.double(cfg.RectangleMinAspect),
		rectangleMaxAspect:     C.double(cfg.RectangleMaxAspect),
		wordLevel:              wordLevel,
	}

	result := C.NeruDetectElements(cgRect, cCfg)
	if result == nil {
		return nil, nil
	}

	defer C.NeruFreeVisionResult(result)

	count := int(result.count)
	if count == 0 {
		return nil, nil
	}

	// Convert C regions to Go DetectedRegions
	regions := make([]DetectedRegion, 0, count)
	cRegions := (*[1 << 30]C.VisionRegion)(unsafe.Pointer(result.regions))[:count:count]

	for _, cRegion := range cRegions {
		isText := cRegion.isText != 0
		if isText && !cfg.DetectText {
			continue
		}
		if !isText && !cfg.DetectRectangles {
			continue
		}
		if float64(cRegion.score) < cfg.MinimumConfidence {
			continue
		}

		region := DetectedRegion{
			Bounds: image.Rectangle{
				Min: image.Point{X: int(cRegion.x), Y: int(cRegion.y)},
				Max: image.Point{
					X: int(cRegion.x + cRegion.width),
					Y: int(cRegion.y + cRegion.height),
				},
			},
			Score:  float64(cRegion.score),
			IsText: isText,
		}
		if cRegion.label != nil {
			region.Label = C.GoString(cRegion.label)
		}
		regions = append(regions, region)
	}

	if !splitWord {
		regions = mergeWordRuns(regions)
	}

	// Merge overlapping regions via NMS
	merged := MergeRegions(regions, cfg.MergeIOUThreshold)

	// Filter regions outside the window bounds
	windowElements := make([]DetectedRegion, 0, len(merged))
	for _, region := range merged {
		if region.Bounds.Overlaps(screenBounds) {
			windowElements = append(windowElements, region)
		}
	}

	// Classify and convert to domain elements
	classifier := newRegionClassifier(cfg)

	elements, skipped := elementsFromRegions(windowElements, &classifier)

	a.logger.Debug("Vision detection complete",
		zap.Int("raw_regions", count),
		zap.Int("merged_elements", len(elements)),
		zap.Int("skipped_regions", skipped),
	)

	return elements, nil
}

// DetectContours captures the main display and returns the target-shaped edges
// inside screenBounds as hintable elements. Unlike DetectElements this runs no
// Vision request: the detector is platform-neutral Go over the pixels, so the only
// native part of this path is the capture.
//
// Two consequences of that capture being the main display's, both of which are
// limitations rather than choices:
//
// A window on a secondary display cannot be read. NeruDetectElements captures the
// display containing the rect it is given; NeruCaptureScreen takes the main one and
// nothing else. Clipping would then reduce every rectangle to nothing, so this
// refuses instead - an empty overlay says nothing about why it is empty.
//
// And the whole display is detected before the clip, so a small window on a large
// display costs a full-screen pass. Region capture on macOS is the fix and it
// belongs in the platform layer beside the Windows and Linux ones, not here.
//
// The two scales are the same number, derived rather than assumed: the frame is
// pixels and screenBounds is points, so the ratio between the captured width and
// the display's width in points is both the display's backing scale and the frame
// pixels per point. They cancel, which is right - the detector's thresholds are
// logical-pixel numbers and points already are logical pixels.
func (a *Adapter) DetectContours(
	ctx context.Context,
	screenBounds image.Rectangle,
) ([]*element.Element, error) {
	select {
	case <-ctx.Done():
		return nil, derrors.Wrap(ctx.Err(), derrors.CodeContextCanceled, "operation canceled")
	default:
	}

	region := screenBounds.Canon()
	if region.Empty() {
		return nil, derrors.Newf(
			derrors.CodeActionFailed,
			"contour detection needs a region to read; %v is empty",
			screenBounds,
		)
	}

	mainDisplay := mainDisplayBounds()
	if !region.Overlaps(mainDisplay) {
		return nil, derrors.Newf(
			derrors.CodeNotSupported,
			"the contour strategy reads the main display only, and %v is outside it (%v)",
			region,
			mainDisplay,
		)
	}

	img, err := a.CaptureScreen(ctx)
	if err != nil {
		return nil, err
	}

	scale := 1.0
	if width := mainDisplay.Dx(); width > 0 && img.Rect.Dx() > 0 {
		scale = float64(img.Rect.Dx()) / float64(width)
	}

	elements, err := contourElements(img, image.Point{}, region, scale, scale)
	if err != nil {
		return nil, err
	}

	a.logger.Debug("Contour detection complete",
		zap.Float64("scale", scale),
		zap.Int("frame_width", img.Rect.Dx()),
		zap.Int("frame_height", img.Rect.Dy()),
		zap.Int("elements", len(elements)),
	)

	return elements, nil
}

// mainDisplayBounds is the main display's rectangle in global points, top-left
// origin, which is the space window bounds arrive in. It is the frame's extent as
// the rest of the pipeline measures it, so it answers both where the capture
// reaches and how many pixels went into each point of it.
func mainDisplayBounds() image.Rectangle {
	bounds := C.CGDisplayBounds(C.CGMainDisplayID())

	return image.Rect(
		int(bounds.origin.x),
		int(bounds.origin.y),
		int(bounds.origin.x+bounds.size.width),
		int(bounds.origin.y+bounds.size.height),
	)
}

// CaptureScreen captures the current screen image for the primary display.
func (a *Adapter) CaptureScreen(_ context.Context) (*image.RGBA, error) {
	cgImage := C.NeruCaptureScreen()
	if uintptr(cgImage) == 0 {
		return nil, derrors.New(derrors.CodeInternal, "failed to capture screen")
	}

	defer C.CGImageRelease(cgImage)

	width := int(C.CGImageGetWidth(cgImage))
	height := int(C.CGImageGetHeight(cgImage))

	if width == 0 || height == 0 {
		return nil, derrors.New(derrors.CodeInternal, "captured screen image has zero size")
	}

	bytesPerRow := width * bytesPerPixel
	buf := make([]byte, height*bytesPerRow)

	colorSpace := C.CGColorSpaceCreateDeviceRGB()

	defer C.CGColorSpaceRelease(colorSpace)

	ctx := C.CGBitmapContextCreate(
		unsafe.Pointer(&buf[0]),
		C.size_t(width),
		C.size_t(height),
		8, // bits per component
		C.size_t(bytesPerRow),
		colorSpace,
		C.kCGImageAlphaPremultipliedLast,
	)

	if uintptr(ctx) == 0 {
		return nil, derrors.New(derrors.CodeInternal, "failed to create bitmap context")
	}
	defer C.CGContextRelease(ctx)

	// Draw the captured image into our context
	C.CGContextDrawImage(ctx, C.CGRect{
		origin: C.CGPoint{x: 0, y: 0},
		size:   C.CGSize{width: C.double(width), height: C.double(height)},
	}, cgImage)

	// Create RGBA image
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	copy(img.Pix, buf)

	return img, nil
}

// Health reports whether the vision strategy can run on this machine, which on
// macOS it always can: the Vision framework ships with the OS and is linked into
// this binary, so a build that starts has it. There is no per-machine
// prerequisite the way there is on Linux and Windows, where the OCR language data
// is a separate install.
//
// This used to smoke-test by taking a screen capture, which was defensible while
// nothing called it. HintService.Health now does, on every `neru doctor` and
// `neru info`, and a diagnostic that reads the user's entire screen to find out
// whether it is able to read the screen is the wrong trade - it would also report
// a missing Screen Recording grant as CodeInternal, when a permission is not a
// capability. SystemPort.CheckScreenCapturePermission owns that question and asks
// the user ahead of the activation, which is the same split the Linux adapter
// documents.
func (a *Adapter) Health(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return derrors.Wrap(ctx.Err(), derrors.CodeContextCanceled, "operation canceled")
	default:
	}

	return nil
}
