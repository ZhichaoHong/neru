package modes

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/y3owk1n/neru/internal/derrors"
	"github.com/y3owk1n/neru/internal/domain"
	"github.com/y3owk1n/neru/internal/domain/modecmd"
	"github.com/y3owk1n/neru/internal/ports"
)

// ResetCurrentMode clears the active mode's accumulated input without exiting
// it. A mode with no input to clear says so in the debug log.
func (h *Handler) ResetCurrentMode() {
	h.mu.Lock()
	defer h.mu.Unlock()

	editor, ok := activeModeEffect[inputEditor](&h.handlerState, extensionInputEditing)
	if !ok {
		return
	}

	editor.ResetInput()
}

// BackspaceCurrentMode takes back the active mode's most recent unit of input
// without exiting it. A mode with no input to take back says so in the debug
// log.
func (h *Handler) BackspaceCurrentMode() {
	h.mu.Lock()
	defer h.mu.Unlock()

	editor, ok := activeModeEffect[inputEditor](&h.handlerState, extensionInputEditing)
	if !ok {
		return
	}

	editor.Backspace()
}

// MoveCellCurrentMode slides the active mode's selection count cells in dir
// without changing the active layer.
//
// Grid mode moves an open subgrid to the neighboring cell; recursive-grid
// mode slides the highlighted region at the current depth, crossing into a
// neighboring parent when it runs off the edge of its own. Modes with no cell
// selection ignore it, as does a move that would leave the screen.
func (h *Handler) MoveCellCurrentMode(dir domain.Direction, count int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	navigator, ok := activeModeExtension[cellNavigator](&h.handlerState)
	if !ok {
		return
	}

	navigator.MoveCell(dir, count)
}

// StartHintSearch activates text filtering for hints mode.
func (h *Handler) StartHintSearch() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.startHintSearch()
}

// ExpandHintScope widens the running hints session to the whole active screen
// and re-scans, so the controls in the windows behind the focused one are hinted
// too. It reports whether the session actually widened; a session already
// reading the screen says no and is left alone rather than re-scanning for the
// same answer, and one running a strategy the scope does not bound is refused.
func (h *Handler) ExpandHintScope() (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.expandHintScope()
}

// expandHintScope is the locked body of ExpandHintScope.
//
// One way and sticky: there is no narrowing counterpart, and the override lives
// on the context until the session ends, so every later refresh in this session
// - a space change, a monitor move, a passthrough - keeps the wider scope. The
// next activation starts from capture_scope again because applyHintFlagsFresh
// writes the override back to what the activation asked for.
//
// The re-scan goes through the ordinary in-place refresh rather than the
// screen-change path, because that is the one that takes the labels off screen
// before a screen-reading strategy captures it. Expanding is only worth doing
// under such a strategy, so capturing our own labels would be the common case
// rather than the corner one.
func (h *handlerState) expandHintScope() (bool, error) {
	if h.appState.CurrentMode() != domain.ModeHints {
		return false, derrors.New(
			derrors.CodeInvalidInput,
			"expand_hint_scope requires hints mode",
		)
	}

	if h.hints == nil || h.hints.Context == nil {
		return false, derrors.New(derrors.CodeActionFailed, "hints component not available")
	}

	strategy, captureScope := h.sessionCaptureSettings()

	// Only the screen-capture strategies are bounded by the scope, so under
	// axtree the expansion would set the override, pay for a re-scan, and hint
	// exactly the same window. Say so rather than doing that: the user pressed a
	// key expecting more hints, and silence would read as the key being broken.
	if strategy != domain.StrategyVision && strategy != domain.StrategyContour {
		return false, derrors.Newf(
			derrors.CodeInvalidInput,
			"expand_hint_scope has no effect under the %q strategy", strategy,
		)
	}

	if captureScope == domain.CaptureScopeScreen {
		h.logger.Debug("Hints already read the whole screen; not expanding")

		return false, nil
	}

	h.hints.Context.SetCaptureScopeOverride(domain.CaptureScopeScreen)

	// A bare activation: the refresh path writes only the fields an activation
	// carries, so every filter and override the session was started with - and
	// the scope just set - comes from the context untouched.
	h.activateHintModeInternal(modecmd.Activation{Mode: domain.ModeHints})

	return true, nil
}

