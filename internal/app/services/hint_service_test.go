package services_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/y3owk1n/neru/internal/adapter/logger"
	"github.com/y3owk1n/neru/internal/app/services"
	"github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/derrors"
	"github.com/y3owk1n/neru/internal/domain"
	"github.com/y3owk1n/neru/internal/domain/element"
	"github.com/y3owk1n/neru/internal/domain/hint"
	"github.com/y3owk1n/neru/internal/ports"
	"github.com/y3owk1n/neru/internal/ports/mocks"
)

func TestHintService_RefreshHints(t *testing.T) {
	tests := []struct {
		name           string
		overlayVisible bool
		expectRefresh  bool
		refreshError   error
		wantErr        bool
	}{
		{
			name:           "refresh when visible",
			overlayVisible: true,
			expectRefresh:  true,
			refreshError:   nil,
			wantErr:        false,
		},
		{
			name:           "skip refresh when not visible",
			overlayVisible: false,
			expectRefresh:  false,
			refreshError:   nil,
			wantErr:        false,
		},
		{
			name:           "refresh error when visible",
			overlayVisible: true,
			expectRefresh:  true,
			refreshError:   derrors.New(derrors.CodeOverlayFailed, "overlay refresh failed"),
			wantErr:        true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			mockAcc := &mocks.MockAccessibilityPort{}
			mockOverlay := &mocks.MockOverlayPort{}

			refreshCalled := false
			mockOverlay.IsVisibleFunc = func() bool {
				return testCase.overlayVisible
			}
			mockOverlay.RefreshFunc = func(_ context.Context) error {
				refreshCalled = true

				return testCase.refreshError
			}

			generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)
			logger := logger.Get()

			service := services.NewHintService(
				mockAcc,
				mockOverlay,
				&mocks.MockSystemPort{},
				generator,
				config.HintsConfig{},
				logger,
				nil,
			)

			ctx := context.Background()
			refreshHintsErr := service.RefreshHints(ctx)

			if (refreshHintsErr != nil) != testCase.wantErr {
				t.Errorf("RefreshHints() error = %v, wantErr %v", refreshHintsErr, testCase.wantErr)
			}

			if refreshCalled != testCase.expectRefresh {
				t.Errorf("Refresh called = %v, want %v", refreshCalled, testCase.expectRefresh)
			}
		})
	}
}

// TestHintService_DetectMissionControlFollowsTheConfigInForce pins that the flag
// the collection decides on is read per activation from the configuration now in
// force. The accessibility adapter used to hold its own copy, taken at
// construction, so flipping hints.detect_mission_control and reloading left every
// later collection acting on the value the daemon booted with — in both
// directions.
//
// The assertion is on what the port was asked for, because that is the whole of
// what the service contributes: it is the disagreement between the config in
// force and the collection's answer that was the bug.
func TestHintService_DetectMissionControlFollowsTheConfigInForce(t *testing.T) {
	var asked []bool

	mockAcc := &mocks.MockAccessibilityPort{}
	mockAcc.ClickableElementsFunc = func(
		_ context.Context,
		filter ports.ElementFilter,
	) ([]*element.Element, error) {
		asked = append(asked, filter.DetectMissionControl)

		return []*element.Element{mustNewElement("btn", image.Rect(0, 0, 20, 20))}, nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)
	service := services.NewHintService(
		mockAcc,
		&mocks.MockOverlayPort{},
		&mocks.MockSystemPort{},
		generator,
		config.HintsConfig{DetectMissionControl: false},
		logger.Get(),
		nil,
	)

	generate := func() {
		t.Helper()

		_, err := service.GenerateHints(
			context.Background(),
			nil,
			nil,
			"com.example.app",
			domain.StrategyAXTree,
			"",
			false,
		)
		if err != nil {
			t.Fatalf("GenerateHints() unexpected error: %v", err)
		}
	}

	generate()

	service.UpdateConfig(config.HintsConfig{DetectMissionControl: true})
	generate()

	service.UpdateConfig(config.HintsConfig{DetectMissionControl: false})
	generate()

	if want := []bool{false, true, false}; !slices.Equal(asked, want) {
		t.Errorf("filter.DetectMissionControl per activation = %v, want %v", asked, want)
	}
}

func TestHintService_GenerateHintsVisionCombinesSupplementaryAndWindowElements(
	t *testing.T,
) {
	supplementElement := mustNewElement("menubar", image.Rect(10, 0, 60, 20))
	windowElement := mustNewElement("window", image.Rect(10, 40, 60, 90))

	mockAcc := &mocks.MockAccessibilityPort{}
	mockAcc.ClickableElementsFunc = func(
		_ context.Context,
		filter ports.ElementFilter,
	) ([]*element.Element, error) {
		if !filter.SkipWindowElements {
			t.Error("accessibility should not collect window elements when using vision strategy")

			return nil, nil
		}

		return []*element.Element{supplementElement}, nil
	}

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 200, 200), true, nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)
	service := services.NewHintService(
		mockAcc,
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{
			ClickableRoles:                []string{string(element.SemanticButton)},
			IncludeMenubarHints:           true,
			AdditionalMenubarHintsTargets: []string{"Clock"},
			IncludeDockHints:              true,
			IncludeNCHints:                true,
			IncludeStageManagerHints:      true,
			IncludePIPHints:               true,
			IncludeScreenCaptureHints:     true,
		},
		logger.Get(),
		&mockVisionPort{
			detectedElements: []*element.Element{windowElement},
		},
	)

	hints, err := service.GenerateHints(
		context.Background(),
		nil,
		nil,
		"com.example.app",
		domain.StrategyVision,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if len(hints) != 2 {
		t.Fatalf("GenerateHints() returned %d hints, want 2", len(hints))
	}

	seen := map[element.ID]int{}
	for _, generatedHint := range hints {
		seen[generatedHint.Element().ID()]++
	}

	if seen[supplementElement.ID()] != 1 {
		t.Errorf("supplementary element count = %d, want 1", seen[supplementElement.ID()])
	}

	if seen[windowElement.ID()] != 1 {
		t.Errorf("window element count = %d, want 1", seen[windowElement.ID()])
	}
}

