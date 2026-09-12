// Package capture makes this PC's own mouse and keyboard drive the Mac. It is
// the mirror of the Mac's event tap and edge-crossing model, and it sends the
// same messages the Mac sends, in the other direction, over the same
// connection.
//
// The state machine in this file is a direct port of
// mac/Sources/PorthmossCore/CaptureModel.swift, including the reasons for each
// rule. It is deliberately free of Win32 so the crossing rules — which are
// most of what makes this feel good or awful — can be tested on any host,
// including the Mac this project is developed on.
package capture

import (
	"fmt"
	"math"
	"time"
)

// Point is a position or a movement. Which pixel space it is in depends on the
// field holding it: local points are Windows virtual-desktop pixels, remote
// points are the Mac's.
type Point struct{ X, Y float64 }

// Rect is a display or a whole desktop.
type Rect struct{ X, Y, W, H float64 }

// MaxX and MaxY are the exclusive far edges: a 2560-wide desktop at X=0 has
// MaxX 2560 and a rightmost pixel of 2559.
func (r Rect) MaxX() float64 { return r.X + r.W }
func (r Rect) MaxY() float64 { return r.Y + r.H }

// Edge is the edge of *this PC's* desktop that the Mac sits beyond.
//
// It is the opposite of the Mac's own setting. If the Mac stands to the left
// of the PC, the Mac has the PC beyond its right edge and the PC has the Mac
// beyond its left, so the Mac is configured `right` and the agent `left`.
type Edge string

const (
	EdgeLeft   Edge = "left"
	EdgeRight  Edge = "right"
	EdgeTop    Edge = "top"
	EdgeBottom Edge = "bottom"
)

// ParseEdge turns a command-line value into an Edge.
func ParseEdge(s string) (Edge, error) {
	switch Edge(s) {
	case EdgeLeft, EdgeRight, EdgeTop, EdgeBottom:
		return Edge(s), nil
	default:
		return "", fmt.Errorf("capture: unknown edge %q, want left, right, top or bottom", s)
	}
}

// Config holds the crossing rules. The defaults are the Mac's defaults: two
// machines that disagree about how hard a push has to be would feel like two
// different products.
type Config struct {
	// Edge is the edge the Mac sits beyond.
	Edge Edge

	// PushThreshold is how hard the user must push past the edge before
	// control crosses over, in pixels. Without this, reaching for a scrollbar
	// or a window's close button would fling the cursor onto the other
	// machine.
	PushThreshold float64

	// PushWindow is how long accumulated push survives a pause, so a slow
	// drift along the edge never adds up to a crossing.
	PushWindow time.Duration

	// Sensitivity scales PC mouse movement into Mac pixels.
	Sensitivity float64

	// ReturnArmDistance is how far into the Mac's desktop the cursor must
	// travel before pushing back can hand control home again.
	//
	// You arrive pinned against the entry edge, which means the return gesture
	// is already satisfied the moment you land — a couple of movements back
	// would eject you straight away, and the resulting flapping looks like the
	// PC cursor moving on its own. Control only becomes returnable once you
	// have actually gone somewhere.
	ReturnArmDistance float64
}

// DefaultConfig matches CaptureConfig on the Mac.
func DefaultConfig() Config {
	return Config{
		Edge:              EdgeLeft,
		PushThreshold:     14,
		PushWindow:        400 * time.Millisecond,
		Sensitivity:       1,
		ReturnArmDistance: 64,
	}
}

// ActionKind is what the model decided one movement means.
type ActionKind int

const (
	// ActionNone means the movement belongs to this PC.
	ActionNone ActionKind = iota
	// ActionEnterRemote means control crosses to the Mac: park this PC's
	// pointer and tell the Mac where to appear.
	ActionEnterRemote
	// ActionMoveRemote is ordinary movement while the Mac has control.
	ActionMoveRemote
	// ActionReturnToLocal hands control back, putting this PC's cursor at
	// Local.
	ActionReturnToLocal
)

// Action is one decision. X and Y are normalised 0..65535 over the Mac's
// desktop and are only meaningful for ActionEnterRemote and ActionMoveRemote;
// Local is only meaningful for ActionReturnToLocal.
type Action struct {
	Kind  ActionKind
	X, Y  uint16
	Local Point
}

