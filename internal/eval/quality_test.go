package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func TestRefactorAndVerificationEvaluation(t *testing.T) {
	workspace := t.TempDir()
	mustWrite(t, filepath.Join(workspace, "go.mod"), "module refactorfixture\n\ngo 1.26.0\n")
	const source = `package refactorfixture

import "strings"

func Greeting(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	return "hello " + normalized
}

func Farewell(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	return "goodbye " + normalized
}
`
	const refactored = `package refactorfixture

import "strings"

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func Greeting(name string) string {
	return "hello " + normalizeName(name)
}

func Farewell(name string) string {
	return "goodbye " + normalizeName(name)
}
`
	mustWrite(t, filepath.Join(workspace, "greeting.go"), source)
	mustWrite(t, filepath.Join(workspace, "greeting_test.go"), `package refactorfixture

import "testing"

func TestMessages(t *testing.T) {
	if got := Greeting("  ALICE "); got != "hello alice" { t.Fatalf("Greeting() = %q", got) }
	if got := Farewell("  BOB "); got != "goodbye bob" { t.Fatalf("Farewell() = %q", got) }
}
`)
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("read", "read_file", `{"path":"greeting.go"}`)},
		{check: requireLastToolContains("func Greeting", "func Farewell"), response: encodedToolResponse("patch", "apply_patch", map[string]any{
			"operations": []map[string]string{{"op": "update", "path": "greeting.go", "old_text": source, "new_text": refactored}},
		})},
		{check: requireLastToolContains(`"op":"update"`, `"path":"greeting.go"`), response: toolResponse("test", "run_command", `{"command":"go test ./...","timeout_seconds":60}`)},
		{check: requireLastToolContains("ok"), response: provider.Response{Content: "Extracted one normalization helper and verified existing behavior."}},
	}}
	runtime, tracker := newEvaluationAgent(t, workspace, client, "autopilot")
	var events []event.Event
	answer, err := runtime.Run(t.Context(), "Refactor the duplicate normalization without changing behavior, then verify it.", func(e event.Event) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "greeting.go"))
	if err != nil {
		t.Fatal(err)
	}
	if answer == "" || strings.Count(string(data), "strings.ToLower") != 1 || !strings.Contains(string(data), "normalizeName") {
		t.Fatalf("answer=%q refactored source:\n%s", answer, data)
	}
	if changed := tracker.Changed(); len(changed) != 1 || countKind(events, event.KindToolStart) != 3 || deniedDecisions(events) != 0 {
		t.Fatalf("changed=%v starts=%d denied=%d", changed, countKind(events, event.KindToolStart), deniedDecisions(events))
	}
}

func TestGeneratedTestsAreExecutedEvaluation(t *testing.T) {
	workspace := t.TempDir()
	mustWrite(t, filepath.Join(workspace, "go.mod"), "module testfixture\n\ngo 1.26.0\n")
	mustWrite(t, filepath.Join(workspace, "classify.go"), `package testfixture

func Sign(value int) string {
	if value < 0 { return "negative" }
	if value > 0 { return "positive" }
	return "zero"
}
`)
	const tests = `package testfixture

import "testing"

func TestSign(t *testing.T) {
	for _, test := range []struct {
		value int
		want string
	}{{-2, "negative"}, {0, "zero"}, {7, "positive"}} {
		if got := Sign(test.value); got != test.want {
			t.Fatalf("Sign(%d) = %q, want %q", test.value, got, test.want)
		}
	}
}
`
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("read", "read_file", `{"path":"classify.go"}`)},
		{check: requireLastToolContains(`func Sign`), response: encodedToolResponse("write-tests", "write_file", map[string]string{"path": "classify_test.go", "content": tests})},
		{check: requireLastToolContains("wrote", "classify_test.go"), response: toolResponse("run-tests", "run_command", `{"command":"go test ./...","timeout_seconds":60}`)},
		{check: requireLastToolContains("ok"), response: provider.Response{Content: "Added boundary-focused tests and ran them successfully."}},
	}}
	runtime, tracker := newEvaluationAgent(t, workspace, client, "autopilot")
	answer, err := runtime.Run(t.Context(), "Add useful tests for Sign and run them.", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "classify_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "ran them") || !strings.Contains(string(data), `{-2, "negative"}`) || len(tracker.Changed()) != 1 {
		t.Fatalf("answer=%q changed=%v tests:\n%s", answer, tracker.Changed(), data)
	}
}

