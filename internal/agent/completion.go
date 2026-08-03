package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	// ErrGoalAwaitingReview is the successful stop of a candidate wave. It is a
	// sentinel rather than a failure because nothing went wrong: verified
	// candidates are retained and selecting one is the user's decision.
	ErrGoalAwaitingReview = errors.New("orchestrated goal is awaiting review of retained candidates")
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
	// GoalAwaitingReview likewise belongs to the interactive controller: the
	// graph produced verified candidates and stopped for reviewed integration.
	GoalAwaitingReview GoalOutcome = "awaiting_review"
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
	Name               string
	Action             tools.Action
	Failed             bool
	FailureKind        goalgraph.FailureKind
	FailureDetail      string
	ResultSummary      string
	Retryable          bool
	Started            time.Time
	Finished           time.Time
	GraphRecorded      bool
	IgnoreGraphFailure bool
	Verification       bool
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
type verificationAssessment struct {
	Recognized       bool
	VerificationLike bool
	Reason           string
	Suggestion       string
}

func isVerificationCommand(command, workspace string) bool {
	return assessVerificationCommand(command, workspace).Recognized
}

func assessVerificationCommand(command, workspace string) verificationAssessment {
	candidate := strings.TrimSpace(command)
	candidate = stripSafeVerificationStderrMerge(candidate)
	candidate, _ = stripSafeVerificationWorkspaceCD(candidate, workspace)
	normalized := strings.Join(strings.Fields(candidate), " ")
	if normalized == "" {
		return verificationAssessment{}
	}
	if compoundAt := verificationShellOperator(candidate); compoundAt >= 0 {
		direct := verificationDirectPrefix(candidate, compoundAt)
		if directVerificationCommand(direct, workspace) {
			return verificationAssessment{
				VerificationLike: true,
				Reason:           "the command contains shell composition or redirection, so the shell's final status may mask the verification command's exit status",
				Suggestion:       strings.Join(strings.Fields(direct), " "),
			}
		}
		return verificationAssessment{}
	}
	if directVerificationCommand(normalized, workspace) {
		return verificationAssessment{Recognized: true, VerificationLike: true}
	}
	return verificationAssessment{}
}

// stripSafeVerificationWorkspaceCD removes only the redundant working-
// directory wrapper that run_command itself already supplies. The exact
// workspace (or .) followed by && is safe because a failed cd is non-zero and,
// after a successful cd, the verifier remains the final command whose status
// the shell returns. Every other compound form remains ineligible.
func stripSafeVerificationWorkspaceCD(command, workspace string) (string, bool) {
	operator := strings.Index(command, "&&")
	if operator < 0 {
		return command, false
	}
	prefix := strings.TrimSpace(command[:operator])
	if !strings.HasPrefix(prefix, "cd ") {
		return command, false
	}
	argument := strings.TrimSpace(strings.TrimPrefix(prefix, "cd "))
	cleanWorkspace := filepath.Clean(strings.TrimSpace(workspace))
	allowed := []string{".", "'.'", `"."`}
	if cleanWorkspace != "." && cleanWorkspace != "" {
		allowed = append(allowed, cleanWorkspace, "'"+cleanWorkspace+"'", `"`+cleanWorkspace+`"`)
	}
	if !slices.Contains(allowed, argument) {
		return command, false
	}
	remainder := strings.TrimSpace(command[operator+2:])
	if remainder == "" {
		return command, false
	}
	return remainder, true
}

// A final stderr-to-stdout merge changes presentation, not the command's exit
// status. It is the only shell redirection accepted around verification.
func stripSafeVerificationStderrMerge(command string) string {
	trimmed := strings.TrimSpace(command)
	if strings.HasSuffix(trimmed, " 2>&1") {
		return strings.TrimSpace(strings.TrimSuffix(trimmed, " 2>&1"))
	}
	return trimmed
}

func directVerificationCommand(command, workspace string) bool {
	normalized := strings.Join(strings.Fields(command), " ")
	if normalized == "" {
		return false
	}
	// A command the repository itself declares is authoritative for that
	// repository, whatever ecosystem it belongs to. The table below only has to
	// cover the conventional forms a model reaches for unprompted.
	_, detected := tools.DetectVerificationCommands(workspace)
	for _, candidate := range detected {
		if normalized == strings.Join(strings.Fields(candidate.Command), " ") {
			return true
		}
	}
	return recognizedVerifier(strings.Fields(normalized), 0)
}

