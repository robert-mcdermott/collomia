package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The textarea keeps an unexported pointer to whichever style struct was
// current when it was last focused. Building it focused and then copying it
// into the Model left that pointer addressing a discarded local, so every
// themed assignment landed somewhere nothing rendered from and the prompt
// stayed the terminal's default grey.
func TestComposerPromptUsesThemeColor(t *testing.T) {
	m := newTestModel(t)
	theme, ok := themeByName("outrun")
	if !ok {
		t.Fatal("outrun theme missing")
	}
	m.applyTheme(theme)

	view := m.input.View()
	want := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary)).Bold(true).Render("❯")
	if !strings.Contains(view, strings.TrimSuffix(want, "\x1b[0m")) {
		t.Fatalf("composer prompt does not carry the theme's secondary color\nview: %q", view)
	}
}

// The default cursor line paints a solid background the width of the editor,
// which showed up as a grey bar across a themed composer.
func TestComposerCursorLineIsNotPainted(t *testing.T) {
	m := newTestModel(t)
	theme, _ := themeByName("outrun")
	m.applyTheme(theme)
	if got := m.input.FocusedStyle.CursorLine.GetBackground(); got != lipgloss.NoColor(struct{}{}) && got != nil {
		if _, isNo := got.(lipgloss.NoColor); !isNo {
			t.Fatalf("cursor line background = %#v, want none", got)
		}
	}
}

func TestComposerStartsAtOneRowAndGrows(t *testing.T) {
	m := newTestModel(t)
	if got := m.input.Height(); got != composerMinRows {
		t.Fatalf("initial composer height = %d, want %d", got, composerMinRows)
	}
	m.setComposerValue("one\ntwo\nthree")
	if got := m.input.Height(); got != 3 {
		t.Fatalf("composer height after a three-line draft = %d, want 3", got)
	}
	m.setComposerValue(strings.Repeat("x\n", composerMaxRows*2))
	if got := m.input.Height(); got != composerMaxRows {
		t.Fatalf("composer height = %d, want it capped at %d", got, composerMaxRows)
	}
}

// alt+enter and ctrl+j are documented as newline chords. The textarea binds
// newline insertion to "enter"/"ctrl+m" by default, neither of which either
// chord produces, so before the rebind both were swallowed silently.
func TestNewlineChordsInsertWithoutSending(t *testing.T) {
	for _, chord := range []tea.KeyMsg{
		{Type: tea.KeyEnter, Alt: true},
		{Type: tea.KeyCtrlJ},
	} {
		m := newTestModel(t)
		m.setComposerValue("first line")
		updated, _ := m.Update(chord)
		m = updated.(Model)
		if !strings.Contains(m.input.Value(), "\n") {
			t.Fatalf("%s did not insert a newline; value = %q", chord.String(), m.input.Value())
		}
		if m.busy {
			t.Fatalf("%s started a turn", chord.String())
		}
	}
}

func TestSmartContinuation(t *testing.T) {
	cases := []struct {
		name   string
		draft  string
		want   string
		extend bool
	}{
		{name: "trailing backslash consumes the marker", draft: `keep going\`, want: "keep going\n", extend: true},
		{name: "open fence", draft: "```go\nfunc main() {}", want: "```go\nfunc main() {}\n", extend: true},
		{name: "closed fence sends", draft: "```go\nx\n```", extend: false},
		{name: "ordinary prose sends", draft: "what does mount( do?", extend: false},
		{name: "plain text sends", draft: "hello", extend: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, extend := continueDraft(tc.draft)
			if extend != tc.extend {
				t.Fatalf("continueDraft(%q) extend = %t, want %t", tc.draft, extend, tc.extend)
			}
			if extend && got != tc.want {
				t.Fatalf("continueDraft(%q) = %q, want %q", tc.draft, got, tc.want)
			}
		})
	}
}

func TestEnterExtendsUnfinishedDraft(t *testing.T) {
	m := newTestModel(t)
	m.setComposerValue(`still typing\`)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.busy {
		t.Fatal("enter started a turn on a draft ending in a backslash")
	}
	if m.input.Value() != "still typing\n" {
		t.Fatalf("draft = %q, want the backslash replaced by a newline", m.input.Value())
	}
	if m.continuationHint() == "" {
		t.Fatal("a multi-line draft should explain how to send")
	}
}

// Terminals speaking a keyboard disambiguation protocol report shift+enter
// and ctrl+enter as CSI sequences Bubble Tea 1.x cannot parse, so they never
// arrive as a KeyMsg.
func TestUnknownCSISequenceInsertsNewline(t *testing.T) {
	m := newTestModel(t)
	m.setComposerValue("draft")
	msg := fakeCSIMsg(unknownCSIKey("\x1b[13;2u"))
	if !isNewlineSequence(msg) {
		t.Fatal("shift+enter CSI report was not recognised")
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if !strings.Contains(m.input.Value(), "\n") {
		t.Fatalf("value = %q, want a newline", m.input.Value())
	}
}

func TestUnrelatedCSISequenceIsIgnored(t *testing.T) {
	if isNewlineSequence(fakeCSIMsg(unknownCSIKey("\x1b[99;9u"))) {
		t.Fatal("an unrelated CSI report should not insert a newline")
	}
	if isNewlineSequence(tea.KeyMsg{Type: tea.KeyEnter}) {
		t.Fatal("a real key press is never a CSI report")
	}
}

type fakeCSIMsg string

func (f fakeCSIMsg) String() string { return string(f) }

// The palette padded every command to the width of the longest one, which is
// /mcp's argument list. On any ordinary terminal that pushed descriptions off
// the edge and lipgloss soft-wrapped each row into the next.
func TestPaletteRowsNeverWrap(t *testing.T) {
	for _, width := range []int{60, 80, 100, 140} {
		m := newTestModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		m = updated.(Model)
		m = typeKeys(t, m, "/")
		if !m.paletteOn {
			t.Fatalf("width %d: typing / did not open the palette", width)
		}
		rendered := m.renderPalette()
		for i, line := range strings.Split(rendered, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("width %d: palette line %d is %d columns:\n%s", width, i, got, ansi.Strip(line))
			}
		}
		if lines := strings.Count(rendered, "\n") + 1; lines != m.paletteHeight() {
			t.Fatalf("width %d: palette rendered %d lines but reserved %d", width, lines, m.paletteHeight())
		}
	}
}

func TestPaletteMatchesComposerWidth(t *testing.T) {
	m := newTestModel(t)
	m = typeKeys(t, m, "/")
	palette := lipgloss.Width(m.renderPalette())
	composer := lipgloss.Width(strings.Split(m.renderComposer(), "\n")[0])
	if palette != composer {
		t.Fatalf("palette width %d != composer width %d", palette, composer)
	}
}
