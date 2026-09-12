package inject

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"github.com/janjamscikov/porthmoss/win/internal/proto"
	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procSendInput                     = user32.NewProc("SendInput")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procEnumDisplayMonitors           = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
)

// INPUT is 40 bytes on 64-bit Windows: a 4-byte type, 4 bytes of alignment
// padding, then a 32-byte union. We fill the union by hand rather than
// declaring parallel structs so the layout is explicit and cannot drift.
const inputSize = 40

type input struct {
	typ   uint32
	_     uint32
	union [32]byte
}

// Compile-time assertion that our INPUT matches Windows'. A mismatch would make
// SendInput read garbage, and there is no way to catch that from a Mac — so it
// is caught by the cross-compiler instead. Both arrays are zero-length only
// when the sizes are equal; either direction of drift is a negative length.
var (
	_ [unsafe.Sizeof(input{}) - inputSize]byte
	_ [inputSize - unsafe.Sizeof(input{})]byte
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove        = 0x0001
	mouseeventfLeftDown    = 0x0002
	mouseeventfLeftUp      = 0x0004
	mouseeventfRightDown   = 0x0008
	mouseeventfRightUp     = 0x0010
	mouseeventfMiddleDown  = 0x0020
	mouseeventfMiddleUp    = 0x0040
	mouseeventfXDown       = 0x0080
	mouseeventfXUp         = 0x0100
	mouseeventfWheel       = 0x0800
	mouseeventfHWheel      = 0x1000
	mouseeventfNoCoalesce  = 0x2000
	mouseeventfVirtualDesk = 0x4000
	mouseeventfAbsolute    = 0x8000

	xbutton1 = 0x0001
	xbutton2 = 0x0002

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfScancode    = 0x0008

	wheelDelta = 120

	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	monitorinfofPrimary = 0x0001

	dpiAwarenessPerMonitorV2 = ^uintptr(3) // (HANDLE)-4
)

// Windows injector uses SendInput, which needs no driver and no elevation for
// ordinary desktop apps. It cannot reach the secure desktop (UAC prompts, the
// lock screen, Ctrl+Alt+Del) — that is a documented limitation, not a bug.
type Windows struct {
	heldKeys    map[uint16]bool // scancode -> extended
	heldButtons map[byte]bool

	// lastX, lastY is the position the Mac last asked for, in normalised
	// coordinates. Movement is applied as the difference from it, so the PC's
	// own mouse is never undone.
	lastX, lastY uint16
}

func New() (*Windows, error) {
	// Without per-monitor DPI awareness Windows lies to us about screen
	// geometry on scaled displays, and every coordinate lands in the wrong place.
	if procSetProcessDpiAwarenessContext.Find() == nil {
		procSetProcessDpiAwarenessContext.Call(dpiAwarenessPerMonitorV2)
	}
	return &Windows{
		heldKeys:    make(map[uint16]bool),
		heldButtons: make(map[byte]bool),
	}, nil
}

func (w *Windows) send(inputs ...input) error {
	n, _, err := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		inputSize,
	)
	if int(n) != len(inputs) {
		return fmt.Errorf("SendInput sent %d of %d events: %w", n, len(inputs), err)
	}
	return nil
}

// mouseInput builds an INPUT holding a MOUSEINPUT:
// LONG dx, LONG dy, DWORD mouseData, DWORD dwFlags, DWORD time, ULONG_PTR dwExtraInfo.
func mouseInput(dx, dy int32, mouseData, flags uint32) input {
	var in input
	in.typ = inputMouse
	binary.LittleEndian.PutUint32(in.union[0:], uint32(dx))
	binary.LittleEndian.PutUint32(in.union[4:], uint32(dy))
	binary.LittleEndian.PutUint32(in.union[8:], mouseData)
	binary.LittleEndian.PutUint32(in.union[12:], flags)
	// union[16:20] time = 0, union[20:24] padding, union[24:32] dwExtraInfo = 0
	return in
}

// keybdInput builds an INPUT holding a KEYBDINPUT:
// WORD wVk, WORD wScan, DWORD dwFlags, DWORD time, ULONG_PTR dwExtraInfo.
func keybdInput(scancode uint16, flags uint32) input {
	var in input
	in.typ = inputKeyboard
	// wVk stays 0: with KEYEVENTF_SCANCODE the scancode is authoritative.
	binary.LittleEndian.PutUint16(in.union[2:], scancode)
	binary.LittleEndian.PutUint32(in.union[4:], flags)
	return in
}

// EnterAt places the pointer where the Mac says it crossed over, absolutely,
// and makes that the baseline for the movement that follows.
func (w *Windows) EnterAt(x, y uint16) error {
	w.lastX, w.lastY = x, y
	return w.warp(x, y)
}

// MoveTo applies the Mac's movement to wherever the pointer actually is.
//
// The coordinates on the wire are absolute, but placing the pointer at them
// would undo anything the PC's own mouse did in between: the user nudges it,
// the next message from the Mac snaps it back, and the cursor visibly
// teleports. Taking the difference instead lets both mice move one cursor,
// with neither cancelling the other.
func (w *Windows) MoveTo(x, y uint16) error {
	screens, err := w.Screens()
	if err != nil {
		return err
	}
	virtual := screens.Virtual

	deltaX := (float64(x) - float64(w.lastX)) / 65535 * float64(virtual.Width-1)
	deltaY := (float64(y) - float64(w.lastY)) / 65535 * float64(virtual.Height-1)
	w.lastX, w.lastY = x, y

	current, ok := w.cursor()
	if !ok {
		// No idea where the pointer is, so the absolute position the Mac asked
		// for is the best answer available.
		return w.warp(x, y)
	}

	targetX := clampFloat(float64(current.X)+deltaX,
		float64(virtual.Left), float64(virtual.Left+virtual.Width-1))
	targetY := clampFloat(float64(current.Y)+deltaY,
		float64(virtual.Top), float64(virtual.Top+virtual.Height-1))

	return w.warp(
		normalise(targetX, float64(virtual.Left), float64(virtual.Width)),
		normalise(targetY, float64(virtual.Top), float64(virtual.Height)),
	)
}

