package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"
)

// Theme is a named terminal color palette. All values are hex colors so they
// render consistently on truecolor terminals and degrade via lipgloss on others.
type Theme struct {
	Name      string
	Dark      bool
	Primary   string
	Secondary string
	Accent    string
	Success   string
	Warning   string
	Error     string
	Muted     string
	Border    string
	StatusBG  string
}

const defaultThemeName = "collomia"

var themes = []Theme{
	{Name: "collomia", Dark: true, Primary: "#AF5FFF", Secondary: "#FF5FAF", Accent: "#5FD7FF", Success: "#42D77D", Warning: "#F1C40F", Error: "#FF6B6B", Muted: "#8A8A9E", Border: "#5F5F87", StatusBG: "#1A1A2E"},
	{Name: "synthwave", Dark: true, Primary: "#FF2E97", Secondary: "#00F0FF", Accent: "#B967FF", Success: "#72F1B8", Warning: "#FEDE5D", Error: "#FE4450", Muted: "#848BBD", Border: "#495495", StatusBG: "#241B2F"},
	{Name: "outrun", Dark: true, Primary: "#FF6C11", Secondary: "#FF3864", Accent: "#2DE2E6", Success: "#61E786", Warning: "#F9C80E", Error: "#FF3864", Muted: "#8B7F9E", Border: "#552E85", StatusBG: "#261447"},
	{Name: "matrix", Dark: true, Primary: "#00FF41", Secondary: "#008F11", Accent: "#7FFF9E", Success: "#00FF41", Warning: "#ADFF2F", Error: "#FF5555", Muted: "#4E9A63", Border: "#003B00", StatusBG: "#081C0D"},
	{Name: "monokai", Dark: true, Primary: "#F92672", Secondary: "#A6E22E", Accent: "#66D9EF", Success: "#A6E22E", Warning: "#E6DB74", Error: "#F92672", Muted: "#75715E", Border: "#49483E", StatusBG: "#272822"},
	{Name: "dracula", Dark: true, Primary: "#BD93F9", Secondary: "#FF79C6", Accent: "#8BE9FD", Success: "#50FA7B", Warning: "#F1FA8C", Error: "#FF5555", Muted: "#6272A4", Border: "#44475A", StatusBG: "#282A36"},
	{Name: "nord", Dark: true, Primary: "#88C0D0", Secondary: "#81A1C1", Accent: "#B48EAD", Success: "#A3BE8C", Warning: "#EBCB8B", Error: "#BF616A", Muted: "#616E88", Border: "#4C566A", StatusBG: "#2E3440"},
	{Name: "tokyo-night", Dark: true, Primary: "#7AA2F7", Secondary: "#BB9AF7", Accent: "#7DCFFF", Success: "#9ECE6A", Warning: "#E0AF68", Error: "#F7768E", Muted: "#565F89", Border: "#3B4261", StatusBG: "#1A1B26"},
	{Name: "fredhutch-dark", Dark: true, Primary: "#AA4AC4", Secondary: "#00ABC8", Accent: "#7FD7E8", Success: "#4CC38A", Warning: "#FFB500", Error: "#E5484D", Muted: "#64748B", Border: "#24324A", StatusBG: "#10192B"},
	{Name: "fredhutch-light", Dark: false, Primary: "#1B365D", Secondary: "#00ABC8", Accent: "#FFB500", Success: "#2E7D32", Warning: "#B45309", Error: "#C62828", Muted: "#64748B", Border: "#94A3B8", StatusBG: "#E2E8F0"},
}

func themeByName(name string) (Theme, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, t := range themes {
		if t.Name == name {
			return t, true
		}
	}
	return Theme{}, false
}

func defaultTheme() Theme {
	t, _ := themeByName(defaultThemeName)
	return t
}

func (t Theme) glamourStyle() string {
	if t.Dark {
		return "dark"
	}
	return "light"
}

// styles holds every lipgloss style derived from the active theme so they are
// computed once per theme switch instead of on every render.
type styles struct {
	brand       lipgloss.Style
	tabActive   lipgloss.Style
	tabInactive lipgloss.Style
	rule        lipgloss.Style
	userBadge   lipgloss.Style
	botBadge    lipgloss.Style
	tool        lipgloss.Style
	toolName    lipgloss.Style
	toolResult  lipgloss.Style
	system      lipgloss.Style
	errText     lipgloss.Style
	inputBox    lipgloss.Style
	paletteBox  lipgloss.Style
	paletteSel  lipgloss.Style
	paletteCmd  lipgloss.Style
	paletteDesc lipgloss.Style
	approvalBox lipgloss.Style
	statusBase  lipgloss.Style
	statusKey   lipgloss.Style
	muted       lipgloss.Style
	accent      lipgloss.Style
	success     lipgloss.Style
	warning     lipgloss.Style
	heading     lipgloss.Style
}

