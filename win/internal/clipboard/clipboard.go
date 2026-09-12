// Package clipboard reads and writes the system clipboard.
package clipboard

// Clipboard is the text clipboard of the machine the agent runs on.
//
// Only text for now. Images and files are a different problem: they need a
// size budget and a transfer that can be interrupted, and pretending otherwise
// by stuffing a bitmap into the same message would work right up until someone
// copied a screenshot.
type Clipboard interface {
	// Text returns the current clipboard text, or "" when it holds something
	// that is not text.
	Text() (string, error)
	// SetText replaces the clipboard contents.
	SetText(string) error
	// Sequence changes whenever the clipboard does, so it can be polled
	// cheaply without reading the contents on every tick.
	Sequence() uint32
}

// MaxText bounds what is worth synchronising. A clipboard is not a file
// transfer, and a 10 MB paste has no business being mirrored across a link
// that exists to carry keystrokes.
const MaxText = 256 * 1024
