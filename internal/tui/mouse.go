package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// mouseScrollLines is how far one wheel notch moves a viewport. Three lines
// is the terminal convention and is short enough that a notch never skips
// past the thing the user was reading.
const mouseScrollLines = 3

// handleMouse routes wheel and click events. Only the wheel and a plain left
// click are consumed: drag, motion, and every modifier are left alone so the
// terminal's own selection and the user's muscle memory keep working.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// A dialog owns the screen. Scrolling what is behind it would move
	// content the user cannot reach and cannot fully see.
	if m.modalActive() {
		return m, nil
	}
	if view := m.fullScreenViewport(); view != nil {
		scrollViewport(view, msg)
		return m, nil
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.viewport.LineUp(mouseScrollLines)
		if m.tab == tabChat {
			m.chatFollow = false
		}
	case msg.Button == tea.MouseButtonWheelDown:
		m.viewport.LineDown(mouseScrollLines)
		if m.tab == tabChat && m.viewport.AtBottom() {
			m.chatFollow = true
		}
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		return m.clickTab(msg)
	default:
		return m, nil
	}
	m.tabOffsets[m.tab] = m.viewport.YOffset
	return m, nil
}

// clickTab selects a tab from the header row. Anywhere else on screen is
// ignored: the transcript is not a set of controls, and treating a stray
// click as one would be worse than doing nothing.
func (m Model) clickTab(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Y != 0 || m.width < 44 {
		return m, nil
	}
	x := ansi.StringWidth(m.styles.brand.Render(" ✿ collomia "))
	for i, name := range tabNames {
		width := ansi.StringWidth(m.styles.tabInactive.Render(name))
		if msg.X >= x && msg.X < x+width {
			if i == m.tab {
				return m, nil
			}
			var cmd tea.Cmd
			if i == tabSession {
				cmd = m.refreshWorkspaceStatus()
			}
			m.switchTab(i)
			return m, cmd
		}
		x += width
	}
	return m, nil
}

// fullScreenViewport is the viewport currently covering the whole screen, or
// nil when the normal chat layout is showing.
func (m Model) fullScreenViewport() *viewport.Model {
	switch {
	case m.transcript != nil:
		return &m.transcript.viewport
	case m.diffView != nil:
		return &m.diffView.viewport
	case m.activityView != nil:
		return &m.activityView.viewport
	}
	return nil
}

func scrollViewport(view *viewport.Model, msg tea.MouseMsg) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		view.LineUp(mouseScrollLines)
	case tea.MouseButtonWheelDown:
		view.LineDown(mouseScrollLines)
	}
}
