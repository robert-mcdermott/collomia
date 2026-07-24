//go:build windows

package tools

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setProcessGroup gives the command its own process group and terminates the
// whole tree on cancellation via taskkill /T. Full job-object containment is
// tracked in the roadmap's phase 1 exit gate.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// TerminateProcess is asynchronous: taskkill returns before the
		// kernel finishes tearing each process down. Sandboxed descendants
		// (AppContainer debuggees launched by the shim) can therefore
		// briefly outlive cancellation while still holding their workspace
		// working directory open, which breaks workspace removal at
		// shutdown. Snapshot the descendant tree before the kill, then wait
		// on each process handle — a handle only signals once the kernel has
		// released the process's resources.
		descendants := openDescendants(uint32(cmd.Process.Pid))
		defer func() {
			waitAllExited(descendants, 5*time.Second)
			for _, handle := range descendants {
				_ = windows.CloseHandle(handle)
			}
		}()
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
}

// openDescendants returns SYNCHRONIZE handles for every live descendant of
// the given process so callers can wait for their complete termination.
func openDescendants(root uint32) []windows.Handle {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)
	children := map[uint32][]uint32{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for iterErr := windows.Process32First(snapshot, &entry); iterErr == nil; iterErr = windows.Process32Next(snapshot, &entry) {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
	}
	var handles []windows.Handle
	visited := map[uint32]bool{root: true}
	queue := []uint32{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			// Stale parent PIDs from reused identifiers can create cycles in
			// the snapshot; visited keeps the walk finite.
			if visited[child] {
				continue
			}
			visited[child] = true
			queue = append(queue, child)
			if handle, openErr := windows.OpenProcess(windows.SYNCHRONIZE, false, child); openErr == nil {
				handles = append(handles, handle)
			}
		}
	}
	return handles
}

// waitAllExited waits, under one shared deadline, for every process handle to
// signal. The bound keeps a stuck teardown from hanging cancellation.
func waitAllExited(handles []windows.Handle, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for _, handle := range handles {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		_, _ = windows.WaitForSingleObject(handle, uint32(remaining/time.Millisecond))
	}
}
