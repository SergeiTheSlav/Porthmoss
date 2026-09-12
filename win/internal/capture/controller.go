package capture

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/janjamscikov/porthmoss/win/internal/proto"
)

// Sender writes one message to the connected Mac. The server hands one out per
// session; it serialises with the session's own writes and is safe to call
// from any goroutine.
type Sender func(typ byte, body []byte) error

// Pointer moves and pins this PC's physical pointer.
//
// Swallowing a mouse event is not enough to hold the pointer still — the same
// trap the Mac hit, where the window server moved the cursor from the HID
// stream whatever the tap did. The pointer is therefore warped back to a fixed
// anchor after every movement, and movement is measured from that anchor
// rather than from wherever the pointer ended up.
type Pointer interface {
	// Park pins the pointer inside `within` — the display the crossing
	// happened on — and returns the anchor every later movement is measured
	// against. Called once per crossing.
	Park(within Rect) (Point, error)
	// Warp puts the pointer at p. It is called from the hook thread after
	// every captured movement, so it must not block, and whatever input it
	// generates must be recognisable as injected or the two will feed back.
	Warp(p Point)
	// Release unpins the pointer and puts it at `to`.
	Release(to Point) error
}

// Status is what the user should be told about capture.
type Status struct {
	// Remote is true while this PC's input is driving the Mac.
	Remote bool
	// Detail is a sentence for the window.
	Detail string
}

// Options configures a Controller.
type Options struct {
	Config Config

	// Screens reports this PC's display layout. It is re-read when the cursor
	// leaves the display the model is aimed at, so a monitor plugged in
	// mid-session is picked up without polling.
	Screens func() (proto.ScreenInfo, error)

	// RemoteDesktop is the Mac's desktop in the Mac's own pixels. A zero value
	// means "assume it is the same size as this PC's virtual desktop".
	//
	// The Mac never tells the agent its geometry: READY carries ScreenInfo the
	// other way and the Mac sends none of its own. Only the ratio matters —
	// the Mac maps the normalised 0..65535 position onto whatever desktop it
	// really has — so a plausible rectangle plus Config.Sensitivity is enough,
	// and inventing a message for it would need a change on the Mac.
	RemoteDesktop Rect

	// Pointer parks the physical pointer. A nil Pointer runs the model with
	// nothing pinning the cursor, which is only useful in tests.
	Pointer Pointer

	Log *slog.Logger

	// OnStatus reports control moving between the two machines.
	OnStatus func(Status)
}

// drivenGrace is how long this PC keeps its own pointer swallowed after the
// last message from the Mac. Long enough to cover an idle moment mid-session,
// short enough that anything going wrong restores the mouse almost at once.
const drivenGrace = 2 * time.Second

const (
	// queueDepth is how many messages may be waiting on the socket before the
	// hook starts dropping them. Movement is a few bytes and the link is a
	// LAN, so this is far past anything a healthy session reaches; it exists
	// so a wedged socket cannot back up into the hook procedure.
	queueDepth = 1024

	// edgeNudge is how far inside the edge the pointer is pulled when a shove
	// has run out of screen, in pixels.
	//
	// This is the one place the ported rules need help. Windows stops the
	// pointer dead at the edge of the virtual desktop, so a user who keeps
	// shoving produces events that report no movement at all and the push
	// never reaches its threshold. macOS does not have the problem: the Mac
	// decouples the mouse from the cursor and real deltas keep arriving.
	//
	// Small enough to be invisible over the two or three frames a crossing
	// takes, large enough that the shove after it is not whittled down to
	// nothing.
	edgeNudge = 6
)

type message struct {
	typ  byte
	body []byte
}

// notice is something to tell the user or the log about. It goes through the
// writer goroutine for the same reason input does: writing a log line or
// calling into the UI from the hook procedure is I/O, and a low-level hook
// that blocks on I/O is a low-level hook Windows removes.
type notice struct {
	status Status
	log    string
}

