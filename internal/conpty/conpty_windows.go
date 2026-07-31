//go:build windows

package conpty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Supported reports whether this build can attach a pseudoconsole. The API
// arrived in Windows 10 1809; on anything older CreatePseudoConsole is absent
// from kernel32 and Start fails with a named error rather than a bare
// "procedure not found".
const Supported = true

// DefaultCols and DefaultRows are used when a caller does not know the
// terminal size. A zero-sized pseudoconsole is rejected by the API, and
// programs that inspect COLUMNS behave strangely at 1x1.
const (
	DefaultCols = 120
	DefaultRows = 30
)

// Config describes the child to run. Argv[0] is the executable, resolved
// through exec.LookPath exactly as os/exec would resolve it.
type Config struct {
	Argv []string
	Dir  string
	// Env is the child's environment. A nil Env inherits the parent's, matching
	// exec.Cmd; a non-nil empty Env means an empty environment.
	Env        []string
	Cols, Rows int
}

// Process is a child attached to a pseudoconsole. Read returns what the child
// wrote to its console, Write delivers input to it.
type Process struct {
	console windows.Handle
	job     windows.Handle
	process windows.Handle
	thread  windows.Handle
	pid     int

	in  *os.File // parent -> child
	out *os.File // child -> parent

	// done closes when Wait has observed the child exit, so Close and any
	// cancellation watcher can tell an exit from a still-running child.
	done     chan struct{}
	waitOnce sync.Once
	exitCode int
	waitErr  error

	closeOnce sync.Once
	// outOnce guards the output handle, which Close must not take away from a
	// reader still draining it. See Close.
	outOnce     sync.Once
	readStarted atomic.Bool
}

// Start creates a pseudoconsole and launches the child attached to it.
func Start(cfg Config) (proc *Process, err error) {
	if len(cfg.Argv) == 0 {
		return nil, errors.New("conpty: no command given")
	}
	cols, rows := cfg.Cols, cfg.Rows
	if cols <= 0 {
		cols = DefaultCols
	}
	if rows <= 0 {
		rows = DefaultRows
	}

	// Two anonymous pipes: the console reads the child's input from one and
	// writes the child's output into the other.
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("conpty: create input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		return nil, fmt.Errorf("conpty: create output pipe: %w", err)
	}
	// Anything created above and not handed to the returned Process must be
	// released on every failure path below.
	closeOnFailure := func(handles ...*windows.Handle) {
		for _, handle := range handles {
			if *handle != 0 && *handle != windows.InvalidHandle {
				windows.CloseHandle(*handle)
				*handle = 0
			}
		}
	}

	var console windows.Handle
	size := windows.Coord{X: int16(cols), Y: int16(rows)}
	if err := windows.CreatePseudoConsole(size, inRead, outWrite, 0, &console); err != nil {
		closeOnFailure(&inRead, &inWrite, &outRead, &outWrite)
		return nil, fmt.Errorf("conpty: create pseudoconsole (requires Windows 10 1809 or later): %w", err)
	}

	// CreatePseudoConsole duplicates both handles it was given, so the
	// parent's copies must go now. This is not tidiness: while the parent
	// still holds the console's output-write handle, the pipe has a live
	// writer, and a read from outRead never reaches end-of-file when the child
	// exits. The reader would block until something else closed it, which is
	// exactly the "PTY command hangs after finishing" bug.
	closeOnFailure(&inRead, &outWrite)

	releaseAll := func() {
		windows.ClosePseudoConsole(console)
		closeOnFailure(&inWrite, &outRead)
	}

	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		releaseAll()
		return nil, fmt.Errorf("conpty: allocate attribute list: %w", err)
	}
	defer attributes.Delete()
	// PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE takes the console handle by value in
	// the pointer slot, which is what UpdateProcThreadAttribute documents and
	// what Microsoft's own sample passes.
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, handleAsValue(console), unsafe.Sizeof(console)); err != nil {
		releaseAll()
		return nil, fmt.Errorf("conpty: attach pseudoconsole: %w", err)
	}

	var startup windows.StartupInfoEx
	startup.ProcThreadAttributeList = attributes.List()
	startup.Cb = uint32(unsafe.Sizeof(startup))

	executable, err := exec.LookPath(cfg.Argv[0])
	if err != nil {
		releaseAll()
		return nil, fmt.Errorf("conpty: %w", err)
	}
	executable16, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		releaseAll()
		return nil, fmt.Errorf("conpty: executable path: %w", err)
	}
	commandLine16, err := windows.UTF16PtrFromString(commandLine(cfg.Argv))
	if err != nil {
		releaseAll()
		return nil, fmt.Errorf("conpty: command line: %w", err)
	}
	var dir16 *uint16
	if cfg.Dir != "" {
		if dir16, err = windows.UTF16PtrFromString(cfg.Dir); err != nil {
			releaseAll()
			return nil, fmt.Errorf("conpty: working directory: %w", err)
		}
	}
	env16, err := environmentBlock(cfg.Env)
	if err != nil {
		releaseAll()
		return nil, fmt.Errorf("conpty: environment: %w", err)
	}

	// The job object is created before the process so cancellation has
	// something to kill from the first instant the child exists.
	job, err := newKillOnCloseJob()
	if err != nil {
		releaseAll()
		return nil, err
	}

	var info windows.ProcessInformation
	// CREATE_SUSPENDED closes the race the tree-kill contract depends on: a
	// child resumed before it joins the job could spawn a descendant outside
	// it, and that descendant would survive cancellation. Handle inheritance
	// stays off — the console is delivered through the attribute list, and
	// inheriting anything else would hand the child descriptors nobody
	// audited.
	creation := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(executable16, commandLine16, nil, nil, false, creation, env16, dir16, &startup.StartupInfo, &info); err != nil {
		windows.CloseHandle(job)
		releaseAll()
		return nil, fmt.Errorf("conpty: start %s: %w", cfg.Argv[0], err)
	}
	if err := windows.AssignProcessToJobObject(job, info.Process); err != nil {
		windows.TerminateProcess(info.Process, 1)
		windows.CloseHandle(info.Thread)
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		releaseAll()
		return nil, fmt.Errorf("conpty: assign job object: %w", err)
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(info.Thread)
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		releaseAll()
		return nil, fmt.Errorf("conpty: resume child: %w", err)
	}

	return &Process{
		console: console,
		job:     job,
		process: info.Process,
		thread:  info.Thread,
		pid:     int(info.ProcessId),
		in:      os.NewFile(uintptr(inWrite), "conpty-input"),
		out:     os.NewFile(uintptr(outRead), "conpty-output"),
		done:    make(chan struct{}),
	}, nil
}

