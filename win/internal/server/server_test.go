package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/janjamscikov/porthmoss/win/internal/inject"
	"github.com/janjamscikov/porthmoss/win/internal/pairing"
	"github.com/janjamscikov/porthmoss/win/internal/proto"
)

// testRig starts a real TLS agent on a loopback port with a recording injector.
type testRig struct {
	addr     string
	fake     *inject.Fake
	identity *pairing.Identity
	codes    chan string
	srv      *Server
}

// busy reports whether a controller still holds the agent's single slot.
func (r *testRig) busy() bool {
	r.srv.mu.Lock()
	defer r.srv.mu.Unlock()
	return r.srv.busy
}

func newRig(t *testing.T) *testRig {
	t.Helper()
	identity, err := pairing.Load(t.TempDir())
	if err != nil {
		t.Fatalf("pairing.Load: %v", err)
	}
	rig := &testRig{fake: inject.NewFake(), identity: identity, codes: make(chan string, 4)}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	rig.addr = ln.Addr().String()
	ln.Close() // hand the port straight back to the server

	srv := &Server{
		Addr:          rig.addr,
		Identity:      identity,
		Injector:      rig.fake,
		Log:           slog.New(slog.DiscardHandler),
		OnPairingCode: func(code string) { rig.codes <- code },
	}
	rig.srv = srv
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx)

	waitForListener(t, rig.addr)
	return rig
}

