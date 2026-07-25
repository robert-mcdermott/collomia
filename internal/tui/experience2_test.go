package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// Splicing a modal into live content left mid-word fragments of the
// transcript hard against the dialog border, which reads as a corrupted
// redraw rather than as a dialog.
func TestOverlayClearsAGutterAroundTheModal(t *testing.T) {
	base := strings.Repeat(strings.Repeat("x", 40)+"\n", 9) + strings.Repeat("x", 40)
	overlay := "╭────╮\n│ hi │\n╰────╯"
	got := placeOverlay(base, overlay, 40, 10)
	lines := strings.Split(got, "\n")

	overlayWidth, overlayHeight := 6, 3
	x := (40 - overlayWidth) / 2
	y := (10 - overlayHeight) / 2
	for row := y - overlayGutter; row < y+overlayHeight+overlayGutter; row++ {
		plain := []rune(ansi.Strip(lines[row]))
		for _, col := range []int{x - 1, x + overlayWidth} {
			if plain[col] != ' ' {
				t.Fatalf("row %d column %d = %q, want a cleared gutter cell\n%s", row, col, plain[col], ansi.Strip(got))
			}
		}
	}
}

func TestOverlayDimsTheBaseLayer(t *testing.T) {
	prior := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prior) })

	colored := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3864")).Render(strings.Repeat("transcript ", 4))
	got := placeOverlay(colored+"\n"+colored, "╭──╮\n╰──╯", 40, 6)
	if strings.Contains(got, "38;2;255;56;100") {
		t.Fatalf("the base layer kept its foreground colour under a modal:\n%q", got)
	}
	if !strings.Contains(ansi.Strip(got), "transcript") {
		t.Fatal("the base layer should still be readable, just dimmed")
	}
}

func TestScrimLeavesBlankLinesAlone(t *testing.T) {
	if got := scrim("    "); got != "    " {
		t.Fatalf("scrim(%q) = %q, want it untouched", "    ", got)
	}
}

func TestRailAppearsOnlyWhenThereIsRoomForIt(t *testing.T) {
	cases := []struct {
		width int
		want  bool
	}{
		{width: 80, want: false},
		{width: railMinTotalWidth - 1, want: false},
		{width: railAutoWidth - 1, want: false},
		{width: railAutoWidth, want: true},
		{width: 200, want: true},
	}
	for _, tc := range cases {
		m := newTestModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 40})
		m = updated.(Model)
		if got := m.railVisible(); got != tc.want {
			t.Fatalf("width %d: railVisible = %t, want %t", tc.width, got, tc.want)
		}
	}
}

func TestRailToggleSurvivesResize(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	m = updated.(Model)
	if !m.railVisible() {
		t.Fatal("a 200-column terminal should show the rail by default")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}, Alt: true})
	m = updated.(Model)
	if m.railVisible() {
		t.Fatal("alt+r did not hide the rail")
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 210, Height: 40})
	m = updated.(Model)
	if m.railVisible() {
		t.Fatal("a resize overrode the user's explicit choice to hide the rail")
	}
}

// The rail borrows columns from the transcript. Taking them from the composer
// as well would punish the user for opening a reference panel.
func TestRailNarrowsOnlyTheTranscript(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	m = updated.(Model)
	if m.viewport.Width != m.width-railColumns {
		t.Fatalf("viewport width = %d, want %d", m.viewport.Width, m.width-railColumns)
	}
	if got := lipgloss.Width(strings.Split(m.renderComposer(), "\n")[0]); got != m.width {
		t.Fatalf("composer width = %d, want the full %d", got, m.width)
	}
	for i, line := range strings.Split(m.renderBody(), "\n") {
		if got := ansi.StringWidth(line); got != m.width {
			t.Fatalf("body line %d is %d columns, want %d", i, got, m.width)
		}
	}
}

func TestToolHeaderShowsOutcomeAndDuration(t *testing.T) {
	m := newTestModel(t)
	entry := block{
		role:    "tool",
		content: "run_command\x00go test ./...",
		status:  toolSucceeded,
		elapsed: 1500 * time.Millisecond,
	}
	got := ansi.Strip(m.renderToolHeader(entry))
	if !strings.HasPrefix(got, "✓ run_command") {
		t.Fatalf("header = %q, want it to lead with a success glyph and the tool name", got)
	}
	if !strings.HasSuffix(got, "1.5s") {
		t.Fatalf("header = %q, want the duration right-aligned", got)
	}

	entry.status, entry.elapsed = toolFailed, 20*time.Millisecond
	got = ansi.Strip(m.renderToolHeader(entry))
	if !strings.HasPrefix(got, "✗ run_command") {
		t.Fatalf("header = %q, want a failure glyph", got)
	}
	if strings.Contains(got, "20ms") {
		t.Fatalf("header = %q, want sub-100ms timings omitted", got)
	}
}

