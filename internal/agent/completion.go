package agent

import (
	"context"
	"crypto/sha256"
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
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

const maxCompletionInterventions = 2

var (
	// ErrGoalGraphComplete prevents a terminal one-shot Orchestrated Goal from
	// becoming an accidental container for later unrelated prompts.
	ErrGoalGraphComplete = errors.New("orchestrated goal is already complete")
	// ErrGoalBlocked means the agent reached a truthful terminal response but
	// could not complete the requested work. Missing verification has its own
	// outcome because an unproven success is not the same thing as blocked work.
	ErrGoalBlocked = errors.New("goal blocked")
	// ErrGoalNeedsVerification means the requested work appears complete, but
	// the runtime could not bind recognized verification (or a specific
	// verification exception) to the latest tracked write state.
	ErrGoalNeedsVerification = errors.New("goal needs verification")
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
	GoalDone              GoalOutcome = "done"
	GoalBlocked           GoalOutcome = "blocked"
	GoalCancelled         GoalOutcome = "cancelled"
	GoalBudgetExhausted   GoalOutcome = "budget_exhausted"
	GoalNeedsVerification GoalOutcome = "needs_verification"
	// GoalPaused is a nonterminal turn boundary used only by the interactive
	// Orchestrated Goal controller. It is not a public run.result outcome.
	GoalPaused GoalOutcome = "paused"
	// GoalAwaitingReview likewise belongs to the interactive controller: the
	// graph produced verified candidates and stopped for reviewed integration.
	GoalAwaitingReview GoalOutcome = "awaiting_review"
)

// GoalOutcomeFor reduces every runtime exit to the goal-level states an
// operator can act on. Unexpected runtime/provider failures are blockers with
// structured failure metadata retained separately by the event contract.
func GoalOutcomeFor(err error) GoalOutcome {
	if providerErr, ok := provider.AsError(err); ok && providerErr.Kind == provider.ErrorCancelled {
		return GoalCancelled
	}
	switch {
	case err == nil:
		return GoalDone
	case errors.Is(err, ErrGoalNeedsVerification):
		return GoalNeedsVerification
	case errors.Is(err, ErrTokenBudgetExceeded), errors.Is(err, ErrCostBudgetExceeded), errors.Is(err, ErrIterationBudgetExceeded), errors.Is(err, ErrAggregateBudgetExceeded):
		return GoalBudgetExhausted
	case errors.Is(err, context.Canceled):
		return GoalCancelled
	default:
		return GoalBlocked
	}
}

type toolObservation struct {
	CallID               string
	RetryKey             string
	LegacyRetryKey       string
	RejectedVerification *tools.CommandVerification
	VerificationPurpose  string
	Name                 string
	Action               tools.Action
	Failed               bool
	FailureKind          goalgraph.FailureKind
	FailureDetail        string
	ResultSummary        string
	Retryable            bool
	Started              time.Time
	Finished             time.Time
	GraphRecorded        bool
	IgnoreGraphFailure   bool
	Verification         bool
	ArtifactValidation   bool
	ArtifactEvidence     *tools.Evidence
	ArtifactPath         string
	ValidationRequest    *tools.ArtifactValidationRequirement
	ScopedFiles          []artifactReceipt
	Effects              toolEffects
	ExecutionPrevented   bool
	CommandExited        bool // native runner observed an ordinary nonzero exit
	VerificationCheck    verificationAssessment
}

type completionController struct {
	store                CompletionStore
	pending              *pendingEffect
	effectStarted        bool
	board                *plan.Board
	workspace            string
	taskMode             taskmode.Mode
	enabled              bool
	initialRevision      uint64
	initialOpen          bool
	interventions        int
	dirty                bool
	dirtyUnknown         bool
	dirtyPaths           map[string]struct{}
	artifacts            artifactTracker
	ctx                  context.Context
	waived               bool
	recognizedEver       bool
	noteAtMutation       string
	failures             []unresolvedToolFailure
	successes            map[string]toolObservation
	successOrder         []string
	resolutionIssues     []string
	nextFailureID        int
	failureIDCounts      map[string]int
	progressVersion      uint64
	seenProgress         map[[sha256.Size]byte]struct{}
	lastRevision         uint64
	bestInterventionGaps int
}

type unresolvedToolFailure struct {
	id                   string
	tool                 string
	risk                 tools.Risk
	detail               string
	planRevision         uint64
	retryKey             string
	validationRequest    *tools.ArtifactValidationRequirement
	repairPaths          []string
	repairReady          bool
	rejectedVerification *tools.CommandVerification
}

type completionDecision struct {
	done              bool
	blocked           bool
	needsVerification bool
	reuseCandidate    bool
	reason            string
	notice            string
}

func newCompletionController(board *plan.Board, workspace string, planning bool, mode taskmode.Mode) *completionController {
	if mode == "" {
		mode = taskmode.Developer
	}
	controller := &completionController{board: board, workspace: workspace, taskMode: mode, enabled: board != nil && !planning, seenProgress: make(map[[sha256.Size]byte]struct{}), dirtyPaths: make(map[string]struct{}), successes: make(map[string]toolObservation), failureIDCounts: make(map[string]int)}
	if !controller.enabled {
		return controller
	}
	current, revision := board.Snapshot()
	controller.initialRevision = revision
	controller.lastRevision = revision
	if current != nil {
		controller.initialOpen = current.AssessCompletion().State == plan.CompletionIncomplete
		if controller.initialOpen {
			controller.syncArtifactBrief(current)
		}
	}
	return controller
}

func (c *completionController) observe(observation toolObservation) {
	if c == nil {
		return
	}
	c.observeProgress(observation)
	if !c.enabled {
		return
	}
	if observation.Name == "update_plan" && !observation.Failed {
		c.syncArtifactBrief(c.board.Current())
	}
	if !observation.ExecutionPrevented {
		if observation.Action.Risk == tools.RiskWrite {
			c.markDirty(observation.Action.Paths)
		}
		if len(observation.Effects.Paths) > 0 {
			c.markDirty(observation.Effects.Paths)
		}
	}
	if observation.Failed {
		// A write tool may fail after making a partial mutation. Conservatively
		// stale verification even when the tool reports failure.
		c.recordFailure(observation)
		return
	}
	if strings.TrimSpace(observation.CallID) != "" {
		if _, exists := c.successes[observation.CallID]; !exists {
			c.successOrder = append(c.successOrder, observation.CallID)
		}
		c.successes[observation.CallID] = observation
	}
	// Exact operations and native artifact checks covering the original
	// requirements recover automatically. Other recovery is never guessed from
	// tool names or permission-risk labels; update_plan binds its receipt IDs.
	c.recoverFailures(observation)
	c.resolveFailuresFromPlan()
	if observation.Name == "run_command" && observation.Verification && c.taskMode != taskmode.Work && len(observation.ScopedFiles) == 0 {
		c.clearDirty()
		c.recognizedEver = true
	}
	if observation.ArtifactValidation {
		c.recordArtifactReceipt(observation)
	}
	if len(observation.ScopedFiles) > 0 {
		c.recordScopedVerification(observation.ScopedFiles)
	}
	c.markFileRepairs(observation)
	if len(observation.ScopedFiles) > 0 || observation.ArtifactValidation {
		c.recoverVerifiedFileFailures()
	}
	if observation.Name == "update_plan" && c.dirty && c.board != nil {
		if current := c.board.Current(); current != nil && completionDisclosure(current, c.taskMode) != "" && completionDisclosure(current, c.taskMode) != c.noteAtMutation {
			c.waived = true
		}
	}
}

// observeProgress records novel, externally visible evidence rather than raw
// provider turns. Repeating the same read result does not buy another lease;
// a successful write always does because its content is intentionally absent
// from toolObservation. The separate hard turn envelope still bounds churn.
func (c *completionController) observeProgress(observation toolObservation) {
	if !observation.ExecutionPrevented && (observation.Action.Risk == tools.RiskWrite || len(observation.Effects.Paths) > 0) {
		c.progressVersion++
		return
	}
	if observation.Name == "update_plan" && c.board != nil {
		_, revision := c.board.Snapshot()
		if revision != c.lastRevision {
			c.lastRevision = revision
			c.progressVersion++
			return
		}
	}
	fingerprint := sha256.Sum256([]byte(strings.Join([]string{
		observation.Name,
		string(observation.Action.Risk),
		observation.Action.Summary,
		observation.Action.Command,
		strings.Join(observation.Action.Paths, "\x00"),
		strconv.FormatBool(observation.Failed),
		observation.FailureDetail,
		observation.ResultSummary,
	}, "\x1f")))
	if _, seen := c.seenProgress[fingerprint]; seen {
		return
	}
	c.seenProgress[fingerprint] = struct{}{}
	c.progressVersion++
}

func (c *completionController) awaitingVerificationGuidance() bool {
	return c != nil && c.enabled && c.dirty && !c.waived && c.interventions > 0
}

func (c *completionController) recordFailure(observation toolObservation) {
	id := strings.TrimSpace(observation.CallID)
	if id == "" {
		c.nextFailureID++
		id = fmt.Sprintf("tool-failure-%d", c.nextFailureID)
	} else {
		c.failureIDCounts[id]++
		if c.failureIDCounts[id] > 1 {
			id = fmt.Sprintf("%s#%d", id, c.failureIDCounts[id])
		}
	}
	var revision uint64
	if c.board != nil {
		_, revision = c.board.Snapshot()
	}
	failure := unresolvedToolFailure{id: id, tool: observation.Name, risk: observation.Action.Risk, detail: strings.TrimSpace(observation.Action.Summary), planRevision: revision, retryKey: observation.RetryKey, validationRequest: observation.ValidationRequest, rejectedVerification: observation.RejectedVerification}
	if observation.FailureKind == goalgraph.FailureTool && !observation.ExecutionPrevented && !observation.Effects.Unknown && len(observation.Effects.Paths) <= 64 {
		switch observation.Name {
		case "write_file", "edit_file", "apply_patch", "format_file":
			for _, path := range observation.Effects.Paths {
				failure.repairPaths = append(failure.repairPaths, completionPath(path))
			}
		}
	}
	for suffix := 2; ; suffix++ {
		collision := false
		for _, existing := range c.failures {
			if existing.id == failure.id {
				collision = true
				break
			}
		}
		if !collision {
			break
		}
		failure.id = fmt.Sprintf("%s#%d", id, suffix)
	}
	c.failures = append(c.failures, failure)
}

func (c *completionController) recoverFailures(observation toolObservation) {
	remaining := c.failures[:0]
	for _, failure := range c.failures {
		if !matchesRetry(failure, observation) && !c.recoversRejectedVerification(failure, observation) {
			remaining = append(remaining, failure)
		}
	}
	c.failures = remaining
}

// resolveFailuresFromPlan validates model-authored dispositions against the
// controller's current-turn receipts. The plan gives semantic intent (this
// alternative really replaced that failed attempt); the successful tool-call
// receipt proves only that the referenced alternative actually ran.
func (c *completionController) resolveFailuresFromPlan() {
	c.resolutionIssues = nil
	if c.board == nil || len(c.failures) == 0 {
		return
	}
	current, revision := c.board.Snapshot()
	if current == nil || len(current.ResolvedFailures) == 0 {
		return
	}
	resolutions := make(map[string]plan.FailureResolution, len(current.ResolvedFailures))
	for _, resolution := range current.ResolvedFailures {
		resolutions[strings.TrimSpace(resolution.FailureID)] = resolution
	}
	remaining := c.failures[:0]
	for _, failure := range c.failures {
		if revision <= failure.planRevision {
			remaining = append(remaining, failure)
			continue
		}
		resolution, ok := resolutions[failure.id]
		if !ok {
			remaining = append(remaining, failure)
			continue
		}
		if issue := c.validateFailureResolution(current, failure, resolution); issue != "" {
			c.resolutionIssues = append(c.resolutionIssues, issue)
			remaining = append(remaining, failure)
		}
	}
	c.failures = remaining
}

func (c *completionController) validateFailureResolution(current *plan.Plan, failure unresolvedToolFailure, resolution plan.FailureResolution) string {
	prefix := fmt.Sprintf("failure %s (%s)", failure.id, failure.tool)
	var step *plan.Step
	for i := range current.Steps {
		if current.Steps[i].ID == resolution.StepID {
			step = &current.Steps[i]
			break
		}
	}
	if step == nil {
		return prefix + fmt.Sprintf(" references unknown plan step %d", resolution.StepID)
	}
	switch resolution.Disposition {
	case "recovered_by_retry", "recovered_by_alternative":
		recoveryID := strings.TrimSpace(resolution.RecoveryToolCallID)
		recovery, ok := c.successes[recoveryID]
		if !ok {
			issue := prefix + " references recovery_tool_call_id " + recoveryID + " without a successful current-turn tool receipt"
			if actualID, actual := c.receiptForOutputMarker(recoveryID); actual {
				issue += "; that value is embedded in the output of successful tool call " + actualID + " and is not its receipt ID"
			}
			return issue
		}
		if completionMetaTool(recovery.Name) {
			return prefix + " cannot use completion metadata tool " + recovery.Name + " as recovery evidence"
		}
		if resolution.Disposition == "recovered_by_retry" && !matchesRetry(failure, recovery) {
			return prefix + " says recovered_by_retry but the successful receipt is not the same operation and arguments; use recovered_by_alternative with evidence if the changed operation replaced it"
		}
		if resolution.Disposition == "recovered_by_alternative" && recoveryID == failure.id {
			return prefix + " says recovered_by_alternative but references the failed call itself"
		}
		if step.Status != "done" && step.Status != "skipped" {
			return prefix + fmt.Sprintf(" recovery step %d is %s rather than done or skipped", step.ID, step.Status)
		}
	case "skipped_unnecessary":
		if step.Status != "skipped" {
			return prefix + fmt.Sprintf(" says skipped_unnecessary but plan step %d is %s", step.ID, step.Status)
		}
	case "blocked":
		if step.Status != "blocked" {
			return prefix + fmt.Sprintf(" says blocked but plan step %d is %s", step.ID, step.Status)
		}
	default:
		return prefix + " has unsupported disposition " + resolution.Disposition
	}
	return ""
}

func completionMetaTool(name string) bool {
	return name == "update_plan" || name == "detect_verification" || name == "update_task_context" || name == "read_task_context" || name == "read_session" || name == "search_session"
}

// receiptForOutputMarker recognizes the exact mistake that opaque external
// data wrappers make easy: copying an identifier printed inside a successful
// result and presenting it as the tool call's receipt. The marker is useful for
// provenance, but the provider envelope's call ID is the runtime receipt. This
// helper only improves the correction; it never accepts the alias as proof.
func (c *completionController) receiptForOutputMarker(candidate string) (string, bool) {
	marker := strings.TrimPrefix(strings.TrimSpace(candidate), "call_")
	if len(marker) < 8 {
		return "", false
	}
	for _, callID := range c.successOrder {
		observation := c.successes[callID]
		if strings.Contains(observation.ResultSummary, marker) {
			return callID, true
		}
	}
	return "", false
}

type recoveryReceipt struct {
	callID  string
	tool    string
	summary string
}

// recoveryReceipts gives the model the provider-envelope IDs it otherwise has
// to recover from protocol metadata while output-local source markers compete
// for its attention. Matching-tool/risk candidates come first, followed by
// recent successful non-metadata calls. The model still decides whether a
// receipt semantically recovered the failure; the runtime does not guess.
func (c *completionController) recoveryReceipts() []recoveryReceipt {
	if c == nil || len(c.failures) == 0 || len(c.successOrder) == 0 {
		return nil
	}
	const maxReceipts = 16
	matching := func(observation toolObservation) bool {
		for _, failure := range c.failures {
			if observation.Name == failure.tool || (failure.risk != "" && observation.Action.Risk == failure.risk) {
				return true
			}
		}
		return false
	}
	selected := make(map[string]struct{}, maxReceipts)
	receipts := make([]recoveryReceipt, 0, maxReceipts)
	appendRecent := func(requireMatch bool) {
		for i := len(c.successOrder) - 1; i >= 0 && len(receipts) < maxReceipts; i-- {
			callID := c.successOrder[i]
			if _, ok := selected[callID]; ok {
				continue
			}
			observation := c.successes[callID]
			if completionMetaTool(observation.Name) || requireMatch != matching(observation) {
				continue
			}
			summary := strings.TrimSpace(observation.Action.Summary)
			if summary == "" {
				summary = strings.TrimSpace(firstLine(observation.ResultSummary))
			}
			receipts = append(receipts, recoveryReceipt{callID: callID, tool: observation.Name, summary: clipUTF8(summary, 180)})
			selected[callID] = struct{}{}
		}
	}
	appendRecent(true)
	appendRecent(false)
	return receipts
}

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}

