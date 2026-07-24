package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/failureid"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

const (
	DelegateQueued          = "queued"
	DelegateRunning         = "running"
	DelegateWaitingApproval = "waiting_approval"
	DelegateCancelling      = "cancelling"
	DelegateDone            = "done"
	DelegateError           = "error"
	DelegateCancelled       = "cancelled"
	DelegateTimedOut        = "timed_out"
	DelegateBudgetExhausted = "budget_exhausted"
	DelegateInterrupted     = "interrupted"
	maxDelegateEvidence     = 8
	maxDelegateVerification = 16
	maxDelegateChangedFiles = 256
	maxDelegateGuidance     = 8
	maxDelegateRecentOutput = 4 << 10
)

// DelegateVerification is one machine-observed command result from the
// retained child worktree. StateToken binds it to the exact delegated source
// state; it is evidence, never authorization to publish changes.
type DelegateVerification struct {
	Purpose    string    `json:"purpose,omitempty"`
	Command    string    `json:"command"`
	Status     string    `json:"status"`
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
	StateToken string    `json:"state_token,omitempty"`
	Started    time.Time `json:"started,omitempty"`
	Finished   time.Time `json:"finished,omitempty"`
}

// DelegateStatus is a bounded, durable snapshot of one delegated task. It is
// safe to put in the parent session; it contains results and evidence, not a
// raw child transcript.
type DelegateStatus struct {
	ID              string
	Name            string
	Task            string
	Profile         string
	Provider        string
	Model           string
	Write           bool
	PlanStep        int
	Status          string
	CurrentAction   string
	RecentOutput    string
	Guidance        []string
	PendingGuidance int
	Summary         string
	Error           string
	FailureID       string
	Evidence        []string
	Changed         []string
	Worktree        string
	Branch          string
	BaseCommit      string
	Integrated      []string
	// IntegrationStatus records the parent-side disposition of retained write
	// work without changing the child's terminal execution status.
	IntegrationStatus string
	IntegrationError  string
	// VerificationStatus summarizes machine-observed commands run in the
	// retained child worktree. It is intentionally separate from integration:
	// a passing command grants no publication permission and does not prove
	// the combined parent workspace.
	VerificationStatus   string
	VerificationError    string
	VerificationToken    string
	VerificationRequired []string
	VerificationResults  []DelegateVerification
	Usage                provider.Usage
	TokenBudget          int
	TimeoutSeconds       int
	// Revision makes concurrent durable observer writes order-independent.
	// It is monotonic for one task and has no model-visible meaning.
	Revision uint64
	Started  time.Time
	Finished time.Time

	cancel          context.CancelFunc
	pendingGuidance []string
}

// DelegateStart describes a task before it enters the shared scheduler.
type DelegateStart struct {
	ID, Name, Task, Profile, Provider, Model string
	Write                                    bool
	PlanStep                                 int
	TokenBudget, TimeoutSeconds              int
	Cancel                                   context.CancelFunc
}

// Team tracks queued, active, and completed delegated tasks. The observer is
// called outside the lock on meaningful state changes so sessions can persist
// lifecycle snapshots without deadlocking the UI or scheduler.
type Team struct {
	mu       sync.RWMutex
	items    map[string]*DelegateStatus
	order    []string
	observer func(DelegateStatus)
}

func NewTeam() *Team { return &Team{items: map[string]*DelegateStatus{}} }

func (t *Team) SetObserver(observer func(DelegateStatus)) {
	t.mu.Lock()
	t.observer = observer
	t.mu.Unlock()
}

