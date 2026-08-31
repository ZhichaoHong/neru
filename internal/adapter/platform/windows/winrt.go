//go:build windows

package windows

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Pure-Go WinRT plumbing. CGO is disabled for every Windows build in the
// justfile, so the OCR half of the vision strategy is driven through raw vtable
// calls rather than through a C++/WinRT wrapper. Nothing here is OCR-specific;
// ocr.go is the only caller today.
//
// Vtable slot numbering used throughout:
//
//	IUnknown:     0 QueryInterface, 1 AddRef, 2 Release
//	IInspectable: 3 GetIids, 4 GetRuntimeClassName, 5 GetTrustLevel
//
// so the first interface-specific method of an IInspectable-derived interface is
// slot 6, and of an IUnknown-derived interface is slot 3. Getting that wrong
// corrupts memory instead of failing cleanly, which is why every slot index in
// ocr.go is named beside the interface it belongs to.
//
// Every COM interface pointer here is typed unsafe.Pointer rather than uintptr,
// the same as the UI Automation bridge: it makes each dereference a Pointer->*T
// conversion, which is what keeps go vet's unsafeptr check happy, and it stops a
// handle being mistaken for a number.

var (
	combase = windows.NewLazySystemDLL("combase.dll")

	procRoInitialize              = combase.NewProc("RoInitialize")
	procRoGetActivationFactory    = combase.NewProc("RoGetActivationFactory")
	procWindowsCreateString       = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString       = combase.NewProc("WindowsDeleteString")
	procWindowsGetStringRawBuffer = combase.NewProc("WindowsGetStringRawBuffer")
)

// roInitMultithreaded is RO_INIT_MULTITHREADED. The OCR thread has no message
// pump, so it joins the multi-threaded apartment for the same reason the UI
// Automation worker does.
const roInitMultithreaded = 1

// bytesPerUTF16Unit is sizeof(WCHAR), which the HSTRING copy below needs because
// WindowsGetStringRawBuffer reports a length in characters, not bytes.
const bytesPerUTF16Unit = 2

// releaseSlot is IUnknown::Release.
const releaseSlot = 2

// HRESULTs worth naming. Each of these is reachable from the OCR path and each
// means something a reader would otherwise have to look up.
const (
	hrSFalse             = 0x00000001
	hrEAbort             = 0x80004004
	hrEInvalidArg        = 0x80070057
	hrRPCChangedMode     = 0x80010106
	hrClassNotRegistered = 0x80040154
	hrHighBit            = 0x80000000
)

// comErrorNames turns the HRESULTs the OCR path can actually produce into the
// sentence that says what happened. Anything else is reported as a bare code.
var comErrorNames = map[uint32]string{
	hrEAbort: "E_ABORT (an overlapping RecognizeAsync on one engine, " +
		"or a canceled one still draining)",
	hrEInvalidArg: "E_INVALIDARG (image over MaxImageDimension, " +
		"or an alpha mode the pixel format has no alpha for)",
	hrClassNotRegistered: "REGDB_E_CLASSNOTREG " +
		"(the WinRT class is not registered on this machine)",
	0xC00D7170: "MF_E_BUFFERTOOSMALL (buffer shorter than width*height*bpp)",
	0x8000000E: "E_ILLEGAL_METHOD_CALL (async operation touched after Close)",
	0x80004002: "E_NOINTERFACE",
	0x800401F0: "CO_E_NOTINITIALIZED (this thread never joined a COM apartment)",
}

// comError is one failed COM call: the HRESULT plus the call that produced it.
type comError struct {
	hr  uint32
	ctx string
}

func (e *comError) Error() string {
	if name, ok := comErrorNames[e.hr]; ok {
		return fmt.Sprintf("%s: hr=0x%08X %s", e.ctx, e.hr, name)
	}

	return fmt.Sprintf("%s: hr=0x%08X", e.ctx, e.hr)
}

// comCheck turns an HRESULT into an error, or nil for any success code.
func comCheck(ctx string, result uintptr) error {
	hr := uint32(result)
	if hr&hrHighBit != 0 {
		return &comError{hr: hr, ctx: ctx}
	}

	return nil
}

// isCOMError reports whether err is the given HRESULT, so a caller can branch on
// one failure without matching on its message.
func isCOMError(err error, hr uint32) bool {
	var target *comError

	return errors.As(err, &target) && target.hr == hr
}