func (c *completionController) markDirty(paths []string) {
	c.dirty = true
	c.waived = false
	if len(paths) == 0 {
		c.dirtyUnknown = true
	}
	for _, path := range paths {
		if cleaned := completionPath(path); cleaned != "" {
			c.dirtyPaths[cleaned] = struct{}{}
		}
	}
	c.noteAtMutation = ""
	if c.board != nil {
		if current := c.board.Current(); current != nil {
			c.noteAtMutation = completionDisclosure(current, c.taskMode)
		}
	}
}

func (c *completionController) clearDirty() {
	c.dirty = false
	c.dirtyUnknown = false
	clear(c.dirtyPaths)
	c.waived = false
}

func (c *completionController) acceptArtifactValidation(paths []string) {
	for _, path := range paths {
		delete(c.dirtyPaths, completionPath(path))
	}
	c.dirty = c.dirtyUnknown || len(c.dirtyPaths) > 0
	if !c.dirty {
		c.waived = false
	}
}

// outstandingWorkValidationIssue renders only the tracked paths that still
// need evidence. validate_artifact removes a path from dirtyPaths, so naming
// this set prevents the model from repeatedly validating an artifact whose
// current digest the controller has already accepted. Unknown-path mutations
// remain explicit and fail closed rather than disappearing behind the list.
func (c *completionController) outstandingWorkValidationIssue() string {
	const maxPaths = 8
	paths := make([]string, 0, len(c.dirtyPaths))
	workspace := completionPath(c.workspace)
	for path := range c.dirtyPaths {
		display := path
		if relative, err := filepath.Rel(workspace, path); err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			display = filepath.ToSlash(relative)
		}
		paths = append(paths, strconv.Quote(clipUTF8(display, 240)))
	}
	slices.Sort(paths)
	total := len(paths)
	if len(paths) > maxPaths {
		paths = paths[:maxPaths]
	}

	var issue string
	if len(paths) > 0 {
		issue = "changed files not covered by current verification: " + strings.Join(paths, ", ")
		if total > len(paths) {
			issue += fmt.Sprintf(" (and %d more)", total-len(paths))
		}
		issue += ". These are the controller's remaining tracked paths; artifacts with accepted current receipts are omitted"
	}
	if c.dirtyUnknown {
		if issue != "" {
			issue += "; additional changed workspace state has no reported path"
		} else {
			issue = "changed workspace state still needs current task-appropriate validation, but the mutating tool did not report its paths"
		}
	}
	if issue == "" {
		return "one or more changed artifacts have no current task-appropriate validation"
	}
	return issue
}

