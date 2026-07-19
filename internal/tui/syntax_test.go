package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownCodeFenceUsesSyntaxColors(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	m := newTestModel(t)
	got := m.renderMarkdown("```go\npackage main\n\nfunc main() { println(\"hello\") }\n```")
	if !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("fenced Go code has no true-color syntax output:\n%s", got)
	}
	plain := ansi.Strip(got)
	if !strings.Contains(plain, "package main") || !strings.Contains(plain, "func main") {
		t.Fatalf("highlighting changed code content:\n%s", plain)
	}
}

func TestReadFileToolResultUsesFilenameLexer(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	m := newTestModel(t)
	m.blocks = append(m.blocks, block{
		role:    "tool-result",
		tool:    "read_file",
		summary: "read /workspace/main.go",
		content: "     1\tpackage main\n     2\tfunc answer() int { return 42 }",
	})
	got := m.renderToolResult(len(m.blocks) - 1)
	if !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("read_file source has no syntax colors:\n%s", got)
	}
	if plain := ansi.Strip(got); !strings.Contains(plain, "package main") || !strings.Contains(plain, "return 42") {
		t.Fatalf("highlighting changed read_file output:\n%s", plain)
	}
}

func TestSyntaxColorsFollowRuntimeThemeSwitch(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	m := newTestModel(t)
	matrix, _ := themeByName("matrix")
	m.applyTheme(matrix)
	matrixCode := m.renderMarkdown("```go\npackage main\n```")
	if !strings.Contains(matrixCode, "38;2;0;143;17") {
		t.Fatalf("matrix keyword color missing from code:\n%s", matrixCode)
	}

	dracula, _ := themeByName("dracula")
	m.applyTheme(dracula)
	draculaCode := m.renderMarkdown("```go\npackage main\n```")
	if !strings.Contains(draculaCode, "38;2;255;121;198") {
		t.Fatalf("dracula keyword color missing after theme switch:\n%s", draculaCode)
	}
}

func TestPlainThemeDisablesToolSyntaxColors(t *testing.T) {
	m := newTestModel(t)
	plain, _ := themeByName("plain")
	m.applyTheme(plain)
	m.blocks = append(m.blocks, block{
		role:    "tool-result",
		tool:    "read_file",
		summary: "read main.go",
		content: "package main",
	})
	if got := m.renderToolResult(len(m.blocks) - 1); strings.Contains(got, "\x1b[") {
		t.Fatalf("plain theme emitted ANSI styling: %q", got)
	}
}
