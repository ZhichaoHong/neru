// Package monitorselect holds the platform-neutral geometry for the
// monitor_select overlay: how big the panel on each monitor is, where its label
// and subtitle sit inside it, and how round its corners are. The numbers mirror
// the macOS overlay (monitor_select_overlay_darwin.m), so every backend that
// computes its layout here matches darwin without re-deriving it.
//
// Drawing stays with the backend. Only the sizing lives here.
package monitorselect

import (
	"image"
	"math"

	"github.com/y3owk1n/neru/internal/adapter/overlay/render/badge"
)

const (
	halfDivisor       = 2
	paddingMultiplier = 2

	// labelGap is the vertical space between the label and the subtitle.
	labelGap = 4

	// The autoPad* values derive padding from the label font when the config
	// uses the -1 "auto" sentinel, and maxFraction caps the panel at 80% of the
	// monitor in each axis. maxRadius caps the auto corner radius.
	autoPadXMin   = 24
	autoPadXRatio = 0.3
	autoPadYMin   = 12
	autoPadYRatio = 0.15
	maxFraction   = 0.8
	maxRadius     = 16

	// DefaultFontSize is the label size used when the config leaves it unset.
	DefaultFontSize = 96
	// DefaultSubtitleFontSize is the subtitle equivalent.
	DefaultSubtitleFontSize = 18
)

// Style carries the resolved (theme-applied) appearance for the monitor_select
// overlay. Colors are hex strings, parsed by the backend, to mirror how the
// hints/grid styles are threaded.
type Style struct {
	FontSize           int
	SubtitleFontSize   int
	FontFamily         string
	SubtitleFontFamily string
	BorderRadius       int
	PaddingX           int
	PaddingY           int
	BorderWidth        int
	BackgroundColor    string
	TextColor          string
	MatchedTextColor   string
	BorderColor        string
	BackdropColor      string
	SubtitleTextColor  string
	HideInScreenShare  bool
}

// FontOr returns the configured font size, or fallback when unset (<= 0).
func FontOr(value, fallback int) float64 {
	if value <= 0 {
		return float64(fallback)
	}

	return float64(value)
}

// PanelLayout computes, in device pixels, the centered panel rect, the label and
// subtitle text rects, and the corner radius. Padding and radius honor the "auto"
// (-1) config sentinels. scale is the backend HiDPI factor, and it is 1 for any
// backend whose monitor bounds and drawing surface are already in the same
// units: Wayland scales via the compositor buffer, and Windows gets physical
// pixels for both. X11 is the one that needs its Xft.dpi factor here.
//
// The subtitle rect is the zero Rectangle when subtitle is empty.
func PanelLayout(
	monitor image.Rectangle,
	label, subtitle string,
	style Style,
	scale float64,
) (image.Rectangle, image.Rectangle, image.Rectangle, float64) {
	labelFont := FontOr(style.FontSize, DefaultFontSize) * scale
	subFont := FontOr(style.SubtitleFontSize, DefaultSubtitleFontSize) * scale

	padX := float64(style.PaddingX) * scale
	if style.PaddingX < 0 {
		padX = math.Max(autoPadXMin*scale, math.Round(labelFont*autoPadXRatio))
	}

	padY := float64(style.PaddingY) * scale
	if style.PaddingY < 0 {
		padY = math.Max(autoPadYMin*scale, math.Round(labelFont*autoPadYRatio))
	}

	labelW := badge.EstimateTextWidth(label, labelFont)
	labelH := badge.EstimateTextHeight(labelFont)

	subW, subH, gap := 0, 0, 0
	if subtitle != "" {
		subW = badge.EstimateTextWidth(subtitle, subFont)
		subH = badge.EstimateTextHeight(subFont)
		gap = int(math.Round(float64(labelGap) * scale))
	}

	panelW := max(labelW, subW) + int(padX)*paddingMultiplier

	panelH := labelH + int(padY)*paddingMultiplier
	if subtitle != "" {
		panelH += subH + gap
	}

	if maxW := int(float64(monitor.Dx()) * maxFraction); panelW > maxW {
		panelW = maxW
	}

	if maxH := int(float64(monitor.Dy()) * maxFraction); panelH > maxH {
		panelH = maxH
	}

	// The panel hangs on the monitor's center point.
	center := image.Pt(
		monitor.Min.X+monitor.Dx()/halfDivisor,
		monitor.Min.Y+monitor.Dy()/halfDivisor,
	)
	panel := badge.CenteredOn(center, panelW, panelH)

	radius := float64(style.BorderRadius) * scale
	if style.BorderRadius < 0 {
		radius = math.Min(float64(panelH)/halfDivisor, maxRadius*scale)
	}

	// Vertically center the label (+ subtitle) block within the panel.
	totalTextH := labelH
	if subtitle != "" {
		totalTextH += gap + subH
	}

	textTop := panel.Min.Y + (panelH-totalTextH)/halfDivisor

	labelRect := image.Rect(panel.Min.X, textTop, panel.Max.X, textTop+labelH)

	subtitleRect := image.Rectangle{}
	if subtitle != "" {
		subTop := labelRect.Max.Y + gap
		subtitleRect = image.Rect(panel.Min.X, subTop, panel.Max.X, subTop+subH)
	}

	return panel, labelRect, subtitleRect, radius
}
