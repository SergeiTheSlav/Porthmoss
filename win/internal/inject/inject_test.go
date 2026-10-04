package inject

import (
	"math"
	"testing"
)

// near compares pixel positions, which are computed through a normalised
// integer wire format and so land a fraction of a pixel off.
func near(got, want float64) bool { return math.Abs(got-want) < 1.5 }

// The PC's desktop the fake stands in for: 2560x1440 at the origin.
const (
	deskW = 2560
	deskH = 1440
)

// norm turns a pixel coordinate into what the Mac would put on the wire.
func norm(pixel, size float64) uint16 {
	return uint16(pixel / (size - 1) * 65535)
}

// TestBothMiceMoveOneCursor is the behaviour the whole design turns on.
func TestBothMiceMoveOneCursor(t *testing.T) {
	f := NewFake()
	if err := f.EnterAt(norm(100, deskW), norm(100, deskH)); err != nil {
		t.Fatalf("EnterAt: %v", err)
	}
	if x, y := f.Pointer(); !near(x, 100) || !near(y, 100) {
		t.Fatalf("after EnterAt the pointer is at (%.0f,%.0f), want (100,100)", x, y)
	}

	// The Mac moves 200px right.
	if err := f.MoveTo(norm(300, deskW), norm(100, deskH)); err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	if x, _ := f.Pointer(); !near(x, 300) {
		t.Errorf("pointer x = %.0f, want 300", x)
	}

	// The user nudges the PC's own mouse 50px down.
	f.MoveLocally(0, 50)

	// The Mac moves another 100px right. The nudge must survive it.
	if err := f.MoveTo(norm(400, deskW), norm(100, deskH)); err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	x, y := f.Pointer()
	if !near(x, 400) {
		t.Errorf("pointer x = %.0f, want 400", x)
	}
	if !near(y, 150) {
		t.Errorf("pointer y = %.0f, want 150, the PC's own movement was undone", y)
	}
}

// TestEnterIsAbsolute: crossing over places the pointer exactly where the Mac
// says, rather than somewhere relative to wherever it happened to be.
func TestEnterIsAbsolute(t *testing.T) {
	f := NewFake()
	f.MoveLocally(900, 700)
	if err := f.EnterAt(0, 0); err != nil {
		t.Fatalf("EnterAt: %v", err)
	}
	if x, y := f.Pointer(); !near(x, 0) || !near(y, 0) {
		t.Errorf("entered at (%.0f,%.0f), want the top-left corner", x, y)
	}
}
