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

// Files is a clipboard that can also hold a list of file paths, CF_HDROP on
// Windows, file URLs on macOS. Copying files in a file manager puts them here,
// and that is the only way to get at a set of files the user has chosen
// without watching a drag, which no application is allowed to do from outside
// the one that started it.
type Files interface {
	Clipboard
	// Paths returns the files currently on the clipboard, or nil.
	Paths() ([]string, error)
	// SetPaths puts files on the clipboard, so the next paste produces them.
	SetPaths([]string) error
}

// MaxClipboardFiles bounds a single copy. Selecting a whole folder and
// pressing Ctrl+C should fail immediately rather than quietly starting a
// thousand transfers.
const MaxClipboardFiles = 64
