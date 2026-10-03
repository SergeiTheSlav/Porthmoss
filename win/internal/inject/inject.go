// Package inject turns Porthmoss input messages into real OS input events.
package inject

import "github.com/SergeiTheSlav/Porthmoss/win/internal/proto"

// Injector delivers input to the local desktop. Implementations are not
// required to be safe for concurrent use; the server drives one from a single
// goroutine.
type Injector interface {
	// EnterAt places the cursor at a normalised 0..65535 point on the virtual
	// desktop, and makes that the baseline for later movement. Sent once, when
	// the Mac crosses over.
	EnterAt(x, y uint16) error

	// MoveTo applies the movement since the previous request to wherever the
	// pointer actually is now.
	//
	// Not an absolute placement, despite the absolute coordinates on the wire.
	// The PC's own mouse is still live while the Mac drives it, and if the user
	// nudges it, an absolute placement undoes that nudge on the very next
	// message — the pointer visibly teleports back. Applying the difference
	// instead means both mice move one cursor, and neither cancels the other.
	MoveTo(x, y uint16) error
	// Button presses or releases a mouse button.
	Button(button byte, down bool) error
	// Wheel scrolls by whole wheel notches.
	Wheel(dx, dy int16) error
	// Key presses or releases a PS/2 set-1 scancode.
	Key(scancode uint16, down bool, extended bool) error
	// ReleaseAll releases everything currently held down. It is called on
	// disconnect, on LEAVE, and when the dead-man switch fires, so a dropped
	// connection can never leave a key stuck.
	ReleaseAll() error
	// Screens reports the current display layout.
	Screens() (proto.ScreenInfo, error)
}
