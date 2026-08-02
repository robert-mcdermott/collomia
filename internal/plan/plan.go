// Package plan holds the structured plan artifact: explicit steps with
// status, dependencies, and evidence, maintained by the agent through the
// update_plan tool and persisted with the session. It replaces prose-only
// plans so progress is inspectable state, not chat scrollback.
package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type Step struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"` // pending, in_progress, done, blocked, skipped
	DependsOn []int  `json:"depends_on,omitempty"`
	// Evidence records how completion was verified (test run, file, output).
	Evidence string `json:"evidence,omitempty"`
}

type Plan struct {
	Goal  string `json:"goal"`
	Steps []Step `json:"steps"`
	// VerificationNote is a model-authored explanation for the exceptional
	// case where automated verification does not apply. It is not
	// machine-observed evidence and never substitutes for a command that could
	// meaningfully verify changed files.
	VerificationNote string    `json:"verification_note,omitempty"`
	Updated          time.Time `json:"updated"`
}

type CompletionState string

const (
	CompletionReady      CompletionState = "ready"
	CompletionIncomplete CompletionState = "incomplete"
	CompletionBlocked    CompletionState = "blocked"
)

// Completion is a deterministic assessment of whether a plan can truthfully
// finish. Issues are suitable for a model-visible controller notice; they are
// derived only from structured state, never from parsing an answer.
type Completion struct {
	State  CompletionState
	Issues []string
}

// Board is the shared, concurrency-safe holder for the current plan.
type Board struct {
	mu       sync.Mutex
	current  *Plan
	revision uint64
	// OnUpdate observes every plan change, for session persistence.
	OnUpdate func(Plan)
}

func NewBoard() *Board { return &Board{} }

func (b *Board) Current() *Plan {
	current, _ := b.Snapshot()
	return current
}

// Snapshot returns one consistent plan and revision. The revision changes on
// every Set, Restore, or Clear, letting a turn distinguish a newly maintained
// plan from a completed plan retained only as session history.
func (b *Board) Snapshot() (*Plan, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current == nil {
		return nil, b.revision
	}
	clone := *b.current
	clone.Steps = append([]Step(nil), b.current.Steps...)
	for i := range clone.Steps {
		clone.Steps[i].DependsOn = append([]int(nil), b.current.Steps[i].DependsOn...)
	}
	return &clone, b.revision
}

// Clear drops the current plan without notifying observers; used when
// switching sessions.
func (b *Board) Clear() {
	b.mu.Lock()
	b.current = nil
	b.revision++
	b.mu.Unlock()
}

// Restore installs a plan without notifying observers (it is already
// persisted in the session being loaded).
func (b *Board) Restore(p Plan) {
	b.mu.Lock()
	b.current = &p
	b.revision++
	b.mu.Unlock()
}

// Validate checks the complete plan contract without mutating a board. It is
// shared by new plan writes and completion assessment of restored legacy data.
func Validate(p Plan) error {
	if strings.TrimSpace(p.Goal) == "" {
		return fmt.Errorf("goal must not be empty")
	}
	if len(p.Steps) == 0 {
		return fmt.Errorf("plan must include at least one step")
	}
	seen := map[int]bool{}
	for i, step := range p.Steps {
		if step.ID == 0 {
			return fmt.Errorf("steps[%d] needs a non-zero id", i)
		}
		if strings.TrimSpace(step.Title) == "" {
			return fmt.Errorf("steps[%d] needs a non-empty title", i)
		}
		if seen[step.ID] {
			return fmt.Errorf("duplicate step id %d", step.ID)
		}
		seen[step.ID] = true
		switch step.Status {
		case "pending", "in_progress", "done", "blocked", "skipped":
		default:
			return fmt.Errorf("steps[%d] has invalid status %q", i, step.Status)
		}
		if (step.Status == "done" || step.Status == "blocked" || step.Status == "skipped") && strings.TrimSpace(step.Evidence) == "" {
			return fmt.Errorf("steps[%d] with status %q needs evidence or a reason", i, step.Status)
		}
	}
	for i, step := range p.Steps {
		dependencies := map[int]bool{}
		for _, dep := range step.DependsOn {
			if !seen[dep] {
				return fmt.Errorf("steps[%d] depends on unknown step %d", i, dep)
			}
			if dep == step.ID {
				return fmt.Errorf("steps[%d] cannot depend on itself", i)
			}
			if dependencies[dep] {
				return fmt.Errorf("steps[%d] repeats dependency %d", i, dep)
			}
			dependencies[dep] = true
		}
	}
	if cycle := dependencyCycle(p.Steps); len(cycle) > 0 {
		return fmt.Errorf("plan dependencies contain a cycle through step %d", cycle[0])
	}
	states := make(map[int]string, len(p.Steps))
	for _, step := range p.Steps {
		states[step.ID] = step.Status
	}
	for i, step := range p.Steps {
		if step.Status != "in_progress" && step.Status != "done" {
			continue
		}
		for _, dep := range step.DependsOn {
			if states[dep] != "done" && states[dep] != "skipped" {
				return fmt.Errorf("steps[%d] is %q but dependency %d is %q", i, step.Status, dep, states[dep])
			}
		}
	}
	return nil
}

