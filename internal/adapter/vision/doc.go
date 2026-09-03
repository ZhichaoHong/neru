// Package vision implements ports.VisionPort, the screen-reading element
// detectors behind the hints that do not come from an accessibility tree.
//
// Two detectors live here and they have almost nothing in common but the capture
// underneath them. Text recognition answers the "vision" and "hybrid" strategies
// and is native on every platform - the macOS Vision framework, tesseract on
// Linux, Windows.Media.Ocr on Windows - so what it can do is a per-platform
// question. Contour detection answers the "contour" strategy, runs the same pure
// Go on all of them (subpackage contour, plus contour_detect.go here), and needs
// nothing installed; its per-platform question is only whether the pixels can be
// captured at all.
//
// Neither is the default. The accessibility tree is the source everywhere until
// hints.strategy says otherwise.
//
// adapter_darwin.go captures the frontmost window and runs text-recognition,
// rectangle-detection, and saliency requests, then classifier.go assigns roles
// heuristically. Contour skips the classifier entirely, because a rectangle with
// no text in it has no score to classify on.
//
// adapter_linux.go answers the same port with two native pieces: real pixels —
// wlr-screencopy on wlroots compositors, XGetImage on X11 — and tesseract
// through platform/linux/ocr.c, bound with #cgo pkg-config the way every other
// native dependency in the tree is. It answers the *text* half only: rectangle
// detection and saliency have no OCR equivalent, so hints.vision.detect_rectangles
// and the four rectangle_* options are declared macOS-only and Linux vision is
// text-only (docs/adr/0013). adapter_other.go has neither half and refuses
// everything.
//
// Captured pixels and recognized text are both screen content. Neither is
// logged, written to disk, or held past the call that asked for it; the native
// buffers behind them are wiped before they are released, and the engine is
// cleared of the frame before each recognition returns.
package vision
