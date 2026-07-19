package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/skills"
)

func TestPlainThemeExists(t *testing.T) {
	plain, ok := themeByName("plain")
	if !ok {
		t.Fatal("plain theme missing")
	}
	if !plain.plain() {
		t.Fatal("plain theme should report plain()")
	}
	if got := plain.glamourStyle(); got != "notty" {
		t.Fatalf("plain glamour style = %q, want notty", got)
	}
	if colored := defaultTheme(); colored.plain() {
		t.Fatal("default theme must not be plain")
	}
}

func TestNoColorSelectsPlainTheme(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := newTestModel(t)
	if m.theme.Name != "plain" {
		t.Fatalf("NO_COLOR theme = %q, want plain", m.theme.Name)
	}
}

func TestNotifyTextSanitizes(t *testing.T) {
	if got := notifyText("run:\x07echo\x1b]2;x"); strings.ContainsAny(got, "\x07\x1b") {
		t.Fatalf("control characters survived: %q", got)
	}
	long := strings.Repeat("x", 500)
	if got := notifyText(long); len([]rune(got)) > 120 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long text not truncated: %d runes", len([]rune(got)))
	}
	if got := notifyText("short message"); got != "short message" {
		t.Fatalf("plain text altered: %q", got)
	}
}

func TestSkillPickerPrefillsInput(t *testing.T) {
	m := newTestModel(t)
	m.runtime.Skills = skills.Catalog{Skills: []skills.Skill{{Name: "release-notes", Description: "draft release notes"}}}
	if _, cmd := (&m).slash("/skills"); cmd != nil {
		t.Fatal("opening the skill picker should not start a turn")
	}
	if m.picker == nil {
		t.Fatal("skill picker did not open")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if got := m.input.Value(); !strings.Contains(got, `Use the "release-notes" skill`) {
		t.Fatalf("input not prefilled, got %q", got)
	}
}

func TestSkillPickerWithoutSkills(t *testing.T) {
	m := newTestModel(t)
	(&m).slash("/skills")
	if m.picker != nil {
		t.Fatal("picker should not open without skills")
	}
	last := m.blocks[len(m.blocks)-1]
	if !strings.Contains(last.content, "No skills discovered") {
		t.Fatalf("expected hint, got %q", last.content)
	}
}

func TestMCPPickerWithoutServers(t *testing.T) {
	m := newTestModel(t)
	(&m).slash("/mcp")
	if m.picker != nil {
		t.Fatal("picker should not open without servers")
	}
	last := m.blocks[len(m.blocks)-1]
	if !strings.Contains(last.content, "No MCP servers connected") {
		t.Fatalf("expected hint, got %q", last.content)
	}
}
