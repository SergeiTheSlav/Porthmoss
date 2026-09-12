package capture

import (
	"fmt"
	"sync"
	"unsafe"
)

var (
	procSetCursorPos = user32.NewProc("SetCursorPos")
	procClipCursor   = user32.NewProc("ClipCursor")
)

type point32 struct{ x, y int32 }

type rect32 struct{ left, top, right, bottom int32 }

// WindowsPointer parks the physical pointer with ClipCursor and SetCursorPos.
//
// Swallowing the mouse event is not enough on its own. The Mac learnt this the
// hard way — its window server moved the cursor from the HID stream whatever
// the event tap returned, and it had to be warped back on every event — and
// Windows is assumed to be no better until someone has actually watched the
// pointer sit still on real hardware.
//
// Clipping alone will not do either, in the other direction: a pointer clipped
// to a single pixel cannot move, so Windows reports no movement, and there is
// nothing left to send the Mac. The pointer is therefore clipped to the
// display it crossed from — enough room to generate movement — and warped back
// to the middle of it after every event.
type WindowsPointer struct {
	mu     sync.Mutex
	parked bool
	clip   rect32
}

// NewPointer returns the Win32 pointer control.
func NewPointer() *WindowsPointer { return &WindowsPointer{} }

func (p *WindowsPointer) Park(within Rect) (Point, error) {
	if within.W <= 0 || within.H <= 0 {
		return Point{}, fmt.Errorf("capture: cannot park inside an empty display %+v", within)
	}
	// The middle of the display, so there is room to move in every direction
	// before the pointer runs into an edge and stops reporting movement.
	anchor := Point{
		X: float64(int32(within.X + within.W/2)),
		Y: float64(int32(within.Y + within.H/2)),
	}
	clip := rect32{
		left:   int32(within.X),
		top:    int32(within.Y),
		right:  int32(within.MaxX()),
		bottom: int32(within.MaxY()),
	}

	p.mu.Lock()
	p.clip = clip
	p.parked = true
	p.mu.Unlock()

	if err := setCursorPos(anchor); err != nil {
		return Point{}, err
	}
	// A failed clip is survivable and not worth refusing the crossing over:
	// the warp on every event is what really holds the pointer, and the clip
	// only stops it wandering onto another monitor in between.
	clipCursor(&clip)
	return anchor, nil
}

func (p *WindowsPointer) Warp(to Point) {
	setCursorPos(to)

	// Windows drops a cursor clip on a foreground-window change, a UAC prompt,
	// a lock and a desktop switch, without telling anyone. Re-asserting it
	// here costs one syscall on a path that is already making one, and saves
	// discovering it as "the pointer escapes onto the other monitor
	// sometimes".
	p.mu.Lock()
	clip := p.clip
	parked := p.parked
	p.mu.Unlock()
	if parked {
		clipCursor(&clip)
	}
}

func (p *WindowsPointer) Release(to Point) error {
	p.mu.Lock()
	p.parked = false
	p.mu.Unlock()

	// Unclip first. Putting the pointer back inside a clip that still holds it
	// to the wrong display would silently move it somewhere else.
	if err := clipCursor(nil); err != nil {
		return err
	}
	return setCursorPos(to)
}

func setCursorPos(p Point) error {
	// The arguments are 32-bit ints and the desktop can start at a negative
	// coordinate, so the sign has to survive the trip through uintptr.
	ok, _, err := procSetCursorPos.Call(
		uintptr(uint32(int32(p.X))),
		uintptr(uint32(int32(p.Y))),
	)
	if ok == 0 {
		return fmt.Errorf("SetCursorPos(%v, %v): %w", p.X, p.Y, err)
	}
	return nil
}

// clipCursor confines the pointer to r, or releases it when r is nil.
//
// The two calls are spelled out rather than sharing a uintptr, because the
// conversion has to happen inside the call expression: a uintptr holding an
// address is not a reference the garbage collector can see, and the rect would
// be free to move between the conversion and the call.
func clipCursor(r *rect32) error {
	var (
		ok  uintptr
		err error
	)
	if r == nil {
		ok, _, err = procClipCursor.Call(0)
	} else {
		ok, _, err = procClipCursor.Call(uintptr(unsafe.Pointer(r)))
	}
	if ok == 0 {
		return fmt.Errorf("ClipCursor: %w", err)
	}
	return nil
}

var _ Pointer = (*WindowsPointer)(nil)
