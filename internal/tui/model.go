package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/robert-mcdermott/collomia/internal/app"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	runtimeevent "github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/version"
)

type block struct{ role, content string }
type runMsg struct {
	event *runtimeevent.Event
	done  bool
	final string
	err   error
}

const (
	tabChat = iota
	tabSession
	tabHelp
	tabCount
)

var tabNames = [tabCount]string{"Chat", "Session", "Help"}

type Model struct {
	runtime       *app.Runtime
	broker        *ApprovalBroker
	viewport      viewport.Model
	input         textarea.Model
	spinner       spinner.Model
	blocks        []block
	width, height int
	ready, busy   bool
	runEvents     chan runMsg
	cancel        context.CancelFunc
	pending       *approvalEnvelope
	hunkReview    *hunkReviewState
	question      *questionEnvelope
	picker        *picker
	started       time.Time
	turnStarted   time.Time

	theme  Theme
	styles styles
	tab    int

	vpInit      bool
	expandTools bool
	streaming   bool

	palette          []commandInfo
	paletteSel       int
	paletteOn        bool
	paletteDismissed bool
	lastInput        string

	renderer      *glamour.TermRenderer
	rendererWidth int
}

func New(runtime *app.Runtime, broker *ApprovalBroker, initial string) Model {
	theme := defaultTheme()
	if t, ok := themeByName(runtime.Config.Options.Theme); ok {
		theme = t
	}
	in := textarea.New()
	in.Placeholder = "Ask Collomia to build, debug, explain…  (/ for commands)"
	in.Prompt = "❯ "
	in.ShowLineNumbers = false
	in.SetHeight(3)
	in.CharLimit = 0
	in.Focus()
	spin := spinner.New()
	spin.Spinner = spinner.Points
	m := Model{runtime: runtime, broker: broker, input: in, spinner: spin, started: time.Now()}
	m.applyTheme(theme)
	for _, warning := range runtime.Warnings {
		m.blocks = append(m.blocks, block{role: "error", content: warning.Error()})
	}
	if initial != "" {
		m.input.SetValue(initial)
	}
	return m
}

// applyTheme installs a theme and restyles every themed component.
func (m *Model) applyTheme(t Theme) {
	m.theme = t
	m.styles = newStyles(t)
	m.spinner.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Secondary))
	m.input.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Secondary)).Bold(true)
	m.input.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted))
	m.input.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.input.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted))
	m.renderer = nil // force glamour rebuild with the new style
	setTerminalBackground(t.Background)
}

