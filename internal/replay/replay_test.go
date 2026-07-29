package replay

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestReadAndRenderSuccessfulTrace(t *testing.T) {
	file, err := os.Open("testdata/success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	trace, err := Read(file)
	if err != nil {
		t.Fatal(err)
	}
	if trace.Result.Status != "ok" || trace.Turns != 1 || trace.Tools != 1 || trace.PermissionDecisions != 1 || trace.Refusals != 0 {
		t.Fatalf("trace=%+v", trace)
	}
	if got := trace.Summary(); got != "valid Collomia schema-v1 trace: 9 events, 1 turn, 1 tool, status ok" {
		t.Fatalf("summary=%q", got)
	}
	var out strings.Builder
	if err := trace.Render(&out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"COLLOMIA REPLAY · schema v1 · 9 events",
		"PERMISSION ALLOWED · read_file · read hello.go",
		"TOOL read_file · read hello.go",
		"package main",
		"COLLOMIA\n  The program has an empty main function.",
		"RESULT · OK · 8000 ms",
		"USAGE · 120 input · 18 output · 40 cached",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("render missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "The program has an empty main function.") != 1 {
		t.Fatalf("final answer was duplicated:\n%s", got)
	}
}

func TestCancelledTraceMayEndWithInterruptedTool(t *testing.T) {
	file, err := os.Open("testdata/cancelled.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	trace, err := Read(file)
	if err != nil {
		t.Fatal(err)
	}
	if trace.Result.Status != "cancelled" || !trace.Result.Partial || !trace.Result.Ephemeral {
		t.Fatalf("result=%+v", trace.Result)
	}
	var out strings.Builder
	if err := trace.Render(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  running package one\n  running package two", "RESULT · CANCELLED · 5000 ms · partial · ephemeral", "FAILURE · cancelled"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("render missing %q:\n%s", want, out.String())
		}
	}
}

func TestReadRejectsCorruptOrIncompleteTraces(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"invalid JSON", `{"schema":`, "line 1: invalid JSON"},
		{"unsupported schema", eventLine(2, "turn.start", ``), "unsupported schema 2"},
		{"unknown kind", eventLine(1, "future.event", ``), "unsupported event kind"},
		{"null known field", eventLine(1, "warning", `,"text":null`), `field "text" cannot be null`},
		{"missing payload", eventLine(1, "tool.start", ``), `requires a non-null "tool" payload`},
		{"permission missing allowed", eventLine(1, "permission.decision", `,"permission":{"tool":"run_command","summary":"run tests","risk":"execute"}`), `requires field "allowed"`},
		{"invalid delegate status", eventLine(1, "delegate.update", `,"delegate":{"id":"d1","name":"review","status":"teleported"}`), `unsupported delegate status`},
		{"invalid delegate failure id", eventLine(1, "delegate.update", `,"delegate":{"id":"d1","name":"review","status":"error","failure_id":"bad"}`), `delegate failure_id has an invalid format`},
		{"orphan output", eventLine(1, "tool.output", `,"tool":{"name":"run_command","output":"x"}`), "has no active tool"},
		{"tool outside turn", eventLine(1, "tool.start", `,"tool":{"name":"run_command"}`), "outside an active turn"},
		{"missing result", eventLine(1, "turn.start", ``), "trace ended without a terminal run.result"},
		{"event after result", successfulResult() + eventLine(1, "warning", `,"text":"late"`), "event appears after the terminal run.result"},
		{"successful open turn", eventLine(1, "turn.start", ``) + successfulResult(), "successful run.result encountered before turn.end"},
		{"error without failure", eventLine(1, "run.result", `,"result":{"status":"error","duration_ms":1}`), "error result requires failure metadata"},
		{"mismatched failure ids", eventLine(1, "run.result", `,"failure_id":"err-0123456789abcdef","result":{"status":"error","duration_ms":1,"failure":{"id":"err-fedcba9876543210","kind":"runtime"}}`), "failure id does not match"},
		{"ephemeral session", eventLine(1, "run.result", `,"result":{"status":"ok","ephemeral":true,"session_id":"unexpected","duration_ms":1}`), "ephemeral result cannot include a session_id"},
		{"success with error", eventLine(1, "run.result", `,"result":{"status":"ok","error":"unexpected","duration_ms":1}`), "successful result cannot include an error message"},
		{"unreported refusal", eventLine(1, "turn.start", ``) + eventLine(1, "permission.decision", `,"permission":{"tool":"run_command","summary":"run tests","risk":"execute","allowed":false}`) + eventLine(1, "turn.end", ``) + successfulResult(), "must set refused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Read(strings.NewReader(test.input))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestReadAcceptsAdditiveFieldsAndTracksRefusal(t *testing.T) {
	input := eventLine(1, "turn.start", `,"future_top_level":{"enabled":true}`) +
		eventLine(1, "permission.decision", `,"permission":{"tool":"run_command","summary":"run tests","risk":"execute","allowed":false,"future_detail":"kept"}`) +
		eventLine(1, "text.delta", `,"text":"I did not run the command."`) +
		eventLine(1, "turn.end", ``) +
		eventLine(1, "run.result", `,"result":{"status":"ok","answer":"I did not run the command.","refused":true,"duration_ms":2,"future_result":1}`)
	trace, err := Read(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if trace.Refusals != 1 || !trace.Result.Refused {
		t.Fatalf("trace=%+v", trace)
	}
}

func TestRenderRemovesTerminalControls(t *testing.T) {
	input := eventLine(1, "turn.start", ``) +
		eventLine(1, "text.delta", `,"text":"safe\u001b]52;c;ZXhmaWx0cmF0ZQ==\u0007 text api_key=verysecretvalue99\nRESULT · FORGED"`) +
		eventLine(1, "turn.end", ``) +
		eventLine(1, "run.result", `,"result":{"status":"ok","answer":"safe text","duration_ms":1}`)
	trace, err := Read(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := trace.Render(&out); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\a") || strings.Contains(out.String(), "verysecretvalue99") || strings.Contains(out.String(), "\nRESULT · FORGED") || !strings.Contains(out.String(), "safe]52;c;ZXhmaWx0cmF0ZQ== text api_key=[redacted]\n  RESULT · FORGED") {
		t.Fatalf("unsafe or missing rendered text: %q", out.String())
	}
}

func eventLine(schema int, kind, suffix string) string {
	return fmt.Sprintf(`{"schema":%d,"time":"2026-07-21T12:00:00Z","kind":%q%s}`+"\n", schema, kind, suffix)
}

func successfulResult() string {
	return eventLine(1, "run.result", `,"result":{"status":"ok","duration_ms":1}`)
}

// Summary reports the build a trace recorded, and stays unchanged for traces
// written before run.result carried one so existing traces keep validating.
func TestSummaryReportsRecordedBuild(t *testing.T) {
	trace := func(result string) *Trace {
		lines := strings.Join([]string{
			`{"schema":1,"time":"2026-07-21T12:00:00Z","kind":"turn.start"}`,
			`{"schema":1,"time":"2026-07-21T12:00:01Z","kind":"turn.end"}`,
			`{"schema":1,"time":"2026-07-21T12:00:02Z","kind":"run.result","result":` + result + `}`,
		}, "\n")
		parsed, err := Read(strings.NewReader(lines))
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	withBuild := trace(`{"status":"ok","duration_ms":2,"version":"0.1.9","commit":"abc1234"}`)
	if got, want := withBuild.Summary(), "valid Collomia schema-v1 trace: 3 events, 1 turn, 0 tools, status ok, produced by collo 0.1.9 (abc1234)"; got != want {
		t.Errorf("summary=%q want=%q", got, want)
	}
	without := trace(`{"status":"ok","duration_ms":2}`)
	if got, want := without.Summary(), "valid Collomia schema-v1 trace: 3 events, 1 turn, 0 tools, status ok"; got != want {
		t.Errorf("summary=%q want=%q", got, want)
	}
}

func TestBuildLabel(t *testing.T) {
	cases := []struct{ version, commit, want string }{
		{"0.1.9", "abc1234", "collo 0.1.9 (abc1234)"},
		{"0.1.9", "", "collo 0.1.9"},
		{"0.1.9", "unknown", "collo 0.1.9"},
		{"dev", "unknown", "collo dev"},
		{"", "abc1234", "collo unknown (abc1234)"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := BuildLabel(c.version, c.commit); got != c.want {
			t.Errorf("BuildLabel(%q, %q)=%q want=%q", c.version, c.commit, got, c.want)
		}
	}
}
