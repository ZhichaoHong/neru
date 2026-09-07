package modes

import (
	"context"
	"image"
	"testing"

	"go.uber.org/zap"

	"github.com/y3owk1n/neru/internal/app/components"
	"github.com/y3owk1n/neru/internal/app/components/hints"
	"github.com/y3owk1n/neru/internal/app/components/scroll"
	"github.com/y3owk1n/neru/internal/app/services"
	configpkg "github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/domain"
	"github.com/y3owk1n/neru/internal/domain/element"
	"github.com/y3owk1n/neru/internal/domain/hint"
	"github.com/y3owk1n/neru/internal/domain/state"
	portmocks "github.com/y3owk1n/neru/internal/ports/mocks"
)

// The screen and the window on it every test below distinguishes, plus a
// control that sits on the screen but outside the window - the thing a user
// expands the scope to reach.
var (
	expandScreenBounds  = image.Rect(0, 0, 800, 600)
	expandWindowBounds  = image.Rect(0, 0, 400, 300)
	expandControlBounds = image.Rect(500, 400, 560, 420)
)

// expandHarness is one hints handler wired to record the regions its vision
// port was asked to read.
type expandHarness struct {
	handler *Handler
	regions *[]image.Rectangle
}

func newExpandHarness(t *testing.T, configuredScope string) expandHarness {
	t.Helper()

	regions := &[]image.Rectangle{}

	native := element.ResolveRolesForCurrentPlatform([]string{"button"}).Native
	if len(native) == 0 {
		t.Skipf("no native button role on this platform")
	}

	control, controlErr := element.NewElement(
		element.ID("expand-target"),
		expandControlBounds,
		element.Role(native[0]),
		element.WithVisionOnly(),
	)
	if controlErr != nil {
		t.Fatalf("NewElement: %v", controlErr)
	}

	vision := &portmocks.MockVisionPort{
		DetectElementsFunc: func(
			_ context.Context,
			region image.Rectangle,
			_ configpkg.HintsVisionConfig,
			_ bool,
		) ([]*element.Element, error) {
			*regions = append(*regions, region)

			return []*element.Element{control}, nil
		},
	}

	system := &portmocks.MockSystemPort{}
	system.ScreenBoundsFunc = func(context.Context) (image.Rectangle, error) {
		return expandScreenBounds, nil
	}
	system.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return expandWindowBounds, true, nil
	}

	cfg := configpkg.DefaultConfig()
	cfg.Hints.Strategy = domain.StrategyVision
	cfg.Hints.CaptureScope = configuredScope

	generator, generatorErr := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	if generatorErr != nil {
		t.Fatalf("NewAlphabetGenerator: %v", generatorErr)
	}

	accessibility := &portmocks.MockAccessibilityPort{}
	overlayPort := &portmocks.MockOverlayPort{}

	appState := state.NewAppState()
	appState.SetMode(domain.ModeHints)

	handler := newHandlerWithState(handlerState{
		logger:        zap.NewNop(),
		ctx:           context.Background(),
		config:        cfg,
		appState:      appState,
		modifierState: state.NewModifierState(),
		cursorState:   state.NewCursorState(),
		scroll:        &components.ScrollComponent{Context: &scroll.Context{}},
		overlayPort:   overlayPort,
		system:        system,
		hints:         &components.HintsComponent{Context: &hints.Context{}},
		actionService: services.NewActionService(
			accessibility,
			overlayPort,
			system,
			zap.NewNop(),
		),
		hintService: services.NewHintService(
			accessibility,
			overlayPort,
			system,
			generator,
			cfg.Hints,
			zap.NewNop(),
			vision,
		),
	})

	return expandHarness{handler: handler, regions: regions}
}

// TestExpandHintScope_WidensTheSessionAndRescans is the action's whole job: the
// re-scan reads the screen rather than the window, and the labels that come
// back include the control that was never in the window.
func TestExpandHintScope_WidensTheSessionAndRescans(t *testing.T) {
	t.Parallel()

	harness := newExpandHarness(t, domain.CaptureScopeWindow)

	expanded, err := harness.handler.ExpandHintScope()
	if err != nil {
		t.Fatalf("ExpandHintScope() unexpected error: %v", err)
	}

	if !expanded {
		t.Fatal("ExpandHintScope() reported no change on a window-scoped session")
	}

	if got := *harness.regions; len(got) != 1 || got[0] != expandScreenBounds {
		t.Fatalf("vision regions = %v, want one %v", got, expandScreenBounds)
	}

	if got := harness.handler.hints.Context.CaptureScopeOverride(); got != domain.CaptureScopeScreen {
		t.Errorf("CaptureScopeOverride() = %q, want %q", got, domain.CaptureScopeScreen)
	}

	if harness.handler.appState.CurrentMode() != domain.ModeHints {
		t.Error("the expansion left hints mode")
	}
}