// recognizedVerifier reports whether these argv fields invoke a conventional
// build, type, lint, or test check whose exit status is the check's own.
//
// Ecosystem breadth is a safety property here, not a convenience: the graph
// blocks a mutating node that cannot produce recognized verification, so a
// language missing from this table is a language in which Orchestrated Goal
// cannot finish honest work. Environment-manager wrappers are unwrapped
// recursively because they propagate the wrapped program's status unchanged;
// depth is bounded so a wrapper chain cannot smuggle in something else.
func recognizedVerifier(fields []string, depth int) bool {
	if depth > 2 {
		return false
	}
	for len(fields) > 0 && shellEnvironmentAssignment(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}
	// Virtual-environment and platform-specific executable paths preserve the
	// invoked program's exit status just as the bare executable does.
	executable := strings.ReplaceAll(fields[0], "\\", "/")
	if slash := strings.LastIndexByte(executable, '/'); slash >= 0 {
		executable = executable[slash+1:]
	}
	fields = append([]string(nil), fields...)
	fields[0] = strings.TrimSuffix(executable, ".exe")
	if rest, ok := verificationRunnerRemainder(fields); ok {
		return recognizedVerifier(rest, depth+1)
	}
	// Program names are matched case-insensitively (Rscript, R) while arguments
	// keep their case, because an R subcommand is spelled CMD and a shell
	// subcommand is not a program name.
	program := strings.ToLower(fields[0])
	verb := func(index int, allowed ...string) bool {
		if len(fields) <= index {
			return false
		}
		return slices.Contains(allowed, fields[index])
	}
	// A task runner executes an arbitrary named recipe, so only conventional
	// verification target names qualify.
	targets := []string{"test", "tests", "lint", "vet", "check", "build", "typecheck", "verify", "ci"}
	switch program {
	case "go":
		return verb(1, "test", "vet", "build")
	case "cargo":
		return verb(1, "test", "check", "clippy", "build")
	case "python", "python3":
		return verb(1, "-m") && verb(2, "pytest", "mypy", "unittest", "tox", "nox", "pyright", "ruff")
	case "pytest", "mypy", "pyright", "tox", "nox", "phpunit", "rspec", "ctest", "jest", "vitest", "tsc":
		return true
	case "ruff":
		return verb(1, "check") || (verb(1, "format") && slices.Contains(fields[2:], "--check"))
	case "npm", "pnpm", "yarn", "bun":
		if verb(1, "test") {
			return true
		}
		return verb(1, "run") && verb(2, targets...)
	case "deno":
		return verb(1, "test", "check", "lint")
	case "make", "just", "task", "rake":
		return verb(1, targets...)
	case "mix", "swift", "meson":
		return verb(1, "test", "build", "compile")
	case "stack", "cabal", "bazel", "dotnet":
		return verb(1, "test", "build")
	case "composer":
		return verb(1, targets...)
	case "gradle", "gradlew", "mvn", "mvnw":
		return verb(1, "test", "check", "build", "verify")
	case "rscript":
		// The expression is what runs, so recognize only conventional R test
		// entry points; each reports a non-zero status when a test fails.
		joined := strings.ToLower(strings.Join(fields, " "))
		return verb(1, "-e") && (strings.Contains(joined, "testthat::test") || strings.Contains(joined, "devtools::test") || strings.Contains(joined, "tinytest::test"))
	case "r":
		return verb(1, "CMD") && verb(2, "check")
	case "julia":
		return strings.Contains(strings.ToLower(strings.Join(fields, " ")), "pkg.test")
	default:
		return false
	}
}

// verificationRunnerRemainder strips one environment-manager wrapper and
// returns the command it will actually execute. `git diff --check` is
// deliberately absent from every table here: a whitespace linter passes on
// almost any tree, so accepting it would let a mutating node close its
// verification gate without checking the change at all.
func verificationRunnerRemainder(fields []string) ([]string, bool) {
	switch fields[0] {
	case "uv", "poetry", "pipenv", "hatch", "rye", "pdm", "pixi":
		if len(fields) > 2 && fields[1] == "run" {
			return fields[2:], true
		}
		// `hatch test` and `pdm test` are their own conventional entry points.
		if len(fields) == 2 && fields[1] == "test" {
			return []string{"pytest"}, true
		}
		return nil, false
	case "conda", "mamba", "micromamba":
		if len(fields) < 3 || fields[1] != "run" {
			return nil, false
		}
		rest := fields[2:]
		// Skip the environment selector so the wrapped verifier is assessed.
		for len(rest) > 2 && (rest[0] == "-n" || rest[0] == "--name" || rest[0] == "-p" || rest[0] == "--prefix") {
			rest = rest[2:]
		}
		for len(rest) > 1 && strings.HasPrefix(rest[0], "-") {
			rest = rest[1:]
		}
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return nil, false
		}
		return rest, true
	case "bundle":
		if len(fields) > 2 && fields[1] == "exec" {
			return fields[2:], true
		}
		return nil, false
	case "npx", "pnpx", "bunx":
		rest := fields[1:]
		if len(rest) > 1 && (rest[0] == "exec" || rest[0] == "dlx") {
			rest = rest[1:]
		}
		for len(rest) > 1 && strings.HasPrefix(rest[0], "-") {
			rest = rest[1:]
		}
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return nil, false
		}
		return rest, true
	default:
		return nil, false
	}
}

func verificationShellOperator(command string) int {
	index := strings.IndexAny(command, "\n;&|><`")
	if substitution := strings.Index(command, "$("); substitution >= 0 && (index < 0 || substitution < index) {
		index = substitution
	}
	return index
}

func verificationDirectPrefix(command string, operator int) string {
	end := operator
	if operator >= 0 && operator < len(command) && (command[operator] == '>' || command[operator] == '<') {
		// Drop an adjacent numeric file-descriptor prefix as part of the
		// redirection (`2>&1`), not as a spurious verification argument.
		start := operator
		for start > 0 && command[start-1] >= '0' && command[start-1] <= '9' {
			start--
		}
		if start < operator && (start == 0 || command[start-1] == ' ' || command[start-1] == '\t') {
			end = start
		}
	}
	return strings.TrimSpace(command[:end])
}

func shellEnvironmentAssignment(field string) bool {
	equals := strings.IndexByte(field, '=')
	if equals <= 0 {
		return false
	}
	for index, char := range field[:equals] {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_' || (index > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}
