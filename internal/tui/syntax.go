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
)

// syntaxStyle maps semantic source-code tokens onto the active Collomia
// palette. The same style is shared by Markdown fences and source returned by
// read_file, so code looks consistent throughout the transcript.
func (t Theme) syntaxStyle() *chroma.Style {
	text := t.panelText()
	if text == "" {
		text = "#D8D8DE"
	}
	return chroma.MustNewStyle("collomia-"+t.Name, chroma.StyleEntries{
		chroma.Background:          "bg:" + t.Background,
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