// completionPath gives mutation and validation receipts one stable identity.
// macOS commonly exposes /var through /private/var, and an artifact written
// through one spelling must not remain dirty after the path guard validates
// the other. Resolve the path itself when it exists; for deletions, resolve
// the parent and retain the final component.
func completionPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	parent, base := filepath.Dir(path), filepath.Base(path)
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolved, base)
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func completionDisclosure(current *plan.Plan, mode taskmode.Mode) string {
	if current == nil {
		return ""
	}
	if mode == taskmode.Work {
		if note := strings.TrimSpace(current.ValidationNote); note != "" {
			return note
		}
	}
	return strings.TrimSpace(current.VerificationNote)
}

func (c *completionController) assess() completionDecision {
	if c == nil || !c.enabled {
		return completionDecision{done: true}
	}
	if c.store != nil && c.artifacts.overflow {
		return completionDecision{blocked: true, reason: "durable completion tracking exceeded its bound; split the work into a new session rather than treating omitted obligations as complete"}
	}
	if c.pending != nil {
		return completionDecision{blocked: true, reason: "uncertain outcome from " + c.pending.Tool + "; inspect current state, then /recovery acknowledge REASON; no automatic replay"}
	}
	var issues []string
	planIssueCount := 0
	current, revision := c.board.Snapshot()
	activePlan := current != nil && (c.initialOpen || revision != c.initialRevision)
	if activePlan {
		assessment := current.AssessCompletion()
		if assessment.State == plan.CompletionBlocked {
			return completionDecision{blocked: true, reason: blockedPlanReason(current)}
		}
		issues = append(issues, assessment.Issues...)
		planIssueCount = len(assessment.Issues)
	}
	// Recheck final bytes even when the last operation was a read, a shell
	// command, a failed write, or an out-of-band edit. Permission risk is not
	// evidence that a previously validated artifact stayed unchanged.
	artifactIssues := c.checkArtifacts(current, activePlan)
	issues = append(issues, artifactIssues...)
	verificationGap := c.dirty && !c.waived
	if verificationGap {
		issues = append(issues, c.outstandingWorkValidationIssue())
	}
	for _, failure := range c.failures {
		detail := failure.id + ": " + failure.tool
		if failure.detail != "" {
			detail += " (" + failure.detail + ")"
		}
		issues = append(issues, "unresolved tool failure "+detail)
	}
	issues = append(issues, c.resolutionIssues...)
	if len(issues) == 0 {
		return completionDecision{done: true}
	}
	// The bounded count resets only when the number of actual completion gaps
	// reaches a new low. Diagnostics about a malformed recovery reference are
	// deliberately excluded: changing one guessed receipt ID into another is
	// not corrective progress, and adding then resolving a new failure cannot
	// buy an unlimited sequence of fresh controller retries.
	gapCount := planIssueCount + len(c.failures) + len(artifactIssues)
	if verificationGap {
		if c.taskMode == taskmode.Work {
			validationGaps := len(c.dirtyPaths)
			if c.dirtyUnknown {
				validationGaps++
			}
			if validationGaps == 0 {
				validationGaps = 1
			}
			gapCount += validationGaps
		} else {
			gapCount++
		}
	}
	if c.interventions > 0 && gapCount < c.bestInterventionGaps {
		c.interventions = 0
	}
	if c.interventions >= maxCompletionInterventions {
		if (verificationGap || len(artifactIssues) > 0) && len(c.failures) == 0 && planIssueCount == 0 {
			return completionDecision{needsVerification: true, reason: "completion still needs verification after two controller interventions: " + strings.Join(issues, "; ")}
		}
		return completionDecision{blocked: true, reason: "completion remained unproven after two controller interventions without corrective progress: " + strings.Join(issues, "; ")}
	}
	c.interventions++
	if c.bestInterventionGaps == 0 || gapCount < c.bestInterventionGaps {
		c.bestInterventionGaps = gapCount
	}
	return completionDecision{
		notice:         completionNotice(issues, c.interventions, c.taskMode, c.recoveryReceipts(), len(c.failures) > 0),
		reuseCandidate: planIssueCount == 0 && !verificationGap && len(artifactIssues) == 0 && len(c.failures) > 0 && len(c.resolutionIssues) == 0,
	}
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

func completionNotice(issues []string, intervention int, mode taskmode.Mode, receipts []recoveryReceipt, failed ...bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Collomia completion controller (intervention %d of %d): this response cannot finish the turn yet.\nRecorded gaps:\n", intervention, maxCompletionInterventions)
	for _, issue := range issues {
		b.WriteString("- " + issue + "\n")
	}
	if len(failed) == 0 || failed[0] {
		b.WriteString("A successful retry of the same tool with the same arguments clears its failure automatically; no plan update is needed solely to record that retry. A different path, command, or other argument is a different operation even when the tool name is unchanged. ")
		b.WriteString("Native artifact validation also recovers a corrected check of the same file when every required text, minimum-size, and format check is preserved or strengthened. Dropping a requirement does not recover its failure. A successful native file edit/replacement followed by current verification of all affected paths also recovers an earlier executed file-edit failure automatically. ")
		if len(receipts) > 0 {
			b.WriteString("Successful current-turn tool receipts available for an explicit recovery:\n")
			for _, receipt := range receipts {
				fmt.Fprintf(&b, "- `%s`: %s", receipt.callID, receipt.tool)
				if receipt.summary != "" {
					b.WriteString(" (" + receipt.summary + ")")
				}
				b.WriteByte('\n')
			}
			b.WriteString("Use the exact tool-call ID shown above only when that call truly recovered the named failure. An ID printed inside tool output (for example, a COLLOMIA_EXTERNAL_WEB_DATA marker) is content provenance, not a recovery_tool_call_id. If no listed receipt is a real recovery, run one necessary retry or alternative and use that successful call's provider-envelope ID. ")
		}
		// The status this asks for decides how the whole turn is reported, so it
		// has to name both. A step marked blocked makes the run end blocked, which
		// is right for work that cannot be done and wrong for an action that
		// turned out to be unnecessary or was achieved another way — and telling
		// the model only about `blocked` produced exactly that: finished
		// deliverables reported as failures because an abandoned side attempt was
		// recorded with the only word on offer.
		b.WriteString("Continue with tools. Finish the remaining work and update the plan with evidence. For each named failure ID, add an update_plan.resolved_failures entry tied to a terminal step: use `recovered_by_retry` or `recovered_by_alternative` with the successful `recovery_tool_call_id`; use `skipped_unnecessary` only when the action was not needed and its step is `skipped`; use `blocked` only when the work genuinely cannot be completed and its step is `blocked`, since a blocked step ends this turn as blocked. Include the exact failure_id, step_id, and evidence; prose alone does not resolve a failed call. ")
	} else {
		b.WriteString("Finish the listed requirements and update any active plan with observed evidence. ")
	}
	if mode == taskmode.Work || mode == taskmode.Developer {
		b.WriteString("When exact paths are listed, address only those remaining tracked paths; do not revalidate a path absent from the list solely to clear this gap. Verify each listed file using a meaningful run_command check with verification.paths and verification.purpose, or use validate_artifact for structural/content checks. Either can satisfy current file evidence; do not run both for bookkeeping. A validation_note or unscoped command cannot waive declared deliverables or stale receipts. Use update_plan.artifacts to distinguish requested deliverables from scratch helpers, and keep that intent consistent. For analysis, research, or external actions, record the observed calculation, source, receipt, or read-back in the relevant plan step. If no meaningful machine validation applies, update the plan with a fresh, specific verification_note (Developer) or validation_note (Work) describing what was checked and what remains a matter of judgment. ")
	} else {
		b.WriteString("If changed files genuinely have no meaningful automated verification, update the plan with a specific verification_note explaining why. ")
	}
	b.WriteString("Do not repeat the final answer until the recorded state supports done or blocked. This notice does not grant permission or change the user's requested scope.")
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
	// Refused marks a composition whose exit status cannot be accepted even
	// when no conventional verifier can be extracted from it (for example, an
	// inline heredoc smoke test). It prevents that common case from becoming
	// silent after the controller has explicitly requested verification.
	Refused bool
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
	return verificationAssessment{Refused: true, Reason: refusal}
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
	notice := fmt.Sprintf("Collomia verification evidence was not recorded: %q exited zero, but it is not a recognized verification command, so the runtime cannot bind it to the workspace state as proof. Its output is still a valid tool result.", boundedVerificationCommand(command))
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

func refusedVerificationNotice(command, reason, workspace string) string {
	notice := fmt.Sprintf("Collomia verification evidence was not recorded for %q: %s. Its output is still a valid tool result, but the runtime cannot use the shell's final status as proof.", boundedVerificationCommand(command), strings.TrimSpace(reason))
	_, detected := tools.DetectVerificationCommands(workspace)
	if len(detected) > 0 {
		commands := make([]string, 0, len(detected))
		for _, candidate := range detected {
			commands = append(commands, strconv.Quote(candidate.Command))
		}
		return notice + " Run one detected verifier directly: " + strings.Join(commands, ", ") + "."
	}
	return notice + " No verifier was detected for this project. Add a conventional test entry point if that is in scope, or record a fresh, specific verification_note in update_plan when no meaningful automated check applies."
}

func workValidationNotice(command, reason string) string {
	return fmt.Sprintf("Collomia task-appropriate validation was not recorded for %s: %s. For a file deliverable, use run_command.verification with its paths and purpose for a task-specific check, or validate_artifact for structural/content checks. For analysis, research, retrieval, or an external action with no meaningful machine validator, update the completed plan step with the observed calculation, source, receipt, or read-back and add a fresh specific validation_note describing what was checked and what remains a matter of judgment.", boundedVerificationCommand(command), reason)
}

func boundedVerificationCommand(command string) string {
	normalized := strings.Join(strings.Fields(command), " ")
	const limit = 240
	if len(normalized) <= limit {
		return normalized
	}
	return clipUTF8(normalized, limit) + "…"
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
