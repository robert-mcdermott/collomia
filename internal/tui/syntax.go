package tui

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	colorful "github.com/lucasb-eyer/go-colorful"
)

// syntaxStyle maps semantic source-code tokens onto the active Collomia
// palette. The same style is shared by Markdown fences and source returned by
// read_file, so code looks consistent throughout the transcript.
func (t Theme) syntaxStyle() *chroma.Style {
	return t.syntaxStyleOn("collomia-"+t.Name, t.Background)
}

// syntaxStyleOn is syntaxStyle over a caller-chosen background. Diff previews
// use it to have Chroma paint the added/removed tint itself: emitting the
// background separately does not survive the SGR resets Chroma writes between
// tokens, which would leave the wash stopping at the first keyword.
func (t Theme) syntaxStyleOn(name, background string) *chroma.Style {
	text := t.panelText()
	if text == "" {
		text = "#D8D8DE"
	}
	return chroma.MustNewStyle(name, chroma.StyleEntries{
		chroma.Background:          "bg:" + background,
		chroma.Text:                text,
		chroma.Error:               "bold " + t.Error,
		chroma.Comment:             "italic " + t.Muted,
		chroma.CommentPreproc:      t.Warning,
		chroma.Keyword:             "bold " + t.Primary,
		chroma.KeywordReserved:     "bold " + t.Primary,
		chroma.KeywordNamespace:    t.Secondary,
		chroma.KeywordType:         t.Accent,
		chroma.Operator:            t.Secondary,
		chroma.Punctuation:         text,
		chroma.Name:                text,
		chroma.NameBuiltin:         t.Accent,
		chroma.NameTag:             t.Primary,
		chroma.NameAttribute:       t.Accent,
		chroma.NameClass:           "bold " + t.Accent,
		chroma.NameConstant:        t.Warning,
		chroma.NameDecorator:       t.Secondary,
		chroma.NameException:       t.Error,
		chroma.NameFunction:        t.Secondary,
		chroma.LiteralNumber:       t.Warning,
		chroma.LiteralString:       t.Success,
		chroma.LiteralStringEscape: t.Warning,
		chroma.GenericDeleted:      t.Error,
		chroma.GenericInserted:     t.Success,
		chroma.GenericHeading:      "bold " + t.Primary,
		chroma.GenericSubheading:   "bold " + t.Secondary,
	})
}

func (t Theme) markdownStyle() glamouransi.StyleConfig {
	if t.plain() {
		return glamourstyles.NoTTYStyleConfig
	}
	config := glamourstyles.DarkStyleConfig
	if !t.Dark {
		config = glamourstyles.LightStyleConfig
	}
	style := t.syntaxStyle()
	chromastyles.Register(style)
	// Use a named Chroma style rather than Glamour's shared anonymous style;
	// this lets a runtime /theme switch update code colors as well as chrome.
	config.CodeBlock.Chroma = nil
	config.CodeBlock.Theme = style.Name
	return config
}

// diffTintStrength is how far a status colour is pulled toward the
// background. Enough to tell an added line from a removed one at a glance;
// little enough that the syntax highlighting on top stays legible.
const diffTintStrength = 0.18

func (t Theme) diffTint(accent string) string {
	if t.plain() || t.Background == "" || accent == "" {
		return ""
	}
	base, errBase := colorful.Hex(t.Background)
	edge, errEdge := colorful.Hex(accent)
	if errBase != nil || errEdge != nil {
		return ""
	}
	return base.BlendLuv(edge, diffTintStrength).Clamped().Hex()
}

// looksLikeDiff reports whether a preview is a unified diff. Approval
// previews are not always diffs — a command preview is just text — and
// running plain text through the diff renderer would tint any line that
// happened to start with a hyphen.
func looksLikeDiff(lines []string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			return true
		}
	}
	return false
}

// renderDiffPreview draws a unified diff at a fixed width: added and removed
// rows carry a tinted wash to the right margin, and the code inside them is
// highlighted with the same lexer the transcript uses, so an approval shows
// the change the way an editor would rather than as two-tone plain text.
func (m Model) renderDiffPreview(lines []string, path string, width int) []string {
	addTint := m.theme.diffTint(m.theme.Success)
	delTint := m.theme.diffTint(m.theme.Error)
	var addStyle, delStyle *chroma.Style
	var lexer chroma.Lexer
	// A theme with no colour has nothing to highlight with, and a theme with
	// no declared background has nothing to tint against.
	if addTint != "" && delTint != "" {
		addStyle = m.theme.syntaxStyleOn("collomia-"+m.theme.Name+"-diffadd", addTint)
		delStyle = m.theme.syntaxStyleOn("collomia-"+m.theme.Name+"-diffdel", delTint)
		if lexer = lexers.Match(path); lexer == nil {
			lexer = lexers.Fallback
		}
		lexer = chroma.Coalesce(lexer)
	}

	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = expandTabs(line)
		switch {
		case strings.HasPrefix(line, "@@"):
			out = append(out, m.styles.accent.Render(ansi.Truncate(line, width, "…")))
			continue
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"),
			strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "):
			out = append(out, m.styles.muted.Render(ansi.Truncate(line, width, "…")))
			continue
		}

		marker, body := " ", line
		if line != "" {
			marker, body = line[:1], line[1:]
		}
		tint, style, markerStyle := "", (*chroma.Style)(nil), m.styles.muted
		switch marker {
		case "+":
			tint, style, markerStyle = addTint, addStyle, m.styles.success
		case "-":
			tint, style, markerStyle = delTint, delStyle, m.styles.errText
		}

		body = ansi.Truncate(body, max(1, width-1), "…")
		rendered := markerStyle.Background(lipgloss.Color(tint)).Render(marker)
		if style != nil {
			rendered += highlight(lexer, style, body)
		} else {
			rendered += markerStyle.Render(body)
		}
		if gap := width - 1 - ansi.StringWidth(body); gap > 0 {
			rendered += lipgloss.NewStyle().Background(lipgloss.Color(tint)).Render(strings.Repeat(" ", gap))
		}
		out = append(out, rendered)
	}
	return out
}

// expandTabs replaces tabs with spaces before anything measures the line.
// A tab has no display width of its own, but lipgloss expands it on render,
// so an indented line measured as fitting would come out four columns wider
// than the box holding it.
func expandTabs(line string) string {
	return strings.ReplaceAll(line, "\t", "    ")
}

func highlight(lexer chroma.Lexer, style *chroma.Style, code string) string {
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	var out bytes.Buffer
	if err := formatters.TTY16m.Format(&out, style, iterator); err != nil {
		return code
	}
	return strings.TrimRight(out.String(), "\n")
}

func (m *Model) highlightToolResult(entry block) (string, bool) {
	if m.theme.plain() {
		return "", false
	}
	var lexer chroma.Lexer
	switch entry.tool {
	case "read_file":
		path := strings.TrimSpace(strings.TrimPrefix(entry.summary, "read "))
		lexer = lexers.Match(path)
	case "git_diff":
		lexer = lexers.Get("diff")
	default:
		return "", false
	}
	if lexer == nil {
		return "", false
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, entry.content)
	if err != nil {
		return "", false
	}
	var out bytes.Buffer
	if err := formatters.TTY16m.Format(&out, m.theme.syntaxStyle(), iterator); err != nil {
		return "", false
	}
	return strings.TrimRight(out.String(), "\n"), true
}