// TestHintService_GenerateHintsHybridWalksTheWindowAndMergesTheTwoSets covers
// what separates hybrid from vision at the service: the tree is asked for the
// window as well, with the configured roles left in place, and a recognized
// region the tree already answered for does not survive into the hints.
func TestHintService_GenerateHintsHybridWalksTheWindowAndMergesTheTwoSets(
	t *testing.T,
) {
	treeButton := mustNewElement("tree_button", image.Rect(100, 100, 220, 132))
	recognizedLabel := mustVisionElement("recognized_label", image.Rect(136, 109, 184, 123))
	recognizedText := mustVisionElement("recognized_text", image.Rect(400, 400, 500, 440))

	mockAcc := &mocks.MockAccessibilityPort{}
	mockAcc.ClickableElementsFunc = func(
		_ context.Context,
		filter ports.ElementFilter,
	) ([]*element.Element, error) {
		if filter.SkipWindowElements {
			t.Error("hybrid must walk the window tree, not leave it to vision")
		}

		// With no --role the widened dedupe set collapses to the configured one, so
		// an unfiltered activation walks exactly the tree axtree would.
		configured := nativeRoles(element.SemanticButton)
		if !slices.Equal(filter.Roles, configured) {
			t.Errorf("tree call roles = %v, want the configured set %v", filter.Roles, configured)
		}

		return []*element.Element{treeButton}, nil
	}

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 800, 600), true, nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)
	service := services.NewHintService(
		mockAcc,
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{
			ClickableRoles: []string{string(element.SemanticButton)},
		},
		logger.Get(),
		&mockVisionPort{
			detectedElements: []*element.Element{recognizedLabel, recognizedText},
		},
	)

	hints, err := service.GenerateHints(
		context.Background(),
		nil,
		nil,
		"com.example.app",
		domain.StrategyHybrid,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	got := make(map[element.ID]bool, len(hints))
	for _, generatedHint := range hints {
		got[generatedHint.Element().ID()] = true
	}

	if !got[treeButton.ID()] {
		t.Error("the tree element is missing; hybrid never drops one")
	}

	if !got[recognizedText.ID()] {
		t.Error("the recognized text away from the tree is missing; that is what hybrid adds")
	}

	if got[recognizedLabel.ID()] {
		t.Error("the label recognized inside the tree button survived; the tree wins that overlap")
	}
}

// TestHintService_GenerateHintsHybridDedupesAgainstRolesTheActivationExcluded is
// the rule that a role filter narrows what gets hinted, never what the merge may
// deduplicate against.
//
// The behavior it prevents was measured, not hypothetical: `hybrid --role=button`
// on an Explorer window produced 28 vision-only elements against plain `hybrid`'s
// 4, because an OCR region inside a link or a text field met no tree element to be
// dropped by and the classifier had already guessed a role that passed the filter.
// A narrowing filter widening the output 7x is not a filter.
//
// Every recognized element here carries the requested role while the tree element
// covering one of them does not, so the merge is the only thing that can drop it.
func TestHintService_GenerateHintsHybridDedupesAgainstRolesTheActivationExcluded(
	t *testing.T,
) {
	treeButton := mustRoledElement("tree_button", image.Rect(100, 100, 220, 132), nativeButtonRole)
	treeLink := mustRoledElement("tree_link", image.Rect(300, 100, 380, 130), nativeLinkRole)

	recognizedInButton := mustRoledElement(
		"recognized_in_button",
		image.Rect(136, 109, 184, 123),
		nativeLinkRole,
		element.WithVisionOnly(),
	)
	recognizedAlone := mustRoledElement(
		"recognized_alone",
		image.Rect(400, 400, 500, 440),
		nativeLinkRole,
		element.WithVisionOnly(),
	)

	mockAcc := &mocks.MockAccessibilityPort{}
	mockAcc.ClickableElementsFunc = func(
		_ context.Context,
		filter ports.ElementFilter,
	) ([]*element.Element, error) {
		for _, want := range []element.Role{nativeLinkRole, nativeButtonRole} {
			if !slices.Contains(filter.Roles, want) {
				t.Errorf("tree call roles %v miss %q; the dedupe set is the union of "+
					"the activation's roles and the configured ones", filter.Roles, want)
			}
		}

		// The real adapter applies the filter during the walk, so a mock that
		// returned everything would hide whether the widening reached it.
		return retainMatchingElements(filter, treeButton, treeLink), nil
	}

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 800, 600), true, nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)
	service := services.NewHintService(
		mockAcc,
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{
			ClickableRoles: []string{
				string(element.SemanticButton),
				string(element.SemanticLink),
			},
		},
		logger.Get(),
		&mockVisionPort{
			detectedElements: []*element.Element{recognizedInButton, recognizedAlone},
		},
	)

	hints, err := service.GenerateHints(
		context.Background(),
		[]string{string(element.SemanticLink)},
		nil,
		"com.example.app",
		domain.StrategyHybrid,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	got := make(map[element.ID]bool, len(hints))
	for _, generatedHint := range hints {
		got[generatedHint.Element().ID()] = true
	}

	if got[recognizedInButton.ID()] {
		t.Error("a recognized region inside a button survived --role=link; " +
			"the merge must dedupe against roles the activation excluded")
	}

	if !got[recognizedAlone.ID()] {
		t.Error("the recognized region no tree element covers is missing; that is what hybrid adds")
	}

	if got[treeButton.ID()] {
		t.Error("the excluded tree button was hinted; widening the walk must not widen the hints")
	}

	if !got[treeLink.ID()] {
		t.Error("the requested tree link is missing")
	}
}