func (m Model) Init() tea.Cmd { return tea.Batch(textarea.Blink, m.spinner.Tick, m.broker.wait()) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.renderer = nil
		m.ready = true
		m.layout()
		m.refresh()
	case approvalMsg:
		env := msg.envelope
		m.pending = &env
		m.paletteOn = false
		m.input.Blur()
		ring()
		m.layout()
		m.refresh()
	case modelListMsg:
		// Only interrupt with the model catalog if the user isn't mid-task.
		if !m.busy && m.pending == nil && m.question == nil && m.picker == nil && strings.TrimSpace(m.input.Value()) == "" {
			m.openDiscoveredModels(msg)
		}
	case questionMsg:
		env := msg.envelope
		m.question = &env
		m.paletteOn = false
		text := "❓ " + env.question.Text
		for i, option := range env.question.Options {
			text += fmt.Sprintf("\n  %d. %s", i+1, option)
		}
		text += "\n\nType an answer (or an option number) and press enter; esc declines."
		m.blocks = append(m.blocks, block{role: "system", content: text})
		m.input.Reset()
		m.input.Focus()
		ring()
		m.layout()
		m.refresh()
	case runMsg:
		if msg.event != nil {
			m.handleEvent(*msg.event)
		}
		if msg.done {
			m.busy = false
			m.cancel = nil
			m.input.Focus()
			elapsed := time.Since(m.turnStarted).Round(time.Second / 10)
			// Ding only after long turns — the user has likely tabbed away.
			if elapsed > 10*time.Second {
				ring()
			}
			if msg.err != nil {
				m.blocks = append(m.blocks, block{role: "error", content: msg.err.Error()})
			} else if strings.TrimSpace(msg.final) == "" {
				m.blocks = append(m.blocks, block{role: "system", content: fmt.Sprintf("✓ turn complete in %s", elapsed)})
			}
			m.refresh()
		} else {
			cmds = append(cmds, waitRun(m.runEvents))
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.busy {
			cmds = append(cmds, cmd)
		}
	case tea.KeyMsg:
		if m.hunkReview != nil {
			return m.handleHunkReviewKey(msg)
		}
		if m.pending != nil {
			return m.handleApprovalKey(msg)
		}
		if m.question != nil {
			if handled, model, cmd := m.handleQuestionKey(msg); handled {
				return model, cmd
			}
		}
		if m.picker != nil {
			cmd, _ := m.handlePickerKey(msg)
			return m, cmd
		}
		key := msg.String()
		if key == "ctrl+c" {
			if m.busy && m.cancel != nil {
				m.cancel()
				m.blocks = append(m.blocks, block{role: "system", content: "Cancelling current turn…"})
				m.refresh()
				return m, nil
			}
			return m, tea.Quit
		}
		if key == "ctrl+t" {
			m.tab = (m.tab + 1) % tabCount
			m.refresh()
			return m, nil
		}
		if key == "ctrl+o" {
			m.expandTools = !m.expandTools
			m.refresh()
			return m, nil
		}
		if key == "esc" && m.busy && m.cancel != nil {
			m.cancel()
			return m, nil
		}
		if m.paletteOn {
			switch key {
			case "up", "ctrl+p":
				if m.paletteSel > 0 {
					m.paletteSel--
				} else {
					m.paletteSel = len(m.palette) - 1
				}
				return m, nil
			case "down", "ctrl+n":
				m.paletteSel = (m.paletteSel + 1) % len(m.palette)
				return m, nil
			case "tab":
				chosen := m.palette[m.paletteSel]
				m.input.SetValue(chosen.name + " ")
				m.input.CursorEnd()
				if m.updatePalette() {
					m.layout()
				}
				m.refresh()
				return m, nil
			case "esc":
				m.paletteDismissed = true
				if m.updatePalette() {
					m.layout()
					m.refresh()
				}
				return m, nil
			case "enter":
				fields := strings.Fields(m.input.Value())
				chosen := m.palette[m.paletteSel]
				line := chosen.name
				// Argument completions are already full command lines; for
				// bare commands, keep whatever arguments were typed.
				if !chosen.complete && len(fields) > 1 {
					line += " " + strings.Join(fields[1:], " ")
				}
				m.input.Reset()
				m.paletteOn = false
				m.tab = tabChat
				quit, cmd := m.slash(line)
				m.updatePalette()
				m.layout()
				m.refresh()
				if quit {
					return m, tea.Quit
				}
				return m, cmd
			}
		}
		if key == "enter" && !m.busy && !msg.Alt {
			value := strings.TrimSpace(m.input.Value())
			if value == "" {
				return m, nil
			}
			m.input.Reset()
			m.tab = tabChat
			if strings.HasPrefix(value, "/") {
				quit, cmd := m.slash(value)
				m.updatePalette()
				m.layout()
				m.refresh()
				if quit {
					return m, tea.Quit
				}
				return m, cmd
			}
			cmd := m.startTurn(value)
			m.updatePalette()
			m.layout()
			m.refresh()
			return m, cmd
		}
	}
	var cmd tea.Cmd
	// The viewport's default keymap also binds letters (u/d, j/k, b/f), which
	// would scroll the transcript while the user types a prompt. Only page
	// keys reach the viewport; mouse wheel events pass through unaffected.
	if key, isKey := msg.(tea.KeyMsg); !isKey || key.String() == "pgup" || key.String() == "pgdown" {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}
	if !m.busy && m.pending == nil {
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		// Typing "@" at a word boundary opens the file-mention picker; the
		// chosen path replaces the "@" in the prompt.
		if key, isKey := msg.(tea.KeyMsg); isKey && key.Type == tea.KeyRunes && string(key.Runes) == "@" {
			value := m.input.Value()
			if strings.HasSuffix(value, "@") && (len(value) == 1 || isMentionBoundary(value[len(value)-2])) {
				m.openFilePicker()
			}
		}
		if m.updatePalette() {
			m.layout()
			m.refresh()
		}
	}
	return m, tea.Batch(cmds...)
}

