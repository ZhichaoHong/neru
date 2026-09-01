package services

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/derrors"
	"github.com/y3owk1n/neru/internal/domain"
	"github.com/y3owk1n/neru/internal/domain/element"
	"github.com/y3owk1n/neru/internal/domain/hint"
	"github.com/y3owk1n/neru/internal/ports"
)

// HintService orchestrates hint generation and display.
// It coordinates between the accessibility system, vision detection,
// hint generator, and overlay.
//
// Generators are cached per label direction so that switching direction
// (e.g. via the --label-direction CLI flag) does not require rebuilding
// the entire generator state. The configured label direction is always
// available as the default.
type HintService struct {
	BaseService

	mu               sync.RWMutex
	generators       map[string]hint.Generator // keyed by label direction
	defaultGenerator hint.Generator
	config           config.HintsConfig
	logger           *zap.Logger
	vision           ports.VisionPort
	// visionNotice is the last "the vision strategy cannot run here" reason a
	// user was told, so the same one is not repeated on every activation.
	visionNotice string
}

// NewHintService creates a new hint service with the given dependencies.
//
// The supplied generator is treated as the default (typically the configured
// label direction). Callers that need additional directions for per-activation
// overrides should use UpdateGenerator to register them.
func NewHintService(
	accessibility ports.AccessibilityPort,
	overlay ports.OverlayPort,
	system ports.SystemPort,
	generator hint.Generator,
	config config.HintsConfig,
	logger *zap.Logger,
	vision ports.VisionPort,
) *HintService {
	if logger == nil {
		logger = zap.NewNop()
	}

	generators := make(map[string]hint.Generator)

	if generator != nil {
		generators[generator.LabelDirection().String()] = generator
	}

	return &HintService{
		BaseService:      NewBaseService(accessibility, overlay, system),
		generators:       generators,
		defaultGenerator: generator,
		config:           config,
		logger:           logger.Named("service.hints"),
		vision:           vision,
	}
}

// GenerateHints collects clickable elements and generates labels without
// drawing them, so mode handlers can filter and position hints before the
// first render. A non-empty bundleID skips the AX lookup; non-empty overrides
// win over the config-derived strategy and label direction.
func (s *HintService) GenerateHints(
	ctx context.Context,
	filterRoles []string,
	filterTextContains []string,
	bundleID string,
	strategyOverride string,
	labelDirectionOverride string,
	splitWord bool,
) ([]*hint.Interface, error) {
	// This read must not be widened to span the strategy switch below: the
	// vision branch takes s.mu for writing (notifyVisionUnavailable), and a
	// sync.RWMutex is neither reentrant nor upgradable, so a read lock still
	// held there would deadlock this goroutine against itself.
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()

	if bundleID == "" {
		var bundleIDErr error

		bundleID, bundleIDErr = s.accessibility.FocusedAppBundleID(ctx)
		if bundleIDErr != nil {
			s.logger.Debug(
				"Failed to get focused app bundle ID for hints roles",
				zap.Error(bundleIDErr),
			)
		}
	}

	filter, usable := s.hintFilter(cfg, bundleID, filterRoles, filterTextContains)
	if !usable {
		return nil, nil
	}

	strategy := cfg.StrategyForApp(bundleID)
	if strategyOverride != "" {
		strategy = strategyOverride
	}

	labelDirection := cfg.LabelDirectionForApp(bundleID)
	if labelDirectionOverride != "" {
		labelDirection = labelDirectionOverride
	}

	if splitWord && !domain.StrategyReadsScreen(strategy) {
		return nil, derrors.New(
			derrors.CodeInvalidInput,
			"--split-word is only supported when the resolved strategy is 'vision' or 'hybrid'",
		)
	}

	var (
		elements []*element.Element
		genErr   error
	)

	// Branching on the resolved strategy, which is a config value the user set and
	// not a platform, so the One Rule is intact. What separates the two screen
	// strategies is one filter flag: vision leaves the window to OCR, hybrid walks
	// it as well and then drops the OCR regions the tree answered for.
	switch strategy {
	case domain.StrategyVision:
		elements = s.generateHintsVision(ctx, filter, splitWord, true)
	case domain.StrategyHybrid:
		// The tree half is collected wider than the activation asked for, so the
		// merge can drop an OCR region a clickable control already answers for even
		// when the activation excluded that control's role. The activation's own
		// filter then decides what reaches the overlay, so the extra tree elements
		// are never hinted.
		elements = retainMatching(
			mergeVisionWithTree(
				s.generateHintsVision(ctx, hybridCollectFilter(filter, cfg, bundleID), splitWord, false),
			),
			filter,
		)
	default:
		elements, genErr = s.generateHintsAX(ctx, filter)
	}

	if genErr != nil {
		return nil, genErr
	}

	if len(elements) == 0 {
		s.logger.Debug("No clickable elements found")

		return nil, nil
	}

	s.logger.Debug("Found clickable elements", zap.Int("count", len(elements)))

	return s.labelElements(ctx, elements, labelDirection)
}

