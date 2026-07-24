package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/app"
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
	if !strings.Contains(last.content, "No skills installed") {
		t.Fatalf("expected hint, got %q", last.content)
	}
}

func TestAgentPickerShowsDelegatedOutcome(t *testing.T) {
	m := newTestModel(t)
	m.runtime.Team.Start("delegate-1", "security-audit", "Review authentication", false)
	m.runtime.Team.Finish("delegate-1", "No critical findings; test token sk-1234567890abcdef", []string{"internal/auth.go"}, "", "", nil)
	(&m).slash("/agents")
	if m.picker == nil || len(m.picker.matches) != 1 {
		t.Fatalf("agent picker missing: %+v", m.picker)
	}
	if got := m.picker.matches[0]; got.title != "security-audit" || !strings.Contains(got.desc, "done") {
		t.Fatalf("unexpected agent item: %+v", got)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	last := m.blocks[len(m.blocks)-1]
	if last.role != "panel" || last.title != "Agent · security-audit" || !strings.Contains(last.content, "No critical findings") || !strings.Contains(last.content, "[redacted]") || strings.Contains(last.content, "sk-1234567890abcdef") {
		t.Fatalf("agent detail panel missing: %+v", last)
	}
}

func TestAgentPickerWithoutDelegatesExplainsFeature(t *testing.T) {
	m := newTestModel(t)
	(&m).slash("/agents")
	if m.picker != nil {
		t.Fatal("empty agent list should not open a picker")
	}
	last := m.blocks[len(m.blocks)-1]
	if last.role != "panel" || !strings.Contains(last.content, "delegate tool") {
		t.Fatalf("expected delegated-agent guidance, got %+v", last)
	}
}

func TestAgentDetailsAndComparisonExplainVerificationScope(t *testing.T) {
	m := newTestModel(t)
	status := agent.DelegateStatus{
		ID: "delegate-1", Name: "writer", Task: "change parser", Write: true, Status: agent.DelegateDone,
		WriteScopes: []string{"internal/parser/"},
		Changed:     []string{"parser.go"}, Worktree: "/tmp/delegate-1", VerificationStatus: "passed",
		VerificationResults: []agent.DelegateVerification{{Purpose: "test", Command: "go test ./...", Status: "passed"}},
	}
	detail := m.renderAgentDetails(status)
	if !strings.Contains(detail, "Write scope: internal/parser/") || !strings.Contains(detail, "Child verification: passed") || !strings.Contains(detail, "retained child worktree only") || !strings.Contains(detail, "/agents verify delegate-1") {
		t.Fatalf("verification detail=%q", detail)
	}
	comparison := m.renderDelegateComparison([]app.DelegateCandidateSummary{{
		ID: "delegate-1", Name: "writer", Readiness: "verified", SelectableFiles: 1, SelectableHunks: 2, VerificationStatus: "passed",
	}})
	if !strings.Contains(comparison, "verification passed") || !strings.Contains(comparison, "grants no permission") {
		t.Fatalf("comparison=%q", comparison)
	}
}

func TestAgentDetailsExplainScopeViolations(t *testing.T) {
	m := newTestModel(t)
	status := agent.DelegateStatus{
		ID: "delegate-1", Name: "writer", Task: "change parser", Write: true, Status: agent.DelegateError,
		WriteScopes: []string{"internal/parser/"}, ScopeViolations: []string{"README.md"},
		Changed: []string{"README.md"}, Worktree: "/tmp/delegate-1",
	}
	detail := m.renderAgentDetails(status)
	if !strings.Contains(detail, "Write-scope violations") || !strings.Contains(detail, "README.md") || !strings.Contains(detail, "Guarded integration is blocked") {
		t.Fatalf("scope detail=%q", detail)
	}
}

func TestAgentControlPickerRequiresExplicitStopAction(t *testing.T) {
	m := newTestModel(t)
	ctx, cancel := context.WithCancel(t.Context())
	m.runtime.Team.Enqueue(agent.DelegateStart{ID: "delegate-1", Name: "review", Task: "review", Cancel: cancel})
	m.runtime.Team.MarkRunning("delegate-1")
	m.openAgentControlPicker()
	if m.picker == nil || len(m.picker.matches) != 1 {
		t.Fatalf("agent control picker missing: %+v", m.picker)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.picker == nil || len(m.picker.matches) != 3 {
		t.Fatalf("agent action picker missing: %+v", m.picker)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	select {
	case <-ctx.Done():
	default:
		t.Fatal("selecting the active agent did not cancel its context")
	}
	status, _ := m.runtime.Team.Get("delegate-1")
	if status.Status != agent.DelegateCancelling {
		t.Fatalf("status=%s", status.Status)
	}
}

func TestBusyComposerRunsOnlyLocalCommandsAndKeepsPromptDraft(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	m.setComposerValue("draft for the next turn")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.input.Value() != "draft for the next turn" || !m.busy {
		t.Fatalf("busy prompt draft changed: value=%q busy=%t", m.input.Value(), m.busy)
	}
	if last := m.blocks[len(m.blocks)-1]; last.role != "system" || !strings.Contains(last.content, "Draft kept") {
		t.Fatalf("missing draft notice: %+v", last)
	}
	m.setComposerValue("/status")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.input.Value() != "" || !m.busy {
		t.Fatalf("local command did not run cleanly: value=%q busy=%t", m.input.Value(), m.busy)
	}
	if last := m.blocks[len(m.blocks)-1]; last.role != "panel" || last.title != "Status" {
		t.Fatalf("status command did not run while busy: %+v", last)
	}
	m.setComposerValue("/model")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.input.Value() != "/model" {
		t.Fatalf("unsafe busy command was not retained: %q", m.input.Value())
	}
}

func TestBusyAgentSteeringCommandQueuesGuidance(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	m.runtime.Team.Enqueue(agent.DelegateStart{ID: "delegate-1", Name: "review", Task: "review"})
	m.runtime.Team.MarkRunning("delegate-1")
	m.setComposerValue("/agents steer delegate-1 inspect the cancellation path")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	status, _ := m.runtime.Team.Get("delegate-1")
	if status.PendingGuidance != 1 || len(status.Guidance) != 1 || !strings.Contains(status.Guidance[0], "cancellation path") {
		t.Fatalf("steering status=%+v", status)
	}
}

func TestPanelTextIsThemedNotDefault(t *testing.T) {
	for _, theme := range themes {
		got := theme.panelText()
		if theme.plain() {
			if got != "" {
				t.Fatalf("plain theme panelText should be empty, got %q", got)
			}
			continue
		}
		if got == "" || got == theme.Muted {
			t.Fatalf("theme %s: panelText should be a distinct tint of Muted, got %q (Muted %q)", theme.Name, got, theme.Muted)
		}
	}
}

func TestPanelBodyRendersInThemeColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	// The test harness's stdout isn't a terminal, so lipgloss normally
	// strips all color; force truecolor so the ANSI sequence is observable.
	prior := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prior) })

	m := newTestModel(t)
	out := m.renderPanel("Status", "workspace: /tmp")
	want := colorful.Color{}
	if c, err := colorful.Hex(m.theme.panelText()); err == nil {
		want = c
	}
	r, g, b := uint8(want.R*255+0.5), uint8(want.G*255+0.5), uint8(want.B*255+0.5)
	seq := fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
	if !strings.Contains(out, seq) {
		t.Fatalf("panel body missing the theme tint ANSI sequence %q in output:\n%s", seq, out)
	}
}

