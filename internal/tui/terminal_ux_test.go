package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/app"
)

func TestConfiguredGlobalKeybindingAndEffectiveHelp(t *testing.T) {
	m := newTestModel(t)
	m.runtime.Config.Options.Keybindings["next_tab"] = "alt+t"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}, Alt: true})
	m = updated.(Model)
	if m.tab != tabSession {
		t.Fatalf("custom next-tab key did not switch tabs: %d", m.tab)
	}
	if help := m.helpContent(); !strings.Contains(help, "alt+t") {
		t.Fatalf("help does not show effective binding:\n%s", help)
	}
}

func TestBusyTurnCancellationRemainsInteractive(t *testing.T) {
	m := newTestModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	m.busy = true
	m.cancel = cancel
	m.setComposerValue("draft survives cancellation")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("cancelling an active turn unexpectedly quit the TUI")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("ctrl+c did not cancel the active turn")
	}
	if !m.busy || m.input.Value() != "draft survives cancellation" || !m.input.Focused() {
		t.Fatalf("cancel request blocked interaction: busy=%t draft=%q focused=%t", m.busy, m.input.Value(), m.input.Focused())
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Cancelling current turn") {
		t.Fatalf("cancel acknowledgement is not visible:\n%s", view)
	}
}

func TestChatScrollPositionSurvivesStreamingRefresh(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 100; i++ {
		m.blocks = append(m.blocks, block{role: "system", content: "history line"})
	}
	m.refresh()
	m = press(t, m, tea.KeyPgUp)
	if m.chatFollow {
		t.Fatal("page-up should pause live follow")
	}
	offset := m.viewport.YOffset
	m.handleEvent(deltaEvent("new streamed answer"))
	if got := m.viewport.YOffset; got != offset {
		t.Fatalf("stream refresh moved scrolled transcript: %d -> %d", offset, got)
	}
	m = press(t, m, tea.KeyEnd)
	if !m.chatFollow || !m.viewport.AtBottom() {
		t.Fatal("end should resume live follow at the bottom")
	}
}

func BenchmarkChatViewLongTranscript(b *testing.B) {
	home := b.TempDir()
	b.Setenv("HOME", home)
	b.Setenv("USERPROFILE", home)
	runtime, err := app.New(context.Background(), app.Options{Workspace: b.TempDir(), Ephemeral: true})
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.Close()
	m := New(runtime, NewApprovalBroker(), "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)
	for i := 0; i < 500; i++ {
		if i%2 == 0 {
			m.blocks = append(m.blocks, block{role: "user", content: "Inspect the representative repository state."})
		} else {
			m.blocks = append(m.blocks, block{role: "assistant", content: "The repository state is stable.\n\n```go\nfunc example() {}\n```"})
		}
	}
	_ = m.chatContent() // initialize the cached renderer outside the timer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if output := m.chatContent(); len(output) == 0 {
			b.Fatal("empty chat render")
		}
	}
}

func TestTranscriptSearchNavigationAndCopyFallback(t *testing.T) {
	m := newTestModel(t)
	m.blocks = []block{
		{role: "user", content: "please inspect alpha"},
		{role: "assistant", content: "the needle is here"},
		{role: "tool-result", tool: "read_file", content: "alpha.go"},
	}
	m.openTranscriptView()
	if m.transcript == nil {
		t.Fatal("transcript view did not open")
	}
	updated, _ := m.handleTranscriptKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	for _, r := range "needle" {
		updated, _ = m.handleTranscriptKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, _ = m.handleTranscriptKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.transcript.cursor != 1 || len(m.transcript.matches) != 1 {
		t.Fatalf("search cursor=%d matches=%v", m.transcript.cursor, m.transcript.matches)
	}
	updated, _ = m.handleTranscriptKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !strings.Contains(m.transcript.notice, "copy unavailable") {
		t.Fatalf("non-terminal copy should explain fallback, got %q", m.transcript.notice)
	}
	if view := m.renderTranscriptView(); !strings.Contains(view, "message 2/3") || !strings.Contains(view, "the needle is here") {
		t.Fatalf("transcript view missing selection:\n%s", view)
	}
}

func TestInteractiveDiffViewAdaptsAndNavigatesFiles(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 130, 40
	for i, name := range []string{"one.go", "two.go"} {
		path := filepath.Join(m.runtime.Workspace, name)
		before := "package main\n\nfunc value() int {\n\treturn 1\n}\n"
		after := strings.Replace(before, "return 1", "return 2", 1) + "\n// changed\n"
		if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
			t.Fatal(err)
		}
		m.runtime.Changes.Record(path, "edit", &before, &after)
		_ = i
	}
	m.openDiffView()
	if m.diffView == nil || m.diffView.mode != "side-by-side" {
		t.Fatalf("wide diff mode=%v", m.diffView)
	}
	if view := m.renderDiffView(); !strings.Contains(view, "one.go") || !strings.Contains(view, "side-by-side") {
		t.Fatalf("wide diff view:\n%s", view)
	}
	updated, _ := m.handleDiffViewKey(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.diffView.file != 1 {
		t.Fatalf("file navigation=%d", m.diffView.file)
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	if m.diffView.mode != "unified" {
		t.Fatalf("narrow resize should select unified mode, got %q", m.diffView.mode)
	}
	if view := m.renderDiffView(); !strings.Contains(view, "two.go") || !strings.Contains(view, "@@") {
		t.Fatalf("narrow diff view:\n%s", view)
	}
}

func TestCoreViewsFitSmallTerminals(t *testing.T) {
	for _, themeName := range []string{"collomia", "plain"} {
		for _, size := range []struct{ width, height int }{{80, 24}, {40, 12}} {
			m := newTestModel(t)
			theme, _ := themeByName(themeName)
			m.applyTheme(theme)
			m.blocks = append(m.blocks, block{role: "system", content: strings.Repeat("long-unbroken-path/", 20)})
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = updated.(Model)
			assertScreenFits(t, m.View(), size.width, size.height)
			m.openTranscriptView()
			assertScreenFits(t, m.View(), size.width, size.height)
		}
	}
}

func assertScreenFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("screen has %d lines at %dx%d:\n%s", len(lines), width, height, view)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d is %d cells at width %d: %q", i, got, width, ansi.Strip(line))
		}
	}
}
