package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
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
}

func newRig(t *testing.T) *testRig {
	t.Helper()
	identity, err := pairing.Load(t.TempDir())
	if err != nil {
		t.Fatalf("pairing.Load: %v", err)
	}
	rig := &testRig{fake: inject.NewFake(), identity: identity, codes: make(chan string, 1)}

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

	if err := proto.WriteFrame(conn, proto.TypeHello,
		proto.Hello{Version: proto.Version, Name: "test-mac"}.Encode()); err != nil {
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