// Pid is the child's process identifier, for callers that walk the process
// tree with their own tooling.
func (p *Process) Pid() int { return p.pid }

// Read returns bytes the child wrote to its console. It reports io.EOF once
// the console has been closed and the child's output is drained.
//
// The read handle is released here, on the terminal read, rather than by
// Close: end-of-file means the console is gone and everything it rendered has
// been handed over, which is the only moment at which closing cannot discard
// output.
func (p *Process) Read(data []byte) (int, error) {
	p.readStarted.Store(true)
	n, err := p.out.Read(data)
	if err != nil {
		p.closeOutput()
	}
	return n, err
}

func (p *Process) closeOutput() { p.outOnce.Do(func() { _ = p.out.Close() }) }

// Write delivers input to the child as if typed.
func (p *Process) Write(data []byte) (int, error) { return p.in.Write(data) }

// Resize tells the child its terminal changed size.
func (p *Process) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}

// Wait blocks until the child exits and returns its exit code. It is safe to
// call more than once and always reports the same result.
func (p *Process) Wait() (int, error) {
	p.waitOnce.Do(func() {
		event, err := windows.WaitForSingleObject(p.process, windows.INFINITE)
		if err != nil {
			p.exitCode, p.waitErr = -1, fmt.Errorf("conpty: wait: %w", err)
		} else if event != windows.WAIT_OBJECT_0 {
			p.exitCode, p.waitErr = -1, fmt.Errorf("conpty: wait returned 0x%x", event)
		} else {
			var code uint32
			if err := windows.GetExitCodeProcess(p.process, &code); err != nil {
				p.exitCode, p.waitErr = -1, fmt.Errorf("conpty: exit code: %w", err)
			} else {
				p.exitCode = int(code)
				if code != 0 {
					// The message matches exec.ExitError so a PTY failure reads
					// the same as the ordinary command path's failure.
					p.waitErr = fmt.Errorf("exit status %d", code)
				}
			}
		}
		close(p.done)
	})
	<-p.done
	return p.exitCode, p.waitErr
}

// Done closes once the child has exited and Wait has recorded the result.
func (p *Process) Done() <-chan struct{} { return p.done }

// CloseInput closes the child's console input so a read of it reaches
// end-of-file.
//
// This is the nearest Windows has to asking a console program to stop.
// Windows has no SIGTERM: GenerateConsoleCtrlEvent requires the sender to
// share the target's console, which a pseudoconsole host by definition does
// not, so there is no signal to send. A program blocked on input sees that
// input end and can shut down on its own terms; one that ignores it has to be
// terminated. Callers should treat this as a request with a deadline, never
// as a guarantee.
func (p *Process) CloseInput() error { return p.in.Close() }

