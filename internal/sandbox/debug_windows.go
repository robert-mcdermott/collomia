//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32DLL            = windows.NewLazySystemDLL("kernel32.dll")
	procWaitForDebugEvent  = kernel32DLL.NewProc("WaitForDebugEvent")
	procContinueDebugEvent = kernel32DLL.NewProc("ContinueDebugEvent")
)

const (
	exceptionDebugEvent     = 1
	createProcessDebugEvent = 3
	exitProcessDebugEvent   = 5
	loadDLLDebugEvent       = 6

	dbgContinue            = 0x00010002
	dbgExceptionNotHandled = 0x80010001
	exceptionBreakpoint    = 0x80000003
)

// debugEventInfo reserves enough aligned storage for the largest DEBUG_EVENT
// union member on supported 64-bit Windows targets. Starting it with uintptr
// gives the union the native pointer alignment on both amd64 and arm64. The
// extra capacity is harmless because WaitForDebugEvent receives only a pointer.
type debugEventInfo struct {
	_    uintptr
	rest [152]byte
}

type debugEvent struct {
	Code      uint32
	ProcessID uint32
	ThreadID  uint32
	Info      debugEventInfo
}

type createProcessDebugInfo struct {
	File                windows.Handle
	Process             windows.Handle
	Thread              windows.Handle
	BaseOfImage         uintptr
	DebugInfoFileOffset uint32
	DebugInfoSize       uint32
	ThreadLocalBase     uintptr
	StartAddress        uintptr
	ImageName           uintptr
	Unicode             uint16
}

type exceptionDebugInfoPrefix struct {
	Code uint32
}

type exitProcessDebugInfo struct {
	ExitCode uint32
}

// brokerAppContainerDescendants handles only process-creation lifecycle
// events. It does not inspect or modify process memory, install breakpoints, or
// suppress application exceptions. Its sole purpose is to apply the private
// DOS device map to each new AppContainer descendant before that process
// executes.
func brokerAppContainerDescendants(rootProcessID uint32, rootProcess windows.Handle, nullDevice *appContainerNullDevice) (uint32, error) {
	awaitingInitialBreakpoint := make(map[uint32]bool)
	for {
		var event debugEvent
		if err := waitForDebugEvent(&event); err != nil {
			return 0, fmt.Errorf("wait for AppContainer descendant: %w", err)
		}

		continueStatus := uint32(dbgContinue)
		var eventErr error
		var rootExited bool
		var rootExitCode uint32

		switch event.Code {
		case createProcessDebugEvent:
			info := (*createProcessDebugInfo)(unsafe.Pointer(&event.Info))
			awaitingInitialBreakpoint[event.ProcessID] = true
			if info.Process == 0 {
				eventErr = errors.New("Windows reported an AppContainer descendant without a process handle")
			} else if err := nullDevice.Install(info.Process); err != nil {
				eventErr = fmt.Errorf("install device map for AppContainer descendant %d: %w", event.ProcessID, err)
			}
			// CREATE_PROCESS_DEBUG_INFO owns only the image-file handle. Windows
			// owns the process and thread handles until their exit event is
			// continued.
			if info.File != 0 && info.File != windows.InvalidHandle {
				_ = windows.CloseHandle(info.File)
			}
		case loadDLLDebugEvent:
			// LOAD_DLL_DEBUG_INFO begins with the image-file handle, which the
			// debugger is responsible for closing.
			file := *(*windows.Handle)(unsafe.Pointer(&event.Info))
			if file != 0 && file != windows.InvalidHandle {
				_ = windows.CloseHandle(file)
			}
		case exceptionDebugEvent:
			code := (*exceptionDebugInfoPrefix)(unsafe.Pointer(&event.Info)).Code
			if code == exceptionBreakpoint && awaitingInitialBreakpoint[event.ProcessID] {
				delete(awaitingInitialBreakpoint, event.ProcessID)
			} else {
				// Preserve normal process exception handling. The broker handles
				// only Windows' initial debugger breakpoint.
				continueStatus = dbgExceptionNotHandled
			}
		case exitProcessDebugEvent:
			delete(awaitingInitialBreakpoint, event.ProcessID)
			if event.ProcessID == rootProcessID {
				rootExited = true
				rootExitCode = (*exitProcessDebugInfo)(unsafe.Pointer(&event.Info)).ExitCode
			}
		}

		continueErr := continueDebugEvent(event.ProcessID, event.ThreadID, continueStatus)
		if eventErr != nil {
			_ = windows.TerminateProcess(rootProcess, 1)
			if continueErr != nil {
				return 0, errors.Join(eventErr, continueErr)
			}
			return 0, eventErr
		}
		if continueErr != nil {
			_ = windows.TerminateProcess(rootProcess, 1)
			return 0, continueErr
		}
		if rootExited {
			return rootExitCode, nil
		}
	}
}

func waitForDebugEvent(event *debugEvent) error {
	result, _, callErr := procWaitForDebugEvent.Call(
		uintptr(unsafe.Pointer(event)),
		uintptr(windows.INFINITE),
	)
	if result != 0 {
		return nil
	}
	return debugAPICallError("WaitForDebugEvent", callErr)
}

func continueDebugEvent(processID, threadID, status uint32) error {
	result, _, callErr := procContinueDebugEvent.Call(
		uintptr(processID),
		uintptr(threadID),
		uintptr(status),
	)
	if result != 0 {
		return nil
	}
	return debugAPICallError("ContinueDebugEvent", callErr)
}

func debugAPICallError(name string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return fmt.Errorf("%s failed", name)
	}
	return fmt.Errorf("%s: %w", name, err)
}