// Controller turns captured input into Porthmoss messages.
//
// Every entry point is called from the low-level hook procedure and must be
// quick: Windows silently removes a hook whose callback is slow, exactly as
// macOS disables a slow CGEventTap. Nothing here does I/O — decisions are pure
// arithmetic, and the messages they produce go onto a channel for another
// goroutine to write to the socket.
type Controller struct {
	log     *slog.Logger
	pointer Pointer

	// now is overridable so the crossing rules can be driven at a known clock.
	now func() time.Time

	mu        sync.Mutex
	opts      Options
	model     *Model
	send      Sender
	suspended bool
	// drivenUntil bounds how long this PC's own pointer stays swallowed.
	//
	// Swallowing is right while the Mac is actually driving — one pointer, one
	// source — but it must not be able to outlive the thing that justifies it.
	// If the Mac stops sending without saying LEAVE, an unbounded suspension
	// leaves this PC with a mouse that does nothing at all, which is a far
	// worse failure than the teleport it was introduced to prevent. Each
	// message from the Mac extends this; silence expires it.
	drivenUntil time.Time

	// enabled is the Mac's say-so. Reverse control is configured from there,
	// and stays off until the Mac asks for it — an older Mac that cannot be
	// driven never asks, and a user who has not turned it on has not asked
	// either.
	enabled bool
	anchor  Point
	// last is the previous pointer position, because a low-level mouse hook
	// reports where the pointer is and never how far it moved.
	last Point
	// hasLast guards the first movement of a capture: differencing against a
	// position from minutes ago would look like one enormous push and could
	// cross the edge on its own.
	hasLast bool
	// aimed is the display the cursor was last resolved to, and aimedOK
	// whether a crossing is possible from it. See aimAtDisplay.
	aimed   Rect
	aimedOK bool
	// held tracks which modifier scancodes are down, for the panic hotkey. It
	// is fed by every key the hook sees, captured or not, so the hotkey works
	// whatever state the modifiers were in when control crossed.
	held map[uint16]bool

	out     chan message
	notices chan notice
	urgent  chan struct{}
	done    chan struct{}
	closed  sync.Once

	// dropped counts movements discarded because the socket was behind. Each
	// one is a frame of smoothness lost, so it is worth surfacing rather than
	// hiding.
	dropped atomic.Uint64
}

