package tui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

const maxClipboardBytes = 100 * 1024

type transcriptState struct {
	viewport  viewport.Model
	query     string
	searching bool
	cursor    int
	matches   []int
	match     int
	offsets   []int
	notice    string
}

func (m *Model) openTranscriptView() {
	if len(m.blocks) == 0 {
		m.addSystem("The transcript is empty.")
		m.refresh()
		return
	}
	h := max(1, m.height-2)
	state := &transcriptState{viewport: viewport.New(max(1, m.width), h), cursor: len(m.blocks) - 1}
	m.transcript = state
	m.input.Blur()
	m.rebuildTranscriptView()
	state.viewport.GotoBottom()
}

func (m *Model) rebuildTranscriptView() {
	state := m.transcript
	if state == nil {
		return
	}
	offset := state.viewport.YOffset
	state.offsets = state.offsets[:0]
	var b strings.Builder
	line := 0
	width := max(8, m.width-2)
	for i, entry := range m.blocks {
		state.offsets = append(state.offsets, line)
		marker := "  "
		if i == state.cursor {
			marker = "▸ "
		}
		title := marker + transcriptBlockTitle(entry)
		if i == state.cursor {
			title = m.styles.accent.Render(title)
		} else {
			title = m.styles.muted.Render(title)
		}
		body := ansi.Hardwrap(rawBlockText(entry), width, true)
		b.WriteString(title + "\n" + body)
		if i < len(m.blocks)-1 {
			b.WriteString("\n\n")
		}
		line += strings.Count(title+"\n"+body, "\n") + 1
		if i < len(m.blocks)-1 {
			line += 2
		}
	}
	state.viewport.Width = max(1, m.width)
	state.viewport.Height = max(1, m.height-2)
	state.viewport.SetContent(b.String())
	state.viewport.SetYOffset(offset)
}

func transcriptBlockTitle(entry block) string {
	switch entry.role {
	case "user":
		return "YOU"
	case "assistant":
		return "COLLOMIA"
	case "tool":
		name, _, _ := strings.Cut(entry.content, "\x00")
		return "TOOL · " + name
	case "tool-result":
		if entry.tool != "" {
			return "RESULT · " + entry.tool
		}
		return "TOOL RESULT"
	case "error":
		return "ERROR"
	case "panel":
		return strings.ToUpper(entry.title)
	default:
		return "COLLOMIA STATUS"
	}
}

func rawBlockText(entry block) string {
	if entry.role == "tool" {
		_, summary, _ := strings.Cut(entry.content, "\x00")
		return summary
	}
	return entry.content
}

