package capture

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/janjamscikov/porthmoss/win/internal/proto"
)

// fakePointer stands in for the Win32 pointer parking, and records it so the
// ordering — park before ENTER, LEAVE before unpark — can be asserted from a
// Mac.
type fakePointer struct {
	mu       sync.Mutex
	anchor   Point
	parked   bool
	released []Point
	warped   []Point
	log      []string
}

func (p *fakePointer) Park(Rect) (Point, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.parked = true
	p.log = append(p.log, "park")
	return p.anchor, nil
}

func (p *fakePointer) Warp(to Point) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.warped = append(p.warped, to)
	if p.parked {
		p.log = append(p.log, "recentre")
	} else {
		p.log = append(p.log, "nudge")
	}
}

func (p *fakePointer) Release(to Point) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.parked = false
	p.released = append(p.released, to)
	p.log = append(p.log, "release")
	return nil
}

func (p *fakePointer) isParked() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.parked
}

func (p *fakePointer) events() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.log)
}

// recorder is a Sender that keeps what the controller tried to send.
type recorder struct {
	mu   sync.Mutex
	sent []proto.Frame
}

func (r *recorder) send(typ byte, body []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, proto.Frame{Type: typ, Body: slices.Clone(body)})
	return nil
}

func (r *recorder) frames() []proto.Frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.sent)
}

func (r *recorder) types() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.sent))
	for i, frame := range r.sent {
		out[i] = frame.Type
	}
	return out
}

// waitFor polls until cond holds, because the writer goroutine is what
// actually puts messages on the wire.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newRig(t *testing.T) (*Controller, *recorder, *fakePointer) {
	t.Helper()
	pointer := &fakePointer{anchor: Point{X: 1280, Y: 720}}
	rec := &recorder{}
	config := DefaultConfig()
	config.Edge = EdgeLeft
	c := New(Options{
		Config:        config,
		RemoteDesktop: macDesktop,
		Pointer:       pointer,
		Screens: func() (proto.ScreenInfo, error) {
			return proto.ScreenInfo{
				Virtual:  proto.Monitor{Left: 0, Top: 0, Width: 2560, Height: 1440},
				Monitors: []proto.Monitor{{Left: 0, Top: 0, Width: 2560, Height: 1440, Primary: true}},
			}, nil
		},
	})
	t.Cleanup(c.Close)
	// Reverse control is off until the Mac asks for it, so every test that
	// expects capture has to say so.
	c.SetEnabled(true)
	c.Attach(rec.send)
	return c, rec, pointer
}

// push shoves the pointer into the PC's left edge until control crosses over,
// the way a hand does: once the pointer is pinned at x=0 every further event
// reports the same position, and the controller pulls it clear so the next one
// carries real movement again.
func push(t *testing.T, c *Controller) {
	t.Helper()
	// The first movement only establishes a reference position: a low-level
	// hook reports where the pointer is, never how far it moved.
	c.MouseMoved(Point{X: 20, Y: 720})
	c.MouseMoved(Point{X: 10, Y: 720})
	for range 20 {
		c.MouseMoved(Point{X: 0, Y: 720})
		if c.IsRemote() {
			return
		}
	}
	t.Fatal("setup: never crossed onto the Mac")
}

func TestCapturedInputReachesTheMac(t *testing.T) {
	c, rec, pointer := newRig(t)
	push(t, c)

	if !pointer.isParked() {
		t.Error("the pointer must be parked the moment control crosses")
	}

	// While remote the hook reports positions around the anchor; the distance
	// from it is the movement.
	if !c.MouseMoved(Point{X: pointer.anchor.X + 12, Y: pointer.anchor.Y}) {
		t.Error("movement must be swallowed while the Mac has control")
	}
	if !c.MouseButton(proto.ButtonLeft, true) {
		t.Error("a click must be swallowed while the Mac has control")
	}
	if !c.MouseButton(proto.ButtonLeft, false) {
		t.Error("a release must be swallowed while the Mac has control")
	}
	if !c.MouseWheel(0, -3) {
		t.Error("a scroll must be swallowed while the Mac has control")
	}
	// Ctrl down, C, Ctrl up: the Mac turns the PC's Ctrl back into Cmd.
	if !c.Key(0x1D, true, false) || !c.Key(0x2E, true, false) ||
		!c.Key(0x2E, false, false) || !c.Key(0x1D, false, false) {
		t.Error("keys must be swallowed while the Mac has control")
	}

	want := []byte{
		proto.TypeEnter,
		proto.TypeMouseMove,
		proto.TypeMouseButton,
		proto.TypeMouseButton,
		proto.TypeMouseWheel,
		proto.TypeKey, proto.TypeKey, proto.TypeKey, proto.TypeKey,
	}
	waitFor(t, "every captured event to be sent", func() bool {
		return len(rec.types()) == len(want)
	})
	if got := rec.types(); !slices.Equal(got, want) {
		t.Errorf("sent message types\n got: %#v\nwant: %#v", got, want)
	}

	// ENTER must carry the Mac's right edge: the PC's left edge leads there.
	move, err := proto.DecodeMouseMove(rec.frames()[0].Body)
	if err != nil {
		t.Fatalf("decoding ENTER: %v", err)
	}
	if move.X != 65535 {
		t.Errorf("ENTER x = %d, want 65535", move.X)
	}
}

