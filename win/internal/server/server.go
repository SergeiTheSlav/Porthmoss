// Package server accepts a paired Mac and applies its input to this desktop.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/janjamscikov/porthmoss/win/internal/inject"
	"github.com/janjamscikov/porthmoss/win/internal/pairing"
	"github.com/janjamscikov/porthmoss/win/internal/proto"
)

const (
	// DefaultPort is the agent's listening port.
	DefaultPort = 47654

	// handshakeTimeout bounds how long an unauthenticated peer may hold the socket.
	handshakeTimeout = 10 * time.Second
	// idleTimeout is the dead-man switch: the Mac pings every 500 ms, so silence
	// this long means the link is gone and everything held must be released.
	idleTimeout = 2 * time.Second
	// writeTimeout bounds a single reply. It is applied per write: a deadline
	// left over from an earlier phase would eventually kill a healthy session.
	writeTimeout = 2 * time.Second
	// maxPairAttempts before the code is rotated, so a 6-digit code cannot be
	// ground down by reconnecting.
	maxPairAttempts = 5
)

// Server is the agent's control endpoint. One Mac at a time.
type Server struct {
	Addr     string
	Identity *pairing.Identity
	Injector inject.Injector
	Log      *slog.Logger

	// OnPairingCode is called with a fresh code when an unpaired Mac connects.
	// The CLI prints it; a tray UI would show it in a window.
	OnPairingCode func(code string)

	mu           sync.Mutex
	busy         bool
	pairCode     string
	pairAttempts int
}

// Serve listens until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	cfg := &tls.Config{
		Certificates: []tls.Certificate{s.Identity.Certificate},
		MinVersion:   tls.VersionTLS13,
	}
	lc := net.ListenConfig{}
	raw, err := lc.Listen(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("server: listen on %s: %w", s.Addr, err)
	}
	ln := tls.NewListener(raw, cfg)
	defer ln.Close()

	s.Log.Info("listening", "addr", raw.Addr().String(),
		"fingerprint", s.Identity.FingerprintHex(), "paired", s.Identity.Paired())

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("server: accept: %w", err)
		}
		if !s.claim() {
			s.Log.Warn("rejecting second controller", "remote", conn.RemoteAddr())
			proto.WriteFrame(conn, proto.TypeError, []byte("already controlled by another Mac"))
			conn.Close()
			continue
		}
		go func() {
			defer s.release()
			defer conn.Close()
			if err := s.handle(ctx, conn); err != nil && !errors.Is(err, io.EOF) {
				s.Log.Info("session ended", "remote", conn.RemoteAddr(), "reason", err)
			}
			// Whatever went wrong, nothing stays held down.
			if err := s.Injector.ReleaseAll(); err != nil {
				s.Log.Error("release on disconnect failed", "err", err)
			}
		}()
	}
}

func (s *Server) claim() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
	return true
}

func (s *Server) release() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

func (s *Server) handle(ctx context.Context, conn net.Conn) error {
	if tcp, ok := underlyingTCP(conn); ok {
		// Input events are tiny and latency-critical; Nagle would batch them
		// into perceptible cursor stutter.
		if err := tcp.SetNoDelay(true); err != nil {
			s.Log.Warn("could not disable Nagle", "err", err)
		}
	}

	conn.SetDeadline(time.Now().Add(handshakeTimeout))
	name, err := s.handshake(conn)
	if err != nil {
		proto.WriteFrame(conn, proto.TypeError, []byte(err.Error()))
		return err
	}
	s.Log.Info("controller connected", "name", name, "remote", conn.RemoteAddr())

	// Drop the handshake deadline. SetDeadline set a *write* deadline too, and
	// the loop below only ever refreshes the read side — leaving it in place
	// makes every session die once it outlives handshakeTimeout, and because a
	// timeout can land midway through a TLS record, the peer sees stream
	// corruption ("bad MAC") rather than a clean disconnect.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clearing handshake deadline: %w", err)
	}

	buf := make([]byte, proto.MaxFrame)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn.SetReadDeadline(time.Now().Add(idleTimeout))
		frame, err := proto.ReadFrame(conn, buf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return fmt.Errorf("dead-man switch: no traffic for %s", idleTimeout)
			}
			return err
		}
		if err := s.dispatch(conn, frame); err != nil {
			return err
		}
	}
}