// startTurn submits one prompt to the agent and begins streaming events.
func (m *Model) startTurn(value string) tea.Cmd {
	m.blocks = append(m.blocks, block{role: "user", content: value})
	m.busy = true
	m.turnStarted = time.Now()
	m.input.Blur()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.runEvents = make(chan runMsg, 64)
	events := m.runEvents
	runtime := m.runtime
	go func() {
		final, err := runtime.Agent.Run(ctx, value, func(e runtimeevent.Event) {
			runtime.LogEvent(e)
			events <- runMsg{event: &e}
		})
		events <- runMsg{done: true, final: final, err: err}
		close(events)
	}()
	return tea.Batch(waitRun(events), m.spinner.Tick)
}

func isMentionBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '(' || c == '"' || c == '\''
}

func waitRun(events <-chan runMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return runMsg{done: true}
		}
		return msg
	}
}

func (m *Model) handleEvent(e runtimeevent.Event) {
	switch e.Kind {
	case runtimeevent.KindTextDelta:
		if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].role != "assistant" {
			m.blocks = append(m.blocks, block{role: "assistant"})
		}
		m.blocks[len(m.blocks)-1].content += e.Text
	case runtimeevent.KindToolStart:
		if e.Tool != nil {
			m.streaming = false
			m.blocks = append(m.blocks, block{role: "tool", content: e.Tool.Name + "\x00" + e.Tool.Summary})
		}
	case runtimeevent.KindToolOutput:
		// Live output from a running command: grow a tool-result block in
		// place so the user watches builds and tests as they happen.
		if e.Tool != nil && e.Tool.Output != "" {
			if !m.streaming || len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].role != "tool-result" {
				m.blocks = append(m.blocks, block{role: "tool-result"})
				m.streaming = true
			}
			m.blocks[len(m.blocks)-1].content += e.Tool.Output
		}
	case runtimeevent.KindToolResult:
		if e.Tool != nil {
			summary := e.Tool.Output
			if len(summary) > 1200 {
				summary = summary[:1200] + "\n…"
			}
			if m.streaming && len(m.blocks) > 0 && m.blocks[len(m.blocks)-1].role == "tool-result" {
				// The final result replaces the streamed view (it may add
				// error text and truncation markers).
				m.blocks[len(m.blocks)-1].content = summary
			} else {
				m.blocks = append(m.blocks, block{role: "tool-result", content: summary})
			}
			m.streaming = false
		}
	case runtimeevent.KindWarning:
		m.blocks = append(m.blocks, block{role: "system", content: e.Text})
	}
	m.refresh()
}

