package capture

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"unsafe"

	"github.com/janjamscikov/porthmoss/win/internal/proto"
	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procMapVirtualKeyW      = user32.NewProc("MapVirtualKeyW")

	procGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")
)

const (
	whKeyboardLL = 13
	whMouseLL    = 14

	hcAction = 0

	wmQuit = 0x0012

	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmMouseWheel  = 0x020A
	wmXButtonDown = 0x020B
	wmXButtonUp   = 0x020C
	wmMouseHWheel = 0x020E

	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105

	// llmhfInjected marks a mouse event that came from SendInput rather than
	// from a hand. Both machines inject: the Mac's input arrives here as
	// SendInput, and this agent's own pointer warping is injected too.
	// Forwarding either would set the two driving each other.
	llmhfInjected = 0x00000001

	// llkhfExtended is the E0 prefix — arrows, right-hand modifiers, numpad
	// enter and friends. It travels to the Mac as proto.KeyFlagExtended.
	llkhfExtended = 0x01
	llkhfInjected = 0x10

	xbutton1 = 0x0001

	wheelDelta = 120

	// mapvkVKToVSC asks MapVirtualKeyW for a scancode, for the handful of
	// events that arrive with none.
	mapvkVKToVSC = 0
)

// mouseLLHook is MSLLHOOKSTRUCT. The explicit padding word keeps dwExtraInfo
// at offset 24 on 64-bit Windows, where the C struct puts it.
type mouseLLHook struct {
	pt          point32
	mouseData   uint32
	flags       uint32
	time        uint32
	_           uint32
	dwExtraInfo uintptr
}

