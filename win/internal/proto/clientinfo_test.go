package proto

import "testing"

func TestClientInfoRoundTrip(t *testing.T) {
	want := ClientInfo{
		Desktop: Monitor{Left: -243, Top: -1440, Width: 3440, Height: 2552},
		Edge:    "bottom",
		Flags:   ClientInfoFlagReverseControl,
	}
	got, err := DecodeClientInfo(want.Encode())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Desktop != want.Desktop {
		t.Errorf("desktop = %+v, want %+v", got.Desktop, want.Desktop)
	}
	if got.Edge != want.Edge {
		t.Errorf("edge = %q, want %q", got.Edge, want.Edge)
	}
	if !got.ReverseControl() {
		t.Error("reverse control flag did not survive")
	}
}

func TestClientInfoWithReverseControlOff(t *testing.T) {
	info := ClientInfo{Desktop: Monitor{Width: 1710, Height: 1112}, Edge: "left"}
	got, err := DecodeClientInfo(info.Encode())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ReverseControl() {
		t.Error("reverse control should be off when the flag is clear")
	}
}

func TestClientInfoRejectsTruncation(t *testing.T) {
	full := ClientInfo{Desktop: Monitor{Width: 100, Height: 100}, Edge: "top"}.Encode()
	for n := range full {
		if _, err := DecodeClientInfo(full[:n]); err == nil {
			t.Errorf("decoding %d of %d bytes should have failed", n, len(full))
		}
	}
}
