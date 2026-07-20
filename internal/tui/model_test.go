package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
	runtimeevent "github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
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
