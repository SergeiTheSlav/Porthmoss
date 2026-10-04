package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SergeiTheSlav/Porthmoss/win/internal/clipboard"
	"github.com/SergeiTheSlav/Porthmoss/win/internal/proto"
)

// clipboardPollInterval is how often the PC's clipboard is checked. Windows
// offers a change notification, but it needs a window with a message loop, and
// the tray already owns one thread for that. Polling a sequence number is a
// single cheap call and costs nothing next to being wrong about which thread
// owns what.
const clipboardPollInterval = 400 * time.Millisecond

// clipboardBridge keeps the PC's clipboard and the Mac's in step.
//
// The hard part is not copying text, it is not echoing: writing what the Mac
// sent changes the local clipboard, the poller notices, and without care it
// sends it straight back and the two bounce it between them.
//
// There are two guards against that, and either alone is enough, the test
// only fails with both removed. Both are kept deliberately. Re-reading the
// sequence number after a write is the cheap one, but on Windows the sequence
// number moves for reasons that are not ours and the read-back can race a
// third application; remembering the text itself catches what slips past.
type clipboardBridge struct {
	clip clipboard.Clipboard
	log  *slog.Logger

	mu        sync.Mutex
	lastText  string
	lastPaths []string
	lastSeq   uint32
}

func samePaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func newClipboardBridge(clip clipboard.Clipboard, log *slog.Logger) *clipboardBridge {
	return &clipboardBridge{clip: clip, log: log, lastSeq: clip.Sequence()}
}

// notePastedFiles records files this session just put on the clipboard, so the
// watcher does not read them straight back and send them to the Mac again.
func (b *clipboardBridge) notePastedFiles(paths []string) {
	b.mu.Lock()
	b.lastPaths = append([]string(nil), paths...)
	b.mu.Unlock()
	b.mu.Lock()
	b.lastSeq = b.clip.Sequence()
	b.mu.Unlock()
}

// applyRemote puts text from the Mac on this PC's clipboard.
func (b *clipboardBridge) applyRemote(text string) error {
	b.mu.Lock()
	b.lastText = text
	b.mu.Unlock()

	if err := b.clip.SetText(text); err != nil {
		return err
	}
	// Take the sequence number after writing, so our own write is not mistaken
	// for the user copying something.
	b.mu.Lock()
	b.lastSeq = b.clip.Sequence()
	b.mu.Unlock()
	return nil
}

// watch sends local clipboard changes to the Mac until ctx is done.
func (b *clipboardBridge) watch(
	ctx context.Context,
	send func(byte, []byte) error,
	sendFiles func([]string) error,
) {
	ticker := time.NewTicker(clipboardPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		sequence := b.clip.Sequence()
		b.mu.Lock()
		unchanged := sequence == b.lastSeq
		b.mu.Unlock()
		if unchanged {
			continue
		}

		// Files first: copying files in Explorer also leaves a text form on
		// the clipboard, and sending that instead would be the wrong thing.
		if files, ok := b.clip.(clipboard.Files); ok {
			paths, err := files.Paths()
			if err != nil {
				b.log.Debug("clipboard files unreadable", "err", err)
			}
			if len(paths) > 0 {
				b.mu.Lock()
				b.lastSeq = sequence
				echo := samePaths(paths, b.lastPaths)
				if !echo {
					b.lastPaths = append([]string(nil), paths...)
				}
				b.mu.Unlock()
				if !echo && sendFiles != nil {
					if err := sendFiles(paths); err != nil {
						return
					}
				}
				continue
			}
		}

		text, err := b.clip.Text()
		b.mu.Lock()
		b.lastSeq = sequence
		// The second of the two guards; see the type comment.
		echo := text == b.lastText
		if !echo {
			b.lastText = text
		}
		b.mu.Unlock()

		if err != nil {
			b.log.Debug("clipboard unreadable", "err", err)
			continue
		}
		// Empty means the clipboard holds something that is not text, which is
		// not a reason to wipe the Mac's.
		if text == "" || echo {
			continue
		}
		if len(text) > clipboard.MaxText {
			b.log.Debug("clipboard too large to share", "bytes", len(text))
			continue
		}
		if err := send(proto.TypeClipboardText, []byte(text)); err != nil {
			return // the session is over; the read loop will report why
		}
	}
}