func TestNothingIsCapturedWithoutAMac(t *testing.T) {
	c, _, pointer := newRig(t)
	c.Detach()

	// A hard push at the edge with nobody to send to must leave the PC's own
	// input completely alone — swallowing it would strand the user.
	c.MouseMoved(Point{X: 20, Y: 720})
	c.MouseMoved(Point{X: 10, Y: 720})
	for range 20 {
		if c.MouseMoved(Point{X: 0, Y: 720}) {
			t.Fatal("input was swallowed with no Mac connected")
		}
	}
	if c.IsRemote() || pointer.isParked() {
		t.Error("control crossed over with no Mac connected")
	}
	if c.MouseButton(proto.ButtonLeft, true) || c.Key(0x2E, true, false) {
		t.Error("buttons and keys were swallowed with no Mac connected")
	}
}

func TestTheMacTakingControlSuspendsCapture(t *testing.T) {
	c, _, _ := newRig(t)
	// The Mac is driving this PC: its own edge detection must stay out of the
	// way, or the two would fight over one pointer.
	c.Suspend(true)
	c.MouseMoved(Point{X: 20, Y: 720})
	c.MouseMoved(Point{X: 10, Y: 720})
	for range 20 {
		if c.MouseMoved(Point{X: 0, Y: 720}) == false {
			t.Fatal("the PC's own mouse was let through while the Mac was driving it")
		}
	}
	if c.IsRemote() {
		t.Error("capture crossed over while suspended")
	}

	c.Suspend(false)
	push(t, c)
	if !c.IsRemote() {
		t.Error("capture should work again once the Mac hands this PC back")
	}
}

// TestTheMacDrivingLeavesOneCursor is why the test above changed.
//
// Passing the PC's own pointer input through while the Mac drives it gives one
// pointer two sources: the hand moves it, the Mac's next absolute position
// snaps it back, and the cursor visibly teleports. Reported from a real
// session, and the reason pointer input is now swallowed while suspended.
func TestTheMacDrivingLeavesOneCursor(t *testing.T) {
	c, rec, _ := newRig(t)
	c.Suspend(true)

	for _, p := range []Point{{X: 900, Y: 400}, {X: 400, Y: 200}, {X: 1500, Y: 900}} {
		if !c.MouseMoved(p) {
			t.Errorf("movement to %v reached Windows and would fight the Mac's cursor", p)
		}
	}
	if !c.MouseButton(proto.ButtonLeft, true) {
		t.Error("a click on the PC reached Windows while the Mac was driving it")
	}
	if !c.MouseWheel(0, 3) {
		t.Error("a scroll on the PC reached Windows while the Mac was driving it")
	}

	// The keyboard is deliberately not swallowed: two keyboards interleaving is
	// survivable, and a wedged Mac must not leave this PC unable to type.
	if c.Key(0x2E, true, false) {
		t.Error("the PC's keyboard was swallowed; a wedged Mac would lock the user out")
	}

	if len(rec.frames()) != 0 {
		t.Errorf("sent %d frames to the Mac while suspended", len(rec.frames()))
	}
}

func TestTheEscapeHotkeyHandsControlBack(t *testing.T) {
	c, rec, pointer := newRig(t)
	push(t, c)

	// Ctrl+Alt+Win down, then P.
	c.Key(scanControl, true, false)
	c.Key(scanAlt, true, false)
	c.Key(scanWinL, true, true)
	if !c.Key(scanP, true, false) {
		t.Error("the escape hotkey must be swallowed, never forwarded")
	}

	if c.IsRemote() {
		t.Error("the escape hotkey did not hand control back")
	}
	waitFor(t, "the pointer to be unparked", func() bool { return !pointer.isParked() })

	// LEAVE is what makes the Mac let go of everything it is holding, and it
	// has to be the last thing the Mac hears.
	waitFor(t, "LEAVE to be sent", func() bool {
		sent := rec.types()
		return len(sent) > 0 && sent[len(sent)-1] == proto.TypeLeave
	})
	// The P that triggered it must not have gone out with it.
	for _, frame := range rec.frames() {
		if frame.Type != proto.TypeKey {
			continue
		}
		key, _ := proto.DecodeKey(frame.Body)
		if key.Scancode == scanP {
			t.Error("the escape hotkey was forwarded to the Mac")
		}
	}
}

