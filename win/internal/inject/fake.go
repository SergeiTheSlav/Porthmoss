package inject

import (
	"fmt"
	"sync"

	"github.com/janjamscikov/porthmoss/win/internal/proto"
)

// Fake records input instead of delivering it. It backs the agent on non-Windows
// hosts so the server, handshake and protocol can be exercised from a Mac.
type Fake struct {
	mu          sync.Mutex
	Events      []string
	Layout      proto.ScreenInfo
	heldKeys    map[uint16]bool
	heldButtons map[byte]bool
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

func (f *Fake) MoveTo(x, y uint16) error { return f.record("move %d,%d", x, y) }

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
