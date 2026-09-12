package server

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/janjamscikov/porthmoss/win/internal/clipboard"
	"github.com/janjamscikov/porthmoss/win/internal/proto"
)

type sentFrames struct {
	mu   sync.Mutex
	text []string
}

func (s *sentFrames) send(typ byte, body []byte) error {
	if typ == proto.TypeClipboardText {
		s.mu.Lock()
		s.text = append(s.text, string(body))
		s.mu.Unlock()
	}
	return nil
}

func (s *sentFrames) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.text...)
}

// waitForSends blocks until the bridge has sent count frames, or gives up.
func waitForSends(t *testing.T, sent *sentFrames, count int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := sent.all(); len(got) >= count {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sent.all()
}

func TestClipboardSharesLocalChanges(t *testing.T) {
	clip := clipboard.New()
	bridge := newClipboardBridge(clip, slog.New(slog.DiscardHandler))
	sent := &sentFrames{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.watch(ctx, sent.send)

	if err := clip.SetText("copied on the PC"); err != nil {
		t.Fatalf("SetText: %v", err)
	}
	got := waitForSends(t, sent, 1)
	if len(got) != 1 || got[0] != "copied on the PC" {
		t.Fatalf("sent %q, want one frame with the copied text", got)
	}
}

// TestClipboardDoesNotEchoRemoteText guards the failure that makes clipboard
// sharing unusable rather than merely broken: text arriving from the Mac is
// written locally, the watcher sees the change, sends it straight back, and
// the two machines bounce it between them forever.
func TestClipboardDoesNotEchoRemoteText(t *testing.T) {
	clip := clipboard.New()
	bridge := newClipboardBridge(clip, slog.New(slog.DiscardHandler))
	sent := &sentFrames{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.watch(ctx, sent.send)

	if err := bridge.applyRemote("copied on the Mac"); err != nil {
		t.Fatalf("applyRemote: %v", err)
	}
	// Long enough for several poll ticks to have run.
	time.Sleep(clipboardPollInterval * 3)

	if got := sent.all(); len(got) != 0 {
		t.Errorf("echoed the Mac's own text back to it: %q", got)
	}
	if text, _ := clip.Text(); text != "copied on the Mac" {
		t.Errorf("local clipboard = %q, want the text from the Mac", text)
	}

	// And a genuine local copy afterwards must still get through.
	if err := clip.SetText("copied on the PC after"); err != nil {
		t.Fatalf("SetText: %v", err)
	}
	got := waitForSends(t, sent, 1)
	if len(got) != 1 || got[0] != "copied on the PC after" {
		t.Fatalf("sent %q, want the later local copy", got)
	}
}

func TestClipboardIgnoresNonText(t *testing.T) {
	clip := clipboard.New()
	bridge := newClipboardBridge(clip, slog.New(slog.DiscardHandler))
	sent := &sentFrames{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.watch(ctx, sent.send)

	// An empty read is how "the clipboard holds an image" arrives, and it must
	// not wipe the other machine's clipboard.
	if err := clip.SetText(""); err != nil {
		t.Fatalf("SetText: %v", err)
	}
	time.Sleep(clipboardPollInterval * 3)
	if got := sent.all(); len(got) != 0 {
		t.Errorf("sent %q for a non-text clipboard", got)
	}
}
