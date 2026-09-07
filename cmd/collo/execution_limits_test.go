package main

import "testing"

func TestExecutionLimitFlagsAndWebForwarding(t *testing.T) {
	for _, args := range [][]string{{"--max-turns", "500", "--max-no-progress=30"}, {"run", "--max-turns=500", "--max-no-progress", "30", "task"}} {
		opts, err := parse(args)
		if err != nil || opts.maxTurns != 500 || opts.maxIterations != 30 {
			t.Fatalf("args=%v opts=%+v err=%v", args, opts, err)
		}
		child, err := parse(tuiChildArgs(opts))
		if err != nil || child.maxTurns != 500 || child.maxIterations != 30 {
			t.Fatalf("web child lost limits: %+v err=%v", child, err)
		}
	}
	for _, value := range []string{"0", "-1", "10001", "abc"} {
		if _, err := parse([]string{"--max-turns", value}); err == nil {
			t.Fatalf("invalid limit %q accepted", value)
		}
	}
	if _, err := parse([]string{"--max-no-progress"}); err == nil {
		t.Fatal("missing limit accepted")
	}
}
