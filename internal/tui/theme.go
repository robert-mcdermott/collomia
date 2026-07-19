package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"
	"golang.org/x/term"
)

// Theme is a named terminal color palette. All values are hex colors so they
// render consistently on truecolor terminals and degrade via lipgloss on others.
type Theme struct {
	Name       string
	Dark       bool
	Primary    string
	Secondary  string
	Accent     string
	Success    string
	Warning    string
	Error      string
	Muted      string
	Border     string
	StatusBG   string
	Background string
}

const defaultThemeName = "collomia"

var themes = []Theme{
	{Name: "collomia", Dark: true, Primary: "#AF5FFF", Secondary: "#FF5FAF", Accent: "#5FD7FF", Success: "#42D77D", Warning: "#F1C40F", Error: "#FF6B6B", Muted: "#8A8A9E", Border: "#5F5F87", StatusBG: "#1A1A2E", Background: "#101018"},
	{Name: "synthwave", Dark: true, Primary: "#FF2E97", Secondary: "#00F0FF", Accent: "#B967FF", Success: "#72F1B8", Warning: "#FEDE5D", Error: "#FE4450", Muted: "#848BBD", Border: "#495495", StatusBG: "#241B2F", Background: "#191325"},
	{Name: "outrun", Dark: true, Primary: "#FF6C11", Secondary: "#FF3864", Accent: "#2DE2E6", Success: "#61E786", Warning: "#F9C80E", Error: "#FF3864", Muted: "#8B7F9E", Border: "#552E85", StatusBG: "#261447", Background: "#1A0E31"},
	{Name: "blade-runner-2049", Dark: true, Primary: "#00FFE5", Secondary: "#1AFFA3", Accent: "#FFB74D", Success: "#79E96D", Warning: "#E6E373", Error: "#FF6E6E", Muted: "#6FB7AE", Border: "#087A6B", StatusBG: "#001A1A", Background: "#001F1F"},
	{Name: "chaos-theory", Dark: true, Primary: "#61F21D", Secondary: "#EDF25E", Accent: "#0FC9F2", Success: "#61F21D", Warning: "#EDF25E", Error: "#F2594B", Muted: "#818C69", Border: "#495945", StatusBG: "#20261D", Background: "#0D0D0D"},
	{Name: "cyberpunk-2077-blue", Dark: true, Primary: "#0EF3FF", Secondary: "#FF2E97", Accent: "#FFD400", Success: "#3DD69C", Warning: "#FFD400", Error: "#EE1682", Muted: "#3E8EFD", Border: "#034685", StatusBG: "#060144", Background: "#03102C"},
	{Name: "cyberpunk-2077-violet", Dark: true, Primary: "#FF2CF1", Secondary: "#FF2E97", Accent: "#C832FF", Success: "#55F0B5", Warning: "#FFEA61", Error: "#EE1682", Muted: "#D46AA0", Border: "#7A0044", StatusBG: "#24002F", Background: "#120018"},
	{Name: "catppuccin-mocha", Dark: true, Primary: "#CBA6F7", Secondary: "#89B4FA", Accent: "#89DCEB", Success: "#A6E3A1", Warning: "#F9E2AF", Error: "#F38BA8", Muted: "#6C7086", Border: "#585B70", StatusBG: "#181825", Background: "#1E1E2E"},
	{Name: "gruvbox-dark", Dark: true, Primary: "#FABD2F", Secondary: "#FE8019", Accent: "#83A598", Success: "#B8BB26", Warning: "#FABD2F", Error: "#FB4934", Muted: "#928374", Border: "#504945", StatusBG: "#1D2021", Background: "#282828"},
	{Name: "rose-pine-moon", Dark: true, Primary: "#C4A7E7", Secondary: "#EB6F92", Accent: "#9CCFD8", Success: "#3E8FB0", Warning: "#F6C177", Error: "#EB6F92", Muted: "#908CAA", Border: "#393552", StatusBG: "#2A273F", Background: "#232136"},
	{Name: "kanagawa-wave", Dark: true, Primary: "#7E9CD8", Secondary: "#957FB8", Accent: "#E6C384", Success: "#98BB6C", Warning: "#FF9E3B", Error: "#E46876", Muted: "#727169", Border: "#54546D", StatusBG: "#16161D", Background: "#1F1F28"},
	{Name: "matrix", Dark: true, Primary: "#00FF41", Secondary: "#008F11", Accent: "#7FFF9E", Success: "#00FF41", Warning: "#ADFF2F", Error: "#FF5555", Muted: "#4E9A63", Border: "#003B00", StatusBG: "#081C0D", Background: "#04120A"},
	{Name: "monokai", Dark: true, Primary: "#F92672", Secondary: "#A6E22E", Accent: "#66D9EF", Success: "#A6E22E", Warning: "#E6DB74", Error: "#F92672", Muted: "#75715E", Border: "#49483E", StatusBG: "#272822", Background: "#1D1E19"},
	{Name: "dracula", Dark: true, Primary: "#BD93F9", Secondary: "#FF79C6", Accent: "#8BE9FD", Success: "#50FA7B", Warning: "#F1FA8C", Error: "#FF5555", Muted: "#6272A4", Border: "#44475A", StatusBG: "#282A36", Background: "#1D1F27"},
	{Name: "nord", Dark: true, Primary: "#88C0D0", Secondary: "#81A1C1", Accent: "#B48EAD", Success: "#A3BE8C", Warning: "#EBCB8B", Error: "#BF616A", Muted: "#616E88", Border: "#4C566A", StatusBG: "#2E3440", Background: "#242933"},
	{Name: "tokyo-night", Dark: true, Primary: "#7AA2F7", Secondary: "#BB9AF7", Accent: "#7DCFFF", Success: "#9ECE6A", Warning: "#E0AF68", Error: "#F7768E", Muted: "#565F89", Border: "#3B4261", StatusBG: "#1A1B26", Background: "#16161E"},
	{Name: "fredhutch-dark", Dark: true, Primary: "#AA4AC4", Secondary: "#00ABC8", Accent: "#7FD7E8", Success: "#4CC38A", Warning: "#FFB500", Error: "#E5484D", Muted: "#64748B", Border: "#24324A", StatusBG: "#10192B", Background: "#0B1220"},
	{Name: "fredhutch-light", Dark: false, Primary: "#1B365D", Secondary: "#00ABC8", Accent: "#FFB500", Success: "#2E7D32", Warning: "#B45309", Error: "#C62828", Muted: "#64748B", Border: "#94A3B8", StatusBG: "#E2E8F0", Background: "#F8FAFC"},
	// plain uses no color at all: structure comes from bold, reverse video,
	// and borders. Selected automatically when NO_COLOR is set.
	{Name: "plain", Dark: true},
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

// plain reports whether the theme intentionally renders without color
// (the `plain` theme, also auto-selected under NO_COLOR).
func (t Theme) plain() bool { return t.Primary == "" }

// panelText is the color for text inside a titled output panel: a lighter
// (dark themes) or darker (light themes) tint of Muted, so panel bodies read
// as part of the themed box rather than the terminal's raw default
// foreground, while staying easy on the eyes for paragraph-length output.
// Plain theme stays uncolored.
func (t Theme) panelText() string {
	if t.plain() {
		return ""
	}
	target := "#FFFFFF"
	if !t.Dark {
		target = "#000000"
	}
	muted, err := colorful.Hex(t.Muted)
	if err != nil {
		return t.Muted
	}
	edge, err := colorful.Hex(target)
	if err != nil {
		return t.Muted
	}
	return muted.BlendLuv(edge, 0.45).Clamped().Hex()
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
	statusBase  lipgloss.Style
	statusKey   lipgloss.Style
	muted       lipgloss.Style
	accent      lipgloss.Style
	success     lipgloss.Style
	warning     lipgloss.Style
	heading     lipgloss.Style
	panelTitle  lipgloss.Style
	panelBody   lipgloss.Style
}

func newStyles(t Theme) styles {
	c := func(v string) lipgloss.Color { return lipgloss.Color(v) }
	s := styles{
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
		statusBase:  lipgloss.NewStyle().Background(c(t.StatusBG)).Foreground(c(t.Muted)),
		statusKey:   lipgloss.NewStyle().Background(c(t.StatusBG)).Foreground(c(t.Accent)).Bold(true),
		muted:       lipgloss.NewStyle().Foreground(c(t.Muted)),
		accent:      lipgloss.NewStyle().Foreground(c(t.Accent)),
		success:     lipgloss.NewStyle().Foreground(c(t.Success)),
		warning:     lipgloss.NewStyle().Foreground(c(t.Warning)),
		heading:     lipgloss.NewStyle().Bold(true).Foreground(c(t.Secondary)),
		panelTitle:  lipgloss.NewStyle().Bold(true).Foreground(c(t.Accent)),
		panelBody:   lipgloss.NewStyle().Foreground(c(t.panelText())),
	}
	if t.plain() {
		// Empty colors already render as uncolored text; the styles that rely
		// on background fills fall back to reverse video so the active tab,
		// role badges, and palette selection stay distinguishable.
		pill := lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1)
		s.tabActive, s.userBadge, s.botBadge = pill, pill, pill
		s.paletteSel = lipgloss.NewStyle().Bold(true).Reverse(true)
		s.system = lipgloss.NewStyle().Italic(true)
		s.errText = lipgloss.NewStyle().Bold(true)
	}
	return s
}

