package eval

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The decision that Orchestrated Goal will never be the default mode carries a
// condition: the documentation must say which work the mode is for, and every
// case it names must be one the evaluation matrix actually measured.
//
// That condition is easy to state and easy to let rot. Guidance outlives the
// measurements behind it — an evaluation gets renamed, or deleted because it
// looked redundant, and the user-facing claim it justified stays on the page
// with nothing under it. This test makes the condition mechanical: every
// evaluation the guidance cites must still exist.
//
// It deliberately does not check that the numbers in the prose match what the
// evaluations print. That would be brittle against fixtures and machines, and
// the evaluations are the record either way. What it checks is the thing that
// silently breaks: a citation pointing at nothing.

var guidanceCitation = regexp.MustCompile("`(Test[A-Za-z0-9_]*Evaluation)`")

func TestGuidanceCitesOnlyEvaluationsThatExist(t *testing.T) {
	root := filepath.Join("..", "..")
	guide, err := os.ReadFile(filepath.Join(root, "docs", "USER_GUIDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	section := whenToUseSection(t, string(guide))
	matches := guidanceCitation.FindAllStringSubmatch(section, -1)
	if len(matches) == 0 {
		t.Fatal("the when-to-use-it guidance cites no evaluation; the never-default decision requires every case it names to be measured")
	}

	defined := definedEvaluations(t)
	cited := map[string]bool{}
	for _, match := range matches {
		cited[match[1]] = true
	}
	var missing []string
	for name := range cited {
		if !defined[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("the guidance cites %d evaluation(s) that no longer exist: %s\n"+
			"Either restore them or remove the guidance they justified — a documented case with no measurement behind it is exactly what the never-default decision was meant to prevent.",
			len(missing), strings.Join(missing, ", "))
	}

	// Both halves have to be grounded. Guidance that only says when to use the
	// mode is marketing; the case against it is the half a person selecting an
	// optional mode most needs.
	lower := strings.ToLower(section)
	if !strings.Contains(lower, "do not reach for it when") {
		t.Fatal("the guidance has no 'when not to' half")
	}
	against := section[strings.Index(lower, "do not reach for it when"):]
	if len(guidanceCitation.FindAllString(against, -1)) == 0 {
		t.Fatal("the 'when not to' half cites no evaluation, so the case against the mode is unmeasured")
	}
}

// whenToUseSection returns the guidance subsection, failing loudly if it has
// been renamed or removed rather than silently passing on an empty string.
func whenToUseSection(t *testing.T, guide string) string {
	t.Helper()
	const heading = "#### When to use it, and when not to"
	start := strings.Index(guide, heading)
	if start < 0 {
		t.Fatalf("the user guide no longer contains %q; the never-default decision requires that guidance to exist", heading)
	}
	rest := guide[start+len(heading):]
	// The next heading of any level ends the section.
	if end := strings.Index(rest, "\n#"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func definedEvaluations(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, match := range regexp.MustCompile(`func (Test[A-Za-z0-9_]*)\(`).FindAllStringSubmatch(string(source), -1) {
			defined[match[1]] = true
		}
	}
	return defined
}
