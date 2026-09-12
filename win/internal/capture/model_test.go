package capture

import (
	"math"
	"testing"
	"time"
)

// The mirror of mac/Tests/PorthmossCoreTests/CaptureModelTests.swift. The Mac
// sits beyond this PC's *left* edge, which is the same physical arrangement as
// the Swift suite's "Windows is beyond the Mac's right edge" — so every test
// here is that test with the horizontal sign flipped.
var (
	pcDisplay  = Rect{X: 0, Y: 0, W: 2560, H: 1440}
	macDesktop = Rect{X: 0, Y: 0, W: 1512, H: 982}
)

func makeModel(edge Edge) *Model {
	config := DefaultConfig()
	config.Edge = edge
	return NewModel(config, macDesktop, pcDisplay)
}

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(seconds float64) time.Time {
	return epoch.Add(time.Duration(seconds * float64(time.Second)))
}

// crossOver drives the model until control reaches the Mac, failing the test
// if it never does.
func crossOver(t *testing.T, m *Model) {
	t.Helper()
	for i := range 10 {
		// Hard against the left edge, vertically centred, pushing left.
		m.MouseMoved(Point{X: 0, Y: 720}, Point{X: -5}, at(float64(i)*0.02))
		if m.IsRemote() {
			return
		}
	}
	t.Fatal("setup: failed to cross over")
}

func TestBrushingTheEdgeDoesNotHandOverControl(t *testing.T) {
	m := makeModel(EdgeLeft)
	// Reaching for a scrollbar: at the edge, but nowhere near the push
	// threshold. This is the failure mode that makes naive edge detection
	// unusable, so it is the first thing worth pinning down.
	for i := range 5 {
		got := m.MouseMoved(Point{X: 0, Y: 400}, Point{X: -2}, at(float64(i)*0.02))
		if got.Kind != ActionNone {
			t.Fatalf("step %d: brushing the edge produced %v", i, got.Kind)
		}
	}
	if m.IsRemote() {
		t.Error("control crossed over on a brush")
	}
}

func TestSustainedPushCrossesAtTheMatchingHeight(t *testing.T) {
	m := makeModel(EdgeLeft)
	var crossed Action
	for i := range 10 {
		got := m.MouseMoved(Point{X: 0, Y: 720}, Point{X: -5}, at(float64(i)*0.02))
		if got.Kind == ActionEnterRemote {
			crossed = got
			break
		}
	}
	if crossed.Kind != ActionEnterRemote {
		t.Fatalf("never crossed; last action was %v", crossed.Kind)
	}
	if !m.IsRemote() {
		t.Error("the model should be remote after entering")
	}
	// Crossing the PC's left edge means arriving at the Mac's right edge.
	if crossed.X != 65535 {
		t.Errorf("entry x = %d, want 65535 (the Mac's right edge)", crossed.X)
	}
	// Halfway down the PC display should be halfway down the Mac's.
	if math.Abs(float64(crossed.Y)-32767) > 400 {
		t.Errorf("entry y = %d, want roughly 32767", crossed.Y)
	}
}

func TestPushDecaysSoASlowDriftNeverCrosses(t *testing.T) {
	m := makeModel(EdgeLeft)
	edge := Point{X: 0, Y: 400}
	m.MouseMoved(edge, Point{X: -10}, at(0))
	// A whole second later: well past the 400 ms decay window, so this is a
	// fresh push of 10 rather than an accumulated 20.
	if got := m.MouseMoved(edge, Point{X: -10}, at(1.0)); got.Kind != ActionNone {
		t.Errorf("a drift across the decay window produced %v", got.Kind)
	}
	if m.IsRemote() {
		t.Error("a slow drift handed over control")
	}
}

func TestTheRemoteCursorStaysInsideTheMacDesktop(t *testing.T) {
	m := makeModel(EdgeLeft)
	crossOver(t, m)
	for range 200 {
		m.MouseMoved(Point{}, Point{X: -50, Y: 50}, at(1.0))
	}
	// Deeper into the Mac is leftward and downward from the right-hand entry
	// edge, and both must stop at the desktop's last pixel.
	if got := m.RemoteCursor(); got.X != 0 || got.Y != 981 {
		t.Errorf("remote cursor = %+v, want {X:0 Y:981}", got)
	}
}

func TestPushingBackAtTheFarEdgeReturnsControl(t *testing.T) {
	m := makeModel(EdgeLeft)
	crossOver(t, m)
	// Use the Mac: move well clear of the entry edge.
	m.MouseMoved(Point{}, Point{X: -500}, at(1.0))

	// Walk back towards the PC. Control returns partway through this loop,
	// once the cursor has been pinned against the Mac's right edge long enough
	// to clear the push threshold.
	var returned Action
	for i := range 200 {
		got := m.MouseMoved(Point{}, Point{X: 10}, at(1.0+float64(i)*0.02))
		if got.Kind == ActionReturnToLocal {
			returned = got
			break
		}
	}
	if returned.Kind != ActionReturnToLocal {
		t.Fatal("control never came back")
	}
	if m.IsRemote() {
		t.Error("the model should be local again after returning")
	}
	// Two pixels inside the edge, so the next movement towards it is a fresh
	// push rather than an instant re-crossing.
	if math.Abs(returned.Local.X-(pcDisplay.X+2)) > 0.001 {
		t.Errorf("returned to x = %v, want %v", returned.Local.X, pcDisplay.X+2)
	}
}

