// Package conpty attaches a child process to a Windows pseudoconsole.
//
// It exists because two callers need the same primitive — `run_command` with
// `pty: true` and the browser terminal — and the part that is easy to get
// wrong is handle lifetime, not the API calls. Two copies would each have to
// rediscover that the parent's copy of the console's output handle must be
// closed before anyone reads, that a child must be created suspended so it
// cannot spawn a descendant before the job object exists, and that closing
// the pseudoconsole before killing the job can block. A single
// implementation is the point.
//
// The package builds to nothing on other platforms; each caller keeps its own
// build-tagged file and only the Windows one imports this.
//
// This is deliberately not layered on os/exec. syscall.SysProcAttr on Windows
// exposes no proc-thread attribute list, and a pseudoconsole is attached only
// through PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE on a STARTUPINFOEX, so the
// process must be created here. Command-line construction still matches
// os/exec exactly — the same LookPath resolution and the same
// syscall.EscapeArg quoting — because `pty: true` and `pty: false` quoting a
// command differently would be a far worse surprise than either quoting rule
// on its own.
package conpty
