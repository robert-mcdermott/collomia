package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
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
	// The status this asks for decides how the whole turn is reported, so it
	// has to name both. A step marked blocked makes the run end blocked, which
	// is right for work that cannot be done and wrong for an action that
	// turned out to be unnecessary or was achieved another way — and telling
	// the model only about `blocked` produced exactly that: finished
	// deliverables reported as failures because an abandoned side attempt was
	// recorded with the only word on offer.
	b.WriteString("Continue with tools. Finish the remaining work and update the plan with evidence; recover safely from a failed tool by retrying or choosing an alternative; or record the relevant plan step with an exact reason — `skipped` when the action proved unnecessary or you achieved it another way, `blocked` only when the work genuinely cannot be completed, since a blocked step ends this turn as blocked. If changed files genuinely have no meaningful automated verification, update the plan with a specific verification_note explaining why. Do not repeat the final answer until the recorded state supports done or blocked. This notice does not grant permission or change the user's requested scope.")
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
	// Unrecognized marks a direct command whose composition was fine and which
	// simply is not in the recognizer's table. It is separate from a refused
	// composition because the correction differs: there is no direct form to
	// suggest, and the honest answer is which verifiers this project actually
	// has. The recognizer is a finite table, so this case will outlive any
	// particular ecosystem being added to it.
	Unrecognized bool
}

func isVerificationCommand(command, workspace string) bool {
	return assessVerificationCommand(command, workspace).Recognized
}

func assessVerificationCommand(command, workspace string) verificationAssessment {
	candidate := stripSafeVerificationStderrMerge(strings.TrimSpace(command))
	if candidate == "" {
		return verificationAssessment{}
	}
	final, refusal := safeVerificationChain(candidate, workspace)
	if refusal == "" {
		if directVerificationCommand(final, workspace) {
			return verificationAssessment{Recognized: true, VerificationLike: true}
		}
		return verificationAssessment{Unrecognized: true}
	}
	// The composition is ineligible. Naming the direct form is the difference
	// between a model that corrects itself on the next call and one that
	// repeats an equivalent command until its progress lease runs out, so the
	// whole command is searched rather than only its leading segment: the
	// verifier can be first (`pytest || true`) or last (`export CACHE=... &&
	// pytest`).
	if suggestion := verificationChainSuggestion(candidate, workspace); suggestion != "" {
		return verificationAssessment{VerificationLike: true, Reason: refusal, Suggestion: suggestion}
	}
	return verificationAssessment{}
}

// safeVerificationChain returns the command whose exit status the shell will
// report, when that is provably the final command's own status, and otherwise
// the reason the composition is refused.
//
// For `A && B` the shell reports B's status when A succeeded and A's non-zero
// status otherwise, so observing zero proves B ran and exited zero. That is
// exactly the property this gate protects, and it holds whatever A is: an
// environment export, a cache mkdir, a virtualenv activation. Every other shell
// form can report zero without the check passing — `||` substitutes a success,
// `;` reports only the last command, a pipeline reports the last stage, and a
// trailing command replaces the status entirely — and stays ineligible.
//
// One refusal is not about exit status at all: a leading segment that moves the
// verifier out of the workspace would bind evidence from a different tree to
// this workspace's state token.
func safeVerificationChain(command, workspace string) (string, string) {
	const masking = "the command contains shell composition or redirection, so the shell's final status may mask the verification command's exit status"
	if unsafeVerificationOperator(command) {
		return "", masking
	}
	segments := strings.Split(command, "&&")
	final := strings.TrimSpace(segments[len(segments)-1])
	if final == "" {
		return "", masking
	}
	// The recognizer decides what a command is from its literal words, so a
	// final segment assembled at runtime cannot be classified at all.
	if strings.Contains(final, "$(") {
		return "", "the verification command is assembled by command substitution, so the runtime cannot tell which check would run"
	}
	for _, segment := range segments[:len(segments)-1] {
		if relocatesVerification(strings.TrimSpace(segment), workspace) {
			return "", "the command changes directory before verifying, so its result would not describe the workspace the evidence is bound to"
		}
	}
	return final, ""
}

// unsafeVerificationOperator reports any shell construct that can decouple the
// reported status from the verifier. `&&` is the sole exception and is handled
// by the caller.
func unsafeVerificationOperator(command string) bool {
	for index := 0; index < len(command); index++ {
		switch command[index] {
		case '\n', ';', '<', '>', '`', '|':
			return true
		case '&':
			if index+1 < len(command) && command[index+1] == '&' {
				index++
				continue
			}
			return true
		}
	}
	return false
}

// relocatesVerification reports whether a leading segment would run the final
// command somewhere other than the workspace. The redundant workspace `cd` that
// run_command already supplies is the one relocation that changes nothing.
func relocatesVerification(segment, workspace string) bool {
	fields := strings.Fields(segment)
	for len(fields) > 0 && shellEnvironmentAssignment(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "pushd", "popd", "chdir":
		return true
	case "cd":
		if len(fields) != 2 {
			return true
		}
		cleanWorkspace := filepath.Clean(strings.TrimSpace(workspace))
		allowed := []string{".", "'.'", `"."`}
		if cleanWorkspace != "." && cleanWorkspace != "" {
			allowed = append(allowed, cleanWorkspace, "'"+cleanWorkspace+"'", `"`+cleanWorkspace+`"`)
		}
		return !slices.Contains(allowed, fields[1])
	}
	return false
}