func newStyles(t Theme) styles {
	c := func(v string) lipgloss.Color { return lipgloss.Color(v) }
	return styles{
		brand:       lipgloss.NewStyle().Bold(true).Foreground(c(t.Primary)),
		tabActive:   lipgloss.NewStyle().Bold(true).Foreground(c(onColor(t.Primary))).Background(c(t.Primary)).Padding(0, 1),
		tabInactive: lipgloss.NewStyle().Foreground(c(t.Muted)).Padding(0, 1),
		rule:        lipgloss.NewStyle().Foreground(c(t.Border)),
		userBadge:   lipgloss.NewStyle().Bold(true).Foreground(c(onColor(t.Accent))).Background(c(t.Accent)).Padding(0, 1),
		botBadge:    lipgloss.NewStyle().Bold(true).Foreground(c(onColor(t.Secondary))).Background(c(t.Secondary)).Padding(0, 1),
		tool:        lipgloss.NewStyle().Foreground(c(t.Muted)),
		toolName:    lipgloss.NewStyle().Bold(true).Foreground(c(t.Accent)),
		toolResult:  lipgloss.NewStyle().Foreground(c(t.Muted)),
		system:      lipgloss.NewStyle().Foreground(c(t.Muted)).Italic(true),
		errText:     lipgloss.NewStyle().Bold(true).Foreground(c(t.Error)),
		inputBox:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c(t.Primary)),
		paletteBox:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c(t.Accent)).Padding(0, 1),
		paletteSel:  lipgloss.NewStyle().Bold(true).Foreground(c(onColor(t.Primary))).Background(c(t.Primary)),
		paletteCmd:  lipgloss.NewStyle().Bold(true).Foreground(c(t.Accent)),
		paletteDesc: lipgloss.NewStyle().Foreground(c(t.Muted)),
		approvalBox: lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(c(t.Warning)).Padding(0, 1),
		statusBase:  lipgloss.NewStyle().Background(c(t.StatusBG)).Foreground(c(t.Muted)),
		statusKey:   lipgloss.NewStyle().Background(c(t.StatusBG)).Foreground(c(t.Accent)).Bold(true),
		muted:       lipgloss.NewStyle().Foreground(c(t.Muted)),
		accent:      lipgloss.NewStyle().Foreground(c(t.Accent)),
		success:     lipgloss.NewStyle().Foreground(c(t.Success)),
		warning:     lipgloss.NewStyle().Foreground(c(t.Warning)),
		heading:     lipgloss.NewStyle().Bold(true).Foreground(c(t.Secondary)),
	}
}

// badge renders a small colored pill like " AUTOPILOT ".
func badge(text, bg string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(onColor(bg))).Background(lipgloss.Color(bg)).Padding(0, 1).Render(text)
}

// onColor picks a readable foreground for text placed on the given background.
func onColor(hex string) string {
	c, err := colorful.Hex(hex)
	if err != nil {
		return "#FFFFFF"
	}
	if l, _, _ := c.Lab(); l > 0.55 {
		return "#14141E"
	}
	return "#F8F8F5"
}

// gradient colors each rune of text along a linear blend between two colors.
func gradient(text, from, to string) string {
	a, errA := colorful.Hex(from)
	b, errB := colorful.Hex(to)
	if errA != nil || errB != nil {
		return text
	}
	var out strings.Builder
	for _, line := range strings.Split(text, "\n") {
		runes := []rune(line)
		n := len(runes)
		for i, r := range runes {
			if r == ' ' {
				out.WriteRune(r)
				continue
			}
			ratio := 0.0
			if n > 1 {
				ratio = float64(i) / float64(n-1)
			}
			blend := a.BlendLuv(b, ratio).Clamped()
			out.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(blend.Hex())).Render(string(r)))
		}
		out.WriteString("\n")
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// contextGauge renders a small usage bar such as ▰▰▰▱▱▱▱▱▱▱ 34%.
func contextGauge(t Theme, used, window, width int) string {
	if width < 4 {
		width = 4
	}
	if window <= 0 {
		return fmt.Sprintf("ctx ~%s", formatTokens(used))
	}
	frac := float64(used) / float64(window)
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	color := t.Success
	switch {
	case frac >= 0.85:
		color = t.Error
	case frac >= 0.60:
		color = t.Warning
	}
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(strings.Repeat("▰", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color(t.Border)).Render(strings.Repeat("▱", width-filled))
	return fmt.Sprintf("ctx %s %d%%", bar, int(frac*100+0.5))
}

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk tok", float64(n)/1000)
	}
	return fmt.Sprintf("%d tok", n)
}