// Enqueue registers a task before it waits for scheduler admission.
func (t *Team) Enqueue(start DelegateStart) {
	status := DelegateStatus{
		ID: start.ID, Name: start.Name, Task: start.Task, Profile: start.Profile,
		Provider: start.Provider, Model: start.Model, Write: start.Write, PlanStep: start.PlanStep,
		Status: DelegateQueued, CurrentAction: "waiting for scheduler",
		TokenBudget: start.TokenBudget, TimeoutSeconds: start.TimeoutSeconds,
		Revision: 1, Started: time.Now(), cancel: start.Cancel,
	}
	boundDelegateStatus(&status)
	t.mu.Lock()
	t.items[status.ID] = &status
	t.order = append(t.order, status.ID)
	t.mu.Unlock()
	t.notify(status)
}

// Start retains the original test/helper API and immediately marks a simple
// task running. Production delegation uses Enqueue followed by MarkRunning.
func (t *Team) Start(id, name, task string, write bool) {
	t.Enqueue(DelegateStart{ID: id, Name: name, Task: task, Write: write})
	t.MarkRunning(id)
}

func (t *Team) MarkRunning(id string) {
	t.update(id, func(s *DelegateStatus) {
		if s.Status != DelegateQueued {
			return
		}
		s.Status = DelegateRunning
		s.CurrentAction = "working"
	})
}

func (t *Team) SetAction(id, action string) {
	t.update(id, func(s *DelegateStatus) {
		if !terminalDelegateStatus(s.Status) && s.Status != DelegateCancelling {
			s.Status = DelegateRunning
			s.CurrentAction = action
		}
	})
}

func (t *Team) SetWaitingApproval(id, action string) {
	t.update(id, func(s *DelegateStatus) {
		if !terminalDelegateStatus(s.Status) && s.Status != DelegateCancelling {
			s.Status = DelegateWaitingApproval
			s.CurrentAction = action
		}
	})
}

func (t *Team) SetUsage(id string, usage provider.Usage) {
	t.update(id, func(s *DelegateStatus) { s.Usage = usage })
}

// AppendOutput retains a small, control-safe tail for live inspection. It is
// intentionally not observer-notified for every streaming token; the next
// lifecycle update (and the terminal result) persists the latest bounded tail.
func (t *Team) AppendOutput(id, chunk string) {
	if chunk == "" {
		return
	}
	t.mu.Lock()
	status := t.items[id]
	if status != nil && !terminalDelegateStatus(status.Status) {
		status.RecentOutput = tailDelegateText(status.RecentOutput+chunk, maxDelegateRecentOutput)
		status.Revision++
	}
	t.mu.Unlock()
}

// Steer queues bounded parent guidance for delivery at the child's next
// provider boundary. It does not interrupt an executing tool, answer an
// approval, or otherwise change the child's permission state.
func (t *Team) Steer(idOrName, guidance string) error {
	guidance = strings.TrimSpace(boundedDelegateText(guidance, 1024))
	if guidance == "" {
		return errors.New("steering guidance is empty")
	}
	t.mu.Lock()
	selected, err := t.findActiveLocked(idOrName)
	if err != nil {
		t.mu.Unlock()
		return err
	}
	if selected.Status == DelegateCancelling {
		t.mu.Unlock()
		return fmt.Errorf("delegated agent %q is cancelling", idOrName)
	}
	if len(selected.Guidance) >= maxDelegateGuidance {
		t.mu.Unlock()
		return fmt.Errorf("delegated agent %q already has the maximum %d steering updates", idOrName, maxDelegateGuidance)
	}
	selected.Guidance = append(selected.Guidance, guidance)
	selected.pendingGuidance = append(selected.pendingGuidance, guidance)
	selected.PendingGuidance = len(selected.pendingGuidance)
	selected.Revision++
	boundDelegateStatus(selected)
	snapshot := copyDelegateStatus(*selected)
	observer := t.observer
	t.mu.Unlock()
	if observer != nil {
		observer(snapshot)
	}
	return nil
}