// verificationChainSuggestion finds the recognized check inside a command the
// gate refused, so the correction can name the exact direct form to run.
func verificationChainSuggestion(command, workspace string) string {
	for _, segment := range splitVerificationSegments(command) {
		if directVerificationCommand(segment, workspace) {
			return strings.Join(strings.Fields(segment), " ")
		}
	}
	return ""
}

func splitVerificationSegments(command string) []string {
	var segments []string
	for _, field := range strings.FieldsFunc(command, func(r rune) bool {
		return r == '\n' || r == ';' || r == '&' || r == '|'
	}) {
		// A redirection ends the command it belongs to. Drop it along with any
		// attached file-descriptor digits (`2>&1`) rather than leaving them as
		// spurious verification arguments.
		if cut := strings.IndexAny(field, "<>"); cut >= 0 {
			for cut > 0 && field[cut-1] >= '0' && field[cut-1] <= '9' {
				cut--
			}
			field = field[:cut]
		}
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			segments = append(segments, trimmed)
		}
	}
	return segments
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
	case "node":
		return nodeVerification(fields)
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

// unrecognizedVerificationNotice explains a passing command the runtime did
// not accept as verification, and says what this project offers instead.
//
// The silence this replaces is the expensive part. A model told only that
// verification is missing, immediately after watching its own test suite pass,
// has nothing to correct: it re-runs the same command, inspects detection,
// and spends its bounded remediation lease diagnosing rather than repairing.
// Naming the refused command and the project's real verifiers turns that into
// one step. The recognizer will always be a finite table, so this has to work
// for an ecosystem nobody has added yet.
func unrecognizedVerificationNotice(command, workspace string) string {
	notice := fmt.Sprintf("Collomia verification evidence was not recorded: %q exited zero, but it is not a recognized verification command, so the runtime cannot bind it to the workspace state as proof. Its output is still a valid tool result.", strings.Join(strings.Fields(command), " "))
	_, detected := tools.DetectVerificationCommands(workspace)
	if len(detected) > 0 {
		commands := make([]string, 0, len(detected))
		for _, candidate := range detected {
			commands = append(commands, strconv.Quote(candidate.Command))
		}
		return notice + " This project's detected verification commands are " + strings.Join(commands, ", ") + "; run one of those directly."
	}
	return notice + " This project has no detected verification commands, because it has no recognized project manifest at its root. If creating one is within this node's scope, add the manifest your ecosystem uses to declare a test entry point (for a plain JavaScript project, a package.json whose scripts.test runs your test file), then run that entry point directly."
}

// nodeVerification recognizes Node's two ordinary check entry points: the
// built-in test runner, and a script in a conventional test location.
//
// Node's absence from the table above was worse than any other language's
// would have been. A directory with no package.json is precisely the "no
// applicable test surface" case in which the proposal contract requires the
// first mutating node to establish a focused test — so the runtime asked for a
// test to be created and then refused to accept the only way to run it. There
// was no exit: the node could not verify, and could not stop needing to.
//
// An inline expression is never recognized. `node -e ...` is arbitrary code
// whose text can be spelled to look like a test path, and unlike a script file
// it is not something a project can be said to have.
func nodeVerification(fields []string) bool {
	script, builtinRunner := "", false
	for _, field := range fields[1:] {
		switch {
		case field == "-e" || field == "--eval" || field == "-p" || field == "--print":
			return false
		// --check parses without executing and exits non-zero on a syntax
		// error, which is the same kind of proof `tsc` and `go vet` give and
		// the natural check for a project with no test surface at all.
		case field == "--test" || strings.HasPrefix(field, "--test=") || field == "--check" || field == "-c":
			builtinRunner = true
		case strings.HasPrefix(field, "-"):
		case script == "":
			script = field
		}
	}
	return builtinRunner || conventionalTestScript(script)
}

// conventionalTestScript reports whether a path is one a project would
// conventionally treat as tests, by directory or by filename. `smoke` is
// included deliberately: it is the word Collomia's own proposal contract uses
// when it asks a node to create a focused test, so it is the name the model is
// most likely to choose in the case this recognition exists to serve.
func conventionalTestScript(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	if path == "" {
		return false
	}
	segments := strings.Split(path, "/")
	for _, segment := range segments[:len(segments)-1] {
		switch segment {
		case "test", "tests", "spec", "specs", "__tests__":
			return true
		}
	}
	name := segments[len(segments)-1]
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		name = name[:dot]
	}
	for _, marker := range []string{"test", "spec", "smoke"} {
		if name == marker ||
			strings.HasPrefix(name, marker+"_") || strings.HasPrefix(name, marker+"-") ||
			strings.HasSuffix(name, "."+marker) || strings.HasSuffix(name, "_"+marker) || strings.HasSuffix(name, "-"+marker) {
			return true
		}
	}
	return false
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
