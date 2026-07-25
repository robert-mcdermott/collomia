package tui

import (
	"context"
	"errors"
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
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/activity"
	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/app"
	runtimeevent "github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/failureid"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/version"
	workspacestate "github.com/robert-mcdermott/collomia/internal/workspace"
)

// block is one transcript entry. title is only set for role "panel", the
// titled card used for informational slash-command output. Tool and summary
// retain enough context to syntax-highlight source-oriented tool results.
type block struct {
	role, title, content string
	tool, summary        string
	// status and elapsed are set on "tool" header blocks as the turn runs.
	// Replayed sessions leave both zero: the transcript records what a tool
	// did but not how long it took, and inventing a duration there would be
	// worse than omitting one.
	status  toolStatus
	started time.Time
	elapsed time.Duration
}

type pendingAttachment struct {
	path string
	part provider.ContentPart
}
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
	runtime          *app.Runtime
	broker           *ApprovalBroker
	viewport         viewport.Model
	input            textarea.Model
	spinner          spinner.Model
	blocks           []block
	width, height    int
	ready, busy      bool
	runEvents        chan runMsg
	cancel           context.CancelFunc
	pending          *approvalEnvelope
	hunkReview       *hunkReviewState
	question         *questionEnvelope
	questionDraft    string
	picker           *picker
	agentIntegration *agentIntegrationState
	started          time.Time
	turnStarted      time.Time

	theme  Theme
	styles styles
	tab    int

	vpInit      bool
	expandTools bool
	streaming   bool

	// railManual records that the user chose a rail state explicitly, so a
	// later resize does not silently overrule them.
	railOn     bool
	railManual bool

	palette          []commandInfo
	paletteSel       int
	paletteOn        bool
	paletteDismissed bool
	lastInput        string

	renderer      *glamour.TermRenderer
	rendererWidth int

	chatFollow   bool
	tabOffsets   [tabCount]int
	transcript   *transcriptState
	diffView     *diffViewState
	activityView *activityViewState
	activities   []activity.Item

	promptHistory      []string
	historyIndex       int
	historyDraft       string
	sessionDrafts      map[string]string
	pendingAttachments []pendingAttachment
	sessionAttachments map[string][]pendingAttachment

	workspaceStatus     workspacestate.GitStatus
	workspaceLoading    bool
	workspaceGeneration int
	workspaceRefreshed  time.Time
}

func New(runtime *app.Runtime, broker *ApprovalBroker, initial string) Model {
	theme := defaultTheme()
	if t, ok := themeByName(runtime.Config.Options.Theme); ok {
		theme = t
	}
	// NO_COLOR (https://no-color.org) wins over any configured theme.
	if os.Getenv("NO_COLOR") != "" {
		theme, _ = themeByName("plain")
	}
	in := newComposer()
	spin := spinner.New()
	spin.Spinner = spinner.Points
	m := Model{
		runtime: runtime, broker: broker, input: in, spinner: spin,
		started: time.Now(), chatFollow: true, sessionDrafts: map[string]string{}, sessionAttachments: map[string][]pendingAttachment{},
		workspaceLoading: true, workspaceGeneration: 1,
	}
	m.applyTheme(theme)
	m.rebuildTranscript()
	for _, warning := range runtime.Warnings {
		m.blocks = append(m.blocks, block{role: "error", content: warning.Error()})
	}
	if initial != "" {
		m.setComposerValue(initial)
	}
	return m
}

// applyTheme installs a theme and restyles every themed component.
func (m *Model) applyTheme(t Theme) {
	m.theme = t
	m.styles = newStyles(t)
	m.spinner.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Secondary))
	m.restyleComposer()
	m.renderer = nil // force glamour rebuild with the new style
	if t.Background == "" {
		ResetTerminalBackground()
	} else {
		setTerminalBackground(t.Background)
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.progressTick(), m.broker.wait(), inspectWorkspaceCmd(m.runtime.Workspace, m.workspaceGeneration))
}