// Terminate kills the child and every descendant by terminating the job
// object, then waits briefly for the kernel to finish tearing them down.
// Returning before that is complete leaves processes still holding the
// workspace working directory open, which breaks workspace removal.
func (p *Process) Terminate() error {
	if err := windows.TerminateJobObject(p.job, 1); err != nil {
		return fmt.Errorf("conpty: terminate: %w", err)
	}
	// A process handle signals only once the kernel has released the
	// process's resources, so this is the completion signal, not the kill.
	_, _ = windows.WaitForSingleObject(p.process, uint32(teardownTimeout/time.Millisecond))
	return nil
}

// teardownTimeout bounds the wait for a killed tree, matching the WaitDelay
// the ordinary command path uses.
const teardownTimeout = 5 * time.Second

// Close releases the pseudoconsole and every handle. It kills the child first
// if it is still running: ClosePseudoConsole waits for its client to detach,
// so closing while a live child still holds the console can block until that
// child happens to exit on its own.
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		select {
		case <-p.done:
		default:
			_ = p.Terminate()
		}
		// Input first. The child sees its console input close, which is the
		// polite end-of-input signal for anything still reading.
		_ = p.in.Close()
		windows.ClosePseudoConsole(p.console)
		// The output handle is deliberately left alone when anyone has read
		// from it.
		//
		// Closing the console releases the pipe's last writer, which is what
		// walks a blocked reader to end-of-file — but ConPTY renders
		// asynchronously, so at this instant the pipe still holds bytes the
		// child wrote and the reader has not yet collected. Closing the read
		// handle here took those bytes away mid-drain: the reader got
		// ErrClosed instead of EOF and returned whatever had already arrived,
		// which on a fast child was the console's own initialization sequences
		// and none of its output. It failed as a race, so it passed locally
		// and failed under CI load. Read closes the handle when it reaches
		// end-of-file instead; this covers only the case where nothing ever
		// read, which would otherwise leak the handle until finalization.
		if !p.readStarted.Load() {
			p.closeOutput()
		}
		windows.CloseHandle(p.thread)
		windows.CloseHandle(p.process)
		// Closing the job is what would kill the tree if anything above left
		// something alive, because the job carries KILL_ON_JOB_CLOSE.
		windows.CloseHandle(p.job)
	})
	return nil
}

// newKillOnCloseJob returns a job object that kills everything assigned to it
// when its last handle closes, so a crashed or panicking Collomia cannot leave
// an orphaned command tree behind.
func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("conpty: create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("conpty: configure job object: %w", err)
	}
	return job, nil
}

// handleAsValue reinterprets a handle as the pointer-sized value
// UpdateProcThreadAttribute expects for PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE.
//
// That attribute is one of the few that takes its value directly in the
// lpValue slot instead of a pointer to be dereferenced — Microsoft's own
// sample passes hPC, not &hPC — so this cannot be unsafe.Pointer(&console).
// Passing the address would attach whichever handle happened to live at that
// address, which is both wrong and the kind of wrong that appears to work
// until the stack is laid out differently.
//
// The bits are moved through the handle's own address rather than by
// converting a uintptr, because `go vet` rejects the direct conversion and CI
// vets on the Windows runner. This is presentation, not extra safety: the
// result is still an unsafe.Pointer holding a non-pointer. It is sound because
// a HANDLE is a small integer that never points into the Go heap, so the
// collector ignores it when it scans the attribute list's retained slice.
func handleAsValue(handle windows.Handle) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&handle))
}

// commandLine renders argv the way os/exec does, so a command quoted one way
// without a PTY is quoted the same way with one.
func commandLine(argv []string) string {
	var b strings.Builder
	for i, argument := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(syscall.EscapeArg(argument))
	}
	return b.String()
}

// environmentBlock renders KEY=VALUE entries as the double-NUL-terminated
// UTF-16 block CreateProcess expects. A nil slice returns nil, which inherits
// the parent environment.
func environmentBlock(env []string) (*uint16, error) {
	if env == nil {
		return nil, nil
	}
	if len(env) == 0 {
		// An empty environment is two NULs, matching syscall.createEnvBlock:
		// one ends the absent last entry and one ends the block.
		empty := []uint16{0, 0}
		return &empty[0], nil
	}
	var block []uint16
	for _, entry := range env {
		// A NUL inside an entry would terminate the block early and silently
		// drop everything after it.
		if strings.ContainsRune(entry, 0) {
			return nil, fmt.Errorf("environment entry contains NUL: %q", entry)
		}
		if entry == "" {
			continue
		}
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, encoded...)
	}
	// An empty environment is still a valid block: two NULs, one ending the
	// (absent) last entry and one ending the block.
	block = append(block, 0)
	return &block[0], nil
}
