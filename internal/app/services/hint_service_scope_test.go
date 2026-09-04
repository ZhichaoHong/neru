package services_test

import (
	"context"
	"image"
	"testing"

	"github.com/y3owk1n/neru/internal/adapter/logger"
	"github.com/y3owk1n/neru/internal/app/services"
	"github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/derrors"
	"github.com/y3owk1n/neru/internal/domain"
	"github.com/y3owk1n/neru/internal/domain/element"
	"github.com/y3owk1n/neru/internal/domain/hint"
	"github.com/y3owk1n/neru/internal/ports/mocks"
)

// scopeBounds are the two rectangles every test below distinguishes: a window
// that is a quarter of the monitor it sits on.
var (
	scopeWindowBounds = image.Rect(0, 0, 400, 300)
	scopeScreenBounds = image.Rect(0, 0, 800, 600)
)

// newScopeService builds a hint service whose only interesting behavior is
// which region it hands the vision port. focusedAsked, when non-nil, records
// that the focused window was consulted at all.
func newScopeService(
	t *testing.T,
	scope string,
	vision *mockVisionPort,
	focusedAsked *bool,
) *services.HintService {
	t.Helper()

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		if focusedAsked != nil {
			*focusedAsked = true
		}

		return scopeWindowBounds, true, nil
	}
	mockSystem.ScreenBoundsFunc = func(context.Context) (image.Rectangle, error) {
		return scopeScreenBounds, nil
	}

	generator, generatorErr := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	if generatorErr != nil {
		t.Fatalf("NewAlphabetGenerator: %v", generatorErr)
	}

	return services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{
			Scope:          scope,
			ClickableRoles: []string{string(element.SemanticButton)},
		},
		logger.Get(),
		vision,
	)
}

// TestHintService_ScopeScreenReadsTheWholeMonitor is the point of the option:
// the region the OCR pass is given is the monitor rather than the window.
//
// It also pins that the focused window is not consulted. A scope that was asked
// for deliberately does not depend on whether a window is focused, so asking
// would only add a way for the answer to fail.
func TestHintService_ScopeScreenReadsTheWholeMonitor(t *testing.T) {
	vision := &mockVisionPort{}
	focusedAsked := false
	service := newScopeService(t, domain.HintScopeScreen, vision, &focusedAsked)

	_, err := service.GenerateHints(
		context.Background(), nil, nil, "", domain.StrategyVision, "", false, "",
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if got := vision.detectRegions; len(got) != 1 || got[0] != scopeScreenBounds {
		t.Errorf("vision regions = %v, want one %v", got, scopeScreenBounds)
	}

	if focusedAsked {
		t.Error("the focused window was consulted for a scope that does not depend on it")
	}
}

// TestHintService_ScopeWindowReadsTheFocusedWindow pins the default, so a
// configuration that says nothing keeps the region it has always read.
func TestHintService_ScopeWindowReadsTheFocusedWindow(t *testing.T) {
	for _, scope := range []string{domain.HintScopeWindow, ""} {
		t.Run("scope="+scope, func(t *testing.T) {
			vision := &mockVisionPort{}
			service := newScopeService(t, scope, vision, nil)

			_, err := service.GenerateHints(
				context.Background(), nil, nil, "", domain.StrategyVision, "", false, "",
			)
			if err != nil {
				t.Fatalf("GenerateHints() unexpected error: %v", err)
			}

			if got := vision.detectRegions; len(got) != 1 || got[0] != scopeWindowBounds {
				t.Errorf("vision regions = %v, want one %v", got, scopeWindowBounds)
			}
		})
	}
}

// TestHintService_ScopeOverrideWidensAWindowScopedConfig is what the
// expand_hint_scope action rides on: the override outranks hints.scope for the
// one activation that carries it.
func TestHintService_ScopeOverrideWidensAWindowScopedConfig(t *testing.T) {
	vision := &mockVisionPort{}
	service := newScopeService(t, domain.HintScopeWindow, vision, nil)

	_, err := service.GenerateHints(
		context.Background(), nil, nil, "", domain.StrategyVision, "", false,
		domain.HintScopeScreen,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if got := vision.detectRegions; len(got) != 1 || got[0] != scopeScreenBounds {
		t.Errorf("vision regions = %v, want one %v", got, scopeScreenBounds)
	}
}

// TestHintService_ScopeReachesTheContourDetector covers the other screen-reading
// strategy, which resolves its region through the same path.
func TestHintService_ScopeReachesTheContourDetector(t *testing.T) {
	vision := &mockVisionPort{}
	service := newScopeService(t, domain.HintScopeScreen, vision, nil)

	_, err := service.GenerateHints(
		context.Background(), nil, nil, "", domain.StrategyContour, "", false, "",
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if got := vision.contourRegions; len(got) != 1 || got[0] != scopeScreenBounds {
		t.Errorf("contour regions = %v, want one %v", got, scopeScreenBounds)
	}
}

// TestHintService_ScopeScreenSurvivesAFocusedWindowThatCannotBeRead is the
// failure mode the short-circuit removes: a window query that errors used to
// decide the region, and now cannot, because it is never made.
func TestHintService_ScopeScreenSurvivesAFocusedWindowThatCannotBeRead(t *testing.T) {
	vision := &mockVisionPort{}

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rectangle{}, false, derrors.New(derrors.CodeBridgeFailed, "no window")
	}
	mockSystem.ScreenBoundsFunc = func(context.Context) (image.Rectangle, error) {
		return scopeScreenBounds, nil
	}

	generator, generatorErr := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	if generatorErr != nil {
		t.Fatalf("NewAlphabetGenerator: %v", generatorErr)
	}

	service := services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{
			Scope:          domain.HintScopeScreen,
			ClickableRoles: []string{string(element.SemanticButton)},
		},
		logger.Get(),
		vision,
	)

	_, err := service.GenerateHints(
		context.Background(), nil, nil, "", domain.StrategyVision, "", false, "",
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if got := vision.detectRegions; len(got) != 1 || got[0] != scopeScreenBounds {
		t.Errorf("vision regions = %v, want one %v", got, scopeScreenBounds)
	}
}
