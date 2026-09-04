package modes

import (
	"context"
	"image"
	"testing"

	"go.uber.org/zap"

	"github.com/y3owk1n/neru/internal/app/components"
	hintscomponent "github.com/y3owk1n/neru/internal/app/components/hints"
	"github.com/y3owk1n/neru/internal/app/components/scroll"
	"github.com/y3owk1n/neru/internal/app/services"
	configpkg "github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/derrors"
	"github.com/y3owk1n/neru/internal/domain"
	"github.com/y3owk1n/neru/internal/domain/element"
	"github.com/y3owk1n/neru/internal/domain/hint"
	"github.com/y3owk1n/neru/internal/domain/state"
	"github.com/y3owk1n/neru/internal/ports"
	portmocks "github.com/y3owk1n/neru/internal/ports/mocks"
)

// The geometry a monitor cycle distinguishes. The window is on the display the
// session started on, which is the point: the cursor moves to targetDisplay and
// focus does not follow it, so a window-scoped re-scan would read cycleWindow
// and find nothing that belongs on the monitor the user asked to look at.
var (
	cycleWindow        = image.Rect(0, 0, 800, 600)
	cycleTargetControl = image.Rect(2000, 100, 2100, 140)
	cycleSourceControl = image.Rect(100, 100, 200, 140)
)

// cycleHarness is a hints session wired to run refreshHintsForMonitorMove end to
// end, recording the regions its vision port was asked to read.
type cycleHarness struct {
	handler       *Handler
	regions       *[]image.Rectangle
	accessibility *portmocks.MockAccessibilityPort
}

// newCycleHarness builds a window-scoped hints session whose screen-reading pass
// returns controls at the given bounds, or fails with visionErr when non-nil.
func newCycleHarness(
	t *testing.T,
	controlBounds []image.Rectangle,
	visionErr error,
) cycleHarness {
	t.Helper()

	native := element.ResolveRolesForCurrentPlatform([]string{"button"}).Native
	if len(native) == 0 {
		t.Skipf("no native button role on this platform")
	}

	controls := make([]*element.Element, 0, len(controlBounds))

	for idx, bounds := range controlBounds {
		control, controlErr := element.NewElement(
			element.ID(string(rune('a'+idx))),
			bounds,
			element.Role(native[0]),
			element.WithVisionOnly(),
		)
		if controlErr != nil {
			t.Fatalf("NewElement: %v", controlErr)
		}

		controls = append(controls, control)
	}

	regions := &[]image.Rectangle{}

	vision := &portmocks.MockVisionPort{
		DetectElementsFunc: func(
			_ context.Context,
			region image.Rectangle,
			_ configpkg.HintsVisionConfig,
			_ bool,
		) ([]*element.Element, error) {
			*regions = append(*regions, region)

			if visionErr != nil {
				return nil, visionErr
			}

			return controls, nil
		},
	}

	system := &portmocks.MockSystemPort{}
	// The cursor has already warped, so the active screen is the target.
	system.ScreenBoundsFunc = func(context.Context) (image.Rectangle, error) {
		return targetDisplay, nil
	}
	system.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return cycleWindow, true, nil
	}

	cfg := configpkg.DefaultConfig()
	cfg.Hints.Strategy = domain.StrategyVision
	cfg.Hints.Scope = domain.HintScopeWindow

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
		hints:         &components.HintsComponent{Context: &hintscomponent.Context{}},
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

	return cycleHarness{
		handler:       handler,
		regions:       regions,
		accessibility: accessibility,
	}
}

// cycle runs the monitor-move refresh the way refreshActiveModeForMonitorMove
// does: under the handler lock, against bounds MoveMonitor already resolved.
func (h cycleHarness) cycle() {
	h.handler.mu.Lock()
	defer h.handler.mu.Unlock()

	h.handler.refreshHintsForMonitorMove(context.Background(), targetDisplay)
}

// TestHintsMonitorCycle_WidensAWindowScopedSessionToTheTargetMonitor is the
// reason the cycle sets the scope override at all. Read window-scoped, the pass
// would have been given cycleWindow — a window on the display just left.
func TestHintsMonitorCycle_WidensAWindowScopedSessionToTheTargetMonitor(t *testing.T) {
	t.Parallel()

	harness := newCycleHarness(t, []image.Rectangle{cycleTargetControl}, nil)

	harness.cycle()

	if got := *harness.regions; len(got) != 1 || got[0] != targetDisplay {
		t.Fatalf("vision regions = %v, want one %v", got, targetDisplay)
	}

	if got := harness.handler.hints.Context.ScopeOverride(); got != domain.HintScopeScreen {
		t.Errorf("ScopeOverride() = %q, want %q", got, domain.HintScopeScreen)
	}

	if harness.handler.appState.CurrentMode() != domain.ModeHints {
		t.Error("the cycle left hints mode")
	}

	if got := harness.handler.hints.Context.Hints().Count(); got != 1 {
		t.Errorf("hints on the target monitor = %d, want 1", got)
	}
}

// TestHintsMonitorCycle_HoldsTheSessionWhenTheTargetMonitorHasNothing is the
// sweep made possible: landing on a bare display clears the labels and keeps the
// mode, so the next press carries on to the display after it.
func TestHintsMonitorCycle_HoldsTheSessionWhenTheTargetMonitorHasNothing(t *testing.T) {
	t.Parallel()

	harness := newCycleHarness(t, nil, nil)

	harness.cycle()

	if got := harness.handler.appState.CurrentMode(); got != domain.ModeHints {
		t.Fatalf("mode after a cycle onto an empty monitor = %v, want hints", got)
	}

	if got := harness.handler.hints.Context.Hints(); got == nil || !got.Empty() {
		t.Error("the held session kept labels for a monitor with nothing on it")
	}

	if got := harness.handler.screenBounds; got != targetDisplay {
		t.Errorf("screen bounds = %v, want the target %v", got, targetDisplay)
	}
}

