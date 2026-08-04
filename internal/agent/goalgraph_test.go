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

// The completion answer also has to carry checks a later node's mutation left
// behind. The model writes the closing summary from inside the last node, where
// an earlier node's suite is not something it re-ran or is likely to mention.
func TestGoalDoneAnswerNamesChecksALaterMutationLeftBehind(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "two features", Nodes: []goalgraph.NodeSpec{
		{ID: 1, Title: "feature A with tests", Acceptance: []string{"A's tests pass"}},
		{ID: 2, Title: "feature B with tests", DependsOn: []int{1}, Acceptance: []string{"B's tests pass"}},
	}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	drive := func(startToken, endToken, command string) {
		t.Helper()
		_, attempt, startErr := graph.StartNext(t.Context(), startToken)
		if startErr != nil {
			t.Fatal(startErr)
		}
		if err := graph.BeginTool(t.Context(), attempt.ID, goalgraph.ToolAction{Tool: "edit_file", Risk: "write", Summary: "change", PotentialMutation: true, NonReplayable: true}, startToken); err != nil {
			t.Fatal(err)
		}
		if err := graph.FinishTool(t.Context(), attempt.ID, goalgraph.ToolResult{Tool: "edit_file", Risk: "write", Summary: "changed", WorkspaceToken: endToken}); err != nil {
			t.Fatal(err)
		}
		if err := graph.BeginTool(t.Context(), attempt.ID, goalgraph.ToolAction{Tool: "run_command", Risk: "execute", Summary: command, PotentialMutation: true, NonReplayable: true}, endToken); err != nil {
			t.Fatal(err)
		}
		if err := graph.FinishTool(t.Context(), attempt.ID, goalgraph.ToolResult{Tool: "run_command", Command: command, Risk: "execute", Summary: "ok", Verification: true, WorkspaceToken: endToken}); err != nil {
			t.Fatal(err)
		}
		if _, err := graph.ProposeCompletion(t.Context(), "node complete and checked", endToken); err != nil {
			t.Fatal(err)
		}
	}
	drive("state-0", "state-A", "go test ./featureA")
	drive("state-A", "state-B", "go test ./featureB")

	answer := goalDoneAnswer("both features are implemented and tested", graph)
	if !strings.HasPrefix(answer, "both features are implemented and tested") {
		t.Fatalf("the model's own answer was discarded: %q", answer)
	}
	for _, phrase := range []string{"passed against a workspace that later work changed", "node 1 (feature A with tests)", "go test ./featureA", "not established either way"} {
		if !strings.Contains(answer, phrase) {
			t.Fatalf("the answer does not disclose %q:\n%s", phrase, answer)
		}
	}
	// Node 2's own check described the state the plan finished in, so naming it
	// would be noise that teaches the reader to skip the warning.
	if strings.Contains(answer, "go test ./featureB") {
		t.Fatalf("a check bound to the final workspace was reported as behind:\n%s", answer)
	}
}