// handleQuestionKey routes input while an ask_user question is pending:
// enter submits the typed answer (a bare option number selects that
// option), esc declines. Other keys fall through to normal input editing.
func (m Model) handleQuestionKey(key tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	switch key.String() {
	case "enter":
		answer := strings.TrimSpace(m.input.Value())
		options := m.question.question.Options
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(options) {
			answer = options[n-1]
		}
		m.question.reply <- answer
		m.question = nil
		m.blocks = append(m.blocks, block{role: "user", content: answer})
		m.input.Reset()
		m.layout()
		m.refresh()
		return true, m, m.broker.wait()
	case "esc":
		m.question.reply <- ""
		m.question = nil
		m.blocks = append(m.blocks, block{role: "system", content: "Question declined."})
		m.input.Reset()
		m.layout()
		m.refresh()
		return true, m, m.broker.wait()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return true, m, cmd
}

func (m Model) handleApprovalKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if strings.ToLower(key.String()) == "h" {
		return m.tryEnterHunkReview(), nil
	}
	var decision *permission.Decision
	switch strings.ToLower(key.String()) {
	case "y", "enter":
		value := permission.Decision{Allow: true}
		decision = &value
	case "a":
		value := permission.Decision{Allow: true, Always: true}
		decision = &value
	case "n", "esc":
		value := permission.Decision{}
		decision = &value
	}
	if decision == nil {
		return m, nil
	}
	m.pending.reply <- *decision
	m.pending = nil
	m.input.Focus()
	m.layout()
	m.refresh()
	return m, m.broker.wait()
}

func (m *Model) layout() {
	headerHeight := 2
	statusHeight := 1
	inputArea := 5
	if m.pending != nil {
		inputArea = 7
	}
	if m.hunkReview != nil {
		inputArea = 20
	}
	if m.picker != nil {
		inputArea += m.pickerHeight()
	} else {
		inputArea += m.paletteHeight()
	}
	h := m.height - headerHeight - statusHeight - inputArea
	if h < 3 {
		h = 3
	}
	if !m.vpInit {
		m.viewport = viewport.New(max(10, m.width), h)
		m.vpInit = true
	}
	m.viewport.Width = max(10, m.width)
	m.viewport.Height = h
	m.input.SetWidth(max(10, m.width-2))
}

func (m *Model) refresh() {
	if !m.ready {
		return
	}
	switch m.tab {
	case tabSession:
		m.viewport.SetContent(m.sessionContent())
		m.viewport.GotoTop()
	case tabHelp:
		m.viewport.SetContent(m.helpContent())
		m.viewport.GotoTop()
	default:
		m.viewport.SetContent(m.chatContent())
		m.viewport.GotoBottom()
	}
}

func (m *Model) chatContent() string {
	var b strings.Builder
	b.WriteString(m.banner() + "\n\n")
	for i, block := range m.blocks {
		switch block.role {
		case "user":
			b.WriteString(m.styles.userBadge.Render("YOU") + "\n" + block.content + "\n\n")
		case "assistant":
			b.WriteString(m.styles.botBadge.Render("✿ COLLOMIA") + "\n" + m.renderMarkdown(block.content) + "\n\n")
		case "tool":
			name, summary, _ := strings.Cut(block.content, "\x00")
			b.WriteString(m.styles.tool.Render("⚙ ") + m.styles.toolName.Render(name) + m.styles.tool.Render("  "+summary) + "\n")
		case "tool-result":
			b.WriteString(m.renderToolResult(i) + "\n\n")
		case "error":
			b.WriteString(m.styles.errText.Render("✖ "+block.content) + "\n\n")
		default:
			b.WriteString(m.styles.system.Render("· "+block.content) + "\n\n")
		}
	}
	return b.String()
}

// Tool results at or below this line count are always shown in full;
// collapsing them would not save meaningful space.
const toolCollapseThreshold = 4

// renderToolResult shows the newest tool output in full while the turn is
// still running, then collapses it to a one-line summary once the agent moves
// on. ctrl+o expands every collapsed result for inspection.
func (m *Model) renderToolResult(i int) string {
	content := m.blocks[i].content
	lines := strings.Count(content, "\n") + 1
	current := m.busy && i == len(m.blocks)-1
	if m.expandTools || current || lines <= toolCollapseThreshold {
		return m.styles.toolResult.Render(indent(content, "  │ "))
	}
	return m.styles.toolResult.Render("  ▸ ") +
		m.styles.tool.Render(fmt.Sprintf("%d lines hidden · ", lines)) +
		m.styles.toolName.Render("ctrl+o") + m.styles.tool.Render(" to expand")
}