// comMethod reads one vtable slot off a COM interface pointer.
func comMethod(this unsafe.Pointer, slot int) uintptr {
	vtable := *(*unsafe.Pointer)(this)

	return *(*uintptr)(unsafe.Add(vtable, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
}

// comVCall invokes vtable slot on this and checks the HRESULT it returns.
//
// The directive below is load-bearing, not decoration. syscall.SyscallN is
// //go:nosplit and //go:uintptrkeepalive, so converting an out-parameter's
// address to uintptr is only safe inside *its* argument list. Routing that
// uintptr through a wrapper loses both guarantees: the append below can grow the
// stack, the goroutine stack moves, and the callee writes its out-parameter into
// the abandoned frame - leaving the caller reading a zero next to a successful
// HRESULT. //go:uintptrescapes forces those pointees onto the heap and keeps
// them alive across the call, which is why syscall.Proc.Call carries the same
// directive. go vet does not flag its absence; `go build -gcflags=-m` does, by
// showing the out-parameters move from the stack to the heap.
//
//go:uintptrescapes
func comVCall(ctx string, this unsafe.Pointer, slot int, args ...uintptr) error {
	if this == nil {
		return fmt.Errorf("%s: nil COM pointer", ctx)
	}

	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(this))
	all = append(all, args...)

	result, _, _ := syscall.SyscallN(comMethod(this, slot), all...)

	return comCheck(ctx, result)
}

// comRelease drops one reference. A nil pointer is ignored so callers can
// release unconditionally on their error paths.
func comRelease(this unsafe.Pointer) {
	if this == nil {
		return
	}

	syscall.SyscallN(comMethod(this, releaseSlot), uintptr(this))
}

// iidIClosable is IClosable, whose Close is slot 6.
var iidIClosable = mustIID("{30D5A829-7FA4-4026-83BB-D75BAE4EA99E}")

// comQueryInterface asks this for another interface. A null pointer beside a
// successful HRESULT is treated as a failure rather than returned, because every
// caller would otherwise have to check it again.
func comQueryInterface(
	this unsafe.Pointer,
	iid windows.GUID,
	ctx string,
) (unsafe.Pointer, error) {
	var out unsafe.Pointer

	err := comVCall(ctx, this, 0,
		uintptr(unsafe.Pointer(&iid)),
		uintptr(unsafe.Pointer(&out)),
	)

	runtime.KeepAlive(iid)

	if err != nil {
		return nil, err
	}

	if out == nil {
		return nil, fmt.Errorf("%s: QueryInterface returned S_OK with a null pointer", ctx)
	}

	return out, nil
}

// comClose calls IClosable.Close on an object that implements it, and does
// nothing for one that does not. WinRT objects holding native resources - a
// locked bitmap buffer, a finished async operation - need this before their last
// Release, and there is nothing to report when it fails: the Release that
// follows is what frees the memory either way.
func comClose(this unsafe.Pointer) {
	closable, err := comQueryInterface(this, iidIClosable, "IClosable")
	if err != nil {
		return
	}

	_ = comVCall("IClosable.Close", closable, 6)

	comRelease(closable)
}

// mustIID parses an interface ID written the way the Windows SDK headers write
// it. It panics, because every caller is a package-level literal: a typo is a
// build-time mistake, not a runtime condition.
func mustIID(value string) windows.GUID {
	guid, err := windows.GUIDFromString(value)
	if err != nil {
		panic("bad IID literal " + value + ": " + err.Error())
	}

	return guid
}

// hstring is a WinRT HSTRING. The zero value is the empty string, which WinRT
// treats as legal rather than as a null pointer.
type hstring uintptr

// newHString allocates an HSTRING. The caller frees it.
func newHString(value string) (hstring, error) {
	utf16, err := syscall.UTF16FromString(value)
	if err != nil {
		return 0, err
	}

	var out hstring

	result, _, _ := procWindowsCreateString.Call(
		uintptr(unsafe.Pointer(&utf16[0])),
		uintptr(len(utf16)-1),
		uintptr(unsafe.Pointer(&out)),
	)

	runtime.KeepAlive(utf16)

	if err := comCheck("WindowsCreateString", result); err != nil {
		return 0, err
	}

	return out, nil
}

func (h hstring) free() {
	if h != 0 {
		procWindowsDeleteString.Call(uintptr(h))
	}
}

// String copies an HSTRING into a Go string.
//
// The copy goes through RtlMoveMemory rather than through unsafe.Slice on the
// buffer WinRT points at, and that is not a style choice.
// WindowsGetStringRawBuffer hands the PCWSTR back as its *return value*, so
// there is no pointer-typed out-parameter to route it through, and converting
// that uintptr to an unsafe.Pointer is exactly what go vet's unsafeptr check
// rejects. Handing it straight back to a syscall keeps it a number on the Go
// side. These are short strings - one word, or one line of recognized text - so
// the extra copy is cheaper than a suppressed check.
func (h hstring) String() string {
	if h == 0 {
		return ""
	}

	var length uint32

	buffer, _, _ := procWindowsGetStringRawBuffer.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&length)),
	)
	if buffer == 0 || length == 0 {
		return ""
	}

	utf16 := make([]uint16, length)

	procRtlMoveMemory.Call(
		uintptr(unsafe.Pointer(&utf16[0])),
		buffer,
		uintptr(length)*bytesPerUTF16Unit,
	)

	return syscall.UTF16ToString(utf16)
}