// TestExpandHintScope_IsIdempotent pins the one-way, sticky shape: a second
// press is not an error and does not pay for a scan that would return the same
// answer.
func TestExpandHintScope_IsIdempotent(t *testing.T) {
	t.Parallel()

	harness := newExpandHarness(t, domain.CaptureScopeWindow)

	_, err := harness.handler.ExpandHintScope()
	if err != nil {
		t.Fatalf("ExpandHintScope() unexpected error: %v", err)
	}

	scansAfterFirst := len(*harness.regions)

	expanded, err := harness.handler.ExpandHintScope()
	if err != nil {
		t.Fatalf("second ExpandHintScope() unexpected error: %v", err)
	}

	if expanded {
		t.Error("second ExpandHintScope() reported a change")
	}

	if got := len(*harness.regions); got != scansAfterFirst {
		t.Errorf("scans = %d, want the %d the first call made", got, scansAfterFirst)
	}
}

// TestExpandHintScope_LeavesAnAlreadyScreenScopedSessionAlone covers the
// configuration that already reads the screen: there is nothing to widen, so
// the key does nothing rather than re-scanning.
func TestExpandHintScope_LeavesAnAlreadyScreenScopedSessionAlone(t *testing.T) {
	t.Parallel()

	harness := newExpandHarness(t, domain.CaptureScopeScreen)

	expanded, err := harness.handler.ExpandHintScope()
	if err != nil {
		t.Fatalf("ExpandHintScope() unexpected error: %v", err)
	}

	if expanded {
		t.Error("ExpandHintScope() reported a change on a screen-scoped configuration")
	}

	if got := *harness.regions; len(got) != 0 {
		t.Errorf("vision regions = %v, want none", got)
	}
}

// TestExpandHintScope_RefusesUnderAStrategyTheScopeDoesNotBound covers the
// combination that cannot do anything: axtree walks the focused window whatever
// the scope says, so widening it would cost a re-scan and hint the same window.
func TestExpandHintScope_RefusesUnderAStrategyTheScopeDoesNotBound(t *testing.T) {
	t.Parallel()

	harness := newExpandHarness(t, domain.CaptureScopeWindow)

	harness.handler.mu.Lock()
	harness.handler.config.Hints.Strategy = domain.StrategyAXTree
	harness.handler.mu.Unlock()

	_, err := harness.handler.ExpandHintScope()
	if err == nil {
		t.Fatal("ExpandHintScope() accepted a call under axtree")
	}

	if got := *harness.regions; len(got) != 0 {
		t.Errorf("vision regions = %v, want none", got)
	}

	if got := harness.handler.hints.Context.CaptureScopeOverride(); got != "" {
		t.Errorf("CaptureScopeOverride() = %q, want the refusal to leave it unset", got)
	}
}

// TestExpandHintScope_RefusesOutsideHintsMode is the guard every hints-only
// action carries: the action reaches the handler from a hotkey table or raw IPC,
// and neither can promise a mode is open.
func TestExpandHintScope_RefusesOutsideHintsMode(t *testing.T) {
	t.Parallel()

	harness := newExpandHarness(t, domain.CaptureScopeWindow)
	harness.handler.appState.SetMode(domain.ModeIdle)

	_, err := harness.handler.ExpandHintScope()
	if err == nil {
		t.Fatal("ExpandHintScope() accepted a call with no hints session")
	}
}

// TestExpandHintScope_SurvivesAScreenChange is the sticky half stated as
// behavior: the display changing under an expanded session re-scans at the
// wider scope rather than falling back to the configured one.
func TestExpandHintScope_SurvivesAScreenChange(t *testing.T) {
	t.Parallel()

	harness := newExpandHarness(t, domain.CaptureScopeWindow)

	_, err := harness.handler.ExpandHintScope()
	if err != nil {
		t.Fatalf("ExpandHintScope() unexpected error: %v", err)
	}

	harness.handler.mu.Lock()
	refreshed := harness.handler.refreshHintsForScreenChange(context.Background())
	harness.handler.mu.Unlock()

	if !refreshed {
		t.Fatal("the screen-change refresh put nothing on screen")
	}

	regions := *harness.regions
	if len(regions) != 2 {
		t.Fatalf("vision regions = %v, want two", regions)
	}

	if regions[1] != expandScreenBounds {
		t.Errorf("the refresh read %v, want the expanded %v", regions[1], expandScreenBounds)
	}
}