// keyboardLLHook is KBDLLHOOKSTRUCT.
type keyboardLLHook struct {
	vkCode      uint32
	scanCode    uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

// msgStruct is MSG. Only GetMessageW writes it, but the layout still has to be
// right or it scribbles past the end.
type msgStruct struct {
	hwnd    uintptr
	message uint32
	_       uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point32
	_       uint32
}

// Compile-time assertions that these match Windows' own structs. A mismatch
// would make the hook read garbage, and there is no way to catch that from a
// Mac — so the cross-compiler catches it instead. Both arrays in a pair are
// zero-length only when the sizes are equal; either direction of drift gives
// one of them a negative length. Same trick as internal/inject.
var (
	_ [unsafe.Sizeof(mouseLLHook{}) - 32]byte
	_ [32 - unsafe.Sizeof(mouseLLHook{})]byte
	_ [unsafe.Sizeof(keyboardLLHook{}) - 24]byte
	_ [24 - unsafe.Sizeof(keyboardLLHook{})]byte
	_ [unsafe.Sizeof(msgStruct{}) - 48]byte
	_ [48 - unsafe.Sizeof(msgStruct{})]byte
)

// hookData turns the LPARAM a hook procedure is handed into a pointer.
//
// go vet's unsafeptr check flags this, and is right to in the general case: a
// uintptr is not a reference the garbage collector can see. It is safe here
// because the struct belongs to Windows, lives for the duration of the call,
// and is not in the Go heap at all — there is nothing to move or reclaim.
// Isolated in one place so the exception is visible rather than scattered,
// exactly as internal/clipboard does it.
func hookData[T any](lParam uintptr) *T {
	return (*T)(unsafe.Pointer(lParam)) //nolint:govet
}

// Hook is a pair of low-level Windows hooks on a thread of their own.
//
// It has to be its own thread. A low-level hook is delivered through the
// installing thread's message queue, so that thread needs a message loop —
// and neither loop the agent already has will do: systray locks the main OS
// thread and owns the main loop, and the WebView2 window owns a locked thread
// of its own (see internal/ui/windows.go). Sharing either would put input
// latency behind a UI repaint, and Windows removes a hook that answers too
// slowly.
//
// There is no notification when that happens — unlike a CGEventTap, which at
// least tells the Mac it was disabled — so the only defence is for the hook
// procedures to do no I/O whatever. They decide, queue, and return.
type Hook struct {
	controller *Controller
	log        *slog.Logger

	// Held for the life of the process: windows.NewCallback allocates from a
	// fixed-size table it can never free, so these are made once.
	mouseCallback    uintptr
	keyboardCallback uintptr

	// Wheel remainders, for high-resolution wheels that report less than a
	// whole notch at a time. Touched only from the hook thread, so no lock:
	// rounding each event to zero on its own would make a precision wheel
	// scroll nothing at all.
	remainderX, remainderY int32

	mu       sync.Mutex
	threadID uint32
	started  bool
	stopped  chan struct{}
}

// NewHook returns a hook that feeds c. Nothing is installed until Start.
func NewHook(c *Controller, log *slog.Logger) *Hook {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	h := &Hook{controller: c, log: log, stopped: make(chan struct{})}
	h.mouseCallback = windows.NewCallback(h.mouseProc)
	h.keyboardCallback = windows.NewCallback(h.keyboardProc)
	return h
}

// Start installs the hooks on their own thread and returns once they are up,
// or with the error that stopped them going up.
func (h *Hook) Start() error {
	h.mu.Lock()
	if h.started {
		h.mu.Unlock()
		return nil
	}
	h.started = true
	h.mu.Unlock()

	ready := make(chan error, 1)
	go h.run(ready)
	return <-ready
}

// Stop removes the hooks and lets the thread go. It waits, so a caller
// shutting down knows the user's input is their own again.
func (h *Hook) Stop() {
	h.mu.Lock()
	threadID, started := h.threadID, h.started
	h.mu.Unlock()
	if !started || threadID == 0 {
		return
	}
	procPostThreadMessageW.Call(uintptr(threadID), wmQuit, 0, 0)
	<-h.stopped
}

func (h *Hook) run(ready chan<- error) {
	// The hooks, the message loop and the thread live and die together.
	// Unlocking would let the runtime move this goroutine to another thread,
	// where the messages that drive the hooks would never arrive.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(h.stopped)

	threadID, _, _ := procGetCurrentThreadID.Call()
	h.mu.Lock()
	h.threadID = uint32(threadID)
	h.mu.Unlock()

	module, _, _ := procGetModuleHandleW.Call(0)

	mouse, _, err := procSetWindowsHookExW.Call(whMouseLL, h.mouseCallback, module, 0)
	if mouse == 0 {
		ready <- fmt.Errorf("SetWindowsHookExW(WH_MOUSE_LL): %w", err)
		return
	}
	defer procUnhookWindowsHookEx.Call(mouse)

	keyboard, _, err := procSetWindowsHookExW.Call(whKeyboardLL, h.keyboardCallback, module, 0)
	if keyboard == 0 {
		ready <- fmt.Errorf("SetWindowsHookExW(WH_KEYBOARD_LL): %w", err)
		return
	}
	defer procUnhookWindowsHookEx.Call(keyboard)

	h.log.Info("capturing this PC's mouse and keyboard")
	ready <- nil

	var message msgStruct
	for {
		ret, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		switch int32(ret) {
		case 0: // WM_QUIT
			h.log.Debug("capture thread asked to stop")
			return
		case -1:
			h.log.Error("the capture message loop failed", "err", err)
			return
		}
	}
}

// mouseProc is WH_MOUSE_LL. It runs on the hook thread for every mouse event
// anywhere on the desktop, so it does no I/O of any kind.
func (h *Hook) mouseProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) != hcAction {
		return h.next(nCode, wParam, lParam)
	}
	event := hookData[mouseLLHook](lParam)

	// Our own pointer warping and the Mac's SendInput both arrive here.
	if event.flags&llmhfInjected != 0 {
		return h.next(nCode, wParam, lParam)
	}

	var swallow bool
	switch wParam {
	case wmMouseMove:
		swallow = h.controller.MouseMoved(Point{X: float64(event.pt.x), Y: float64(event.pt.y)})

	case wmLButtonDown, wmLButtonUp:
		swallow = h.controller.MouseButton(proto.ButtonLeft, wParam == wmLButtonDown)
	case wmRButtonDown, wmRButtonUp:
		swallow = h.controller.MouseButton(proto.ButtonRight, wParam == wmRButtonDown)
	case wmMButtonDown, wmMButtonUp:
		swallow = h.controller.MouseButton(proto.ButtonMiddle, wParam == wmMButtonDown)
	case wmXButtonDown, wmXButtonUp:
		button := byte(proto.ButtonX2)
		if event.mouseData>>16 == xbutton1 {
			button = proto.ButtonX1
		}
		swallow = h.controller.MouseButton(button, wParam == wmXButtonDown)

	case wmMouseWheel:
		if notches := h.accumulateY(wheelAmount(event.mouseData)); notches != 0 {
			swallow = h.controller.MouseWheel(0, notches)
		} else {
			// Part of a notch: consumed while capturing, so the fragments are
			// not applied to this PC as well as being counted for the Mac.
			swallow = h.controller.IsRemote()
		}
	case wmMouseHWheel:
		if notches := h.accumulateX(wheelAmount(event.mouseData)); notches != 0 {
			swallow = h.controller.MouseWheel(notches, 0)
		} else {
			swallow = h.controller.IsRemote()
		}
	}

	if swallow {
		// Returning non-zero instead of calling CallNextHookEx is how a
		// low-level hook consumes an event, exactly as the Mac's tap returns
		// NULL. Nothing below sees it, this PC's own desktop included.
		return 1
	}
	return h.next(nCode, wParam, lParam)
}

