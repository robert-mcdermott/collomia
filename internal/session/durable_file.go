package session

import (
	"io"
	"os"
)

// durableFile is the smallest file surface needed by immutable session blobs.
// Keeping the opener per manager gives tests deterministic write/sync/close
// failure seams without mutable process-global hooks.
type durableFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type durableFileOpener func(string, int, os.FileMode) (durableFile, error)

func writeExclusiveDurable(path string, data []byte, mode os.FileMode, opener durableFileOpener) error {
	if opener == nil {
		opener = func(path string, flag int, mode os.FileMode) (durableFile, error) {
			return os.OpenFile(path, flag, mode)
		}
	}
	file, err := opener(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = os.Remove(path)
		}
	}()
	for len(data) > 0 {
		written, writeErr := file.Write(data)
		if writeErr != nil {
			_ = file.Close()
			return writeErr
		}
		if written <= 0 || written > len(data) {
			_ = file.Close()
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	accepted = true
	return nil
}
