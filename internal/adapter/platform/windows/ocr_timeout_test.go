//go:build windows

package windows

import (
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestTimeoutOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params OCRParams
		want   time.Duration
	}{
		{"a budget in milliseconds", OCRParams{TimeoutMS: 5000}, 5 * time.Second},
		{"zero means no deadline", OCRParams{TimeoutMS: 0}, 0},
		{"so does a negative one", OCRParams{TimeoutMS: -1}, 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := timeoutOf(test.params); got != test.want {
				t.Errorf("timeoutOf(%d) = %s, want %s", test.params.TimeoutMS, got, test.want)
			}
		})
	}
}

// fakeAsyncInfo is an IAsyncInfo whose vtable is Go callbacks, which is enough to
// drive awaitTerminal without an OCR engine: the poll loop reads one slot.
//
// The first field must stay the vtable pointer, because that is what a COM
// interface pointer points at.
type fakeAsyncInfo struct {
	vtable *[asyncInfoSlots]uintptr
}

// asyncInfoSlots covers IUnknown, IInspectable and IAsyncInfo, whose last method
// (Close) is slot 10.
const asyncInfoSlots = 11

func newFakeAsyncInfo(getStatus uintptr) *fakeAsyncInfo {
	slots := new([asyncInfoSlots]uintptr)
	slots[7] = getStatus

	return &fakeAsyncInfo{vtable: slots}
}

// TestAwaitTerminalGivesUpOnTheConfiguredBudget covers the timeout path with a
// recognition that never leaves Started. The status is left at its zero value,
// which is AsyncStatus.Started, so the fake reports success and writes nothing.
func TestAwaitTerminalGivesUpOnTheConfiguredBudget(t *testing.T) {
	t.Parallel()

	var polls int

	stillStarted := syscall.NewCallback(func(_, _ uintptr) uintptr {
		polls++

		return 0
	})

	info := newFakeAsyncInfo(stillStarted)

	started := time.Now()

	status, err := awaitTerminal(unsafe.Pointer(info), 20*time.Millisecond)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatalf("awaitTerminal returned status %d and no error, want the budget refused", status)
	}

	if !strings.Contains(err.Error(), "hints.vision.request_timeout_ms") {
		t.Errorf(
			"the timeout error is %q, want it to name the option that raises the budget",
			err.Error(),
		)
	}

	if elapsed < 20*time.Millisecond {
		t.Errorf("awaitTerminal gave up after %s, before the 20ms budget", elapsed)
	}

	if polls < 2 {
		t.Errorf("the status was read %d time(s); the deadline is checked between polls", polls)
	}
}

// TestAwaitTerminalReportsAnUnreadableStatus keeps the poll loop from spinning on
// a failure. A COM error out of get_Status is not a status, and treating it as
// one would loop until the deadline instead of saying what happened.
func TestAwaitTerminalReportsAnUnreadableStatus(t *testing.T) {
	t.Parallel()

	const eFail = 0x80004005

	failing := syscall.NewCallback(func(_, _ uintptr) uintptr {
		return eFail
	})

	info := newFakeAsyncInfo(failing)

	_, err := awaitTerminal(unsafe.Pointer(info), 0)
	if err == nil {
		t.Fatal("awaitTerminal returned no error for a get_Status that failed")
	}

	if !strings.Contains(err.Error(), "status could not be read") {
		t.Errorf("the error is %q, want it to name the failed read", err.Error())
	}
}

func TestRecognitionErrorNamesTheBusyEngine(t *testing.T) {
	t.Parallel()

	err := recognitionError(&comError{hr: hrEAbort, ctx: "IOcrEngine.RecognizeAsync"})

	if !strings.Contains(err.Error(), "still busy") {
		t.Errorf("an E_ABORT maps to %q, want the overlapping-recognition sentence", err.Error())
	}
}
