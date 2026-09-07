package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
)

func TestVerificationSuggestionPreservesExecutionContext(t *testing.T) {
	workspace := t.TempDir()
	for _, dir := range []string{"frontend", "with spaces", "frontend/nested"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ command, want string }{
		{`cd frontend && npm run build --cache "$PWD/.npm-cache" --no-fund --no-audit 2>&1 | tail -15`, `cd frontend && npm run build --cache "$PWD/.npm-cache" --no-fund --no-audit`},
		{`export CHECK="two  words | literal" && cd 'with spaces' && npm run build | tail -5`, `export CHECK="two  words | literal" && cd 'with spaces' && npm run build`},
		{`cd frontend && cd nested && npm test | tail -1`, `cd frontend && cd nested && npm test`},
		{`npm test || true`, `npm test`},
		{`export FOO=bar; npm test`, ``},
		{`cd /tmp && npm test | tail -1`, ``},
		{`npm install && npm test | tail -1`, ``},
		{`FOO=bar npm install && npm test | tail -1`, ``},
		{`export CDPATH=/tmp && cd frontend && npm test | tail -1`, ``},
		{`cd "$UNKNOWN" && npm test | tail -1`, ``},
		{`cd frontend && cd ../.. && npm test | tail -1`, ``},
	} {
		if got := verificationChainSuggestion(tc.command, workspace); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.command, got, tc.want)
		}
		if tc.want != "" {
			if _, refusal := scopedVerificationChain(tc.want, workspace); refusal != "" {
				t.Errorf("suggested check refused: %s", refusal)
			}
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(workspace, "outside")); err == nil {
		for _, command := range []string{"cd outside && npm test", "cd frontend && cd ../outside && npm test"} {
			if isVerificationCommand(command, workspace) {
				t.Errorf("outside symlink accepted: %s", command)
			}
			if _, refusal := scopedVerificationChain(command, workspace); refusal == "" {
				t.Errorf("outside scope accepted: %s", command)
			}
		}
	}
}

// The original command succeeded but its pipe masked the verifier's status.
// Execute the actual runtime suggestion against a native npm fixture, proving
// both the frontend cwd and $PWD/quoted environment survive the correction.
func TestKanban26VerificationSuggestionRunsInFrontend(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		t.Run(string(mode), func(t *testing.T) {
			a, c, dir := scopedFixture(t, mode)
			frontend := filepath.Join(dir, "frontend")
			if err := os.MkdirAll(frontend, 0700); err != nil {
				t.Fatal(err)
			}
			putScopedFile(t, frontend, "package.json", "{}")
			fixture := "#!/bin/sh\n[ -f package.json ] || { echo ENOENT; exit 254; }\n[ \"$CHECK\" = 'two  words' ] || exit 2\n[ \"$4\" = \"$PWD/.npm-cache\" ] || exit 3\necho BUILD_PASSED\n"
			if err := os.WriteFile(filepath.Join(dir, "npm"), []byte(fixture), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			original := `export CHECK="two  words" && cd frontend && npm run build --cache "$PWD/.npm-cache" 2>&1 | tail -15`
			assessment := assessVerificationCommand(original, dir)
			if assessment.Recognized || assessment.Suggestion == "" {
				t.Fatalf("assessment=%+v", assessment)
			}
			r, o := runScopedTool(t, a, c, "original", "run_command", taskArgs(map[string]any{"command": original}))
			if o.Failed || !strings.Contains(r.Content, "BUILD_PASSED") {
				t.Fatalf("original=%+v %+v", r, o)
			}
			r, o = runScopedTool(t, a, c, "direct", "run_command", taskArgs(map[string]any{"command": assessment.Suggestion, "verification": map[string]any{"paths": []string{"frontend"}, "purpose": "Build frontend"}}))
			if o.Failed || r.Evidence == nil || !strings.Contains(r.Content, "BUILD_PASSED") || c.pending != nil {
				t.Fatalf("direct=%+v %+v", r, o)
			}
			if d := c.assess(); !d.done {
				t.Fatalf("corrected verification did not complete: %+v", d)
			}
			if !isVerificationCommand(assessment.Suggestion, dir) {
				t.Fatal("graph recognizer rejected corrected command")
			}
		})
	}
}

func TestKanban26HighExitAllowsCorrectedCommandAndResume(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		for _, code := range []int{200, 254, 255} {
			t.Run(fmt.Sprintf("%s/%d", mode, code), func(t *testing.T) {
				a, c, dir := scopedFixture(t, mode)
				_, o := runScopedTool(t, a, c, "wrong-directory", "run_command", taskArgs(map[string]any{"command": fmt.Sprintf("exit %d", code)}))
				if !o.Failed || !o.CommandExited || c.pending != nil || len(c.failures) != 1 {
					t.Fatalf("failure classification=%+v %+v", o, c.recoveryState())
				}
				next := newCompletionController(c.board, dir, false, mode)
				if err := next.restoreCompletion(c.store); err != nil {
					t.Fatal(err)
				}
				_, o = runScopedTool(t, a, next, "repair", "write_file", `{"path":"repair.txt","content":"repaired"}`)
				if o.Failed || next.pending != nil || len(next.failures) != 1 {
					t.Fatal("repair blocked or hid failed operation")
				}
				_, o = runScopedTool(t, a, next, "corrected", "run_command", `{"command":"test -s repair.txt","verification":{"paths":["repair.txt"],"purpose":"Check repaired content"}}`)
				if o.Failed || next.pending != nil {
					t.Fatal("corrected command fenced")
				}
				// A changed command needs the agent's explicit semantic link to
				// the successful receipt; unrelated passes must not erase it.
				if err := next.board.Set(plan.Plan{Goal: "repair", Steps: []plan.Step{{ID: 1, Title: "Repair and check", Status: "done", Evidence: "Corrected check passed"}}, ResolvedFailures: []plan.FailureResolution{{FailureID: "wrong-directory", StepID: 1, Disposition: "recovered_by_alternative", RecoveryToolCallID: "corrected", Evidence: "Check succeeded after repair"}}}); err != nil {
					t.Fatal(err)
				}
				next.observe(toolObservation{Name: "update_plan", CallID: "resolved"})
				if d := next.assess(); !d.done {
					t.Fatalf("recovered operation could not complete: %+v", d)
				}

			})
		}
	}
}

func TestVerificationDirectoryWindowsShortName(t *testing.T) {
	if filepath.Separator != '\\' {
		t.Skip("Windows short-path semantics")
	}
	root := filepath.Join(t.TempDir(), "RUNNER~1")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if !isVerificationCommand(fmt.Sprintf(`cd "%s" && go test ./...`, root), root) {
		t.Fatal("literal Windows short-name path rejected")
	}
	for _, arg := range []string{`%TEMP%`, `!DIR!`, `..`} {
		if _, ok := verificationDirectory(`cd "`+arg+`"`, root, root); ok {
			t.Fatalf("dynamic or outside directory accepted: %q", arg)
		}
	}
}
