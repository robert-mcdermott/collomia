package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	// composerMinRows keeps a single-line prompt to a single line: the editor
	// grows only once there is something to show.
	composerMinRows = 1
	// composerMaxRows caps growth so a long draft scrolls inside the composer
	// instead of eating the transcript.
	composerMaxRows = 10
	// composerPromptWidth must match the rendered width of every string
	// composerPrompt returns; the textarea reserves exactly this much.
	composerPromptWidth = 2
)

// newComposer builds the prompt editor.
//
// Enter submits, so newline insertion needs keys of its own. The textarea's
// default binding is "enter"/"ctrl+m", which this replaces outright: leaving
// it in place meant Enter both sent the turn and inserted a newline depending
// on which handler saw it first.
func newComposer() textarea.Model {
	in := textarea.New()
	in.Placeholder = "Ask Collomia to build, debug, explain…  (/ for commands)"
	in.ShowLineNumbers = false
	in.CharLimit = 0
	in.MaxHeight = composerMaxRows
	in.EndOfBufferCharacter = ' '
	// ctrl+j is a literal line feed, so it survives every terminal, terminal
	// multiplexer, and SSH hop unchanged. alt+enter is the conventional chord
	// and is what `collo terminal-setup` maps shift+enter onto.
	in.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "insert newline"),
	)
	in.SetPromptFunc(composerPromptWidth, composerPrompt)
	in.SetHeight(composerMinRows)
	in.Focus()
	return in
}

// composerPrompt marks only the first row. Repeating the chevron down the
// left edge made an empty three-row editor look like three empty prompts
// waiting for input.
func composerPrompt(lineIdx int) string {
	if lineIdx == 0 {
		return "❯ "
	}
	return "  "
}

// restyleComposer re-derives every composer style from the active theme.
//
// The textarea keeps an unexported pointer to whichever Style struct was
// current the last time Focus or Blur ran. That pointer survives the struct
// copies Bubble Tea makes on every Update, so after New copied a focused
// textarea into the Model it still addressed the *local* variable's
// FocusedStyle -- and every assignment below wrote to a struct nothing
// rendered from. Re-selecting the current focus state rebinds the pointer to
// this Model's own styles, which is what makes the assignments take effect.
func (m *Model) restyleComposer() {
	t := m.theme
	prompt := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Secondary)).Bold(true)
	placeholder := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted))
	if t.plain() {
		prompt = lipgloss.NewStyle().Bold(true)
		placeholder = lipgloss.NewStyle().Faint(true)
	}
	m.input.FocusedStyle.Prompt = prompt
	m.input.FocusedStyle.Placeholder = placeholder
	m.input.FocusedStyle.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(t.panelText()))
	// The default cursor line paints a solid background across the editor,
	// which reads as a stray grey bar over a themed terminal background.
	m.input.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.input.FocusedStyle.EndOfBuffer = lipgloss.NewStyle()
	m.input.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted))
	m.input.BlurredStyle.Placeholder = placeholder
	m.input.BlurredStyle.CursorLine = lipgloss.NewStyle()
	m.input.BlurredStyle.EndOfBuffer = lipgloss.NewStyle()
	if m.input.Focused() {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
}

// composerRows is the number of display rows the current draft needs,
// counting soft wraps so a single long paragraph grows the editor too.
func (m Model) composerRows() int {
	width := max(1, m.input.Width())
	rows := 0
	for _, line := range strings.Split(m.input.Value(), "\n") {
		w := ansi.StringWidth(line)
		rows += max(1, (w+width-1)/width)
	}
	return rows
}

// syncComposerHeight resizes the editor to its content and reports whether
// the surrounding layout has to be recomputed.
func (m *Model) syncComposerHeight() bool {
	rows := min(max(m.composerRows(), composerMinRows), composerMaxRows)
	if rows == m.input.Height() {
		return false
	}
	m.input.SetHeight(rows)
	return true
}

// composerHeight is the total rows the composer occupies, including its
// border and the continuation hint when one is showing.
func (m Model) composerHeight() int {
	height := m.input.Height() + 2
	if m.continuationHint() != "" {
		height++
	}
	return height
}