const asciiBanner = `╔═╗╔═╗╦  ╦  ╔═╗╔╦╗╦╔═╗
║  ║ ║║  ║  ║ ║║║║║╠═╣
╚═╝╚═╝╩═╝╩═╝╚═╝╩ ╩╩╩ ╩`

func (m *Model) banner() string {
	if m.width < 30 {
		return m.styles.brand.Render("✿ Collomia")
	}
	art := gradient(asciiBanner, m.theme.Primary, m.theme.Secondary)
	provider, model := m.runtime.Agent.Selection()
	sub := m.styles.muted.Render(fmt.Sprintf("✿ %s · %s/%s · theme %s", version.String(), provider, model, m.theme.Name))
	tips := m.styles.system.Render("type a prompt · / commands · ctrl+t tabs · ctrl+o tool output")
	return art + "\n" + sub + "\n" + tips
}

func (m *Model) sessionContent() string {
	h := m.styles.heading.Render
	kv := func(key, value string) string {
		return m.styles.accent.Render(fmt.Sprintf("  %-12s", key)) + value
	}
	provider, model := m.runtime.Agent.Selection()
	usage := m.runtime.Agent.Usage()
	estimate, window := m.runtime.Agent.ContextEstimate()
	windowText := "unknown"
	if window > 0 {
		windowText = formatTokens(window)
	}
	var b strings.Builder
	b.WriteString(h("Session") + "\n")
	b.WriteString(kv("workspace", m.runtime.Workspace) + "\n")
	if m.runtime.Session != nil {
		id := m.runtime.Session.Meta.ID
		if title := m.runtime.Session.Meta.Title; title != "" {
			id += "  (" + title + ")"
		}
		b.WriteString(kv("session", id) + "\n")
	}
	b.WriteString(kv("provider", provider+"/"+model) + "\n")
	b.WriteString(kv("autonomy", m.runtime.Permissions.Mode()) + "\n")
	b.WriteString(kv("planning", fmt.Sprintf("%t", m.runtime.Agent.Plan())) + "\n")
	b.WriteString(kv("config", m.runtime.Config.Source) + "\n")
	b.WriteString(kv("theme", m.theme.Name) + "\n")
	b.WriteString(kv("uptime", time.Since(m.started).Round(time.Second).String()) + "\n\n")

	b.WriteString(h("Context") + "\n")
	usageText := fmt.Sprintf("%d input / %d output tokens", usage.InputTokens, usage.OutputTokens)
	if usage.CachedTokens > 0 {
		usageText += fmt.Sprintf(" (%d cached)", usage.CachedTokens)
	}
	b.WriteString(kv("usage", usageText) + "\n")
	b.WriteString(kv("prompt", fmt.Sprintf("~%s of %s", formatTokens(estimate), windowText)) + "\n")
	b.WriteString(kv("messages", fmt.Sprintf("%d", m.runtime.Agent.MessageCount())) + "\n")
	b.WriteString("  " + contextGauge(m.theme, estimate, window, 30) + "\n\n")

	if current := m.runtime.Plan.Current(); current != nil && len(current.Steps) > 0 {
		b.WriteString(h("Plan") + "\n")
		marks := map[string]string{"pending": "○", "in_progress": "◐", "done": "●", "blocked": "✗", "skipped": "−"}
		colors := map[string]string{"pending": m.theme.Muted, "in_progress": m.theme.Warning, "done": m.theme.Success, "blocked": m.theme.Error, "skipped": m.theme.Muted}
		b.WriteString(kv("goal", current.Goal) + "\n")
		for _, step := range current.Steps {
			mark := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[step.Status])).Render(marks[step.Status])
			line := fmt.Sprintf("  %s %d. %s", mark, step.ID, step.Title)
			if step.Evidence != "" {
				line += m.styles.muted.Render("  — " + step.Evidence)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if changed := m.runtime.Changes.Changed(); len(changed) > 0 {
		b.WriteString(h("Changed files") + "\n")
		for _, path := range changed {
			display := path
			if rel, err := filepath.Rel(m.runtime.Workspace, path); err == nil && !strings.HasPrefix(rel, "..") {
				display = rel
			}
			b.WriteString("  " + m.styles.accent.Render(display) + "\n")
		}
		b.WriteString(m.styles.muted.Render("  /diff to review · /undo to revert the latest") + "\n\n")
	}

	if procs := m.runtime.Processes.Snapshot(); len(procs) > 0 {
		b.WriteString(h("Background processes") + "\n")
		for _, p := range procs {
			mark := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Success)).Render("●")
			if !p.Running {
				mark = m.styles.muted.Render("○")
			}
			b.WriteString(fmt.Sprintf("  %s [%d] %s  %s\n", mark, p.ID, m.styles.accent.Render(p.Command), m.styles.muted.Render(p.Status)))
		}
		b.WriteString(m.styles.muted.Render("  /ps to manage · all stopped at exit") + "\n\n")
	}

	if agents := m.runtime.Team.Snapshot(); len(agents) > 0 {
		b.WriteString(h("Agents") + "\n")
		marks := map[string]string{"running": "◐", "done": "●", "error": "✗"}
		colors := map[string]string{"running": m.theme.Warning, "done": m.theme.Success, "error": m.theme.Error}
		for _, a := range agents {
			mark := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[a.Status])).Render(marks[a.Status])
			kind := "read"
			if a.Write {
				kind = "write"
			}
			line := fmt.Sprintf("  %s %s  %s", mark, m.styles.accent.Render(a.Name), m.styles.muted.Render("("+kind+")"))
			b.WriteString(line + "\n")
			if a.Status == "running" {
				continue
			}
			if len(a.Changed) > 0 {
				b.WriteString(m.styles.muted.Render(fmt.Sprintf("      changed %d file(s) — worktree %s (branch %s)", len(a.Changed), a.Worktree, a.Branch)) + "\n")
			} else if a.Status == "error" {
				b.WriteString(m.styles.muted.Render("      "+a.Summary) + "\n")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(h("Providers") + "\n")
	for _, name := range m.runtime.Config.ProviderNames() {
		p := m.runtime.Config.Providers[name]
		marker := "  "
		if name == provider {
			marker = m.styles.success.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%s  [%s]  %s\n", marker, m.styles.accent.Render(name), p.Type, p.Model))
	}
	b.WriteString("\n" + h("Tools") + "\n  " + strings.Join(m.runtime.Registry.Names(), ", ") + "\n\n")

	b.WriteString(h("Skills") + "\n")
	if len(m.runtime.Skills.Skills) == 0 {
		b.WriteString(m.styles.muted.Render("  none discovered") + "\n")
	}
	for _, skill := range m.runtime.Skills.Skills {
		b.WriteString("  " + m.styles.accent.Render(skill.Name) + "  " + m.styles.muted.Render(skill.Description) + "\n")
	}
	b.WriteString("\n" + h("MCP servers") + "\n")
	servers := m.runtime.MCP.Servers()
	if len(servers) == 0 {
		b.WriteString(m.styles.muted.Render("  none connected") + "\n")
	} else {
		sort.Strings(servers)
		b.WriteString("  " + strings.Join(servers, ", ") + "\n")
	}
	b.WriteString("\n" + h("Themes") + "\n")
	for _, t := range themes {
		marker := "  "
		if t.Name == m.theme.Name {
			marker = m.styles.success.Render("▸ ")
		}
		swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Primary)).Render("●") +
			lipgloss.NewStyle().Foreground(lipgloss.Color(t.Secondary)).Render("●") +
			lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Render("●")
		b.WriteString(fmt.Sprintf("%s%s %s\n", marker, swatch, t.Name))
	}
	return b.String()
}