func (b *Board) Set(p Plan) error {
	if err := Validate(p); err != nil {
		return err
	}
	p.Updated = time.Now().UTC()
	b.mu.Lock()
	b.current = &p
	b.revision++
	notify := b.OnUpdate
	b.mu.Unlock()
	if notify != nil {
		notify(p)
	}
	return nil
}

func dependencyCycle(steps []Step) []int {
	edges := make(map[int][]int, len(steps))
	for _, step := range steps {
		edges[step.ID] = append([]int(nil), step.DependsOn...)
	}
	visiting := map[int]bool{}
	visited := map[int]bool{}
	var visit func(int) []int
	visit = func(id int) []int {
		if visiting[id] {
			return []int{id}
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range edges[id] {
			if cycle := visit(dependency); len(cycle) > 0 {
				return cycle
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, step := range steps {
		if cycle := visit(step.ID); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}

// AssessCompletion interprets status and evidence without changing the plan.
// Set rejects these gaps for new plans, while this method also protects
// restored plans written by older Collomia versions.
func (p *Plan) AssessCompletion() Completion {
	if p == nil {
		return Completion{State: CompletionReady}
	}
	if err := Validate(*p); err != nil {
		return Completion{State: CompletionIncomplete, Issues: []string{"active plan is invalid: " + err.Error()}}
	}
	var issues []string
	blocked := false
	for _, step := range p.Steps {
		switch step.Status {
		case "pending", "in_progress":
			issues = append(issues, fmt.Sprintf("plan step %d (%s) is %s", step.ID, step.Title, step.Status))
		case "done", "skipped":
			if strings.TrimSpace(step.Evidence) == "" {
				issues = append(issues, fmt.Sprintf("plan step %d (%s) is %s without evidence or a reason", step.ID, step.Title, step.Status))
			}
		case "blocked":
			if strings.TrimSpace(step.Evidence) == "" {
				issues = append(issues, fmt.Sprintf("plan step %d (%s) is blocked without a reason", step.ID, step.Title))
			} else {
				blocked = true
			}
		default:
			issues = append(issues, fmt.Sprintf("plan step %d (%s) has unknown status %q", step.ID, step.Title, step.Status))
		}
	}
	if len(issues) > 0 {
		return Completion{State: CompletionIncomplete, Issues: issues}
	}
	if blocked {
		return Completion{State: CompletionBlocked}
	}
	return Completion{State: CompletionReady}
}

// Render formats the plan for the TUI and tool results.
func (p *Plan) Render() string {
	if p == nil || len(p.Steps) == 0 {
		return "No plan recorded. The agent maintains one with the update_plan tool."
	}
	marks := map[string]string{"pending": "[ ]", "in_progress": "[~]", "done": "[x]", "blocked": "[!]", "skipped": "[-]"}
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", p.Goal)
	for _, step := range p.Steps {
		fmt.Fprintf(&b, "%s %d. %s", marks[step.Status], step.ID, step.Title)
		if len(step.DependsOn) > 0 {
			deps := make([]string, len(step.DependsOn))
			for i, d := range step.DependsOn {
				deps[i] = fmt.Sprint(d)
			}
			fmt.Fprintf(&b, " (after %s)", strings.Join(deps, ","))
		}
		if step.Evidence != "" {
			fmt.Fprintf(&b, " — %s", step.Evidence)
		}
		b.WriteString("\n")
	}
	if p.VerificationNote != "" {
		fmt.Fprintf(&b, "Verification not applicable: %s\n", p.VerificationNote)
	}
	return b.String()
}

// Tool returns the update_plan tool bound to a board. Updating the plan is
// read-risk: it changes agent state, never the repository.
func Tool(board *Board) tools.Tool {
	return tools.Function{
		Def: provider.ToolDefinition{
			Name:        "update_plan",
			Description: "Create or update the structured task plan. Send the complete plan each time: a goal and steps with id, title, status (pending|in_progress|done|blocked|skipped), optional depends_on ids, and evidence. Done steps require evidence; blocked and skipped steps require a reason in evidence. If files changed and no meaningful automated verification applies, set verification_note to the specific reason; it is an explicit model-authored exception, not machine-observed proof. Keep the plan current as work progresses; it is shown to the user.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string","minLength":1},"steps":{"type":"array","minItems":1,"items":{"type":"object","properties":{"id":{"type":"integer"},"title":{"type":"string","minLength":1},"status":{"type":"string","enum":["pending","in_progress","done","blocked","skipped"]},"depends_on":{"type":"array","items":{"type":"integer"}},"evidence":{"type":"string"}},"required":["id","title","status"],"additionalProperties":false}},"verification_note":{"type":"string","description":"specific reason automated verification does not apply after changed files; not machine-observed evidence"}},"required":["goal","steps"],"additionalProperties":false}`),
		},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "update the task plan"},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var p Plan
			if err := json.Unmarshal(raw, &p); err != nil {
				return "", err
			}
			if err := board.Set(p); err != nil {
				return "", err
			}
			return "Plan updated:\n" + board.Current().Render(), nil
		},
	}
}