func TestTheHotkeyIsIgnoredWhenNotCapturing(t *testing.T) {
	c, _, _ := newRig(t)
	c.Key(scanControl, true, false)
	c.Key(scanAlt, true, false)
	c.Key(scanWinL, true, true)
	if c.Key(scanP, true, false) {
		t.Error("an agent that is not capturing has no business eating hotkeys")
	}
}

func TestLosingTheMacReturnsControlToThePC(t *testing.T) {
	c, rec, pointer := newRig(t)
	push(t, c)
	waitFor(t, "ENTER to be sent", func() bool { return len(rec.types()) > 0 })

	// A dropped link must never leave the user with a parked pointer and no
	// cursor on either machine.
	c.Detach()
	if c.IsRemote() {
		t.Error("still driving the Mac after the session ended")
	}
	waitFor(t, "the pointer to be unparked", func() bool { return !pointer.isParked() })
}

func TestControlReturnsToWhereItLeft(t *testing.T) {
	c, _, pointer := newRig(t)
	push(t, c)
	c.ForceRelease("test")
	waitFor(t, "a release", func() bool { return !pointer.isParked() })

	pointer.mu.Lock()
	defer pointer.mu.Unlock()
	if len(pointer.released) != 1 {
		t.Fatalf("released %d times, want 1", len(pointer.released))
	}
	// Two pixels inside the edge it left by, at the height it left at.
	if got := pointer.released[0]; got.X != 2 || got.Y < 600 || got.Y > 840 {
		t.Errorf("came back at %+v, want x=2 and y near 720", got)
	}
}

func TestTheParkingOrderIsParkEnterLeaveUnpark(t *testing.T) {
	c, rec, pointer := newRig(t)
	push(t, c)
	waitFor(t, "ENTER", func() bool { return len(rec.types()) > 0 })
	c.ForceRelease("test")
	waitFor(t, "a release", func() bool { return !pointer.isParked() })

	// Ignore the nudges that made the crossing possible; what matters is that
	// the pointer is pinned exactly once and let go exactly once, in that
	// order.
	var pins []string
	for _, event := range pointer.events() {
		if event == "park" || event == "release" {
			pins = append(pins, event)
		}
	}
	if !slices.Equal(pins, []string{"park", "release"}) {
		t.Errorf("pointer was pinned and released as %v, want [park release]", pins)
	}
}

func TestAnInnerDisplayEdgeStaysAnInnerEdge(t *testing.T) {
	pointer := &fakePointer{anchor: Point{X: 1280, Y: 720}}
	rec := &recorder{}
	config := DefaultConfig()
	config.Edge = EdgeLeft
	// Two monitors side by side. The left edge of the right-hand one is an
	// ordinary boundary between two PC displays, not the way to the Mac.
	c := New(Options{
		Config:        config,
		RemoteDesktop: macDesktop,
		Pointer:       pointer,
		Screens: func() (proto.ScreenInfo, error) {
			return proto.ScreenInfo{
				Virtual: proto.Monitor{Left: 0, Top: 0, Width: 5120, Height: 1440},
				Monitors: []proto.Monitor{
					{Left: 0, Top: 0, Width: 2560, Height: 1440, Primary: true},
					{Left: 2560, Top: 0, Width: 2560, Height: 1440},
				},
			}, nil
		},
	})
	t.Cleanup(c.Close)
	c.Attach(rec.send)

	c.MouseMoved(Point{X: 2580, Y: 720})
	c.MouseMoved(Point{X: 2570, Y: 720})
	for range 20 {
		if c.MouseMoved(Point{X: 2560, Y: 720}) {
			t.Fatal("pushing at an inner display boundary handed control to the Mac")
		}
	}
	if c.IsRemote() {
		t.Error("crossed over at a boundary between two PC displays")
	}
	pointer.mu.Lock()
	defer pointer.mu.Unlock()
	if len(pointer.warped) != 0 {
		t.Errorf("the pointer was moved at an inner boundary: %v", pointer.warped)
	}
}