func (m *Model) helpContent() string {
	var b strings.Builder
	b.WriteString(m.styles.heading.Render("Slash commands") + "\n")
	for _, cmd := range slashCommands {
		label := cmd.name
		if cmd.args != "" {
			label += " " + cmd.args
		}
		b.WriteString("  " + m.styles.accent.Render(fmt.Sprintf("%-26s", label)) + m.styles.muted.Render(cmd.desc) + "\n")
	}
	b.WriteString("\n" + m.styles.heading.Render("Keys") + "\n")
	keys := [][2]string{
		{"enter", "send prompt / run selected command"},
		{"alt+enter", "insert newline"},
		{"/", "open the command palette (with argument completion)"},
		{"@", "fuzzy-pick a workspace file into the prompt"},
		{"↑ ↓ (palette)", "select a command or completion"},
		{"tab (palette)", "complete the selected command"},
		{"ctrl+t", "cycle Chat / Session / Help tabs"},
		{"ctrl+o", "expand / collapse finished tool output"},
		{"y / a / n (approval)", "approve once / always for this tool / deny"},
		{"h (approval)", "review a multi-hunk file write and approve only some hunks"},
		{"esc", "cancel turn · dismiss palette or picker"},
		{"pgup/pgdn", "scroll the transcript"},
		{"ctrl+c", "cancel turn, again to quit"},
	}
	for _, k := range keys {
		b.WriteString("  " + m.styles.accent.Render(fmt.Sprintf("%-26s", k[0])) + m.styles.muted.Render(k[1]) + "\n")
	}
	return b.String()
}

