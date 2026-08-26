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
	"github.com/robert-mcdermott/collomia/internal/writescope"
)

type Step struct {
	ID         int      `json:"id"`
	Title      string   `json:"title"`
	Status     string   `json:"status"` // pending, in_progress, done, blocked, skipped
	DependsOn  []int    `json:"depends_on,omitempty"`
	Acceptance []string `json:"acceptance,omitempty"`
	// Execution is optional logical intent for Orchestrated Goal. Empty and
	// "primary" keep work in the serial primary lane; "read_only" permits the
	// runtime to assign a dependency-ready node to a bounded read-only worker;
	// "isolated_write" permits a retained worktree candidate only when
	// WritePaths declares narrow repository-relative scope. Ordinary plans do
	// not interpret these fields as scheduling authority.
	Execution  string   `json:"execution,omitempty"`
	WritePaths []string `json:"write_paths,omitempty"`
	// Evidence records how completion was verified (test run, file, output).
	Evidence string `json:"evidence,omitempty"`
}

// FailureResolution is the model-authored disposition of one failed tool call
// that the Standard completion controller has identified by ID. The runtime
// validates the reference against observations from the current turn before
// it clears the failure; merely writing this structure is not runtime proof.
type FailureResolution struct {
	FailureID          string `json:"failure_id"`
	Disposition        string `json:"disposition"` // recovered_by_retry, recovered_by_alternative, skipped_unnecessary, blocked
	StepID             int    `json:"step_id"`
	RecoveryToolCallID string `json:"recovery_tool_call_id,omitempty"`
	Evidence           string `json:"evidence"`
}