// badge renders a small colored pill like " AUTOPILOT ". Without a color
// (plain theme) it falls back to reverse video.
func badge(text, bg string) string {
	if bg == "" {
		return lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1).Render(text)
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(onColor(bg))).Background(lipgloss.Color(bg)).Padding(0, 1).Render(text)
}

// onColor picks a readable foreground for text placed on the given background.
func onColor(hex string) string {
	if hex == "" {
		return ""
	}
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

// setTerminalBackground asks the hosting terminal to adopt the theme
// background via OSC 11 so unpainted cells match the theme. Terminals
// without OSC 11 support ignore the sequence. No-op when stdout is not a
// terminal (tests, pipes).
func setTerminalBackground(hex string) {
	if hex == "" {
		return
	}
	emitOSC(fmt.Sprintf("\x1b]11;%s\x07", hex))
}

// ResetTerminalBackground restores the terminal's default background color
// (OSC 111). Call it after the Bubble Tea program exits.
func ResetTerminalBackground() {
	emitOSC("\x1b]111\x07")
}

// emitOSC writes an OSC sequence to the terminal. Inside tmux the sequence is
// wrapped in tmux's DCS passthrough envelope (each ESC doubled) so it reaches
// the outer terminal; tmux 3.3+ additionally requires `allow-passthrough on`.
func emitOSC(seq string) {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	if os.Getenv("TMUX") != "" {
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	fmt.Fprint(os.Stdout, seq)
}

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk tok", float64(n)/1000)
	}
	return fmt.Sprintf("%d tok", n)
}
