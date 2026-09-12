package clipboard

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")

	procDragQueryFileW = shell32.NewProc("DragQueryFileW")
)

const (
	cfHDrop = 15

	// DROPFILES, the header CF_HDROP data starts with:
	//   DWORD pFiles; POINT pt; BOOL fNC; BOOL fWide;
	// 20 bytes on every architecture — the fields are all fixed width.
	dropFilesHeaderSize = 20
)

// Paths returns the files on the clipboard, if it holds any.
func (Windows) Paths() ([]string, error) {
	if available, _, _ := procIsClipboardFormat.Call(cfHDrop); available == 0 {
		return nil, nil
	}
	if err := open(); err != nil {
		return nil, err
	}
	defer procCloseClipboard.Call()

	handle, _, err := procGetClipboardData.Call(cfHDrop)
	if handle == 0 {
		return nil, fmt.Errorf("reading clipboard files: %w", err)
	}

	// DragQueryFileW understands the DROPFILES layout, so we do not have to.
	count, _, _ := procDragQueryFileW.Call(handle, 0xFFFFFFFF, 0, 0)
	if count == 0 {
		return nil, nil
	}
	if int(count) > MaxClipboardFiles {
		return nil, fmt.Errorf("%d files on the clipboard, over the limit of %d",
			count, MaxClipboardFiles)
	}

	paths := make([]string, 0, count)
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	for i := range count {
		n, _, _ := procDragQueryFileW.Call(
			handle, i, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)),
		)
		if n == 0 {
			continue
		}
		paths = append(paths, windows.UTF16ToString(buffer[:n]))
	}
	return paths, nil
}

// SetPaths puts files on the clipboard, so the next paste produces them.
func (Windows) SetPaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	// CF_HDROP is a DROPFILES header followed by a double-null-terminated list
	// of null-terminated wide strings.
	var names []uint16
	for _, path := range paths {
		encoded, err := windows.UTF16FromString(path)
		if err != nil {
			return fmt.Errorf("path is not valid UTF-16: %w", err)
		}
		names = append(names, encoded...) // UTF16FromString already null-terminates
	}
	names = append(names, 0) // the extra null that ends the list

	size := uintptr(dropFilesHeaderSize + len(names)*2)
	handle, _, allocErr := procGlobalAlloc.Call(gmemMoveable, size)
	if handle == 0 {
		return fmt.Errorf("allocating clipboard memory: %w", allocErr)
	}
	pointer, _, lockErr := procGlobalLock.Call(handle)
	if pointer == 0 {
		procGlobalFree.Call(handle)
		return fmt.Errorf("locking clipboard memory: %w", lockErr)
	}

	block := unsafe.Slice(at[byte](pointer), size)
	for i := range block {
		block[i] = 0
	}
	// pFiles: where the names start. fWide: they are UTF-16.
	block[0] = dropFilesHeaderSize
	block[16] = 1
	copy(unsafe.Slice(at[uint16](pointer+dropFilesHeaderSize), len(names)), names)
	procGlobalUnlock.Call(handle)

	if err := open(); err != nil {
		procGlobalFree.Call(handle)
		return err
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	if ret, _, setErr := procSetClipboardData.Call(cfHDrop, handle); ret == 0 {
		procGlobalFree.Call(handle)
		return fmt.Errorf("setting clipboard files: %w", setErr)
	}
	return nil
}
