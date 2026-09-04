//go:build windows

package appwatcher

import (
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	kindActivate   = "activate"
	kindDeactivate = "deactivate"

	appExplorer = `C:\Windows\explorer.exe`
	appNotepad  = `C:\Windows\System32\notepad.exe`

	// otherPID is any process id that is not this one, so tick treats the sample
	// as a foreign application.
	otherPID = 4242
	// ownPID stands in for this process in tests that exercise the self-focus
	// drop.
	ownPID = 1234
)

type watchEvent struct {
	kind   string
	name   string
	bundle string
}

// newRecordingWatcher returns a Watcher whose activate/deactivate events are
// appended to the returned slice pointer.
func newRecordingWatcher() (*Watcher, *[]watchEvent) {
	watcher := NewWatcher(nil)

	var events []watchEvent

	watcher.OnActivate(func(name, bundle string) {
		events = append(events, watchEvent{kindActivate, name, bundle})
	})
	watcher.OnDeactivate(func(name, bundle string) {
		events = append(events, watchEvent{kindDeactivate, name, bundle})
	})

	return watcher, &events
}

func TestWindowsAppWatcherTickDispatch(t *testing.T) {
	watcher, events := newRecordingWatcher()

	var (
		curID string
		curOK bool
	)

	sampler := &windowsAppWatcher{
		identity: func() (string, int, bool) { return curID, otherPID, curOK },
		ownPID:   ownPID,
		interval: time.Millisecond,
		watcher:  watcher,
	}

	steps := []struct {
		name     string
		id       string
		ok       bool
		expected []watchEvent
	}{
		{
			name: "initial focus emits activate only",
			id:   appExplorer, ok: true,
			expected: []watchEvent{{kindActivate, "", appExplorer}},
		},
		{
			name: "same app repeated emits nothing",
			id:   appExplorer, ok: true,
			expected: nil,
		},
		{
			name: "switch app emits deactivate then activate",
			id:   appNotepad, ok: true,
			expected: []watchEvent{
				{kindDeactivate, "", appExplorer},
				{kindActivate, "", appNotepad},
			},
		},
		{
			name: "focus lost emits deactivate only",
			id:   "", ok: false,
			expected: []watchEvent{{kindDeactivate, "", appNotepad}},
		},
		{
			name: "still no focus emits nothing",
			id:   "", ok: false,
			expected: nil,
		},
		{
			name: "regain focus emits activate only",
			id:   appExplorer, ok: true,
			expected: []watchEvent{{kindActivate, "", appExplorer}},
		},
	}

	for _, step := range steps {
		curID, curOK = step.id, step.ok
		*events = nil

		sampler.tick()

		if !eventsEqual(*events, step.expected) {
			t.Errorf("%s: got %+v, want %+v", step.name, *events, step.expected)
		}
	}
}

// TestWindowsAppWatcherOKFalseWithIDTreatedAsNoFocus ensures a non-ok result is
// treated as "no focus" even when the identity returns a stray non-empty path.
func TestWindowsAppWatcherOKFalseWithIDTreatedAsNoFocus(t *testing.T) {
	watcher, events := newRecordingWatcher()

	sampler := &windowsAppWatcher{
		identity: func() (string, int, bool) { return "stale", otherPID, false },
		ownPID:   ownPID,
		interval: time.Millisecond,
		watcher:  watcher,
	}

	sampler.tick()

	if len(*events) != 0 {
		t.Fatalf("expected no events when ok=false, got %+v", *events)
	}

	if sampler.last != "" {
		t.Fatalf("expected last to stay empty, got %q", sampler.last)
	}
}

// TestWindowsAppWatcherOwnProcessForegroundIgnored covers the tray-menu case:
// systray takes foreground on its own hidden window before TrackPopupMenu, and
// publishing that would evaluate excluded_apps against neru.exe. The previously
// focused application has to stay the focused one.
func TestWindowsAppWatcherOwnProcessForegroundIgnored(t *testing.T) {
	watcher, events := newRecordingWatcher()

	var (
		curID  string
		curPID int
	)

	sampler := &windowsAppWatcher{
		identity: func() (string, int, bool) { return curID, curPID, true },
		ownPID:   ownPID,
		interval: time.Millisecond,
		watcher:  watcher,
	}

	curID, curPID = appExplorer, otherPID
	sampler.tick()
	*events = nil

	curID, curPID = `C:\neru\neru.exe`, ownPID
	sampler.tick()

	if len(*events) != 0 {
		t.Fatalf("expected no events for our own foreground window, got %+v", *events)
	}

	if sampler.last != appExplorer {
		t.Fatalf("last = %q, want the previous application %q", sampler.last, appExplorer)
	}

	// Focus returning to the same application it never left must stay silent.
	curID, curPID = appExplorer, otherPID
	sampler.tick()

	if len(*events) != 0 {
		t.Fatalf("expected no events when focus returns unchanged, got %+v", *events)
	}
}

