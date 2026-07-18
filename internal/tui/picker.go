package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// pickerItem is one selectable row in a fuzzy picker overlay.
type pickerItem struct {
	id    string // value handed to the action
	title string
	desc  string
}

// picker is a modal fuzzy-filtered list: type to filter, ↑/↓ to select,
// enter to act, esc to dismiss. One component powers model, theme, session,
// and file pickers.
type picker struct {
	title   string
	query   string
	items   []pickerItem
	matches []pickerItem
	sel     int
	// action runs on enter with the chosen item.
	action func(m *Model, item pickerItem) tea.Cmd
	// onCancel runs when the picker is dismissed without a choice.
	onCancel func(m *Model)
}

func newPicker(title string, items []pickerItem, action func(*Model, pickerItem) tea.Cmd) *picker {
	p := &picker{title: title, items: items, action: action}
	p.filter()
	return p
}

// fuzzyScore reports how well query matches candidate as a case-insensitive
// subsequence. Higher is better; ok is false when it does not match at all.
// Consecutive runs and word starts score higher, earlier matches break ties.
func fuzzyScore(query, candidate string) (score int, ok bool) {
	if query == "" {
		return 0, true
	}
	q := []rune(strings.ToLower(query))
	c := []rune(strings.ToLower(candidate))
	qi := 0
	last := -2
	for ci := 0; ci < len(c) && qi < len(q); ci++ {
		if c[ci] != q[qi] {
			continue
		}
		switch {
		case ci == 0:
			score += 4
		case ci == last+1:
			score += 3
		case isWordStart(c, ci):
			score += 2
		default:
			score++
		}
		// Earlier matches are slightly better.
		score -= ci / 8
		last = ci
		qi++
	}
	if qi < len(q) {
		return 0, false
	}
	// Shorter candidates that fully consume the query rank higher.
	score -= len(c) / 16
	return score, true
}

func isWordStart(runes []rune, i int) bool {
	if i == 0 {
		return true
	}
	prev := runes[i-1]
	return prev == '/' || prev == '-' || prev == '_' || prev == '.' || prev == ' ' || unicode.IsUpper(runes[i])
}

func (p *picker) filter() {
	type scored struct {
		item  pickerItem
		score int
		index int
	}
	var out []scored
	for i, item := range p.items {
		best, ok := fuzzyScore(p.query, item.title)
		if descScore, descOK := fuzzyScore(p.query, item.desc); descOK && (!ok || descScore-2 > best) {
			// Matching the description counts, slightly discounted.
			best, ok = descScore-2, true
		}
		if ok {
			out = append(out, scored{item, best, i})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].index < out[j].index
	})
	p.matches = p.matches[:0]
	for _, s := range out {
		p.matches = append(p.matches, s.item)
	}
	if p.sel >= len(p.matches) {
		p.sel = 0
	}
}

// handleKey processes one key while the picker is open. It returns the
// resulting command and whether the picker consumed the key.
func (m *Model) handlePickerKey(key tea.KeyMsg) (tea.Cmd, bool) {
	p := m.picker
	switch key.String() {
	case "esc", "ctrl+c":
		if p.onCancel != nil {
			p.onCancel(m)
		}
		m.picker = nil
		m.layout()
		m.refresh()
		return nil, true
	case "up", "ctrl+p":
		if len(p.matches) > 0 {
			p.sel = (p.sel - 1 + len(p.matches)) % len(p.matches)
		}
		return nil, true
	case "down", "ctrl+n":
		if len(p.matches) > 0 {
			p.sel = (p.sel + 1) % len(p.matches)
		}
		return nil, true
	case "enter", "tab":
		if len(p.matches) == 0 {
			return nil, true
		}
		item := p.matches[p.sel]
		action := p.action
		m.picker = nil
		var cmd tea.Cmd
		if action != nil {
			cmd = action(m, item)
		}
		m.layout()
		m.refresh()
		return cmd, true
	case "backspace":
		if p.query != "" {
			runes := []rune(p.query)
			p.query = string(runes[:len(runes)-1])
			p.filter()
		}
		return nil, true
	}
	if key.Type == tea.KeyRunes && !key.Alt {
		p.query += string(key.Runes)
		p.filter()
		p.sel = 0
		return nil, true
	}
	if key.Type == tea.KeySpace {
		p.query += " "
		p.filter()
		return nil, true
	}
	return nil, true // modal: swallow everything else
}

const pickerMaxRows = 9

func (m *Model) pickerHeight() int {
	if m.picker == nil {
		return 0
	}
	rows := len(m.picker.matches)
	if rows > pickerMaxRows {
		rows = pickerMaxRows
	}
	if rows == 0 {
		rows = 1
	}
	return rows + 4 // title+query, rows, hint, borders
}

func (m *Model) renderPicker() string {
	p := m.picker
	rows := len(p.matches)
	if rows > pickerMaxRows {
		rows = pickerMaxRows
	}
	start := 0
	if p.sel >= rows && rows > 0 {
		start = p.sel - rows + 1
	}
	titleWidth := 0
	for _, item := range p.matches {
		if len(item.title) > titleWidth {
			titleWidth = len(item.title)
		}
	}
	if titleWidth > 48 {
		titleWidth = 48
	}
	header := m.styles.accent.Render(p.title) + "  " + m.styles.paletteCmd.Render("🔎 "+p.query+"▌")
	var lines []string
	if len(p.matches) == 0 {
		lines = append(lines, m.styles.paletteDesc.Render("  no matches — esc to dismiss"))
	}
	for i := start; i < start+rows && i < len(p.matches); i++ {
		item := p.matches[i]
		title := item.title
		if len(title) > 48 {
			title = title[:47] + "…"
		}
		title += strings.Repeat(" ", max(0, titleWidth-len(title)))
		if i == p.sel {
			lines = append(lines, m.styles.paletteSel.Render("▸ "+title)+"  "+m.styles.paletteDesc.Render(item.desc))
		} else {
			lines = append(lines, "  "+m.styles.paletteCmd.Render(title)+"  "+m.styles.paletteDesc.Render(item.desc))
		}
	}
	extra := ""
	if len(p.matches) > rows {
		extra = m.styles.paletteDesc.Render(fmt.Sprintf("  … %d more (keep typing to narrow)", len(p.matches)-rows))
	}
	hint := m.styles.paletteDesc.Render("type to filter · ↑↓ select · enter choose · esc dismiss")
	body := header + "\n" + strings.Join(lines, "\n")
	if extra != "" {
		body += "\n" + extra
	}
	body += "\n" + hint
	return m.styles.paletteBox.Width(max(1, m.width-2) - 4).Render(body)
}