func TestMidScreenMovementIsMovementNotARequestToComeBack(t *testing.T) {
	m := makeModel(EdgeLeft)
	crossOver(t, m)
	m.MouseMoved(Point{}, Point{X: -1200}, at(1.0))
	for i := range 20 {
		got := m.MouseMoved(Point{}, Point{X: 5}, at(1.0+float64(i)*0.02))
		if got.Kind == ActionReturnToLocal {
			t.Fatalf("returned while mid-screen, at step %d", i)
		}
	}
	if !m.IsRemote() {
		t.Error("control left the Mac without a push-back")
	}
}

func TestThePanicReleaseAlwaysHandsControlBackAndIsIdempotent(t *testing.T) {
	m := makeModel(EdgeLeft)
	crossOver(t, m)

	action, released := m.ForceReturn()
	if !released || action.Kind != ActionReturnToLocal {
		t.Fatalf("panic release gave (%v, %t), want a ReturnToLocal", action.Kind, released)
	}
	if m.IsRemote() {
		t.Error("still remote after a panic release")
	}
	if _, released := m.ForceReturn(); released {
		t.Error("a second panic release should be a no-op")
	}
}

func TestCrossingTheRightEdgeArrivesOnTheMacsLeft(t *testing.T) {
	m := makeModel(EdgeRight)
	var crossed Action
	for i := range 10 {
		got := m.MouseMoved(Point{X: pcDisplay.MaxX(), Y: 720}, Point{X: 5}, at(float64(i)*0.02))
		if got.Kind == ActionEnterRemote {
			crossed = got
			break
		}
	}
	if crossed.Kind != ActionEnterRemote {
		t.Fatal("never crossed the right edge")
	}
	if crossed.X != 0 {
		t.Errorf("entry x = %d, want 0 (the Mac's left edge)", crossed.X)
	}
}

// The bug that made the PC cursor appear to move while driving the Mac: on
// arrival the cursor sits against the Mac's entry edge, so the return gesture
// was already satisfied. A small jiggle bounced control straight back, and the
// rapid flapping re-warped the cursor each time.
func TestAJiggleRightAfterCrossingDoesNotBounceControlBack(t *testing.T) {
	m := makeModel(EdgeLeft)
	crossOver(t, m)

	// Exactly what a hand does settling after a push: small movements in both
	// directions, still hard against the entry edge.
	for i := range 40 {
		dx := 6.0
		if i%2 != 0 {
			dx = -4
		}
		got := m.MouseMoved(Point{}, Point{X: dx, Y: 1}, at(1.0+float64(i)*0.016))
		if got.Kind == ActionReturnToLocal {
			t.Fatalf("control bounced back on a jiggle at step %d", i)
		}
	}
	if !m.IsRemote() {
		t.Error("control left the Mac during a jiggle")
	}
}

func TestMovingTowardsThePCOnArrivalDoesNotBounceControlBack(t *testing.T) {
	m := makeModel(EdgeLeft)
	crossOver(t, m)
	// You land pinned against the Mac's right edge. Moving right from there is
	// the most natural thing in the world and must not eject you.
	for i := range 30 {
		got := m.MouseMoved(Point{}, Point{X: 8}, at(1.0+float64(i)*0.016))
		if got.Kind == ActionReturnToLocal {
			t.Fatalf("control bounced back after %d movements on arrival", i)
		}
	}
	if !m.IsRemote() {
		t.Error("control left the Mac on arrival")
	}
}

// The escape hatch must still work — but only after actually using the Mac.
func TestReturningStillWorksOnceTheCursorHasMovedIntoTheMac(t *testing.T) {
	m := makeModel(EdgeLeft)
	crossOver(t, m)
	m.MouseMoved(Point{}, Point{X: -600, Y: 200}, at(1.0))

	for i := range 300 {
		if got := m.MouseMoved(Point{}, Point{X: 10}, at(2.0+float64(i)*0.016)); got.Kind == ActionReturnToLocal {
			if m.IsRemote() {
				t.Error("returned but still marked remote")
			}
			return
		}
	}
	t.Error("deliberately walking back to the edge must still return control")
}

func TestParseEdgeRejectsAnythingElse(t *testing.T) {
	for _, name := range []string{"left", "right", "top", "bottom"} {
		if _, err := ParseEdge(name); err != nil {
			t.Errorf("ParseEdge(%q): %v", name, err)
		}
	}
	if _, err := ParseEdge("sideways"); err == nil {
		t.Error("ParseEdge should reject an unknown edge")
	}
}
