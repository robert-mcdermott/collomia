package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
	runtimeevent "github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
	workspacestate "github.com/robert-mcdermott/collomia/internal/workspace"
)

func deltaEvent(text string) runtimeevent.Event {
	e := runtimeevent.New(runtimeevent.KindTextDelta)
	e.Text = text
	return e
}

func toolStartEvent(name, summary string) runtimeevent.Event {
	e := runtimeevent.New(runtimeevent.KindToolStart)
	e.Tool = &runtimeevent.Tool{Name: name, Summary: summary}
	return e
}

func toolResultEvent(name, output string) runtimeevent.Event {
	e := runtimeevent.New(runtimeevent.KindToolResult)
	e.Tool = &runtimeevent.Tool{Name: name, Output: output}
	return e
}

func newTestModel(t *testing.T) Model {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	runtime, err := app.New(context.Background(), app.Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(runtime.Close)
	m := New(runtime, NewApprovalBroker(), "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return updated.(Model)
}

func typeKeys(t *testing.T, m Model, keys string) Model {
	t.Helper()
	for _, r := range keys {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	return m
}

func press(t *testing.T, m Model, key tea.KeyType) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: key})
	return updated.(Model)
}

func TestTabCycling(t *testing.T) {
	m := newTestModel(t)
	if m.tab != tabChat {
		t.Fatalf("initial tab = %d, want chat", m.tab)
	}
	m = press(t, m, tea.KeyCtrlT)
	if m.tab != tabSession {
		t.Fatalf("after ctrl+t tab = %d, want session", m.tab)
	}
	if view := m.viewport.View(); !strings.Contains(view, "Providers") {
		t.Fatalf("session tab should list providers, got:\n%s", view)
	}
	m = press(t, m, tea.KeyCtrlT)
	if m.tab != tabHelp {
		t.Fatalf("after second ctrl+t tab = %d, want help", m.tab)
	}
	if view := m.viewport.View(); !strings.Contains(view, "Slash commands") {
		t.Fatalf("help tab should list slash commands, got:\n%s", view)
	}
	m = press(t, m, tea.KeyCtrlT)
	if m.tab != tabChat {
		t.Fatalf("tabs should wrap back to chat, got %d", m.tab)
	}
}

func TestSessionTabShowsWorkspaceHealthAndRecentActivity(t *testing.T) {
	m := newTestModel(t)
	m.workspaceLoading = false
	m.workspaceStatus.InRepository = true
	m.workspaceStatus.Branch = "wave15"
	m.workspaceStatus.Staged = 1
	decision := runtimeevent.New(runtimeevent.KindPermissionDecision)
	decision.Permission = &runtimeevent.Permission{Tool: "run_command", Summary: "run tests", Source: "interactive", Allowed: true}
	m.handleEvent(decision)
	failure := toolResultEvent("run_command", "Tool error: failed")
	failure.Tool.IsError = true
	m.handleEvent(failure)
	view := m.sessionContent()
	for _, want := range []string{"Workspace", "wave15", "staged 1", "Runtime health", "Recent decisions and failures", "allowed via interactive", "run_command"} {
		if !strings.Contains(view, want) {
			t.Fatalf("session content missing %q:\n%s", want, view)
		}
	}
}

func TestWorkspaceStatusIgnoresStaleAsyncResult(t *testing.T) {
	m := newTestModel(t)
	m.workspaceGeneration = 4
	updated, _ := m.Update(workspaceStatusMsg{generation: 3, status: workspacestate.GitStatus{InRepository: true, Branch: "stale"}})
	m = updated.(Model)
	if m.workspaceStatus.Branch == "stale" {
		t.Fatal("stale workspace result was applied")
	}
	updated, _ = m.Update(workspaceStatusMsg{generation: 4, status: workspacestate.GitStatus{InRepository: true, Branch: "current"}})
	m = updated.(Model)
	if m.workspaceStatus.Branch != "current" || m.workspaceLoading {
		t.Fatalf("current workspace result not applied: %+v", m.workspaceStatus)
	}
}

