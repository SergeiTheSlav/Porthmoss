package clipboard

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard        = user32.NewProc("OpenClipboard")
	procCloseClipboard       = user32.NewProc("CloseClipboard")
	procEmptyClipboard       = user32.NewProc("EmptyClipboard")
	procGetClipboardData     = user32.NewProc("GetClipboardData")
	procSetClipboardData     = user32.NewProc("SetClipboardData")
	procIsClipboardFormat    = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardSequence = user32.NewProc("GetClipboardSequenceNumber")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// at converts an address returned by a Win32 call into a pointer.
//
// go vet's unsafeptr check flags this, and is right to in the general case: a
// uintptr is not a reference the garbage collector can see, so heap memory
// could move out from under it. It is safe here because the memory came from
// GlobalAlloc and lives outside the Go heap entirely — there is nothing for
// the collector to move or reclaim. Isolated in one place so the exception is
// visible rather than scattered.
func at[T any](address uintptr) *T {
	return (*T)(unsafe.Pointer(address)) //nolint:govet
}

// Windows is the system clipboard.
type Windows struct{}

func New() *Windows { return &Windows{} }

func (Windows) Sequence() uint32 {
	n, _, _ := procGetClipboardSequence.Call()
	return uint32(n)
}

// open takes the clipboard, retrying briefly. Exactly one process may hold it
// at a time, and something else having it for a moment is normal rather than
// an error worth reporting.
func open() error {
	var err error
	for range 10 {
		ret, _, callErr := procOpenClipboard.Call(0)
		if ret != 0 {
			return nil
		}
		err = callErr
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("clipboard busy: %w", err)
}

func (Windows) Text() (string, error) {
	if available, _, _ := procIsClipboardFormat.Call(cfUnicodeText); available == 0 {
		return "", nil // something that is not text; not an error
	}
	if err := open(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()

	handle, _, err := procGetClipboardData.Call(cfUnicodeText)
	if handle == 0 {
		return "", fmt.Errorf("reading clipboard: %w", err)
	}
	pointer, _, err := procGlobalLock.Call(handle)
	if pointer == 0 {
		return "", fmt.Errorf("locking clipboard memory: %w", err)
	}
	defer procGlobalUnlock.Call(handle)

	text := windows.UTF16PtrToString(at[uint16](pointer))
	if len(text) > MaxText {
		return "", errors.New("clipboard contents too large to share")
	}
	return text, nil
}

func (Windows) SetText(text string) error {
	encoded, err := windows.UTF16FromString(text)
	if err != nil {
		return fmt.Errorf("clipboard text is not valid UTF-16: %w", err)
	}
	size := uintptr(len(encoded) * 2)

	// The clipboard takes ownership of this on success, so it must be moveable
	// global memory and must not be freed afterwards.
	handle, _, allocErr := procGlobalAlloc.Call(gmemMoveable, size)
	if handle == 0 {
		return fmt.Errorf("allocating clipboard memory: %w", allocErr)
	}
	pointer, _, lockErr := procGlobalLock.Call(handle)
	if pointer == 0 {
		procGlobalFree.Call(handle)
		return fmt.Errorf("locking clipboard memory: %w", lockErr)
	}
	copy(unsafe.Slice(at[uint16](pointer), len(encoded)), encoded)
	procGlobalUnlock.Call(handle)

	if err := open(); err != nil {
		procGlobalFree.Call(handle)
		return err
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	if ret, _, setErr := procSetClipboardData.Call(cfUnicodeText, handle); ret == 0 {
		procGlobalFree.Call(handle)
		return fmt.Errorf("setting clipboard: %w", setErr)
	}
	return nil
}
