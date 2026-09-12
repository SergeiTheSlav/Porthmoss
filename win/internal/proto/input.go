package proto

import (
	"encoding/binary"
	"io"
)

// Mouse button identifiers as they travel on the wire.
const (
	ButtonLeft   = 1
	ButtonRight  = 2
	ButtonMiddle = 3
	ButtonX1     = 4
	ButtonX2     = 5
)

// KeyFlagExtended marks scancodes that need the E0 prefix (arrows, right ctrl,
// numpad enter, and friends).
const KeyFlagExtended = 1 << 0

type MouseMove struct{ X, Y uint16 } // normalised 0..65535 over the virtual desktop

type MouseButton struct {
	Button byte
	Down   bool
}

type MouseWheel struct{ DX, DY int16 } // in WHEEL_DELTA (120) units

type Key struct {
	Scancode uint16
	Down     bool
	Flags    byte
}

func (m MouseMove) Encode() []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint16(out[0:], m.X)
	binary.BigEndian.PutUint16(out[2:], m.Y)
	return out
}

func DecodeMouseMove(b []byte) (MouseMove, error) {
	if len(b) < 4 {
		return MouseMove{}, io.ErrUnexpectedEOF
	}
	return MouseMove{X: binary.BigEndian.Uint16(b), Y: binary.BigEndian.Uint16(b[2:])}, nil
}

func (m MouseButton) Encode() []byte { return []byte{m.Button, b2u(m.Down)} }

func DecodeMouseButton(b []byte) (MouseButton, error) {
	if len(b) < 2 {
		return MouseButton{}, io.ErrUnexpectedEOF
	}
	return MouseButton{Button: b[0], Down: b[1] == 1}, nil
}

func (m MouseWheel) Encode() []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint16(out[0:], uint16(m.DX))
	binary.BigEndian.PutUint16(out[2:], uint16(m.DY))
	return out
}

func DecodeMouseWheel(b []byte) (MouseWheel, error) {
	if len(b) < 4 {
		return MouseWheel{}, io.ErrUnexpectedEOF
	}
	return MouseWheel{DX: int16(binary.BigEndian.Uint16(b)), DY: int16(binary.BigEndian.Uint16(b[2:]))}, nil
}

func (k Key) Encode() []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint16(out[0:], k.Scancode)
	out[2] = b2u(k.Down)
	out[3] = k.Flags
	return out
}

func DecodeKey(b []byte) (Key, error) {
	if len(b) < 4 {
		return Key{}, io.ErrUnexpectedEOF
	}
	return Key{Scancode: binary.BigEndian.Uint16(b), Down: b[2] == 1, Flags: b[3]}, nil
}

func (k Key) Extended() bool { return k.Flags&KeyFlagExtended != 0 }

// EncodeU64 / DecodeU64 carry PING and PONG ids.
func EncodeU64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

func DecodeU64(b []byte) (uint64, error) {
	if len(b) < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	return binary.BigEndian.Uint64(b), nil
}
