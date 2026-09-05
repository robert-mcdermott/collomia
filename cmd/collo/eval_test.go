package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/quality"
)

func TestEvalListAndExplicitOptIn(t *testing.T) {
	var out bytes.Buffer
	if err := evalCommand([]string{"list"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "work_totals") || !strings.Contains(out.String(), "code_boundary") {
		t.Fatal(out.String())
	}
	for _, args := range [][]string{{"run"}, {"run", "--provider", "fixture", "--output", filepath.Join(t.TempDir(), "run")}, {"run", "--live", "--provider", "fixture"}} {
		if err := evalCommand(args, &out, &out); err == nil {
			t.Fatalf("missing opt-in accepted: %v", args)
		}
	}
}

func TestEvalReviewIsExplicitAndBounded(t *testing.T) {
	directory := t.TempDir()
	r := quality.Result{Schema: 1, Suite: quality.SuiteVersion, State: "complete", Trials: []quality.Trial{{Task: "sample", Trial: 1, Questions: 1, RepeatedToolCalls: 2}}}
	if err := quality.Save(directory, r); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"review", directory, "--task", "sample", "--accepted", "true", "--note", "Inspected the output and traces", "--unnecessary-questions", "1"}
	if err := evalCommand(args, &out, &out); err != nil {
		t.Fatal(err)
	}
	loaded, err := quality.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Trials[0].Review == nil || !loaded.Trials[0].Review.Accepted {
		t.Fatal("review not saved")
	}
	args = append(args, "--wasted-repeats", "3")
	if err := evalCommand(args, &out, &out); err == nil {
		t.Fatal("review exceeds observations")
	}
}
