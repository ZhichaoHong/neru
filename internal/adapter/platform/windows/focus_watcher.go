//go:build windows

package windows

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Foreground-application and display-configuration notification for the app
// watcher. This file owns the Win32 surface only: a SetWinEventHook, the thread
// that pumps messages for it, and the hidden window WM_DISPLAYCHANGE arrives at.
// It reports that something changed and never says what — the app watcher
// re-queries FocusedApplicationIdentity, so one identity serves every per-app
// table.
const (
	// eventSystemForeground fires when the foreground window changes. It is the
	// closest Windows equivalent of the NSWorkspace activation notification the
	// darwin backend observes.
	eventSystemForeground = 0x0003

	// wineventOutOfContext delivers the callback through the installing thread's
	// message queue instead of injecting a DLL into every observed process. A Go
	// binary has no in-context option.
	wineventOutOfContext = 0x0000

	// wineventSkipOwnProcess drops events our own windows raise, at the OS
	// level. The app watcher still checks the PID itself: its periodic
	// re-sample asks who holds foreground without going through the hook.
	wineventSkipOwnProcess = 0x0002

	wmDisplayChange = 0x007E

	focusWatcherClassName = "NeruFocusWatcherWindow"
)

// focusWatcherStopJoinTimeout bounds how long Stop waits for the pump goroutine to
// exit. The join itself is expected to be immediate, for the reason Stop carries.
// What the bound guards is the wake failing: PostThreadMessageW can be refused —
// a queue at its message limit reports ERROR_NOT_ENOUGH_QUOTA — and a pump that
// was never woken stays parked in GetMessageW and never closes done. Blocking
// there forever would stall App cleanup on daemon shutdown, so past the timeout
// the goroutine is reaped in the background instead.
const focusWatcherStopJoinTimeout = 250 * time.Millisecond

var (
	procSetWinEventHook = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent  = user32.NewProc("UnhookWinEvent")

	// activeFocusWatcher is the watcher both callbacks deliver to. It is atomic
	// because the callbacks share no lock with run: run stores it on the pump
	// thread and the callbacks load it on whichever thread Windows happens to
	// call them from.
	activeFocusWatcher atomic.Pointer[FocusWatcher]

	// focusWinEventProcPtr and focusWndProcPtr are allocated once for the
	// process and never again, for the reason monitorEnumProcPtr in win32.go
	// carries in full: a syscall.NewCallback slot is never freed and the process
	// gets a fixed 2000 of them. Neither procedure holds per-watcher state — both
	// read activeFocusWatcher — so one of each serves every Start.
	focusWinEventProcPtr = sync.OnceValue(func() uintptr {
		return syscall.NewCallback(focusWinEventProc)
	})

	focusWndProcPtr = sync.OnceValue(func() uintptr {
		return syscall.NewCallback(focusWndProc)
	})

	focusWatcherClassOnce = sync.OnceValue(registerFocusWatcherClass)
)

var errFocusHookInstallFailed = errors.New(
	"SetWinEventHook failed: foreground hook not installed",
)

// FocusWatcher reports foreground changes and display-configuration changes on
// two channels. Each has a buffer of one and is written to without blocking, so
// a burst coalesces into a single notification and the reader sees the state as
// it is when it looks rather than a backlog of states that have passed.
type FocusWatcher struct {
	focus  chan struct{}
	screen chan struct{}

	threadID atomic.Uint32
	ready    chan error
	done     chan struct{}
	stopOnce sync.Once
}

// StartFocusWatcher installs the foreground hook and begins delivering
// notifications. It returns once the hook and the window are in place, so a
// caller that gets no error has a watcher that is already listening.
func StartFocusWatcher() (*FocusWatcher, error) {
	watcher := &FocusWatcher{
		focus:  make(chan struct{}, 1),
		screen: make(chan struct{}, 1),
		ready:  make(chan error, 1),
		done:   make(chan struct{}),
	}

	go watcher.run()

	err := <-watcher.ready
	if err != nil {
		return nil, err
	}

	return watcher, nil
}

// Focus becomes readable when the foreground window changes.
func (w *FocusWatcher) Focus() <-chan struct{} { return w.focus }

// Screen becomes readable when the display configuration changes.
func (w *FocusWatcher) Screen() <-chan struct{} { return w.screen }