func waitForListener(t *testing.T, addr string) {
	t.Helper()
	for range 100 {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			// Let the server finish rejecting the probe and free its single slot.
			time.Sleep(50 * time.Millisecond)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never came up on %s", addr)
}

// client is a minimal Mac-side implementation, and doubles as the reference
// the Swift client is ported from.
type client struct {
	conn   net.Conn
	buf    []byte
	screen proto.ScreenInfo
}

func dial(t *testing.T, rig *testRig, code string) (*client, error) {
	return dialWithPairingRequest(t, rig, code, false)
}

// requestPairing sets the HELLO flag meaning "this Mac holds no secret for
// you". The agent's answer, read out of the CHALLENGE below, is a separate
// thing and the two must not be conflated.
func dialWithPairingRequest(t *testing.T, rig *testRig, code string, requestPairing bool) (*client, error) {
	t.Helper()
	var gotFingerprint [32]byte
	conn, err := tls.Dial("tcp", rig.addr, &tls.Config{
		InsecureSkipVerify: true, // we pin the fingerprint ourselves, below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			gotFingerprint = sha256.Sum256(rawCerts[0])
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { conn.Close() })
	c := &client{conn: conn, buf: make([]byte, proto.MaxFrame)}

	hello := proto.Hello{Version: proto.Version, Name: "test-mac"}
	if requestPairing {
		hello.Flags |= proto.HelloFlagNeedsPairing
	}
	if err := proto.WriteFrame(conn, proto.TypeHello, hello.Encode()); err != nil {
		return nil, err
	}
	frame, err := proto.ReadFrame(conn, c.buf)
	if err != nil {
		return nil, err
	}
	if frame.Type != proto.TypeChallenge {
		return nil, errors.New("expected CHALLENGE, got " + hex(frame.Type))
	}
	nonce := slices.Clone(frame.Body[:proto.NonceSize])
	needsPairing := frame.Body[proto.NonceSize] == 1

	secret := rig.identity.Secret()
	if needsPairing {
		if secret, err = pairing.DeriveSecret(code, gotFingerprint); err != nil {
			return nil, err
		}
	}
	if err := proto.WriteFrame(conn, proto.TypeAuth, pairing.Sign(secret, nonce)); err != nil {
		return nil, err
	}
	if frame, err = proto.ReadFrame(conn, c.buf); err != nil {
		return nil, err
	}
	if frame.Type != proto.TypeReady {
		return nil, errors.New("auth rejected: " + string(frame.Body))
	}
	if c.screen, err = proto.DecodeScreenInfo(frame.Body); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *client) send(typ byte, body []byte) error { return proto.WriteFrame(c.conn, typ, body) }

func TestPairAndControl(t *testing.T) {
	rig := newRig(t)

	// First connection with no code should fail: the agent is unpaired and has
	// not shown a code to anyone yet.
	if _, err := dial(t, rig, "000000"); err == nil {
		t.Fatal("expected auth failure with a guessed code")
	}
	code := <-rig.codes

	c, err := dial(t, rig, code)
	if err != nil {
		t.Fatalf("pairing with the displayed code: %v", err)
	}
	if got := c.screen.Virtual.Width; got != 2560 {
		t.Errorf("virtual desktop width = %d, want 2560", got)
	}
	if !rig.identity.Paired() {
		t.Error("agent should be paired after a successful handshake")
	}

	// Drop the release-all events the probe and rejected connections produced.
	rig.fake.Drain()

	mustSend(t, c, proto.TypeEnter, proto.MouseMove{X: 100, Y: 200}.Encode())
	mustSend(t, c, proto.TypeMouseMove, proto.MouseMove{X: 32768, Y: 16384}.Encode())
	mustSend(t, c, proto.TypeMouseButton, proto.MouseButton{Button: proto.ButtonLeft, Down: true}.Encode())
	mustSend(t, c, proto.TypeMouseButton, proto.MouseButton{Button: proto.ButtonLeft, Down: false}.Encode())
	mustSend(t, c, proto.TypeMouseWheel, proto.MouseWheel{DY: -3}.Encode())
	// Ctrl down, C, Ctrl up — what Cmd+C on the Mac keyboard becomes.
	mustSend(t, c, proto.TypeKey, proto.Key{Scancode: 0x1D, Down: true}.Encode())
	mustSend(t, c, proto.TypeKey, proto.Key{Scancode: 0x2E, Down: true}.Encode())
	mustSend(t, c, proto.TypeKey, proto.Key{Scancode: 0x2E, Down: false}.Encode())
	mustSend(t, c, proto.TypeKey, proto.Key{Scancode: 0x1D, Down: false}.Encode())

	// PING must round-trip, since the Mac's dead-man switch relies on it.
	mustSend(t, c, proto.TypePing, proto.EncodeU64(42))
	c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	frame, err := proto.ReadFrame(c.conn, c.buf)
	if err != nil {
		t.Fatalf("reading PONG: %v", err)
	}
	if frame.Type != proto.TypePong {
		t.Fatalf("expected PONG, got %s", hex(frame.Type))
	}
	if id, _ := proto.DecodeU64(frame.Body); id != 42 {
		t.Errorf("PONG id = %d, want 42", id)
	}

	want := []string{
		"move 100,200",
		"move 32768,16384",
		"button 1 down=true",
		"button 1 down=false",
		"wheel 0,-3",
		"key 0x1d down=true ext=false",
		"key 0x2e down=true ext=false",
		"key 0x2e down=false ext=false",
		"key 0x1d down=false ext=false",
	}
	if got := rig.fake.Drain(); !slices.Equal(got, want) {
		t.Errorf("injected events\n got: %q\nwant: %q", got, want)
	}
}

func TestDeadManSwitchReleasesHeldKeys(t *testing.T) {
	rig := newRig(t)
	if _, err := dial(t, rig, "000000"); err == nil {
		t.Fatal("expected failure before pairing")
	}
	c, err := dial(t, rig, <-rig.codes)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	// Hold a key and a button, then go silent, as a dropped Wi-Fi link would.
	mustSend(t, c, proto.TypeKey, proto.Key{Scancode: 0x1D, Down: true}.Encode())
	mustSend(t, c, proto.TypeMouseButton, proto.MouseButton{Button: proto.ButtonLeft, Down: true}.Encode())
	if rig.fake.Held() != 2 {
		t.Fatalf("expected 2 inputs held, got %d", rig.fake.Held())
	}

	deadline := time.Now().Add(idleTimeout + 3*time.Second)
	for time.Now().Before(deadline) {
		if rig.fake.Held() == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("dead-man switch did not release input; %d still held", rig.fake.Held())
}

func TestRejectsSecondController(t *testing.T) {
	rig := newRig(t)
	if _, err := dial(t, rig, "000000"); err == nil {
		t.Fatal("expected failure before pairing")
	}
	if _, err := dial(t, rig, <-rig.codes); err != nil {
		t.Fatalf("first controller: %v", err)
	}
	if _, err := dial(t, rig, ""); err == nil {
		t.Error("a second Mac should not be able to take control")
	}
}

// TestTheAgentCanDriveTheMac covers the seam the capture path hangs off: a
// session hands out a Sender, and whatever is written to it reaches the Mac as
// a real frame on the connection the Mac itself opened. Everything above this
// — the low-level hooks — needs a Windows desktop, so this is as far as the
// reverse direction can be proved from either machine.
func TestTheAgentCanDriveTheMac(t *testing.T) {
	rig := newRig(t)
	senders := make(chan Sender, 4)
	rig.srv.OnController = func(send Sender) { senders <- send }

	if _, err := dial(t, rig, "000000"); err == nil {
		t.Fatal("expected failure before pairing")
	}
	c, err := dial(t, rig, <-rig.codes)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	send := waitForSender(t, senders)
	if send == nil {
		t.Fatal("the session offered a nil sender while a Mac was connected")
	}

	// The PC crosses onto the Mac, moves, types, and hands control back.
	messages := []struct {
		typ  byte
		body []byte
	}{
		{proto.TypeEnter, proto.MouseMove{X: 65535, Y: 32768}.Encode()},
		{proto.TypeMouseMove, proto.MouseMove{X: 40000, Y: 32768}.Encode()},
		{proto.TypeKey, proto.Key{Scancode: 0x2E, Down: true}.Encode()},
		{proto.TypeLeave, nil},
	}
	for _, message := range messages {
		if err := send(message.typ, message.body); err != nil {
			t.Fatalf("sending %s to the Mac: %v", hex(message.typ), err)
		}
	}

	for i, message := range messages {
		c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		frame, err := proto.ReadFrame(c.conn, c.buf)
		if err != nil {
			t.Fatalf("reading message %d: %v", i, err)
		}
		if frame.Type != message.typ {
			t.Fatalf("message %d arrived as %s, want %s", i, hex(frame.Type), hex(message.typ))
		}
		if !bytes.Equal(frame.Body, message.body) {
			t.Errorf("message %d body = %x, want %x", i, frame.Body, message.body)
		}
	}

	// The sender must be withdrawn when the session ends. Without that, a
	// dropped link would leave this PC's pointer parked with nowhere to send.
	c.conn.Close()
	if send := waitForSender(t, senders); send != nil {
		t.Error("the session ended without withdrawing its sender")
	}
}

// TestTheMacTakingControlIsReportedToTheCapturePath is what stops the two
// machines fighting over one pointer: while the Mac drives this PC, this PC
// must not be trying to drive the Mac.
func TestTheMacTakingControlIsReportedToTheCapturePath(t *testing.T) {
	rig := newRig(t)
	control := make(chan bool, 8)
	rig.srv.OnRemoteControl = func(active bool) { control <- active }

	if _, err := dial(t, rig, "000000"); err == nil {
		t.Fatal("expected failure before pairing")
	}
	c, err := dial(t, rig, <-rig.codes)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	mustSend(t, c, proto.TypeEnter, proto.MouseMove{X: 100, Y: 200}.Encode())
	if active := waitForControl(t, control); !active {
		t.Error("ENTER from the Mac should suspend this PC's capture")
	}
	mustSend(t, c, proto.TypeLeave, nil)
	if active := waitForControl(t, control); active {
		t.Error("LEAVE from the Mac should let this PC capture again")
	}
}

func waitForSender(t *testing.T, senders chan Sender) Sender {
	t.Helper()
	select {
	case send := <-senders:
		return send
	case <-time.After(idleTimeout + 3*time.Second):
		t.Fatal("the session never reported its sender")
		return nil
	}
}

func waitForControl(t *testing.T, control chan bool) bool {
	t.Helper()
	select {
	case active := <-control:
		return active
	case <-time.After(2 * time.Second):
		t.Fatal("the session never reported who has control")
		return false
	}
}

func mustSend(t *testing.T, c *client, typ byte, body []byte) {
	t.Helper()
	if err := c.send(typ, body); err != nil {
		t.Fatalf("send 0x%02x: %v", typ, err)
	}
	// Let the server apply it before the next assertion.
	time.Sleep(5 * time.Millisecond)
}

func hex(b byte) string {
	const digits = "0123456789abcdef"
	return "0x" + string([]byte{digits[b>>4], digits[b&0xf]})
}

// TestSessionOutlivesHandshakeTimeout guards a bug that made every session die
// a few seconds in: the handshake used SetDeadline, which sets a *write*
// deadline too, and the dispatch loop only refreshed the read side. Once it
// expired, a reply could fail midway through a TLS record and the Mac saw
// stream corruption ("bad MAC") rather than a clean disconnect.
func TestSessionOutlivesHandshakeTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("takes longer than the handshake timeout by design")
	}
	rig := newRig(t)
	if _, err := dial(t, rig, "000000"); err == nil {
		t.Fatal("expected failure before pairing")
	}
	c, err := dial(t, rig, <-rig.codes)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	// Ping across the old deadline the way the Mac's heartbeat does, and read
	// every reply: a corrupted record shows up here as a read error.
	deadline := time.Now().Add(handshakeTimeout + 3*time.Second)
	for id := uint64(0); time.Now().Before(deadline); id++ {
		if err := c.send(proto.TypePing, proto.EncodeU64(id)); err != nil {
			t.Fatalf("ping %d failed after %s: %v", id, handshakeTimeout, err)
		}
		c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		frame, err := proto.ReadFrame(c.conn, c.buf)
		if err != nil {
			t.Fatalf("pong %d failed after %s: %v", id, handshakeTimeout, err)
		}
		if frame.Type != proto.TypePong {
			t.Fatalf("expected PONG, got %s", hex(frame.Type))
		}
		if got, _ := proto.DecodeU64(frame.Body); got != id {
			t.Fatalf("PONG id = %d, want %d", got, id)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestMacThatLostItsPairingCanPairAgain covers a Mac whose stored secret is
// gone — reinstalled, restored from backup, or migrated between stores — while
// the agent still believes it is paired. Without this the only way back is to
// unpair physically at the PC, which is a dead end if the PC is not to hand.
func TestMacThatLostItsPairingCanPairAgain(t *testing.T) {
	rig := newRig(t)
	if _, err := dial(t, rig, "000000"); err == nil {
		t.Fatal("expected failure before pairing")
	}
	firstCode := <-rig.codes

	paired, err := dial(t, rig, firstCode)
	if err != nil {
		t.Fatalf("initial pairing: %v", err)
	}
	oldSecret := slices.Clone(rig.identity.Secret())
	// The agent takes one controller at a time, so this has to go before
	// anything else can be let in.
	paired.conn.Close()
	waitForFreeSlot(t, rig)

	// A Mac that admits it holds no secret is offered a fresh code, and a wrong
	// guess at that code still gets nowhere.
	if _, err := dialWithPairingRequest(t, rig, "000000", true); err == nil {
		t.Fatal("a guessed pairing code must not be accepted")
	}
	newCode := <-rig.codes
	if newCode == firstCode {
		t.Error("re-pairing should mint a new code, not reuse the old one")
	}
	if got := rig.identity.Secret(); !slices.Equal(got, oldSecret) {
		t.Error("the existing pairing must survive until a new one completes")
	}
	waitForFreeSlot(t, rig)

	if _, err := dialWithPairingRequest(t, rig, newCode, true); err != nil {
		t.Fatalf("re-pairing with the displayed code: %v", err)
	}
	if slices.Equal(rig.identity.Secret(), oldSecret) {
		t.Error("a completed re-pair should have replaced the secret")
	}
}

// waitForFreeSlot blocks until the agent has finished tearing down the last
// session, since it refuses a second controller while one is still attached.
func waitForFreeSlot(t *testing.T, rig *testRig) {
	t.Helper()
	for range 50 {
		conn, err := net.DialTimeout("tcp", rig.addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
		}
		time.Sleep(20 * time.Millisecond)
		if !rig.busy() {
			return
		}
	}
	t.Fatal("the agent never released its controller slot")
}

// TestFileArrivesInTheDropFolder drives a whole transfer through a real
// session: BEGIN, chunks, END, and the file on disk at the other end.
func TestFileArrivesInTheDropFolder(t *testing.T) {
	rig := newRig(t)
	dropDir := t.TempDir()
	rig.srv.DropDir = dropDir

	if _, err := dial(t, rig, "000000"); err == nil {
		t.Fatal("expected failure before pairing")
	}
	c, err := dial(t, rig, <-rig.codes)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	// Larger than one chunk, so the reassembly is actually exercised.
	content := bytes.Repeat([]byte("porthmoss"), 40000)
	begin := proto.FileBegin{Name: "notes.txt", Size: uint64(len(content)), Total: 1}
	mustSend(t, c, proto.TypeFileBegin, begin.Encode())
	for offset := 0; offset < len(content); offset += proto.FileChunkSize {
		end := min(offset+proto.FileChunkSize, len(content))
		mustSend(t, c, proto.TypeFileChunk, content[offset:end])
	}
	mustSend(t, c, proto.TypeFileEnd, nil)
	time.Sleep(300 * time.Millisecond)

	got, err := os.ReadFile(filepath.Join(dropDir, "notes.txt"))
	if err != nil {
		t.Fatalf("the file did not arrive: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("received %d bytes, sent %d", len(got), len(content))
	}
}

// TestFileCannotEscapeTheDropFolder is the one that matters: the name is
// chosen by whatever is at the other end of the socket.
func TestFileCannotEscapeTheDropFolder(t *testing.T) {
	rig := newRig(t)
	parent := t.TempDir()
	dropDir := filepath.Join(parent, "drop")
	rig.srv.DropDir = dropDir

	if _, err := dial(t, rig, "000000"); err == nil {
		t.Fatal("expected failure before pairing")
	}
	c, err := dial(t, rig, <-rig.codes)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	content := []byte("owned")
	begin := proto.FileBegin{Name: "../escaped.txt", Size: uint64(len(content)), Total: 1}
	mustSend(t, c, proto.TypeFileBegin, begin.Encode())
	mustSend(t, c, proto.TypeFileChunk, content)
	mustSend(t, c, proto.TypeFileEnd, nil)
	time.Sleep(300 * time.Millisecond)

	if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); err == nil {
		t.Fatal("a file was written outside the drop folder")
	}
	// The session must survive a rejected file: the user is still holding a
	// mouse that has to keep working.
	if err := c.send(proto.TypePing, proto.EncodeU64(1)); err != nil {
		t.Errorf("the session died over a rejected file: %v", err)
	}
}