// keyboardProc is WH_KEYBOARD_LL.
func (h *Hook) keyboardProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) != hcAction {
		return h.next(nCode, wParam, lParam)
	}
	event := hookData[keyboardLLHook](lParam)
	if event.flags&llkhfInjected != 0 {
		return h.next(nCode, wParam, lParam)
	}

	down := wParam == wmKeyDown || wParam == wmSysKeyDown
	if !down && wParam != wmKeyUp && wParam != wmSysKeyUp {
		return h.next(nCode, wParam, lParam)
	}

	scancode := uint16(event.scanCode)
	if scancode == 0 {
		// Some virtual keyboards and remapping drivers send no scancode at
		// all. The virtual key still knows which physical key it stands for,
		// and the Mac can only be sent a scancode.
		mapped, _, _ := procMapVirtualKeyW.Call(uintptr(event.vkCode), mapvkVKToVSC)
		scancode = uint16(mapped)
		if scancode == 0 {
			return h.next(nCode, wParam, lParam)
		}
	}

	if h.controller.Key(scancode, down, event.flags&llkhfExtended != 0) {
		return 1
	}
	return h.next(nCode, wParam, lParam)
}

func (h *Hook) next(nCode, wParam, lParam uintptr) uintptr {
	// The hook handle argument is ignored on every supported version of
	// Windows, and passing zero is what the documentation now recommends;
	// keeping the handle only to hand it back would need a lock on the hot
	// path.
	ret, _, _ := procCallNextHookEx.Call(0, nCode, wParam, lParam)
	return ret
}

// wheelAmount pulls the signed wheel delta out of the high word of mouseData.
func wheelAmount(mouseData uint32) int32 {
	return int32(int16(mouseData >> 16))
}

// accumulateY turns wheel deltas into whole notches, keeping the remainder.
// Called only from the hook thread.
func (h *Hook) accumulateY(amount int32) int16 {
	h.remainderY += amount
	notches := h.remainderY / wheelDelta
	h.remainderY -= notches * wheelDelta
	return int16(notches)
}

func (h *Hook) accumulateX(amount int32) int16 {
	h.remainderX += amount
	notches := h.remainderX / wheelDelta
	h.remainderX -= notches * wheelDelta
	return int16(notches)
}