// TakeSteering drains guidance exactly once. The child calls it immediately
// before constructing a provider request, which is the only safe point to
// alter its conversational instructions.
func (t *Team) TakeSteering(id string) []string {
	t.mu.Lock()
	status := t.items[id]
	if status == nil || len(status.pendingGuidance) == 0 || terminalDelegateStatus(status.Status) {
		t.mu.Unlock()
		return nil
	}
	out := append([]string(nil), status.pendingGuidance...)
	status.pendingGuidance = nil
	status.PendingGuidance = 0
	status.Revision++
	boundDelegateStatus(status)
	snapshot := copyDelegateStatus(*status)
	observer := t.observer
	t.mu.Unlock()
	if observer != nil {
		observer(snapshot)
	}
	return out
}

// Finish retains the original API. New code uses FinishDetailed so usage and
// evidence survive in the durable parent inbox.
func (t *Team) Finish(id, summary string, changed []string, worktree, branch string, err error) {
	t.FinishDetailed(id, summary, nil, changed, worktree, branch, "", provider.Usage{}, err)
}

func (t *Team) FinishDetailed(id, summary string, evidence, changed []string, worktree, branch, baseCommit string, usage provider.Usage, err error) {
	t.update(id, func(s *DelegateStatus) {
		s.Finished = time.Now()
		s.Summary = summary
		s.Evidence = append([]string(nil), evidence...)
		s.Changed = append([]string(nil), changed...)
		s.Worktree = worktree
		s.Branch = branch
		s.BaseCommit = baseCommit
		s.Usage = usage
		s.CurrentAction = ""
		s.cancel = nil
		s.Error = ""
		s.FailureID = ""
		s.Status = DelegateDone
		if err == nil {
			return
		}
		err = failureid.Ensure(err)
		s.Error = err.Error()
		s.FailureID = failureid.ID(err)
		switch {
		case errors.Is(err, ErrTokenBudgetExceeded):
			s.Status = DelegateBudgetExhausted
		case errors.Is(err, context.Canceled):
			s.Status = DelegateCancelled
		case errors.Is(err, context.DeadlineExceeded):
			s.Status = DelegateTimedOut
		default:
			s.Status = DelegateError
		}
	})
}

// Stop cancels one queued or active task. IDs are authoritative; an exact
// unique name is accepted for TUI convenience.
func (t *Team) Stop(idOrName string) error {
	t.mu.Lock()
	selected, err := t.findActiveLocked(idOrName)
	if err != nil {
		t.mu.Unlock()
		return err
	}
	if selected.Status == DelegateCancelling {
		t.mu.Unlock()
		return fmt.Errorf("delegated agent %q is already cancelling", idOrName)
	}
	selected.Status = DelegateCancelling
	selected.CurrentAction = "cancellation requested"
	selected.Revision++
	boundDelegateStatus(selected)
	cancel := selected.cancel
	snapshot := copyDelegateStatus(*selected)
	observer := t.observer
	t.mu.Unlock()
	if observer != nil {
		observer(snapshot)
	}
	if cancel != nil {
		cancel()
	}
	return nil
}

func (t *Team) findActiveLocked(idOrName string) (*DelegateStatus, error) {
	if item := t.items[idOrName]; item != nil {
		if terminalDelegateStatus(item.Status) {
			return nil, fmt.Errorf("delegated agent %q already finished (%s)", idOrName, item.Status)
		}
		return item, nil
	}
	var selected *DelegateStatus
	for _, id := range t.order {
		item := t.items[id]
		if item.Name != idOrName || terminalDelegateStatus(item.Status) {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("more than one active delegated agent is named %q; use its id", idOrName)
		}
		selected = item
	}
	if selected == nil {
		return nil, fmt.Errorf("unknown active delegated agent %q", idOrName)
	}
	return selected, nil
}

// MarkIntegrationReview records that the primary agent or user inspected the
// current bounded diff. It is observational and grants no write permission.
func (t *Team) MarkIntegrationReview(id, status, message string) {
	t.update(id, func(s *DelegateStatus) {
		s.IntegrationStatus = status
		s.IntegrationError = message
	})
}

