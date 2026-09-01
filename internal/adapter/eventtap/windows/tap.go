//go:build windows

package windows

import (
	"context"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/y3owk1n/neru/internal/adapter/eventtap/tap"
	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
	"github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/domain/keyvocab"
)

// EventTap is a keyboard event interceptor on Windows.
type EventTap struct {
	logger *zap.Logger

	mu                   sync.RWMutex
	callback             tap.Callback
	passthroughCallback  tap.PassthroughCallback
	hotkeys              []string
	stickyModifierToggle bool
	enabled              bool
	dispatcher           *keyDispatcher

	hook *winplatform.KeyboardHook
}

// keyQueueSize bounds the keys waiting for the dispatcher goroutine. Human
// typing never comes close; filling it means a mode action has wedged.
const keyQueueSize = 256

// keyDispatcher carries keys from the hook thread to a goroutine that runs the
// mode action. A WH_KEYBOARD_LL procedure has to return within
// LowLevelHooksTimeout (HKCU\Control Panel\Desktop, 300 ms when absent) or
// Windows drops the hook out of the chain for that event and delivers the key to
// the foreground application whatever the procedure returns. A mode action is
// nowhere near that fast: the drag walk in MouseUp sleeps between steps, and
// "hints --repeat" re-walks the UIA tree to recompute labels. Running either on
// the hook thread leaks the key into the app, which in Notepad types over the
// selection the drag just made.
type keyDispatcher struct {
	keys chan string
	stop chan struct{}
}

// NewEventTap creates a new event tap.
func NewEventTap(callback tap.Callback, logger *zap.Logger) *EventTap {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &EventTap{
		logger:   logger.Named("eventtap"),
		callback: callback,
	}
}

// Enable enables the event tap.
func (et *EventTap) Enable() {
	et.mu.Lock()
	if et.enabled {
		et.mu.Unlock()

		return
	}

	et.enabled = true
	et.mu.Unlock()

	// Before the hook, so the first event already has somewhere to queue.
	et.startDispatch()

	hook, err := winplatform.StartKeyboardHook(et.handleKey)
	if err != nil {
		et.logger.Error("failed to start keyboard hook", zap.Error(err))
		et.stopDispatch()
		et.mu.Lock()
		et.enabled = false
		et.mu.Unlock()

		return
	}

	et.mu.Lock()
	et.hook = hook
	et.mu.Unlock()
}

// Disable disables the event tap.
func (et *EventTap) Disable() {
	et.mu.Lock()
	if !et.enabled {
		et.mu.Unlock()

		return
	}

	hook := et.hook
	et.hook = nil
	et.enabled = false
	et.mu.Unlock()

	if hook != nil {
		hook.Stop()
	}

	// After the hook, so a key read during teardown still reaches the handler.
	// Never joined: the caller can be holding the mode handler's lock, which the
	// in-flight action may be waiting on.
	et.stopDispatch()
}

// Destroy destroys the event tap.
func (et *EventTap) Destroy() {
	et.Disable()
}

// SetHotkeys sets the hotkeys. These are the global [hotkeys] chords the
// platform backend registered, which handleKey leaves to RegisterHotKey rather
// than dispatching itself.
func (et *EventTap) SetHotkeys(hotkeys []string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.hotkeys = append([]string(nil), hotkeys...)
}

// SetModifierPassthrough sets modifier passthrough.
func (et *EventTap) SetModifierPassthrough(_ bool, _ []string) {}

// SetInterceptedModifierKeys sets intercepted modifier keys.
func (et *EventTap) SetInterceptedModifierKeys(_ []string) {}

// SetPassthroughCallback sets the passthrough callback.
func (et *EventTap) SetPassthroughCallback(cb tap.PassthroughCallback) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.passthroughCallback = cb
}

// SetStickyModifierToggle enables or disables sticky modifier toggle detection.
func (et *EventTap) SetStickyModifierToggle(enabled bool) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.stickyModifierToggle = enabled
}

// PostModifierEvent simulates a physical modifier key press or release.
func (et *EventTap) PostModifierEvent(_ string, _ bool) {}

// SetKeyboardLayout sets the keyboard layout.
func (et *EventTap) SetKeyboardLayout(_ string) bool { return true }

// IsEnabled returns whether the event tap is enabled.
func (et *EventTap) IsEnabled() bool {
	et.mu.RLock()
	defer et.mu.RUnlock()

	return et.enabled
}

// SetHandler sets the key handler.
func (et *EventTap) SetHandler(handler func(key string)) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.callback = handler
}

// EnableWithContext enables the event tap with context.
func (et *EventTap) EnableWithContext(_ context.Context) error {
	et.Enable()

	return nil
}

// DisableWithContext disables the event tap with context.
func (et *EventTap) DisableWithContext(_ context.Context) error {
	et.Disable()

	return nil
}