func TestWindowsAppWatcherStartStopLifecycle(t *testing.T) {
	watcher, _ := newRecordingWatcher()

	var callMu sync.Mutex

	calls := 0

	sampler := &windowsAppWatcher{
		identity: func() (string, int, bool) {
			callMu.Lock()
			calls++
			callMu.Unlock()

			return appExplorer, otherPID, true
		},
		ownPID:   ownPID,
		interval: time.Millisecond,
		watcher:  watcher,
	}

	// stop before start must be safe.
	sampler.stop()

	sampler.start()

	// start is idempotent while running.
	sampler.start()

	time.Sleep(20 * time.Millisecond)

	sampler.stop()

	callMu.Lock()
	got := calls
	callMu.Unlock()

	if got == 0 {
		t.Fatal("expected the loop to sample identity at least once")
	}

	// stop is idempotent.
	sampler.stop()
}

// TestWindowsAppWatcherEventLoopWakesOnFocusChannel proves the event-driven path
// dispatches an activation when the foreground hook signals, rather than waiting
// for the safety re-sample. The interval is set long enough that a pass can only
// come from the channel.
func TestWindowsAppWatcherEventLoopWakesOnFocusChannel(t *testing.T) {
	watcher := NewWatcher(nil)

	activated := make(chan string, 4)
	watcher.OnActivate(func(_, bundle string) { activated <- bundle })

	focus := make(chan struct{}, 1)

	var (
		curMu sync.Mutex
		cur   string
	)

	sampler := &windowsAppWatcher{
		identity: func() (string, int, bool) {
			curMu.Lock()
			defer curMu.Unlock()

			if cur == "" {
				return "", otherPID, false
			}

			return cur, otherPID, true
		},
		subscribe: func() (*focusSubscription, error) {
			return &focusSubscription{focus: focus}, nil
		},
		ownPID:   ownPID,
		interval: time.Hour,
		watcher:  watcher,
	}

	sampler.start()
	t.Cleanup(sampler.stop)

	curMu.Lock()
	cur = appNotepad
	curMu.Unlock()

	focus <- struct{}{}

	select {
	case got := <-activated:
		if got != appNotepad {
			t.Fatalf("activate bundle = %q, want %q", got, appNotepad)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event loop did not dispatch activate after a foreground signal")
	}
}

// TestWindowsAppWatcherScreenChannelDispatch pins that a display-configuration
// signal reaches the screen-parameters callback on the same loop as focus.
func TestWindowsAppWatcherScreenChannelDispatch(t *testing.T) {
	watcher := NewWatcher(nil)

	changed := make(chan struct{}, 4)
	watcher.OnScreenParametersChanged(func() { changed <- struct{}{} })

	screen := make(chan struct{}, 1)

	sampler := &windowsAppWatcher{
		identity: func() (string, int, bool) { return appExplorer, otherPID, true },
		subscribe: func() (*focusSubscription, error) {
			return &focusSubscription{screen: screen}, nil
		},
		ownPID:   ownPID,
		interval: time.Hour,
		watcher:  watcher,
	}

	sampler.start()
	t.Cleanup(sampler.stop)

	screen <- struct{}{}

	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("event loop did not dispatch a screen-parameters change")
	}
}

// TestWindowsAppWatcherStopsSubscription pins that the subscription is released
// on stop, which on the real backend is what unhooks the event and ends the Win32
// pump thread.
func TestWindowsAppWatcherStopsSubscription(t *testing.T) {
	watcher, _ := newRecordingWatcher()

	stopped := make(chan struct{})

	sampler := &windowsAppWatcher{
		identity: func() (string, int, bool) { return appExplorer, otherPID, true },
		subscribe: func() (*focusSubscription, error) {
			return &focusSubscription{stop: func() { close(stopped) }}, nil
		},
		ownPID:   ownPID,
		interval: time.Hour,
		watcher:  watcher,
	}

	sampler.start()
	sampler.stop()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not release the event subscription")
	}
}

// TestWindowsAppWatcherSubscribeFailureStillSamples covers the degrade: if the
// hook cannot be installed, focus tracking has to survive on the periodic
// re-sample rather than go silent.
func TestWindowsAppWatcherSubscribeFailureStillSamples(t *testing.T) {
	watcher := NewWatcher(nil)

	activated := make(chan string, 4)
	watcher.OnActivate(func(_, bundle string) { activated <- bundle })

	sampler := &windowsAppWatcher{
		identity: func() (string, int, bool) { return appNotepad, otherPID, true },
		subscribe: func() (*focusSubscription, error) {
			return nil, errors.New("hook unavailable")
		},
		ownPID:   ownPID,
		interval: time.Millisecond,
		watcher:  watcher,
	}

	sampler.start()
	t.Cleanup(sampler.stop)

	select {
	case got := <-activated:
		if got != appNotepad {
			t.Fatalf("activate bundle = %q, want %q", got, appNotepad)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no activation dispatched after the subscription failed")
	}
}

func TestWindowsAppWatcherStartWithoutWatcherIsNoop(t *testing.T) {
	sampler := &windowsAppWatcher{
		identity: func() (string, int, bool) { return appExplorer, otherPID, true },
		ownPID:   ownPID,
		interval: time.Millisecond,
	}

	sampler.start()
	defer sampler.stop()

	sampler.mu.Lock()
	running := sampler.cancel != nil
	sampler.mu.Unlock()

	if running {
		t.Fatal("start with no registered watcher should not launch the loop")
	}
}

func eventsEqual(left, right []watchEvent) bool {
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}
