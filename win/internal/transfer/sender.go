package transfer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/janjamscikov/porthmoss/win/internal/proto"
)

// Send streams files to the other machine as BEGIN, CHUNK…, END.
//
// Read in chunks rather than whole: a 500 MB file read into memory to send it
// is a needless spike on a machine that is also running the user's actual work.
func Send(paths []string, flags byte, send func(byte, []byte) error) error {
	if len(paths) == 0 {
		return nil
	}
	for index, path := range paths {
		if err := sendOne(path, index, len(paths), flags, send); err != nil {
			// Tell the other end so it discards the partial file, then stop:
			// the rest of the batch would land without its siblings anyway.
			send(proto.TypeFileAbort, []byte(err.Error()))
			return err
		}
	}
	return nil
}

func sendOne(path string, index, total int, flags byte, send func(byte, []byte) error) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	if info.IsDir() {
		// Folders would need the tree walked and the structure carried in the
		// protocol. Refusing clearly beats sending something surprising.
		return fmt.Errorf("%s is a folder; only files can be copied across",
			filepath.Base(path))
	}
	size := uint64(info.Size())
	if size > proto.MaxFileSize {
		return fmt.Errorf("%s is %d bytes, over the %d byte limit",
			filepath.Base(path), size, proto.MaxFileSize)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Base(path), err)
	}
	defer file.Close()

	begin := proto.FileBegin{
		Name:  filepath.Base(path),
		Size:  size,
		Index: uint16(index),
		Total: uint16(total),
		Flags: flags,
	}
	if err := send(proto.TypeFileBegin, begin.Encode()); err != nil {
		return err
	}

	buffer := make([]byte, proto.FileChunkSize)
	var sent uint64
	for sent < size {
		n, err := file.Read(buffer)
		if n > 0 {
			// Never send more than was declared, even if the file grew while
			// it was being read.
			if sent+uint64(n) > size {
				n = int(size - sent)
			}
			if err := send(proto.TypeFileChunk, buffer[:n]); err != nil {
				return err
			}
			sent += uint64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
		}
	}
	if sent != size {
		return fmt.Errorf("%s changed size while it was being sent", filepath.Base(path))
	}
	return send(proto.TypeFileEnd, nil)
}
