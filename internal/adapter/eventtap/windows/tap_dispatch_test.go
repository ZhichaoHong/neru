//go:build windows

package windows

// A WH_KEYBOARD_LL procedure that outstays LowLevelHooksTimeout (300 ms by
// default) is dropped from the chain for that event, and Windows delivers the key
// to the foreground application no matter what the procedure returned. Mode
// actions are slower than that - the drag walk sleeps between steps, and
// "hints --repeat" re-walks the UIA tree - so handleKey must not wait for one.
// Deleting the queue is silent everywhere except on a Windows desktop, where the
// symptom is a hint label typing itself over the text a drag just selected.

import (
	"sync"
	"testing"
	"time"
)

func TestEventTap_HandleKeyDoesNotWaitForTheHandler(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})

	tap := &EventTap{}
	tap.callback = func(_ string) {
		close(entered)
		<-release
	}

	tap.startDispatch()
	defer tap.stopDispatch()
	defer close(release)

	start := time.Now()
	if !tap.handleKey("K", false) {
		t.Fatal("handleKey(\"K\") = false, want true: a bare key-down is consumed by the tap")
	}

	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf(
			"handleKey took %s, want well under the 300ms hook timeout: it waited for the handler",
			elapsed,
		)
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler never ran: the key was queued but nothing drained it")
	}
}

func TestEventTap_DispatchKeepsKeyOrder(t *testing.T) {
	t.Parallel()

	const want = 8

	var (
		mu   sync.Mutex
		got  []string
		done = make(chan struct{})
	)

	tap := &EventTap{}
	tap.callback = func(key string) {
		mu.Lock()
		defer mu.Unlock()

		got = append(got, key)
		if len(got) == want {
			close(done)
		}
	}

	tap.startDispatch()
	defer tap.stopDispatch()

	// NormalizeKey lowercases a bare letter, so the handler reads them lowercase.
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for _, key := range keys {
		tap.handleKey(key, false)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		mu.Lock()
		defer mu.Unlock()

		t.Fatalf("only %d of %d keys were delivered: %v", len(got), want, got)
	}

	mu.Lock()
	defer mu.Unlock()

	for i, key := range keys {
		if got[i] != key {
			t.Fatalf("delivered %v, want %v: a two-key hint label needs its keys in order", got, keys)
		}
	}
}

// With no dispatcher running - the tap is disabled, or a unit test drives
// handleKey directly - the key still reaches the handler.
func TestEventTap_DispatchWithoutDispatcherStillDelivers(t *testing.T) {
	t.Parallel()

	var got string

	tap := &EventTap{}
	tap.callback = func(key string) { got = key }

	tap.handleKey("K", false)

	if got != "k" {
		t.Errorf("handler got %q, want %q", got, "k")
	}
}
