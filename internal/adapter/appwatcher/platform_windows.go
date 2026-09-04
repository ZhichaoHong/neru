//go:build windows

package appwatcher

import (
	"context"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"

	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
)

// Windows app-watcher backend. macOS receives focus changes from an NSWorkspace
// observer; on Windows they come from a SetWinEventHook on
// EVENT_SYSTEM_FOREGROUND, and the identity of the focused application is the
// executable path — the same identity FocusedAppBundleID answers with, so
// per-app configuration matches whether it was resolved from an event or from a
// live query.
//
// The hook says only that foreground changed. This layer re-queries who has it,
// which collapses a burst to the final foreground: the windows alt-tabbed past
// were never really focused.
//
// focusEventSafetyInterval bounds how long the loop goes without a forced
// re-sample. Focus changes normally wake it immediately through the hook; this is
// the safety net against a coalesced or missed event, which would otherwise leave
// a stale identity and therefore the wrong hotkey table until the next switch.
const focusEventSafetyInterval = 3 * time.Second

// globalWindowsWatcher is the process-wide app watcher, mirroring the single
// darwin.SetAppWatcher registration. NewWatcher registers itself here via
// platformRegisterWatcher.
var globalWindowsWatcher = &windowsAppWatcher{
	identity:  windowsFocusedIdentity,
	subscribe: subscribeWindowsFocus,
	ownPID:    os.Getpid(),
	interval:  focusEventSafetyInterval,
}

// focusSubscription is the event source the loop waits on. Both channels may be
// nil, which blocks that arm of the select forever and leaves the periodic
// re-sample as the only driver.
type focusSubscription struct {
	focus  <-chan struct{}
	screen <-chan struct{}
	stop   func()
}

// windowsAppWatcher re-samples the focused-application identity on each
// foreground event and dispatches the changes to the registered Watcher.
type windowsAppWatcher struct {
	// identity resolves the focused executable path and its process id;
	// injectable for tests.
	identity func() (bundleID string, pid int, ok bool)
	// subscribe installs the foreground hook and the display-change window;
	// injectable for tests. A nil subscribe leaves only the periodic re-sample.
	subscribe func() (*focusSubscription, error)
	// ownPID is this process's id, used to drop our own windows taking
	// foreground.
	ownPID   int
	interval time.Duration

	mu      sync.Mutex
	watcher *Watcher
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// last is the most recently dispatched executable path ("" means "none
	// focused"). It is owned exclusively by the loop goroutine, so it needs no
	// lock.
	last string
}

// register records the Watcher that events are dispatched to.
func (w *windowsAppWatcher) register(watcher *Watcher) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.watcher = watcher
}

// start launches the event loop. It is idempotent: a second call while already
// running is a no-op.
func (w *windowsAppWatcher) start() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.watcher == nil || w.cancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.last = ""

	w.wg.Add(1)

	go w.loop(ctx)
}

// stop halts the event loop and waits for it to exit.
func (w *windowsAppWatcher) stop() {
	w.mu.Lock()

	if w.cancel == nil {
		w.mu.Unlock()

		return
	}

	cancel := w.cancel
	w.cancel = nil

	w.mu.Unlock()

	cancel()
	w.wg.Wait()
}

// loop samples the focused application until the context is canceled: once
// immediately, then on every foreground event and at least once per interval.
// Display changes arrive on the same select, so one goroutine and one Win32
// thread cover both and there is one thing to shut down.
func (w *windowsAppWatcher) loop(ctx context.Context) {
	defer w.wg.Done()

	// Sample once immediately so per-app state converges without waiting for the
	// first event.
	w.tick()

	var (
		focus  <-chan struct{}
		screen <-chan struct{}
	)

	if w.subscribe != nil {
		subscription, err := w.subscribe()

		switch {
		case err != nil:
			// Degrade rather than go silent: the periodic re-sample still tracks
			// focus, just with up to one interval of latency.
			w.watcher.logger.Warn(
				"App watcher: foreground hook unavailable, falling back to periodic sampling",
				zap.Duration("interval", w.interval),
				zap.Error(err))
		case subscription != nil:
			focus, screen = subscription.focus, subscription.screen

			if subscription.stop != nil {
				defer subscription.stop()
			}
		}
	}

	safety := time.NewTicker(w.interval)
	defer safety.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-focus:
			w.tick()
		case <-safety.C:
			w.tick()
		case <-screen:
			w.watcher.logger.Debug("App watcher: display configuration changed")
			w.watcher.HandleScreenParametersChanged()
		}
	}
}

// tick samples the focused application once and dispatches activation changes.
// A transition to "no focused app" emits a deactivate for the previous one with
// no matching activate.
func (w *windowsAppWatcher) tick() {
	bundleID, pid, ok := w.identity()
	if !ok {
		bundleID = ""
	}

	// One of our own windows holding foreground is not an application switch.
	// The tray menu takes foreground before TrackPopupMenu so it dismisses on an
	// outside click, and publishing that would evaluate excluded_apps and the
	// per-app hotkey table against neru.exe. The hook carries
	// WINEVENT_SKIPOWNPROCESS, but the periodic re-sample asks the OS directly
	// and so needs its own check.
	if ok && pid == w.ownPID {
		return
	}

	if bundleID == w.last {
		return
	}

	prev := w.last
	w.last = bundleID

	// The app name is left empty: the port documents it as optional, and the
	// Windows alternatives are a window title that changes under the user's hands
	// or a version-resource read on every event. Every consumer keys on the
	// identifier.
	if prev != "" {
		w.watcher.HandleDeactivate("", prev)
	}

	if bundleID != "" {
		w.watcher.logger.Debug("App watcher: focused app changed",
			zap.String("bundle_id", bundleID),
			zap.String("previous", prev))
		w.watcher.HandleActivate("", bundleID)
	}
}

// windowsFocusedIdentity reports the focused application's executable path and
// process id. A failed query reads as "no focused app", matching the Linux
// backend, because a transient failure and an empty desktop are
// indistinguishable from here.
func windowsFocusedIdentity() (string, int, bool) {
	bundleID, pid, err := winplatform.FocusedApplicationIdentity()
	if err != nil || bundleID == "" {
		return "", pid, false
	}

	return bundleID, pid, true
}

func subscribeWindowsFocus() (*focusSubscription, error) {
	source, err := winplatform.StartFocusWatcher()
	if err != nil {
		return nil, err
	}

	return &focusSubscription{
		focus:  source.Focus(),
		screen: source.Screen(),
		stop:   source.Stop,
	}, nil
}

func platformRegisterWatcher(w *Watcher) { globalWindowsWatcher.register(w) }
func platformStartWatcher()              { globalWindowsWatcher.start() }
func platformStopWatcher()               { globalWindowsWatcher.stop() }

// platformSetMCDetection is a no-op on Windows: Mission Control is a macOS
// concept with no Windows equivalent.
func platformSetMCDetection(_ bool) {}
