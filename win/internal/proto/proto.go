// Package proto implements the Porthmoss v1 wire protocol (see docs/protocol.md).
package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	Version   = 1
	NonceSize = 32
	MACSize   = 32

	// MaxFrame is generous because of the clipboard. Input events are a few
	// bytes each; a pasted document is not, and chunking it would buy nothing
	// over a link that already carries the whole thing in one write.
	MaxFrame = 1 << 20
)

// Message types.
const (
	TypeHello     = 0x01
	TypeChallenge = 0x02
	TypeAuth      = 0x03
	TypeReady     = 0x04
	TypeError     = 0x05

	TypeMouseMove   = 0x10
	TypeMouseButton = 0x11
	TypeMouseWheel  = 0x12
	TypeKey         = 0x20
	TypeKeyReset    = 0x21

	TypeEnter = 0x30
	TypeLeave = 0x31
	TypePing  = 0x40
	TypePong  = 0x41

	// TypeClipboardText carries UTF-8 text in either direction.
	TypeClipboardText = 0x50
)

var ErrFrameTooLarge = errors.New("proto: frame exceeds maximum size")

// Frame is one decoded message: a type byte plus its raw body.
type Frame struct {
	Type byte
	Body []byte
}

// ReadFrame reads a single length-prefixed frame from r.
func ReadFrame(r io.Reader, buf []byte) (Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n < 1 || n > MaxFrame {
		return Frame{}, fmt.Errorf("%w: %d", ErrFrameTooLarge, n)
	}
	if uint32(cap(buf)) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	if _, err := io.ReadFull(r, buf); err != nil {
		return Frame{}, err
	}
	return Frame{Type: buf[0], Body: buf[1:]}, nil
}

// WriteFrame writes one message. body may be nil.
func WriteFrame(w io.Writer, typ byte, body []byte) error {
	n := 1 + len(body)
	if n > MaxFrame {
		return fmt.Errorf("%w: %d", ErrFrameTooLarge, n)
	}
	out := make([]byte, 4+n)
	binary.BigEndian.PutUint32(out[:4], uint32(n))
	out[4] = typ
	copy(out[5:], body)
	_, err := w.Write(out)
	return err
}

// Monitor describes one Windows display in virtual-desktop pixels.
type Monitor struct {
	Left, Top, Width, Height int32
	Primary                  bool
}

// ScreenInfo is the agent's display layout, sent in READY.
type ScreenInfo struct {
	Virtual  Monitor // Primary field unused
	Monitors []Monitor
}

func (s ScreenInfo) Encode() []byte {
	out := make([]byte, 0, 17+len(s.Monitors)*17)
	out = appendI32(out, s.Virtual.Left, s.Virtual.Top, s.Virtual.Width, s.Virtual.Height)
	out = append(out, byte(len(s.Monitors)))
	for _, m := range s.Monitors {
		out = appendI32(out, m.Left, m.Top, m.Width, m.Height)
		out = append(out, b2u(m.Primary))
	}
	return out
}

func DecodeScreenInfo(b []byte) (ScreenInfo, error) {
	if len(b) < 17 {
		return ScreenInfo{}, io.ErrUnexpectedEOF
	}
	var s ScreenInfo
	s.Virtual.Left, s.Virtual.Top = i32(b[0:]), i32(b[4:])
	s.Virtual.Width, s.Virtual.Height = i32(b[8:]), i32(b[12:])
	count := int(b[16])
	b = b[17:]
	if len(b) < count*17 {
		return ScreenInfo{}, io.ErrUnexpectedEOF
	}
	s.Monitors = make([]Monitor, count)
	for i := range s.Monitors {
		s.Monitors[i] = Monitor{
			Left: i32(b[0:]), Top: i32(b[4:]), Width: i32(b[8:]), Height: i32(b[12:]),
			Primary: b[16] == 1,
		}
		b = b[17:]
	}
	return s, nil
}

// HelloFlagNeedsPairing says the Mac holds no secret for this agent and is
// asking to pair again. An agent that is already paired would otherwise just
// reject it, leaving no way back except physically unpairing at the PC.
const HelloFlagNeedsPairing = 1 << 0

// Hello is the client's opening message.
type Hello struct {
	Version uint16
	Name    string
	Flags   byte
}

func (h Hello) Encode() []byte {
	out := make([]byte, 5+len(h.Name))
	binary.BigEndian.PutUint16(out[0:], h.Version)
	binary.BigEndian.PutUint16(out[2:], uint16(len(h.Name)))
	copy(out[4:], h.Name)
	out[4+len(h.Name)] = h.Flags
	return out
}

func DecodeHello(b []byte) (Hello, error) {
	if len(b) < 4 {
		return Hello{}, io.ErrUnexpectedEOF
	}
	n := int(binary.BigEndian.Uint16(b[2:]))
	if len(b) < 4+n {
		return Hello{}, io.ErrUnexpectedEOF
	}
	hello := Hello{Version: binary.BigEndian.Uint16(b), Name: string(b[4 : 4+n])}
	// The flag byte was added after the first release; a HELLO without it is
	// simply a Mac that is not asking to re-pair.
	if len(b) > 4+n {
		hello.Flags = b[4+n]
	}
	return hello, nil
}

// NeedsPairing reports whether this Mac is asking for a fresh pairing code.
func (h Hello) NeedsPairing() bool { return h.Flags&HelloFlagNeedsPairing != 0 }

func appendI32(dst []byte, vals ...int32) []byte {
	for _, v := range vals {
		dst = binary.BigEndian.AppendUint32(dst, uint32(v))
	}
	return dst
}

func i32(b []byte) int32 { return int32(binary.BigEndian.Uint32(b)) }

func b2u(v bool) byte {
	if v {
		return 1
	}
	return 0
}