// RefreshHints updates the hint display (e.g., after screen changes).
func (s *HintService) RefreshHints(ctx context.Context) error {
	s.logger.Debug("Refreshing hints")

	if !s.overlay.IsVisible() {
		s.logger.Debug("Overlay not visible, skipping refresh")

		return nil
	}

	refreshOverlayErr := s.overlay.Refresh(ctx)
	if refreshOverlayErr != nil {
		s.logger.Error("Failed to refresh overlay", zap.Error(refreshOverlayErr))

		return derrors.WrapOverlayFailed(refreshOverlayErr, "refresh hints")
	}

	s.logger.Debug("Hints refreshed successfully")

	return nil
}

// UpdateConfig updates the hints configuration.
// Hint filters can therefore change without a restart.
func (s *HintService) UpdateConfig(config config.HintsConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config = config

	s.logger.Debug("Hints configuration updated",
		zap.Bool("include_menubar", config.IncludeMenubarHints),
		zap.Bool("include_dock", config.IncludeDockHints),
		zap.Bool("include_nc", config.IncludeNCHints),
		zap.Bool("include_stage_manager", config.IncludeStageManagerHints),
		zap.Bool("include_pip", config.IncludePIPHints),
		zap.Bool("include_screen_capture", config.IncludeScreenCaptureHints))
}

// Health adds the vision port to what BaseService already reports, so a machine
// that cannot run the vision strategy says so in `neru doctor` under
// hints.vision.
//
// This is VisionPort.Health's only caller, and it is load-bearing rather than a
// nicety. The strategy has one per-machine prerequisite on two platforms - OCR
// language data on Windows, tesseract language data on Linux - and the path that
// would otherwise tell the user is notifyVisionUnavailable, which goes through
// ShowNotification. That is stubbed on Windows, so the sentence is logged and
// dropped there. Without this a user gets an empty overlay and no explanation
// anywhere.
//
// No lock: s.vision is set once by the constructor and never reassigned, and
// holding s.mu across a port call that can take milliseconds is how the vision
// path deadlocked before.
//
// The nil check is real. NewHintService accepts a nil vision port, and the
// darwin, linux and windows builds all pass one; a build without vision wired up
// would otherwise panic inside a diagnostic.
func (s *HintService) Health(ctx context.Context) map[string]error {
	health := s.BaseService.Health(ctx)

	if s.vision == nil {
		health["vision"] = derrors.New(
			derrors.CodeNotSupported,
			"the vision strategy is unavailable: no vision backend is wired into this build",
		)

		return health
	}

	health["vision"] = s.vision.Health(ctx)

	return health
}

