package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/app"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/version"
)

type block struct{ role, content string }
type runMsg struct {
	event *agent.Event
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
	started       time.Time
	turnStarted   time.Time

	theme  Theme
	styles styles
	tab    int

	vpInit      bool
	expandTools bool

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
		if m.pending != nil {
			return m.handleApprovalKey(msg)
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
				m.input.SetValue(m.palette[m.paletteSel].name + " ")
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
				line := m.palette[m.paletteSel].name
				if len(fields) > 1 {
					line += " " + strings.Join(fields[1:], " ")
				}
				m.input.Reset()
				m.paletteOn = false
				m.tab = tabChat
				quit := m.slash(line)
				m.updatePalette()
				m.layout()
				m.refresh()
				if quit {
					return m, tea.Quit
				}
				return m, nil
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
				quit := m.slash(value)
				m.updatePalette()
				m.layout()
				m.refresh()
				if quit {
					return m, tea.Quit
				}
				return m, nil
			}
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
				final, err := runtime.Agent.Run(ctx, value, func(event agent.Event) { events <- runMsg{event: &event} })
				events <- runMsg{done: true, final: final, err: err}
				close(events)
			}()
			m.updatePalette()
			m.layout()
			m.refresh()
			return m, tea.Batch(waitRun(events), m.spinner.Tick)
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
		if m.updatePalette() {
			m.layout()
			m.refresh()
		}
	}
	return m, tea.Batch(cmds...)
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

func (m *Model) handleEvent(event agent.Event) {
	switch event.Kind {
	case agent.EventDelta:
		if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].role != "assistant" {
			m.blocks = append(m.blocks, block{role: "assistant"})
		}
		m.blocks[len(m.blocks)-1].content += event.Text
	case agent.EventToolStart:
		m.blocks = append(m.blocks, block{role: "tool", content: event.Tool + "\x00" + event.Text})
	case agent.EventToolResult:
		summary := event.Text
		if len(summary) > 1200 {
			summary = summary[:1200] + "\n…"
		}
		m.blocks = append(m.blocks, block{role: "tool-result", content: summary})
	case agent.EventNotice:
		m.blocks = append(m.blocks, block{role: "system", content: event.Text})
	}
	m.refresh()
}

func (m Model) handleApprovalKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	inputArea += m.paletteHeight()
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
	b.WriteString(kv("provider", provider+"/"+model) + "\n")
	b.WriteString(kv("autonomy", m.runtime.Permissions.Mode()) + "\n")
	b.WriteString(kv("planning", fmt.Sprintf("%t", m.runtime.Agent.Plan())) + "\n")
	b.WriteString(kv("config", m.runtime.Config.Source) + "\n")
	b.WriteString(kv("theme", m.theme.Name) + "\n")
	b.WriteString(kv("uptime", time.Since(m.started).Round(time.Second).String()) + "\n\n")

	b.WriteString(h("Context") + "\n")
	b.WriteString(kv("usage", fmt.Sprintf("%d input / %d output tokens", usage.InputTokens, usage.OutputTokens)) + "\n")
	b.WriteString(kv("prompt", fmt.Sprintf("~%s of %s", formatTokens(estimate), windowText)) + "\n")
	b.WriteString(kv("messages", fmt.Sprintf("%d", m.runtime.Agent.MessageCount())) + "\n")
	b.WriteString("  " + contextGauge(m.theme, estimate, window, 30) + "\n\n")

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
		{"/", "open the command palette"},
		{"↑ ↓ (palette)", "select a command"},
		{"tab (palette)", "complete the selected command"},
		{"ctrl+t", "cycle Chat / Session / Help tabs"},
		{"ctrl+o", "expand / collapse finished tool output"},
		{"esc", "cancel turn · dismiss palette"},
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
	if m.paletteOn && m.pending == nil {
		sections = append(sections, m.renderPalette())
	}
	if m.pending != nil {
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
	body := fmt.Sprintf("%s\n%s  %s\n\n%s  approve once   %s  always for %s   %s  deny",
		title,
		m.styles.accent.Render(req.Tool), req.Action.Summary,
		badge("y", m.theme.Success), badge("a", m.theme.Warning), req.Tool, badge("n", m.theme.Error))
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
