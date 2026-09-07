//go:build windows

package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// finalAppContainerPath asks the kernel for the target of an opened file or
// directory. EvalSymlinks can leave a hosted-toolcache alias unchanged; sandbox
// ACLs, PATH lookup and SDK self-discovery must all use the same final target.
// Opening with zero desired access queries identity without reading file data.
func finalAppContainerPath(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	for size := uint32(512); size <= 32768; {
		buffer := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], size, 0)
		if err != nil {
			return "", err
		}
		if n < size {
			final := windows.UTF16ToString(buffer[:n])
			if strings.HasPrefix(final, `\\?\UNC\`) {
				final = `\\` + strings.TrimPrefix(final, `\\?\UNC\`)
			} else {
				final = strings.TrimPrefix(final, `\\?\`)
			}
			if !filepath.IsAbs(final) {
				return "", fmt.Errorf("non-absolute final AppContainer path %q", final)
			}
			return filepath.Clean(final), nil
		}
		size = n + 1
	}
	return "", fmt.Errorf("final AppContainer path exceeds Windows path limit")
}