// MarkIntegrationOutcome records files explicitly copied into the parent
// workspace and the guarded integration disposition. The child worktree
// remains available and is never removed automatically.
func (t *Team) MarkIntegrationOutcome(id, status string, paths []string, message string) {
	t.update(id, func(s *DelegateStatus) {
		s.Integrated = additiveValues(s.Integrated, paths)
		s.IntegrationStatus = status
		s.IntegrationError = message
	})
}

// MarkIntegrated retains the original helper API for callers that only need
// to report a successful integration.
func (t *Team) MarkIntegrated(id string, paths []string) {
	t.MarkIntegrationOutcome(id, "integrated", paths, "")
}

// MarkVerificationRunning records the exact suite and child-state token before
// one command begins. Starting from a new state invalidates older results.
func (t *Team) MarkVerificationRunning(id, token string, required []string, command string) {
	t.update(id, func(s *DelegateStatus) {
		if s.VerificationToken != "" && s.VerificationToken != token {
			s.VerificationResults = nil
		}
		s.VerificationToken = token
		s.VerificationRequired = append([]string(nil), required...)
		s.VerificationStatus = "running"
		s.VerificationError = ""
		for i := range s.VerificationResults {
			if s.VerificationResults[i].Command == command {
				s.VerificationResults = append(s.VerificationResults[:i], s.VerificationResults[i+1:]...)
				break
			}
		}
	})
}

// MarkVerificationResult replaces the observation for one command and derives
// the aggregate suite status. Every required command must pass against the
// same state token before the result is called passed.
func (t *Team) MarkVerificationResult(id, token string, required []string, result DelegateVerification) {
	t.update(id, func(s *DelegateStatus) {
		if s.VerificationToken != "" && s.VerificationToken != token {
			s.VerificationResults = nil
		}
		s.VerificationToken = token
		s.VerificationRequired = append([]string(nil), required...)
		replaced := false
		for i := range s.VerificationResults {
			if s.VerificationResults[i].Command == result.Command {
				s.VerificationResults[i] = result
				replaced = true
				break
			}
		}
		if !replaced {
			s.VerificationResults = append(s.VerificationResults, result)
		}
		s.VerificationStatus, s.VerificationError = verificationAggregate(s.VerificationRequired, s.VerificationResults)
	})
}

// MarkVerificationStale keeps the prior evidence visible but makes clear that
// it no longer describes the current child bytes.
func (t *Team) MarkVerificationStale(id, message string) {
	t.update(id, func(s *DelegateStatus) {
		if len(s.VerificationResults) == 0 && s.VerificationToken == "" {
			return
		}
		s.VerificationStatus = "stale"
		s.VerificationError = message
	})
}

// MarkVerificationUnavailable records that Collomia could not derive a
// standard suite from the retained worktree. It does not invent a command.
func (t *Team) MarkVerificationUnavailable(id, message string) {
	t.update(id, func(s *DelegateStatus) {
		s.VerificationStatus = "unavailable"
		s.VerificationError = message
	})
}

// StopAll requests cancellation for every queued or running task. It is used
// during runtime shutdown so delegated work cannot outlive Collomia. Snapshot
// first because Stop notifies observers and invokes cancellation functions.
func (t *Team) StopAll() {
	for _, status := range t.Snapshot() {
		if terminalDelegateStatus(status.Status) {
			continue
		}
		_ = t.Stop(status.ID)
	}
}

