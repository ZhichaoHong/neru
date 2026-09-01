//go:build windows

package windows

import (
	"image"
	"strings"

	"go.uber.org/zap"

	"github.com/y3owk1n/neru/internal/adapter/overlay/manager"
	"github.com/y3owk1n/neru/internal/adapter/overlay/render/badge"
	"github.com/y3owk1n/neru/internal/adapter/overlay/render/monitorselect"
	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
	"github.com/y3owk1n/neru/internal/derrors"
)

var _ manager.MonitorSelector = (*Manager)(nil)

// DrawMonitorSelect renders one labeled panel per monitor for the interactive
// monitor picker, then shows the overlay.
//
// The panels go into a dedicated layered window spanning every target's bounds
// rather than into the shared overlay, for two reasons. The shared window is
// sized to the *active* monitor, and this is the one overlay that has to be on
// all of them at once; and a mode that borrowed the shared surface would have to
// hand it back at the size the next mode expects. The lazily-created siblings
// beside it (the mode, sticky, and mouse-action indicators) already work this
// way.
func (m *Manager) DrawMonitorSelect(
	targets []manager.MonitorSelectTarget,
	style manager.MonitorSelectStyle,
) error {
	m.renderMu.Lock()
	defer m.renderMu.Unlock()

	desktop := monitorSelectSpan(targets)
	if desktop.Empty() {
		return derrors.New(
			derrors.CodeOverlayFailed,
			"monitor_select has no monitor with drawable bounds",
		)
	}

	win, err := m.ensureMonitorSelectWindowLocked(desktop)
	if err != nil {
		return err
	}

	win.Clear()

	// Colors and base font sizes are parsed once for the whole frame rather
	// than per panel. The families arrive already resolved (overlay/style.go),
	// so they are used as written.
	backdrop := badge.ParseHexARGB(style.BackdropColor)
	hasBackdrop := strings.TrimSpace(style.BackdropColor) != ""
	background := badge.ParseHexARGB(style.BackgroundColor)
	borderColor := badge.ParseHexARGB(style.BorderColor)
	textColor := badge.ParseHexARGB(style.TextColor)
	subtitleColor := badge.ParseHexARGB(style.SubtitleTextColor)
	// The sizes stay unscaled here because the factor is per monitor: see the
	// loop below.
	baseBorderWidth := float64(max(style.BorderWidth, 1))
	baseLabelFont := monitorselect.FontOr(style.FontSize, monitorselect.DefaultFontSize)
	baseSubtitleFont := monitorselect.FontOr(
		style.SubtitleFontSize,
		monitorselect.DefaultSubtitleFontSize,
	)

	for _, target := range targets {
		if target.Bounds.Empty() {
			continue
		}

		// The window's pixel buffer is indexed from its own top-left, and the
		// span's origin is negative whenever a monitor sits left of or above the
		// primary one, so every rect is translated out of screen coordinates
		// before it is drawn.
		if hasBackdrop {
			win.FillRect(target.Bounds.Sub(desktop.Min), backdrop)
		}

		// This is the one overlay drawn on every monitor at once, so the scale is
		// read per target rather than once for the frame: on a mixed-DPI desk the
		// panels are meant to look the same size, not to be the same number of
		// pixels. The window's own scale would describe whichever monitor its
		// span happens to start on.
		scale := winplatform.ScreenScaleAt(image.Pt(
			target.Bounds.Min.X+target.Bounds.Dx()/2,
			target.Bounds.Min.Y+target.Bounds.Dy()/2,
		))
		labelFont := baseLabelFont * scale
		subtitleFont := baseSubtitleFont * scale
		borderWidth := baseBorderWidth * scale

		panel, labelRect, subtitleRect, radius := monitorselect.PanelLayout(
			target.Bounds, target.Label, target.Subtitle, style, scale,
		)

		localPanel := panel.Sub(desktop.Min)

		win.FillRoundedRect(localPanel, radius, background)
		win.StrokeRoundedRect(localPanel, radius, borderColor, borderWidth)

		// Like darwin and Linux, the label uses the single text color: a matched
		// prefix or a selected panel is not visually distinguished, so
		// Style.MatchedTextColor goes unread here on purpose.
		win.DrawTextCentered(
			target.Label,
			labelRect.Sub(desktop.Min),
			style.FontFamily,
			labelFont,
			winplatform.FontWeightBold,
			textColor,
		)

		if target.Subtitle != "" {
			// The subtitle family is never empty: an unset one is settled to the
			// label's family with the rest of the Style.
			win.DrawTextCentered(
				target.Subtitle,
				subtitleRect.Sub(desktop.Min),
				style.SubtitleFontFamily,
				subtitleFont,
				winplatform.FontWeightBold,
				subtitleColor,
			)
		}
	}

	// Flush composites the queued fills and text into the pixel buffer and
	// sends the frame to the HWND. It has to happen before Show() so the window
	// appears with the panels already on it.
	flushErr := win.Flush()
	if flushErr != nil {
		return derrors.Wrap(
			flushErr,
			derrors.CodeOverlayFailed,
			"failed to paint the monitor_select overlay",
		)
	}

	win.Show()

	return nil
}

// HideMonitorSelect hides the monitor_select overlay. The window is kept for the
// next activation, the way the other dedicated indicator windows are.
func (m *Manager) HideMonitorSelect() {
	m.renderMu.Lock()
	defer m.renderMu.Unlock()

	if m.monitorSelectWin != nil {
		m.monitorSelectWin.Hide()
	}
}

// monitorSelectSpan returns the smallest rectangle covering every target's
// bounds, in screen coordinates. Empty targets are skipped so one bad entry does
// not stretch the window to the origin.
//
// This is the union of the monitors the mode offers rather than the virtual
// desktop the system reports, which are the same thing when every monitor is
// offered and the right answer when they are not.
func monitorSelectSpan(targets []manager.MonitorSelectTarget) image.Rectangle {
	var span image.Rectangle

	for _, target := range targets {
		if target.Bounds.Empty() {
			continue
		}

		span = span.Union(target.Bounds)
	}

	return span
}

// ensureMonitorSelectWindowLocked returns the monitor_select window sized to
// span, creating it or resizing it as needed. Callers must hold renderMu.
func (m *Manager) ensureMonitorSelectWindowLocked(
	span image.Rectangle,
) (*winplatform.OverlayWindow, error) {
	if m.monitorSelectWin != nil && m.monitorSelectWin.Healthy() {
		resizeErr := m.monitorSelectWin.ResizeTo(
			span.Min.X, span.Min.Y, span.Dx(), span.Dy(),
		)
		if resizeErr != nil {
			return nil, derrors.Wrap(
				resizeErr,
				derrors.CodeOverlayFailed,
				"failed to resize the monitor_select overlay window",
			)
		}

		return m.monitorSelectWin, nil
	}

	if m.monitorSelectWin != nil {
		m.monitorSelectWin.Destroy()
		m.monitorSelectWin = nil
	}

	win, err := m.newOverlayWindowAt(
		span.Min.X, span.Min.Y, span.Dx(), span.Dy(),
	)
	if err != nil {
		if m.logger != nil {
			m.logger.Error("failed to create monitor_select overlay window", zap.Error(err))
		}

		return nil, derrors.Wrap(
			err,
			derrors.CodeOverlayFailed,
			"failed to create the monitor_select overlay window",
		)
	}

	m.monitorSelectWin = win

	return win, nil
}
