package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/app"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
)

const agentIntegrationModalMaxWidth = 110

type agentIntegrationState struct {
	preview *app.DelegateIntegration
	hunks   [][]diffmodel.Hunk
	keep    [][]bool
	file    int
	cursor  int
}

type agentIntegrationAppliedMsg struct {
	paths []string
	err   error
}

type agentVerificationCompletedMsg struct {
	id      string
	results []agent.DelegateVerification
	err     error
}

func (m *Model) startAgentVerification(id string) (tea.Cmd, error) {
	if m.busy {
		return nil, fmt.Errorf("wait for the current turn to finish before verifying delegated changes")
	}
	plan, err := m.runtime.PrepareDelegateVerification(context.Background(), id)
	if err != nil {
		return nil, err
	}
	if len(plan.Commands) == 0 {
		return nil, fmt.Errorf("no standard verification commands were detected in delegated worktree %s", id)
	}
	m.busy = true
	m.turnStarted = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.input.Blur()
	m.addSystem(fmt.Sprintf("Verifying delegated agent %s (%s) in its isolated worktree with %d detected command(s). Each command uses the normal run_command permission and sandbox policy.", plan.Name, id, len(plan.Commands)))
	m.layout()
	m.refresh()
	runtime := m.runtime
	cmd := func() tea.Msg {
		results, runErr := runtime.VerifyDelegateSuite(ctx, id, nil)
		return agentVerificationCompletedMsg{id: id, results: results, err: runErr}
	}
	return tea.Batch(cmd, m.progressTick()), nil
}

func (m *Model) openAgentIntegration(id string) error {
	if m.busy {
		return fmt.Errorf("wait for the current turn to finish before integrating delegated changes")
	}
	preview, err := m.runtime.PrepareDelegateIntegration(context.Background(), id)
	if err != nil {
		return err
	}
	// Say this before the review rather than after it. Opening a selection UI
	// whose apply key is going to be refused wastes the user's attention on
	// choosing hunks that cannot be published from here.
	if preview.GraphOwned {
		return fmt.Errorf("%s is an Orchestrated Goal candidate; its node, attempt, and evidence belong to the graph, so it cannot be applied from here. Inspect it with /orchestrate status or in %s, and use /orchestrate reconcile and /orchestrate discard to manage the worktree", id, preview.Worktree)
	}
	state := &agentIntegrationState{preview: preview, hunks: make([][]diffmodel.Hunk, len(preview.Files)), keep: make([][]bool, len(preview.Files))}
	for i, file := range preview.Files {
		if file.Conflict != "" || file.AlreadyApplied || file.Unified == "" {
			continue
		}
		hunks, parseErr := diffmodel.ParseHunks(file.Unified)
		if parseErr != nil {
			preview.Files[i].Conflict = "diff cannot be selected safely: " + parseErr.Error()
			continue
		}
		state.hunks[i] = hunks
		state.keep[i] = make([]bool, len(hunks))
		for j := range state.keep[i] {
			state.keep[i][j] = true
		}
	}
	reviewStatus := "reviewed"
	for _, file := range preview.Files {
		if file.Conflict != "" {
			reviewStatus = "reviewed_with_conflicts"
			break
		}
	}
	if m.runtime.Team != nil {
		m.runtime.Team.MarkIntegrationReview(id, reviewStatus, "")
	}
	m.agentIntegration = state
	m.input.Blur()
	m.layout()
	m.refresh()
	return nil
}

func (m Model) handleAgentIntegrationKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.agentIntegration
	if state == nil || len(state.preview.Files) == 0 {
		m.agentIntegration = nil
		return m, nil
	}
	currentHunks := state.hunks[state.file]
	switch key.String() {
	case "esc", "q":
		m.agentIntegration = nil
		m.input.Focus()
		m.layout()
		m.refresh()
		return m, nil
	case "left", "[":
		state.file = (state.file - 1 + len(state.preview.Files)) % len(state.preview.Files)
		state.cursor = 0
	case "right", "]":
		state.file = (state.file + 1) % len(state.preview.Files)
		state.cursor = 0
	case "up", "k":
		if state.cursor > 0 {
			state.cursor--
		}
	case "down", "j":
		if state.cursor+1 < len(currentHunks) {
			state.cursor++
		}
	case " ":
		if len(currentHunks) > 0 {
			state.keep[state.file][state.cursor] = !state.keep[state.file][state.cursor]
		}
	case "x":
		if len(state.keep[state.file]) > 0 {
			any := false
			for _, keep := range state.keep[state.file] {
				any = any || keep
			}
			for i := range state.keep[state.file] {
				state.keep[state.file][i] = !any
			}
		}
	case "a":
		for i := range state.keep {
			for j := range state.keep[i] {
				state.keep[i][j] = true
			}
		}
	case "enter":
		var selections []app.DelegateIntegrationSelection
		for i, file := range state.preview.Files {
			if len(state.keep[i]) == 0 {
				continue
			}
			selections = append(selections, app.DelegateIntegrationSelection{Path: file.Path, Keep: append([]bool(nil), state.keep[i]...)})
		}
		id := state.preview.ID
		m.agentIntegration = nil
		m.input.Focus()
		m.busy = true
		m.turnStarted = time.Now()
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		m.layout()
		m.refresh()
		runtime := m.runtime
		return m, tea.Batch(func() tea.Msg {
			paths, err := runtime.ApplyDelegateIntegration(ctx, id, selections)
			return agentIntegrationAppliedMsg{paths: paths, err: err}
		}, m.progressTick())
	}
	m.refresh()
	return m, nil
}