// New returns a Controller with no session attached. Nothing is captured until
// Attach is called.
func New(opts Options) *Controller {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Config.Edge == "" {
		opts.Config = DefaultConfig()
	}
	c := &Controller{
		log:     opts.Log,
		pointer: opts.Pointer,
		now:     time.Now,
		opts:    opts,
		model:   NewModel(opts.Config, opts.RemoteDesktop, Rect{}),
		held:    make(map[uint16]bool),
		out:     make(chan message, queueDepth),
		notices: make(chan notice, 16),
		urgent:  make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go c.pump()
	return c
}

// Attach binds the controller to a live session. Input is only captured while
// a Mac is connected: there is nowhere for it to go otherwise, and swallowing
// the user's mouse with no link to send it down would be the worst possible
// failure.
func (c *Controller) Attach(send Sender) {
	c.mu.Lock()
	c.send = send
	c.hasLast = false
	// A monitor may have come or gone since the last session.
	c.forgetDisplays()
	c.mu.Unlock()
	c.log.Debug("capture attached to a session")
}

// Detach ends the session, handing control back to this PC first if the Mac
// had it. Losing the link must never leave the user with a parked pointer.
func (c *Controller) Detach() {
	c.ForceRelease("the Mac disconnected")
	c.mu.Lock()
	c.send = nil
	c.hasLast = false
	c.mu.Unlock()
	// Anything still queued belongs to a session that no longer exists.
	c.drain()
	// Every dropped movement was a frame in which the Mac's cursor stuttered.
	// Silence here would make a slow link look like a bug in the crossing.
	if dropped := c.dropped.Swap(0); dropped > 0 {
		c.log.Warn("the link could not keep up with this PC's mouse", "movements_dropped", dropped)
	}
}

// While suspended, this PC's own *pointer* input is swallowed but its keyboard
// is not, and the asymmetry is deliberate.
//
// A pointer has one position. Two sources moving it means the hand moves it,
// the Mac's next absolute position snaps it back, and the cursor visibly
// teleports — the behaviour this suspension exists to stop. Keystrokes have no
// such conflict: two keyboards typing into one machine interleave, which is
// survivable and occasionally what someone wants.
//
// It also matters when things go wrong. If the Mac wedges mid-session, a PC
// whose keyboard still works can be used; one that has had everything taken
// away cannot, and its escape hotkey lives on the machine that stopped
// responding.
//
// Suspend stops this PC capturing its own input while the Mac is driving it.
// Without it the two machines would fight over one pointer: the Mac's injected
// movement is flagged and ignored, but the user's own hand on the PC mouse is
// not, and it would start a crossing nobody asked for.
func (c *Controller) Suspend(suspended bool) {
	c.mu.Lock()
	if c.suspended == suspended {
		c.mu.Unlock()
		return
	}
	c.suspended = suspended
	c.hasLast = false
	if suspended {
		c.drivenUntil = time.Now().Add(drivenGrace)
	} else {
		c.drivenUntil = time.Time{}
	}
	c.mu.Unlock()
	if suspended {
		c.ForceRelease("the Mac took control of this PC")
	}
}

// SetEnabled turns capture on or off wholesale, at the Mac's request.
func (c *Controller) SetEnabled(on bool) {
	c.mu.Lock()
	if c.enabled == on {
		c.mu.Unlock()
		return
	}
	c.enabled = on
	c.hasLast = false
	c.mu.Unlock()
	if !on {
		c.ForceRelease("the Mac turned reverse control off")
	}
}

// Config returns the crossing rules in force.
func (c *Controller) Config() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.model.Config
}

// Enabled reports whether the Mac has asked for reverse control.
func (c *Controller) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// SetRemote replaces what this PC believes the Mac's desktop to be.
//
// It only affects where the cursor lands over there, not whether a crossing
// happens: positions travel normalised. But guessing it wrong makes the Mac's
// pointer move at the wrong speed and stop short of its own edges, which feels
// broken long before anyone suspects the geometry.
func (c *Controller) SetRemote(remote Rect) {
	c.mu.Lock()
	c.model.Remote = remote
	c.mu.Unlock()
}

// SetConfig replaces the crossing rules. Control comes home first when the
// edge changes, since doing it mid-crossing would leave the pointer with no
// way back that matches what the user just chose.
func (c *Controller) SetConfig(config Config) {
	c.mu.Lock()
	changedEdge := config.Edge != c.model.Config.Edge
	c.mu.Unlock()
	if changedEdge {
		c.ForceRelease("the edge setting changed")
	}
	c.mu.Lock()
	c.opts.Config = config
	c.model.Config = config
	// Which display can cross depends on the edge, so the cached verdict is
	// worthless now.
	c.forgetDisplays()
	c.mu.Unlock()
}

// IsRemote reports whether this PC's input is currently driving the Mac.
func (c *Controller) IsRemote() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.model.IsRemote()
}

// Close hands control back and stops the writer goroutine.
func (c *Controller) Close() {
	c.ForceRelease("shutting down")
	c.closed.Do(func() { close(c.done) })
}

// --- Hook entry points. All return true when the event must be swallowed. ---

