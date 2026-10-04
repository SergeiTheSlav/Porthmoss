// Package transfer receives files dragged from the other machine.
package transfer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/SergeiTheSlav/Porthmoss/win/internal/proto"
)

// Receiver writes incoming files into a directory, one at a time.
//
// Not safe for concurrent use: the protocol delivers one file at a time in
// order, and the session drives this from a single goroutine.
type Receiver struct {
	// Dir is where files land.
	Dir string

	file      *os.File
	path      string
	remaining uint64

	// batch collects the files of one copy, so they can be put on the
	// clipboard together once the last one has arrived. A paste that produced
	// files one at a time as they landed would be worse than useless.
	batch         []string
	batchTotal    int
	fromClipboard bool
}

// SafeName reduces a name chosen by the other machine to something that can
// only ever land inside the destination directory.
func SafeName(name string) (string, error) {
	// Both separators, because the sender may be either platform.
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	// Windows ignores trailing dots and spaces when opening a file, so "a.txt."
	// and "a.txt" are the same file to it but different strings to us.
	name = strings.TrimRight(name, ". ")

	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("unusable file name %q", name)
	}

	// Characters Windows forbids, plus anything unprintable.
	const forbidden = `<>:"/\|?*`
	for _, r := range name {
		if strings.ContainsRune(forbidden, r) || r < 0x20 || !unicode.IsPrint(r) {
			return "", fmt.Errorf("file name contains %q", r)
		}
	}

	// Reserved DOS device names, with or without an extension. Writing to one
	// of these opens a device rather than creating a file.
	stem := strings.ToUpper(name)
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	switch stem {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "", fmt.Errorf("%q is a reserved device name", name)
	}

	// 255 bytes is the usual per-component limit; leave room for " (2)".
	if len(name) > 240 {
		return "", fmt.Errorf("file name is too long (%d bytes)", len(name))
	}
	return name, nil
}

// uniquePath avoids overwriting whatever is already there, the way a browser
// download does.
func uniquePath(dir, name string) (string, error) {
	candidate := filepath.Join(dir, name)
	if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate, nil
	}
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for n := 2; n < 1000; n++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, extension))
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("too many files named %q", name)
}

// Begin opens the next file.
func (r *Receiver) Begin(begin proto.FileBegin) error {
	r.discard()

	if begin.Size > proto.MaxFileSize {
		return fmt.Errorf("%q is %d bytes, over the %d byte limit",
			begin.Name, begin.Size, proto.MaxFileSize)
	}
	name, err := SafeName(begin.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", r.Dir, err)
	}
	path, err := uniquePath(r.Dir, name)
	if err != nil {
		return err
	}
	// O_EXCL so a symlink planted at the destination cannot redirect the write.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	r.file, r.path, r.remaining = file, path, begin.Size
	if begin.Index == 0 {
		r.batch = r.batch[:0]
	}
	r.batchTotal = int(begin.Total)
	r.fromClipboard = begin.FromClipboard()
	return nil
}

// Batch returns the files of a completed clipboard copy, and whether the batch
// is now finished. Empty for a drag, which does not touch the clipboard.
func (r *Receiver) Batch() (paths []string, done bool) {
	if !r.fromClipboard {
		return nil, false
	}
	return r.batch, len(r.batch) > 0 && len(r.batch) >= r.batchTotal
}

// Chunk writes the next piece.
func (r *Receiver) Chunk(data []byte) error {
	if r.file == nil {
		return errors.New("file chunk arrived before the file began")
	}
	if uint64(len(data)) > r.remaining {
		r.discard()
		return errors.New("file is longer than it declared")
	}
	if _, err := r.file.Write(data); err != nil {
		r.discard()
		return err
	}
	r.remaining -= uint64(len(data))
	return nil
}

// End closes the file and returns where it landed.
func (r *Receiver) End() (string, error) {
	if r.file == nil {
		return "", errors.New("file ended before it began")
	}
	if r.remaining != 0 {
		r.discard()
		return "", io.ErrUnexpectedEOF
	}
	path := r.path
	err := r.file.Close()
	r.file, r.path = nil, ""
	if err != nil {
		return "", err
	}
	// Readable by the user now that it is complete.
	os.Chmod(path, 0o644)
	r.batch = append(r.batch, path)
	return path, nil
}

// Abort throws away a partial file.
func (r *Receiver) Abort() { r.discard() }

func (r *Receiver) discard() {
	if r.file == nil {
		return
	}
	path := r.path
	r.file.Close()
	os.Remove(path)
	r.file, r.path, r.remaining = nil, "", 0
}
