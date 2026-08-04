package app

import (
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
)

// The candidate wave's end state is the one thing users read wrongly: it stops
// having produced verified work and changed nothing, which looks like failure
// unless you know publication is a separate act. The notice says so at the last
// moment before worktrees exist.
func TestCandidateWaveNoticeWarnsOnlyWhenAWaveWillRun(t *testing.T) {
	writers := goalgraph.Spec{Goal: "produce candidates", Nodes: []goalgraph.NodeSpec{
		{ID: 1, Title: "change docs", Execution: goalgraph.ExecutionIsolatedWrite, WritePaths: []string{"docs/"}, Acceptance: []string{"docs checks pass"}},
	}}
	notice := candidateWaveNotice(writers)
	for _, phrase := range []string{
		"byte-for-byte unchanged",
		"that is the design, not a failure",
		"/orchestrate integrate",
		"still marked experimental",
	} {
		if !strings.Contains(notice, phrase) {
			t.Fatalf("the approval notice does not say %q:\n%s", phrase, notice)
		}
	}

	// An end-to-end graph is the graduated path and behaves the way people
	// already expect, so warning about it would be noise that teaches users to
	// skip the notice that matters.
	endToEnd := goalgraph.Spec{Goal: "implement", Nodes: []goalgraph.NodeSpec{
		{ID: 1, Title: "inspect", Execution: goalgraph.ExecutionReadOnly, Acceptance: []string{"evidence is grounded"}},
		{ID: 2, Title: "implement", DependsOn: []int{1}, Acceptance: []string{"tests pass"}},
	}}
	if got := candidateWaveNotice(endToEnd); got != "" {
		t.Fatalf("an end-to-end graph was warned about:\n%s", got)
	}
}