// The pointer stops dead at the edge of the Windows desktop, so a shove that
// keeps going reports no movement at all. Without pulling the pointer clear,
// the push can never reach its threshold and a crossing is impossible.
func TestAShoveThatRunsOutOfScreenStillCrosses(t *testing.T) {
	c, _, pointer := newRig(t)

	// Approach the edge gently, so the movement that lands on it is nowhere
	// near the threshold on its own.
	for _, x := range []float64{20, 16, 12, 8, 4, 0} {
		c.MouseMoved(Point{X: x, Y: 720})
	}
	if c.IsRemote() {
		t.Fatal("a gentle approach crossed on its own")
	}
	// From here Windows reports nothing but repeats of the same pinned
	// position, which is exactly what a shove against the edge looks like.
	crossedAfter := -1
	for i := range 20 {
		c.MouseMoved(Point{X: 0, Y: 720})
		if c.IsRemote() {
			crossedAfter = i
			break
		}
	}
	if crossedAfter < 0 {
		t.Fatal("a sustained shove at the edge never crossed over")
	}

	pointer.mu.Lock()
	defer pointer.mu.Unlock()
	if len(pointer.warped) == 0 {
		t.Fatal("the pointer was never pulled clear of the edge")
	}
	// Pulled inward from the left edge, keeping its height.
	if got := pointer.warped[0]; got.X != edgeNudge || got.Y != 720 {
		t.Errorf("nudged to %+v, want {X:%d Y:720}", got, edgeNudge)
	}
}

// The counterpart: arriving at the edge and stopping is reaching for a
// scrollbar, and must leave the pointer exactly where the user put it.
func TestArrivingAtTheEdgeAndStoppingDoesNotMoveThePointer(t *testing.T) {
	c, _, pointer := newRig(t)

	for _, x := range []float64{20, 16, 12, 8, 4, 0} {
		c.MouseMoved(Point{X: x, Y: 720})
	}
	// Then movement along the edge, as a hand does travelling down a
	// scrollbar. Every event carries real movement, so none of them is a
	// shove that ran out of screen.
	for i := range 10 {
		if c.MouseMoved(Point{X: 0, Y: float64(720 + 4*(i+1))}) {
			t.Fatal("moving along the edge was swallowed")
		}
	}
	if c.IsRemote() {
		t.Error("travelling along the edge handed control to the Mac")
	}
	pointer.mu.Lock()
	defer pointer.mu.Unlock()
	if len(pointer.warped) != 0 {
		t.Errorf("the pointer was pulled off the edge while in use: %v", pointer.warped)
	}
}

// TestCaptureStaysOffUntilTheMacAsks covers the gate itself. Reverse control
// is configured on the Mac, and a PC that captured before being asked would
// swallow the user's own input on the strength of a default.
func TestCaptureStaysOffUntilTheMacAsks(t *testing.T) {
	pointer := &fakePointer{anchor: Point{X: 1280, Y: 720}}
	rec := &recorder{}
	config := DefaultConfig()
	config.Edge = EdgeLeft
	c := New(Options{
		Config:        config,
		RemoteDesktop: macDesktop,
		Pointer:       pointer,
		Screens: func() (proto.ScreenInfo, error) {
			return proto.ScreenInfo{
				Virtual:  proto.Monitor{Left: 0, Top: 0, Width: 2560, Height: 1440},
				Monitors: []proto.Monitor{{Left: 0, Top: 0, Width: 2560, Height: 1440, Primary: true}},
			}, nil
		},
	})
	t.Cleanup(c.Close)
	c.Attach(rec.send)

	// Shove at the edge as hard as the crossing test does.
	for i := range 40 {
		c.MouseMoved(Point{X: 0, Y: 400 + float64(i%3)})
	}
	if c.IsRemote() {
		t.Error("captured without the Mac asking for reverse control")
	}
	if len(rec.frames()) != 0 {
		t.Errorf("sent %d frames with reverse control off", len(rec.frames()))
	}

	c.SetEnabled(true)
	if !c.Enabled() {
		t.Fatal("SetEnabled(true) did not take")
	}
}

// TestSwallowingExpiresIfTheMacGoesQuiet is the safety net under the
// one-cursor behaviour.
//
// Swallowing this PC's pointer is right while the Mac is actually driving it,
// but it must never outlive the thing that justifies it. A Mac that stops
// sending without saying LEAVE would otherwise leave this PC with a mouse that
// does nothing at all — a far worse failure than the teleport that swallowing
// was introduced to prevent.
func TestSwallowingExpiresIfTheMacGoesQuiet(t *testing.T) {
	c, _, _ := newRig(t)
	c.Suspend(true)

	if !c.MouseMoved(Point{X: 900, Y: 400}) {
		t.Fatal("the PC's pointer should be swallowed while the Mac drives it")
	}

	// The Mac stops sending, and never says LEAVE.
	c.expireDrivenForTest()

	if c.MouseMoved(Point{X: 901, Y: 401}) {
		t.Error("the PC's mouse is still dead after the Mac went quiet")
	}
	if c.MouseButton(proto.ButtonLeft, true) {
		t.Error("clicks are still swallowed after the Mac went quiet")
	}

	// And a message from the Mac puts it back under the Mac's control.
	c.NoteDriven()
	if !c.MouseMoved(Point{X: 902, Y: 402}) {
		t.Error("input from the Mac should resume swallowing")
	}
}
