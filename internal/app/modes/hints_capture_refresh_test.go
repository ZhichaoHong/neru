package modes

import (
	"context"
	"image"
	"slices"
	"testing"

	"go.uber.org/zap"

	"github.com/y3owk1n/neru/internal/app/components"
	"github.com/y3owk1n/neru/internal/app/components/hints"
	"github.com/y3owk1n/neru/internal/app/components/scroll"
	"github.com/y3owk1n/neru/internal/app/services"
	configpkg "github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/domain"
	"github.com/y3owk1n/neru/internal/domain/element"
	"github.com/y3owk1n/neru/internal/domain/modecmd"
	"github.com/y3owk1n/neru/internal/domain/state"
	"github.com/y3owk1n/neru/internal/ports"
	portmocks "github.com/y3owk1n/neru/internal/ports/mocks"
)

// TestActivateHints_RefreshClearsTheOverlayOnlyForScreenReadingStrategies pins
// the ordering the screen-reading strategies depend on and the flash-free redraw
// axtree keeps.
//
// A refresh deliberately leaves the previous activation's labels on screen so
// the redraw does not blink. Vision and contour read the screen, so for them
// those labels are input: they come back as targets of their own, and they cover
// what they point at. The accessibility tree cannot see the overlay, so nothing
// there needs clearing and the blink would be paid for nothing.
func TestActivateHints_RefreshClearsTheOverlayOnlyForScreenReadingStrategies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		strategy  string
		wantOrder []string
	}{
		{
			name:     "vision clears before reading the screen",
			strategy: domain.StrategyVision,
			// The clear comes first, then the supplementary tree lookup the
			// vision strategy still makes, then the screen read itself.
			wantOrder: []string{"clear", "ax", "vision"},
		},
		{
			name:      "contour clears before reading the screen",
			strategy:  domain.StrategyContour,
			wantOrder: []string{"clear", "ax", "contour"},
		},
		{
			name:      "ax tree keeps its labels",
			strategy:  domain.StrategyAXTree,
			wantOrder: []string{"ax"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var callOrder []string

			overlayPort := &portmocks.MockOverlayPort{
				ClearFrameFunc: func(_ context.Context) error {
					callOrder = append(callOrder, "clear")

					return nil
				},
			}

			accessibility := &portmocks.MockAccessibilityPort{
				ClickableElementsFunc: func(
					_ context.Context,
					_ ports.ElementFilter,
				) ([]*element.Element, error) {
					callOrder = append(callOrder, "ax")

					return nil, nil
				},
			}

			vision := &portmocks.MockVisionPort{
				DetectElementsFunc: func(
					_ context.Context,
					_ image.Rectangle,
					_ configpkg.HintsVisionConfig,
					_ bool,
				) ([]*element.Element, error) {
					callOrder = append(callOrder, "vision")

					return nil, nil
				},
				DetectContoursFunc: func(
					_ context.Context,
					_ image.Rectangle,
				) ([]*element.Element, error) {
					callOrder = append(callOrder, "contour")

					return nil, nil
				},
			}

			system := &portmocks.MockSystemPort{}

			cfg := configpkg.DefaultConfig()
			cfg.Hints.Strategy = test.strategy

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
					nil,
					cfg.Hints,
					zap.NewNop(),
					vision,
				),
			})

			handler.activateHintModeInternal(modecmd.Activation{Mode: domain.ModeHints})

			// Everything after the last element read is teardown: both mocks
			// return no elements, so the activation abandons and clears again on
			// the way out. Only what happened before matters here.
			lastRead := max(
				slices.Index(callOrder, "ax"),
				slices.Index(callOrder, "vision"),
				slices.Index(callOrder, "contour"),
			)
			if lastRead < 0 {
				t.Fatalf("activation never read any elements, call order %v", callOrder)
			}

			got := callOrder[:lastRead+1]

			// The whole order, not just whether a clear happened: a clear that
			// lands after something already read the screen protects nothing.
			if !slices.Equal(got, test.wantOrder) {
				t.Fatalf("call order = %v, want %v", got, test.wantOrder)
			}
		})
	}
}