// retainMatchingElements stands in for the role filtering the accessibility
// adapter performs during its own walk.
func retainMatchingElements(
	filter ports.ElementFilter,
	elements ...*element.Element,
) []*element.Element {
	kept := make([]*element.Element, 0, len(elements))

	for _, candidate := range elements {
		if filter.Matches(candidate) {
			kept = append(kept, candidate)
		}
	}

	return kept
}

// TestHintService_GenerateHintsSplitWordNeedsAScreenStrategy pins the gate that
// hybrid had to widen. --split-word describes recognized text, so it is
// meaningful under both strategies that read the screen and refused under the one
// that does not.
func TestHintService_GenerateHintsSplitWordNeedsAScreenStrategy(t *testing.T) {
	tests := []struct {
		strategy string
		refused  bool
	}{
		{domain.StrategyVision, false},
		{domain.StrategyHybrid, false},
		{domain.StrategyAXTree, true},
	}

	for _, test := range tests {
		t.Run(test.strategy, func(t *testing.T) {
			mockAcc := &mocks.MockAccessibilityPort{}
			mockAcc.ClickableElementsFunc = func(
				context.Context,
				ports.ElementFilter,
			) ([]*element.Element, error) {
				return nil, nil
			}

			mockSystem := &mocks.MockSystemPort{}
			mockSystem.FocusedWindowBoundsFunc = func(
				context.Context,
			) (image.Rectangle, bool, error) {
				return image.Rect(0, 0, 200, 200), true, nil
			}

			generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)
			service := services.NewHintService(
				mockAcc,
				&mocks.MockOverlayPort{},
				mockSystem,
				generator,
				config.HintsConfig{},
				logger.Get(),
				&mockVisionPort{},
			)

			_, err := service.GenerateHints(
				context.Background(),
				nil,
				nil,
				"com.example.app",
				test.strategy,
				"",
				true,
			)

			if test.refused && err == nil {
				t.Errorf("--split-word under %q was accepted, want it refused", test.strategy)
			}

			if !test.refused && err != nil {
				t.Errorf("--split-word under %q was refused: %v", test.strategy, err)
			}
		})
	}
}