// Generator returns the registered hint generator for the given label
// direction. An empty direction resolves to the default generator. If no
// generator exists for the requested direction the default is returned as a
// fallback so hint generation never fails purely because of a direction
// mismatch (e.g. during the brief window after a config reload before the
// caller registers the new generator).
func (s *HintService) Generator(direction string) hint.Generator {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if direction != "" {
		if g, ok := s.generators[direction]; ok {
			return g
		}
	}

	return s.defaultGenerator
}

// UpdateGenerator registers a hint generator for a specific label direction.
// The first registration becomes the default fallback; subsequent
// registrations for the *same* direction also replace the default so a
// config reload that changes `hint_characters` keeps the empty/unknown
// direction fallback in sync with the configured generator. A nil
// generator is ignored to avoid replacing a live generator with nothing.
func (s *HintService) UpdateGenerator(_ context.Context, generator hint.Generator) {
	if generator == nil {
		s.logger.Warn("Attempted to set nil generator, ignoring")

		return
	}

	direction := generator.LabelDirection().String()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.defaultGenerator == nil ||
		s.defaultGenerator.LabelDirection() == generator.LabelDirection() {
		s.defaultGenerator = generator
	}

	s.generators[direction] = generator

	s.logger.Debug("Hint generator updated", zap.String("direction", direction))
}

// generateHintsAX collects elements using the AX tree (default strategy).
func (s *HintService) generateHintsAX(
	ctx context.Context,
	filter ports.ElementFilter,
) ([]*element.Element, error) {
	axStart := time.Now()
	elements, err := s.accessibility.ClickableElements(ctx, filter)
	s.logger.Debug("TIMING: ClickableElements (axtree)",
		zap.Duration("elapsed", time.Since(axStart)),
		zap.Int("element_count", len(elements)),
		zap.Error(err))

	if err != nil {
		s.logger.Error("Failed to get clickable elements via AX", zap.Error(err))

		return nil, derrors.WrapAccessibilityFailed(err, "get clickable elements")
	}

	return elements, nil
}

