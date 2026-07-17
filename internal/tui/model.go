package tui

import (
	"context"
	"fmt"
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
)

type block struct{ role, content string }
type runMsg struct {
	event *agent.Event
	done  bool
	final string
	err   error
}

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
}

var (
	purple      = lipgloss.Color("99")
	pink        = lipgloss.Color("205")
	muted       = lipgloss.Color("243")
	green       = lipgloss.Color("42")
	red         = lipgloss.Color("203")
	yellow      = lipgloss.Color("220")
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(pink)
	userStyle   = lipgloss.NewStyle().Bold(true).Foreground(purple)
	toolStyle   = lipgloss.NewStyle().Foreground(muted)
	systemStyle = lipgloss.NewStyle().Foreground(muted).Italic(true)
)

func New(runtime *app.Runtime, broker *ApprovalBroker, initial string) Model {
	in := textarea.New()
	in.Placeholder = "Ask Collomia to build, debug, explain…"
	in.Prompt = "❯ "
	in.ShowLineNumbers = false
	in.SetHeight(3)
	in.CharLimit = 0
	in.Focus()
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(pink)
	m := Model{runtime: runtime, broker: broker, input: in, spinner: spin, started: time.Now()}
	m.blocks = append(m.blocks, block{role: "system", content: "Collomia is ready. Type /help for commands."})
	for _, warning := range runtime.Warnings {
		m.blocks = append(m.blocks, block{role: "error", content: warning.Error()})
	}
	if initial != "" {
		m.input.SetValue(initial)
	}
	return m
}

func (m Model) Init() tea.Cmd { return tea.Batch(textarea.Blink, m.spinner.Tick, m.broker.wait()) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.refresh()
		if !m.ready {
			m.ready = true
		}
	case approvalMsg:
		env := msg.envelope
		m.pending = &env
		m.input.Blur()
		m.refresh()
	case runMsg:
		if msg.event != nil {
			m.handleEvent(*msg.event)
		}
		if msg.done {
			m.busy = false
			m.cancel = nil
			m.input.Focus()
			if msg.err != nil {
				m.blocks = append(m.blocks, block{role: "error", content: msg.err.Error()})
			} else if strings.TrimSpace(msg.final) == "" {
				m.blocks = append(m.blocks, block{role: "system", content: "Turn complete."})
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
		if msg.String() == "ctrl+c" {
			if m.busy && m.cancel != nil {
				m.cancel()
				m.blocks = append(m.blocks, block{role: "system", content: "Cancelling current turn…"})
				m.refresh()
				return m, nil
			}
			return m, tea.Quit
		}
		if msg.String() == "esc" && m.busy && m.cancel != nil {
			m.cancel()
			return m, nil
		}
		if msg.String() == "enter" && !m.busy && !msg.Alt {
			value := strings.TrimSpace(m.input.Value())
			if value == "" {
				return m, nil
			}
			m.input.Reset()
			if strings.HasPrefix(value, "/") {
				quit := m.slash(value)
				m.refresh()
				if quit {
					return m, tea.Quit
				}
				return m, nil
			}
			m.blocks = append(m.blocks, block{role: "user", content: value})
			m.busy = true
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
			m.refresh()
			return m, tea.Batch(waitRun(events), m.spinner.Tick)
		}
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	if !m.busy && m.pending == nil {
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
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
		m.blocks = append(m.blocks, block{role: "tool", content: "◆ " + event.Tool + "  " + event.Text})
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
	m.refresh()
	return m, m.broker.wait()
}

func (m *Model) layout() {
	headerHeight := 2
	footerHeight := 2
	inputHeight := 5
	h := m.height - headerHeight - footerHeight - inputHeight
	if h < 3 {
		h = 3
	}
	if !m.ready {
		m.viewport = viewport.New(max(10, m.width), h)
	}
	m.viewport.Width = max(10, m.width)
	m.viewport.Height = h
	m.input.SetWidth(max(10, m.width-2))
}

func (m *Model) refresh() {
	if !m.ready {
		return
	}
	var b strings.Builder
	for _, block := range m.blocks {
		switch block.role {
		case "user":
			b.WriteString(userStyle.Render("YOU") + "\n" + block.content + "\n\n")
		case "assistant":
			b.WriteString(headerStyle.Render("COLLOMIA") + "\n" + m.renderMarkdown(block.content) + "\n")
		case "tool":
			b.WriteString(toolStyle.Render(block.content) + "\n")
		case "tool-result":
			b.WriteString(toolStyle.Render(indent(block.content, "  ")) + "\n\n")
		case "error":
			b.WriteString(lipgloss.NewStyle().Foreground(red).Render("Error: "+block.content) + "\n\n")
		default:
			b.WriteString(systemStyle.Render(block.content) + "\n\n")
		}
	}
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

func (m *Model) renderMarkdown(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(20, m.width-6)))
	if err != nil {
		return value
	}
	rendered, err := renderer.Render(value)
	if err != nil {
		return value
	}
	return strings.TrimSpace(rendered)
}

func (m Model) View() string {
	if !m.ready {
		return "Starting Collomia…"
	}
	p, model := m.runtime.Agent.Selection()
	mode := m.runtime.Permissions.Mode()
	plan := ""
	if m.runtime.Agent.Plan() {
		plan = "  PLAN"
	}
	header := headerStyle.Render("✿ COLLOMIA") + lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("  %s/%s  %s%s", p, model, strings.ToUpper(mode), plan))
	var input string
	if m.pending != nil {
		req := m.pending.request
		input = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(yellow).Padding(0, 1).Width(max(1, m.width-4)).Render(fmt.Sprintf("Permission required: %s\n[y] once   [a] always for %s   [n] deny", req.Action.Summary, req.Tool))
	} else {
		input = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(purple).Width(max(1, m.width-2)).Render(m.input.View())
	}
	status := "enter send  •  / commands  •  ctrl+c quit"
	if m.busy {
		status = m.spinner.View() + " working  •  esc cancel"
	}
	return header + "\n" + m.viewport.View() + "\n" + input + "\n" + lipgloss.NewStyle().Foreground(muted).Render(status)
}

func indent(value, prefix string) string {
	return prefix + strings.ReplaceAll(value, "\n", "\n"+prefix)
}