// Restore replaces the process-local view from durable session snapshots.
// Any non-terminal state is marked interrupted; it is never re-enqueued.
func (t *Team) Restore(statuses []DelegateStatus) {
	t.mu.Lock()
	t.items = make(map[string]*DelegateStatus, len(statuses))
	t.order = nil
	for _, status := range statuses {
		status = copyDelegateStatus(status)
		status.cancel = nil
		status.pendingGuidance = nil
		status.PendingGuidance = 0
		if !terminalDelegateStatus(status.Status) {
			status.Status = DelegateInterrupted
			status.CurrentAction = ""
			err := failureid.Ensure(errors.New("Collomia exited before this delegated task recorded a terminal result; it was not restarted"))
			status.Error = err.Error()
			status.FailureID = failureid.ID(err)
			status.Revision++
			if status.Finished.IsZero() {
				status.Finished = time.Now()
			}
		}
		boundDelegateStatus(&status)
		t.items[status.ID] = &status
		t.order = append(t.order, status.ID)
	}
	t.mu.Unlock()
}

func (t *Team) Reset() { t.Restore(nil) }

func (t *Team) Snapshot() []DelegateStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]DelegateStatus, 0, len(t.order))
	for _, id := range t.order {
		out = append(out, copyDelegateStatus(*t.items[id]))
	}
	return out
}

func (t *Team) Get(id string) (DelegateStatus, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	status := t.items[id]
	if status == nil {
		return DelegateStatus{}, false
	}
	return copyDelegateStatus(*status), true
}

func (t *Team) Active() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := 0
	for _, id := range t.order {
		if !terminalDelegateStatus(t.items[id].Status) {
			n++
		}
	}
	return n
}

func (t *Team) update(id string, mutate func(*DelegateStatus)) {
	t.mu.Lock()
	status := t.items[id]
	if status == nil {
		t.mu.Unlock()
		return
	}
	mutate(status)
	status.Revision++
	boundDelegateStatus(status)
	snapshot := copyDelegateStatus(*status)
	observer := t.observer
	t.mu.Unlock()
	if observer != nil {
		observer(snapshot)
	}
}

func (t *Team) notify(status DelegateStatus) {
	t.mu.RLock()
	observer := t.observer
	t.mu.RUnlock()
	if observer != nil {
		observer(copyDelegateStatus(status))
	}
}

func copyDelegateStatus(status DelegateStatus) DelegateStatus {
	status.Evidence = append([]string(nil), status.Evidence...)
	status.Changed = append([]string(nil), status.Changed...)
	status.Guidance = append([]string(nil), status.Guidance...)
	status.Integrated = append([]string(nil), status.Integrated...)
	status.VerificationRequired = append([]string(nil), status.VerificationRequired...)
	status.VerificationResults = append([]DelegateVerification(nil), status.VerificationResults...)
	status.pendingGuidance = append([]string(nil), status.pendingGuidance...)
	status.cancel = nil
	return status
}