// sessionCaptureSettings is the strategy the running session scans with and the
// region it scans, resolved the way the activation resolves them: the session
// override where there is one, otherwise the per-app configuration. Both come
// from one bundle-ID read, fetched with the short timeout the activation uses;
// failing to read it falls back to the global settings, which are the answer for
// every app that carries no override of its own.
func (h *handlerState) sessionCaptureSettings() (string, string) {
	bundleCtx, bundleCancel := context.WithTimeout(h.ctx, 1*time.Second)
	defer bundleCancel()

	bundleID, err := h.actionService.FocusedAppBundleID(bundleCtx)
	if err != nil {
		h.logger.Debug("Failed to get focused app bundle ID for capture settings",
			zap.Error(err))

		bundleID = ""
	}

	strategy := h.config.Hints.StrategyForApp(bundleID)
	if override := h.hints.Context.StrategyOverride(); override != "" {
		strategy = override
	}

	captureScope := h.config.Hints.CaptureScopeForApp(bundleID)
	if override := h.hints.Context.CaptureScopeOverride(); override != "" {
		captureScope = override
	}

	return strategy, captureScope
}

// CycleHint cycles through visible hints in hints mode, selecting the next or previous one.
// When executeAction is true, any pending action is performed on the selected hint
// (used by search confirmation). When false, only the cursor moves (used by the
// cycle_hint IPC action so users can browse results without triggering clicks).
func (h *Handler) CycleHint(ctx context.Context, backward bool, executeAction bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.appState.CurrentMode() != domain.ModeHints {
		return derrors.New(derrors.CodeInvalidInput, "cycle_hint requires hints mode")
	}

	if h.hints == nil || h.hints.Context == nil {
		return derrors.New(derrors.CodeActionFailed, "hints component not available")
	}

	manager := h.hints.Context.Manager()
	if manager == nil {
		return derrors.New(derrors.CodeActionFailed, "hints manager not available")
	}

	filteredHints := manager.FilteredHints()
	if len(filteredHints) == 0 {
		filteredHints = h.hints.Context.Hints().All()
	}

	if len(filteredHints) == 0 {
		return derrors.New(derrors.CodeActionFailed, "no hints available")
	}

	if h.cycleHintIndex >= len(filteredHints) {
		h.cycleHintIndex = len(filteredHints) - 1
	}

	switch {
	case h.cycleHintIndex < 0:
		h.cycleHintIndex = 0
		if backward {
			h.cycleHintIndex = len(filteredHints) - 1
		}
	default:
		if backward {
			if h.cycleHintIndex > 0 {
				h.cycleHintIndex--
			} else {
				h.cycleHintIndex = len(filteredHints) - 1
			}
		} else {
			if h.cycleHintIndex < len(filteredHints)-1 {
				h.cycleHintIndex++
			} else {
				h.cycleHintIndex = 0
			}
		}
	}

	selectedHint := filteredHints[h.cycleHintIndex]

	center := selectedHint.Element().Center()

	moveErr := h.actionService.MoveCursorToPoint(ctx, center)
	if moveErr != nil {
		h.logger.Error("Failed to move cursor during cycle_hint", zap.Error(moveErr))

		return derrors.New(derrors.CodeActionFailed, "failed to move cursor: "+moveErr.Error())
	}

	pendingAction := h.hints.Context.PendingAction()

	pendingModifier := h.hints.Context.PendingModifier()
	if pendingAction != nil && executeAction {
		repeat := h.hints.Context.Repeat()
		cursorFollowSelection := h.hints.Context.CursorFollowSelection()
		filterRoles := h.hints.Context.FilterRoles()
		filterTextContains := h.hints.Context.FilterTextContains()
		startWithSearch := h.hints.Context.StartWithSearch()
		strategyOverride := h.hints.Context.StrategyOverride()
		captureScopeOverride := h.hints.Context.CaptureScopeOverride()
		labelDirectionOverride := h.hints.Context.LabelDirectionOverride()
		splitWord := h.hints.Context.SplitWord()

		h.executeActionAtPoint(pendingAction, pendingModifier, center, repeat, func() {
			h.activateHintModeInternal(modecmd.Activation{
				Mode:               domain.ModeHints,
				FilterRoles:        filterRoles,
				FilterTextContains: filterTextContains,
				Search:             &startWithSearch,
				Strategy:           &strategyOverride,
				CaptureScope:       &captureScopeOverride,
				LabelDirection:     &labelDirectionOverride,
				SplitWord:          &splitWord,
				// OnExit is left nil to preserve the stored steps across
				// re-activation.
			})

			// Restore state so subsequent cycles continue to execute the action
			// Guard: only restore if repeat was originally set (mode is still hints).
			if repeat && h.appState.CurrentMode() == domain.ModeHints &&
				h.hints != nil && h.hints.Context != nil {
				h.hints.Context.SetPendingAction(pendingAction)
				h.hints.Context.SetPendingModifier(pendingModifier)
				h.hints.Context.SetRepeat(true)
				h.hints.Context.SetCursorFollowSelection(cursorFollowSelection)
				h.hints.Context.SetFilterRoles(filterRoles)
				h.hints.Context.SetFilterTextContains(filterTextContains)
				h.hints.Context.SetStartWithSearch(startWithSearch)
				h.hints.Context.SetStrategyOverride(strategyOverride)
				h.hints.Context.SetCaptureScopeOverride(captureScopeOverride)
				h.hints.Context.SetLabelDirectionOverride(labelDirectionOverride)
				h.hints.Context.SetSplitWord(splitWord)
			}
		})
	}

	return nil
}