// Stop removes the hook, destroys the window and waits for the pump thread to
// exit.
func (w *FocusWatcher) Stop() {
	if w == nil {
		return
	}

	w.stopOnce.Do(func() {
		// The pump is parked in GetMessageW, so only a message wakes it. The
		// thread has had a message queue since CreateWindowExW, which
		// StartFocusWatcher waited for, so the post cannot land on a thread with
		// nowhere to put it.
		if threadID := w.threadID.Load(); threadID != 0 {
			discardCall(procPostThreadMessageW.Call(uintptr(threadID), wmQuit, 0, 0))
		}
	})

	// Joining here cannot deadlock the way the keyboard hook's Stop can: neither
	// callback takes a caller-held lock, both only ever do a non-blocking send,
	// so the pump thread is always free to reach the next GetMessageW. The
	// timeout covers the other failure, a wake that never landed.
	select {
	case <-w.done:
	case <-time.After(focusWatcherStopJoinTimeout):
		go func() { <-w.done }()
	}
}

func (w *FocusWatcher) run() {
	// SetWinEventHook belongs to the thread that installs it and delivers its
	// callback through that thread's queue, so one thread has to install, pump
	// and unhook.
	runtime.LockOSThread()

	defer runtime.UnlockOSThread()
	defer close(w.done)

	hwnd, err := createFocusWatcherWindow()
	if err != nil {
		w.ready <- err

		return
	}

	defer func() { discardCall(procDestroyWindow.Call(hwnd)) }()

	hook, _, hookErr := procSetWinEventHook.Call(
		eventSystemForeground,
		eventSystemForeground,
		0,
		focusWinEventProcPtr(),
		0,
		0,
		wineventOutOfContext|wineventSkipOwnProcess,
	)
	if hook == 0 {
		w.ready <- fmt.Errorf("%w: %w", errFocusHookInstallFailed, hookErr)

		return
	}

	defer func() { discardCall(procUnhookWinEvent.Call(hook)) }()

	threadID, _, _ := procGetCurrentThreadID.Call()
	w.threadID.Store(uint32(threadID))
	activeFocusWatcher.Store(w)

	// Only this watcher's own registration is cleared: a later watcher may
	// already have installed itself, and clearing unconditionally would leave it
	// receiving events with nowhere to deliver them.
	defer func() { activeFocusWatcher.CompareAndSwap(w, nil) }()

	w.ready <- nil

	var message msg

	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if ret == 0 || int32(ret) == -1 {
			return
		}

		discardCall(procTranslateMessage.Call(uintptr(unsafe.Pointer(&message))))
		discardCall(procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message))))
	}
}

// createFocusWatcherWindow creates the window WM_DISPLAYCHANGE is delivered to.
//
// It is a top-level window that is never shown, not a message-only
// (HWND_MESSAGE) window: message-only windows receive no broadcast messages, and
// WM_DISPLAYCHANGE is broadcast to top-level windows. WS_EX_TOOLWINDOW keeps it
// out of the alt-tab list, and since it is never shown it never paints and never
// takes foreground.
func createFocusWatcherWindow() (uintptr, error) {
	err := focusWatcherClassOnce()
	if err != nil {
		return 0, err
	}

	className, err := windows.UTF16PtrFromString(focusWatcherClassName)
	if err != nil {
		return 0, err
	}

	hwnd, _, callErr := procCreateWindowExW.Call(
		wsExToolWindow,
		uintptr(unsafe.Pointer(className)),
		0,
		wsPopup,
		0,
		0,
		0,
		0,
		0,
		0,
		moduleHandle(),
		0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("CreateWindowExW: %w", callErr)
	}

	return hwnd, nil
}

func registerFocusWatcherClass() error {
	className, err := windows.UTF16PtrFromString(focusWatcherClassName)
	if err != nil {
		return err
	}

	class := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   focusWndProcPtr(),
		hInstance:     windows.Handle(moduleHandle()),
		lpszClassName: className,
	}

	atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return fmt.Errorf("RegisterClassExW: %w", callErr)
	}

	return nil
}

// focusWinEventProc is the WINEVENTPROC Windows calls for a foreground change.
// Every parameter is declared uintptr so syscall.NewCallback sees only
// pointer-sized arguments; the HWND that gained foreground is deliberately
// ignored, because the app watcher re-queries the focused identity instead.
func focusWinEventProc(_, event, _, _, _, _, _ uintptr) uintptr {
	if event != eventSystemForeground {
		return 0
	}

	current := activeFocusWatcher.Load()
	if current != nil {
		notifyFocusChannel(current.focus)
	}

	return 0
}

func focusWndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	if uint32(message) == wmDisplayChange {
		current := activeFocusWatcher.Load()
		if current != nil {
			notifyFocusChannel(current.screen)
		}
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)

	return ret
}

// notifyFocusChannel signals a channel without ever blocking. Both callbacks run
// on the pump thread, so a blocking send would stall the very thread that has to
// drain the queue for the next event to arrive at all.
func notifyFocusChannel(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