func boundDelegateStatus(status *DelegateStatus) {
	status.ID = boundedDelegateText(status.ID, 512)
	status.Name = boundedDelegateText(status.Name, 512)
	status.Task = boundedDelegateText(status.Task, 16<<10)
	status.Profile = boundedDelegateText(status.Profile, 512)
	status.Provider = boundedDelegateText(status.Provider, 512)
	status.Model = boundedDelegateText(status.Model, 1024)
	status.CurrentAction = boundedDelegateText(status.CurrentAction, 2<<10)
	status.RecentOutput = tailDelegateText(status.RecentOutput, maxDelegateRecentOutput)
	status.Summary = boundedDelegateText(status.Summary, 16<<10)
	status.Error = boundedDelegateText(status.Error, 4<<10)
	status.IntegrationStatus = boundedDelegateText(status.IntegrationStatus, 64)
	status.IntegrationError = boundedDelegateText(status.IntegrationError, 4<<10)
	status.VerificationStatus = boundedDelegateText(status.VerificationStatus, 64)
	status.VerificationError = boundedDelegateText(status.VerificationError, 4<<10)
	status.VerificationToken = boundedDelegateText(status.VerificationToken, 128)
	status.Worktree = boundedDelegateText(status.Worktree, 4<<10)
	status.Branch = boundedDelegateText(status.Branch, 1024)
	status.BaseCommit = boundedDelegateText(status.BaseCommit, 128)
	if len(status.Guidance) > maxDelegateGuidance {
		status.Guidance = status.Guidance[:maxDelegateGuidance]
	}
	for i := range status.Guidance {
		status.Guidance[i] = boundedDelegateText(status.Guidance[i], 1024)
	}
	if len(status.Integrated) > maxDelegateChangedFiles {
		status.Integrated = status.Integrated[:maxDelegateChangedFiles]
	}
	for i := range status.Integrated {
		status.Integrated[i] = boundedDelegateText(status.Integrated[i], 1024)
	}
	if len(status.VerificationRequired) > maxDelegateVerification {
		status.VerificationRequired = status.VerificationRequired[:maxDelegateVerification]
	}
	for i := range status.VerificationRequired {
		status.VerificationRequired[i] = boundedDelegateText(status.VerificationRequired[i], 2<<10)
	}
	if len(status.VerificationResults) > maxDelegateVerification {
		status.VerificationResults = status.VerificationResults[:maxDelegateVerification]
	}
	for i := range status.VerificationResults {
		result := &status.VerificationResults[i]
		result.Purpose = boundedDelegateText(result.Purpose, 128)
		result.Command = boundedDelegateText(result.Command, 2<<10)
		result.Status = boundedDelegateText(result.Status, 64)
		result.Output = tailDelegateText(result.Output, 4<<10)
		result.Error = boundedDelegateText(result.Error, 2<<10)
		result.StateToken = boundedDelegateText(result.StateToken, 128)
	}
	if len(status.Evidence) > maxDelegateEvidence {
		status.Evidence = status.Evidence[:maxDelegateEvidence]
	}
	for i := range status.Evidence {
		status.Evidence[i] = boundedDelegateText(status.Evidence[i], 1024)
	}
	if len(status.Changed) > maxDelegateChangedFiles {
		status.Changed = status.Changed[:maxDelegateChangedFiles]
	}
	for i := range status.Changed {
		status.Changed[i] = boundedDelegateText(status.Changed[i], 1024)
	}
}

func verificationAggregate(required []string, results []DelegateVerification) (string, string) {
	observed := make(map[string]DelegateVerification, len(results))
	for _, result := range results {
		observed[result.Command] = result
		switch result.Status {
		case "failed", "cancelled", "timed_out", "blocked", "rejected", "stale":
			message := result.Error
			if message == "" {
				message = result.Command + " " + result.Status
			}
			return result.Status, message
		}
	}
	if len(required) == 0 {
		return "unavailable", "no standard verification commands were detected"
	}
	for _, command := range required {
		result, ok := observed[command]
		if !ok || result.Status != "passed" {
			return "partial", "not every detected verification command has passed"
		}
	}
	return "passed", ""
}

func tailDelegateText(value string, limit int) string {
	value = boundedDelegateText(value, len(value))
	if len(value) <= limit {
		return value
	}
	value = value[len(value)-limit:]
	for len(value) > 0 && value[0]&0xc0 == 0x80 {
		value = value[1:]
	}
	return "…" + value
}

func terminalDelegateStatus(status string) bool {
	switch status {
	case DelegateDone, DelegateError, DelegateCancelled, DelegateTimedOut, DelegateBudgetExhausted, DelegateInterrupted:
		return true
	default:
		return false
	}
}

