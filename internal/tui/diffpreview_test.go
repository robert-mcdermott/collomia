package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Every theme has to survive the diff renderer. Chroma builds its styles from
// string entries, so a theme with no declared background would otherwise hand
// it "bg:" and take the whole approval dialog down with it.
func TestDiffPreviewRendersUnderEveryTheme(t *testing.T) {
	lines := []string{
		"@@ -1,4 +1,4 @@",
		" func main() {",
		`-	fmt.Println("old")`,
		`+	fmt.Println("new")`,
		" }",
	}
	want := expandTabs(strings.Join(lines, "\n"))
	for _, theme := range themes {
		t.Run(theme.Name, func(t *testing.T) {
			m := newTestModel(t)
			m.applyTheme(theme)
			for i, got := range m.renderDiffPreview(lines, "main.go", 60) {
				if width := ansi.StringWidth(got); width > 60 {
					t.Fatalf("line %d is %d columns, want at most 60", i, width)
				}
				if plain := strings.TrimRight(ansi.Strip(got), " "); !strings.Contains(want, plain) {
					t.Fatalf("line %d text changed: %q", i, plain)
				}
			}
		})
	}
}

// A command preview is plain text, and a hyphenated line in it is not a
// deletion.
func TestPlainPreviewIsNotTreatedAsADiff(t *testing.T) {
	if looksLikeDiff([]string{"- run the migration", "- restart the service"}) {
		t.Fatal("a bulleted list was mistaken for a unified diff")
	}
	if !looksLikeDiff([]string{"@@ -1 +1 @@", "-a", "+b"}) {
		t.Fatal("a unified diff was not recognised")
	}
}