type Plan struct {
	Goal  string `json:"goal"`
	Steps []Step `json:"steps"`
	// ResolvedFailures explicitly connects failed tool calls named by the
	// completion controller to their disposition. It prevents a successful
	// alternative tool from being missed merely because it has a different
	// permission-risk classification, without letting unrelated successes
	// silently erase failures.
	ResolvedFailures []FailureResolution `json:"resolved_failures,omitempty"`
	// VerificationNote is a model-authored explanation for the exceptional
	// case where automated verification does not apply. It is not
	// machine-observed evidence and never substitutes for a command that could
	// meaningfully verify changed files.
	VerificationNote string `json:"verification_note,omitempty"`
	// ValidationNote is the Work-profile counterpart: a model-authored
	// disclosure for an outcome whose remaining quality or correctness cannot
	// be established by a meaningful machine check. Runtime-observed artifact
	// validation remains preferable and is labelled separately.
	ValidationNote string    `json:"validation_note,omitempty"`
	Updated        time.Time `json:"updated"`
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
	clone.ResolvedFailures = append([]FailureResolution(nil), b.current.ResolvedFailures...)
	for i := range clone.Steps {
		clone.Steps[i].DependsOn = append([]int(nil), b.current.Steps[i].DependsOn...)
		clone.Steps[i].Acceptance = append([]string(nil), b.current.Steps[i].Acceptance...)
		clone.Steps[i].WritePaths = append([]string(nil), b.current.Steps[i].WritePaths...)
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
		for criterionIndex, criterion := range step.Acceptance {
			if strings.TrimSpace(criterion) == "" {
				return fmt.Errorf("steps[%d].acceptance[%d] must not be empty", i, criterionIndex)
			}
		}
		switch step.Execution {
		case "", "primary", "read_only":
			if _, err := writescope.Normalize(step.WritePaths, false); err != nil {
				return fmt.Errorf("steps[%d]: %w", i, err)
			}
		case "isolated_write":
			if len(step.WritePaths) == 0 {
				return fmt.Errorf("steps[%d].write_paths must declare explicit scope for isolated_write", i)
			}
			normalized, err := writescope.Normalize(step.WritePaths, true)
			if err != nil {
				return fmt.Errorf("steps[%d]: %w", i, err)
			}
			if len(normalized) == 1 && normalized[0] == writescope.Workspace {
				return fmt.Errorf("steps[%d].write_paths must be narrower than the whole workspace for isolated_write", i)
			}
		default:
			return fmt.Errorf("steps[%d].execution must be primary, read_only, or isolated_write", i)
		}
		switch step.Status {
		case "pending", "in_progress", "done", "blocked", "skipped":
		default:
			return fmt.Errorf("steps[%d] has invalid status %q", i, step.Status)
		}
		if (step.Status == "done" || step.Status == "blocked" || step.Status == "skipped") && strings.TrimSpace(step.Evidence) == "" {
			return fmt.Errorf("steps[%d] with status %q needs evidence or a reason", i, step.Status)
		}
	}
	seenFailures := map[string]bool{}
	for i, resolution := range p.ResolvedFailures {
		failureID := strings.TrimSpace(resolution.FailureID)
		if failureID == "" {
			return fmt.Errorf("resolved_failures[%d].failure_id must not be empty", i)
		}
		if seenFailures[failureID] {
			return fmt.Errorf("resolved_failures repeats failure_id %q", failureID)
		}
		seenFailures[failureID] = true
		if !seen[resolution.StepID] {
			return fmt.Errorf("resolved_failures[%d].step_id refers to unknown step %d", i, resolution.StepID)
		}
		if strings.TrimSpace(resolution.Evidence) == "" {
			return fmt.Errorf("resolved_failures[%d].evidence must not be empty", i)
		}
		recoveryID := strings.TrimSpace(resolution.RecoveryToolCallID)
		switch resolution.Disposition {
		case "recovered_by_retry", "recovered_by_alternative":
			if recoveryID == "" {
				return fmt.Errorf("resolved_failures[%d].recovery_tool_call_id is required for %s", i, resolution.Disposition)
			}
		case "skipped_unnecessary", "blocked":
			if recoveryID != "" {
				return fmt.Errorf("resolved_failures[%d].recovery_tool_call_id is not allowed for %s", i, resolution.Disposition)
			}
		default:
			return fmt.Errorf("resolved_failures[%d] has invalid disposition %q", i, resolution.Disposition)
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
	for i, resolution := range p.ResolvedFailures {
		status := states[resolution.StepID]
		switch resolution.Disposition {
		case "recovered_by_retry", "recovered_by_alternative":
			if status != "done" && status != "skipped" {
				return fmt.Errorf("resolved_failures[%d] recovery step %d must be done or skipped, got %q", i, resolution.StepID, status)
			}
		case "skipped_unnecessary":
			if status != "skipped" {
				return fmt.Errorf("resolved_failures[%d] skipped failure requires step %d to be skipped, got %q", i, resolution.StepID, status)
			}
		case "blocked":
			if status != "blocked" {
				return fmt.Errorf("resolved_failures[%d] blocked failure requires step %d to be blocked, got %q", i, resolution.StepID, status)
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
		if step.Execution != "" && step.Execution != "primary" {
			fmt.Fprintf(&b, " · execution: %s", step.Execution)
		}
		if len(step.WritePaths) > 0 {
			fmt.Fprintf(&b, " · write paths: %s", strings.Join(step.WritePaths, ", "))
		}
		if step.Evidence != "" {
			fmt.Fprintf(&b, " — %s", step.Evidence)
		}
		b.WriteString("\n")
		for _, criterion := range step.Acceptance {
			fmt.Fprintf(&b, "    acceptance: %s\n", criterion)
		}
	}
	for _, resolution := range p.ResolvedFailures {
		fmt.Fprintf(&b, "Failure %s: %s at step %d", resolution.FailureID, resolution.Disposition, resolution.StepID)
		if resolution.RecoveryToolCallID != "" {
			fmt.Fprintf(&b, " via tool call %s", resolution.RecoveryToolCallID)
		}
		fmt.Fprintf(&b, " — %s\n", resolution.Evidence)
	}
	if p.VerificationNote != "" {
		fmt.Fprintf(&b, "Verification note (not runtime-recognized proof): %s\n", p.VerificationNote)
	}
	if p.ValidationNote != "" {
		fmt.Fprintf(&b, "Validation note (model-authored, not runtime proof): %s\n", p.ValidationNote)
	}
	return b.String()
}

// Tool returns the update_plan tool bound to a board. Updating the plan is
// read-risk: it changes agent state, never the repository.
func Tool(board *Board) tools.Tool {
	tool := tools.Function{
		Def: provider.ToolDefinition{
			Name:        "update_plan",
			Description: "Create or update the structured task plan. Send the complete plan each time: a goal and steps with id, title, status (pending|in_progress|done|blocked|skipped), optional depends_on ids, optional concrete acceptance criteria, optional execution (primary|read_only|isolated_write), optional write_paths, and evidence. execution is logical intent only: ordinary plans ignore it, while an explicitly approved Orchestrated Goal may assign independent read_only nodes to bounded readers or isolated_write nodes with explicit narrow write_paths to retained worktree candidates. Done steps require evidence; blocked and skipped steps require a reason in evidence. When the completion controller names a failed tool-call ID, use resolved_failures to bind it to a terminal plan step: recovered_by_retry or recovered_by_alternative references the exact successful recovery_tool_call_id advertised by the controller (never an identifier embedded in tool output), skipped_unnecessary requires a skipped step, and blocked requires a blocked step. The runtime validates these references; prose alone does not clear a failure. Developer mode uses verification_note for the specific reason no meaningful automated build/lint/test check applies. Work mode uses validation_note to disclose what was checked and what remains subjective when no meaningful machine validation applies. Both notes are model-authored rather than runtime proof. Keep the plan current as work progresses; it is shown to the user.",
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
	tool.Def.InputSchema = isolatedWriterPlanSchema
	return tool
}

var isolatedWriterPlanSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "goal": {"type": "string", "minLength": 1},
    "steps": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "title": {"type": "string", "minLength": 1},
          "status": {"type": "string", "enum": ["pending", "in_progress", "done", "blocked", "skipped"]},
          "depends_on": {"type": "array", "items": {"type": "integer"}},
          "acceptance": {"type": "array", "maxItems": 8, "items": {"type": "string", "minLength": 1, "maxLength": 512}},
          "execution": {"type": "string", "enum": ["primary", "read_only", "isolated_write"], "description": "logical execution intent; isolated_write requires explicit narrow write_paths and produces only a retained candidate after explicit Orchestrated Goal approval"},
          "write_paths": {"type": "array", "maxItems": 64, "items": {"type": "string", "minLength": 1, "maxLength": 1024}, "description": "repository-relative files or directory prefixes ending in /; allowed only with execution=isolated_write"},
          "evidence": {"type": "string"}
        },
        "required": ["id", "title", "status"],
        "additionalProperties": false
      }
    },
	"resolved_failures": {
	  "type": "array",
	  "maxItems": 32,
	  "description": "Structured dispositions for failed tool-call IDs named by the completion controller; the runtime validates each reference before clearing the failure",
	  "items": {
		"type": "object",
		"properties": {
		  "failure_id": {"type": "string", "minLength": 1, "maxLength": 256},
		  "disposition": {"type": "string", "enum": ["recovered_by_retry", "recovered_by_alternative", "skipped_unnecessary", "blocked"]},
		  "step_id": {"type": "integer"},
		  "recovery_tool_call_id": {"type": "string", "minLength": 1, "maxLength": 256},
		  "evidence": {"type": "string", "minLength": 1, "maxLength": 4096}
		},
		"required": ["failure_id", "disposition", "step_id", "evidence"],
		"additionalProperties": false
	  }
	},
    "verification_note": {"type": "string", "description": "Developer-mode reason automated build/lint/test verification does not apply after changed files; not machine-observed evidence"},
    "validation_note": {"type": "string", "description": "Work-mode disclosure of what was checked and what remains subjective when no meaningful machine validation applies; not runtime proof"}
  },
  "required": ["goal", "steps"],
  "additionalProperties": false
}`)