// Event converts process-local state to the stable durable event payload.
func (status DelegateStatus) Event() event.DelegateStatus {
	return event.DelegateStatus{
		ID: status.ID, Name: status.Name, Task: status.Task, Profile: status.Profile,
		Provider: status.Provider, Model: status.Model, Write: status.Write, PlanStep: status.PlanStep,
		Status: status.Status, CurrentAction: status.CurrentAction,
		RecentOutput: status.RecentOutput, Guidance: append([]string(nil), status.Guidance...), PendingGuidance: status.PendingGuidance,
		Summary: status.Summary, Error: status.Error,
		FailureID: status.FailureID,
		Evidence:  append([]string(nil), status.Evidence...), ChangedFiles: append([]string(nil), status.Changed...),
		Worktree: status.Worktree, Branch: status.Branch, BaseCommit: status.BaseCommit, IntegratedFiles: append([]string(nil), status.Integrated...),
		IntegrationStatus: status.IntegrationStatus, IntegrationError: status.IntegrationError,
		VerificationStatus: status.VerificationStatus, VerificationError: status.VerificationError, VerificationToken: status.VerificationToken,
		VerificationRequired: append([]string(nil), status.VerificationRequired...), VerificationResults: verificationEvents(status.VerificationResults),
		Usage:       event.Usage{InputTokens: status.Usage.InputTokens, OutputTokens: status.Usage.OutputTokens, CachedTokens: status.Usage.CachedTokens, ReasoningTokens: status.Usage.ReasoningTokens},
		TokenBudget: status.TokenBudget, TimeoutSeconds: status.TimeoutSeconds, Revision: status.Revision,
		Started: status.Started, Finished: status.Finished,
	}
}

// DelegateStatusFromEvent restores inert durable state. Cancel functions are
// deliberately absent, so stored work cannot be restarted or controlled as a
// live process.
func DelegateStatusFromEvent(status event.DelegateStatus) DelegateStatus {
	return DelegateStatus{
		ID: status.ID, Name: status.Name, Task: status.Task, Profile: status.Profile,
		Provider: status.Provider, Model: status.Model, Write: status.Write, PlanStep: status.PlanStep,
		Status: status.Status, CurrentAction: status.CurrentAction,
		RecentOutput: status.RecentOutput, Guidance: append([]string(nil), status.Guidance...), PendingGuidance: status.PendingGuidance,
		Summary: status.Summary, Error: status.Error, FailureID: status.FailureID,
		Evidence: append([]string(nil), status.Evidence...), Changed: append([]string(nil), status.ChangedFiles...),
		Worktree: status.Worktree, Branch: status.Branch, BaseCommit: status.BaseCommit, Integrated: append([]string(nil), status.IntegratedFiles...),
		IntegrationStatus: status.IntegrationStatus, IntegrationError: status.IntegrationError,
		VerificationStatus: status.VerificationStatus, VerificationError: status.VerificationError, VerificationToken: status.VerificationToken,
		VerificationRequired: append([]string(nil), status.VerificationRequired...), VerificationResults: verificationStatuses(status.VerificationResults),
		Usage:       provider.Usage{InputTokens: status.Usage.InputTokens, OutputTokens: status.Usage.OutputTokens, CachedTokens: status.Usage.CachedTokens, ReasoningTokens: status.Usage.ReasoningTokens},
		TokenBudget: status.TokenBudget, TimeoutSeconds: status.TimeoutSeconds, Revision: status.Revision,
		Started: status.Started, Finished: status.Finished,
	}
}

func verificationEvents(results []DelegateVerification) []event.DelegateVerification {
	out := make([]event.DelegateVerification, len(results))
	for i, result := range results {
		out[i] = event.DelegateVerification{
			Purpose: result.Purpose, Command: result.Command, Status: result.Status,
			Output: result.Output, Error: result.Error, StateToken: result.StateToken,
			Started: result.Started, Finished: result.Finished,
		}
	}
	return out
}

func verificationStatuses(results []event.DelegateVerification) []DelegateVerification {
	out := make([]DelegateVerification, len(results))
	for i, result := range results {
		out[i] = DelegateVerification{
			Purpose: result.Purpose, Command: result.Command, Status: result.Status,
			Output: result.Output, Error: result.Error, StateToken: result.StateToken,
			Started: result.Started, Finished: result.Finished,
		}
	}
	return out
}
