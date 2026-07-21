package main

import (
	"fmt"
	"io"
	"os"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/replay"
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
		return nil
	}
	return trace.Render(out)
}