func (m Model) renderAgentIntegration() string {
	state := m.agentIntegration
	inner := m.modalInnerWidth(agentIntegrationModalMaxWidth)
	file := state.preview.Files[state.file]
	var body strings.Builder
	body.WriteString(m.modalHeader("⇢", "Integrate delegated changes", m.theme.Accent, inner))
	body.WriteString("\n\n" + m.styles.muted.Render(ansi.Truncate(fmt.Sprintf("Agent %s · file %d/%d · branch %s", state.preview.Name, state.file+1, len(state.preview.Files), state.preview.Branch), inner, "…")))
	body.WriteString("\n" + m.styles.heading.Render(ansi.Truncate(file.Path, inner, "…")))
	if file.Conflict != "" {
		body.WriteString("\n\n" + m.styles.errText.Render(wrapAndLimit("Not selectable: "+file.Conflict+". Resolve the parent copy manually or ask the child to re-run from a fresh base.", inner, 4)))
		if file.ConflictPreview != "" {
			body.WriteString("\n\n" + m.styles.muted.Render(wrapAndLimit(file.ConflictPreview, inner, min(12, max(3, m.height-18)))))
		}
	} else if file.AlreadyApplied {
		body.WriteString("\n\n" + m.styles.success.Render("Already present in the parent workspace."))
	} else if len(state.hunks[state.file]) > 0 {
		if file.Reconciled {
			body.WriteString("\n" + m.styles.warning.Render("Three-way preview: non-overlapping parent and delegated edits are preserved."))
		}
		hunks := state.hunks[state.file]
		cursor := min(state.cursor, len(hunks)-1)
		hunk := hunks[cursor]
		selected := 0
		for _, keep := range state.keep[state.file] {
			if keep {
				selected++
			}
		}
		mark := "☐ excluded"
		color := m.theme.Muted
		if state.keep[state.file][cursor] {
			mark, color = "☑ included", m.theme.Success
		}
		body.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(fmt.Sprintf("%s · hunk %d/%d · %d selected", mark, cursor+1, len(hunks), selected)))
		body.WriteString("\n" + m.styles.muted.Render(fmt.Sprintf("@@ -%d,%d +%d,%d @@", hunk.AStart, hunk.ACount, hunk.BStart, hunk.BCount)))
		maxLines := min(14, max(2, m.height-16))
		lines := hunk.Lines
		hidden := 0
		if len(lines) > maxLines {
			hidden = len(lines) - maxLines
			lines = lines[:maxLines]
		}
		body.WriteString("\n\n")
		for i, line := range lines {
			line = ansi.Truncate(line, inner, "…")
			switch {
			case strings.HasPrefix(line, "+"):
				line = m.styles.success.Render(line)
			case strings.HasPrefix(line, "-"):
				line = m.styles.errText.Render(line)
			default:
				line = m.styles.muted.Render(line)
			}
			body.WriteString(line)
			if i < len(lines)-1 || hidden > 0 {
				body.WriteByte('\n')
			}
		}
		if hidden > 0 {
			body.WriteString(m.styles.muted.Render(fmt.Sprintf("… %d more lines", hidden)))
		}
	}
	body.WriteString("\n\n" + ansi.Wordwrap(
		badge("[ ]  File", m.theme.Border)+"  "+badge("↑↓  Hunk", m.theme.Border)+"  "+badge("Space  Toggle", m.theme.Warning)+"  "+badge("X  Toggle file", m.theme.Warning)+"  "+badge("Enter  Review/apply", m.theme.Success)+"  "+badge("Esc  Cancel", m.theme.Error),
		inner, ""))
	body.WriteString("\n" + m.styles.muted.Render(wrapAndLimit("Applying selected hunks uses the normal permission policy and rechecks base, parent, child, and any three-way preview after approval. Overlapping conflicts are never selected automatically. No commit, branch merge, push, or worktree removal occurs.", inner, 3)))
	return m.modalFrame(body.String(), m.theme.Accent, agentIntegrationModalMaxWidth)
}