func (h *handlerState) startHintSearch() error {
	if h.appState.CurrentMode() != domain.ModeHints {
		return derrors.New(derrors.CodeInvalidInput, "search_hints requires hints mode")
	}

	if h.hints == nil || h.hints.Context == nil {
		return derrors.New(derrors.CodeActionFailed, "hints component not available")
	}

	if h.hints.Context.SourceHints() == nil {
		return derrors.New(derrors.CodeActionFailed, "hints not available")
	}

	h.stopHintSearchTextInput(true)
	h.hints.Context.SetSearchQuery("")
	h.hints.Context.SetSearchActive(true)

	if h.hints.Context.HideOnEmptySearch() {
		// When hide-on-empty-search is active, hide all hints initially.
		// Hints will appear as the user types a query.
		setHintsErr := h.hints.Context.ClearVisibleHints()
		if setHintsErr != nil {
			return setHintsErr
		}
	} else {
		setHintsErr := h.hints.Context.SetVisibleHints(
			h.hints.Context.SourceHints(),
		)
		if setHintsErr != nil {
			return setHintsErr
		}
	}

	h.cycleHintIndex = -1
	h.drawHintSearchInput()

	if h.textInput == nil {
		return nil
	}

	// The IME field sits over the drawn search input, so its placement is
	// asked for rather than derived a second time here.
	bounds := h.hintSearchBounds()
	if bounds.Empty() {
		// The overlay put no box on screen — it draws none, or it could not
		// place the anchor it was configured with. Handing the platform's field
		// the keyboard anyway would put an invisible input somewhere the user
		// is not looking; the query keeps arriving through the event tap's key
		// stream instead, which is what every overlay without a search box
		// already relies on.
		//
		// That promise only holds if the tap is on, and this method opened by
		// stopping any live session *without* re-enabling it — on the
		// assumption that the session about to start would want it off. There
		// is no session now, so give the keyboard back or the search has no way
		// left to receive a key.
		h.stopHintSearchTextInput(false)
		h.logger.Debug("Hint search text input skipped: no search input on screen")

		return nil
	}

	textInputFrame := ports.TextInputFrame{
		X:      bounds.Min.X,
		Y:      bounds.Min.Y,
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
	}

	started, _ := h.textInput.StartHintSearchSession(
		h.ctx,
		ports.TextInputCallbacks{
			OnQueryChanged: func(query string) {
				h.outer.mu.Lock()
				defer h.outer.mu.Unlock()

				if h.appState.CurrentMode() != domain.ModeHints || h.hints == nil ||
					h.hints.Context == nil {
					return
				}

				if !h.hints.Context.SearchActive() {
					return
				}

				h.hints.Context.SetSearchQuery(query)
				h.applyHintSearchFilter()
			},
			OnConfirm: func() {
				h.outer.mu.Lock()
				defer h.outer.mu.Unlock()

				if h.appState.CurrentMode() != domain.ModeHints {
					return
				}

				h.confirmHintSearch()
			},
			OnCancel: func() {
				h.outer.mu.Lock()
				defer h.outer.mu.Unlock()

				if h.appState.CurrentMode() != domain.ModeHints {
					return
				}

				h.cancelHintSearch()
			},
		},
		textInputFrame,
	)

	if started {
		h.hintSearchTextInputActive = true

		if h.hasEventTap() {
			h.disableEventTap()
			h.hintSearchEventTapDisabled = true
		}
	}

	return nil
}

func (h *handlerState) stopHintSearchTextInput(keepEventTapDisabled bool) {
	if h.hintSearchTextInputActive && h.textInput != nil {
		// Use Background context since this may be called during cleanup,
		// after h.ctx has already been canceled.
		_ = h.textInput.StopHintSearchSession(context.Background())
	}

	h.hintSearchTextInputActive = false

	if h.hintSearchEventTapDisabled && h.hasEventTap() &&
		h.appState.CurrentMode() == domain.ModeHints && !keepEventTapDisabled {
		h.enableEventTap()
		h.hintSearchEventTapDisabled = false
	}
}