func TestReadOnlyReviewFindsGroundedRegressionEvaluation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	workspace := t.TempDir()
	runEvalGit(t, workspace, "init", "-b", "main")
	runEvalGit(t, workspace, "config", "core.autocrlf", "false")
	path := filepath.Join(workspace, "eligibility.go")
	mustWrite(t, path, "package eligibility\n\nfunc IsAdult(age int) bool { return age >= 18 }\n")
	runEvalGit(t, workspace, "add", ".")
	runEvalGit(t, workspace, "commit", "-m", "base")
	mustWrite(t, path, "package eligibility\n\nfunc IsAdult(age int) bool { return age > 18 }\n")

	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("status", "git_status", `{}`)},
		{check: requireLastToolContains("eligibility.go"), response: toolResponse("diff", "git_diff", `{"path":"eligibility.go"}`)},
		{check: requireLastToolContains("-func IsAdult(age int) bool { return age >= 18 }", "+func IsAdult(age int) bool { return age > 18 }"), response: toolResponse("context", "read_file", `{"path":"eligibility.go"}`)},
		{check: requireLastToolContains("3", "age > 18"), response: provider.Response{Content: "High: eligibility.go:3 excludes exactly age 18, regressing the established adult boundary. Restore >= 18 and add a boundary test. Verdict: not safe to merge as-is."}},
	}}
	runtime, tracker := newEvaluationAgent(t, workspace, client, "ask")
	answer, err := runtime.Run(t.Context(), app.ReviewPrompt("-", "Focus on boundary behavior."), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "eligibility.go:3") || !strings.Contains(answer, "not safe") {
		t.Fatalf("review answer=%q", answer)
	}
	if changed := tracker.Changed(); len(changed) != 0 {
		t.Fatalf("read-only review changed files: %v", changed)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "age > 18") {
		t.Fatalf("review changed the worktree: %q err=%v", data, err)
	}
}

func TestCompactionQualityRetainsDecisionsAndRecentWorkEvaluation(t *testing.T) {
	workspace := t.TempDir()
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{check: func(request provider.Request) error {
			if len(request.Tools) != 0 {
				return errors.New("compaction unexpectedly exposed tools")
			}
			source := request.Messages[0].Content
			for _, want := range []string{"Preserve the Parse API", "parser.go", "Use table-driven tests"} {
				if !strings.Contains(source, want) {
					return errors.New("compaction source omitted " + want)
				}
			}
			return nil
		}, response: provider.Response{Content: "Preserve the Parse API. Refactor parser.go internally and use table-driven tests."}},
		{check: func(request provider.Request) error {
			var combined strings.Builder
			for _, message := range request.Messages {
				combined.WriteString(message.Content)
				combined.WriteByte('\n')
			}
			for _, want := range []string{"Preserve the Parse API", "parser.go", "table-driven tests", "recent observation 5"} {
				if !strings.Contains(combined.String(), want) {
					return errors.New("active context omitted " + want)
				}
			}
			return nil
		}, response: provider.Response{Content: "The compacted decisions and recent evidence remain available."}},
	}}
	cfg := appconfig.Defaults()
	runtime := agent.New(agent.Options{
		Client: client, ProviderName: "offline-evaluation", Model: "scripted",
		ProviderConfig: appconfig.Provider{Type: "fixture", MaxTokens: 256, Context: 4_000},
		Workspace:      workspace, Registry: tools.NewRegistry(), Permissions: permission.New(cfg.Permissions, nil),
		MaxIterations: 4, MaxToolOutput: cfg.Options.MaxToolOutputBytes,
	})
	messages := []provider.Message{
		{Role: "user", Content: "Preserve the Parse API while refactoring parser.go."},
		{Role: "assistant", Content: "Decision: keep the exported signature unchanged."},
		{Role: "user", Content: "Use table-driven tests for the behavior."},
		{Role: "assistant", Content: "Decision accepted."},
	}
	for i := 0; i < 6; i++ {
		messages = append(messages, provider.Message{Role: "user", Content: "recent observation " + string(rune('0'+i))})
	}
	runtime.SetMessages(messages)
	replaced, err := runtime.Compact(t.Context(), "decisions, constraints, and current verification evidence")
	if err != nil {
		t.Fatal(err)
	}
	if replaced != 4 {
		t.Fatalf("compaction replaced %d messages, want 4", replaced)
	}
	answer, err := runtime.Run(t.Context(), "Continue using the compacted decisions.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "remain available") || client.next != len(client.steps) {
		t.Fatalf("answer=%q provider steps=%d/%d", answer, client.next, len(client.steps))
	}
}

type blockingEvaluationProvider struct {
	started chan struct{}
	once    sync.Once
}

func (p *blockingEvaluationProvider) Name() string { return "blocking-evaluation" }

func (p *blockingEvaluationProvider) Chat(ctx context.Context, _ provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	return provider.Response{}, ctx.Err()
}

func TestCancellationStopsProviderWithoutStartingToolsEvaluation(t *testing.T) {
	workspace := t.TempDir()
	client := &blockingEvaluationProvider{started: make(chan struct{})}
	runtime, tracker := newEvaluationAgent(t, workspace, client, "autopilot")
	ctx, cancel := context.WithCancel(t.Context())
	var events []event.Event
	done := make(chan error, 1)
	go func() {
		_, err := runtime.Run(ctx, "Wait for the provider, then cancel.", func(e event.Event) {
			events = append(events, e)
		})
		done <- err
	}()
	<-client.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run error=%v", err)
	}
	if changed := tracker.Changed(); len(changed) != 0 {
		t.Fatalf("cancelled provider call changed files: %v", changed)
	}
	if countKind(events, event.KindToolStart) != 0 {
		t.Fatalf("cancelled provider call started a tool: %+v", events)
	}
}

func encodedToolResponse(id, name string, arguments any) provider.Response {
	data, err := json.Marshal(arguments)
	if err != nil {
		panic(err)
	}
	return toolResponse(id, name, string(data))
}
