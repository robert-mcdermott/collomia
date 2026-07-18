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
	Goal    string    `json:"goal"`
	Steps   []Step    `json:"steps"`
	Updated time.Time `json:"updated"`
}

// Board is the shared, concurrency-safe holder for the current plan.
type Board struct {
	mu      sync.Mutex
	current *Plan
	// OnUpdate observes every plan change, for session persistence.
	OnUpdate func(Plan)
}

func NewBoard() *Board { return &Board{} }

func (b *Board) Current() *Plan {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current == nil {
		return nil
	}
	clone := *b.current
	clone.Steps = append([]Step(nil), b.current.Steps...)
	return &clone
}

// Clear drops the current plan without notifying observers; used when
// switching sessions.
func (b *Board) Clear() {
	b.mu.Lock()
	b.current = nil
	b.mu.Unlock()
}

// Restore installs a plan without notifying observers (it is already
// persisted in the session being loaded).
func (b *Board) Restore(p Plan) {
	b.mu.Lock()
	b.current = &p
	b.mu.Unlock()
}

func (b *Board) Set(p Plan) error {
	seen := map[int]bool{}
	for i, step := range p.Steps {
		if step.ID == 0 {
			return fmt.Errorf("steps[%d] needs a non-zero id", i)
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
	}
	for i, step := range p.Steps {
		for _, dep := range step.DependsOn {
			if !seen[dep] {
				return fmt.Errorf("steps[%d] depends on unknown step %d", i, dep)
			}
		}
	}
	p.Updated = time.Now().UTC()
	b.mu.Lock()
	b.current = &p
	notify := b.OnUpdate
	b.mu.Unlock()
	if notify != nil {
		notify(p)
	}
	return nil
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
	return b.String()
}

// Tool returns the update_plan tool bound to a board. Updating the plan is
// read-risk: it changes agent state, never the repository.
func Tool(board *Board) tools.Tool {
	return tools.Function{
		Def: provider.ToolDefinition{
			Name:        "update_plan",
			Description: "Create or update the structured task plan. Send the complete plan each time: a goal and steps with id, title, status (pending|in_progress|done|blocked|skipped), optional depends_on ids, and evidence for completed steps (e.g. the test command that proved it). Keep it current as work progresses; it is shown to the user.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string"},"steps":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"title":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","done","blocked","skipped"]},"depends_on":{"type":"array","items":{"type":"integer"}},"evidence":{"type":"string"}},"required":["id","title","status"],"additionalProperties":false}}},"required":["goal","steps"],"additionalProperties":false}`),
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
