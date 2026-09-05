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
	ID, Tool, Detail, RetryKey string
	Risk                       tools.Risk
}
type pendingEffect struct {
	Tool, Summary, RetryKey string
	Paths                   []string
	Unknown                 bool
}
type completionState struct {
	Mode            taskmode.Mode     `json:"task_mode,omitempty"`
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
	if state.Schema != 1 || len(state.Paths) > 128 || len(state.Roles) > 64 || len(state.Failures) > 64 {
		return state, errors.New("unsupported or oversized completion state")
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
		c.failures = append(c.failures, unresolvedToolFailure{id: failure.ID, tool: failure.Tool, risk: failure.Risk, detail: failure.Detail, retryKey: failure.RetryKey, planRevision: c.initialRevision})
	}
	if c.dirty || len(c.failures) > 0 || len(state.Roles) > 0 || c.pending != nil {
		c.initialOpen = true
	}
	if current := c.board.Current(); current != nil {
		c.noteAtMutation = completionDisclosure(current, c.taskMode)
	}
	// Every new turn obtains fresh validation. No old digest, waiver, permission,
	// successful tool receipt, or process-local root identity is restored.
	return nil
}
func (c *completionController) recoveryState() completionState {
	state := completionState{Schema: 1, Mode: c.taskMode, Dirty: c.dirty, Unknown: c.dirtyUnknown, Roles: c.artifacts.roles, Overflow: c.artifacts.overflow, Pending: c.pending}
	for p := range c.dirtyPaths {
		state.Paths = append(state.Paths, p)
	}
	slices.Sort(state.Paths)
	if len(state.Paths) > 128 {
		state.Paths = state.Paths[:128]
		state.Unknown = true
	}
	for _, f := range c.failures {
		state.Failures = append(state.Failures, recoveryFailure{ID: f.id, Tool: f.tool, Detail: clipUTF8(f.detail, 512), RetryKey: f.retryKey, Risk: f.risk})
	}
	if len(state.Failures) > 64 {
		state.Failures = state.Failures[:64]
		state.Overflow = true
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
		if !o.Failed || !o.Effects.Unknown {
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
	return "Runtime recovery state from unfinished work (not user instructions). These obligations survive turns and restart; use fresh validation and current-turn recovery receipts. Do not replay historical actions. An uncertain outcome requires read-only inspection and the user's /recovery acknowledge REASON.\n" + clipUTF8(string(data), 8192)
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
