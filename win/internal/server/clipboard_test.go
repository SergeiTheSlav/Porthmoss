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
	mu    sync.Mutex
	text  []string
	files [][]string
}

func (s *sentFrames) send(typ byte, body []byte) error {
	if typ == proto.TypeClipboardText {
		s.mu.Lock()
		s.text = append(s.text, string(body))
		s.mu.Unlock()
	}
	return nil
}

func (s *sentFrames) sendFiles(paths []string) error {
	s.mu.Lock()
	s.files = append(s.files, paths)
	s.mu.Unlock()
	return nil
}

func (s *sentFrames) allFiles() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.files...)
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
	go bridge.watch(ctx, sent.send, sent.sendFiles)

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
	go bridge.watch(ctx, sent.send, sent.sendFiles)

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
	go bridge.watch(ctx, sent.send, sent.sendFiles)

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

// TestClipboardSharesCopiedFiles covers copying files on the PC: the paths go
// to the Mac, which fetches them. Copying files in Explorer also leaves a text
// form on the clipboard, and sending that instead would be the wrong thing.
func TestClipboardSharesCopiedFiles(t *testing.T) {
	clip := clipboard.New()
	bridge := newClipboardBridge(clip, slog.New(slog.DiscardHandler))
	sent := &sentFrames{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.watch(ctx, sent.send, sent.sendFiles)

	if err := clip.SetPaths([]string{`C:\Users\jan\report.pdf`, `C:\Users\jan\notes.txt`}); err != nil {
		t.Fatalf("SetPaths: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(sent.allFiles()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	got := sent.allFiles()
	if len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("shared %v, want one batch of two files", got)
	}
	if text := sent.all(); len(text) != 0 {
		t.Errorf("also sent text %q for a file copy", text)
	}
}

// TestClipboardDoesNotEchoPastedFiles is the file-shaped version of the echo
// problem: files arriving from the Mac are put on the clipboard, the watcher
// sees the change, and without care sends them straight back.
func TestClipboardDoesNotEchoPastedFiles(t *testing.T) {
	clip := clipboard.New()
	bridge := newClipboardBridge(clip, slog.New(slog.DiscardHandler))
	sent := &sentFrames{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.watch(ctx, sent.send, sent.sendFiles)

	paths := []string{`C:\Users\jan\Downloads\Porthmoss\from-mac.txt`}
	if err := clip.SetPaths(paths); err != nil {
		t.Fatalf("SetPaths: %v", err)
	}
	bridge.notePastedFiles(paths)

	time.Sleep(clipboardPollInterval * 3)
	if got := sent.allFiles(); len(got) != 0 {
		t.Errorf("echoed the Mac's own files back to it: %v", got)
	}
}
