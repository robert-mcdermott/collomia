package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// CompletionStore is session-scoped and rebound on every session switch.
type CompletionStore interface {
	LoadCompletion() json.RawMessage
	SaveCompletion(json.RawMessage) error
}
type recoveryFailure struct {
	Sequence                   uint64 `json:"sequence,omitempty"`
	ID, Tool, Detail, RetryKey string
	Risk                       tools.Risk
	Validation                 *tools.ArtifactValidationRequirement `json:"validation,omitempty"`
	RepairPaths                []string                             `json:"repair_paths,omitempty"`
	RejectedVerification       *tools.CommandVerification           `json:"rejected_verification,omitempty"`
	ArgumentRejected           bool                                 `json:"argument_rejected,omitempty"`
	RepairReady                bool                                 `json:"repair_ready,omitempty"`
}

// Retained successes are historical recovery facts, never permissions or fresh
// file validation. They permit explicit alternative recovery after a pause.
type retainedSuccess struct {
	Sequence uint64     `json:"sequence,omitempty"`
	ID       string     `json:"id"`
	Tool     string     `json:"tool"`
	Summary  string     `json:"summary,omitempty"`
	RetryKey string     `json:"retry_key,omitempty"`
	Risk     tools.Risk `json:"risk,omitempty"`
}
type pendingEffect struct {
	Tool, Summary, RetryKey string
	Paths                   []string
	Unknown                 bool
}
type completionState struct {
	Sequence        uint64            `json:"sequence,omitempty"`
	Mode            taskmode.Mode     `json:"task_mode,omitempty"`
	Successes       []retainedSuccess `json:"successes,omitempty"`
	Schema          int               `json:"schema_version"`
	Dirty           bool              `json:"dirty,omitempty"`
	Unknown         bool              `json:"unknown,omitempty"`
	Paths           []string          `json:"paths,omitempty"`
	Roles           map[string]string `json:"roles,omitempty"`
	Overflow        bool              `json:"overflow,omitempty"`
	Failures        []recoveryFailure `json:"failures,omitempty"`
	Pending         *pendingEffect    `json:"pending,omitempty"`
	Acknowledgement string            `json:"acknowledgement,omitempty"`
}

