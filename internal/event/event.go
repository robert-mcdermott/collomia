// Package event defines Collomia's stable runtime event contract.
//
// Every observable moment of a session — turns, streamed text, tool
// lifecycle, permission decisions, usage, warnings, and errors — is
// represented as one typed Event. The TUI, non-interactive JSONL output,
// the audit ledger, and future persistence layers all consume this one
// schema rather than inventing their own shapes.
package event

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// SchemaVersion identifies the wire shape of Event. It changes only on
// breaking changes; additive optional fields do not bump it.
const SchemaVersion = 1

type Kind string

const (
	KindSessionStart       Kind = "session.start"
	KindTurnStart          Kind = "turn.start"
	KindTextDelta          Kind = "text.delta"
	KindReasoningDelta     Kind = "reasoning.delta"
	KindToolCallDelta      Kind = "tool.call.delta"
	KindToolStart          Kind = "tool.start"
	KindToolOutput         Kind = "tool.output"
	KindToolResult         Kind = "tool.result"
	KindPermissionRequest  Kind = "permission.request"
	KindPermissionDecision Kind = "permission.decision"
	KindFileChange         Kind = "file.change"
	KindPlanUpdate         Kind = "plan.update"
	KindDelegateUpdate     Kind = "delegate.update"
	KindUsage              Kind = "usage"
	KindCompaction         Kind = "context.compaction"
	KindWarning            Kind = "warning"
	KindError              Kind = "error"
	KindTurnEnd            Kind = "turn.end"
	// KindRunResult is emitted exactly once, last, by non-interactive runs:
	// a machine-readable summary of the whole run (additive to schema v1).
	KindRunResult Kind = "run.result"
)

// Event is one runtime occurrence. Kind determines which optional payload
// fields are populated.
type Event struct {
	Schema int       `json:"schema"`
	Time   time.Time `json:"time"`
	Kind   Kind      `json:"kind"`
	Turn   int       `json:"turn,omitempty"`
	// Text carries streamed deltas, notices, warnings, and plan text.
	Text       string           `json:"text,omitempty"`
	Tool       *Tool            `json:"tool,omitempty"`
	Permission *Permission      `json:"permission,omitempty"`
	File       *FileChange      `json:"file,omitempty"`
	Usage      *Usage           `json:"usage,omitempty"`
	ToolCall   *ToolCallDelta   `json:"tool_call,omitempty"`
	Delegate   *DelegateStatus  `json:"delegate,omitempty"`
	Result     *RunResult       `json:"result,omitempty"`
	Provider   *ProviderFailure `json:"provider,omitempty"`
	Error      string           `json:"error,omitempty"`
	// FailureID is an opaque per-failure correlation value. It contains no
	// session, provider, path, prompt, or credential material.
	FailureID string `json:"failure_id,omitempty"`
}

// ToolCallDelta carries an incremental provider tool request. ArgumentsDelta
// can be incomplete JSON until Done is true. It is intended for JSONL clients
// and diagnostics; tool execution still waits for the provider's final,
// validated ToolCall.
type ToolCallDelta struct {
	Index          int    `json:"index"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
	Done           bool   `json:"done,omitempty"`
}

// ProviderFailure is the machine-readable classification attached to an
// error event when the failure came from a built-in provider adapter.
type ProviderFailure struct {
	Name         string `json:"name"`
	Operation    string `json:"operation,omitempty"`
	Kind         string `json:"kind"`
	StatusCode   int    `json:"status_code,omitempty"`
	Retryable    bool   `json:"retryable"`
	RetryAfterMS int64  `json:"retry_after_ms,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
}

// Tool describes one tool invocation's lifecycle.
type Tool struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	Output  string `json:"output,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

// Permission describes a requested privileged action and its decision.
type Permission struct {
	Tool      string   `json:"tool"`
	Summary   string   `json:"summary"`
	Risk      string   `json:"risk"`
	Resources []string `json:"resources,omitempty"`
	// Source is where the decision came from: rule, mode, session, interactive, or denied-tool.
	Source  string `json:"source,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Allowed bool   `json:"allowed"`
}

// FileChange records a mutation the agent made to a file.
type FileChange struct {
	Path      string `json:"path"`
	Operation string `json:"operation"` // write, edit, delete
}