func (s *Server) handshake(conn net.Conn) (string, error) {
	buf := make([]byte, proto.MaxFrame)

	frame, err := proto.ReadFrame(conn, buf)
	if err != nil {
		return "", fmt.Errorf("handshake: read hello: %w", err)
	}
	if frame.Type != proto.TypeHello {
		return "", fmt.Errorf("handshake: expected HELLO, got 0x%02x", frame.Type)
	}
	hello, err := proto.DecodeHello(frame.Body)
	if err != nil {
		return "", fmt.Errorf("handshake: bad hello: %w", err)
	}
	if hello.Version != proto.Version {
		return "", fmt.Errorf("handshake: protocol version %d, agent speaks %d", hello.Version, proto.Version)
	}

	nonce, err := pairing.NewNonce()
	if err != nil {
		return "", err
	}
	expected, pairingNow, err := s.expectedSecret()
	if err != nil {
		return "", err
	}
	challenge := append(append([]byte{}, nonce...), boolByte(pairingNow))
	if err := proto.WriteFrame(conn, proto.TypeChallenge, challenge); err != nil {
		return "", err
	}

	frame, err = proto.ReadFrame(conn, buf)
	if err != nil {
		return "", fmt.Errorf("handshake: read auth: %w", err)
	}
	if frame.Type != proto.TypeAuth {
		return "", fmt.Errorf("handshake: expected AUTH, got 0x%02x", frame.Type)
	}
	if len(frame.Body) != proto.MACSize {
		return "", fmt.Errorf("handshake: auth proof is %d bytes, want %d", len(frame.Body), proto.MACSize)
	}
	if !pairing.Verify(expected, nonce, frame.Body) {
		s.failedAttempt(pairingNow)
		return "", errors.New("handshake: authentication failed")
	}
	if pairingNow {
		if err := s.Identity.Save(expected, hello.Name); err != nil {
			return "", err
		}
		s.clearPairing()
		s.Log.Info("paired", "name", hello.Name)
	}

	screens, err := s.Injector.Screens()
	if err != nil {
		return "", fmt.Errorf("handshake: read display layout: %w", err)
	}
	if err := proto.WriteFrame(conn, proto.TypeReady, screens.Encode()); err != nil {
		return "", err
	}
	return hello.Name, nil
}

// expectedSecret returns the secret the peer must prove, and whether this is a
// first-time pairing (in which case the secret comes from the displayed code).
func (s *Server) expectedSecret() (secret []byte, pairingNow bool, err error) {
	if s.Identity.Paired() {
		return s.Identity.Secret(), false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pairCode == "" {
		if s.pairCode, err = pairing.NewCode(); err != nil {
			return nil, false, err
		}
		s.pairAttempts = 0
		if s.OnPairingCode != nil {
			s.OnPairingCode(s.pairCode)
		}
	}
	secret, err = pairing.DeriveSecret(s.pairCode, s.Identity.Fingerprint)
	return secret, true, err
}

func (s *Server) failedAttempt(pairingNow bool) {
	if !pairingNow {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pairAttempts++
	if s.pairAttempts >= maxPairAttempts {
		s.Log.Warn("too many failed pairing attempts, rotating code")
		s.pairCode = ""
	}
}

func (s *Server) clearPairing() {
	s.mu.Lock()
	s.pairCode = ""
	s.pairAttempts = 0
	s.mu.Unlock()
}

func (s *Server) dispatch(conn net.Conn, frame proto.Frame) error {
	switch frame.Type {
	case proto.TypeMouseMove:
		m, err := proto.DecodeMouseMove(frame.Body)
		if err != nil {
			return err
		}
		return s.Injector.MoveTo(m.X, m.Y)

	case proto.TypeMouseButton:
		b, err := proto.DecodeMouseButton(frame.Body)
		if err != nil {
			return err
		}
		return s.Injector.Button(b.Button, b.Down)

	case proto.TypeMouseWheel:
		w, err := proto.DecodeMouseWheel(frame.Body)
		if err != nil {
			return err
		}
		return s.Injector.Wheel(w.DX, w.DY)

	case proto.TypeKey:
		k, err := proto.DecodeKey(frame.Body)
		if err != nil {
			return err
		}
		return s.Injector.Key(k.Scancode, k.Down, k.Extended())

	case proto.TypeKeyReset, proto.TypeLeave:
		return s.Injector.ReleaseAll()

	case proto.TypeEnter:
		m, err := proto.DecodeMouseMove(frame.Body)
		if err != nil {
			return err
		}
		return s.Injector.MoveTo(m.X, m.Y)

	case proto.TypePing:
		return writeFrame(conn, proto.TypePong, frame.Body)

	case proto.TypePong:
		return nil

	default:
		// Unknown types are ignored so a newer Mac can add messages without
		// breaking an older agent.
		s.Log.Debug("ignoring unknown message", "type", fmt.Sprintf("0x%02x", frame.Type))
		return nil
	}
}

// writeFrame sends one message under its own fresh deadline, so a slow or
// wedged peer cannot block the session forever and no stale deadline can leak
// in from an earlier phase.
func writeFrame(conn net.Conn, typ byte, body []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return proto.WriteFrame(conn, typ, body)
}

func underlyingTCP(conn net.Conn) (*net.TCPConn, bool) {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		conn = tlsConn.NetConn()
	}
	tcp, ok := conn.(*net.TCPConn)
	return tcp, ok
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}
