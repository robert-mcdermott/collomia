package main

import (
	"fmt"
	"io"
	"os"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/replay"
	"github.com/robert-mcdermott/collomia/internal/version"
)

func runReplayCommand(opts options) error {
	if len(opts.args) != 1 {
		return withCommandError(fmt.Errorf("replay requires exactly one JSONL trace path (or - for stdin)"), exitUsage, event.FailureUsage)
	}
	var (
		in    io.Reader = os.Stdin
		close func() error
	)
	if opts.args[0] != "-" {
		file, err := os.Open(opts.args[0])
		if err != nil {
			return fmt.Errorf("open replay trace: %w", err)
		}
		in = file
		close = file.Close
	}
	if close != nil {
		defer close()
	}
	if err := writeReplay(in, os.Stdout, opts.check); err != nil {
		return fmt.Errorf("replay %s: %w", opts.args[0], err)
	}
	return nil
}

func writeReplay(in io.Reader, out io.Writer, check bool) error {
	trace, err := replay.Read(in)
	if err != nil {
		return fmt.Errorf("invalid replay trace: %w", err)
	}
	if check {
		fmt.Fprintln(out, trace.Summary())
		fmt.Fprintln(out, buildProvenance(trace))
		return nil
	}
	return trace.Render(out)
}

// buildProvenance states how the trace's recorded build relates to this
// binary. It reports the fact and stops there rather than warning: prompts,
// tool descriptions, and agent logic are all compiled in, so a differing build
// may or may not have behaved differently on the same input, and only the
// reader knows whether the difference matters to what they are diagnosing.
func buildProvenance(trace *replay.Trace) string {
	self := replay.BuildLabel(version.Version, version.Commit)
	if trace.Result.Version == "" && trace.Result.Commit == "" {
		return "build: not recorded in this trace; this binary is " + self
	}
	recorded := replay.BuildLabel(trace.Result.Version, trace.Result.Commit)
	if trace.Result.Version == version.Version && trace.Result.Commit == version.Commit {
		return "build: produced by " + recorded + ", matching this binary"
	}
	return "build: produced by " + recorded + "; this binary is " + self
}