func TestPaletteFiltersAndRuns(t *testing.T) {
	m := newTestModel(t)
	m = typeKeys(t, m, "/mod")
	if !m.paletteOn {
		t.Fatal("palette should open for a slash prefix")
	}
	if len(m.palette) != 2 || m.palette[0].name != "/model" || m.palette[1].name != "/models" {
		t.Fatalf("palette for /mod = %+v", m.palette)
	}
	m = press(t, m, tea.KeyDown)
	if m.paletteSel != 1 {
		t.Fatalf("down should select second entry, got %d", m.paletteSel)
	}
	m = press(t, m, tea.KeyEnter)
	if m.paletteOn {
		t.Fatal("palette should close after enter")
	}
	if m.input.Value() != "" {
		t.Fatalf("input should reset after running a command, got %q", m.input.Value())
	}
	last := m.blocks[len(m.blocks)-1]
	if last.role != "panel" || last.title != "Provider models" || !strings.Contains(last.content, "supported: tools") {
		t.Fatalf("expected /models panel, got %+v", last)
	}
}

func TestImageAttachmentCommandsAndSessionScopedDrafts(t *testing.T) {
	m := newTestModel(t)
	imagePath := filepath.Join(m.runtime.Workspace, "screen shot.png")
	image := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fixture")...)
	if err := os.WriteFile(imagePath, image, 0o600); err != nil {
		t.Fatal(err)
	}
	if quit, cmd := (&m).slash(`/attach "screen shot.png"`); quit || cmd != nil {
		t.Fatalf("attach quit=%v cmd=%v", quit, cmd)
	}
	if len(m.pendingAttachments) != 1 || m.pendingAttachments[0].part.MediaType != "image/png" {
		t.Fatalf("pending=%+v", m.pendingAttachments)
	}
	(&m).slash("/attachments")
	if got := m.blocks[len(m.blocks)-1].content; !strings.Contains(got, "screen shot.png") || !strings.Contains(got, "image/png") {
		t.Fatalf("attachment panel=%q", got)
	}
	firstID := m.runtime.Session.Meta.ID
	m.saveSessionDraft()
	if err := m.runtime.NewSession(); err != nil {
		t.Fatal(err)
	}
	m.rebuildTranscript()
	if len(m.pendingAttachments) != 0 {
		t.Fatal("pending attachment leaked into a new session")
	}
	m.saveSessionDraft()
	if err := m.runtime.SwitchSession(firstID); err != nil {
		t.Fatal(err)
	}
	m.rebuildTranscript()
	if len(m.pendingAttachments) != 1 {
		t.Fatal("pending attachment did not follow its session draft")
	}
	(&m).slash("/detach 1")
	if len(m.pendingAttachments) != 0 {
		t.Fatal("detach did not remove the pending image")
	}
}

