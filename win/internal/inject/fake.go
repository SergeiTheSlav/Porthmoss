package inject

import (
	"fmt"
	"sync"

	"github.com/SergeiTheSlav/Porthmoss/win/internal/proto"
)

// Fake records input instead of delivering it. It backs the agent on non-Windows
// hosts so the server, handshake and protocol can be exercised from a Mac.
type Fake struct {
	mu          sync.Mutex
	Events      []string
	Layout      proto.ScreenInfo
	heldKeys    map[uint16]bool
	heldButtons map[byte]bool

	pointerX, pointerY float64
	lastX, lastY       uint16
}

func NewFake() *Fake {
	return &Fake{
		Layout: proto.ScreenInfo{
			Virtual:  proto.Monitor{Left: 0, Top: 0, Width: 2560, Height: 1440},
			Monitors: []proto.Monitor{{Width: 2560, Height: 1440, Primary: true}},
		},
		heldKeys:    make(map[uint16]bool),
		heldButtons: make(map[byte]bool),
	}
}

func (f *Fake) record(format string, args ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Events = append(f.Events, fmt.Sprintf(format, args...))
	return nil
}

// Drain returns the recorded events and clears the log.
func (f *Fake) Drain() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.Events
	f.Events = nil
	return out
}

// Pointer is where the fake believes the cursor is, in virtual-desktop pixels.
// Tests move it directly to stand in for a hand on the PC's own mouse.
func (f *Fake) Pointer() (x, y float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pointerX, f.pointerY
}

// MoveLocally stands in for the user nudging the PC's own mouse.
func (f *Fake) MoveLocally(dx, dy float64) {
	f.mu.Lock()
	f.pointerX += dx
	f.pointerY += dy
	f.mu.Unlock()
}

func (f *Fake) EnterAt(x, y uint16) error {
	f.mu.Lock()
	f.pointerX, f.pointerY = f.toPixels(x, y)
	f.lastX, f.lastY = x, y
	f.mu.Unlock()
	return f.record("enter %d,%d", x, y)
}

func (f *Fake) MoveTo(x, y uint16) error {
	f.mu.Lock()
	dx, dy := f.deltaPixels(x, y)
	f.lastX, f.lastY = x, y
	f.pointerX += dx
	f.pointerY += dy
	f.mu.Unlock()
	return f.record("move %d,%d", x, y)
}

// toPixels converts a normalised point to virtual-desktop pixels.
func (f *Fake) toPixels(x, y uint16) (float64, float64) {
	v := f.Layout.Virtual
	return float64(v.Left) + float64(x)/65535*float64(v.Width-1),
		float64(v.Top) + float64(y)/65535*float64(v.Height-1)
}

// deltaPixels is how far the Mac means to move, in pixels.
func (f *Fake) deltaPixels(x, y uint16) (float64, float64) {
	v := f.Layout.Virtual
	return (float64(x) - float64(f.lastX)) / 65535 * float64(v.Width-1),
		(float64(y) - float64(f.lastY)) / 65535 * float64(v.Height-1)
}

func (f *Fake) Button(button byte, down bool) error {
	f.mu.Lock()
	if down {
		f.heldButtons[button] = true
	} else {
		delete(f.heldButtons, button)
	}
	f.mu.Unlock()
	return f.record("button %d down=%t", button, down)
}

func (f *Fake) Wheel(dx, dy int16) error { return f.record("wheel %d,%d", dx, dy) }

func (f *Fake) Key(scancode uint16, down, extended bool) error {
	f.mu.Lock()
	if down {
		f.heldKeys[scancode] = extended
	} else {
		delete(f.heldKeys, scancode)
	}
	f.mu.Unlock()
	return f.record("key 0x%02x down=%t ext=%t", scancode, down, extended)
}

func (f *Fake) ReleaseAll() error {
	f.mu.Lock()
	n := len(f.heldKeys) + len(f.heldButtons)
	clear(f.heldKeys)
	clear(f.heldButtons)
	f.mu.Unlock()
	return f.record("release-all (%d held)", n)
}

func (f *Fake) Screens() (proto.ScreenInfo, error) { return f.Layout, nil }

// Held reports how many keys and buttons the fake believes are down.
func (f *Fake) Held() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.heldKeys) + len(f.heldButtons)
}