// RunResult is the final summary of a non-interactive run. Consumers should
// use Status — not the presence of an error event mid-stream — to decide how
// the run ended: "ok", "error", or "cancelled".
type RunResult struct {
	Status  string   `json:"status"`
	Answer  string   `json:"answer,omitempty"`
	Error   string   `json:"error,omitempty"`
	Failure *Failure `json:"failure,omitempty"`
	// Partial is true when a failed or cancelled run produced visible text,
	// tool activity, or changed files before it stopped. It is intentionally
	// independent of Status so automation does not have to infer progress.
	Partial bool `json:"partial,omitempty"`
	// Ephemeral reports that this run deliberately omitted durable
	// conversation/session persistence. Audit records and workspace changes
	// are unaffected.
	Ephemeral bool `json:"ephemeral,omitempty"`
	// Refused reports that at least one requested action was denied by policy
	// or could not obtain interactive approval. A run may still end "ok" when
	// the agent recovers and explains the refusal.
	Refused      bool     `json:"refused,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	DurationMS   int64    `json:"duration_ms"`
}

// FailureKind is the stable, provider-neutral reason a non-interactive run
// did not complete. Provider-specific detail, when available, is nested in
// Failure.Provider without changing the top-level classification.
type FailureKind string

const (
	FailureUsage         FailureKind = "usage"
	FailureConfiguration FailureKind = "configuration"
	FailurePermission    FailureKind = "permission"
	FailureProvider      FailureKind = "provider"
	FailureTimeout       FailureKind = "timeout"
	FailureCancelled     FailureKind = "cancelled"
	FailureRuntime       FailureKind = "runtime"
)

// Failure adds machine-readable error classification to run.result. Error
// remains the human-readable message for schema-v1 compatibility.
type Failure struct {
	ID        string           `json:"id,omitempty"`
	Kind      FailureKind      `json:"kind"`
	Retryable bool             `json:"retryable,omitempty"`
	Provider  *ProviderFailure `json:"provider,omitempty"`
}

// Usage carries provider-reported token accounting.
type Usage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// DelegateStatus is the durable, provider-neutral parent-inbox state for one
// delegated task. Lifecycle updates replace earlier snapshots with the same
// ID when a session is restored; no stored task is ever executed by replay.
type DelegateStatus struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Task              string    `json:"task,omitempty"`
	Profile           string    `json:"profile,omitempty"`
	Provider          string    `json:"provider,omitempty"`
	Model             string    `json:"model,omitempty"`
	Write             bool      `json:"write,omitempty"`
	PlanStep          int       `json:"plan_step,omitempty"`
	Status            string    `json:"status"`
	CurrentAction     string    `json:"current_action,omitempty"`
	RecentOutput      string    `json:"recent_output,omitempty"`
	Guidance          []string  `json:"guidance,omitempty"`
	PendingGuidance   int       `json:"pending_guidance,omitempty"`
	Summary           string    `json:"summary,omitempty"`
	Error             string    `json:"error,omitempty"`
	FailureID         string    `json:"failure_id,omitempty"`
	Evidence          []string  `json:"evidence,omitempty"`
	ChangedFiles      []string  `json:"changed_files,omitempty"`
	Worktree          string    `json:"worktree,omitempty"`
	Branch            string    `json:"branch,omitempty"`
	BaseCommit        string    `json:"base_commit,omitempty"`
	IntegratedFiles   []string  `json:"integrated_files,omitempty"`
	IntegrationStatus string    `json:"integration_status,omitempty"`
	IntegrationError  string    `json:"integration_error,omitempty"`
	Usage             Usage     `json:"usage,omitempty"`
	TokenBudget       int       `json:"token_budget,omitempty"`
	TimeoutSeconds    int       `json:"timeout_seconds,omitempty"`
	Revision          uint64    `json:"revision,omitempty"`
	Started           time.Time `json:"started,omitempty"`
	Finished          time.Time `json:"finished,omitempty"`
}

// New returns an Event stamped with the schema version and current time.
func New(kind Kind) Event {
	return Event{Schema: SchemaVersion, Time: time.Now().UTC(), Kind: kind}
}

// Emit is the callback shape used by the agent loop and its consumers.
type Emit func(Event)

// Sink receives every event of a run. Sinks must tolerate concurrent calls.
type Sink interface {
	Handle(Event)
}

// JSONLWriter is a Sink that writes one JSON object per line. A redactor,
// when set, scrubs serialized events before they leave the process.
type JSONLWriter struct {
	mu     sync.Mutex
	out    io.Writer
	Redact func(string) string
}

func NewJSONLWriter(out io.Writer) *JSONLWriter { return &JSONLWriter{out: out} }

func (w *JSONLWriter) Handle(e Event) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	line := string(data)
	if w.Redact != nil {
		line = w.Redact(line)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = io.WriteString(w.out, line+"\n")
}

// Multi fans one event out to several sinks.
func Multi(sinks ...Sink) Emit {
	return func(e Event) {
		for _, s := range sinks {
			if s != nil {
				s.Handle(e)
			}
		}
	}
}