// Model is the edge-crossing state machine.
//
// It is not safe for concurrent use. The Windows hook procedure and everything
// that can force a release are serialised by the Controller that owns it.
type Model struct {
	// Config may be replaced between events; the caller owns when.
	Config Config

	// Remote is the Mac's whole desktop, in the Mac's own pixels.
	//
	// The agent is never told this: READY carries ScreenInfo the other way,
	// and the Mac sends none of its own. Only the ratio between the two
	// matters — the Mac maps the normalised 0..65535 position onto whatever
	// desktop it actually has — so a plausible rectangle and the Sensitivity
	// knob are enough, and adding a message for it would need a change on the
	// Mac.
	Remote Rect

	// Local is the PC display the crossing happens on, in virtual-desktop
	// pixels. The Controller keeps it pointed at the display under the cursor.
	Local Rect

	isRemote     bool
	remoteCursor Point

	push       float64
	lastPushAt time.Time
	// returnArmed is false from the moment control crosses over until the
	// cursor has moved ReturnArmDistance clear of the entry edge.
	returnArmed bool
	// exitFraction is where on the edge we left, as a 0..1 fraction, so
	// control comes back to the same place instead of jumping to a corner.
	exitFraction float64
}

// NewModel returns a model that has not yet handed control over.
func NewModel(config Config, remote, local Rect) *Model {
	return &Model{Config: config, Remote: remote, Local: local, exitFraction: 0.5}
}

// IsRemote reports whether the Mac currently has control.
func (m *Model) IsRemote() bool { return m.isRemote }

// Push is how much outward movement has accumulated at the edge so far, in
// pixels. Windows needs it: the pointer stops dead at the edge of the desktop,
// so the platform layer has to know a shove is under way before it can make
// room for the next one. See Controller.MouseMoved.
func (m *Model) Push() float64 { return m.push }

// IsAtEdge reports whether this position is against the edge the Mac lies
// beyond.
func (m *Model) IsAtEdge(cursor Point) bool { return m.isAtEdge(cursor) }

// RemoteCursor is where the cursor sits on the Mac's desktop, in its pixels.
func (m *Model) RemoteCursor() Point { return m.remoteCursor }

// MouseMoved feeds one movement in. cursor is this PC's cursor position, which
// is ignored while the Mac has control and the pointer is parked.
func (m *Model) MouseMoved(cursor, delta Point, now time.Time) Action {
	if m.isRemote {
		return m.moveWhileRemote(delta, now)
	}
	return m.maybeCross(cursor, delta, now)
}

// ForceReturn hands control back unconditionally — the panic hotkey, a dropped
// connection, a changed setting. It returns false when the Mac did not have
// control, so callers can stay idempotent.
func (m *Model) ForceReturn() (Action, bool) {
	if !m.isRemote {
		return Action{}, false
	}
	m.isRemote = false
	m.push = 0
	m.returnArmed = false
	return Action{Kind: ActionReturnToLocal, Local: m.localPointOnEdge(m.exitFraction)}, true
}

// --- Local side ---

func (m *Model) maybeCross(cursor, delta Point, now time.Time) Action {
	outward := m.outwardComponent(delta)
	if outward <= 0 || !m.isAtEdge(cursor) {
		m.push = 0
		return Action{}
	}
	if now.Sub(m.lastPushAt) > m.Config.PushWindow {
		m.push = 0
	}
	m.push += outward
	m.lastPushAt = now
	if m.push < m.Config.PushThreshold {
		return Action{}
	}

	m.push = 0
	m.isRemote = true
	m.returnArmed = false
	m.exitFraction = m.edgeFraction(cursor)
	m.remoteCursor = m.remoteEntryPoint(m.exitFraction)
	x, y := m.normalized(m.remoteCursor)
	return Action{Kind: ActionEnterRemote, X: x, Y: y}
}

func (m *Model) isAtEdge(cursor Point) bool {
	const slack = 1.0
	switch m.Config.Edge {
	case EdgeRight:
		return cursor.X >= m.Local.MaxX()-slack
	case EdgeLeft:
		return cursor.X <= m.Local.X+slack
	case EdgeBottom:
		return cursor.Y >= m.Local.MaxY()-slack
	default: // EdgeTop
		return cursor.Y <= m.Local.Y+slack
	}
}

// outwardComponent is how much of this movement pushes *through* the
// configured edge.
func (m *Model) outwardComponent(delta Point) float64 {
	switch m.Config.Edge {
	case EdgeRight:
		return delta.X
	case EdgeLeft:
		return -delta.X
	case EdgeBottom:
		return delta.Y
	default: // EdgeTop
		return -delta.Y
	}
}

func (m *Model) edgeFraction(cursor Point) float64 {
	switch m.Config.Edge {
	case EdgeLeft, EdgeRight:
		return clamp((cursor.Y-m.Local.Y)/math.Max(m.Local.H, 1), 0, 1)
	default:
		return clamp((cursor.X-m.Local.X)/math.Max(m.Local.W, 1), 0, 1)
	}
}

func (m *Model) localPointOnEdge(fraction float64) Point {
	// Come back one pixel inside the edge, so the very next movement towards
	// it is a fresh push rather than an instant re-crossing.
	const inset = 2.0
	switch m.Config.Edge {
	case EdgeRight:
		return Point{X: m.Local.MaxX() - inset, Y: m.Local.Y + fraction*m.Local.H}
	case EdgeLeft:
		return Point{X: m.Local.X + inset, Y: m.Local.Y + fraction*m.Local.H}
	case EdgeBottom:
		return Point{X: m.Local.X + fraction*m.Local.W, Y: m.Local.MaxY() - inset}
	default: // EdgeTop
		return Point{X: m.Local.X + fraction*m.Local.W, Y: m.Local.Y + inset}
	}
}