func (m Model) rawTranscript() string {
	var b strings.Builder
	for i, entry := range m.blocks {
		fmt.Fprintf(&b, "--- %s ---\n%s", transcriptBlockTitle(entry), rawBlockText(entry))
		if i < len(m.blocks)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

func (m Model) handleTranscriptKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.transcript
	keyName := key.String()
	if state.searching {
		switch keyName {
		case "esc":
			state.searching = false
			state.query = ""
		case "enter":
			state.searching = false
			m.findTranscriptMatches()
		case "backspace":
			if state.query != "" {
				_, size := utf8.DecodeLastRuneInString(state.query)
				state.query = state.query[:len(state.query)-size]
			}
		default:
			if key.Type == tea.KeyRunes {
				state.query += string(key.Runes)
			}
		}
		return m, nil
	}

	switch keyName {
	case "esc", "q":
		m.transcript = nil
		m.input.Focus()
		m.refresh()
		return m, nil
	case "/":
		state.searching = true
		state.query = ""
		state.notice = ""
		return m, nil
	case "n":
		m.moveTranscriptMatch(1)
		return m, nil
	case "N":
		m.moveTranscriptMatch(-1)
		return m, nil
	case "[", "left":
		m.selectTranscriptBlock(-1)
		return m, nil
	case "]", "right":
		m.selectTranscriptBlock(1)
		return m, nil
	case "up", "k":
		state.viewport.LineUp(1)
	case "down", "j":
		state.viewport.LineDown(1)
	case "y":
		err := copyTerminalText(rawBlockText(m.blocks[state.cursor]))
		state.notice = clipboardNotice(err, "message copied")
		return m, nil
	case "Y":
		err := copyTerminalText(m.rawTranscript())
		state.notice = clipboardNotice(err, "transcript copied")
		return m, nil
	default:
		switch {
		case m.keyIs("page_up", keyName):
			state.viewport.PageUp()
		case m.keyIs("page_down", keyName):
			state.viewport.PageDown()
		case m.keyIs("scroll_top", keyName):
			state.viewport.GotoTop()
		case m.keyIs("scroll_bottom", keyName):
			state.viewport.GotoBottom()
		}
	}
	m.selectTranscriptAtOffset()
	return m, nil
}

func (m *Model) selectTranscriptBlock(delta int) {
	state := m.transcript
	state.cursor = max(0, min(len(m.blocks)-1, state.cursor+delta))
	m.rebuildTranscriptView()
	state.viewport.SetYOffset(state.offsets[state.cursor])
}

func (m *Model) selectTranscriptAtOffset() {
	state := m.transcript
	for i := len(state.offsets) - 1; i >= 0; i-- {
		if state.offsets[i] <= state.viewport.YOffset {
			if state.cursor != i {
				state.cursor = i
				m.rebuildTranscriptView()
			}
			return
		}
	}
}

func (m *Model) findTranscriptMatches() {
	state := m.transcript
	state.matches = state.matches[:0]
	query := strings.ToLower(strings.TrimSpace(state.query))
	if query == "" {
		state.notice = "empty search"
		return
	}
	for i, entry := range m.blocks {
		haystack := strings.ToLower(transcriptBlockTitle(entry) + "\n" + rawBlockText(entry))
		if strings.Contains(haystack, query) {
			state.matches = append(state.matches, i)
		}
	}
	if len(state.matches) == 0 {
		state.notice = fmt.Sprintf("no matches for %q", state.query)
		return
	}
	state.match = 0
	state.notice = fmt.Sprintf("match 1/%d for %q", len(state.matches), state.query)
	state.cursor = state.matches[0]
	m.rebuildTranscriptView()
	state.viewport.SetYOffset(state.offsets[state.cursor])
}

func (m *Model) moveTranscriptMatch(delta int) {
	state := m.transcript
	if len(state.matches) == 0 {
		state.notice = "press / to search"
		return
	}
	state.match = (state.match + delta + len(state.matches)) % len(state.matches)
	state.cursor = state.matches[state.match]
	state.notice = fmt.Sprintf("match %d/%d for %q", state.match+1, len(state.matches), state.query)
	m.rebuildTranscriptView()
	state.viewport.SetYOffset(state.offsets[state.cursor])
}

func (m Model) renderTranscriptView() string {
	state := m.transcript
	header := m.styles.brand.Render(" Transcript ") + m.styles.muted.Render(fmt.Sprintf("message %d/%d", state.cursor+1, len(m.blocks)))
	footer := "[/] search · [ ] message · ↑↓/page scroll · y copy message · Y copy all · esc close"
	if state.searching {
		footer = "Search: " + state.query + "█  · enter find · esc cancel"
	} else if state.notice != "" {
		footer = state.notice + "  ·  " + footer
	}
	return fitLine(header, max(1, m.width)) + "\n" + state.viewport.View() + "\n" + fitLine(m.styles.muted.Render(footer), max(1, m.width))
}

func clipboardNotice(err error, success string) string {
	if err != nil {
		return "copy unavailable: " + err.Error()
	}
	return success + " via terminal clipboard"
}

// copyTerminalText uses OSC 52 so copying needs no platform-specific helper.
// Terminals may disable clipboard writes; the no-alt-screen mode remains a
// selection-friendly fallback in that case.
func copyTerminalText(value string) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("stdout is not a terminal")
	}
	if len(value) > maxClipboardBytes {
		return fmt.Errorf("selection is %d KiB; terminal clipboard limit is %d KiB", (len(value)+1023)/1024, maxClipboardBytes/1024)
	}
	payload := base64.StdEncoding.EncodeToString([]byte(value))
	emitOSC("\x1b]52;c;" + payload + "\x07")
	return nil
}

func (m *Model) resizeFullScreenViews() {
	if m.transcript != nil {
		m.rebuildTranscriptView()
	}
	if m.diffView != nil {
		m.rebuildDiffView()
	}
	if m.activityView != nil {
		m.rebuildActivityView()
	}
}
