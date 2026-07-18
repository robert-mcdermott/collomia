package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
)

func newTestModel(t *testing.T) Model {
	t.Helper()
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
	if last.role != "system" || !strings.Contains(last.content, "Configured providers") {
		t.Fatalf("expected /models output, got %+v", last)
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
	if m.paletteOn {
		t.Fatal("palette should close once arguments begin")
	}
	m = typeKeys(t, m, "x")
	m = press(t, m, tea.KeyEsc)
	m = typeKeys(t, m, "y")
	if m.paletteOn {
		t.Fatal("palette should stay closed while typing arguments")
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
