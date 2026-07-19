package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook test scripts use /bin/sh")
	}
}

func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func runner(t *testing.T, event string, hook appconfig.Hook, notes *[]Note) *Runner {
	t.Helper()
	r := NewRunner(t.TempDir(), map[string][]appconfig.Hook{event: {hook}}, func(n Note) {
		if notes != nil {
			*notes = append(*notes, n)
		}
	})
	if r == nil {
		t.Fatal("runner should exist when hooks are configured")
	}
	return r
}

func TestNilRunnerIsSafe(t *testing.T) {
	var r *Runner
	r.Fire(t.Context(), Payload{Event: "stop"})
	if err := r.Gate(t.Context(), Payload{Event: "tool_start"}); err != nil {
		t.Fatal(err)
	}
	if NewRunner("", nil, nil) != nil {
		t.Fatal("no hooks should mean a nil runner")
	}
}

func TestGateBlocksOnExitCode2(t *testing.T) {
	requireUnix(t)
	hook := appconfig.Hook{Command: script(t, `echo "dangerous path"; exit 2`)}
	r := runner(t, "tool_start", hook, nil)
	err := r.Gate(t.Context(), Payload{Event: "tool_start", Subject: "run_command", Tool: "run_command"})
	if err == nil || !strings.Contains(err.Error(), "dangerous path") {
		t.Fatalf("expected block with reason, got %v", err)
	}
}

func TestGateBlocksOnJSONDecision(t *testing.T) {
	requireUnix(t)
	hook := appconfig.Hook{Command: script(t, `echo '{"decision":"block","reason":"policy says no"}'`)}
	r := runner(t, "user_prompt", hook, nil)
	err := r.Gate(t.Context(), Payload{Event: "user_prompt", Subject: "user_prompt"})
	if err == nil || !strings.Contains(err.Error(), "policy says no") {
		t.Fatalf("expected JSON block, got %v", err)
	}
}

func TestGateFailuresAreBoundedNotBlocking(t *testing.T) {
	requireUnix(t)
	var notes []Note
	hook := appconfig.Hook{Command: script(t, `echo "boom" >&2; exit 1`)}
	r := runner(t, "tool_start", hook, &notes)
	if err := r.Gate(t.Context(), Payload{Event: "tool_start", Subject: "read_file"}); err != nil {
		t.Fatalf("exit 1 must not block, got %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "boom") {
		t.Fatalf("failure should surface as a note, got %v", notes)
	}
}

func TestMatcherFiltersBySubject(t *testing.T) {
	requireUnix(t)
	marker := filepath.Join(t.TempDir(), "ran")
	hook := appconfig.Hook{Command: script(t, "touch "+marker), Matcher: "^write_file$"}
	r := runner(t, "tool_start", hook, nil)
	if err := r.Gate(t.Context(), Payload{Event: "tool_start", Subject: "read_file"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("hook ran despite non-matching subject")
	}
	if err := r.Gate(t.Context(), Payload{Event: "tool_start", Subject: "write_file"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("hook did not run for matching subject")
	}
}

func TestPayloadDeliveredOnStdin(t *testing.T) {
	requireUnix(t)
	out := filepath.Join(t.TempDir(), "payload.json")
	hook := appconfig.Hook{Command: script(t, "cat > "+out)}
	r := runner(t, "file_change", hook, nil)
	r.Fire(t.Context(), Payload{Event: "file_change", Subject: "write_file", Tool: "write_file", Paths: []string{"/tmp/x.go"}})
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"event":"file_change"`, `"tool":"write_file"`, `"/tmp/x.go"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("payload missing %s: %s", want, data)
		}
	}
}

func TestTimeoutIsBounded(t *testing.T) {
	requireUnix(t)
	var notes []Note
	hook := appconfig.Hook{Command: script(t, "sleep 30"), TimeoutSeconds: 1}
	r := runner(t, "stop", hook, &notes)
	r.Fire(t.Context(), Payload{Event: "stop", Subject: "stop"})
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "timed out") {
		t.Fatalf("expected timeout note, got %v", notes)
	}
}

func TestHookOutputIsCapped(t *testing.T) {
	requireUnix(t)
	hook := appconfig.Hook{Command: script(t, `yes x | head -c 100000; exit 2`)}
	r := runner(t, "tool_start", hook, nil)
	err := r.Gate(t.Context(), Payload{Event: "tool_start", Subject: "run_command"})
	if err == nil {
		t.Fatal("expected block")
	}
	if len(err.Error()) > maxHookOutput+64 {
		t.Fatalf("block reason not capped: %d bytes", len(err.Error()))
	}
}

func TestHookValidation(t *testing.T) {
	cfg := appconfig.Config{
		Providers:       map[string]appconfig.Provider{"p": {Type: "openai-compatible", BaseURL: "http://localhost", Model: "m"}},
		DefaultProvider: "p",
		Permissions:     appconfig.Permissions{Mode: "ask"},
		Hooks: map[string][]appconfig.Hook{
			"no_such_event": {{Command: "x"}},
			"tool_start":    {{Command: "", TimeoutSeconds: -1, Matcher: "("}},
		},
	}
	errs := cfg.ValidateFields()
	var fields []string
	for _, fe := range errs {
		fields = append(fields, fe.Field)
	}
	joined := strings.Join(fields, " ")
	for _, want := range []string{"hooks.no_such_event", "hooks.tool_start[0].command", "hooks.tool_start[0].timeout_seconds", "hooks.tool_start[0].matcher"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing validation for %s in %v", want, fields)
		}
	}
}