// warp places the pointer at a normalised point. Absolute rather than relative
// so Windows' pointer acceleration is not applied to it a second time.
func (w *Windows) warp(x, y uint16) error {
	return w.send(mouseInput(int32(x), int32(y), 0,
		mouseeventfMove|mouseeventfAbsolute|mouseeventfVirtualDesk|mouseeventfNoCoalesce))
}

type point struct{ X, Y int32 }

// cursor reads where the pointer actually is, which is not necessarily where
// this agent last put it: the user's own hand is still on the PC's mouse.
func (w *Windows) cursor() (point, bool) {
	var p point
	ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p, ret != 0
}

func clampFloat(v, low, high float64) float64 { return math.Min(math.Max(v, low), high) }

func normalise(pixel, origin, size float64) uint16 {
	if size <= 1 {
		return 0
	}
	return uint16(clampFloat(math.Round((pixel-origin)/(size-1)*65535), 0, 65535))
}

func (w *Windows) Button(button byte, down bool) error {
	flags, data, err := buttonFlags(button, down)
	if err != nil {
		return err
	}
	if err := w.send(mouseInput(0, 0, data, flags)); err != nil {
		return err
	}
	if down {
		w.heldButtons[button] = true
	} else {
		delete(w.heldButtons, button)
	}
	return nil
}

func buttonFlags(button byte, down bool) (flags, data uint32, err error) {
	switch button {
	case proto.ButtonLeft:
		flags = pick(down, mouseeventfLeftDown, mouseeventfLeftUp)
	case proto.ButtonRight:
		flags = pick(down, mouseeventfRightDown, mouseeventfRightUp)
	case proto.ButtonMiddle:
		flags = pick(down, mouseeventfMiddleDown, mouseeventfMiddleUp)
	case proto.ButtonX1:
		flags, data = pick(down, mouseeventfXDown, mouseeventfXUp), xbutton1
	case proto.ButtonX2:
		flags, data = pick(down, mouseeventfXDown, mouseeventfXUp), xbutton2
	default:
		return 0, 0, fmt.Errorf("inject: unknown mouse button %d", button)
	}
	return flags, data, nil
}

func (w *Windows) Wheel(dx, dy int16) error {
	var events []input
	if dy != 0 {
		events = append(events, mouseInput(0, 0, uint32(int32(dy)*wheelDelta), mouseeventfWheel))
	}
	if dx != 0 {
		events = append(events, mouseInput(0, 0, uint32(int32(dx)*wheelDelta), mouseeventfHWheel))
	}
	if len(events) == 0 {
		return nil
	}
	return w.send(events...)
}

func (w *Windows) Key(scancode uint16, down, extended bool) error {
	flags := uint32(keyeventfScancode)
	if extended {
		flags |= keyeventfExtendedKey
	}
	if !down {
		flags |= keyeventfKeyUp
	}
	if err := w.send(keybdInput(scancode, flags)); err != nil {
		return err
	}
	if down {
		w.heldKeys[scancode] = extended
	} else {
		delete(w.heldKeys, scancode)
	}
	return nil
}

func (w *Windows) ReleaseAll() error {
	var firstErr error
	for scancode, extended := range w.heldKeys {
		if err := w.Key(scancode, false, extended); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for button := range w.heldButtons {
		if err := w.Button(button, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	clear(w.heldKeys)
	clear(w.heldButtons)
	return firstErr
}

type rect struct{ left, top, right, bottom int32 }

type monitorInfo struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
}

func (w *Windows) Screens() (proto.ScreenInfo, error) {
	info := proto.ScreenInfo{
		Virtual: proto.Monitor{
			Left:   int32(systemMetric(smXVirtualScreen)),
			Top:    int32(systemMetric(smYVirtualScreen)),
			Width:  int32(systemMetric(smCXVirtualScreen)),
			Height: int32(systemMetric(smCYVirtualScreen)),
		},
	}

	callback := windows.NewCallback(func(hMonitor, hdc, lprc, data uintptr) uintptr {
		mi := monitorInfo{cbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
		if ok, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi))); ok == 0 {
			return 1 // skip this one, keep enumerating
		}
		info.Monitors = append(info.Monitors, proto.Monitor{
			Left:    mi.rcMonitor.left,
			Top:     mi.rcMonitor.top,
			Width:   mi.rcMonitor.right - mi.rcMonitor.left,
			Height:  mi.rcMonitor.bottom - mi.rcMonitor.top,
			Primary: mi.dwFlags&monitorinfofPrimary != 0,
		})
		return 1
	})
	if ok, _, err := procEnumDisplayMonitors.Call(0, 0, callback, 0); ok == 0 {
		return proto.ScreenInfo{}, fmt.Errorf("EnumDisplayMonitors: %w", err)
	}
	if info.Virtual.Width <= 0 || info.Virtual.Height <= 0 {
		return proto.ScreenInfo{}, fmt.Errorf("inject: implausible virtual desktop %dx%d",
			info.Virtual.Width, info.Virtual.Height)
	}
	return info, nil
}

func systemMetric(index int) int {
	v, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(int32(v))
}

func pick(cond bool, whenTrue, whenFalse uint32) uint32 {
	if cond {
		return whenTrue
	}
	return whenFalse
}