func TestPanelRendersTitledBorderedBox(t *testing.T) {
	m := newTestModel(t)
	longPath := "/workspace/" + strings.Repeat("deeply/nested/directories/", 10) + "config.json"
	out := m.renderPanel("Background processes", "[1] npm run dev — running\nsecond line\n"+longPath)
	lines := strings.Split(out, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected a box, got %d lines: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "Background processes") || !strings.Contains(lines[0], "╭") {
		t.Fatalf("title missing from top border: %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "╰") {
		t.Fatalf("bottom border missing: %q", lines[len(lines)-1])
	}
	// Every line of the box must be exactly as wide as the top border so the
	// frame is straight.
	want := lipgloss.Width(lines[0])
	for i, line := range lines {
		if got := lipgloss.Width(line); got != want {
			t.Fatalf("line %d width %d != top border width %d\n%s", i, got, want, out)
		}
	}
}

func TestPanelTruncatesLongTitle(t *testing.T) {
	m := newTestModel(t)
	m.width = 30
	out := m.renderPanel(strings.Repeat("t", 100), "content")
	lines := strings.Split(out, "\n")
	if top := lipgloss.Width(lines[0]); top != lipgloss.Width(lines[len(lines)-1]) {
		t.Fatalf("truncated-title border misaligned: top %d bottom %d", top, lipgloss.Width(lines[len(lines)-1]))
	}
	if !strings.Contains(lines[0], "…") {
		t.Fatalf("long title should be truncated: %q", lines[0])
	}
}

func TestStatusCommandUsesPanel(t *testing.T) {
	m := newTestModel(t)
	(&m).slash("/status")
	last := m.blocks[len(m.blocks)-1]
	if last.role != "panel" || last.title != "Status" {
		t.Fatalf("expected status panel, got %+v", last)
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