// A replayed session records what a tool did but not how long it took.
func TestReplayedToolHeaderHasNoOutcome(t *testing.T) {
	m := newTestModel(t)
	got := ansi.Strip(m.renderToolHeader(block{role: "tool", content: "read_file\x00read main.go"}))
	if !strings.HasPrefix(got, "⚙ read_file") {
		t.Fatalf("header = %q, want the neutral glyph", got)
	}
}

func TestCancelledTurnStopsClaimingToolsAreRunning(t *testing.T) {
	m := newTestModel(t)
	m.blocks = append(m.blocks, block{role: "tool", content: "run_command\x00sleep 30", status: toolRunning, started: time.Now()})
	m.settleRunningTools()
	if m.blocks[0].status == toolRunning {
		t.Fatal("a finished turn left a tool marked as still running")
	}
}

func TestWheelScrollsTheTranscript(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 200; i++ {
		m.blocks = append(m.blocks, block{role: "system", content: "line"})
	}
	m.refresh()
	m.viewport.GotoBottom()
	before := m.viewport.YOffset

	updated, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.viewport.YOffset >= before {
		t.Fatalf("wheel up did not scroll: %d -> %d", before, m.viewport.YOffset)
	}
	if m.chatFollow {
		t.Fatal("scrolling up should stop following the live turn")
	}
}

func TestClickSelectsATab(t *testing.T) {
	m := newTestModel(t)
	x := ansi.StringWidth(m.styles.brand.Render(" ✿ collomia ")) +
		ansi.StringWidth(m.styles.tabInactive.Render(tabNames[0])) + 1
	updated, _ := m.Update(tea.MouseMsg{X: x, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.tab != tabSession {
		t.Fatalf("tab = %d after clicking Session, want %d", m.tab, tabSession)
	}
}

func TestClickInTheTranscriptDoesNothing(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.MouseMsg{X: 10, Y: 12, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if updated.(Model).tab != tabChat {
		t.Fatal("a click in the transcript changed tabs")
	}
}

func TestEmptyStateOrientsTheUser(t *testing.T) {
	m := newTestModel(t)
	got := ansi.Strip(m.chatContent())
	for _, want := range []string{"workspace", "model", "autonomy", "Try", "/verify"} {
		if !strings.Contains(got, want) {
			t.Fatalf("empty state is missing %q:\n%s", want, got)
		}
	}
}

func TestEmptyStateYieldsToTheTranscript(t *testing.T) {
	m := newTestModel(t)
	m.blocks = append(m.blocks, block{role: "user", content: "hello"})
	if strings.Contains(ansi.Strip(m.chatContent()), "Try") {
		t.Fatal("the empty state should disappear once there is a transcript")
	}
}

func TestSessionTabUsesTwoColumnsWhenWide(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = updated.(Model)
	m.switchTab(tabSession)
	wide := ansi.Strip(m.sessionContent())

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)
	narrow := ansi.Strip(m.sessionContent())

	if strings.Count(wide, "\n") >= strings.Count(narrow, "\n") {
		t.Fatalf("two columns did not shorten the tab: %d rows wide vs %d narrow",
			strings.Count(wide, "\n"), strings.Count(narrow, "\n"))
	}
	for _, want := range []string{"Session", "Security", "Providers", "Themes"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("the two-column layout dropped the %q section", want)
		}
	}
	for i, line := range strings.Split(wide, "\n") {
		if got := len([]rune(line)); got > 160 {
			t.Fatalf("session line %d is %d columns, want at most 160", i, got)
		}
	}
}

func TestContextGaugeShowsSubCellProgress(t *testing.T) {
	theme, _ := themeByName("outrun")
	// Three percent of a ten-cell bar is a third of one cell: without partial
	// blocks the gauge would be indistinguishable from empty.
	if got := ansi.Strip(contextGauge(theme, 300, 10000, 10)); !strings.ContainsAny(got, "▏▎▍▌▋▊▉") {
		t.Fatalf("gauge = %q, want a partial block", got)
	}
	if got := ansi.Strip(contextGauge(theme, 10000, 10000, 10)); !strings.Contains(got, strings.Repeat("█", 10)) {
		t.Fatalf("a full window should fill the gauge; got %q", got)
	}
}
