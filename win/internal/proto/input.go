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

// FileFlagClipboard marks a batch that came from the sender's clipboard
// rather than from a drag. The receiver still writes the files to disk — it
// has to put them somewhere — but also puts them on its own clipboard, so the
// user's next paste produces the files rather than nothing.
const FileFlagClipboard = 1 << 0

// FileBegin announces a file: its name, its size, and where it sits in the
// batch so the receiver can report "2 of 5" rather than counting.
type FileBegin struct {
	Name  string
	Size  uint64
	Index uint16
	Total uint16
	Flags byte
}

// FromClipboard reports whether this batch should land on the clipboard too.
func (f FileBegin) FromClipboard() bool { return f.Flags&FileFlagClipboard != 0 }

func (f FileBegin) Encode() []byte {
	name := []byte(f.Name)
	out := make([]byte, 0, 15+len(name))
	out = binary.BigEndian.AppendUint16(out, uint16(len(name)))
	out = append(out, name...)
	out = binary.BigEndian.AppendUint64(out, f.Size)
	out = binary.BigEndian.AppendUint16(out, f.Index)
	out = binary.BigEndian.AppendUint16(out, f.Total)
	out = append(out, f.Flags)
	return out
}

func DecodeFileBegin(b []byte) (FileBegin, error) {
	if len(b) < 2 {
		return FileBegin{}, io.ErrUnexpectedEOF
	}
	n := int(binary.BigEndian.Uint16(b))
	if len(b) < 2+n+12 {
		return FileBegin{}, io.ErrUnexpectedEOF
	}
	rest := b[2+n:]
	begin := FileBegin{
		Name:  string(b[2 : 2+n]),
		Size:  binary.BigEndian.Uint64(rest),
		Index: binary.BigEndian.Uint16(rest[8:]),
		Total: binary.BigEndian.Uint16(rest[10:]),
	}
	// The flag byte came after the first version of this message.
	if len(rest) > 12 {
		begin.Flags = rest[12]
	}
	return begin, nil
}

// ClientInfoFlagReverseControl says the Mac is willing to be driven by the PC.
// Reverse control is configured entirely from the Mac, because that is where
// the user set up which edge leads where; having to agree a layout separately
// on each machine is how you end up pushing an edge that nothing listens to.
const ClientInfoFlagReverseControl = 1 << 0

// ClientInfo is the Mac describing itself to the agent.
//
// Without it the agent has to guess: it assumed the Mac's desktop matched its
// own, and took the edge the Mac lies beyond from a command-line flag that
// defaulted to "left". A wrong guess there is silent — the crossing simply
// never fires, whichever edge you push.
type ClientInfo struct {
	// Desktop is the Mac's whole desktop, in the Mac's own pixels.
	Desktop Monitor
	// Edge is the edge of the *PC's* desktop that the Mac lies beyond, already
	// mirrored from the Mac's own setting. If the PC is to the Mac's right,
	// the Mac is to the PC's left.
	Edge  string
	Flags byte
}

// ReverseControl reports whether the Mac accepts being driven.
func (c ClientInfo) ReverseControl() bool { return c.Flags&ClientInfoFlagReverseControl != 0 }

func (c ClientInfo) Encode() []byte {
	edge := []byte(c.Edge)
	out := make([]byte, 0, 18+len(edge))
	out = appendI32(out, c.Desktop.Left, c.Desktop.Top, c.Desktop.Width, c.Desktop.Height)
	out = append(out, c.Flags, byte(len(edge)))
	out = append(out, edge...)
	return out
}

func DecodeClientInfo(b []byte) (ClientInfo, error) {
	if len(b) < 18 {
		return ClientInfo{}, io.ErrUnexpectedEOF
	}
	length := int(b[17])
	if len(b) < 18+length {
		return ClientInfo{}, io.ErrUnexpectedEOF
	}
	return ClientInfo{
		Desktop: Monitor{
			Left: i32(b[0:]), Top: i32(b[4:]),
			Width: i32(b[8:]), Height: i32(b[12:]),
		},
		Flags: b[16],
		Edge:  string(b[18 : 18+length]),
	}, nil
}