// generateHintsVision reads the screen for elements and asks the accessibility
// tree for the rest.
//
// visionOwnsWindow is what separates the two strategies that come through here.
// Under vision it is true: the tree is asked for supplementary surfaces only -
// the menubar, the dock, notification centre - and the focused window is left
// entirely to OCR, which is the point of choosing it for an app whose tree is
// broken. Under hybrid it is false, so the window is walked as well and the
// caller merges the two sets.
//
// Role filtering follows from that. Vision drops filter.Roles for the tree call,
// because the supplementary surfaces are system chrome a user filtering for
// buttons still wants. Hybrid keeps them, but its caller hands in a filter already
// widened by hybridCollectFilter and narrows the merged result itself, so what
// arrives here is the dedupe set rather than the hint set. Vision elements are
// filtered below either way, since the vision port does not take a filter.
func (s *HintService) generateHintsVision(
	ctx context.Context,
	filter ports.ElementFilter,
	splitWord bool,
	visionOwnsWindow bool,
) []*element.Element {
	var allElements []*element.Element

	treeFilter := filter
	if visionOwnsWindow {
		treeFilter.Roles = nil
		treeFilter.SkipWindowElements = true
	}

	supplementStart := time.Now()

	supplementElements, err := s.accessibility.ClickableElements(ctx, treeFilter)
	if err != nil {
		s.logger.Debug("Failed to get elements via AX", zap.Error(err))
	} else {
		allElements = append(allElements, supplementElements...)
	}

	s.logger.Debug("TIMING: Tree elements (AX)",
		zap.Duration("elapsed", time.Since(supplementStart)),
		zap.Bool("window_walked", !visionOwnsWindow),
		zap.Int("count", len(supplementElements)))

	if s.vision == nil {
		s.logger.Warn("Vision strategy selected but vision port is unavailable")

		return allElements
	}

	// Get focused window bounds for vision detection
	windowBounds, found, boundsErr := s.system.FocusedWindowBounds(ctx)
	if boundsErr != nil || !found {
		// The two ways of getting here are not the same event. found=false with
		// no error is a desktop with nothing focused — routine, and the whole
		// screen is the right answer. An error means the platform could not
		// answer at all, and then scanning the whole screen is a degradation
		// nobody asked for: slower, noisier, and silent until now.
		if boundsErr != nil {
			s.logger.Warn(
				"Could not read the focused window, scanning the whole screen instead",
				zap.Error(boundsErr),
			)
		} else {
			s.logger.Debug("No focused window, scanning the whole screen")
		}

		windowBounds, boundsErr = s.system.ScreenBounds(ctx)
		if boundsErr != nil {
			s.logger.Error("Failed to get screen bounds for vision detection", zap.Error(boundsErr))

			return allElements
		}
	}

	// Detect window elements via vision
	visionStart := time.Now()
	windowElements, visionErr := s.vision.DetectElements(
		ctx,
		windowBounds,
		s.config.Vision,
		splitWord,
	)
	s.logger.Debug("TIMING: Window elements (vision)",
		zap.Duration("elapsed", time.Since(visionStart)),
		zap.Int("count", len(windowElements)),
		zap.Error(visionErr))

	if visionErr != nil {
		s.logger.Error("Failed to detect elements via vision", zap.Error(visionErr))

		// CodeNotSupported here means the machine cannot run this strategy at
		// all, and the error names what to install or which display server has
		// no path. That has to reach a person: under vision what a user sees is
		// an overlay with nothing on it, because the tree elements kept above
		// are macOS surfaces with no counterpart elsewhere, and a log line
		// reaches nobody (ADR 0002). Under hybrid the hints still work off the
		// tree, which is worse to leave silent rather than better - the strategy
		// has permanently become axtree and nothing says so. Transient failures
		// stay in the log.
		if derrors.IsNotSupported(visionErr) {
			s.notifyVisionUnavailable(ctx, visionErr.Error())
		}

		return allElements
	}

	// Same filter the tree half is held to. Roles used to be the only thing checked
	// here, so --text was inert under vision and half-working under hybrid: it
	// filtered the tree elements and silently passed every OCR one, which reads as a
	// filter that lost results rather than one that was ignored.
	for _, detected := range windowElements {
		if filter.Matches(detected) {
			allElements = append(allElements, detected)
		}
	}

	return allElements
}

// notifyVisionUnavailable tells the user, once, that the vision strategy
// cannot run here, carrying the reason the port gave.
//
// Once per distinct reason: a user who keeps pressing the hotkey gets one
// notification rather than one per press, and a different reason (the language
// data arrived, the session changed) is a new thing worth saying. The notice is
// the port's own sentence, which names a package or a display server and never
// anything read off the screen.
//
// Three things about how it is sent, each of which decides whether it arrives:
//
// It goes out on its own goroutine, because showing a notification is a
// session-bus round trip on Linux and three of the four callers of
// GenerateHints reach here holding the mode handler's lock.
//
// It drops the activation context's cancellation. Those callers build the
// context with a hint timeout and cancel it on return, which is microseconds
// after this is reached — and the Linux notification path honors a caller's
// deadline, so keeping it would cancel the very first send, the one that also
// dials the session bus. The send is still bounded, by the deadline that path
// imposes itself.
//
// And "told" is only remembered if the telling worked. A send claimed under the
// lock so two activations cannot both fire, and released again when it fails,
// so a session that had no notification daemon at the first attempt is not
// silenced for the life of the daemon.
//
// Locking: s.mu sits below the mode handler's lock — nothing held under it does
// I/O or reaches the handler, which is what makes taking it from locked context
// safe (internal/app/modes/AGENTS.md).
func (s *HintService) notifyVisionUnavailable(ctx context.Context, reason string) {
	if s.system == nil {
		return
	}

	s.mu.Lock()

	alreadyTold := s.visionNotice == reason
	if !alreadyTold {
		s.visionNotice = reason
	}
	s.mu.Unlock()

	if alreadyTold {
		return
	}

	notifyCtx := context.WithoutCancel(ctx)
	system, log := s.system, s.logger

	go func() {
		err := system.ShowNotification(notifyCtx, "neru hints", reason)
		if err == nil {
			return
		}

		log.Warn("Could not notify that the vision strategy is unavailable", zap.Error(err))

		s.mu.Lock()
		if s.visionNotice == reason {
			s.visionNotice = ""
		}
		s.mu.Unlock()
	}()
}