func TestHintService_GenerateHintsVisionWithNilPortReturnsSupplementaryElements(
	t *testing.T,
) {
	supplementElement := mustNewElement("menubar", image.Rect(10, 0, 60, 20))

	mockAcc := &mocks.MockAccessibilityPort{}
	mockAcc.ClickableElementsFunc = func(
		_ context.Context,
		filter ports.ElementFilter,
	) ([]*element.Element, error) {
		if !filter.SkipWindowElements {
			t.Error("nil vision port should not trigger window AX collection")
		}

		return []*element.Element{supplementElement}, nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)
	service := services.NewHintService(
		mockAcc,
		&mocks.MockOverlayPort{},
		&mocks.MockSystemPort{},
		generator,
		config.HintsConfig{
			IncludeMenubarHints: true,
		},
		logger.Get(),
		nil,
	)

	hints, err := service.GenerateHints(
		context.Background(),
		nil,
		nil,
		"com.example.app",
		domain.StrategyVision,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if len(hints) != 1 {
		t.Fatalf("GenerateHints() returned %d hints, want 1", len(hints))
	}

	if hints[0].Element().ID() != supplementElement.ID() {
		t.Errorf("hint element = %q, want %q", hints[0].Element().ID(), supplementElement.ID())
	}
}

// TestHintService_GenerateHintsVisionNotifiesWhenTheStrategyIsUnavailable is
// about a failure a log line cannot fix.
//
// When vision detection reports CodeNotSupported the machine cannot run the
// strategy at all — no tesseract language data, no capture backend, a build
// with no engine in it — and the error names what to install. Swallowing that
// leaves the user pressing the hotkey and getting an overlay with nothing on
// it, since the supplementary elements the pipeline keeps are macOS surfaces
// with no counterpart elsewhere. The message has to reach a person, which is
// what ADR 0002 says a log line does not do.
func TestHintService_GenerateHintsVisionNotifiesWhenTheStrategyIsUnavailable(t *testing.T) {
	const missing = "install the tesseract eng language data"

	notified := make(chan string, 4)

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 200, 200), true, nil
	}
	mockSystem.ShowNotificationFunc = func(_ context.Context, _, message string) error {
		notified <- message

		return nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	service := services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{},
		logger.Get(),
		&mockVisionPort{detectErr: derrors.New(derrors.CodeNotSupported, missing)},
	)

	for range 3 {
		_, err := service.GenerateHints(
			context.Background(), nil, nil, "com.example.app", domain.StrategyVision, "", false,
		)
		if err != nil {
			t.Fatalf("GenerateHints() unexpected error: %v", err)
		}
	}

	select {
	case message := <-notified:
		if !strings.Contains(message, missing) {
			t.Errorf("notification %q does not carry what the error named", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("vision reported CodeNotSupported and the user was never told")
	}

	// Three activations, one notification: a user who keeps pressing the hotkey
	// is told once, not once per press.
	select {
	case extra := <-notified:
		t.Errorf("the same failure notified twice: %q", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestHintService_GenerateHintsVisionNoticeSurvivesTheActivationContext is the
// regression test for the way this notification is easiest to lose.
//
// Every caller that reaches vision detection under the mode handler's lock
// builds its context with a hint timeout and cancels it on return, microseconds
// after the notification is queued — and the Linux notification path honors a
// caller's deadline. A notice that carried that context would be canceled
// before the first send, which is also the send that dials the session bus, and
// the user would be told nothing.
func TestHintService_GenerateHintsVisionNoticeSurvivesTheActivationContext(t *testing.T) {
	// proceed holds the send until the activation context has been canceled,
	// so this observes the hazard rather than racing it.
	proceed := make(chan struct{})
	sent := make(chan error, 1)

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 200, 200), true, nil
	}
	mockSystem.ShowNotificationFunc = func(ctx context.Context, _, _ string) error {
		<-proceed

		sent <- ctx.Err()

		return nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	service := services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{},
		logger.Get(),
		&mockVisionPort{detectErr: derrors.New(derrors.CodeNotSupported, "no language data")},
	)

	ctx, cancel := context.WithCancel(context.Background())

	_, err := service.GenerateHints(
		ctx, nil, nil, "com.example.app", domain.StrategyVision, "", false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	// What every locked caller does on return.
	cancel()
	close(proceed)

	select {
	case ctxErr := <-sent:
		if ctxErr != nil {
			t.Errorf("the notification was sent on a canceled context: %v", ctxErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the notification was never sent")
	}
}

// TestHintService_GenerateHintsVisionRetriesANoticeThatFailedToSend pins that
// the dedupe remembers a *telling*, not an attempt. A session whose
// notification daemon was not up at the first activation would otherwise be
// silenced for the life of the daemon, which is the same silence the
// notification exists to break.
func TestHintService_GenerateHintsVisionRetriesANoticeThatFailedToSend(t *testing.T) {
	attempts := make(chan struct{}, 4)

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 200, 200), true, nil
	}
	mockSystem.ShowNotificationFunc = func(context.Context, string, string) error {
		attempts <- struct{}{}

		return derrors.New(derrors.CodeActionFailed, "no notification daemon on the bus")
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	service := services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{},
		logger.Get(),
		&mockVisionPort{detectErr: derrors.New(derrors.CodeNotSupported, "no language data")},
	)

	// The release of a failed notice happens on the sending goroutine, so which
	// later activation retries is a scheduling detail rather than a promise.
	// What is asserted is that one of them does: a dedupe that remembered the
	// attempt would let none of them.
	seen := 0
	deadline := time.After(5 * time.Second)

	for seen < 2 {
		_, err := service.GenerateHints(
			context.Background(), nil, nil, "com.example.app", domain.StrategyVision, "", false,
		)
		if err != nil {
			t.Fatalf("GenerateHints() unexpected error: %v", err)
		}

		select {
		case <-attempts:
			seen++
		case <-deadline:
			t.Fatalf("a failed notice was never retried: %d attempts", seen)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestHintService_GenerateHintsVisionStaysQuietForAnOrdinaryFailure keeps the
// notification for the case a user can act on. A capture that timed out or an
// engine that failed one frame is a transient fault, and a toast on every
// hotkey press for those would be noise rather than help.
func TestHintService_GenerateHintsVisionStaysQuietForAnOrdinaryFailure(t *testing.T) {
	notified := make(chan string, 2)

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 200, 200), true, nil
	}
	mockSystem.ShowNotificationFunc = func(_ context.Context, _, message string) error {
		notified <- message

		return nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	service := services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{},
		logger.Get(),
		&mockVisionPort{
			detectErr: derrors.New(
				derrors.CodeActionFailed,
				"the compositor did not answer in time",
			),
		},
	)

	_, err := service.GenerateHints(
		context.Background(), nil, nil, "com.example.app", domain.StrategyVision, "", false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	select {
	case message := <-notified:
		t.Errorf("a transient vision failure notified the user: %q", message)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHintService_UpdateGenerator(t *testing.T) {
	mockAcc := &mocks.MockAccessibilityPort{}
	mockOverlay := &mocks.MockOverlayPort{}
	log := logger.Get()

	initialGen, err := hint.NewAlphabetGenerator("abcd", hint.LabelDirectionReverse)
	if err != nil {
		t.Fatalf("NewAlphabetGenerator() error = %v", err)
	}

	service := services.NewHintService(
		mockAcc,
		mockOverlay,
		&mocks.MockSystemPort{},
		initialGen,
		config.HintsConfig{},
		log,
		nil,
	)

	normal, err := hint.NewAlphabetGenerator("efgh", hint.LabelDirectionNormal)
	if err != nil {
		t.Fatalf("NewAlphabetGenerator() error = %v", err)
	}

	service.UpdateGenerator(context.Background(), normal)

	// The registered generator must be retrievable under its own direction —
	// this is what the per-activation `hints --label-direction` override reads.
	got := service.Generator(hint.LabelDirectionNormal.String())
	if got == nil {
		t.Fatal("Generator(normal) = nil after UpdateGenerator")
	}

	if got.LabelDirection() != hint.LabelDirectionNormal {
		t.Errorf(
			"Generator(normal).LabelDirection() = %v, want %v",
			got.LabelDirection(),
			hint.LabelDirectionNormal,
		)
	}

	// A nil generator must be ignored rather than wiping a live one.
	service.UpdateGenerator(context.Background(), nil)

	if service.Generator(hint.LabelDirectionNormal.String()) == nil {
		t.Error("UpdateGenerator(nil) cleared the previously registered generator")
	}
}

func TestHintService_GeneratorReturnsDirectionSpecificInstance(t *testing.T) {
	mockAcc := &mocks.MockAccessibilityPort{}
	mockOverlay := &mocks.MockOverlayPort{}
	logger := logger.Get()

	reverseGen, _ := hint.NewAlphabetGenerator("abcd", hint.LabelDirectionReverse)
	normalGen, _ := hint.NewAlphabetGenerator("abcd", hint.LabelDirectionNormal)

	service := services.NewHintService(
		mockAcc,
		mockOverlay,
		&mocks.MockSystemPort{},
		reverseGen,
		config.HintsConfig{},
		logger,
		nil,
	)

	// Register a normal-direction generator on top of the reverse default.
	ctx := context.Background()
	service.UpdateGenerator(ctx, normalGen)

	// Each direction must resolve to its own generator instance, not the
	// shared default.
	gotReverse := service.Generator(domain.LabelDirectionReverse)
	if gotReverse == nil {
		t.Fatal("Generator(reverse) returned nil")
	}

	if gotReverse.LabelDirection() != hint.LabelDirectionReverse {
		t.Errorf(
			"Generator(reverse).LabelDirection() = %v, want %v",
			gotReverse.LabelDirection(),
			hint.LabelDirectionReverse,
		)
	}

	gotNormal := service.Generator(domain.LabelDirectionNormal)
	if gotNormal == nil {
		t.Fatal("Generator(normal) returned nil")
	}

	if gotNormal.LabelDirection() != hint.LabelDirectionNormal {
		t.Errorf(
			"Generator(normal).LabelDirection() = %v, want %v",
			gotNormal.LabelDirection(),
			hint.LabelDirectionNormal,
		)
	}

	if gotReverse == gotNormal {
		t.Error("reverse and normal resolved to the same generator instance")
	}

	// Empty direction falls back to the default (reverse) generator.
	gotDefault := service.Generator("")
	if gotDefault == nil {
		t.Fatal("Generator(\"\") returned nil")
	}

	if gotDefault.LabelDirection() != hint.LabelDirectionReverse {
		t.Errorf(
			"Generator(\"\").LabelDirection() = %v, want %v",
			gotDefault.LabelDirection(),
			hint.LabelDirectionReverse,
		)
	}

	// Unknown direction falls back to the default rather than failing.
	gotUnknown := service.Generator("made-up")
	if gotUnknown == nil {
		t.Fatal("Generator(\"made-up\") returned nil")
	}

	if gotUnknown.LabelDirection() != hint.LabelDirectionReverse {
		t.Errorf(
			"Generator(\"made-up\").LabelDirection() = %v, want %v",
			gotUnknown.LabelDirection(),
			hint.LabelDirectionReverse,
		)
	}
}

func TestHintService_GenerateHintsPicksDirectionGenerator(t *testing.T) {
	mockAcc := &mocks.MockAccessibilityPort{}
	mockOverlay := &mocks.MockOverlayPort{}
	logger := logger.Get()

	normalGen, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	reverseGen, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)

	// Five elements force both algorithms into the two-character tier, where
	// reverse and normal produce *different* label sequences. The exact
	// normal sequence is [A S D FA FS]; the exact reverse sequence is
	// [AA SA DA FA AS]. The 4th and 5th labels expose the difference.
	mockAcc.ClickableElementsFunc = func(_ context.Context, _ ports.ElementFilter) ([]*element.Element, error) {
		return []*element.Element{
			mustNewElement("e1", image.Rect(0, 0, 10, 10)),
			mustNewElement("e2", image.Rect(20, 20, 30, 30)),
			mustNewElement("e3", image.Rect(40, 40, 50, 50)),
			mustNewElement("e4", image.Rect(60, 60, 70, 70)),
			mustNewElement("e5", image.Rect(80, 80, 90, 90)),
		}, nil
	}

	service := services.NewHintService(
		mockAcc,
		mockOverlay,
		&mocks.MockSystemPort{},
		normalGen,
		config.HintsConfig{},
		logger,
		nil,
	)

	ctx := context.Background()
	service.UpdateGenerator(ctx, reverseGen)

	// Without an override, the configured (empty) label direction resolves
	// to the default normal generator. The normal algorithm keeps 3
	// single-char slots ([A S D]) and expands the 4th alphabet slot (F)
	// into 2-char labels starting at [FA].
	hints, err := service.GenerateHints(ctx, nil, nil, "", "", "", false)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if len(hints) != 5 {
		t.Fatalf("GenerateHints() returned %d hints, want 5", len(hints))
	}

	wantNormalLabels := []string{"A", "S", "D", "FA", "FS"}
	for i, want := range wantNormalLabels {
		if got := hints[i].Label(); got != want {
			t.Errorf("default-direction hint[%d].Label() = %q, want %q", i, got, want)
		}
	}

	// With a reverse override, the override must resolve to the registered
	// reverse generator — not silently fall back to the default normal one.
	// The reverse algorithm fills all 4 single-char slots ([AA SA DA FA])
	// before yielding a 2-char label ([AS]). The 1st and 5th labels (AA, AS)
	// prove the override actually engaged.
	hints, err = service.GenerateHints(ctx, nil, nil, "", "", domain.LabelDirectionReverse, false)
	if err != nil {
		t.Fatalf("GenerateHints() with reverse override unexpected error: %v", err)
	}

	if len(hints) != 5 {
		t.Fatalf(
			"GenerateHints() with reverse override returned %d hints, want 5",
			len(hints),
		)
	}

	wantReverseLabels := []string{"AA", "SA", "DA", "FA", "AS"}
	for i, want := range wantReverseLabels {
		if got := hints[i].Label(); got != want {
			t.Errorf(
				"reverse-override hint[%d].Label() = %q, want %q",
				i,
				got,
				want,
			)
		}
	}
}

func TestHintService_Health(t *testing.T) {
	mockAcc := &mocks.MockAccessibilityPort{}
	mockOverlay := &mocks.MockOverlayPort{}
	generator, _ := hint.NewAlphabetGenerator("abcd", hint.LabelDirectionReverse)
	logger := logger.Get()

	service := services.NewHintService(
		mockAcc,
		mockOverlay,
		&mocks.MockSystemPort{},
		generator,
		config.HintsConfig{},
		logger,
		nil,
	)

	// Setup mocks
	mockAcc.HealthFunc = func(_ context.Context) error {
		return nil
	}
	mockOverlay.HealthFunc = func(_ context.Context) error {
		return derrors.New(derrors.CodeOverlayFailed, "overlay unhealthy")
	}

	ctx := context.Background()
	health := service.Health(ctx)

	if len(health) != 3 {
		t.Errorf("Health() returned %d entries, want 3", len(health))
	}

	if _, ok := health["accessibility"]; !ok {
		t.Error("Health() missing 'accessibility' key")
	}

	if _, ok := health["overlay"]; !ok {
		t.Error("Health() missing 'overlay' key")
	}

	if health["overlay"] == nil {
		t.Error("Health() overlay should have error")
	}

	if health["accessibility"] != nil {
		t.Error("Health() accessibility should not have error")
	}

	// The service above was built with a nil vision port, which is a state the
	// constructor accepts. It still has to report something, because a missing
	// key reads as "not checked" rather than "unavailable".
	if !derrors.IsNotSupported(health["vision"]) {
		t.Errorf("Health() vision = %v with no vision port, want CodeNotSupported",
			health["vision"])
	}
}

// TestHintService_HealthReportsTheVisionPort is why the override exists. The
// vision strategy has a per-machine prerequisite on two platforms - OCR language
// data - and the notification that would otherwise carry that news goes through
// ShowNotification, which is stubbed on Windows. `neru doctor` reading
// hints.vision is the only surface a Windows user has, so the reason has to
// arrive verbatim rather than as a generic failure.
func TestHintService_HealthReportsTheVisionPort(t *testing.T) {
	generator, _ := hint.NewAlphabetGenerator("abcd", hint.LabelDirectionReverse)

	reason := derrors.New(derrors.CodeNotSupported, "no OCR language data installed")

	mockVision := &mocks.MockVisionPort{
		HealthFunc: func(_ context.Context) error { return reason },
	}

	service := services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		&mocks.MockSystemPort{},
		generator,
		config.HintsConfig{},
		logger.Get(),
		mockVision,
	)

	health := service.Health(context.Background())

	if !errors.Is(health["vision"], reason) {
		t.Errorf("Health() vision = %v, want the port's own reason %v", health["vision"], reason)
	}
}

// nativeButtonRole and nativeLinkRole are the accessibility roles a button and a
// link report on the platform running the tests. Configured roles resolve to
// native names, so an element built with a role from another platform would
// silently stop matching a config that asks for "button".
var (
	nativeButtonRole = nativeRole(element.SemanticButton, element.RoleButton)
	nativeLinkRole   = nativeRole(element.SemanticLink, element.RoleLink)
)

func nativeRole(semantic element.SemanticRole, fallback element.Role) element.Role {
	native := nativeRoles(semantic)
	if len(native) == 0 {
		return fallback
	}

	return native[0]
}

// nativeRoles is every native role a semantic name resolves to here. One semantic
// name can cover several - "button" is Button and SplitButton on Windows - so a
// test asserting on a role set has to resolve rather than assume.
func nativeRoles(semantic ...element.SemanticRole) []element.Role {
	requested := make([]string, 0, len(semantic))
	for _, name := range semantic {
		requested = append(requested, string(name))
	}

	native := element.ResolveRolesForCurrentPlatform(requested).Native

	roles := make([]element.Role, 0, len(native))
	for _, name := range native {
		roles = append(roles, element.Role(name))
	}

	return roles
}

func mustNewElement(id string, bounds image.Rectangle) *element.Element {
	return mustRoledElement(id, bounds, nativeButtonRole)
}

// mustRoledElement builds an element whose role the test chose, for the cases
// where the role is what is under test rather than incidental.
func mustRoledElement(
	id string,
	bounds image.Rectangle,
	role element.Role,
	opts ...element.Option,
) *element.Element {
	built, err := element.NewElement(element.ID(id), bounds, role, opts...)
	if err != nil {
		panic(err)
	}

	return built
}

// mustVisionElement builds what a vision port returns. The provenance flag is the
// only thing the hybrid merge tells the two sets apart by.
func mustVisionElement(id string, bounds image.Rectangle) *element.Element {
	built, err := element.NewElement(
		element.ID(id),
		bounds,
		nativeButtonRole,
		element.WithVisionOnly(),
	)
	if err != nil {
		panic(err)
	}

	return built
}

type mockVisionPort struct {
	detectedElements  []*element.Element
	detectErr         error
	contouredElements []*element.Element
	contourErr        error
	contourCalls      int
}

func (m *mockVisionPort) DetectElements(
	context.Context,
	image.Rectangle,
	config.HintsVisionConfig,
	bool,
) ([]*element.Element, error) {
	if m.detectErr != nil {
		return nil, m.detectErr
	}

	return m.detectedElements, nil
}

func (m *mockVisionPort) DetectContours(
	context.Context,
	image.Rectangle,
) ([]*element.Element, error) {
	m.contourCalls++

	if m.contourErr != nil {
		return nil, m.contourErr
	}

	return m.contouredElements, nil
}

func (m *mockVisionPort) CaptureScreen(context.Context) (*image.RGBA, error) {
	return nil, derrors.New(derrors.CodeBridgeFailed, "capture screen not implemented")
}

func (m *mockVisionPort) Health(context.Context) error {
	return nil
}

func TestHintService_GenerateHintsRejectsSplitWordForNonVisionStrategy(t *testing.T) {
	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	service := services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		&mocks.MockSystemPort{},
		generator,
		config.HintsConfig{
			Strategy: domain.StrategyAXTree,
		},
		logger.Get(),
		nil,
	)

	ctx := context.Background()

	_, err := service.GenerateHints(
		ctx,
		nil,
		nil,
		"",
		domain.StrategyAXTree,
		"",
		true, // splitWord
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !derrors.IsCode(err, derrors.CodeInvalidInput) {
		t.Errorf("expected invalid input error, got: %v", err)
	}
}

// TestHintService_GenerateHintsRoleFilterResolvingToNothing pins the behavior
// of a role filter that is configured but resolves to no native role on this
// platform — for example a config carrying only Linux entries, run on macOS.
//
// An empty ports.ElementFilter.Roles means "match every role", so the naive
// outcome is that an unusable filter hints *everything*. It must hint nothing.
func TestHintService_GenerateHintsRoleFilterResolvingToNothing(t *testing.T) {
	testElements := []*element.Element{
		mustNewElement("elem1", image.Rect(10, 10, 50, 50)),
		mustNewElement("elem2", image.Rect(60, 10, 100, 50)),
	}

	tests := []struct {
		name        string
		roles       []string
		filterRoles []string
	}{
		{
			name:  "configured roles all belong to another platform",
			roles: foreignRolesForCurrentPlatform(),
		},
		{
			name:        "role flag entries are unresolvable",
			roles:       []string{string(element.SemanticButton)},
			filterRoles: []string{"AXButton"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if len(testCase.roles) == 0 {
				t.Skipf("no foreign role set defined for %s", runtime.GOOS)
			}

			mockAcc := &mocks.MockAccessibilityPort{}
			mockAcc.ClickableElementsFunc = func(
				_ context.Context,
				_ ports.ElementFilter,
			) ([]*element.Element, error) {
				return testElements, nil
			}

			generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)
			service := services.NewHintService(
				mockAcc,
				&mocks.MockOverlayPort{},
				&mocks.MockSystemPort{},
				generator,
				config.HintsConfig{ClickableRoles: testCase.roles},
				logger.Get(),
				nil,
			)

			hints, err := service.GenerateHints(
				context.Background(),
				testCase.filterRoles,
				nil,
				"com.example.app",
				"",
				"",
				false,
			)
			if err != nil {
				t.Fatalf("GenerateHints() unexpected error: %v", err)
			}

			if len(hints) != 0 {
				t.Errorf(
					"GenerateHints() returned %d hints for an unusable role filter, want 0",
					len(hints),
				)
			}
		})
	}
}

// TestHintService_GenerateHintsRoleFlagOverridesConfig covers the happy path of
// `neru hints --role ...`: the flag replaces the configured roles entirely and
// is resolved through the same vocabulary, so it accepts semantic names and
// vocabulary-prefixed native names alike.
func TestHintService_GenerateHintsRoleFlagOverridesConfig(t *testing.T) {
	testElements := []*element.Element{
		mustNewElement("elem1", image.Rect(10, 10, 50, 50)),
	}

	var captured []element.Role

	mockAcc := &mocks.MockAccessibilityPort{}
	mockAcc.ClickableElementsFunc = func(
		_ context.Context,
		filter ports.ElementFilter,
	) ([]*element.Element, error) {
		captured = filter.Roles

		return testElements, nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionReverse)
	service := services.NewHintService(
		mockAcc,
		&mocks.MockOverlayPort{},
		&mocks.MockSystemPort{},
		generator,
		config.HintsConfig{
			ClickableRoles: []string{string(element.SemanticButton)},
		},
		logger.Get(),
		nil,
	)

	_, err := service.GenerateHints(
		context.Background(),
		[]string{string(element.SemanticLink)},
		nil,
		"com.example.app",
		"",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	want := element.ResolveRolesForCurrentPlatform(
		[]string{string(element.SemanticLink)},
	).Native
	if len(want) == 0 {
		t.Skip("link has no native role on this platform")
	}

	for _, role := range want {
		if !slices.Contains(captured, element.Role(role)) {
			t.Errorf("filter.Roles = %v, missing overridden role %q", captured, role)
		}
	}

	// The configured role must not survive the override.
	for _, role := range element.ResolveRolesForCurrentPlatform(
		[]string{string(element.SemanticButton)},
	).Native {
		if slices.Contains(captured, element.Role(role)) {
			t.Errorf("filter.Roles = %v, configured role %q leaked past the override",
				captured, role)
		}
	}
}

// newVisionHintService builds a vision-strategy service whose focused-window
// answer is whatever the caller wants to observe the fallback for.
func newVisionHintService(
	log *zap.Logger,
	bounds func(context.Context) (image.Rectangle, bool, error),
) *services.HintService {
	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = bounds
	mockSystem.ScreenBoundsFunc = func(context.Context) (image.Rectangle, error) {
		return image.Rect(0, 0, 1920, 1080), nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)

	return services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{},
		log,
		&mockVisionPort{},
	)
}

// TestHintService_GenerateHintsVisionSaysWhyItFellBackToTheScreen pins the
// difference between the two ways vision ends up scanning a whole monitor.
//
// A platform that cannot report focused-window geometry — a wlroots compositor
// with no IPC, a KWin bridge that never installed — is not the same event as a
// desktop with nothing focused, and it used to be logged as if it were. Scoping
// OCR to the entire screen instead of one window is slower and noisier, and the
// reason has to be readable without a debug build.
func TestHintService_GenerateHintsVisionSaysWhyItFellBackToTheScreen(t *testing.T) {
	tests := []struct {
		name      string
		bounds    func(context.Context) (image.Rectangle, bool, error)
		wantLevel zapcore.Level
		wantText  string
	}{
		{
			name: "a platform that cannot answer is a warning",
			bounds: func(context.Context) (image.Rectangle, bool, error) {
				return image.Rectangle{}, false, derrors.New(
					derrors.CodeNotSupported,
					"no focused-window geometry source on linux backend wayland-wlroots",
				)
			},
			wantLevel: zapcore.WarnLevel,
			wantText:  "wayland-wlroots",
		},
		{
			name: "nothing focused is routine",
			bounds: func(context.Context) (image.Rectangle, bool, error) {
				return image.Rectangle{}, false, nil
			},
			wantLevel: zapcore.DebugLevel,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)

			service := newVisionHintService(zap.New(core), testCase.bounds)

			_, err := service.GenerateHints(
				context.Background(), nil, nil, "com.example.app", domain.StrategyVision, "", false,
			)
			if err != nil {
				t.Fatalf("GenerateHints() unexpected error: %v", err)
			}

			entries := logs.FilterLevelExact(testCase.wantLevel).
				FilterMessageSnippet("focused window").
				All()
			if len(entries) == 0 {
				t.Fatalf("no %s entry about the focused window; logged %v",
					testCase.wantLevel, logs.All())
			}

			if testCase.wantText == "" {
				return
			}

			logged := fmt.Sprint(entries[0].Message, entries[0].ContextMap())
			if !strings.Contains(logged, testCase.wantText) {
				t.Errorf("entry %q does not carry %q, so the reason is unreadable",
					logged, testCase.wantText)
			}
		})
	}
}

// TestHintService_GenerateHintsContourNeverAsksTheTree pins what makes contour a
// separate strategy rather than a third flavour of vision.
//
// Contour exists for the windows a tree cannot see into - an RDP client, a canvas,
// custom-drawn UI - so it asks the tree for nothing at all. Vision still collects
// the supplementary surfaces and hybrid walks the window as well; a refactor that
// gave contour either would hint whatever chrome the tree happens to expose beside
// a window it reported nothing for, and the hints would look like contour found
// them.
func TestHintService_GenerateHintsContourNeverAsksTheTree(t *testing.T) {
	shape := mustVisionElement("contour_shape", image.Rect(40, 40, 120, 72))
	other := mustVisionElement("contour_other", image.Rect(40, 100, 120, 132))

	mockAcc := &mocks.MockAccessibilityPort{}
	mockAcc.ClickableElementsFunc = func(
		_ context.Context,
		_ ports.ElementFilter,
	) ([]*element.Element, error) {
		t.Error("contour walked the accessibility tree; it is the strategy that asks for nothing")

		return nil, nil
	}

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 800, 600), true, nil
	}

	visionPort := &mockVisionPort{contouredElements: []*element.Element{shape, other}}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	service := services.NewHintService(
		mockAcc,
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{},
		logger.Get(),
		visionPort,
	)

	hints, err := service.GenerateHints(
		context.Background(), nil, nil, "com.example.app", domain.StrategyContour, "", false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if len(hints) != 2 {
		t.Fatalf("GenerateHints() returned %d hints, want the 2 the detector found", len(hints))
	}

	if visionPort.contourCalls != 1 {
		t.Errorf("DetectContours called %d times, want 1", visionPort.contourCalls)
	}
}

// TestHintService_GenerateHintsContourHoldsResultsToTheFilter pins that the
// activation's filter still runs over contour results, and documents what that
// costs.
//
// A contour element is geometry with a Button role and no text whatsoever, so
// --text can only ever match nothing. That is the strategy's documented
// limitation rather than a filter bug, and the check has to stay: skipping it
// would let --text pass every hint, which reads as a filter that lost results
// rather than one that had nothing to match against.
func TestHintService_GenerateHintsContourHoldsResultsToTheFilter(t *testing.T) {
	shape := mustVisionElement("contour_shape", image.Rect(40, 40, 120, 72))

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 800, 600), true, nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	service := services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{},
		logger.Get(),
		&mockVisionPort{contouredElements: []*element.Element{shape}},
	)

	hints, err := service.GenerateHints(
		context.Background(),
		nil,
		[]string{"Save"},
		"com.example.app",
		domain.StrategyContour,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if len(hints) != 0 {
		t.Errorf(
			"a text filter kept %d contour hints; contour elements carry no text to match",
			len(hints),
		)
	}
}