// IsUinputScrollAvailable returns false on Windows.
func IsUinputScrollAvailable() bool {
	return false
}

// IsWaylandEvdevKeyboardActive returns false on Windows (no evdev / Wayland).
func IsWaylandEvdevKeyboardActive() bool {
	return false
}

// isRegisteredHotkey reports whether key is one of the chords registered as a
// global hotkey, compared in the normalized form both sides are matched in so a
// binding written "Primary+G" answers for the "Ctrl+G" the hook reads.
func (et *EventTap) isRegisteredHotkey(key string) bool {
	normalized := config.NormalizeKeyForComparison(key)
	if normalized == "" {
		return false
	}

	et.mu.RLock()
	defer et.mu.RUnlock()

	for _, hotkey := range et.hotkeys {
		if config.NormalizeKeyForComparison(hotkey) == normalized {
			return true
		}
	}

	return false
}

func (et *EventTap) handleKey(key string, isUp bool) bool {
	if key == "" {
		return false
	}

	if mod := keyvocab.CanonicalModifier(key); mod != "" {
		if et.stickyToggleEnabled() {
			et.dispatchKey(keyvocab.ModifierToggleEvent(mod, !isUp))
		}

		return false
	}

	if isUp {
		if keyUp := keyvocab.KeyUpEvent(key); keyUp != "" {
			et.dispatchKey(keyUp)
		}

		return false
	}

	normalized := keyvocab.NormalizeKey(key)

	// Key-down with system-level modifiers (ctrl, alt, cmd) should pass through
	// to the application so system shortcuts like Ctrl+C, Alt+Tab, Win+D still
	// work while a mode is active. The mode handler still receives the key for
	// hotkey matching.
	lower := strings.ToLower(normalized)
	if strings.Contains(lower, "ctrl+") || strings.Contains(lower, "alt+") ||
		strings.Contains(lower, "cmd+") {
		// Unless the chord is one RegisterHotKey owns. That mechanism keeps
		// firing while a mode is active and this hook runs ahead of it, so
		// passing the key on without dispatching leaves exactly one of the two
		// to run the binding. Dispatching as well would run it twice, because
		// the mode handler falls back to the global table for a chord the mode
		// does not bind (internal/app/modes/keymap.go, settledKeymaps),
		// and a double-run of "recursive_grid --toggle" exits the mode and
		// re-enters it. macOS answers the same question the same way, in its own
		// tap's hotkey check (platform/darwin/eventtap_darwin.m).
		//
		// Bare keys and Shift-only combos are deliberately not asked about: the
		// tap consumes those below so a mode keeps every key it reads as input,
		// and the handler's fallback leaves them alone for the same reason.
		if et.isRegisteredHotkey(normalized) {
			return false
		}

		et.dispatchKey(normalized)

		return false
	}

	// Non-modifier key-down (single character, Shift+letter, named key like
	// Return/Space/arrows): consume the key so it does not reach the
	// application behind the overlay. This matches macOS behavior where all
	// non-modifier keys are consumed by the event tap.
	et.dispatchKey(normalized)

	return true
}

// dispatchKey hands the key to the dispatcher goroutine and returns, so
// handleKey can answer the hook immediately. Ordering is kept because one
// goroutine drains one channel.
func (et *EventTap) dispatchKey(key string) {
	if key == "" {
		return
	}

	et.mu.RLock()
	dispatcher := et.dispatcher
	et.mu.RUnlock()

	if dispatcher == nil {
		et.deliverKey(key)

		return
	}

	select {
	case dispatcher.keys <- key:
	case <-dispatcher.stop:
	}
}

func (et *EventTap) startDispatch() {
	dispatcher := &keyDispatcher{
		keys: make(chan string, keyQueueSize),
		stop: make(chan struct{}),
	}

	et.mu.Lock()
	et.dispatcher = dispatcher
	et.mu.Unlock()

	go et.runDispatch(dispatcher)
}

func (et *EventTap) stopDispatch() {
	et.mu.Lock()
	dispatcher := et.dispatcher
	et.dispatcher = nil
	et.mu.Unlock()

	if dispatcher != nil {
		close(dispatcher.stop)
	}
}

func (et *EventTap) runDispatch(dispatcher *keyDispatcher) {
	for {
		select {
		case <-dispatcher.stop:
			return
		case key := <-dispatcher.keys:
			et.deliverKey(key)
		}
	}
}

func (et *EventTap) deliverKey(key string) {
	et.mu.RLock()
	callback := et.callback
	et.mu.RUnlock()

	if callback != nil {
		callback(key)
	}
}

func (et *EventTap) stickyToggleEnabled() bool {
	et.mu.RLock()
	defer et.mu.RUnlock()

	return et.stickyModifierToggle
}