// TestHintsMonitorCycle_ClearsTheSourceCollectionToo is the bug the hold would
// have if it only cleared what is drawn: a `/` search reads the source
// collection, so labels left there would be selectable and would click on the
// display the user just left.
func TestHintsMonitorCycle_ClearsTheSourceCollectionToo(t *testing.T) {
	t.Parallel()

	harness := newCycleHarness(t, nil, nil)

	seeded := hint.NewCollection(nil)
	if setErr := harness.handler.hints.Context.SetHints(seeded); setErr != nil {
		t.Fatalf("SetHints: %v", setErr)
	}

	harness.cycle()

	if got := harness.handler.hints.Context.SourceHints(); got == nil || !got.Empty() {
		t.Error("the held session left a source collection a search could select from")
	}
}

// TestHintsMonitorCycle_HoldsWhenEveryHintFallsOutsideTheTargetMonitor covers
// the second empty answer: the pass returned something, but all of it belongs to
// a display the cycle moved away from.
func TestHintsMonitorCycle_HoldsWhenEveryHintFallsOutsideTheTargetMonitor(t *testing.T) {
	t.Parallel()

	harness := newCycleHarness(t, []image.Rectangle{cycleSourceControl}, nil)

	harness.cycle()

	if got := harness.handler.appState.CurrentMode(); got != domain.ModeHints {
		t.Fatalf("mode after every hint was filtered out = %v, want hints", got)
	}

	if got := harness.handler.hints.Context.Hints(); got == nil || !got.Empty() {
		t.Error("hints from another display survived the filter")
	}
}

// TestHintsMonitorCycle_HoldsWhenTheScreenPassItselfFails is the boundary worth
// pinning: a vision failure is deliberately not an error out of GenerateHints
// (it is logged, notified on CodeNotSupported, and the tree half's elements are
// returned — hint_service.go), so it arrives here as an empty answer and takes
// the hold like any other. The session stays open rather than dropping the user
// out over a scan that may work on the next display.
func TestHintsMonitorCycle_HoldsWhenTheScreenPassItselfFails(t *testing.T) {
	t.Parallel()

	harness := newCycleHarness(
		t,
		nil,
		derrors.New(derrors.CodeBridgeFailed, "the screen could not be read"),
	)

	harness.cycle()

	if got := harness.handler.appState.CurrentMode(); got != domain.ModeHints {
		t.Fatalf("mode after a failed screen pass = %v, want hints", got)
	}
}

// TestHintsMonitorCycle_StillExitsWhenTheWalkErrors is the line the hold does
// not cross. An empty monitor is an answer; a tree walk that failed is not, and
// it fails the same way on every display rather than only this one, so holding
// would leave the user pressing a key that can never come back with labels.
func TestHintsMonitorCycle_StillExitsWhenTheWalkErrors(t *testing.T) {
	t.Parallel()

	harness := newCycleHarness(t, []image.Rectangle{cycleTargetControl}, nil)

	// The accessibility walk is the half whose failures do surface as an error
	// from GenerateHints, so the session is put on the strategy that uses it.
	harness.handler.hints.Context.SetStrategyOverride(domain.StrategyAXTree)
	harness.accessibility.ClickableElementsFunc = func(
		context.Context,
		ports.ElementFilter,
	) ([]*element.Element, error) {
		return nil, derrors.New(derrors.CodeBridgeFailed, "the tree could not be walked")
	}

	harness.cycle()

	if got := harness.handler.appState.CurrentMode(); got != domain.ModeIdle {
		t.Fatalf("mode after a failed walk = %v, want idle", got)
	}
}

// TestHintsMonitorCycle_SingleMonitorLeavesTheSessionAlone matters because the
// keys are bound by default: on a one-display machine pressing them must be inert
// rather than disruptive. The target resolves before the frame is taken down, so
// nothing is cleared and no re-scan is paid for.
func TestHintsMonitorCycle_SingleMonitorLeavesTheSessionAlone(t *testing.T) {
	t.Parallel()

	harness := newCycleHarness(t, []image.Rectangle{cycleTargetControl}, nil)

	system, ok := harness.handler.system.(*portmocks.MockSystemPort)
	if !ok {
		t.Fatal("the harness system port is not the mock")
	}

	system.ScreenNamesFunc = func(context.Context) ([]string, error) {
		return []string{"the only display"}, nil
	}

	err := harness.handler.MoveMonitor(context.Background(), MonitorDirectionNext)
	if err == nil {
		t.Fatal("MoveMonitor() accepted a cycle on a single-monitor machine")
	}

	if got := harness.handler.appState.CurrentMode(); got != domain.ModeHints {
		t.Errorf("mode after a single-monitor cycle = %v, want hints", got)
	}

	if got := *harness.regions; len(got) != 0 {
		t.Errorf("a refused cycle re-scanned %v, want no scan at all", got)
	}

	if got := harness.handler.hints.Context.ScopeOverride(); got != "" {
		t.Errorf("a refused cycle widened the scope to %q", got)
	}
}

// TestHintsMonitorCycle_StillExitsWithoutAHintService pins the other genuine
// failure, which cannot regenerate anything at all.
func TestHintsMonitorCycle_StillExitsWithoutAHintService(t *testing.T) {
	t.Parallel()

	harness := newCycleHarness(t, nil, nil)
	harness.handler.hintService = nil

	harness.cycle()

	if got := harness.handler.appState.CurrentMode(); got != domain.ModeIdle {
		t.Fatalf("mode with no hint service = %v, want idle", got)
	}
}
