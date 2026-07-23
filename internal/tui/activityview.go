package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/activity"
)

type activityViewState struct {
	viewport  viewport.Model
	category  int // index into Model.activityFilters; 0 is all
	visible   []int
	cursor    int // index into Model.activities
	offsets   map[int]int
	query     string
	searching bool
	matches   []int
	match     int
	notice    string
}

func (m *Model) openActivityView() {
	if len(m.activities) == 0 {
		m.addSystem("No operator activity has been recorded in this session yet.")
		m.refresh()
		return
	}
	m.activityView = &activityViewState{
		viewport: viewport.New(max(1, m.width), max(1, m.height-2)),
		cursor:   len(m.activities) - 1,
		offsets:  map[int]int{},
	}
	m.input.Blur()
	m.rebuildActivityView()
	m.activityView.viewport.GotoBottom()
}

func (m *Model) rebuildActivityView() {
	state := m.activityView
	if state == nil {
		return
	}
	offset := state.viewport.YOffset
	state.viewport.Width = max(1, m.width)
	state.viewport.Height = max(1, m.height-2)
	state.visible = state.visible[:0]
	state.offsets = map[int]int{}
	filters := m.activityFilters()
	if state.category >= len(filters) {
		state.category = 0
	}
	selectedCategory := filters[state.category]
	for i, item := range m.activities {
		if selectedCategory == "" || item.Category == selectedCategory {
			state.visible = append(state.visible, i)
		}
	}
	if len(state.visible) == 0 {
		state.cursor = -1
		state.viewport.SetContent(m.styles.muted.Render("No activity in this category."))
		state.viewport.SetYOffset(0)
		return
	}
	if !containsActivityIndex(state.visible, state.cursor) {
		state.cursor = state.visible[len(state.visible)-1]
	}
	var b strings.Builder
	line := 0
	for _, index := range state.visible {
		item := m.activities[index]
		state.offsets[index] = line
		marker := "  "
		if index == state.cursor {
			marker = m.styles.accent.Render("▸ ")
		}
		when := "--:--:--"
		if !item.Time.IsZero() {
			when = item.Time.Local().Format("15:04:05")
		}
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
		title := m.runtime.Redactor.Redact(item.Title)
		row := marker + m.styles.muted.Render(when+" ") + statusStyle.Render(status) + " " +
			m.styles.muted.Render(string(item.Category)+" ") + m.styles.panelBody.Render(title)
		b.WriteString(fitLine(row, max(1, m.width)))
		b.WriteByte('\n')
		line++
		if item.Detail != "" {
			detail := "    " + m.runtime.Redactor.Redact(item.Detail)
			b.WriteString(fitLine(m.styles.muted.Render(detail), max(1, m.width)))
			b.WriteByte('\n')
			line++
		}
		if item.FailureID != "" {
			b.WriteString(fitLine(m.styles.errText.Render("    failure "+item.FailureID), max(1, m.width)))
			b.WriteByte('\n')
			line++
		}
	}
	state.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
	state.viewport.SetYOffset(offset)
}

func (m Model) activityCategoryLabel() string {
	if m.activityView == nil || m.activityView.category == 0 {
		return "all"
	}
	filters := m.activityFilters()
	if m.activityView.category >= len(filters) {
		return "all"
	}
	return string(filters[m.activityView.category])
}

func (m Model) activityFilters() []activity.Category {
	present := map[activity.Category]bool{}
	for _, item := range m.activities {
		present[item.Category] = true
	}
	filters := []activity.Category{""}
	for _, category := range activity.Categories {
		if present[category] {
			filters = append(filters, category)
		}
	}
	return filters
}

func (m Model) renderActivityView() string {
	state := m.activityView
	header := m.styles.brand.Render(" Activity ") + m.styles.muted.Render(fmt.Sprintf("%s · %d/%d", m.activityCategoryLabel(), selectedActivityPosition(state), len(state.visible)))
	footer := "f filter · / search · n/N match · ↑↓/page move · y copy item or failure ID · esc close"
	if state.searching {
		footer = "Search: " + state.query + "█  · enter find · esc cancel"
	} else if state.notice != "" {
		footer = state.notice + "  ·  " + footer
	}
	return fitLine(header, max(1, m.width)) + "\n" + state.viewport.View() + "\n" + fitLine(m.styles.muted.Render(footer), max(1, m.width))
}

func selectedActivityPosition(state *activityViewState) int {
	for i, index := range state.visible {
		if index == state.cursor {
			return i + 1
		}
	}
	return 0
}