// continuationHint explains why Enter is currently inserting a newline
// instead of sending. An input that silently stops submitting is a bug
// report; one that says why is a feature.
func (m Model) continuationHint() string {
	if m.modalActive() || m.picker != nil {
		return ""
	}
	value := m.input.Value()
	if strings.TrimSpace(value) == "" {
		return ""
	}
	switch {
	case openFence(value):
		return "open code fence · enter adds a line · close ``` then enter to send"
	case strings.HasSuffix(value, `\`):
		return `enter continues on a new line · remove the trailing \ to send`
	case strings.Contains(value, "\n"):
		return "enter send · alt+enter or ctrl+j newline · " + m.binding("compose_editor") + " editor"
	}
	return ""
}

// continueDraft decides whether Enter should extend the draft rather than
// submit it, returning the value to install when it should.
//
// The two triggers are deliberately narrow. A trailing backslash and an
// unclosed code fence are both unambiguous statements that the author is not
// finished; unbalanced brackets are not, because ordinary prose asks things
// like "what does mount( do?" and would become unsendable.
func continueDraft(value string) (string, bool) {
	switch {
	case strings.HasSuffix(value, `\`):
		// Consume the marker. Leaving it behind would ship a stray backslash
		// to the model in every multi-line prompt.
		return strings.TrimSuffix(value, `\`) + "\n", true
	case openFence(value):
		return value + "\n", true
	}
	return "", false
}

// openFence reports whether the draft has an unterminated ``` block.
func openFence(value string) bool {
	fences := 0
	for _, line := range strings.Split(value, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fences++
		}
	}
	return fences%2 == 1
}

// newlineSequences are the escape sequences terminals emit for shift+enter
// and ctrl+enter once a keyboard disambiguation protocol is active.
//
// Bubble Tea 1.x parses neither the Kitty keyboard protocol nor xterm's
// modifyOtherKeys, so these arrive as unrecognized CSI sequences rather than
// as key presses. Decoding them here costs nothing on terminals that never
// send them, and means users of terminals that do get the chord they expect
// without configuring anything. Collomia deliberately does not *enable*
// either protocol: the Kitty flag that reports these also re-encodes Escape
// as CSI 27u, which Bubble Tea 1.x cannot read, and that would break every
// esc binding in the program.
var newlineSequences = func() map[string]struct{} {
	out := map[string]struct{}{}
	// Modifier codes: 2 shift, 3 alt, 4 shift+alt, 5 ctrl, 6 ctrl+shift.
	for _, modifier := range []int{2, 3, 4, 5, 6} {
		// Kitty keyboard protocol, and xterm modifyOtherKeys level 2.
		out[unknownCSIKey(fmt.Sprintf("\x1b[13;%du", modifier))] = struct{}{}
		out[unknownCSIKey(fmt.Sprintf("\x1b[27;%d;13~", modifier))] = struct{}{}
	}
	return out
}()

// unknownCSIKey reproduces the string Bubble Tea's unknown-CSI message
// renders for a sequence. The message type is unexported, so its String form
// is the only stable handle on it; building the expected strings from the
// same recipe keeps the match exact without reflection.
func unknownCSIKey(sequence string) string {
	return fmt.Sprintf("?CSI%+v?", []byte(sequence)[2:])
}

// isNewlineSequence reports whether an otherwise unhandled message is a
// shift+enter or ctrl+enter report.
func isNewlineSequence(msg tea.Msg) bool {
	if _, isKey := msg.(tea.KeyMsg); isKey {
		return false
	}
	stringer, ok := msg.(fmt.Stringer)
	if !ok {
		return false
	}
	value := stringer.String()
	if !strings.HasPrefix(value, "?CSI") {
		return false
	}
	_, found := newlineSequences[value]
	return found
}

// insertComposerNewline adds a line break to the draft, honouring the same
// ceiling as the textarea's own newline binding.
func (m *Model) insertComposerNewline() {
	if strings.Count(m.input.Value(), "\n")+1 >= composerMaxRows {
		return
	}
	m.input.InsertRune('\n')
}

// composerActive reports whether keystrokes are reaching the prompt editor
// rather than a dialog, picker, or full-screen view.
func (m Model) composerActive() bool {
	return !m.modalActive() && m.picker == nil && m.transcript == nil &&
		m.diffView == nil && m.activityView == nil
}

type composerEditorMsg struct {
	path string
	err  error
}

// openComposerEditor hands the draft to $EDITOR. The composer caps at ten
// rows by design; past that the right answer is a real editor, not a taller
// box that pushes the transcript off screen.
func (m Model) openComposerEditor() (tea.Model, tea.Cmd) {
	file, err := os.CreateTemp("", "collomia-prompt-*.md")
	if err != nil {
		m.addError(fmt.Errorf("could not create a draft file: %w", err))
		m.refresh()
		return m, nil
	}
	path := file.Name()
	_, writeErr := file.WriteString(m.input.Value())
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(path)
		m.addError(fmt.Errorf("could not write the draft file: %w", err))
		m.refresh()
		return m, nil
	}
	cmd, err := editorCommand(m.runtime.Config.Options.Editor, m.runtime.Workspace, path, 1, 1)
	if err != nil {
		os.Remove(path)
		m.addError(err)
		m.refresh()
		return m, nil
	}
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return composerEditorMsg{path: path, err: err}
	})
}

// finishComposerEditor reads the draft back. A failed editor leaves the
// composer untouched rather than blanking whatever the user had typed.
func (m *Model) finishComposerEditor(msg composerEditorMsg) {
	defer os.Remove(msg.path)
	m.input.Focus()
	if msg.err != nil {
		m.addError(fmt.Errorf("external editor failed: %s", compactEditorError(msg.err.Error())))
		m.refresh()
		return
	}
	data, err := os.ReadFile(msg.path)
	if err != nil {
		m.addError(fmt.Errorf("could not read the draft back: %w", err))
		m.refresh()
		return
	}
	// Editors conventionally add a trailing newline on save; keeping it would
	// leave the composer sitting on a blank final row every round trip.
	m.setComposerValue(strings.TrimRight(string(data), "\n"))
	m.updatePalette()
	m.layout()
	m.refresh()
}
