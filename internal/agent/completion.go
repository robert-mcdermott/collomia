package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

const maxCompletionInterventions = 2

var (
	// ErrGoalGraphComplete prevents a terminal one-shot Orchestrated Goal from
	// becoming an accidental container for later unrelated prompts.
	ErrGoalGraphComplete = errors.New("orchestrated goal is already complete")
	// ErrGoalBlocked means the agent reached a truthful terminal response but
	// could not demonstrate completion. The reason names either an explicitly
	// blocked plan step or the evidence the controller could not obtain.
	ErrGoalBlocked = errors.New("goal blocked")
	// ErrIterationBudgetExceeded distinguishes the ordinary iteration ceiling
	// from a model choosing to stop. Token and cost ceilings have their own
	// sentinels in agent.go; all three map to budget_exhausted.
	ErrIterationBudgetExceeded = errors.New("agent iteration budget exhausted")
	// ErrAggregateBudgetExceeded is the whole Orchestrated Goal envelope across
	// proposal, primary, and automatic-worker work.
	ErrAggregateBudgetExceeded = errors.New("orchestrated goal aggregate budget exhausted")
)

type GoalOutcome string

const (
	GoalDone            GoalOutcome = "done"
	GoalBlocked         GoalOutcome = "blocked"
	GoalCancelled       GoalOutcome = "cancelled"
	GoalBudgetExhausted GoalOutcome = "budget_exhausted"
	// GoalPaused is a nonterminal turn boundary used only by the interactive
	// Orchestrated Goal controller. It is not a public run.result outcome.
	GoalPaused GoalOutcome = "paused"
)

// GoalOutcomeFor reduces every runtime exit to the four goal-level states an
// operator can act on. Unexpected runtime/provider failures are blockers with
// structured failure metadata retained separately by the event contract.
func GoalOutcomeFor(err error) GoalOutcome {
	if providerErr, ok := provider.AsError(err); ok && providerErr.Kind == provider.ErrorCancelled {
		return GoalCancelled
	}
	switch {
	case err == nil:
		return GoalDone
	case errors.Is(err, ErrTokenBudgetExceeded), errors.Is(err, ErrCostBudgetExceeded), errors.Is(err, ErrIterationBudgetExceeded), errors.Is(err, ErrAggregateBudgetExceeded):
		return GoalBudgetExhausted
	case errors.Is(err, context.Canceled):
		return GoalCancelled
	default:
		return GoalBlocked
	}
}

type toolObservation struct {
	Name          string
	Action        tools.Action
	Failed        bool
	FailureKind   goalgraph.FailureKind
	FailureDetail string
	ResultSummary string
	Retryable     bool
	Started       time.Time
	Finished      time.Time
	GraphRecorded bool
}

type completionController struct {
	board           *plan.Board
	workspace       string
	enabled         bool
	initialRevision uint64
	initialOpen     bool
	interventions   int
	dirty           bool
	waived          bool
	noteAtMutation  string
	failures        []unresolvedToolFailure
}

type unresolvedToolFailure struct {
	tool   string
	risk   tools.Risk
	detail string
}

type completionDecision struct {
	done    bool
	blocked bool
	reason  string
	notice  string
}

func newCompletionController(board *plan.Board, workspace string, planning bool) *completionController {
	controller := &completionController{board: board, workspace: workspace, enabled: board != nil && !planning}
	if !controller.enabled {
		return controller
	}
	current, revision := board.Snapshot()
	controller.initialRevision = revision
	if current != nil {
		controller.initialOpen = current.AssessCompletion().State == plan.CompletionIncomplete
	}
	return controller
}

func (c *completionController) observe(observation toolObservation) {
	if c == nil || !c.enabled {
		return
	}
	if observation.Failed {
		// A write tool may fail after making a partial mutation. Conservatively
		// stale verification even when the tool reports failure.
		if observation.Action.Risk == tools.RiskWrite {
			c.markDirty()
		}
		c.recordFailure(observation)
		return
	}
	// A same-tool success is a retry. A different successful tool with the
	// same assessed risk is the narrow deterministic proxy for an alternative;
	// an unrelated read must not erase a failed test or write.
	c.recoverFailures(observation)
	if observation.Action.Risk == tools.RiskWrite {
		c.markDirty()
	}
	if observation.Name == "run_command" && isVerificationCommand(observation.Action.Command, c.workspace) {
		c.dirty = false
		c.waived = false
	}
	if observation.Name == "update_plan" && c.dirty && c.board != nil {
		if current := c.board.Current(); current != nil && strings.TrimSpace(current.VerificationNote) != "" && strings.TrimSpace(current.VerificationNote) != c.noteAtMutation {
			c.waived = true
		}
	}
}

func (c *completionController) recordFailure(observation toolObservation) {
	failure := unresolvedToolFailure{tool: observation.Name, risk: observation.Action.Risk, detail: strings.TrimSpace(observation.Action.Summary)}
	for i := range c.failures {
		if c.failures[i].tool == failure.tool {
			c.failures[i] = failure
			return
		}
	}
	c.failures = append(c.failures, failure)
}