func (m *Model) renderMarkdown(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	width := max(20, m.width-6)
	if m.renderer == nil || m.rendererWidth != width {
		renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle(m.theme.glamourStyle()), glamour.WithWordWrap(width))
		if err != nil {
			return value
		}
		m.renderer = renderer
		m.rendererWidth = width
	}
	rendered, err := m.renderer.Render(value)
	if err != nil {
		return value
	}
	return strings.TrimSpace(rendered)
}

func (m Model) View() string {
	if !m.ready {
		return "Starting Collomia…"
	}
	sections := []string{m.renderHeader(), m.viewport.View()}
	if m.picker != nil && m.pending == nil {
		sections = append(sections, m.renderPicker())
	} else if m.paletteOn && m.pending == nil {
		sections = append(sections, m.renderPalette())
	}
	if m.hunkReview != nil {
		sections = append(sections, m.renderHunkReview())
	} else if m.pending != nil {
		sections = append(sections, m.renderApproval())
	} else {
		sections = append(sections, m.styles.inputBox.Width(max(1, m.width-2)).Render(m.input.View()))
	}
	sections = append(sections, m.renderStatusBar())
	return strings.Join(sections, "\n")
}

func (m Model) renderHeader() string {
	var tabs []string
	for i, name := range tabNames {
		if i == m.tab {
			tabs = append(tabs, m.styles.tabActive.Render(name))
		} else {
			tabs = append(tabs, m.styles.tabInactive.Render(name))
		}
	}
	left := m.styles.brand.Render(" ✿ collomia ")
	row := left + strings.Join(tabs, "")
	rule := m.styles.rule.Render(strings.Repeat("─", max(0, m.width)))
	return row + "\n" + rule
}

