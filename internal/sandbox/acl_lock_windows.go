//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

// ACL updates are read/merge/write operations, not atomic additions. Different
// workspace shims can grant access to the same SDK or temp directory, including
// overlapping parent/child paths with inherited ACEs. A process-local or per-path
// lock cannot prevent those operations from discarding each other's grants.
// Coordinate this user's shims across processes and Windows sessions, only for
// the permission update; sandboxed commands still execute concurrently.
func lockAppContainerACL() (func(), error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("identify AppContainer ACL owner: %w", err)
	}
	name, err := windows.UTF16PtrFromString(`Global\Collomia.AppContainerACL.` + user.User.Sid.String())
	if err != nil {
		return nil, err
	}
	mutex, err := windows.CreateMutex(nil, false, name)
	// x/sys returns ERROR_ALREADY_EXISTS alongside a valid opened handle.
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, fmt.Errorf("create AppContainer ACL mutex: %w", err)
	}
	// Windows mutex ownership belongs to an OS thread, not a Go goroutine.
	runtime.LockOSThread()
	status, err := windows.WaitForSingleObject(mutex, 30000)
	if err != nil || (status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED) {
		runtime.UnlockOSThread()
		_ = windows.CloseHandle(mutex)
		if err != nil {
			return nil, fmt.Errorf("wait for AppContainer ACL mutex: %w", err)
		}
		return nil, fmt.Errorf("wait for AppContainer ACL mutex: status 0x%x", status)
	}
	// An abandoned mutex is owned by us now. Re-read the actual DACL instead
	// of relying on any state from the interrupted writer.
	return func() {
		_ = windows.ReleaseMutex(mutex)
		_ = windows.CloseHandle(mutex)
		runtime.UnlockOSThread()
	}, nil
}