// MouseMoved feeds one movement in. `position` is where the pointer now is, in
// virtual-desktop pixels.
func (c *Controller) MouseMoved(position Point) bool {
	c.mu.Lock()
	if c.beingDrivenLocked() {
		// The Mac is driving this PC, so its injected positions are the only
		// thing that should move the pointer. Passing the user's own mouse
		// through as well gives two sources for one pointer: the hand moves
		// it, the next absolute position from the Mac snaps it back, and the
		// cursor appears to teleport. One cursor, one source of truth.
		c.hasLast = false
		c.mu.Unlock()
		return true
	}
	if !c.active() {
		c.hasLast = false
		c.mu.Unlock()
		return false
	}

	if c.model.IsRemote() {
		return c.moveWhileRemoteLocked(position)
	}
	return c.moveWhileLocalLocked(position)
}

// moveWhileLocalLocked handles movement that still belongs to this PC. It is
// called with the mutex held and releases it.
func (c *Controller) moveWhileLocalLocked(position Point) bool {
	known, previous := c.hasLast, c.last
	c.last, c.hasLast = position, true
	if !known || !c.aimAtDisplay(position) {
		c.mu.Unlock()
		return false
	}
	delta := Point{X: position.X - previous.X, Y: position.Y - previous.Y}

	// A movement event that reports no movement, with the pointer against the
	// edge and a push already under way, is a shove that has run out of
	// screen: the mouse is plainly still moving or there would be no event at
	// all. Make room for the next one rather than letting the model see a zero
	// delta and forget the push. See edgeNudge.
	if delta == (Point{}) && c.model.Push() > 0 && c.model.IsAtEdge(position) {
		if target, ok := c.nudgeTargetLocked(position); ok {
			c.last = target
			c.mu.Unlock()
			c.pointer.Warp(target)
			return false
		}
		c.mu.Unlock()
		return false
	}

	swallow, follow := c.applyLocked(c.model.MouseMoved(position, delta, c.now()))
	c.mu.Unlock()
	follow()
	return swallow
}

// moveWhileRemoteLocked handles movement while the Mac has control. It is
// called with the mutex held and releases it.
func (c *Controller) moveWhileRemoteLocked(position Point) bool {
	// The pointer is warped back to the anchor after every movement, so the
	// distance from the anchor *is* the movement. Reading it any other way
	// would give zero the moment the pointer stopped being allowed to travel.
	delta := Point{X: position.X - c.anchor.X, Y: position.Y - c.anchor.Y}
	if delta.X == 0 && delta.Y == 0 {
		c.mu.Unlock()
		return true
	}
	action := c.model.MouseMoved(position, delta, c.now())
	swallow, follow := c.applyLocked(action)
	c.mu.Unlock()
	follow()
	return swallow
}

// nudgeTargetLocked returns where to pull the pointer so the next shove has
// room, and whether there is a pointer to pull. Callers must hold the mutex.
func (c *Controller) nudgeTargetLocked(position Point) (Point, bool) {
	if c.pointer == nil {
		return Point{}, false
	}
	display := c.model.Local
	switch c.model.Config.Edge {
	case EdgeRight:
		return Point{X: display.MaxX() - 1 - edgeNudge, Y: position.Y}, true
	case EdgeLeft:
		return Point{X: display.X + edgeNudge, Y: position.Y}, true
	case EdgeBottom:
		return Point{X: position.X, Y: display.MaxY() - 1 - edgeNudge}, true
	default: // EdgeTop
		return Point{X: position.X, Y: display.Y + edgeNudge}, true
	}
}

// MouseButton forwards a press or release. Buttons only matter while the Mac
// has control; otherwise they belong to whatever is on this PC.
func (c *Controller) MouseButton(button byte, down bool) bool {
	c.mu.Lock()
	driven := c.beingDrivenLocked()
	c.mu.Unlock()
	if driven {
		return true
	}

	if !c.remote() {
		return false
	}
	c.emit(proto.TypeMouseButton, proto.MouseButton{Button: button, Down: down}.Encode())
	return true
}

