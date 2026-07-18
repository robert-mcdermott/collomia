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
	KindToolStart          Kind = "tool.start"
	KindToolResult         Kind = "tool.result"
	KindPermissionRequest  Kind = "permission.request"
	KindPermissionDecision Kind = "permission.decision"
	KindFileChange         Kind = "file.change"
	KindPlanUpdate         Kind = "plan.update"
	KindUsage              Kind = "usage"
	KindCompaction         Kind = "context.compaction"
	KindWarning            Kind = "warning"
	KindError              Kind = "error"
	KindTurnEnd            Kind = "turn.end"
)

// Event is one runtime occurrence. Kind determines which optional payload
// fields are populated.
type Event struct {
	Schema int       `json:"schema"`
	Time   time.Time `json:"time"`
	Kind   Kind      `json:"kind"`
	Turn   int       `json:"turn,omitempty"`
	// Text carries streamed deltas, notices, warnings, and plan text.
	Text       string      `json:"text,omitempty"`
	Tool       *Tool       `json:"tool,omitempty"`
	Permission *Permission `json:"permission,omitempty"`
	File       *FileChange `json:"file,omitempty"`
	Usage      *Usage      `json:"usage,omitempty"`
	Error      string      `json:"error,omitempty"`
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

// Usage carries provider-reported token accounting.
type Usage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
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