// --- Remote side ---

func (m *Model) moveWhileRemote(delta Point, now time.Time) Action {
	scaled := Point{X: delta.X * m.Config.Sensitivity, Y: delta.Y * m.Config.Sensitivity}

	if !m.returnArmed && m.distanceFromEntryEdge() >= m.Config.ReturnArmDistance {
		m.returnArmed = true
	}

	// Movement back towards the PC only counts once the cursor is already
	// pinned against the entry edge of the Mac's desktop — and only once it
	// has been somewhere else first.
	pinned := m.returnArmed && m.isPinnedToEntryEdge()
	inward := -m.outwardComponent(scaled)
	if pinned && inward > 0 {
		if now.Sub(m.lastPushAt) > m.Config.PushWindow {
			m.push = 0
		}
		m.push += inward
		m.lastPushAt = now
		if m.push >= m.Config.PushThreshold {
			m.push = 0
			m.isRemote = false
			m.exitFraction = m.remoteEdgeFraction()
			return Action{Kind: ActionReturnToLocal, Local: m.localPointOnEdge(m.exitFraction)}
		}
	} else if inward <= 0 {
		m.push = 0
	}

	m.remoteCursor.X = clamp(m.remoteCursor.X+scaled.X, m.Remote.X, m.Remote.MaxX()-1)
	m.remoteCursor.Y = clamp(m.remoteCursor.Y+scaled.Y, m.Remote.Y, m.Remote.MaxY()-1)
	x, y := m.normalized(m.remoteCursor)
	return Action{Kind: ActionMoveRemote, X: x, Y: y}
}

// isPinnedToEntryEdge reports whether the cursor is hard against the Mac
// desktop edge it arrived through — crossing the PC's left edge means entering
// the Mac from its right.
func (m *Model) isPinnedToEntryEdge() bool {
	const slack = 1.0
	switch m.Config.Edge {
	case EdgeRight:
		return m.remoteCursor.X <= m.Remote.X+slack
	case EdgeLeft:
		return m.remoteCursor.X >= m.Remote.MaxX()-1-slack
	case EdgeBottom:
		return m.remoteCursor.Y <= m.Remote.Y+slack
	default: // EdgeTop
		return m.remoteCursor.Y >= m.Remote.MaxY()-1-slack
	}
}

// distanceFromEntryEdge is how far the cursor has travelled into the Mac's
// desktop, measured from the edge it arrived through.
func (m *Model) distanceFromEntryEdge() float64 {
	switch m.Config.Edge {
	case EdgeRight:
		return m.remoteCursor.X - m.Remote.X
	case EdgeLeft:
		return m.Remote.MaxX() - 1 - m.remoteCursor.X
	case EdgeBottom:
		return m.remoteCursor.Y - m.Remote.Y
	default: // EdgeTop
		return m.Remote.MaxY() - 1 - m.remoteCursor.Y
	}
}

func (m *Model) remoteEntryPoint(fraction float64) Point {
	switch m.Config.Edge {
	case EdgeRight:
		return Point{X: m.Remote.X, Y: m.Remote.Y + fraction*m.Remote.H}
	case EdgeLeft:
		return Point{X: m.Remote.MaxX() - 1, Y: m.Remote.Y + fraction*m.Remote.H}
	case EdgeBottom:
		return Point{X: m.Remote.X + fraction*m.Remote.W, Y: m.Remote.Y}
	default: // EdgeTop
		return Point{X: m.Remote.X + fraction*m.Remote.W, Y: m.Remote.MaxY() - 1}
	}
}

func (m *Model) remoteEdgeFraction() float64 {
	switch m.Config.Edge {
	case EdgeLeft, EdgeRight:
		return clamp((m.remoteCursor.Y-m.Remote.Y)/math.Max(m.Remote.H, 1), 0, 1)
	default:
		return clamp((m.remoteCursor.X-m.Remote.X)/math.Max(m.Remote.W, 1), 0, 1)
	}
}

// normalized converts Mac desktop pixels to the 0..65535 space the wire uses.
func (m *Model) normalized(p Point) (uint16, uint16) {
	nx := 65535 * (p.X - m.Remote.X) / math.Max(m.Remote.W-1, 1)
	ny := 65535 * (p.Y - m.Remote.Y) / math.Max(m.Remote.H-1, 1)
	return uint16(clamp(math.Round(nx), 0, 65535)), uint16(clamp(math.Round(ny), 0, 65535))
}

func clamp(value, low, high float64) float64 {
	return math.Min(math.Max(value, low), high)
}