// readHStringOut reads an [out] HSTRING property off slot, freeing the HSTRING
// once its contents have been copied out.
func readHStringOut(ctx string, this unsafe.Pointer, slot int) (string, error) {
	var out hstring

	if err := comVCall(ctx, this, slot, uintptr(unsafe.Pointer(&out))); err != nil {
		return "", err
	}

	defer out.free()

	return out.String(), nil
}

// initMTA joins the calling OS thread to the multi-threaded apartment.
//
// COM apartments are per-thread, so every OS thread that touches a vtable needs
// this - not just the first one. Callers must runtime.LockOSThread beforehand,
// or Go may migrate the goroutine onto a thread that never joined. comThread is
// the only caller and does both.
func initMTA() error {
	result, _, _ := procRoInitialize.Call(roInitMultithreaded)

	// S_FALSE means this thread had already joined. RPC_E_CHANGED_MODE means it
	// is in a single-threaded apartment instead, which is fine for the agile
	// objects the OCR path uses.
	if hr := uint32(result); hr == hrSFalse || hr == hrRPCChangedMode {
		return nil
	}

	return comCheck("RoInitialize", result)
}

// activationFactory resolves a WinRT runtime class to one of its factory or
// statics interfaces.
func activationFactory(class string, iid windows.GUID) (unsafe.Pointer, error) {
	name, err := newHString(class)
	if err != nil {
		return nil, err
	}

	defer name.free()

	var factory unsafe.Pointer

	result, _, _ := procRoGetActivationFactory.Call(
		uintptr(name),
		uintptr(unsafe.Pointer(&iid)),
		uintptr(unsafe.Pointer(&factory)),
	)

	runtime.KeepAlive(iid)

	if err := comCheck("RoGetActivationFactory("+class+")", result); err != nil {
		return nil, err
	}

	if factory == nil {
		return nil, fmt.Errorf(
			"RoGetActivationFactory(%s): S_OK with a null factory",
			class,
		)
	}

	return factory, nil
}

// vectorView wraps IVectorView<T>, whose GetAt is slot 6 and get_Size slot 7.
//
// The element type never appears: every generic the OCR path reads arrives as an
// [out] [retval] already typed as the interface it is, so it is called directly
// rather than QueryInterface'd for with a parameterized IID.
type vectorView struct {
	ptr  unsafe.Pointer
	name string
}

func (v vectorView) size() (uint32, error) {
	var size uint32

	if err := v.call("get_Size", 7, uintptr(unsafe.Pointer(&size))); err != nil {
		return 0, err
	}

	return size, nil
}

// at returns the item at index, which the caller releases.
func (v vectorView) at(index uint32) (unsafe.Pointer, error) {
	var item unsafe.Pointer

	if err := v.call("GetAt", 6, uintptr(index), uintptr(unsafe.Pointer(&item))); err != nil {
		return nil, err
	}

	if item == nil {
		return nil, fmt.Errorf("%s.GetAt(%d): null item", v.name, index)
	}

	return item, nil
}

func (v vectorView) call(method string, slot int, args ...uintptr) error {
	return comVCall(v.name+"."+method, v.ptr, slot, args...)
}

func (v vectorView) release() { comRelease(v.ptr) }

// comThread is one OS thread joined to an apartment, and the only thread the
// WinRT objects above are ever touched from.
//
// Everything the OCR half owns - the activation factories, the engine, the
// bitmap for one recognition - is created, used and released here. That buys
// three things at once. The apartment join happens exactly once instead of on
// whichever goroutine the caller happened to be. The factories and the engine
// survive for the process, so no call pays the factory warm-up twice. And
// RecognizeAsync is single-flight per engine, which this enforces structurally:
// two concurrent callers queue rather than collide, and a collision would not
// fail at call time - it surfaces later as E_ABORT on an operation that already
// looked healthy.
type comThread struct {
	work chan func()
}

// newCOMThread starts the thread and joins its apartment, reporting a join that
// failed rather than leaving a thread nothing can be run on.
func newCOMThread() (*comThread, error) {
	thread := &comThread{work: make(chan func())}
	ready := make(chan error, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		if err := initMTA(); err != nil {
			ready <- err

			return
		}

		ready <- nil

		for fn := range thread.work {
			fn()
		}
	}()

	if err := <-ready; err != nil {
		close(thread.work)

		return nil, err
	}

	return thread, nil
}

// run executes fn on the COM thread and returns once it has finished.
func (t *comThread) run(fn func()) {
	done := make(chan struct{})

	t.work <- func() {
		defer close(done)

		fn()
	}

	<-done
}
