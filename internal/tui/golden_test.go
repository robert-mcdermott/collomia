package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/activity"
	runtimeevent "github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestTerminalGoldenScreens(t *testing.T) {
	t.Run("replayed chat 80x24", func(t *testing.T) {
		m := goldenReplayModel(t, 80, 24)
		assertGoldenScreen(t, "replay_chat_80x24.txt", m.View())
	})

	t.Run("question overlay 80x24", func(t *testing.T) {
		m := goldenReplayModel(t, 80, 24)
		updated, _ := m.Update(questionMsg{envelope: questionEnvelope{
			question: Question{Text: "Which verification target should run next?", Options: []string{"Unit tests", "Full test suite", "Other"}},
			reply:    make(chan string, 1),
		}})
		m = updated.(Model)
		assertGoldenScreen(t, "question_80x24.txt", m.View())
	})

	t.Run("replayed chat 40x12", func(t *testing.T) {
		m := goldenReplayModel(t, 40, 12)
		assertGoldenScreen(t, "replay_chat_40x12.txt", m.View())
	})

	t.Run("resumed session 80x24", func(t *testing.T) {
		m := goldenResumedModel(t, 80, 24)
		assertGoldenScreen(t, "resumed_session_80x24.txt", m.View())
	})

	t.Run("side-by-side diff 120x32", func(t *testing.T) {
		m := newTestModel(t)
		plain, _ := themeByName("plain")
		m.applyTheme(plain)
		before := "package main\n\nfunc answer() int {\n\treturn 41\n}\n"
		after := "package main\n\nfunc answer() int {\n\treturn 42\n}\n"
		path := filepath.Join(m.runtime.Workspace, "answer.go")
		if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
			t.Fatal(err)
		}
		m.runtime.Changes.Record(path, "edit", &before, &after)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
		m = updated.(Model)
		m.openDiffView()
		if m.diffView == nil || m.diffView.mode != "side-by-side" {
			t.Fatalf("expected side-by-side diff, got %+v", m.diffView)
		}
		assertGoldenScreen(t, "diff_120x32.txt", m.View())
	})

	t.Run("activity 80x24", func(t *testing.T) {
		m := goldenReplayModel(t, 80, 24)
		base := time.Date(2026, 7, 22, 12, 0, 0, 0, time.Local)
		m.activities = []activity.Item{
			{Time: base, Category: activity.CategoryTurn, Status: activity.StatusActive, Title: "Turn started · 1"},
			{Time: base.Add(time.Second), Category: activity.CategoryPermission, Status: activity.StatusSuccess, Title: "run_command allowed", Detail: "go test ./... · via interactive"},
			{Time: base.Add(2 * time.Second), Category: activity.CategoryTool, Status: activity.StatusError, Title: "run_command failed", Detail: "unit tests failed", FailureID: "err-0123456789abcdef"},
		}
		m.openActivityView()
		assertGoldenScreen(t, "activity_80x24.txt", m.View())
	})
}

func goldenResumedModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := newTestModel(t)
	call := provider.ToolCall{ID: "read-1", Name: "read_file", Arguments: []byte(`{"path":"main.go"}`)}
	for _, message := range []provider.Message{
		{Role: "user", Content: "Inspect main.go and explain the result."},
		{Role: "assistant", ToolCalls: []provider.ToolCall{call}},
		{Role: "tool", ToolCallID: call.ID, Content: "package main\n\nfunc main() {}"},
		{Role: "assistant", Content: "`main.go` defines an empty Go entry point."},
	} {
		m.runtime.Session.AppendMessage(message)
	}
	plain, _ := themeByName("plain")
	m = New(m.runtime, NewApprovalBroker(), "")
	m.applyTheme(plain)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func goldenReplayModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := newTestModel(t)
	plain, _ := themeByName("plain")
	m.applyTheme(plain)
	m.blocks = append(m.blocks, block{role: "user", content: "Inspect main.go and explain the result."})

	start := runtimeevent.New(runtimeevent.KindToolStart)
	start.Tool = &runtimeevent.Tool{Name: "read_file", Summary: "read main.go"}
	m.handleEvent(start)
	result := runtimeevent.New(runtimeevent.KindToolResult)
	result.Tool = &runtimeevent.Tool{Name: "read_file", Summary: "read main.go", Output: "package main\n\nfunc main() {}"}
	m.handleEvent(result)
	answer := runtimeevent.New(runtimeevent.KindTextDelta)
	answer.Text = "`main.go` defines an empty Go entry point."
	m.handleEvent(answer)
	m.busy = false
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func assertGoldenScreen(t *testing.T, name, got string) {
	t.Helper()
	got = normalizeGoldenScreen(got)
	path := filepath.Join("testdata", "golden", name)
	// Regenerating is deliberate and never automatic: run
	// COLLO_UPDATE_GOLDEN=1 go test ./internal/tui/ and review the diff, so a
	// screen change is always something a person chose.
	if os.Getenv("COLLO_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("update golden %s: %v", path, err)
		}
		t.Logf("updated golden %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n--- actual ---\n%s--- end actual ---", path, err, got)
	}
	wantText := normalizeGoldenLineEndings(string(want))
	if got != wantText {
		t.Fatalf("golden screen %s changed\n--- expected ---\n%s--- actual ---\n%s--- end ---", name, wantText, got)
	}
}

func normalizeGoldenScreen(value string) string {
	value = ansi.Strip(normalizeGoldenLineEndings(value))
	lines := strings.Split(value, "\n")
	normalized := make([]string, 0, len(lines))
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
		if lines[i] == "" && len(normalized) > 0 && normalized[len(normalized)-1] == "" {
			continue
		}
		normalized = append(normalized, lines[i])
	}
	return strings.TrimRight(strings.Join(normalized, "\n"), "\n") + "\n"
}

func normalizeGoldenLineEndings(value string) string {
	return strings.ReplaceAll(value, "\r\n", "\n")
}

func TestNormalizeGoldenLineEndings(t *testing.T) {
	t.Parallel()

	const lf = "first line\nsecond line\n"
	if got := normalizeGoldenLineEndings(lf); got != lf {
		t.Fatalf("LF input changed: got %q, want %q", got, lf)
	}
	if got := normalizeGoldenLineEndings("first line\r\nsecond line\r\n"); got != lf {
		t.Fatalf("CRLF input not normalized: got %q, want %q", got, lf)
	}
}
