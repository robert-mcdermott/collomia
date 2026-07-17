package main

import "testing"

func TestParseFlagsBeforeSubcommandAndTerminator(t *testing.T) {
	opts, err := parse([]string{"--cwd", "/tmp/work", "run", "--autopilot", "--", "prompt", "-with-dash"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "run" || opts.cwd != "/tmp/work" || opts.autonomy != "autopilot" {
		t.Fatalf("options=%+v", opts)
	}
	if len(opts.args) != 2 || opts.args[1] != "-with-dash" {
		t.Fatalf("args=%v", opts.args)
	}
}