// MouseWheel forwards a scroll, in whole wheel notches.
func (c *Controller) MouseWheel(dx, dy int16) bool {
	c.mu.Lock()
	driven := c.beingDrivenLocked()
	c.mu.Unlock()
	if driven {
		return true
	}

	if !c.remote() {
		return false
	}
	if dx == 0 && dy == 0 {
		return true
	}
	c.emit(proto.TypeMouseWheel, proto.MouseWheel{DX: dx, DY: dy}.Encode())
	return true
}

// Key forwards one PS/2 set-1 scancode. The Mac maps it back itself and
// applies the modifier translation — the PC's Ctrl becomes Cmd, so Ctrl+C
// still copies — so nothing here needs to know either keyboard layout.
func (c *Controller) Key(scancode uint16, down, extended bool) bool {
	c.noteModifier(scancode, down, extended)

	c.mu.Lock()
	remote := c.active() && c.model.IsRemote()
	panicking := down && remote && c.isPanicHotkeyLocked(scancode, extended)
	c.mu.Unlock()

	if panicking {
		// Swallowed and never forwarded, so there is always a way back even if
		// the pointer is stranded. Only while the Mac has control: an agent
		// that is not capturing has no business eating the user's hotkeys.
		c.ForceRelease("released with the escape hotkey")
		return true
	}
	if !remote {
		return false
	}
	var flags byte
	if extended {
		flags = proto.KeyFlagExtended
	}
	c.emit(proto.TypeKey, proto.Key{Scancode: scancode, Down: down, Flags: flags}.Encode())
	return true
}

// ForceRelease hands control back unconditionally — the panic hotkey, a lost
// connection, a changed setting, shutdown. It is a no-op when the Mac does not
// have control, so every caller can fire it without checking first.
func (c *Controller) ForceRelease(reason string) {
	c.mu.Lock()
	action, released := c.model.ForceReturn()
	if !released {
		c.mu.Unlock()
		return
	}
	_, follow := c.applyLocked(action)
	c.mu.Unlock()
	follow()
	c.announce(false, "Back on this PC — "+reason+".", "control returned to this PC: "+reason)
}

// --- Internals ---

// active reports whether there is a Mac to send to and nothing else is already
// driving this PC. Callers must hold the mutex.
// expireDrivenForTest pretends the Mac has gone quiet, so the grace period can
// be tested without waiting it out.
func (c *Controller) expireDrivenForTest() {
	c.mu.Lock()
	c.drivenUntil = time.Now().Add(-time.Second)
	c.mu.Unlock()
}

// NoteDriven records that input has just arrived from the Mac, keeping this
// PC's own pointer swallowed for a little longer.
func (c *Controller) NoteDriven() {
	c.mu.Lock()
	if c.suspended {
		c.drivenUntil = time.Now().Add(drivenGrace)
	}
	c.mu.Unlock()
}

// beingDrivenLocked reports whether the Mac is driving this PC *right now*,
// rather than merely having said so at some point.
func (c *Controller) beingDrivenLocked() bool {
	return c.suspended && time.Now().Before(c.drivenUntil)
}

func (c *Controller) active() bool {
	return c.send != nil && c.enabled && !c.suspended
}

func (c *Controller) remote() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active() && c.model.IsRemote()
}

