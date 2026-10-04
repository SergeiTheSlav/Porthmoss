package transfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SergeiTheSlav/Porthmoss/win/internal/proto"
)

// TestSafeNameCannotEscape states the property that actually matters: whatever
// the other machine sends, the result either fails or is a single path
// component that lands inside the download directory.
//
// Rejecting is not the only safe answer, and mostly not the right one, a name
// with a directory in it is reduced to its last component, which is what a
// browser does with Content-Disposition. What must never happen is a result
// that still contains a separator, or that resolves upwards.
func TestSafeNameCannotEscape(t *testing.T) {
	hostile := []string{
		"../../.ssh/authorized_keys",
		`..\..\Windows\System32\drivers\etc\hosts`,
		"/etc/passwd",
		`C:\Windows\System32\calc.exe`,
		"....//....//etc/passwd",
		"foo/../../bar",
		`\server\shareile.txt`}
	for _, name := range hostile {
		got, err := SafeName(name)
		if err != nil {
			continue // refusing outright is also safe
		}
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("SafeName(%q) = %q, which still contains a separator", name, got)
		}
		if got == "." || got == ".." || got == "" {
			t.Errorf("SafeName(%q) = %q, which resolves outside the directory", name, got)
		}
		// The decisive check: joining it cannot leave the directory. Compared
		// with filepath rather than a literal prefix, because the separator is
		// "\" on the machine this actually has to defend.
		const dir = "/downloads"
		joined := filepath.Join(dir, got)
		if filepath.Dir(joined) != filepath.Clean(dir) {
			t.Errorf("SafeName(%q) = %q, which joins to %q, outside %q",
				name, got, joined, filepath.Clean(dir))
		}
	}
}

// Names that cannot be made into a usable file at all.
func TestSafeNameRejectsUnusableNames(t *testing.T) {
	for _, name := range []string{
		"..", ".", "", "   ",
		"CON", "con.txt", "LPT1", "NUL.dat", "aux",
		"bad\x00name",
		"bell\x07name",
		"pipe|name",
		"question?name",
		strings.Repeat("a", 300),
	} {
		if got, err := SafeName(name); err == nil {
			t.Errorf("SafeName(%q) = %q, want an error", name, got)
		}
	}
}

func TestSafeNameKeepsOrdinaryNames(t *testing.T) {
	for _, name := range []string{
		"report.pdf",
		"Screenshot 2026-09-12 at 19.42.01.png",
		"σημειώσεις.txt",
		"a file with spaces.tar.gz",
		"emoji 🎉.txt",
	} {
		got, err := SafeName(name)
		if err != nil {
			t.Errorf("SafeName(%q) errored: %v", name, err)
			continue
		}
		if got != name {
			t.Errorf("SafeName(%q) = %q, want it unchanged", name, got)
		}
	}
}

// Trailing dots and spaces are normalised away rather than rejected. Windows
// ignores them when opening a file, so "report.txt." and "report.txt" are the
// same file to it; landing the file under the name the user expects is both
// safe and more useful than refusing it outright.
func TestSafeNameNormalisesTrailingPunctuation(t *testing.T) {
	for _, name := range []string{"report.txt.", "report.txt ", "report.txt. . "} {
		got, err := SafeName(name)
		if err != nil || got != "report.txt" {
			t.Errorf("SafeName(%q) = %q, %v; want report.txt", name, got, err)
		}
	}
}

func TestSafeNameStripsDirectories(t *testing.T) {
	// A name with a directory in it is not rejected outright, only the last
	// component is kept, which is what a browser does with Content-Disposition.
	for name, want := range map[string]string{
		"folder/report.pdf": "report.pdf",
		`folder\report.pdf`: "report.pdf",
		"a/b/c/deep.txt":    "deep.txt",
	} {
		got, err := SafeName(name)
		if err != nil || got != want {
			t.Errorf("SafeName(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
}

func receive(t *testing.T, dir, name string, content []byte) (string, error) {
	t.Helper()
	r := &Receiver{Dir: dir}
	if err := r.Begin(proto.FileBegin{Name: name, Size: uint64(len(content)), Total: 1}); err != nil {
		return "", err
	}
	if err := r.Chunk(content); err != nil {
		return "", err
	}
	return r.End()
}

func TestReceiveWritesTheFile(t *testing.T) {
	dir := t.TempDir()
	path, err := receive(t, dir, "notes.txt", []byte("hello from the Mac"))
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("wrote to %s, outside %s", path, dir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if string(got) != "hello from the Mac" {
		t.Errorf("contents = %q", got)
	}
}

func TestReceiveDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	first, err := receive(t, dir, "notes.txt", []byte("original"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := receive(t, dir, "notes.txt", []byte("replacement"))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first == second {
		t.Fatal("the second file overwrote the first")
	}
	if got, _ := os.ReadFile(first); string(got) != "original" {
		t.Errorf("the original was modified: %q", got)
	}
	if filepath.Base(second) != "notes (2).txt" {
		t.Errorf("second file is %q, want \"notes (2).txt\"", filepath.Base(second))
	}
}

func TestReceiveRejectsAnOverlongFile(t *testing.T) {
	dir := t.TempDir()
	r := &Receiver{Dir: dir}
	if err := r.Begin(proto.FileBegin{Name: "x.bin", Size: 4, Total: 1}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := r.Chunk([]byte("more than four bytes")); err == nil {
		t.Fatal("a chunk past the declared size should be refused")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left %d file(s) behind after the failure", len(entries))
	}
}

func TestReceiveDiscardsATruncatedFile(t *testing.T) {
	dir := t.TempDir()
	r := &Receiver{Dir: dir}
	if err := r.Begin(proto.FileBegin{Name: "x.bin", Size: 100, Total: 1}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := r.Chunk([]byte("only a little")); err != nil {
		t.Fatalf("chunk: %v", err)
	}
	if _, err := r.End(); err == nil {
		t.Fatal("ending short of the declared size should be refused")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left a half-written file behind")
	}
}

func TestReceiveRejectsAnEnormousFile(t *testing.T) {
	r := &Receiver{Dir: t.TempDir()}
	err := r.Begin(proto.FileBegin{Name: "huge.bin", Size: proto.MaxFileSize + 1, Total: 1})
	if err == nil {
		t.Fatal("a file over the size limit should be refused before any of it arrives")
	}
}