// hybridCollectFilter widens the role set the tree half is collected with to the
// union of the activation's roles and the app's configured clickable roles.
//
// The merge needs it. A role filter says which elements the user wants hints on,
// not which parts of the screen the deduplicator may consult, and collecting the
// tree at the activation's width breaks that: an OCR region inside an excluded
// control meets no tree element to be deduped against and survives with whatever
// role the classifier guessed. Measured on Explorer, `hybrid --role=button`
// produced 28 vision-only elements against plain `hybrid`'s 4 - a narrowing filter
// widening the output 7x.
//
// Wider is not free, and the cost is bounded. Measured on one Explorer window, the
// UIA walk took 22 ms keeping 0 elements, 139 ms keeping 37 and ~180 ms keeping 79 -
// the role condition is pushed into the native walk, so cost tracks the elements
// kept rather than the nodes visited. A role-filtered activation therefore pays what
// an unfiltered one pays, which is the ceiling and an activation users already run.
//
// Two things it is deliberately not. Not the unfiltered tree - default
// clickable_roles excludes static text, so a non-clickable StaticText node would
// then suppress the very OCR region hybrid exists to add. And not the configured
// set alone, which would take away the tree elements an activation asking for a
// role the config omits gets today.
func hybridCollectFilter(
	filter ports.ElementFilter,
	cfg config.HintsConfig,
	bundleID string,
) ports.ElementFilter {
	collect := filter
	collect.Roles = unionRoles(filter.Roles, elementRoles(cfg.ClickableRolesForApp(bundleID)))

	return collect
}

// unionRoles adds the configured roles the activation did not ask for.
//
// Either side being empty returns the activation's set unchanged, and for opposite
// reasons. An empty activation set already means "no role restriction" to
// ElementFilter.Matches, so there is nothing to widen. An empty configured set is a
// config that restricts nothing, and widening a narrow activation to the whole tree
// on that basis is how a StaticText node ends up suppressing OCR text.
func unionRoles(requested, configured []element.Role) []element.Role {
	if len(requested) == 0 || len(configured) == 0 {
		return requested
	}

	union := make([]element.Role, 0, len(requested)+len(configured))
	union = append(union, requested...)

	for _, role := range configured {
		if !slices.Contains(union, role) {
			union = append(union, role)
		}
	}

	return union
}

// elementRoles converts resolved native role names, dropping the empty entries a
// platform resolution can leave behind.
func elementRoles(roles []string) []element.Role {
	converted := make([]element.Role, 0, len(roles))

	for _, role := range roles {
		if role == "" {
			continue
		}

		converted = append(converted, element.Role(role))
	}

	return converted
}

// retainMatching keeps the elements the filter accepts, in order.
func retainMatching(elements []*element.Element, filter ports.ElementFilter) []*element.Element {
	kept := make([]*element.Element, 0, len(elements))

	for _, candidate := range elements {
		if filter.Matches(candidate) {
			kept = append(kept, candidate)
		}
	}

	return kept
}