func (m Model) renderApproval() string {
	req := m.pending.request
	title := m.styles.warning.Render("⚠ Permission required")
	body := fmt.Sprintf("%s\n%s  %s", title, m.styles.accent.Render(req.Tool), req.Action.Summary)
	if req.Reason != "" {
		body += "\n" + m.styles.warning.Render(req.Reason)
	}
	if req.Action.Preview != "" {
		preview := req.Action.Preview
		lines := strings.Split(strings.TrimRight(preview, "\n"), "\n")
		const maxPreview = 14
		if len(lines) > maxPreview {
			lines = append(lines[:maxPreview], fmt.Sprintf("… %d more diff lines (ctrl+o after approval shows full output)", len(lines)-maxPreview))
		}
		var colored []string
		for _, line := range lines {
			switch {
			case strings.HasPrefix(line, "+"):
				colored = append(colored, lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Success)).Render(line))
			case strings.HasPrefix(line, "-"):
				colored = append(colored, lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Error)).Render(line))
			default:
				colored = append(colored, m.styles.statusBase.Render(line))
			}
		}
		body += "\n" + strings.Join(colored, "\n")
	}
	body += fmt.Sprintf("\n\n%s  approve once   %s  always for %s   %s  deny",
		badge("y", m.theme.Success), badge("a", m.theme.Warning), req.Tool, badge("n", m.theme.Error))
	if req.Tool == "write_file" && req.Action.Preview != "" {
		if hunks, err := diffmodel.ParseHunks(req.Action.Preview); err == nil && len(hunks) >= 2 {
			body += fmt.Sprintf("   %s  review %d hunks", badge("h", m.theme.Accent), len(hunks))
		}
	}
	return m.styles.approvalBox.Width(max(1, m.width-2) - 2).Render(body)
}

func (m Model) renderStatusBar() string {
	provider, model := m.runtime.Agent.Selection()
	mode := m.runtime.Permissions.Mode()
	modeColor := m.theme.Success
	switch mode {
	case "workspace":
		modeColor = m.theme.Warning
	case "autopilot":
		modeColor = m.theme.Error
	}
	estimate, window := m.runtime.Agent.ContextEstimate()

	left := badge("✿", m.theme.Primary)
	left += m.styles.statusBase.Render(" ") + badge(provider+"/"+model, m.theme.Border)
	left += m.styles.statusBase.Render(" ") + badge(strings.ToUpper(mode), modeColor)
	if m.runtime.Agent.Plan() {
		left += m.styles.statusBase.Render(" ") + badge("PLAN", m.theme.Accent)
	}
	if current := m.runtime.Plan.Current(); current != nil && len(current.Steps) > 0 {
		done := 0
		for _, step := range current.Steps {
			if step.Status == "done" || step.Status == "skipped" {
				done++
			}
		}
		left += m.styles.statusBase.Render(" ") + badge(fmt.Sprintf("tasks %d/%d", done, len(current.Steps)), m.theme.Secondary)
	}
	if active := m.runtime.Team.Active(); active > 0 {
		left += m.styles.statusBase.Render(" ") + badge(fmt.Sprintf("agents %d", active), m.theme.Accent)
	}
	if running := m.runtime.Processes.Running(); running > 0 {
		left += m.styles.statusBase.Render(" ") + badge(fmt.Sprintf("procs %d", running), m.theme.Secondary)
	}
	left += m.styles.statusBase.Render(" " + contextGauge(m.theme, estimate, window, 10) + " ")

	var right string
	if m.busy {
		elapsed := time.Since(m.turnStarted).Round(time.Second)
		right = m.styles.statusKey.Render(m.spinner.View()) + m.styles.statusBase.Render(fmt.Sprintf(" working %s · esc cancel ", elapsed))
	} else {
		right = m.styles.statusBase.Render("enter send · ") +
			m.styles.statusKey.Render("/") + m.styles.statusBase.Render(" commands · ") +
			m.styles.statusKey.Render("ctrl+t") + m.styles.statusBase.Render(" tabs · ") +
			m.styles.statusKey.Render("ctrl+c") + m.styles.statusBase.Render(" quit ")
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + m.styles.statusBase.Render(strings.Repeat(" ", gap)) + right
}

func indent(value, prefix string) string {
	return prefix + strings.ReplaceAll(value, "\n", "\n"+prefix)
}

// ring sounds the terminal bell so an unattended approval, question, or
// finished long turn gets the user's attention. Terminals map this to their
// configured notification (sound, badge, or nothing) — never intrusive.
func ring() { _, _ = os.Stderr.WriteString("\a") }