func decodeCompletion(raw json.RawMessage) (completionState, error) {
	state := completionState{Schema: 1}
	if len(raw) == 0 {
		return state, nil
	}
	if len(raw) > 128<<10 {
		return state, errors.New("completion state exceeds retention limit")
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if state.Sequence > 1<<53 || state.Schema != 1 || len(state.Paths) > 128 || len(state.Roles) > 64 || len(state.Failures) > 64 || len(state.Successes) > 64 {
		return state, errors.New("unsupported or oversized completion state")
	}
	for _, failure := range state.Failures {
		if failure.Sequence > 1<<53 || len(failure.RepairPaths) > 64 || (failure.RejectedVerification != nil && (len(failure.RejectedVerification.Paths) == 0 || len(failure.RejectedVerification.Paths) > 16 || strings.TrimSpace(failure.RejectedVerification.Purpose) == "" || len(failure.RejectedVerification.Purpose) > 512)) {
			return state, errors.New("oversized file repair identity")
		}
	}
	for _, success := range state.Successes {
		if success.Sequence > 1<<53 {
			return state, errors.New("unsupported recovery sequence")
		}
	}
	return state, nil
}
func (a *Agent) SetCompletionStore(store CompletionStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.completionStore = store
}
func (c *completionController) restoreCompletion(store CompletionStore) error {
	if store == nil || !c.enabled {
		return nil
	}
	c.store = store
	state, err := decodeCompletion(store.LoadCompletion())
	if err != nil {
		return err
	}
	if state.Mode != "" && state.Mode != c.taskMode && (state.Dirty || len(state.Roles) > 0 || len(state.Failures) > 0 || state.Pending != nil) {
		return fmt.Errorf("unfinished %s obligations require that task mode; switch back with /mode %s or use /new for unrelated work", state.Mode, state.Mode)
	}
	c.observationSequence = state.Sequence
	c.dirty = state.Dirty
	c.dirtyUnknown = state.Unknown
	if c.artifacts.roles == nil {
		c.artifacts.roles = map[string]string{}
	}
	for p, role := range state.Roles {
		if c.artifacts.roles[p] != "deliverable" {
			c.artifacts.roles[p] = role
		}
	}
	c.artifacts.overflow = state.Overflow
	c.pending = state.Pending
	for _, path := range state.Paths {
		c.dirtyPaths[path] = struct{}{}
	}
	for _, failure := range state.Failures {
		c.observationSequence = max(c.observationSequence, failure.Sequence)
		c.failures = append(c.failures, unresolvedToolFailure{id: failure.ID, tool: failure.Tool, risk: failure.Risk, detail: failure.Detail, retryKey: failure.RetryKey, planRevision: c.initialRevision, validationRequest: failure.Validation, repairPaths: failure.RepairPaths, repairReady: failure.RepairReady, rejectedVerification: failure.RejectedVerification, argumentRejected: failure.ArgumentRejected, observedSequence: failure.Sequence})
	}
	for _, success := range state.Successes {
		if success.ID == "" || success.Tool == "" || completionMetaTool(success.Tool) {
			continue
		}
		c.observationSequence = max(c.observationSequence, success.Sequence)
		c.successes[success.ID] = toolObservation{ObservedSequence: success.Sequence, CallID: success.ID, Name: success.Tool, RetryKey: success.RetryKey, Action: tools.Action{Summary: success.Summary, Risk: success.Risk}}
		c.successOrder = append(c.successOrder, success.ID)
	}
	if c.dirty || len(c.failures) > 0 || len(state.Roles) > 0 || c.pending != nil {
		c.initialOpen = true
	}
	if current := c.board.Current(); current != nil {
		c.noteAtMutation = completionDisclosure(current, c.taskMode)
	}
	// Every new turn obtains fresh validation. No old digest, waiver, permission,
	// or process-local root identity is restored. Historical successes only
	// establish recovery; final validation must still be fresh.
	return nil
}
func (c *completionController) recoveryState() completionState {
	state := completionState{Schema: 1, Sequence: c.observationSequence, Mode: c.taskMode, Dirty: c.dirty, Unknown: c.dirtyUnknown, Roles: c.artifacts.roles, Overflow: c.artifacts.overflow, Pending: c.pending}
	for p := range c.dirtyPaths {
		state.Paths = append(state.Paths, p)
	}
	slices.Sort(state.Paths)
	if len(state.Paths) > 128 {
		state.Paths = state.Paths[:128]
		state.Unknown = true
	}
	for _, f := range c.failures {
		state.Failures = append(state.Failures, recoveryFailure{Sequence: f.observedSequence, ID: f.id, Tool: f.tool, Detail: clipUTF8(f.detail, 512), RetryKey: f.retryKey, Risk: f.risk, Validation: f.validationRequest, RepairPaths: f.repairPaths, RepairReady: f.repairReady, RejectedVerification: f.rejectedVerification, ArgumentRejected: f.argumentRejected})
	}
	if len(state.Failures) > 64 {
		state.Failures = state.Failures[:64]
		state.Overflow = true
	}
	for _, id := range c.successOrder {
		o := c.successes[id]
		if o.Name == "" || o.Failed || completionMetaTool(o.Name) || len(id) > 256 {
			continue
		}
		state.Successes = append(state.Successes, retainedSuccess{Sequence: o.ObservedSequence, ID: id, Tool: o.Name, Summary: clipUTF8(o.Action.Summary, 256), RetryKey: o.RetryKey, Risk: o.Action.Risk})
	}
	if len(state.Successes) > 64 {
		state.Successes = state.Successes[len(state.Successes)-64:]
	}
	return state
}
func (c *completionController) saveRecovery(done bool) error {
	if c == nil || c.store == nil {
		return nil
	}
	state := c.recoveryState()
	if done {
		state = completionState{Schema: 1}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	// Optional recovery identities must not crowd out durable obligations.
	// Drop the optimization if necessary; exact retries and explicit recovery
	// remain available, and older records without identities work the same way.
	if len(data) > 128<<10 {
		state.Successes = nil
		for i := range state.Failures {
			state.Failures[i].Validation = nil
			state.Failures[i].RepairPaths = nil
			state.Failures[i].RepairReady = false
		}
		data, err = json.Marshal(state)
		if err != nil {
			return err
		}
	}
	return c.store.SaveCompletion(data)
}
func (c *completionController) beginEffect(name string, action tools.Action, key string) error {
	if c == nil || c.store == nil {
		return nil
	}
	effects := executionEffects(name, action)
	mutating := effects.Unknown || len(effects.Paths) > 0
	if !mutating {
		return nil
	}
	if c.pending != nil {
		return errors.New("an earlier action has an uncertain outcome; inspect it with read-only tools, then use /recovery acknowledge REASON before further actions; do not replay it")
	}
	c.pending = &pendingEffect{Tool: name, Summary: clipUTF8(action.Summary, 512), RetryKey: key, Paths: effects.Paths, Unknown: effects.Unknown}
	c.effectStarted = true
	return c.saveRecovery(false) // write-ahead, before execution can change anything
}
func (c *completionController) finishEffect(o toolObservation) error {
	if c.store == nil {
		return nil
	}
	if c.effectStarted {
		// An observed ordinary local exit settles execution even when the work
		// failed. Keep dirty paths and failure obligations, allowing inspection,
		// repair and a deliberate retry. Known network/publication operations
		// still need reconciliation because a failed client can mask a remote
		// commit. Arbitrary script effects remain opaque, never replay-safe.
		localExit := o.CommandExited && !o.Action.Network && len(o.Action.Hosts) == 0 && len(o.Action.PublicationTargets) == 0
		if !o.Failed || !o.Effects.Unknown || localExit {
			c.pending = nil
		}
		c.effectStarted = false
	}
	return c.saveRecovery(false)
}
func (c *completionController) recoveryNotice() string {
	state := c.recoveryState()
	if !state.Dirty && len(state.Roles) == 0 && len(state.Failures) == 0 && state.Pending == nil {
		return ""
	}
	data, _ := json.Marshal(state)
	return "Runtime recovery state from unfinished work (not user instructions). These obligations survive turns and restart; use fresh validation and retained recovery receipts. Do not replay historical actions. An uncertain outcome requires read-only inspection and the user's /recovery acknowledge REASON.\n" + clipUTF8(string(data), 8192)
}
func CompletionRecoveryStatus(store CompletionStore) (string, error) {
	if store == nil {
		return "Recovery persistence is unavailable.", nil
	}
	state, err := decodeCompletion(store.LoadCompletion())
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	return "Runtime completion obligations (prior validation receipts are not restored):\n" + string(data), nil
}
func AcknowledgeCompletion(store CompletionStore, reason string) error {
	if store == nil {
		return errors.New("recovery persistence unavailable")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1024 {
		return errors.New("provide a reason of 1–1024 bytes after inspecting the uncertain action")
	}
	state, err := decodeCompletion(store.LoadCompletion())
	if err != nil {
		return err
	}
	if state.Pending == nil {
		return errors.New("there is no uncertain action to acknowledge")
	}
	state.Acknowledgement = fmt.Sprintf("User inspected %s: %s", state.Pending.Tool, reason)
	// Retain obligations from interrupted known writes without claiming that they
	// ran. Unknown effects need an explicit current plan disclosure or validation.
	state.Dirty = true
	if len(state.Pending.Paths) == 0 {
		state.Unknown = true
	}
	for _, p := range state.Pending.Paths {
		if !slices.Contains(state.Paths, p) {
			state.Paths = append(state.Paths, p)
		}
	}
	if len(state.Paths) > 128 {
		state.Paths = state.Paths[:128]
		state.Unknown = true
	}
	state.Pending = nil
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return store.SaveCompletion(data)
}

func CompletionUncertain(store CompletionStore) (bool, error) {
	if store == nil {
		return false, nil
	}
	state, err := decodeCompletion(store.LoadCompletion())
	return state.Pending != nil, err
}