// hintFilter builds the element filter for one activation. The second result
// is false when every requested role belongs to another platform: hinting
// everything would hide that misconfiguration, so nothing is hinted instead
// (`neru roles --explain` and `neru doctor` report the cause).
func (s *HintService) hintFilter(
	cfg config.HintsConfig,
	bundleID string,
	filterRoles []string,
	filterTextContains []string,
) (ports.ElementFilter, bool) {
	filter := ports.DefaultElementFilter()

	// requested holds the entries as written, before resolution, so "no filter
	// at all" stays distinguishable from "a filter that resolved to nothing".
	var roles, requested []string

	if len(filterRoles) > 0 {
		// `neru hints --role ...` accepts the same vocabulary as the config
		// and is resolved the same way.
		requested = filterRoles

		resolution := element.ResolveRolesForCurrentPlatform(filterRoles)
		roles = resolution.Native

		s.logger.Debug("Using override roles from activation options",
			zap.Int("requested", len(requested)),
			zap.Int("role_count", len(roles)))

		for _, message := range resolution.FatalMessages() {
			s.logger.Warn("Ignoring role filter entry", zap.String("reason", message))
		}
	} else {
		requested = cfg.MergedForApp(bundleID).ClickableRoles
		roles = cfg.ClickableRolesForApp(bundleID)

		s.logger.Debug("Resolved clickable roles for hints",
			zap.String("bundle_id", bundleID),
			zap.Int("requested", len(requested)),
			zap.Int("role_count", len(roles)))
	}

	filter.Roles = elementRoles(roles)

	if len(filter.Roles) == 0 && len(requested) > 0 {
		s.logger.Warn(
			"No configured role applies on this platform; showing no hints",
			zap.Int("requested", len(requested)),
		)

		return filter, false
	}

	filter.IncludeMenubar = cfg.IncludeMenubarHints
	filter.AdditionalMenubarTargets = cfg.AdditionalMenubarHintsTargets
	filter.IncludeDock = cfg.IncludeDockHints
	filter.IncludeNotificationCenter = cfg.IncludeNCHints
	filter.IncludeStageManager = cfg.IncludeStageManagerHints
	filter.IncludePIP = cfg.IncludePIPHints
	filter.IncludeScreenCapture = cfg.IncludeScreenCaptureHints

	// Text filter: an element matches when any term matches.
	//
	// Lowercased here because ElementFilter.Matches compares against lowercased
	// element text and does not fold the terms itself - once per activation rather
	// than once per element. Nothing upstream folds them: --text arrives from
	// splitCSV verbatim, so an uppercase term used to match nothing at all.
	if len(filterTextContains) > 0 {
		first := strings.ToLower(filterTextContains[0])

		filter.TitleContains = first
		filter.DescriptionContains = first
		filter.ValueContains = first

		for _, term := range filterTextContains[1:] {
			filter.TextContainsList = append(
				filter.TextContainsList,
				strings.ToLower(term),
			)
		}

		s.logger.Debug("Applying text filter",
			zap.Int("term_count", len(filterTextContains)))
	}

	return filter, true
}

// labelElements turns collected elements into labeled hints.
func (s *HintService) labelElements(
	ctx context.Context,
	elements []*element.Element,
	labelDirection string,
) ([]*hint.Interface, error) {
	gen := s.Generator(labelDirection)

	maxHints := gen.MaxHints()
	if maxHints > 0 && len(elements) > maxHints {
		s.logger.Warn(
			"Clickable element count exceeds available hint key combinations; showing as many as possible",
			zap.Int("element_count", len(elements)),
			zap.Int("max_hints", maxHints),
			zap.Int("omitted_count", len(elements)-maxHints),
		)
	}

	genStart := time.Now()
	hints, elementsErr := gen.Generate(ctx, elements)
	s.logger.Debug("TIMING: HintGenerator.Generate",
		zap.Duration("elapsed", time.Since(genStart)),
		zap.Int("element_count", len(elements)),
		zap.Int("hint_count", len(hints)),
		zap.String("label_direction", gen.LabelDirection().String()),
		zap.Error(elementsErr))

	if elementsErr != nil {
		s.logger.Error("Failed to generate hints", zap.Error(elementsErr))

		return nil, derrors.WrapInternalFailed(elementsErr, "generate hints")
	}

	s.logger.Debug("Generated hints", zap.Int("count", len(hints)))

	return hints, nil
}
