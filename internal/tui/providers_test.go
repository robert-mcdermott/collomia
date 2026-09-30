package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

func feed(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestProvidersRunsSetupInsideTheSessionAndAppliesIt(t *testing.T) {
	m := newTestModel(t)
	_, cmd := (&m).slash("/providers ollama")
	if m.providerSetup == nil || cmd == nil {
		t.Fatal("/providers ollama must open the setup flow in the session")
	}
	if !strings.Contains(stripANSI(m.View()), "Re-verifying ollama") {
		t.Errorf("the flow must own the screen:\n%s", stripANSI(m.View()))
	}

	// The endpoint's catalog, then verification, arrive as messages.
	m, _ = feed(t, m, catalogMsg{models: []provider.ModelInfo{{ID: "qwen3-coder"}, {ID: "other"}}})
	m, _ = feed(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.providerSetup.stage != stageVerifying {
		t.Fatalf("stage = %d, want verifying", m.providerSetup.stage)
	}
	m, _ = feed(t, m, verifiedMsg{verification: setup.Verification{OK: true, ToolsOK: true, Reply: "ok"},
		limits:    provider.Limits{ContextWindow: 262144, MaxOutput: 16384, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsTable},
		reasoning: provider.ReasoningSupport{Source: provider.LimitsEndpoint}})

	// Keys belong to the flow while it is open: this lands in the limits
	// form, not the session composer.
	m, _ = feed(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m = typeKeys(t, m, "65536")
	if m.input.Value() != "" {
		t.Fatalf("typing reached the session composer: %q", m.input.Value())
	}
	m, _ = feed(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.providerSetup.stage != stageConfirm {
		t.Fatalf("stage = %d, problem %q", m.providerSetup.stage, m.providerSetup.limitsForm.err)
	}
	m, write := feed(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = feed(t, m, write())
	if m.providerSetup.stage != stageDone || !strings.Contains(stripANSI(m.View()), "return to your session") {
		t.Fatalf("stage = %d:\n%s", m.providerSetup.stage, stripANSI(m.View()))
	}
	m, exit := feed(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = feed(t, m, exit())

	if m.providerSetup != nil {
		t.Fatal("finishing must return to the session")
	}
	if got := m.runtime.Agent.ProviderSettings().Context; got != 65536 {
		t.Errorf("the running session must use the saved window from the next turn, got %d", got)
	}
	last := m.blocks[len(m.blocks)-1].content
	if !strings.Contains(last, "Saved ollama/qwen3-coder") || !strings.Contains(last, "context 65536") {
		t.Errorf("the session must say what was saved and applied, got %q", last)
	}
	path, _ := setup.GlobalPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &file); err != nil || file.Providers["ollama"]["context_window"] != float64(65536) {
		t.Errorf("file = %s", data)
	}
}

func TestProvidersClosesWithoutWriting(t *testing.T) {
	m := newTestModel(t)
	(&m).slash("/providers ollama")
	m, _ = feed(t, m, catalogMsg{models: []provider.ModelInfo{{ID: "qwen3-coder"}}})
	m, exit := feed(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if exit == nil {
		t.Fatal("esc on the first screen of a named run must leave the flow")
	}
	m, _ = feed(t, m, exit())
	if m.providerSetup != nil || !strings.Contains(m.blocks[len(m.blocks)-1].content, "nothing was written") {
		t.Errorf("closing must return to the session and say nothing changed")
	}
	if m.runtime.Agent.ProviderSettings().Context != 32768 {
		t.Error("nothing may change when nothing was written")
	}
}

func TestProvidersRefusesWhatItCannotDoSafely(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	(&m).slash("/providers")
	if m.providerSetup != nil || !strings.Contains(m.blocks[len(m.blocks)-1].content, "turn") {
		t.Error("changing providers under a running turn must be refused")
	}
	m.busy = false
	(&m).slash("/providers nothing-here")
	if m.providerSetup != nil || !strings.Contains(m.blocks[len(m.blocks)-1].content, "ollama") {
		t.Error("an unknown name must be refused with the names the file has")
	}
	(&m).slash("/setup")
	if m.providerSetup == nil {
		t.Error("/setup is an alias for /providers")
	}
}

func TestProvidersCtrlCLeavesTheFlowNotTheSession(t *testing.T) {
	m := newTestModel(t)
	(&m).slash("/providers ollama")
	m, exit := feed(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if exit == nil {
		t.Fatal("ctrl+c must leave the flow")
	}
	if _, ok := exit().(setupExitMsg); !ok {
		t.Fatal("ctrl+c inside /providers must close the flow, not quit Collomia")
	}
}