func (m Model) handleActivityKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.activityView
	keyName := key.String()
	if state.searching {
		switch keyName {
		case "esc":
			state.searching = false
			state.query = ""
		case "enter":
			state.searching = false
			m.findActivityMatches()
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
		m.activityView = nil
		m.input.Focus()
		m.refresh()
		return m, nil
	case "/":
		state.searching = true
		state.query = ""
		state.notice = ""
		return m, nil
	case "f", "tab":
		state.category = (state.category + 1) % len(m.activityFilters())
		state.matches = nil
		state.notice = "filter: " + m.activityCategoryLabel()
		m.rebuildActivityView()
		state.viewport.GotoBottom()
		return m, nil
	case "n":
		m.moveActivityMatch(1)
		return m, nil
	case "N":
		m.moveActivityMatch(-1)
		return m, nil
	case "up", "k":
		m.moveActivitySelection(-1)
		return m, nil
	case "down", "j":
		m.moveActivitySelection(1)
		return m, nil
	case "y":
		if state.cursor < 0 || state.cursor >= len(m.activities) {
			return m, nil
		}
		item := m.activities[state.cursor]
		value, label := m.runtime.Redactor.Redact(activityItemText(item)), "activity item copied"
		if item.FailureID != "" {
			value, label = item.FailureID, "failure ID copied"
		}
		state.notice = clipboardNotice(copyTerminalText(value), label)
		return m, nil
	default:
		switch {
		case m.keyIs("page_up", keyName):
			state.viewport.PageUp()
			m.selectActivityAtOffset()
		case m.keyIs("page_down", keyName):
			state.viewport.PageDown()
			m.selectActivityAtOffset()
		case m.keyIs("scroll_top", keyName):
			if len(state.visible) == 0 {
				return m, nil
			}
			state.viewport.GotoTop()
			state.cursor = state.visible[0]
			m.rebuildActivityView()
		case m.keyIs("scroll_bottom", keyName):
			if len(state.visible) == 0 {
				return m, nil
			}
			state.viewport.GotoBottom()
			state.cursor = state.visible[len(state.visible)-1]
			m.rebuildActivityView()
		}
	}
	return m, nil
}

func (m *Model) moveActivitySelection(delta int) {
	state := m.activityView
	if len(state.visible) == 0 {
		return
	}
	position := selectedActivityPosition(state) - 1
	position = max(0, min(len(state.visible)-1, position+delta))
	state.cursor = state.visible[position]
	m.rebuildActivityView()
	state.viewport.SetYOffset(state.offsets[state.cursor])
}

func (m *Model) selectActivityAtOffset() {
	state := m.activityView
	for i := len(state.visible) - 1; i >= 0; i-- {
		index := state.visible[i]
		if state.offsets[index] <= state.viewport.YOffset {
			state.cursor = index
			m.rebuildActivityView()
			return
		}
	}
}

func (m *Model) findActivityMatches() {
	state := m.activityView
	state.matches = state.matches[:0]
	query := strings.ToLower(strings.TrimSpace(state.query))
	if query == "" {
		state.notice = "empty search"
		return
	}
	for _, index := range state.visible {
		if strings.Contains(strings.ToLower(activityItemText(m.activities[index])), query) {
			state.matches = append(state.matches, index)
		}
	}
	if len(state.matches) == 0 {
		state.notice = fmt.Sprintf("no matches for %q", state.query)
		return
	}
	state.match = 0
	state.cursor = state.matches[0]
	state.notice = fmt.Sprintf("match 1/%d for %q", len(state.matches), state.query)
	m.rebuildActivityView()
	state.viewport.SetYOffset(state.offsets[state.cursor])
}

func (m *Model) moveActivityMatch(delta int) {
	state := m.activityView
	if len(state.matches) == 0 {
		state.notice = "press / to search"
		return
	}
	state.match = (state.match + delta + len(state.matches)) % len(state.matches)
	state.cursor = state.matches[state.match]
	state.notice = fmt.Sprintf("match %d/%d for %q", state.match+1, len(state.matches), state.query)
	m.rebuildActivityView()
	state.viewport.SetYOffset(state.offsets[state.cursor])
}

func activityItemText(item activity.Item) string {
	when := ""
	if !item.Time.IsZero() {
		when = item.Time.Format(time.RFC3339)
	}
	parts := []string{when, string(item.Status), string(item.Category), item.Title, item.Detail, item.FailureID}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func containsActivityIndex(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