// TestHintService_GenerateHintsContourNotifiesWhenTheStrategyIsUnavailable is the
// same failure the vision half has, and it bites harder.
//
// Vision keeps the tree's supplementary elements when detection refuses, so
// something reaches the overlay. Contour keeps nothing, so a CodeNotSupported
// swallowed here is a hotkey that does nothing at all with no explanation - and a
// log line reaches nobody (ADR 0002). The error names the missing capture path, so
// it is the sentence the user needs.
func TestHintService_GenerateHintsContourNotifiesWhenTheStrategyIsUnavailable(t *testing.T) {
	const missing = "the contour strategy needs screen capture"

	notified := make(chan string, 4)

	mockSystem := &mocks.MockSystemPort{}
	mockSystem.FocusedWindowBoundsFunc = func(context.Context) (image.Rectangle, bool, error) {
		return image.Rect(0, 0, 200, 200), true, nil
	}
	mockSystem.ShowNotificationFunc = func(_ context.Context, _, message string) error {
		notified <- message

		return nil
	}

	generator, _ := hint.NewAlphabetGenerator("asdf", hint.LabelDirectionNormal)
	service := services.NewHintService(
		&mocks.MockAccessibilityPort{},
		&mocks.MockOverlayPort{},
		mockSystem,
		generator,
		config.HintsConfig{},
		logger.Get(),
		&mockVisionPort{contourErr: derrors.New(derrors.CodeNotSupported, missing)},
	)

	hints, err := service.GenerateHints(
		context.Background(), nil, nil, "com.example.app", domain.StrategyContour, "", false,
	)
	if err != nil {
		t.Fatalf("GenerateHints() unexpected error: %v", err)
	}

	if len(hints) != 0 {
		t.Errorf("GenerateHints() returned %d hints after the detector refused", len(hints))
	}

	select {
	case message := <-notified:
		if !strings.Contains(message, missing) {
			t.Errorf("notification %q does not carry what the error named", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("contour reported CodeNotSupported and the user was never told")
	}
}

// foreignRolesForCurrentPlatform returns native role entries that belong to
// platforms other than the one running the tests, so they resolve to nothing
// here. Returns nil on a platform with no accessibility backend.
func foreignRolesForCurrentPlatform() []string {
	return map[string][]string{
		"darwin":  {"atspi:push button", "uia:Button"},
		"linux":   {"ax:AXButton", "uia:Button"},
		"windows": {"ax:AXButton", "atspi:push button"},
	}[runtime.GOOS]
}