// applyLocked performs the part of an action that touches controller state and
// returns whether to swallow the event, plus a closure holding the part that
// must run with the mutex released. Parking and unparking the pointer are
// Win32 round trips; doing them under the lock would put them in the way of
// every other hook callback.
func (c *Controller) applyLocked(action Action) (swallow bool, follow func()) {
	switch action.Kind {
	case ActionEnterRemote:
		body := proto.MouseMove{X: action.X, Y: action.Y}.Encode()
		display := c.model.Local
		return true, func() {
			// Park before announcing: the pointer has to stop moving on this
			// PC now, not one network round trip from now.
			if c.pointer != nil {
				anchor, err := c.pointer.Park(display)
				if err != nil {
					c.log.Error("could not park the pointer; staying local", "err", err)
					c.ForceRelease("this PC would not hand over its pointer")
					return
				}
				c.mu.Lock()
				c.anchor = anchor
				c.mu.Unlock()
			}
			c.emit(proto.TypeEnter, body)
			c.announce(true, "Controlling your Mac.", "controlling the Mac")
		}

	case ActionMoveRemote:
		body := proto.MouseMove{X: action.X, Y: action.Y}.Encode()
		anchor := c.anchor
		return true, func() {
			// Undo whatever Windows did with this movement before anyone can
			// see it: swallowing the event does not hold the pointer.
			if c.pointer != nil {
				c.pointer.Warp(anchor)
			}
			c.emit(proto.TypeMouseMove, body)
		}

	case ActionReturnToLocal:
		local := action.Local
		return true, func() {
			// LEAVE first: it is what makes the Mac let go of every key and
			// button it is holding, and it has to go out even if unparking the
			// pointer then fails.
			c.emit(proto.TypeLeave, nil)
			if c.pointer != nil {
				if err := c.pointer.Release(local); err != nil {
					c.log.Error("could not unpark the pointer", "err", err)
				}
			}
			// The warp home is where the next local movement is measured from.
			// Leaving `last` on the anchor would make the first twitch after
			// coming back look like a jump across the display.
			c.mu.Lock()
			c.last, c.hasLast = local, true
			c.mu.Unlock()
		}

	default:
		return false, func() {}
	}
}

// aimAtDisplay points the model at the display under the cursor, and reports
// whether a crossing is possible there at all.
//
// Only the outer rim of the whole virtual desktop leads to the Mac: the
// boundary between two PC monitors has to stay an ordinary boundary. Callers
// must hold the mutex.
//
// The answer is cached per display, verdict included. Screens() enumerates
// monitors, which is far too slow to run from a hook procedure on every mouse
// movement — and caching only the displays that *can* cross would leave the
// slow path running continuously for a user whose cursor is sitting on the
// wrong monitor.
func (c *Controller) aimAtDisplay(cursor Point) bool {
	if c.opts.Screens == nil {
		return false
	}
	if within(c.aimed, cursor) {
		return c.aimedOK
	}
	info, err := c.opts.Screens()
	if err != nil {
		c.log.Debug("could not read the display layout", "err", err)
		return false
	}
	desktop := rectOf(info.Virtual)
	if c.opts.RemoteDesktop == (Rect{}) {
		// No better guess is available; see Options.RemoteDesktop.
		c.model.Remote = desktop
	}
	for _, monitor := range info.Monitors {
		display := rectOf(monitor)
		if !within(display, cursor) {
			continue
		}
		c.aimed = display
		c.aimedOK = isOuterEdge(c.model.Config.Edge, display, desktop)
		if c.aimedOK {
			c.model.Local = display
		}
		return c.aimedOK
	}
	// No monitor owns this point. Nothing is cached: the layout is about to
	// change, or already has.
	c.aimed, c.aimedOK = Rect{}, false
	return false
}

// forgetDisplays drops the cached display verdict, so the next movement reads
// the layout again. Anything that changes which edge counts has to call it.
func (c *Controller) forgetDisplays() {
	c.aimed, c.aimedOK = Rect{}, false
}

func within(r Rect, p Point) bool {
	return r.W > 0 && r.H > 0 && p.X >= r.X && p.X < r.MaxX() && p.Y >= r.Y && p.Y < r.MaxY()
}