func (c *completionController) recoverFailures(observation toolObservation) {
	remaining := c.failures[:0]
	for _, failure := range c.failures {
		sameTool := observation.Name == failure.tool
		comparableAlternative := !completionMetaTool(observation.Name) && !completionMetaTool(failure.tool) && failure.risk != "" && observation.Action.Risk == failure.risk
		if !sameTool && !comparableAlternative {
			remaining = append(remaining, failure)
		}
	}
	c.failures = remaining
}

func completionMetaTool(name string) bool {
	return name == "update_plan" || name == "detect_verification"
}

func (c *completionController) markDirty() {
	c.dirty = true
	c.waived = false
	c.noteAtMutation = ""
	if c.board != nil {
		if current := c.board.Current(); current != nil {
			c.noteAtMutation = strings.TrimSpace(current.VerificationNote)
		}
	}
}

func (c *completionController) assess() completionDecision {
	if c == nil || !c.enabled {
		return completionDecision{done: true}
	}
	var issues []string
	current, revision := c.board.Snapshot()
	activePlan := current != nil && (c.initialOpen || revision != c.initialRevision)
	if activePlan {
		assessment := current.AssessCompletion()
		if assessment.State == plan.CompletionBlocked {
			return completionDecision{blocked: true, reason: blockedPlanReason(current)}
		}
		issues = append(issues, assessment.Issues...)
	}
	if c.dirty && !c.waived {
		issues = append(issues, "files changed after the last successful recognized verification command")
	}
	for _, failure := range c.failures {
		detail := failure.tool
		if failure.detail != "" {
			detail += " (" + failure.detail + ")"
		}
		issues = append(issues, "a failed tool has not been recovered or recorded as blocked: "+detail)
	}
	if len(issues) == 0 {
		return completionDecision{done: true}
	}
	if c.interventions >= maxCompletionInterventions {
		return completionDecision{blocked: true, reason: "completion remained unproven after two controller interventions: " + strings.Join(issues, "; ")}
	}
	c.interventions++
	return completionDecision{notice: completionNotice(issues, c.interventions)}
}

func blockedPlanReason(current *plan.Plan) string {
	var reasons []string
	for _, step := range current.Steps {
		if step.Status == "blocked" {
			reasons = append(reasons, fmt.Sprintf("step %d (%s): %s", step.ID, step.Title, strings.TrimSpace(step.Evidence)))
		}
	}
	if len(reasons) == 0 {
		return "the active plan is blocked"
	}
	return "the active plan is blocked — " + strings.Join(reasons, "; ")
}

func completionNotice(issues []string, intervention int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Collomia completion controller (intervention %d of %d): this response cannot finish the turn yet.\nRecorded gaps:\n", intervention, maxCompletionInterventions)
	for _, issue := range issues {
		b.WriteString("- " + issue + "\n")
	}
	b.WriteString("Continue with tools. Finish the remaining work and update the plan with evidence; recover safely from a failed tool by retrying or choosing an alternative; or update the relevant plan step to blocked with the exact reason. If changed files genuinely have no meaningful automated verification, update the plan with a specific verification_note explaining why. Do not repeat the final answer until the recorded state supports done or blocked. This notice does not grant permission or change the user's requested scope.")
	return b.String()
}

// isVerificationCommand deliberately recognizes only direct, conventional
// checks. Shell compounds and redirections are excluded: `tests || true` must
// never become machine-observed passing evidence merely because the shell
// returned zero.
func isVerificationCommand(command, workspace string) bool {
	normalized := strings.Join(strings.Fields(command), " ")
	if normalized == "" {
		return false
	}
	_, detected := tools.DetectVerificationCommands(workspace)
	for _, candidate := range detected {
		if normalized == strings.Join(strings.Fields(candidate.Command), " ") {
			return true
		}
	}
	if strings.ContainsAny(command, "\n;&|><`") || strings.Contains(command, "$(") {
		return false
	}
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return false
	}
	verb := func(index int, allowed ...string) bool {
		if len(fields) <= index {
			return false
		}
		for _, value := range allowed {
			if fields[index] == value {
				return true
			}
		}
		return false
	}
	switch fields[0] {
	case "go":
		return verb(1, "test", "vet", "build")
	case "cargo":
		return verb(1, "test", "check", "clippy", "build")
	case "pytest", "mypy":
		return true
	case "ruff":
		return verb(1, "check") || (verb(1, "format") && slices.Contains(fields[2:], "--check"))
	case "uv":
		if !verb(1, "run") {
			return false
		}
		if verb(2, "pytest", "mypy") {
			return true
		}
		return verb(2, "ruff") && (verb(3, "check") || (verb(3, "format") && slices.Contains(fields[4:], "--check")))
	case "npm", "pnpm", "yarn", "bun":
		if verb(1, "test") {
			return true
		}
		return verb(1, "run") && verb(2, "test", "lint", "build", "check", "typecheck")
	case "make":
		return verb(1, "test", "lint", "vet", "build", "check")
	case "git":
		return verb(1, "diff") && verb(2, "--check")
	case "dotnet":
		return verb(1, "test", "build")
	case "gradle", "./gradlew", "mvn", "mvnw", "./mvnw":
		return verb(1, "test", "check", "build", "verify")
	default:
		return false
	}
}