func (m Model) progressTick() tea.Cmd {
	if m.runtime.Config.Options.ReducedMotion {
		return nil
	}
	return m.spinner.Tick
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	// Terminals that speak a keyboard disambiguation protocol report
	// shift+enter and ctrl+enter as CSI sequences Bubble Tea 1.x does not
	// recognise, so they never arrive as a KeyMsg and have to be intercepted
	// ahead of the type switch.
	if isNewlineSequence(msg) {
		if !m.composerActive() {
			return m, nil
		}
		m.insertComposerNewline()
		m.layout()
		m.refresh()
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		oldOffset := m.viewport.YOffset
		wasBottom := !m.vpInit || m.viewport.AtBottom()
		m.width, m.height = msg.Width, msg.Height
		m.renderer = nil
		m.ready = true
		m.layout()
		m.refreshAt(oldOffset, wasBottom)
		m.resizeFullScreenViews()
	case approvalMsg:
		env := msg.envelope
		m.pending = &env
		m.paletteOn = false
		m.input.Blur()
		m.alert("Approval needed: " + env.request.Action.Summary)
		m.layout()
		m.refresh()
	case modelListMsg:
		// Only interrupt with the model catalog if the user isn't mid-task.
		if !m.busy && m.pending == nil && m.question == nil && m.picker == nil && strings.TrimSpace(m.input.Value()) == "" {
			m.openDiscoveredModels(msg)
		}
	case providerStatusMsg:
		m.replaceProviderStatusPanel(msg.statuses)
	case workspaceStatusMsg:
		if msg.generation == m.workspaceGeneration {
			m.workspaceStatus = msg.status
			m.workspaceLoading = false
			m.workspaceRefreshed = time.Now()
			m.refresh()
		}
	case editorFinishedMsg:
		m.finishExternalEditor(msg)
	case composerEditorMsg:
		m.finishComposerEditor(msg)
	case agentIntegrationAppliedMsg:
		m.busy = false
		m.cancel = nil
		m.input.Focus()
		if msg.err != nil {
			m.addError(msg.err)
		} else {
			m.addSystem(fmt.Sprintf("Integrated %d delegated file(s): %s. The isolated worktree and branch were retained.", len(msg.paths), strings.Join(msg.paths, ", ")))
		}
		cmds = append(cmds, m.refreshWorkspaceStatus())
		m.layout()
		m.refresh()
	case agentVerificationCompletedMsg:
		m.busy = false
		m.cancel = nil
		m.input.Focus()
		passed := 0
		for _, result := range msg.results {
			if result.Status == "passed" {
				passed++
			}
		}
		if msg.err != nil {
			m.addError(fmt.Errorf("delegated verification %s stopped after %d/%d passing command(s): %w", msg.id, passed, len(msg.results), msg.err))
		} else {
			m.addSystem(fmt.Sprintf("Delegated verification %s passed %d command(s) in the isolated child worktree. This is evidence only; publication still requires review and permission, and the combined parent workspace should be verified after integration.", msg.id, passed))
		}
		m.layout()
		m.refresh()
	case questionMsg:
		env := msg.envelope
		m.question = &env
		m.paletteOn = false
		m.questionDraft = m.input.Value()
		m.input.Reset()
		m.input.Focus()
		m.alert("Collomia has a question: " + env.question.Text)
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
			m.settleRunningTools()
			elapsed := time.Since(m.turnStarted).Round(time.Second / 10)
			// Ding on failure, and after long turns — the user has likely
			// tabbed away.
			if msg.err != nil {
				m.alert("Turn failed: " + failureid.Display(msg.err))
			} else if elapsed > 10*time.Second {
				m.alert(fmt.Sprintf("Turn finished after %s", elapsed))
			}
			if msg.err != nil {
				m.blocks = append(m.blocks, block{role: "error", content: failureid.Display(msg.err)})
			} else if strings.TrimSpace(msg.final) == "" {
				m.blocks = append(m.blocks, block{role: "system", content: fmt.Sprintf("✓ turn complete in %s", elapsed)})
			}
			cmds = append(cmds, m.refreshWorkspaceStatus())
			m.refresh()
		} else {
			cmds = append(cmds, waitRun(m.runEvents))
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.busy && !m.runtime.Config.Options.ReducedMotion {
			cmds = append(cmds, cmd)
			if m.tab == tabSession {
				m.refresh()
			}
		}
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		if m.agentIntegration != nil {
			return m.handleAgentIntegrationKey(msg)
		}
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
		if m.transcript != nil {
			return m.handleTranscriptKey(msg)
		}
		if m.diffView != nil {
			return m.handleDiffViewKey(msg)
		}
		if m.activityView != nil {
			return m.handleActivityKey(msg)
		}
		if m.tab == tabSession && strings.EqualFold(msg.String(), "r") {
			cmd := m.refreshWorkspaceStatus()
			m.refresh()
			return m, cmd
		}
		if m.keyIs("next_tab", key) {
			next := (m.tab + 1) % tabCount
			var cmd tea.Cmd
			if next == tabSession {
				cmd = m.refreshWorkspaceStatus()
			}
			m.switchTab(next)
			return m, cmd
		}
		if m.keyIs("session_picker", key) {
			if m.busy {
				m.addError(fmt.Errorf("wait for the current turn to finish before switching sessions"))
				m.refresh()
				return m, nil
			}
			m.openSessionPicker()
			return m, nil
		}
		if m.keyIs("agent_control", key) {
			if m.runtime.Team.Active() > 0 {
				m.openAgentControlPicker()
			} else {
				m.openAgentPicker()
			}
			return m, nil
		}
		if m.keyIs("toggle_tool_output", key) {
			m.expandTools = !m.expandTools
			m.refresh()
			return m, nil
		}
		if m.keyIs("context_rail", key) {
			if m.width < railMinTotalWidth {
				m.addSystem(fmt.Sprintf("The context rail needs a window at least %d columns wide; this one is %d.", railMinTotalWidth, m.width))
				m.refresh()
				return m, nil
			}
			m.toggleRail()
			m.layout()
			m.refresh()
			return m, nil
		}
		if m.keyIs("compose_editor", key) {
			return m.openComposerEditor()
		}
		if m.keyIs("transcript_view", key) {
			m.openTranscriptView()
			return m, nil
		}
		if m.keyIs("diff_view", key) {
			m.openDiffView()
			return m, nil
		}
		if m.keyIs("page_up", key) {
			m.viewport.PageUp()
			m.chatFollow = false
			m.tabOffsets[m.tab] = m.viewport.YOffset
			return m, nil
		}
		if m.keyIs("page_down", key) {
			m.viewport.PageDown()
			if m.tab == tabChat && m.viewport.AtBottom() {
				m.chatFollow = true
			}
			m.tabOffsets[m.tab] = m.viewport.YOffset
			return m, nil
		}
		if m.keyIs("scroll_top", key) {
			m.viewport.GotoTop()
			if m.tab == tabChat {
				m.chatFollow = false
			}
			m.tabOffsets[m.tab] = m.viewport.YOffset
			return m, nil
		}
		if m.keyIs("scroll_bottom", key) {
			m.viewport.GotoBottom()
			if m.tab == tabChat {
				m.chatFollow = true
			}
			m.tabOffsets[m.tab] = m.viewport.YOffset
			return m, nil
		}
		if key == "esc" && m.busy && m.cancel != nil && !m.paletteOn {
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
				m.setComposerValue(chosen.name + " ")
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
				m.switchTab(tabChat)
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
		if (key == "up" || key == "down") && !m.busy && m.navigatePromptHistory(key == "up") {
			m.updatePalette()
			m.layout()
			m.refresh()
			return m, nil
		}
		if key == "enter" && !msg.Alt {
			// A draft that is visibly unfinished extends instead of sending.
			// Most users never discover a newline chord, so the common way to
			// write a multi-line prompt has to be plain Enter.
			if draft, extend := continueDraft(m.input.Value()); extend {
				m.setComposerValue(draft)
				m.layout()
				m.refresh()
				return m, nil
			}
			value := strings.TrimSpace(m.input.Value())
			if value == "" {
				return m, nil
			}
			if m.busy {
				if !strings.HasPrefix(value, "/") {
					m.addSystem("Draft kept while the current turn runs. Use a local slash command now, or press enter after the turn finishes to send it.")
					m.refresh()
					return m, nil
				}
				if !busySlashAllowed(value) {
					m.addError(fmt.Errorf("%s is unavailable while the current turn is running; the command remains in the composer", strings.Fields(value)[0]))
					m.refresh()
					return m, nil
				}
				m.input.Reset()
				m.switchTab(tabChat)
				quit, cmd := m.slash(value)
				m.updatePalette()
				m.layout()
				m.refresh()
				if quit {
					return m, tea.Quit
				}
				return m, cmd
			}
			m.input.Reset()
			m.switchTab(tabChat)
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
	// would scroll the transcript while the user types a prompt. Keyboard
	// scrolling is handled explicitly above; mouse events still pass through.
	if _, isKey := msg.(tea.KeyMsg); !isKey {
		m.viewport, cmd = m.viewport.Update(msg)
		if m.tab == tabChat && !m.viewport.AtBottom() {
			m.chatFollow = false
		}
		cmds = append(cmds, cmd)
	}
	if m.pending == nil {
		if key, isKey := msg.(tea.KeyMsg); isKey && key.String() != "up" && key.String() != "down" {
			if !m.busy {
				m.leavePromptHistory()
			}
		}
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		// Typing "@" at a word boundary opens the file-mention picker; the
		// chosen path replaces the "@" in the prompt.
		if key, isKey := msg.(tea.KeyMsg); !m.busy && isKey && key.Type == tea.KeyRunes && string(key.Runes) == "@" {
			value := m.input.Value()
			if strings.HasSuffix(value, "@") && (len(value) == 1 || isMentionBoundary(value[len(value)-2])) {
				m.openFilePicker()
			}
		}
		// Height first, unconditionally: the editor grows and shrinks as the
		// draft wraps, and the transcript's height is derived from it, so a
		// keystroke that adds a row has to re-run layout even when the
		// palette is unchanged.
		resized := m.syncComposerHeight()
		if m.updatePalette() || resized {
			m.layout()
			m.refresh()
		}
	}
	return m, tea.Batch(cmds...)
}

// startTurn submits one prompt to the agent and begins streaming events.
func (m *Model) startTurn(value string) tea.Cmd {
	parts, err := m.readPendingAttachments()
	if err != nil {
		m.setComposerValue(value)
		m.addError(err)
		m.refresh()
		return nil
	}
	m.recordPrompt(value)
	m.blocks = append(m.blocks, block{role: "user", content: displayMessageWithAttachments(value, parts)})
	m.pendingAttachments = nil
	if key := m.sessionDraftKey(); key != "" {
		delete(m.sessionAttachments, key)
	}
	m.busy = true
	m.turnStarted = time.Now()
	m.input.Focus()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.runEvents = make(chan runMsg, 64)
	events := m.runEvents
	runtime := m.runtime
	go func() {
		final, err := runtime.Agent.RunWithParts(ctx, value, parts, func(e runtimeevent.Event) {
			runtime.LogEvent(e)
			events <- runMsg{event: &e}
		})
		if persistenceErr := runtime.PersistenceError(); persistenceErr != nil {
			persistenceErr = fmt.Errorf("session persistence failed: %w", persistenceErr)
			if err == nil {
				err = persistenceErr
			} else {
				err = errors.Join(err, persistenceErr)
			}
		}
		if err != nil && failureid.ID(err) == "" {
			err = failureid.Ensure(err)
			failureEvent := runtimeevent.New(runtimeevent.KindError)
			failureEvent.Error = err.Error()
			failureEvent.FailureID = failureid.ID(err)
			runtime.LogEvent(failureEvent)
		} else {
			err = failureid.Ensure(err)
		}
		events <- runMsg{done: true, final: final, err: err}
		close(events)
	}()
	return tea.Batch(waitRun(events), m.progressTick())
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
	wasActivityBottom := m.activityView != nil && m.activityView.cursor == len(m.activities)-1
	m.activities = activity.Append(m.activities, e, activity.DefaultLimit)
	switch e.Kind {
	case runtimeevent.KindTextDelta:
		if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].role != "assistant" {
			m.blocks = append(m.blocks, block{role: "assistant"})
		}
		m.blocks[len(m.blocks)-1].content += e.Text
	case runtimeevent.KindToolStart:
		if e.Tool != nil {
			m.streaming = false
			m.blocks = append(m.blocks, block{
				role:    "tool",
				content: e.Tool.Name + "\x00" + e.Tool.Summary,
				status:  toolRunning,
				started: time.Now(),
			})
		}
	case runtimeevent.KindToolOutput:
		// Live output from a running command: grow a tool-result block in
		// place so the user watches builds and tests as they happen.
		if e.Tool != nil && e.Tool.Output != "" {
			if !m.streaming || len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].role != "tool-result" {
				m.blocks = append(m.blocks, block{role: "tool-result", tool: e.Tool.Name, summary: e.Tool.Summary})
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
				m.blocks[len(m.blocks)-1].tool = e.Tool.Name
				m.blocks[len(m.blocks)-1].summary = e.Tool.Summary
			} else {
				m.blocks = append(m.blocks, block{role: "tool-result", content: summary, tool: e.Tool.Name, summary: e.Tool.Summary})
			}
			m.completeToolBlock(e.Tool.Name, e.Tool.IsError)
			m.streaming = false
		}
	case runtimeevent.KindWarning:
		m.blocks = append(m.blocks, block{role: "system", content: e.Text})
	}
	if m.transcript != nil {
		atBottom := m.transcript.viewport.AtBottom()
		m.rebuildTranscriptView()
		if atBottom {
			m.transcript.cursor = len(m.blocks) - 1
			m.rebuildTranscriptView()
			m.transcript.viewport.GotoBottom()
		}
	}
	if m.activityView != nil {
		m.rebuildActivityView()
		if wasActivityBottom && len(m.activities) > 0 {
			m.activityView.cursor = len(m.activities) - 1
			m.rebuildActivityView()
			m.activityView.viewport.GotoBottom()
		}
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
		m.setComposerValue(m.questionDraft)
		m.questionDraft = ""
		m.layout()
		m.refresh()
		return true, m, m.broker.wait()
	case "esc":
		m.question.reply <- ""
		m.question = nil
		m.blocks = append(m.blocks, block{role: "system", content: "Question declined."})
		m.setComposerValue(m.questionDraft)
		m.questionDraft = ""
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
		// Same single authority the dialog uses to decide whether to offer it.
		if !m.pending.request.AllowsAlways {
			return m, nil
		}
		value := permission.Decision{Allow: true, Always: true}
		decision = &value
	case "g":
		// Approve, and remember exactly the reach shown — these executables
		// and these endpoints — for the rest of the session. A later action
		// is automatic only when every dimension it reaches is covered.
		kinds := permission.GrantableKinds(m.pending.request.Capabilities)
		if len(kinds) == 0 {
			return m, nil
		}
		value := permission.Decision{Allow: true, Grants: kinds}
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
	// Width first: the editor wraps against it, and the wrap decides how many
	// rows the draft needs and therefore how much height is left over.
	m.input.SetWidth(max(10, m.width-2))
	m.syncComposerHeight()
	inputArea := m.composerHeight()
	if m.picker != nil {
		inputArea += m.pickerHeight()
	} else {
		inputArea += m.paletteHeight()
	}
	h := m.height - headerHeight - statusHeight - inputArea
	if h < 3 {
		h = 3
	}
	// The context rail borrows columns from the transcript only. Narrowing
	// the composer as well would punish the user for opening a reference
	// panel by shrinking the thing they are typing into.
	if !m.vpInit {
		m.viewport = viewport.New(max(10, m.bodyWidth()), h)
		m.vpInit = true
	}
	m.viewport.Width = max(10, m.bodyWidth())
	m.viewport.Height = h
}

func (m *Model) refresh() {
	if !m.ready {
		return
	}
	m.refreshAt(m.viewport.YOffset, m.viewport.AtBottom())
}

func (m *Model) refreshAt(offset int, wasBottom bool) {
	if !m.ready {
		return
	}
	switch m.tab {
	case tabSession:
		m.viewport.SetContent(m.sessionContent())
		m.viewport.SetYOffset(offset)
	case tabHelp:
		m.viewport.SetContent(m.helpContent())
		m.viewport.SetYOffset(offset)
	default:
		m.viewport.SetContent(m.chatContent())
		if m.chatFollow || wasBottom {
			m.viewport.GotoBottom()
		} else {
			m.viewport.SetYOffset(offset)
		}
	}
	m.tabOffsets[m.tab] = m.viewport.YOffset
}

func (m *Model) switchTab(next int) {
	m.tabOffsets[m.tab] = m.viewport.YOffset
	m.tab = next
	offset := m.tabOffsets[m.tab]
	if m.tab == tabChat && m.chatFollow {
		offset = 0
	}
	m.refreshAt(offset, m.tab == tabChat && m.chatFollow)
}

func (m *Model) chatContent() string {
	if len(m.blocks) == 0 {
		return m.renderEmptyState()
	}
	var b strings.Builder
	b.WriteString(m.banner() + "\n\n")
	for i, block := range m.blocks {
		switch block.role {
		case "user":
			b.WriteString(m.styles.userBadge.Render("YOU") + "\n" + block.content + "\n\n")
		case "assistant":
			b.WriteString(m.styles.botBadge.Render("✿ COLLOMIA") + "\n" + m.renderMarkdown(block.content) + "\n\n")
		case "tool":
			b.WriteString(m.renderToolHeader(block) + "\n")
		case "tool-result":
			b.WriteString(m.renderToolResult(i) + "\n\n")
		case "error":
			b.WriteString(m.styles.errText.Render("✖ "+block.content) + "\n\n")
		case "panel":
			b.WriteString(m.renderPanel(block.title, block.content) + "\n\n")
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
	entry := m.blocks[i]
	content := entry.content
	lines := strings.Count(content, "\n") + 1
	current := m.busy && i == len(m.blocks)-1
	if m.expandTools || current || lines <= toolCollapseThreshold {
		if highlighted, ok := m.highlightToolResult(entry); ok {
			return indent(highlighted, "  │ ")
		}
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
	tips := m.styles.system.Render(fmt.Sprintf("type a prompt · / commands · %s tabs · %s tool output", m.binding("next_tab"), m.binding("toggle_tool_output")))
	return m.bannerArt() + "\n" + tips
}

// bannerArt is the logo and the one-line identity beneath it, without the
// keyboard tips. The empty state carries its own, longer set of openers and
// would otherwise say much the same thing twice, two lines apart.
func (m *Model) bannerArt() string {
	if m.width < 30 {
		return m.styles.brand.Render("✿ Collomia")
	}
	art := gradient(asciiBanner, m.theme.Primary, m.theme.Secondary)
	providerName, model := m.runtime.Agent.Selection()
	sub := m.styles.muted.Render(fmt.Sprintf("✿ %s · %s/%s · theme %s", version.String(), providerName, model, m.theme.Name))
	return art + "\n" + sub
}

// sessionTwoColumnWidth is where the Session tab splits in two. Below it a
// column would be too narrow for the longer values (a workspace path, a
// sandbox summary) to survive without truncation.
const sessionTwoColumnWidth = 120

// sessionColumnGap separates the two columns.
const sessionColumnGap = 3

// sessionContent lays the Session tab out in cards. On a wide terminal it is
// two columns: the tab is a reference sheet a reader scans rather than a
// narrative they read top to bottom, and a single column of it scrolled for
// several screens while half the display sat empty.
func (m *Model) sessionContent() string {
	width := max(1, m.width)
	if m.width < sessionTwoColumnWidth {
		return m.sessionSections(width)
	}
	column := (width - sessionColumnGap) / 2
	// The sections already separate themselves with a blank line, which is
	// exactly the card boundary, so the layout does not need its own notion
	// of where one topic ends and the next begins.
	cards := strings.Split(strings.TrimRight(m.sessionSections(column), "\n"), "\n\n")
	return packColumns(cards, column, sessionColumnGap)
}

// packColumns distributes cards over two columns, splitting where the running
// height first reaches half the total so the columns end at roughly the same
// row.
func packColumns(cards []string, column, gap int) string {
	heights := make([]int, len(cards))
	total := 0
	for i, card := range cards {
		heights[i] = strings.Count(card, "\n") + 1
		total += heights[i] + 1 // the blank line that follows each card
	}
	split, running := len(cards), 0
	for i := range cards {
		if running*2 >= total {
			split = i
			break
		}
		running += heights[i] + 1
	}
	left := strings.Split(strings.Join(cards[:split], "\n\n"), "\n")
	right := strings.Split(strings.Join(cards[split:], "\n\n"), "\n")

	var b strings.Builder
	for i := 0; i < max(len(left), len(right)); i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		row := ""
		if i < len(left) {
			// A section that never learned to budget its width would
			// otherwise run straight through the second column.
			row = ansi.Truncate(left[i], column, "…")
		}
		if i < len(right) && strings.TrimSpace(right[i]) != "" {
			row = fitLine(row, column+gap) + ansi.Truncate(right[i], column, "…")
		}
		b.WriteString(strings.TrimRight(row, " "))
	}
	return b.String()
}

func (m *Model) sessionSections(width int) string {
	h := m.styles.heading.Render
	kv := func(key, value string) string {
		return fitLine(m.styles.accent.Render(fmt.Sprintf("  %-12s", key))+value, max(1, width))
	}
	providerName, model := m.runtime.Agent.Selection()
	usage := m.runtime.Agent.Usage()
	estimate, window := m.runtime.Agent.ContextEstimate()
	windowText := "unknown"
	if window > 0 {
		windowText = formatTokens(window)
	}
	activeProfile, reasoningEffort, tokenBudget, costBudget := m.runtime.Agent.Profile()
	if activeProfile == "" {
		activeProfile = "default"
	}
	if reasoningEffort == "" {
		reasoningEffort = "provider default"
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
	b.WriteString(kv("provider", providerName+"/"+model) + "\n")
	b.WriteString(kv("agent", activeProfile) + "\n")
	b.WriteString(kv("reasoning", reasoningEffort) + "\n")
	b.WriteString(kv("autonomy", m.runtime.Permissions.Mode()) + "\n")
	b.WriteString(kv("planning", fmt.Sprintf("%t", m.runtime.Agent.Plan())) + "\n")
	b.WriteString(kv("config", m.runtime.Config.Source) + "\n")
	b.WriteString(kv("theme", m.theme.Name) + "\n")
	b.WriteString(kv("uptime", time.Since(m.started).Round(time.Second).String()) + "\n\n")

	b.WriteString(h("Workspace") + "\n")
	b.WriteString(kv("git", m.gitStatusText()) + "\n")
	b.WriteString(kv("trust", m.projectTrustText()) + "\n")
	if m.projectConfigurationQuarantined() {
		b.WriteString(m.styles.muted.Render("  action: review the project configuration, then run collo trust") + "\n")
	}
	refresh := "r refresh"
	if !m.workspaceRefreshed.IsZero() {
		refresh += " · checked " + m.workspaceRefreshed.Format("15:04:05")
	}
	if m.workspaceLoading {
		refresh += " · refreshing…"
	}
	b.WriteString(m.styles.muted.Render("  "+refresh) + "\n\n")

	b.WriteString(m.securityContent(width))

	b.WriteString(h("Runtime health") + "\n")
	providerHealth := m.runtime.Agent.ProviderHealth()
	b.WriteString(kv("provider", providerHealth.Summary()) + "\n")
	if providerHealth.State == provider.HealthDegraded || providerHealth.State == provider.HealthOpen || providerHealth.State == provider.HealthHalfOpen {
		b.WriteString(m.styles.muted.Render("  action: inspect /models and run collo doctor") + "\n")
	}
	b.WriteString(kv("MCP", m.mcpHealthText()) + "\n")
	if m.mcpNeedsAttention() {
		b.WriteString(m.styles.muted.Render("  action: inspect /mcp; trust changed project configuration with collo trust") + "\n")
	}
	persistence := "healthy"
	if m.runtime.Session == nil {
		persistence = "ephemeral or unavailable"
	} else if err := m.runtime.PersistenceError(); err != nil {
		persistence = "failed · " + err.Error()
	}
	b.WriteString(kv("persistence", persistence) + "\n")
	if m.runtime.PersistenceError() != nil {
		b.WriteString(m.styles.muted.Render("  action: stop mutating work, verify disk space, then run collo support bundle") + "\n")
	}
	b.WriteString("\n")

	b.WriteString(h("Context") + "\n")
	usageText := fmt.Sprintf("%d input / %d output tokens", usage.InputTokens, usage.OutputTokens)
	if usage.CachedTokens > 0 {
		usageText += fmt.Sprintf(" (%d cached)", usage.CachedTokens)
	}
	b.WriteString(kv("usage", usageText) + "\n")
	if usage.CostAvailable {
		b.WriteString(kv("cost", fmt.Sprintf("$%.6f estimated", usage.CostUSD)) + "\n")
	}
	if tokenBudget > 0 || costBudget > 0 {
		b.WriteString(kv("budgets", fmt.Sprintf("%d tokens / $%.6f", tokenBudget, costBudget)) + "\n")
	}
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
			line := fmt.Sprintf("  %s [%s] %d. %s", mark, step.Status, step.ID, step.Title)
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

	if len(m.activities) > 0 {
		b.WriteString(h("Recent activity") + "\n")
		start := max(0, len(m.activities)-6)
		for i := len(m.activities) - 1; i >= start; i-- {
			item := m.activities[i]
			status := "[" + string(item.Status) + "]"
			statusStyle := m.styles.muted
			switch item.Status {
			case activity.StatusSuccess:
				statusStyle = m.styles.success
			case activity.StatusWarning, activity.StatusActive:
				statusStyle = m.styles.accent
			case activity.StatusError:
				statusStyle = m.styles.errText
			}
			line := fmt.Sprintf("  %s %s  %s", statusStyle.Render(status), m.styles.muted.Render(string(item.Category)), m.styles.panelBody.Render(m.runtime.Redactor.Redact(item.Title)))
			if item.Detail != "" {
				line += m.styles.muted.Render(" · " + m.runtime.Redactor.Redact(item.Detail))
			}
			b.WriteString(fitLine(line, max(1, width)) + "\n")
		}
		b.WriteString(m.styles.muted.Render("  /activity to search, filter, and copy failure IDs") + "\n\n")
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
		parentState := "idle"
		if m.busy {
			parentState = "working"
		}
		b.WriteString("  " + m.styles.accent.Render("Collomia") + " " + m.styles.muted.Render("(parent, "+parentState+")") + "\n")
		marks := map[string]string{
			agent.DelegateQueued: "○", agent.DelegateRunning: "◐", agent.DelegateWaitingApproval: "?", agent.DelegateCancelling: "◒",
			agent.DelegateDone: "●", agent.DelegateError: "✗", agent.DelegateCancelled: "○", agent.DelegateTimedOut: "✗",
			agent.DelegateBudgetExhausted: "!", agent.DelegateInterrupted: "!",
		}
		colors := map[string]string{
			agent.DelegateQueued: m.theme.Muted, agent.DelegateRunning: m.theme.Warning, agent.DelegateWaitingApproval: m.theme.Warning, agent.DelegateCancelling: m.theme.Warning,
			agent.DelegateDone: m.theme.Success, agent.DelegateError: m.theme.Error, agent.DelegateCancelled: m.theme.Muted, agent.DelegateTimedOut: m.theme.Error,
			agent.DelegateBudgetExhausted: m.theme.Error, agent.DelegateInterrupted: m.theme.Error,
		}
		for index, a := range agents {
			mark := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[a.Status])).Render(marks[a.Status])
			branchMark := "├─"
			if index == len(agents)-1 {
				branchMark = "└─"
			}
			kind := "read"
			if a.Write {
				kind = "write"
			}
			line := fmt.Sprintf("  %s %s %s  %s", branchMark, mark, m.styles.accent.Render(m.runtime.Redactor.Redact(a.Name)), m.styles.muted.Render("("+kind+", "+delegateStatusLabel(a.Status)+")"))
			if a.PlanStep > 0 {
				line += m.styles.muted.Render(fmt.Sprintf(" · plan %d", a.PlanStep))
			}
			b.WriteString(line + "\n")
			if a.Status == agent.DelegateQueued || a.Status == agent.DelegateRunning || a.Status == agent.DelegateWaitingApproval || a.Status == agent.DelegateCancelling {
				if a.CurrentAction != "" {
					b.WriteString(m.styles.muted.Render("       "+m.runtime.Redactor.Redact(a.CurrentAction)) + "\n")
				}
				if output := strings.Join(strings.Fields(m.runtime.Redactor.Redact(a.RecentOutput)), " "); output != "" {
					b.WriteString(m.styles.muted.Render("       ↳ "+truncateRunes(output, 100)) + "\n")
				}
				continue
			}
			if len(a.Changed) > 0 {
				if len(a.ScopeViolations) > 0 {
					b.WriteString(m.styles.errText.Render(fmt.Sprintf("       scope violation in %d file(s) — retained worktree requires manual inspection", len(a.ScopeViolations))) + "\n")
					continue
				}
				integration := ""
				if a.IntegrationStatus != "" {
					integration = " · " + delegateStatusLabel(a.IntegrationStatus)
				}
				verification := " · unverified"
				if a.VerificationStatus != "" {
					verification = " · verification " + delegateStatusLabel(a.VerificationStatus)
				}
				b.WriteString(m.styles.muted.Render(fmt.Sprintf("       changed %d file(s)%s%s — /agents verify|apply %s", len(a.Changed), integration, verification, a.ID)) + "\n")
			} else if a.Error != "" {
				b.WriteString(m.styles.muted.Render("      "+m.runtime.Redactor.Redact(a.Error)) + "\n")
			}
		}
		b.WriteString(m.styles.muted.Render("  "+m.binding("agent_control")+" inspect · /agents steer|stop|verify|compare|apply") + "\n\n")
	}

	b.WriteString(h("Providers") + "\n")
	for _, name := range m.runtime.Config.ProviderNames() {
		p := m.runtime.Config.Providers[name]
		marker := "  "
		if name == providerName {
			marker = m.styles.success.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%s  [%s]  %s\n", marker, m.styles.accent.Render(name), p.Type, p.Model))
	}
	b.WriteString("\n" + h("Tools") + "\n" + indent(ansi.Wordwrap(strings.Join(m.runtime.Registry.Names(), ", "), max(1, width-2), ""), "  ") + "\n\n")

	b.WriteString(h("Skills") + "\n")
	if len(m.runtime.Skills.Skills) == 0 {
		b.WriteString(m.styles.muted.Render("  none discovered") + "\n")
	}
	for _, skill := range m.runtime.Skills.Skills {
		row := "  " + m.styles.accent.Render(skill.Name) + "  " + m.styles.muted.Render(skill.Description)
		b.WriteString(ansi.Truncate(row, max(1, width), "…") + "\n")
	}
	b.WriteString("\n" + h("MCP servers") + "\n")
	servers := m.runtime.MCP.Servers()
	if len(servers) == 0 {
		b.WriteString(m.styles.muted.Render("  none connected") + "\n")
	} else {
		sort.Strings(servers)
		b.WriteString(indent(ansi.Wordwrap(strings.Join(servers, ", "), max(1, width-2), ""), "  ") + "\n")
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
		{"alt+enter · ctrl+j", "insert a newline without sending"},
		{"enter (unfinished)", "a draft ending in \\ or inside an open code fence gains a line instead"},
		{m.binding("compose_editor"), "edit the current draft in $EDITOR"},
		{"/", "open the command palette (with argument completion)"},
		{"@", "fuzzy-pick a workspace file into the prompt"},
		{"↑ ↓ (palette)", "select a command or completion"},
		{"tab (palette)", "complete the selected command"},
		{m.binding("next_tab"), "cycle Chat / Session / Help tabs"},
		{m.binding("session_picker"), "open saved sessions without replacing the draft"},
		{m.binding("agent_control"), "inspect active agents; use /agents to steer, verify, compare, stop, or apply"},
		{m.binding("toggle_tool_output"), "expand / collapse finished tool output"},
		{m.binding("transcript_view"), "open transcript search/copy mode"},
		{m.binding("diff_view"), "open the interactive diff viewer"},
		{m.binding("context_rail"), "show / hide the context rail (automatic above " + fmt.Sprintf("%d", railAutoWidth) + " columns)"},
		{"wheel · click", "scroll the transcript · select a tab (set options.mouse false to disable)"},
		{"/activity", "search/filter runtime activity and copy failure IDs"},
		{"↑ / ↓ (composer)", "previous / next prompt at the first or last line"},
		{"y / a / n (approval)", "approve once / always for this tool / deny"},
		{"g (approval)", "approve and allow exactly this reach (commands, endpoints) for the session"},
		{"h (approval)", "review a multi-hunk file write and approve only some hunks"},
		{"esc", "cancel turn · dismiss palette or picker"},
		{m.binding("page_up") + "/" + m.binding("page_down"), "scroll without moving the prompt cursor"},
		{m.binding("scroll_top") + "/" + m.binding("scroll_bottom"), "jump to top / resume live follow"},
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
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStyles(m.theme.markdownStyle()),
			glamour.WithChromaFormatter("terminal16m"),
			glamour.WithWordWrap(width),
		)
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
	if m.transcript != nil && !m.modalActive() {
		return m.renderTranscriptView()
	}
	if m.diffView != nil && !m.modalActive() {
		return m.renderDiffView()
	}
	if m.activityView != nil && !m.modalActive() {
		return m.renderActivityView()
	}
	sections := []string{m.renderHeader(), m.renderBody()}
	if m.picker != nil && !m.modalActive() {
		sections = append(sections, m.renderPicker())
	} else if m.paletteOn && !m.modalActive() {
		sections = append(sections, m.renderPalette())
	}
	sections = append(sections, m.renderComposer())
	sections = append(sections, m.renderStatusBar())
	base := strings.Join(sections, "\n")
	switch {
	case m.agentIntegration != nil:
		return placeOverlay(base, m.renderAgentIntegration(), m.width, m.height)
	case m.hunkReview != nil:
		return placeOverlay(base, m.renderHunkReview(), m.width, m.height)
	case m.pending != nil:
		return placeOverlay(base, m.renderApproval(), m.width, m.height)
	case m.question != nil:
		return placeOverlay(base, m.renderQuestion(), m.width, m.height)
	default:
		return base
	}
}

// renderBody joins the transcript with the context rail. The rail is a chat
// affordance only: the Session tab is already the long-form version of
// everything in it, so mirroring it there would just be two copies of the
// same data competing for the same columns.
func (m Model) renderBody() string {
	body := m.viewport.View()
	if !m.railVisible() {
		return body
	}
	rail := strings.Split(m.renderRail(m.viewport.Height), "\n")
	lines := strings.Split(body, "\n")
	for len(lines) < m.viewport.Height {
		lines = append(lines, "")
	}
	// Pad against the intended body width rather than letting a horizontal
	// join measure the widest transcript line: a short line would otherwise
	// pull the whole rail leftwards for that row.
	for i := range lines {
		lines[i] = fitLine(lines[i], m.bodyWidth())
		if i < len(rail) {
			lines[i] += rail[i]
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderHeader() string {
	if m.width < 44 {
		active := m.styles.tabActive.Render(tabNames[m.tab])
		left := m.styles.brand.Render(" ✿ ") + active
		return fitLine(left, max(1, m.width)) + "\n" + m.styles.rule.Render(strings.Repeat("─", max(0, m.width)))
	}
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
	if m.runtime.ActiveAgent != "" {
		left += m.styles.statusBase.Render(" ") + badge(m.runtime.ActiveAgent, m.theme.Accent)
	}
	left += m.styles.statusBase.Render(" ") + badge(provider+"/"+model, m.theme.Border)
	// Autonomy and containment are one statement about risk, so the shield
	// rides along in the mode badge: always visible, two columns, and it
	// cannot be pushed off by a crowded bar. A louder named form is offered
	// separately below when the stance deviates and there is room.
	left += m.styles.statusBase.Render(" ") + badge(strings.ToUpper(mode)+" "+m.stanceGlyph(), modeColor)
	stanceAnchor := len(left)
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
	if len(m.pendingAttachments) > 0 {
		left += m.styles.statusBase.Render(" ") + badge(fmt.Sprintf("images %d", len(m.pendingAttachments)), m.theme.Accent)
	}
	left += m.styles.statusBase.Render(" " + contextGauge(m.theme, estimate, window, 10) + " ")

	var right string
	switch {
	case m.hunkReview != nil:
		right = m.styles.statusBase.Render("↑↓ move · space toggle · enter apply · esc back ")
	case m.agentIntegration != nil:
		right = m.styles.statusBase.Render("[ ] file · ↑↓ hunk · space toggle · enter apply · esc cancel ")
	case m.pending != nil:
		right = m.styles.statusBase.Render("y approve · a always · n deny ")
	case m.question != nil:
		right = m.styles.statusBase.Render("enter answer · esc decline ")
	case m.busy:
		elapsed := time.Since(m.turnStarted).Round(time.Second)
		progress := m.spinner.View()
		if m.runtime.Config.Options.ReducedMotion {
			progress = "•"
		}
		right = m.styles.statusKey.Render(progress) + m.styles.statusBase.Render(fmt.Sprintf(" working %s · ", elapsed))
		if m.runtime.Team.Active() > 0 {
			right += m.styles.statusKey.Render(m.binding("agent_control")) + m.styles.statusBase.Render(" agents · ")
		}
		right += m.styles.statusKey.Render("/") + m.styles.statusBase.Render(" local commands · esc cancel ")
	default:
		right = m.styles.statusBase.Render("enter send · ") +
			m.styles.statusKey.Render("/") + m.styles.statusBase.Render(" commands · ") +
			m.styles.statusKey.Render(m.binding("next_tab")) + m.styles.statusBase.Render(" tabs · ") +
			m.styles.statusKey.Render("ctrl+c") + m.styles.statusBase.Render(" quit ")
	}
	// The named stance badge is additive: it appears only when the stance is
	// worth spelling out and only when it does not push the run controls off
	// the bar. An indicator that hides "esc cancel" has made the session less
	// safe, not more.
	if named := m.stanceNameBadge(); named != "" {
		candidate := left[:stanceAnchor] + m.styles.statusBase.Render(" ") + named + left[stanceAnchor:]
		if m.width-lipgloss.Width(candidate)-lipgloss.Width(right) >= 1 {
			left = candidate
		}
	}
	// Spend is the last thing to claim space and the first to lose it. It is
	// useful to watch during a turn but never worth displacing the controls
	// that stop one.
	if spend := m.spendReadout(); spend != "" {
		candidate := left + spend + m.styles.statusBase.Render(" ")
		if m.width-lipgloss.Width(candidate)-lipgloss.Width(right) >= 1 {
			left = candidate
		}
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, max(1, m.width), "")
	}
	return left + m.styles.statusBase.Render(strings.Repeat(" ", gap)) + right
}

// spendReadout is the session's running token count and estimated cost.
// Providers that do not report pricing get the token count alone rather than
// a fabricated dollar figure.
func (m Model) spendReadout() string {
	usage := m.runtime.Agent.Usage()
	total := usage.InputTokens + usage.OutputTokens
	if total == 0 {
		return ""
	}
	text := formatTokens(total)
	if usage.CostAvailable && usage.CostUSD > 0 {
		text += " · " + formatCost(usage.CostUSD)
	}
	return m.styles.statusBase.Render(text)
}

func formatCost(usd float64) string {
	switch {
	case usd >= 1:
		return fmt.Sprintf("$%.2f", usd)
	case usd >= 0.01:
		return fmt.Sprintf("$%.3f", usd)
	default:
		return fmt.Sprintf("$%.4f", usd)
	}
}

func indent(value, prefix string) string {
	return prefix + strings.ReplaceAll(value, "\n", "\n"+prefix)
}

// ring sounds the terminal bell so an unattended approval, question, or
// finished long turn gets the user's attention. Terminals map this to their
// configured notification (sound, badge, or nothing) — never intrusive.
func ring() { _, _ = os.Stderr.WriteString("\a") }