// isOuterEdge reports whether this display's configured edge is also the edge
// of the whole desktop — otherwise there is another PC monitor beyond it.
func isOuterEdge(edge Edge, display, desktop Rect) bool {
	const slack = 1.0
	switch edge {
	case EdgeRight:
		return abs(display.MaxX()-desktop.MaxX()) < slack
	case EdgeLeft:
		return abs(display.X-desktop.X) < slack
	case EdgeBottom:
		return abs(display.MaxY()-desktop.MaxY()) < slack
	default: // EdgeTop
		return abs(display.Y-desktop.Y) < slack
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func rectOf(m proto.Monitor) Rect {
	return Rect{X: float64(m.Left), Y: float64(m.Top), W: float64(m.Width), H: float64(m.Height)}
}

// emit queues one message for the writer goroutine.
//
// It never blocks. A low-level hook that waits on anything gets silently
// removed by Windows, which would hand the user's input back to this PC
// mid-gesture with the pointer still parked.
func (c *Controller) emit(typ byte, body []byte) {
	select {
	case c.out <- message{typ: typ, body: body}:
		return
	default:
	}
	if typ == proto.TypeMouseMove {
		// Positions are absolute, so the next one supersedes this one
		// completely. Dropping it costs a frame of smoothness and nothing else.
		c.dropped.Add(1)
		return
	}
	// A dropped button or key would stay down on the Mac indefinitely. Rather
	// than wait for room — the one thing the hook must not do — give up on the
	// crossing and let LEAVE clean up.
	select {
	case c.urgent <- struct{}{}:
	default:
	}
}

func (c *Controller) pump() {
	for {
		select {
		case <-c.done:
			return
		case <-c.urgent:
			c.ForceRelease("this PC could not keep up with the link")
		case n := <-c.notices:
			if n.log != "" {
				c.log.Info(n.log)
			}
			c.mu.Lock()
			onStatus := c.opts.OnStatus
			c.mu.Unlock()
			if onStatus != nil {
				onStatus(n.status)
			}
		case msg := <-c.out:
			c.mu.Lock()
			send := c.send
			c.mu.Unlock()
			if send == nil {
				continue
			}
			if err := send(msg.typ, msg.body); err != nil {
				// The session is on its way out; the server will detach us.
				c.log.Debug("could not send captured input", "type", msg.typ, "err", err)
			}
		}
	}
}

func (c *Controller) drain() {
	for {
		select {
		case <-c.out:
		default:
			return
		}
	}
}

// announce hands a log line and a status update to the writer goroutine. It
// never blocks: the crossing itself has already happened, and a status message
// is not worth stalling the hook procedure for.
func (c *Controller) announce(remote bool, detail, logLine string) {
	select {
	case c.notices <- notice{status: Status{Remote: remote, Detail: detail}, log: logLine}:
	default:
	}
}

// --- Panic hotkey ---
//
// PS/2 set-1 scancodes for the modifiers, so the hotkey is recognised from the
// same stream that is forwarded to the Mac rather than from a second, drifting
// source of truth.

const (
	scanControl = 0x1D // extended: right control
	scanShift   = 0x2A
	scanShiftR  = 0x36
	scanAlt     = 0x38 // extended: right alt
	scanWinL    = 0x5B // always extended
	scanWinR    = 0x5C // always extended
	scanP       = 0x19
)

// modifierKey folds a scancode and its extended flag into one key for `held`.
// Left and right count as the same modifier, as they do for any hotkey.
func modifierKey(scancode uint16, extended bool) (uint16, bool) {
	switch scancode {
	case scanControl, scanAlt, scanShift, scanShiftR:
		return scancode, true
	case scanWinL, scanWinR:
		if extended {
			return scanWinL, true
		}
	}
	return 0, false
}

func (c *Controller) noteModifier(scancode uint16, down, extended bool) {
	key, ok := modifierKey(scancode, extended)
	if !ok {
		return
	}
	c.mu.Lock()
	if down {
		c.held[key] = true
	} else {
		delete(c.held, key)
	}
	c.mu.Unlock()
}

// isPanicHotkeyLocked recognises Ctrl+Alt+Win+P, the mirror of the Mac's
// Ctrl+Option+Cmd+P. Three modifiers, because two is something an application
// might reasonably want for itself.
func (c *Controller) isPanicHotkeyLocked(scancode uint16, extended bool) bool {
	if scancode != scanP || extended {
		return false
	}
	return c.held[scanControl] && c.held[scanAlt] && c.held[scanWinL]
}