func TestImageAttachmentRejectsOutsideAndUnsupportedFiles(t *testing.T) {
	m := newTestModel(t)
	external := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(external, append([]byte("\x89PNG\r\n\x1a\n"), 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.addImageAttachment(external); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside error=%v", err)
	}
	textPath := filepath.Join(m.runtime.Workspace, "fake.png")
	if err := os.WriteFile(textPath, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.addImageAttachment("fake.png"); err == nil || !strings.Contains(err.Error(), "unsupported image type") {
		t.Fatalf("unsupported error=%v", err)
	}
}

func TestPaletteDismissAndTabComplete(t *testing.T) {
	m := newTestModel(t)
	m = typeKeys(t, m, "/the")
	if !m.paletteOn || m.palette[0].name != "/theme" {
		t.Fatalf("palette should offer /theme, got %+v", m.palette)
	}
	m = press(t, m, tea.KeyTab)
	if m.input.Value() != "/theme " {
		t.Fatalf("tab should complete the command, got %q", m.input.Value())
	}
	if !m.paletteOn || len(m.palette) == 0 || !m.palette[0].complete {
		t.Fatalf("palette should now suggest theme names, got %+v", m.palette)
	}
	m = typeKeys(t, m, "dra")
	if !m.paletteOn || !strings.Contains(m.palette[0].name, "dracula") {
		t.Fatalf("argument completion should narrow to dracula, got %+v", m.palette)
	}
	m = press(t, m, tea.KeyEsc)
	m = typeKeys(t, m, "y")
	if m.paletteOn {
		t.Fatal("palette should stay dismissed after esc while typing arguments")
	}
}

func TestStreamedToolOutputGrowsThenFinalizes(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	m.handleEvent(toolStartEvent("run_command", "run: go test"))
	chunk := runtimeevent.New(runtimeevent.KindToolOutput)
	chunk.Tool = &runtimeevent.Tool{Name: "run_command", Output: "line one\n"}
	m.handleEvent(chunk)
	chunk2 := runtimeevent.New(runtimeevent.KindToolOutput)
	chunk2.Tool = &runtimeevent.Tool{Name: "run_command", Output: "line two\n"}
	m.handleEvent(chunk2)
	if got := m.blocks[len(m.blocks)-1].content; got != "line one\nline two\n" {
		t.Fatalf("streamed block=%q", got)
	}
	streamedBlocks := len(m.blocks)
	m.handleEvent(toolResultEvent("run_command", "line one\nline two\nok"))
	if len(m.blocks) != streamedBlocks {
		t.Fatalf("final result should replace the streamed block, blocks went %d → %d", streamedBlocks, len(m.blocks))
	}
	if got := m.blocks[len(m.blocks)-1].content; got != "line one\nline two\nok" {
		t.Fatalf("final block=%q", got)
	}
}

func TestArgumentCompletionRunsCommand(t *testing.T) {
	m := newTestModel(t)
	m = typeKeys(t, m, "/theme syn")
	if !m.paletteOn || len(m.palette) == 0 || !strings.Contains(m.palette[0].name, "synthwave") {
		t.Fatalf("expected synthwave suggestion, got %+v", m.palette)
	}
	m = press(t, m, tea.KeyEnter)
	if m.theme.Name != "synthwave" {
		t.Fatalf("enter on an argument suggestion should run it, theme=%q", m.theme.Name)
	}
}

func TestModelPickerOpensAndFilters(t *testing.T) {
	m := newTestModel(t)
	m = typeKeys(t, m, "/model")
	m = press(t, m, tea.KeyEnter)
	if m.picker == nil {
		t.Fatal("/model with no args should open the provider picker")
	}
	if len(m.picker.matches) == 0 || m.picker.matches[0].title != "ollama" {
		t.Fatalf("picker should list providers, got %+v", m.picker.matches)
	}
	if !strings.Contains(m.picker.matches[0].desc, "tools") || !strings.Contains(m.picker.matches[0].desc, "context") {
		t.Fatalf("picker should expose compact capabilities, got %+v", m.picker.matches[0])
	}
	// Modal: typing filters the picker, not the input.
	m = typeKeys(t, m, "zzz")
	if len(m.picker.matches) != 0 {
		t.Fatalf("no provider should match zzz, got %+v", m.picker.matches)
	}
	m = press(t, m, tea.KeyEsc)
	if m.picker != nil {
		t.Fatal("esc should dismiss the picker")
	}
}

func TestProviderStatusProbeReplacesCheckingPanel(t *testing.T) {
	m := newTestModel(t)
	m.addPanel("Provider models", renderProviderStatuses(m.runtime.ConfiguredProviders()))
	before := len(m.blocks)
	status := m.runtime.ConfiguredProviders()[0]
	status.Availability = app.ProviderAvailable
	status.Models = []provider.ModelInfo{{ID: status.DefaultModel, Capabilities: status.Capabilities}}
	updated, _ := m.Update(providerStatusMsg{statuses: []app.ProviderStatus{status}})
	m = updated.(Model)
	if len(m.blocks) != before {
		t.Fatalf("live result should replace the checking panel, blocks %d -> %d", before, len(m.blocks))
	}
	last := m.blocks[len(m.blocks)-1]
	if !strings.Contains(last.content, "available · 1 model(s)") || strings.Contains(last.content, "checking live catalog") {
		t.Fatalf("panel=%+v", last)
	}
}

func TestFuzzyScoring(t *testing.T) {
	if _, ok := fuzzyScore("mdl", "model.go"); !ok {
		t.Fatal("subsequence should match")
	}
	if _, ok := fuzzyScore("xyz", "model.go"); ok {
		t.Fatal("non-subsequence should not match")
	}
	exact, _ := fuzzyScore("model", "model.go")
	scattered, _ := fuzzyScore("model", "m1o2d3e4l5.go")
	if exact <= scattered {
		t.Fatalf("consecutive match should outrank scattered: %d vs %d", exact, scattered)
	}
}

func TestFileMentionPickerInsertsPath(t *testing.T) {
	m := newTestModel(t)
	if err := os.WriteFile(filepath.Join(m.runtime.Workspace, "notes.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = typeKeys(t, m, "explain @")
	if m.picker == nil {
		t.Fatal("typing @ at a word boundary should open the file picker")
	}
	m = typeKeys(t, m, "notes")
	if len(m.picker.matches) == 0 || m.picker.matches[0].id != "notes.md" {
		t.Fatalf("picker should match notes.md, got %+v", m.picker.matches)
	}
	m = press(t, m, tea.KeyEnter)
	if m.picker != nil {
		t.Fatal("picker should close after selection")
	}
	if got := m.input.Value(); got != "explain notes.md " {
		t.Fatalf("input=%q", got)
	}
	// An email-like @ must not open the picker.
	m.input.Reset()
	m = typeKeys(t, m, "mail user@")
	if m.picker != nil {
		t.Fatal("@ inside a word must not open the picker")
	}
}

func TestFileMentionPickerIncludesFoldersAndQuotesSpaces(t *testing.T) {
	m := newTestModel(t)
	dir := filepath.Join(m.runtime.Workspace, "docs and notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "guide.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = typeKeys(t, m, "review @")
	if m.picker == nil {
		t.Fatal("typing @ should open the path picker")
	}
	m = typeKeys(t, m, "docs and notes/")
	if len(m.picker.matches) == 0 || m.picker.matches[0].id != "docs and notes/" || m.picker.matches[0].desc != "folder" {
		t.Fatalf("picker should match the folder, got %+v", m.picker.matches)
	}
	m = press(t, m, tea.KeyEnter)
	if got := m.input.Value(); got != `review "docs and notes/" ` {
		t.Fatalf("folder mention should be quoted as one path, got %q", got)
	}
}

func TestPromptFileLoadsIntoComposer(t *testing.T) {
	m := newTestModel(t)
	dir := filepath.Join(m.runtime.Workspace, "prompt files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte("Review this change.\nFocus on correctness."), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, cmd := (&m).slash(`/prompt "prompt files/review.md"`); cmd != nil {
		t.Fatal("loading a prompt file should not start a turn")
	}
	if got := m.input.Value(); !strings.Contains(got, "[Prompt loaded from") || !strings.Contains(got, "Focus on correctness") {
		t.Fatalf("prompt was not loaded into composer: %q", got)
	}
	last := m.blocks[len(m.blocks)-1]
	if last.role != "system" || !strings.Contains(last.content, "Review or edit it") {
		t.Fatalf("missing review guidance: %+v", last)
	}
}

func TestPromptWithoutPathOpensFilePicker(t *testing.T) {
	m := newTestModel(t)
	if err := os.WriteFile(filepath.Join(m.runtime.Workspace, "task.txt"), []byte("Do the task"), 0o644); err != nil {
		t.Fatal(err)
	}
	(&m).slash("/prompt")
	if m.picker == nil || m.picker.title != "Load prompt from file" {
		t.Fatalf("/prompt should open a file picker, got %+v", m.picker)
	}
	m = typeKeys(t, m, "task")
	m = press(t, m, tea.KeyEnter)
	if !strings.Contains(m.input.Value(), "Do the task") {
		t.Fatalf("picker did not load the selected prompt: %q", m.input.Value())
	}
}

func TestPromptFileRejectsBinaryAndOutsideWorkspace(t *testing.T) {
	m := newTestModel(t)
	if err := os.WriteFile(filepath.Join(m.runtime.Workspace, "image.bin"), []byte{'P', 'N', 'G', 0, 1}, 0o644); err != nil {
		t.Fatal(err)
	}
	(&m).slash("/prompt image.bin")
	if last := m.blocks[len(m.blocks)-1]; last.role != "error" || !strings.Contains(last.content, "UTF-8 text") {
		t.Fatalf("binary input should be rejected clearly, got %+v", last)
	}
	if err := os.WriteFile(filepath.Join(m.runtime.Workspace, "control.txt"), []byte("hello\x1b[2J"), 0o644); err != nil {
		t.Fatal(err)
	}
	(&m).slash("/prompt control.txt")
	if last := m.blocks[len(m.blocks)-1]; last.role != "error" || !strings.Contains(last.content, "terminal control") {
		t.Fatalf("terminal controls should be rejected, got %+v", last)
	}
	if err := os.WriteFile(filepath.Join(m.runtime.Workspace, "huge.txt"), bytes.Repeat([]byte{'x'}, maxPromptFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	(&m).slash("/prompt huge.txt")
	if last := m.blocks[len(m.blocks)-1]; last.role != "error" || !strings.Contains(last.content, "limit") {
		t.Fatalf("oversized prompt should be rejected, got %+v", last)
	}

	external := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(external, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	(&m).slash("/prompt " + quoteComposerPath(external))
	if last := m.blocks[len(m.blocks)-1]; last.role != "error" || !strings.Contains(last.content, "outside the active workspace") {
		t.Fatalf("outside prompt input should be rejected, got %+v", last)
	}
}

func TestParseTerminalPath(t *testing.T) {
	tests := map[string]string{
		`docs/review.md`:             `docs/review.md`,
		`docs/review\ this.md`:       `docs/review this.md`,
		`"docs/review this.md"`:      `docs/review this.md`,
		`'docs/review this.md'`:      `docs/review this.md`,
		`"C:\Users\me\review.md"`:    `C:\Users\me\review.md`,
		`file:///tmp/review%20me.md`: filepath.FromSlash(`/tmp/review me.md`),
	}
	for raw, want := range tests {
		got, err := parseTerminalPath(raw)
		if err != nil || got != want {
			t.Errorf("parseTerminalPath(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{`docs/review this.md`, `"unterminated`} {
		if _, err := parseTerminalPath(raw); err == nil {
			t.Errorf("parseTerminalPath(%q) should fail", raw)
		}
	}
}

func TestNewSessionAndPickerSwitch(t *testing.T) {
	m := newTestModel(t)
	m.blocks = append(m.blocks, block{role: "user", content: "old talk"})
	first := m.runtime.Session.Meta.ID
	m = typeKeys(t, m, "/new")
	m = press(t, m, tea.KeyEnter)
	if m.runtime.Session.Meta.ID == first {
		t.Fatal("/new should create a fresh session")
	}
	m = typeKeys(t, m, "/sessions")
	m = press(t, m, tea.KeyEnter)
	if m.picker == nil {
		t.Fatal("/sessions should open the session picker")
	}
}

func TestRewindCreatesConversationBranchWithoutChangingWorkspace(t *testing.T) {
	m := newTestModel(t)
	workspaceFile := filepath.Join(m.runtime.Workspace, "state.txt")
	if err := os.WriteFile(workspaceFile, []byte("current workspace state"), 0o600); err != nil {
		t.Fatal(err)
	}
	for turn := 1; turn <= 2; turn++ {
		m.runtime.Session.AppendMessage(provider.Message{Role: "user", Content: fmt.Sprintf("prompt %d", turn)})
		m.runtime.Session.AppendMessage(provider.Message{Role: "assistant", Content: fmt.Sprintf("answer %d", turn)})
		m.runtime.Session.AppendEvent(runtimeevent.New(runtimeevent.KindTurnEnd))
	}
	originalID := m.runtime.Session.Meta.ID
	if quit, cmd := (&m).slash("/rewind 1"); quit || cmd != nil {
		t.Fatalf("rewind unexpectedly quit or returned command: quit=%t cmd=%v", quit, cmd)
	}
	if m.runtime.Session.Meta.ID == originalID || m.runtime.Session.Meta.ForkedFrom != originalID || m.runtime.Session.Meta.Turns != 1 {
		t.Fatalf("rewound session=%+v original=%s", m.runtime.Session.Meta, originalID)
	}
	if transcript := m.runtime.Session.TranscriptMessages(); len(transcript) != 2 || transcript[0].Content != "prompt 1" {
		t.Fatalf("rewound transcript=%+v", transcript)
	}
	data, err := os.ReadFile(workspaceFile)
	if err != nil || string(data) != "current workspace state" {
		t.Fatalf("rewind changed workspace: data=%q err=%v", data, err)
	}
	original, err := m.runtime.Sessions.Load(originalID)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	if original.Meta.Turns != 2 || len(original.TranscriptMessages()) != 4 {
		t.Fatalf("original changed: meta=%+v transcript=%+v", original.Meta, original.TranscriptMessages())
	}
	if last := m.blocks[len(m.blocks)-1].content; !strings.Contains(last, "were not undone") {
		t.Fatalf("rewind safety notice missing: %q", last)
	}
}

func TestRewindWithoutArgumentOpensCompletedTurnPicker(t *testing.T) {
	m := newTestModel(t)
	for turn := 1; turn <= 2; turn++ {
		m.runtime.Session.AppendMessage(provider.Message{Role: "user", Content: fmt.Sprintf("prompt %d", turn)})
		m.runtime.Session.AppendMessage(provider.Message{Role: "assistant", Content: "done"})
		m.runtime.Session.AppendEvent(runtimeevent.New(runtimeevent.KindTurnEnd))
	}
	if quit, cmd := (&m).slash("/rewind"); quit || cmd != nil {
		t.Fatalf("rewind picker unexpectedly quit or returned command: quit=%t cmd=%v", quit, cmd)
	}
	if m.picker == nil || m.picker.title != "Rewind conversation safely" {
		t.Fatalf("rewind picker=%+v", m.picker)
	}
	if len(m.picker.matches) != 2 || m.picker.matches[0].id != "1" || m.picker.matches[1].id != "0" {
		t.Fatalf("rewind targets=%+v", m.picker.matches)
	}
}

func TestResumedTUIRestoresCompleteTranscriptAndToolResults(t *testing.T) {
	m := newTestModel(t)
	id := m.runtime.Session.Meta.ID
	call := provider.ToolCall{ID: "read-1", Name: "read_file", Arguments: []byte(`{"path":"main.go"}`)}
	for _, message := range []provider.Message{
		{Role: "user", Content: "Inspect main.go."},
		{Role: "assistant", ToolCalls: []provider.ToolCall{call}},
		{Role: "tool", ToolCallID: call.ID, Content: "package main\n\nfunc main() {}"},
		{Role: "assistant", Content: "The entry point is empty."},
	} {
		m.runtime.Session.AppendMessage(message)
	}
	// Compaction changes the model-visible context, not the visible durable
	// history restored by the TUI.
	m.runtime.Session.AppendCompaction(provider.Message{Role: "user", Content: "[Context summary: earlier work]"}, 3)
	if err := m.runtime.SwitchSession(id); err != nil {
		t.Fatal(err)
	}
	if active := m.runtime.Session.Active(); len(active) == 0 || !strings.HasPrefix(active[0].Content, "[Context summary") {
		t.Fatalf("test setup did not compact active context: %+v", active)
	}

	restored := New(m.runtime, NewApprovalBroker(), "")
	roles := make([]string, len(restored.blocks))
	for i, entry := range restored.blocks {
		roles[i] = entry.role
	}
	if got, want := strings.Join(roles, ","), "user,tool,tool-result,assistant"; got != want {
		t.Fatalf("restored roles=%q, want %q; blocks=%+v", got, want, restored.blocks)
	}
	if restored.blocks[1].content != "read_file\x00read main.go" || restored.blocks[2].tool != "read_file" || !strings.Contains(restored.blocks[2].content, "func main") {
		t.Fatalf("saved tool call/result not reconstructed: %+v", restored.blocks)
	}
	if len(restored.promptHistory) != 1 || restored.promptHistory[0] != "Inspect main.go." {
		t.Fatalf("prompt history=%q", restored.promptHistory)
	}
}

func TestInterruptedSavedToolIsShownButNeverReplayed(t *testing.T) {
	m := newTestModel(t)
	id := m.runtime.Session.Meta.ID
	target := filepath.Join(m.runtime.Workspace, "must-not-exist.txt")
	m.runtime.Session.AppendMessage(provider.Message{
		Role: "assistant",
		ToolCalls: []provider.ToolCall{{
			ID: "write-1", Name: "write_file",
			Arguments: []byte(`{"path":"must-not-exist.txt","content":"unsafe"}`),
		}},
	})
	if err := m.runtime.SwitchSession(id); err != nil {
		t.Fatal(err)
	}
	restored := New(m.runtime, NewApprovalBroker(), "")
	var interrupted bool
	for _, entry := range restored.blocks {
		interrupted = interrupted || strings.Contains(entry.content, "Tool call interrupted")
	}
	if !interrupted {
		t.Fatalf("interrupted result missing from restored blocks: %+v", restored.blocks)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("restoring the TUI replayed a saved write: stat err=%v", err)
	}
}

func TestPromptHistoryRespectsMultilineEditingAndRestoresDraft(t *testing.T) {
	m := newTestModel(t)
	m.recordPrompt("first prompt")
	m.recordPrompt("second prompt")
	m.setComposerValue("draft first line\ndraft second line")

	// The first up-arrow moves within the multiline draft.
	m = press(t, m, tea.KeyUp)
	if got := m.input.Value(); got != "draft first line\ndraft second line" || m.historyIndex != len(m.promptHistory) {
		t.Fatalf("multiline navigation entered history: value=%q index=%d", got, m.historyIndex)
	}
	// At the first visual line, arrows navigate session prompt history.
	m = press(t, m, tea.KeyUp)
	if got := m.input.Value(); got != "second prompt" {
		t.Fatalf("previous prompt=%q", got)
	}
	m = press(t, m, tea.KeyUp)
	if got := m.input.Value(); got != "first prompt" {
		t.Fatalf("oldest prompt=%q", got)
	}
	m = press(t, m, tea.KeyDown)
	m = press(t, m, tea.KeyDown)
	if got := m.input.Value(); got != "draft first line\ndraft second line" {
		t.Fatalf("draft was not restored after history navigation: %q", got)
	}
}

func TestRetryOnlyLoadsPreviousPromptForReview(t *testing.T) {
	m := newTestModel(t)
	m.recordPrompt("Investigate the failing test")
	before := len(m.blocks)
	quit, cmd := (&m).slash("/retry")
	if quit || cmd != nil || m.busy {
		t.Fatalf("/retry must not execute: quit=%t cmd=%v busy=%t", quit, cmd, m.busy)
	}
	if got := m.input.Value(); got != "Investigate the failing test" {
		t.Fatalf("retry composer=%q", got)
	}
	if len(m.blocks) != before+1 || !strings.Contains(m.blocks[len(m.blocks)-1].content, "Nothing has been sent") {
		t.Fatalf("retry guidance missing: %+v", m.blocks)
	}
}

func TestSessionPickerShortcutPreservesDraftsPerSession(t *testing.T) {
	m := newTestModel(t)
	first := m.runtime.Session.Meta.ID
	if err := m.runtime.NewSession(); err != nil {
		t.Fatal(err)
	}
	second := m.runtime.Session.Meta.ID
	if err := m.runtime.SwitchSession(first); err != nil {
		t.Fatal(err)
	}
	m.rebuildTranscript()
	m.setComposerValue("draft for first")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true})
	m = updated.(Model)
	if m.picker == nil || m.picker.title != "Resume session" {
		t.Fatalf("alt+s did not open the session picker: %+v", m.picker)
	}
	for i, item := range m.picker.matches {
		if item.id == second {
			m.picker.sel = i
			break
		}
	}
	_, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.runtime.Session.Meta.ID != second || m.input.Value() != "" {
		t.Fatalf("switch to second: session=%s draft=%q", m.runtime.Session.Meta.ID, m.input.Value())
	}
	m.setComposerValue("draft for second")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true})
	m = updated.(Model)
	for i, item := range m.picker.matches {
		if item.id == first {
			m.picker.sel = i
			break
		}
	}
	_, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.runtime.Session.Meta.ID != first || m.input.Value() != "draft for first" {
		t.Fatalf("switch back: session=%s draft=%q", m.runtime.Session.Meta.ID, m.input.Value())
	}
}

func TestThemeSlashCommand(t *testing.T) {
	m := newTestModel(t)
	m = typeKeys(t, m, "/theme synthwave")
	m = press(t, m, tea.KeyEnter)
	if m.theme.Name != "synthwave" {
		t.Fatalf("theme = %q, want synthwave", m.theme.Name)
	}
	m = typeKeys(t, m, "/theme nope")
	m = press(t, m, tea.KeyEnter)
	last := m.blocks[len(m.blocks)-1]
	if last.role != "error" || !strings.Contains(last.content, "unknown theme") {
		t.Fatalf("expected unknown-theme error, got %+v", last)
	}
	if m.theme.Name != "synthwave" {
		t.Fatalf("failed switch should keep theme, got %q", m.theme.Name)
	}
}

func TestTypingDoesNotScrollTranscript(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 80; i++ {
		m.blocks = append(m.blocks, block{role: "system", content: "history line"})
	}
	m.refresh()
	if !m.viewport.AtBottom() {
		t.Fatal("chat viewport should start at the bottom")
	}
	offset := m.viewport.YOffset
	m = typeKeys(t, m, "up and down u d j k b f")
	if m.viewport.YOffset != offset {
		t.Fatalf("typing letters scrolled the viewport: offset %d -> %d", offset, m.viewport.YOffset)
	}
	m = press(t, m, tea.KeyPgUp)
	if m.viewport.YOffset >= offset {
		t.Fatalf("pgup should still scroll the viewport, offset %d -> %d", offset, m.viewport.YOffset)
	}
}

func TestBannerVisibleAtStartup(t *testing.T) {
	m := newTestModel(t)
	view := m.viewport.View()
	if !strings.Contains(view, "╔") {
		t.Fatalf("banner art should render on the first frame, got:\n%s", view)
	}
	if !strings.Contains(view, "theme") {
		t.Fatalf("banner subtitle should render on the first frame, got:\n%s", view)
	}
}

func TestToolOutputCollapses(t *testing.T) {
	m := newTestModel(t)
	long := strings.Repeat("output line\n", 20) + "final-marker"
	m.busy = true
	m.handleEvent(toolStartEvent("list_files", "list ."))
	m.handleEvent(toolResultEvent("list_files", long))
	if content := m.chatContent(); !strings.Contains(content, "final-marker") {
		t.Fatal("the running tool's output should be shown in full")
	}
	m.handleEvent(deltaEvent("Here is what I found."))
	content := m.chatContent()
	if strings.Contains(content, "final-marker") {
		t.Fatal("finished tool output should collapse once the agent moves on")
	}
	if !strings.Contains(content, "21 lines hidden") {
		t.Fatalf("collapsed output should summarize its size, got:\n%s", content)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = updated.(Model)
	if content := m.chatContent(); !strings.Contains(content, "final-marker") {
		t.Fatal("ctrl+o should expand collapsed tool output")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = updated.(Model)
	if content := m.chatContent(); strings.Contains(content, "final-marker") {
		t.Fatal("ctrl+o again should re-collapse tool output")
	}
}

func TestShortToolOutputStaysVisible(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	m.handleEvent(toolResultEvent("read_file", "one\ntwo\nthree"))
	m.handleEvent(deltaEvent("Done."))
	if content := m.chatContent(); !strings.Contains(content, "three") {
		t.Fatal("short tool output should never collapse")
	}
}

func TestStatusBarShowsContextGauge(t *testing.T) {
	m := newTestModel(t)
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "ctx") {
		t.Fatalf("status bar should include the context gauge, got %q", bar)
	}
	if !strings.Contains(bar, "ASK") {
		t.Fatalf("status bar should include the autonomy mode, got %q", bar)
	}
}
