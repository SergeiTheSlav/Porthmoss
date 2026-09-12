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

	"github.com/janjamscikov/porthmoss/win/internal/clipboard"
	"github.com/janjamscikov/porthmoss/win/internal/inject"
	"github.com/janjamscikov/porthmoss/win/internal/pairing"
	"github.com/janjamscikov/porthmoss/win/internal/proto"
	"github.com/janjamscikov/porthmoss/win/internal/transfer"
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

// Sender writes one message to the connected Mac. Every write in a session
// goes through one of these, so it is safe to call from any goroutine.
type Sender func(typ byte, body []byte) error

// Server is the agent's control endpoint. One Mac at a time.
type Server struct {
	Addr     string
	Identity *pairing.Identity
	Injector inject.Injector
	Log      *slog.Logger

	// OnPairingCode is called with a fresh code when an unpaired Mac connects.
	// The console prints it; the tray UI shows it in a window.
	OnPairingCode func(code string)

	// OnListening is called once the socket is accepting connections, so the
	// caller can show the user an address they can actually dial.
	OnListening func(addr string)

	// OnSession reports a Mac connecting and disconnecting, so the UI can
	// show who is in control without polling.
	OnSession func(connected bool, peer string)

	// OnController hands out a Sender for the life of a session, and nil when
	// it ends. It is how this PC's own capture path drives the Mac: the same
	// messages, in the other direction, down the connection the Mac opened.
	OnController func(Sender)

	// OnClientInfo reports what the Mac has said about itself: its desktop
	// size, which edge of this PC's desktop it lies beyond, and whether it
	// accepts being driven at all. Reverse control is configured from the Mac,
	// so this is where the capture path learns what to do.
	OnClientInfo func(proto.ClientInfo)

	// OnRemoteControl reports the Mac taking and releasing control of this PC.
	// The capture path uses it to stay out of the way — two machines both
	// trying to own one pointer would fight over it.
	OnRemoteControl func(active bool)

	// Clipboard, when set, is kept in step with the Mac's.
	Clipboard clipboard.Clipboard

	// DropDir is where files dragged from the Mac land. Empty disables
	// receiving them.
	DropDir string

	// OnFileReceived reports a completed file, so the UI can offer to open it.
	OnFileReceived func(path string, index, total int)

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
	if s.OnListening != nil {
		s.OnListening(raw.Addr().String())
	}

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
			proto.WriteFrame(conn, proto.TypeError, []byte("Another Mac is already controlling this PC."))
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

func (s *Server) remoteControl(active bool) {
	if s.OnRemoteControl != nil {
		s.OnRemoteControl(active)
	}
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
	if s.OnSession != nil {
		s.OnSession(true, name)
		defer s.OnSession(false, name)
	}

	// Drop the handshake deadline. SetDeadline set a *write* deadline too, and
	// the loop below only ever refreshes the read side — leaving it in place
	// makes every session die once it outlives handshakeTimeout, and because a
	// timeout can land midway through a TLS record, the peer sees stream
	// corruption ("bad MAC") rather than a clean disconnect.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clearing handshake deadline: %w", err)
	}

	// The clipboard watcher and this loop both write to the socket, so every
	// write goes through one place.
	var writeMu sync.Mutex
	send := func(typ byte, body []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeFrame(conn, typ, body)
	}

	// Let this PC's capture path drive the Mac for as long as the session
	// lasts. Handing back nil on the way out is what makes a dropped link
	// return control here rather than leaving the pointer parked.
	if s.OnController != nil {
		s.OnController(send)
		defer s.OnController(nil)
	}
	// A Mac that disconnects mid-control never sends LEAVE, so the release has
	// to happen here too or this PC would never capture again.
	defer s.remoteControl(false)

	sessionCtx, endSession := context.WithCancel(ctx)
	defer endSession()

	var clip *clipboardBridge
	if s.Clipboard != nil {
		clip = newClipboardBridge(s.Clipboard, s.Log)
		sendFiles := func(paths []string) error {
			s.Log.Info("sharing copied files with the Mac", "count", len(paths))
			if err := transfer.Send(paths, proto.FileFlagClipboard, send); err != nil {
				// A file we cannot read is the user's problem to see, not a
				// reason to end a session they are still using.
				s.Log.Warn("could not share copied files", "err", err)
			}
			return nil
		}
		go clip.watch(sessionCtx, send, sendFiles)
	}

	var incoming *transfer.Receiver
	if s.DropDir != "" {
		incoming = &transfer.Receiver{Dir: s.DropDir}
		// A half-written file must not survive the session that was sending it.
		defer incoming.Abort()
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
		if err := s.dispatch(send, clip, incoming, frame); err != nil {
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
	expected, pairingNow, err := s.expectedSecret(hello.NeedsPairing())
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

// expectedSecret returns the secret the peer must prove, and whether a pairing
// code is in play (in which case the secret is derived from the displayed code).
//
// macRequestsPairing covers a Mac that has lost its half of the pairing — a
// reinstall, or restored-from-backup. Honouring it means anyone on the network
// can make a code appear on the PC, which is a nuisance and nothing more: the
// code still has to be read off that screen, and the existing pairing is only
// replaced once a new one actually completes.
func (s *Server) expectedSecret(macRequestsPairing bool) (secret []byte, pairingNow bool, err error) {
	if s.Identity.Paired() && !macRequestsPairing {
		return s.Identity.Secret(), false, nil
	}
	if macRequestsPairing && s.Identity.Paired() {
		s.Log.Info("a Mac with no stored pairing asked to pair again; showing a code")
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

func (s *Server) dispatch(
	send func(byte, []byte) error,
	clip *clipboardBridge,
	incoming *transfer.Receiver,
	frame proto.Frame,
) error {
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
		// Logged at debug because "the keyboard stopped working" is otherwise
		// impossible to tell apart from "the Mac stopped sending keys".
		s.Log.Debug("key", "scancode", fmt.Sprintf("0x%02x", k.Scancode),
			"down", k.Down, "extended", k.Extended())
		return s.Injector.Key(k.Scancode, k.Down, k.Extended())

	case proto.TypeKeyReset:
		return s.Injector.ReleaseAll()

	case proto.TypeLeave:
		// The Mac has let go of this PC, so this PC may capture for itself
		// again.
		s.remoteControl(false)
		return s.Injector.ReleaseAll()

	case proto.TypeEnter:
		m, err := proto.DecodeMouseMove(frame.Body)
		if err != nil {
			return err
		}
		s.remoteControl(true)
		return s.Injector.MoveTo(m.X, m.Y)

	case proto.TypeClientInfo:
		info, err := proto.DecodeClientInfo(frame.Body)
		if err != nil {
			return err
		}
		s.Log.Info("the Mac described itself",
			"desktop", fmt.Sprintf("%dx%d", info.Desktop.Width, info.Desktop.Height),
			"mac_beyond_edge", info.Edge, "reverse_control", info.ReverseControl())
		if s.OnClientInfo != nil {
			s.OnClientInfo(info)
		}
		return nil

	case proto.TypeClipboardText:
		if clip == nil {
			return nil
		}
		s.Log.Debug("clipboard from the Mac", "bytes", len(frame.Body))
		if err := clip.applyRemote(string(frame.Body)); err != nil {
			// A clipboard another app is holding is a transient annoyance, not
			// a reason to drop the user's session.
			s.Log.Debug("could not set the clipboard", "err", err)
		}
		return nil

	case proto.TypeFileBegin, proto.TypeFileChunk, proto.TypeFileEnd, proto.TypeFileAbort:
		// A rejected file is the sender's problem, not a reason to drop the
		// session: the user is still holding a mouse that has to keep working.
		if err := s.receiveFile(incoming, clip, frame); err != nil {
			s.Log.Warn("file transfer failed", "err", err)
			return send(proto.TypeFileAbort, []byte(err.Error()))
		}
		return nil

	case proto.TypePing:
		return send(proto.TypePong, frame.Body)

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

// receiveFile applies one step of a transfer from the Mac.
func (s *Server) receiveFile(incoming *transfer.Receiver, clip *clipboardBridge, frame proto.Frame) error {
	if incoming == nil {
		return errors.New("this PC is not accepting files")
	}
	switch frame.Type {
	case proto.TypeFileBegin:
		begin, err := proto.DecodeFileBegin(frame.Body)
		if err != nil {
			return err
		}
		s.Log.Info("receiving a file", "name", begin.Name, "bytes", begin.Size)
		return incoming.Begin(begin)

	case proto.TypeFileChunk:
		return incoming.Chunk(frame.Body)

	case proto.TypeFileEnd:
		path, err := incoming.End()
		if err != nil {
			return err
		}
		s.Log.Info("file received", "path", path)
		if s.OnFileReceived != nil {
			s.OnFileReceived(path, 0, 0)
		}
		// A copy becomes a paste: put the whole batch on the clipboard once
		// the last file has landed.
		if paths, done := incoming.Batch(); done {
			if files, ok := s.Clipboard.(clipboard.Files); ok {
				if err := files.SetPaths(paths); err != nil {
					s.Log.Warn("could not put the files on the clipboard", "err", err)
				} else {
					s.Log.Info("files ready to paste", "count", len(paths))
					if clip != nil {
						clip.notePastedFiles(paths)
					}
				}
			}
		}
		return nil

	default: // TypeFileAbort
		incoming.Abort()
		s.Log.Info("the Mac cancelled a file transfer")
		return nil
	}
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
