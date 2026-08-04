package agent

import (
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
)

// The other completion path: a revision drops one node while later nodes still
// finish normally, so the turn ends on the model's own closing message. That
// message is written by the party that proposed the removal, which is why the
// runtime appends its own account rather than trusting the summary.
func TestGoalDoneAnswerAppendsTheRetirementAccount(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "implement and document", Nodes: []goalgraph.NodeSpec{
		{ID: 1, Title: "implement", Acceptance: []string{"tests pass"}},
		{ID: 2, Title: "document", Acceptance: []string{"docs updated"}},
	}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plain := goalDoneAnswer("all done", graph); plain != "all done" {
		t.Fatalf("an untouched plan changed the answer: %q", plain)
	}

	if err := graph.Revise(t.Context(), graph.Snapshot().Generation, "documentation is out of scope", goalgraph.Spec{
		Goal:  "implement and document",
		Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "implement", Acceptance: []string{"tests pass"}}},
	}); err != nil {
		t.Fatal(err)
	}
	answer := goalDoneAnswer("all done", graph)
	if !strings.HasPrefix(answer, "all done") {
		t.Fatalf("the model's own answer was discarded: %q", answer)
	}
	for _, phrase := range []string{"approved plan was reduced", "without completing", "node 2 (document)", "documentation is out of scope"} {
		if !strings.Contains(answer, phrase) {
			t.Fatalf("the answer does not disclose %q:\n%s", phrase, answer)
		}
	}
}
