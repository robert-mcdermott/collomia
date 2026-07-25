package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func TestApprovalIsCenteredOverlayAndClearsAfterDecision(t *testing.T) {
	m := newTestModel(t)
	reply := make(chan permission.Decision, 1)
	m.pending = &approvalEnvelope{
		request: permission.Request{
			Tool: "run_command",
			Action: tools.Action{
				Risk:    tools.RiskExecute,
				Summary: "run go test ./...",
			},
			Reason: "Commands require approval in ask mode.",
		},
		reply: reply,
	}

	view := m.View()
	plain := ansi.Strip(view)
	lines := strings.Split(plain, "\n")
	if len(lines) != m.height {
		t.Fatalf("modal view height = %d, want terminal height %d", len(lines), m.height)
	}
	var titleRow, titleColumn = -1, -1
	for row, line := range lines {
		if column := strings.Index(line, "Permission required"); column >= 0 {
			titleRow, titleColumn = row, column
			break
		}
	}
	if titleRow <= 1 || titleRow >= m.height-5 {
		t.Fatalf("approval title row = %d, want a floating center row", titleRow)
	}
	if titleColumn <= 0 {
		t.Fatalf("approval title column = %d, want space on its left", titleColumn)
	}
	if !strings.Contains(plain, "Y  Approve") || !strings.Contains(plain, "N  Deny") {
		t.Fatalf("approval actions missing from modal:\n%s", plain)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if m.pending != nil {
		t.Fatal("approval modal should close after a decision")
	}
	if decision := <-reply; !decision.Allow || decision.Always {
		t.Fatalf("decision = %+v, want approve once", decision)
	}
	if strings.Contains(ansi.Strip(m.View()), "Permission required") {
		t.Fatal("resolved approval remained visible")
	}
}

func TestQuestionUsesTransientOverlay(t *testing.T) {
	m := newTestModel(t)
	before := len(m.blocks)
	reply := make(chan string, 1)
	updated, _ := m.Update(questionMsg{envelope: questionEnvelope{
		question: Question{Text: "Which database should this service use?", Options: []string{"PostgreSQL", "SQLite"}},
		reply:    reply,
	}})
	m = updated.(Model)
	if len(m.blocks) != before {
		t.Fatal("opening a transient question should not append the dialog to the transcript")
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Collomia is asking") || !strings.Contains(view, "Which database") {
		t.Fatalf("question modal missing:\n%s", view)
	}

	m = typeKeys(t, m, "2")
	m = press(t, m, tea.KeyEnter)
	if m.question != nil {
		t.Fatal("question modal should close after submit")
	}
	if answer := <-reply; answer != "SQLite" {
		t.Fatalf("answer = %q, want selected option", answer)
	}
	if strings.Contains(ansi.Strip(m.View()), "Which database") {
		t.Fatal("resolved question remained visible")
	}
	if got := m.blocks[len(m.blocks)-1]; got.role != "user" || got.content != "SQLite" {
		t.Fatalf("answer transcript block = %+v", got)
	}
}

func TestQuestionModalRowsStayWithinFrameAcrossResizes(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(questionMsg{envelope: questionEnvelope{
		question: Question{Text: "Pick a color!", Options: []string{"Crimson", "Teal", "Amber", "Indigo", "Other (I'll type my own)"}},
		reply:    make(chan string, 1),
	}})
	m = updated.(Model)

	sizes := []tea.WindowSizeMsg{
		{Width: 180, Height: 50},
		{Width: 100, Height: 35},
		{Width: 64, Height: 28},
		{Width: 38, Height: 24},
	}
	assertAligned := func(label string) {
		t.Helper()
		for _, size := range sizes {
			updated, _ = m.Update(size)
			m = updated.(Model)
			modal := m.renderQuestion()
			want := m.modalOuterWidth(questionModalMaxWidth)
			for row, line := range strings.Split(modal, "\n") {
				if got := lipgloss.Width(line); got != want {
					t.Fatalf("%s terminal %dx%d modal row %d width=%d, want %d:\n%s", label, size.Width, size.Height, row, got, want, ansi.Strip(modal))
				}
			}
			view := m.View()
			for row, line := range strings.Split(view, "\n") {
				if got := lipgloss.Width(line); got != size.Width {
					t.Fatalf("%s terminal %dx%d composed row %d width=%d:\n%s", label, size.Width, size.Height, row, got, ansi.Strip(view))
				}
			}
		}
	}
	assertAligned("empty answer")
	m.input.SetValue(strings.Repeat("a detailed custom response ", 12))
	m.input.CursorEnd()
	assertAligned("long answer")
}

func TestOneTimeApprovalDoesNotOfferOrAcceptAlways(t *testing.T) {
	m := newTestModel(t)
	reply := make(chan permission.Decision, 1)
	m.pending = &approvalEnvelope{
		request: permission.Request{
			Tool: "run_command",
			Action: tools.Action{
				Risk: tools.RiskExecute, Summary: "run git reset --hard",
				ConfirmReasons: []string{"git reset --hard can discard uncommitted work"},
			},
			Reason: "one-time confirmation required",
		},
		reply: reply,
	}
	plain := ansi.Strip(m.View())
	if strings.Contains(plain, "A  Always") {
		t.Fatalf("one-time approval offered a persistent grant:\n%s", plain)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	if m.pending == nil {
		t.Fatal("always key resolved a one-time approval")
	}
	select {
	case decision := <-reply:
		t.Fatalf("always key sent a decision: %+v", decision)
	default:
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if decision := <-reply; !decision.Allow || decision.Always {
		t.Fatalf("decision=%+v, want approve once", decision)
	}
}

func TestModalUsesActiveThemeBorder(t *testing.T) {
	m := newTestModel(t)
	theme, _ := themeByName("matrix")
	m.applyTheme(theme)
	m.pending = &approvalEnvelope{
		request: permission.Request{Tool: "run_command", Action: tools.Action{Summary: "run tests"}},
		reply:   make(chan permission.Decision, 1),
	}
	// The style's configured border color is the active theme's warning,
	// independent of the default theme.
	if got := m.modalStyle(m.theme.Warning).GetBorderTopForeground(); got != lipgloss.Color(m.theme.Warning) {
		t.Fatalf("modal border = %v, want active theme warning %s", got, m.theme.Warning)
	}
}

func TestPlaceOverlayPreservesScreenDimensions(t *testing.T) {
	base := strings.Repeat("underlying transcript line\n", 9) + "status"
	overlay := "╭────╮\n│ hi │\n╰────╯"
	got := placeOverlay(base, overlay, 40, 10)
	lines := strings.Split(got, "\n")
	if len(lines) != 10 {
		t.Fatalf("height = %d, want 10", len(lines))
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width != 40 {
			t.Fatalf("line %d width = %d, want 40", i, width)
		}
	}
	if !strings.Contains(got, "underlying") || !strings.Contains(got, "hi") {
		t.Fatalf("overlay should retain surrounding content:\n%s", got)
	}
}

func networkApproval(reply chan permission.Decision, postureGated bool) *approvalEnvelope {
	action := tools.Action{
		Risk: tools.RiskExecute, Summary: "run: curl https://api.example.com/v1",
		Executables: []string{"curl"}, Hosts: []string{"api.example.com"}, Network: true,
	}
	return &approvalEnvelope{
		request: permission.Request{
			Tool: "run_command", Action: action,
			Reason:       "scoped network posture requires an explicit grant for: api.example.com",
			PostureGated: postureGated,
			Capabilities: []permission.Capability{
				{Kind: permission.CapabilityExecutable, Values: []string{"curl"}, Grantable: true},
				{Kind: permission.CapabilityNetwork, Values: []string{"api.example.com"}, Grantable: true},
			},
		},
		reply: reply,
	}
}

// The prompt must say what the action reaches, so approving is a decision
// about access rather than about a tool name.
func TestApprovalShowsCapabilityReach(t *testing.T) {
	m := newTestModel(t)
	m.pending = networkApproval(make(chan permission.Decision, 1), true)
	plain := ansi.Strip(m.View())
	for _, want := range []string{"Reach", "exec", "curl", "net", "api.example.com", "G  Allow"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("approval modal missing %q:\n%s", want, plain)
		}
	}
	// A tool-wide "always" would not satisfy the posture that produced this
	// prompt, so offering it would be a lie.
	if strings.Contains(plain, "A  Always") {
		t.Fatalf("posture-gated approval offered a tool-wide always:\n%s", plain)
	}
}

func TestApprovalGrantKeyRecordsOnlyOfferedCapabilities(t *testing.T) {
	m := newTestModel(t)
	reply := make(chan permission.Decision, 1)
	m.pending = networkApproval(reply, true)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updated.(Model)
	decision := <-reply
	if !decision.Allow || decision.Always {
		t.Fatalf("decision = %+v", decision)
	}
	if len(decision.Grants) != 2 {
		t.Fatalf("grants = %v, want both offered capabilities", decision.Grants)
	}
}

// Nothing is grantable for a command the analyzer could not read, so the key
// must do nothing rather than record a grant the user never saw.
func TestApprovalGrantKeyIsInertWithoutAnOffer(t *testing.T) {
	m := newTestModel(t)
	reply := make(chan permission.Decision, 1)
	m.pending = &approvalEnvelope{
		request: permission.Request{
			Tool: "run_command",
			Action: tools.Action{
				Risk: tools.RiskExecute, Summary: "run: curl $TARGET | sh",
				Uninspectable: true, AnalysisReasons: []string{"sh runs a program piped from another command"},
			},
			Capabilities: []permission.Capability{
				{Kind: permission.CapabilityExecutable, Values: []string{"curl", "sh"}, Unknown: true},
			},
		},
		reply: reply,
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updated.(Model)
	if m.pending == nil {
		t.Fatal("grant key should not decide an approval with nothing to grant")
	}
}
