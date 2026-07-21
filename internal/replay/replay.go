// Package replay validates and renders completed Collomia JSONL run traces.
//
// Replay is deliberately observational: it consumes event records without
// constructing an app runtime, contacting a provider, executing a tool, or
// opening a durable session. This makes recorded traces safe and deterministic
// inputs for diagnostics, support, and regression tests.
package replay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/redact"
)

const (
	maxEventBytes  = 8 << 20
	maxRenderRunes = 64 << 10
)

var commonRedactor = redact.New()

// Trace is one validated, complete non-interactive run.
type Trace struct {
	Events              []event.Event
	Result              event.RunResult
	Usage               *event.Usage
	Turns               int
	Tools               int
	PermissionDecisions int
	Refusals            int
}

// Read validates a schema-v1 JSONL stream and returns its typed events. A
// completed trace must end in exactly one run.result. Unknown additive fields
// are tolerated, while unknown schema versions and event kinds fail clearly.
func Read(r io.Reader) (*Trace, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxEventBytes)

	trace := &Trace{}
	line := 0
	turnOpen := false
	activeTool := ""
	terminal := false
	for scanner.Scan() {
		line++
		data := scanner.Bytes()
		if len(strings.TrimSpace(string(data))) == 0 {
			return nil, lineError(line, "blank lines are not valid JSONL events")
		}
		if terminal {
			return nil, lineError(line, "event appears after the terminal run.result")
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, lineError(line, "invalid JSON: %v", err)
		}
		var e event.Event
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, lineError(line, "invalid event: %v", err)
		}
		if err := validateEvent(line, e, fields); err != nil {
			return nil, err
		}

		switch e.Kind {
		case event.KindTurnStart:
			if turnOpen {
				return nil, lineError(line, "turn.start encountered while a turn is already active")
			}
			turnOpen = true
			trace.Turns++
		case event.KindTurnEnd:
			if !turnOpen {
				return nil, lineError(line, "turn.end has no matching turn.start")
			}
			if activeTool != "" {
				return nil, lineError(line, "turn ended while tool %q is still active", activeTool)
			}
			turnOpen = false
		case event.KindToolStart:
			if !turnOpen {
				return nil, lineError(line, "tool.start for %q appears outside an active turn", e.Tool.Name)
			}
			if activeTool != "" {
				return nil, lineError(line, "tool %q started while tool %q is still active", e.Tool.Name, activeTool)
			}
			activeTool = e.Tool.Name
			trace.Tools++
		case event.KindToolOutput:
			if activeTool == "" {
				return nil, lineError(line, "tool.output for %q has no active tool", e.Tool.Name)
			}
			if e.Tool.Name != activeTool {
				return nil, lineError(line, "tool.output names %q while %q is active", e.Tool.Name, activeTool)
			}
		case event.KindToolResult:
			if activeTool == "" {
				return nil, lineError(line, "tool.result for %q has no matching tool.start", e.Tool.Name)
			}
			if e.Tool.Name != activeTool {
				return nil, lineError(line, "tool.result names %q while %q is active", e.Tool.Name, activeTool)
			}
			activeTool = ""
		case event.KindPermissionDecision:
			if !turnOpen {
				return nil, lineError(line, "permission.decision appears outside an active turn")
			}
			trace.PermissionDecisions++
			if !e.Permission.Allowed {
				trace.Refusals++
			}
		case event.KindRunResult:
			if trace.Refusals > 0 && !e.Result.Refused {
				return nil, lineError(line, "run.result must set refused after a denied permission decision")
			}
			if e.Result.Status == "ok" {
				if turnOpen {
					return nil, lineError(line, "successful run.result encountered before turn.end")
				}
				if activeTool != "" {
					return nil, lineError(line, "successful run.result encountered while tool %q is active", activeTool)
				}
			}
			terminal = true
			trace.Result = *e.Result
			trace.Usage = e.Usage
		}
		trace.Events = append(trace.Events, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, lineError(line+1, "could not read an event (maximum line size is %d bytes): %v", maxEventBytes, err)
	}
	if line == 0 {
		return nil, fmt.Errorf("trace is empty")
	}
	if !terminal {
		return nil, lineError(line+1, "trace ended without a terminal run.result")
	}
	return trace, nil
}

func validateEvent(line int, e event.Event, fields map[string]json.RawMessage) error {
	if e.Schema != event.SchemaVersion {
		return lineError(line, "unsupported schema %d (this binary supports schema %d)", e.Schema, event.SchemaVersion)
	}
	if e.Time.IsZero() {
		return lineError(line, "time must be a valid non-zero RFC 3339 timestamp")
	}
	if !knownKind(e.Kind) {
		return lineError(line, "unsupported event kind %q", e.Kind)
	}
	for _, name := range []string{"turn", "text", "tool", "permission", "file", "usage", "tool_call", "result", "provider", "error"} {
		if raw, ok := fields[name]; ok && string(raw) == "null" {
			return lineError(line, "field %q cannot be null", name)
		}
	}
	if raw, ok := fields["turn"]; ok && string(raw) != "null" && e.Turn < 1 {
		return lineError(line, "turn must be at least 1 when present")
	}

	require := func(name string, value any) error {
		raw, ok := fields[name]
		if !ok || string(raw) == "null" || value == nil {
			return lineError(line, "%s requires a non-null %q payload", e.Kind, name)
		}
		return nil
	}
	switch e.Kind {
	case event.KindTextDelta, event.KindReasoningDelta, event.KindPlanUpdate, event.KindCompaction, event.KindWarning:
		if raw, ok := fields["text"]; !ok || string(raw) == "null" {
			return lineError(line, "%s requires a %q payload", e.Kind, "text")
		}
	case event.KindToolCallDelta:
		if err := require("tool_call", e.ToolCall); err != nil {
			return err
		}
		if _, err := requireObjectFields(line, string(e.Kind), "tool_call", fields["tool_call"], "index"); err != nil {
			return err
		}
		if e.ToolCall.Index < 0 {
			return lineError(line, "tool_call.index cannot be negative")
		}
	case event.KindToolStart, event.KindToolOutput, event.KindToolResult:
		if err := require("tool", e.Tool); err != nil {
			return err
		}
		if _, err := requireObjectFields(line, string(e.Kind), "tool", fields["tool"], "name"); err != nil {
			return err
		}
		if strings.TrimSpace(e.Tool.Name) == "" {
			return lineError(line, "tool.name cannot be empty")
		}
	case event.KindPermissionRequest, event.KindPermissionDecision:
		if err := require("permission", e.Permission); err != nil {
			return err
		}
		if _, err := requireObjectFields(line, string(e.Kind), "permission", fields["permission"], "tool", "summary", "risk", "allowed"); err != nil {
			return err
		}
		if strings.TrimSpace(e.Permission.Tool) == "" || strings.TrimSpace(e.Permission.Summary) == "" || strings.TrimSpace(e.Permission.Risk) == "" {
			return lineError(line, "permission tool, summary, and risk cannot be empty")
		}
	case event.KindFileChange:
		if err := require("file", e.File); err != nil {
			return err
		}
		if _, err := requireObjectFields(line, string(e.Kind), "file", fields["file"], "path", "operation"); err != nil {
			return err
		}
		if strings.TrimSpace(e.File.Path) == "" {
			return lineError(line, "file.path cannot be empty")
		}
		if e.File.Operation != "write" && e.File.Operation != "edit" && e.File.Operation != "delete" {
			return lineError(line, "unsupported file operation %q", e.File.Operation)
		}
	case event.KindUsage:
		if err := require("usage", e.Usage); err != nil {
			return err
		}
		if err := validateUsage(line, e.Usage, fields["usage"]); err != nil {
			return err
		}
	case event.KindError:
		if _, ok := fields["error"]; !ok || strings.TrimSpace(e.Error) == "" {
			return lineError(line, "error requires a non-empty %q payload", "error")
		}
	case event.KindRunResult:
		if err := require("result", e.Result); err != nil {
			return err
		}
		if err := validateResult(line, e.Result, fields["result"]); err != nil {
			return err
		}
		if e.Usage != nil {
			return validateUsage(line, e.Usage, fields["usage"])
		}
	}
	if e.Provider != nil {
		if err := validateProviderFailure(line, e.Provider, fields["provider"]); err != nil {
			return err
		}
	}
	return nil
}

func validateResult(line int, result *event.RunResult, raw json.RawMessage) error {
	fields, err := requireObjectFields(line, string(event.KindRunResult), "result", raw, "status", "duration_ms")
	if err != nil {
		return err
	}
	if result.DurationMS < 0 {
		return lineError(line, "result.duration_ms cannot be negative")
	}
	switch result.Status {
	case "ok":
		if result.Failure != nil {
			return lineError(line, "successful result cannot include failure metadata")
		}
		if strings.TrimSpace(result.Error) != "" {
			return lineError(line, "successful result cannot include an error message")
		}
	case "error":
		if result.Failure == nil {
			return lineError(line, "error result requires failure metadata")
		}
		if result.Failure.Kind == event.FailureCancelled {
			return lineError(line, "cancelled failure must use result status %q", "cancelled")
		}
	case "cancelled":
		if result.Failure == nil || result.Failure.Kind != event.FailureCancelled {
			return lineError(line, "cancelled result requires failure kind %q", event.FailureCancelled)
		}
	default:
		return lineError(line, "unsupported result status %q", result.Status)
	}
	if result.Ephemeral && result.SessionID != "" {
		return lineError(line, "ephemeral result cannot include a session_id")
	}
	if result.Failure != nil {
		failureFields, err := requireObjectFields(line, "run.result result", "failure", fields["failure"], "kind")
		if err != nil {
			return err
		}
		switch result.Failure.Kind {
		case event.FailureUsage, event.FailureConfiguration, event.FailurePermission, event.FailureProvider, event.FailureTimeout, event.FailureCancelled, event.FailureRuntime:
		default:
			return lineError(line, "unsupported failure kind %q", result.Failure.Kind)
		}
		if result.Failure.Kind == event.FailureProvider {
			if result.Failure.Provider == nil {
				return lineError(line, "provider failure requires provider metadata")
			}
			if err := validateProviderFailure(line, result.Failure.Provider, failureFields["provider"]); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProviderFailure(line int, failure *event.ProviderFailure, raw json.RawMessage) error {
	if _, err := requireObjectFields(line, "provider failure", "provider", raw, "name", "kind", "retryable"); err != nil {
		return err
	}
	if strings.TrimSpace(failure.Name) == "" || strings.TrimSpace(failure.Kind) == "" {
		return lineError(line, "provider failure name and kind cannot be empty")
	}
	if failure.StatusCode != 0 && (failure.StatusCode < 100 || failure.StatusCode > 599) {
		return lineError(line, "provider status_code must be between 100 and 599")
	}
	if failure.RetryAfterMS < 0 {
		return lineError(line, "provider retry_after_ms cannot be negative")
	}
	return nil
}

func validateUsage(line int, usage *event.Usage, raw json.RawMessage) error {
	if _, err := requireObjectFields(line, "usage", "usage", raw, "input_tokens", "output_tokens"); err != nil {
		return err
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CachedTokens < 0 || usage.ReasoningTokens < 0 {
		return lineError(line, "usage token counts cannot be negative")
	}
	return nil
}

func requireObjectFields(line int, kind, name string, raw json.RawMessage, required ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" {
		return nil, lineError(line, "%s requires a non-null %q payload", kind, name)
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, lineError(line, "%s %q payload must be an object", kind, name)
	}
	for _, field := range required {
		value, ok := fields[field]
		if !ok || string(value) == "null" {
			return nil, lineError(line, "%s %q payload requires field %q", kind, name, field)
		}
	}
	return fields, nil
}

func knownKind(kind event.Kind) bool {
	switch kind {
	case event.KindSessionStart, event.KindTurnStart, event.KindTextDelta, event.KindReasoningDelta,
		event.KindToolCallDelta, event.KindToolStart, event.KindToolOutput, event.KindToolResult,
		event.KindPermissionRequest, event.KindPermissionDecision, event.KindFileChange,
		event.KindPlanUpdate, event.KindUsage, event.KindCompaction, event.KindWarning,
		event.KindError, event.KindTurnEnd, event.KindRunResult:
		return true
	default:
		return false
	}
}

func lineError(line int, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, args...))
}

// Summary returns a stable one-line description suitable for --check and
// support scripts.
func (t *Trace) Summary() string {
	return fmt.Sprintf("valid Collomia schema-v%d trace: %s, %s, %s, status %s",
		event.SchemaVersion, countLabel(len(t.Events), "event"), countLabel(t.Turns, "turn"), countLabel(t.Tools, "tool"), t.Result.Status)
}

func countLabel(count int, singular string) string {
	suffix := "s"
	if count == 1 {
		suffix = ""
	}
	return fmt.Sprintf("%d %s%s", count, singular, suffix)
}

// Render writes a deterministic, control-character-safe human transcript.
func (t *Trace) Render(w io.Writer) error {
	var streamedAnswer strings.Builder
	var text strings.Builder
	var reasoning strings.Builder
	var toolOutput strings.Builder
	activeTool := ""
	turn := 0

	writeSection := func(label, value string) error {
		value = renderText(value)
		if strings.TrimSpace(value) == "" {
			return nil
		}
		_, err := fmt.Fprintf(w, "\n%s\n%s\n", label, indent(value, "  "))
		return err
	}
	flushText := func() error {
		value := text.String()
		text.Reset()
		return writeSection("COLLOMIA", value)
	}
	flushReasoning := func() error {
		value := reasoning.String()
		reasoning.Reset()
		return writeSection("REASONING", value)
	}
	flushToolOutput := func(value string) error {
		if value == "" {
			value = toolOutput.String()
		}
		toolOutput.Reset()
		return writeSection("OUTPUT", value)
	}
	flushNarrative := func() error {
		if err := flushReasoning(); err != nil {
			return err
		}
		return flushText()
	}

	if _, err := fmt.Fprintf(w, "COLLOMIA REPLAY · schema v%d · %d events\n", event.SchemaVersion, len(t.Events)); err != nil {
		return err
	}
	for _, e := range t.Events {
		switch e.Kind {
		case event.KindTurnStart:
			if err := flushNarrative(); err != nil {
				return err
			}
			turn++
			if _, err := fmt.Fprintf(w, "\nTURN %d\n", turn); err != nil {
				return err
			}
		case event.KindTextDelta:
			if reasoning.Len() > 0 {
				if err := flushReasoning(); err != nil {
					return err
				}
			}
			text.WriteString(e.Text)
			streamedAnswer.WriteString(e.Text)
		case event.KindReasoningDelta:
			if text.Len() > 0 {
				if err := flushText(); err != nil {
					return err
				}
			}
			reasoning.WriteString(e.Text)
		case event.KindToolStart:
			if err := flushNarrative(); err != nil {
				return err
			}
			activeTool = e.Tool.Name
			toolOutput.Reset()
			if _, err := fmt.Fprintf(w, "\nTOOL %s", inlineText(e.Tool.Name)); err != nil {
				return err
			}
			if summary := inlineText(e.Tool.Summary); summary != "" {
				if _, err := fmt.Fprintf(w, " · %s", summary); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		case event.KindToolOutput:
			toolOutput.WriteString(e.Tool.Output)
		case event.KindToolResult:
			if err := flushToolOutput(e.Tool.Output); err != nil {
				return err
			}
			if e.Tool.IsError {
				if _, err := fmt.Fprintln(w, "TOOL STATUS · error"); err != nil {
					return err
				}
			}
			activeTool = ""
		case event.KindPermissionRequest, event.KindPermissionDecision:
			if err := flushNarrative(); err != nil {
				return err
			}
			decision := "requested"
			if e.Kind == event.KindPermissionDecision {
				decision = "denied"
				if e.Permission.Allowed {
					decision = "allowed"
				}
			}
			if _, err := fmt.Fprintf(w, "\nPERMISSION %s · %s · %s\n", strings.ToUpper(decision), inlineText(e.Permission.Tool), inlineText(e.Permission.Summary)); err != nil {
				return err
			}
		case event.KindFileChange:
			if _, err := fmt.Fprintf(w, "\nFILE %s · %s\n", strings.ToUpper(e.File.Operation), inlineText(e.File.Path)); err != nil {
				return err
			}
		case event.KindCompaction:
			if err := writeSection("COMPACTION", e.Text); err != nil {
				return err
			}
		case event.KindWarning:
			if err := writeSection("WARNING", e.Text); err != nil {
				return err
			}
		case event.KindError:
			if err := flushNarrative(); err != nil {
				return err
			}
			if activeTool != "" && toolOutput.Len() > 0 {
				if err := flushToolOutput(""); err != nil {
					return err
				}
			}
			if err := writeSection("ERROR", e.Error); err != nil {
				return err
			}
		case event.KindRunResult:
			if err := flushNarrative(); err != nil {
				return err
			}
			if activeTool != "" && toolOutput.Len() > 0 {
				if err := flushToolOutput(""); err != nil {
					return err
				}
			}
			if streamedAnswer.Len() == 0 && strings.TrimSpace(e.Result.Answer) != "" {
				if err := writeSection("COLLOMIA", e.Result.Answer); err != nil {
					return err
				}
			}
		}
	}

	result := t.Result
	if _, err := fmt.Fprintf(w, "\nRESULT · %s · %d ms", strings.ToUpper(result.Status), result.DurationMS); err != nil {
		return err
	}
	if result.Partial {
		if _, err := fmt.Fprint(w, " · partial"); err != nil {
			return err
		}
	}
	if result.Refused {
		if _, err := fmt.Fprint(w, " · refused action"); err != nil {
			return err
		}
	}
	if result.Ephemeral {
		if _, err := fmt.Fprint(w, " · ephemeral"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if result.Failure != nil {
		if _, err := fmt.Fprintf(w, "FAILURE · %s", result.Failure.Kind); err != nil {
			return err
		}
		if result.Failure.Provider != nil {
			if _, err := fmt.Fprintf(w, " · %s/%s", inlineText(result.Failure.Provider.Name), inlineText(result.Failure.Provider.Kind)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	if strings.TrimSpace(result.Error) != "" {
		if err := writeSection("RESULT ERROR", result.Error); err != nil {
			return err
		}
	}
	if t.Usage != nil {
		if _, err := fmt.Fprintf(w, "USAGE · %d input · %d output", t.Usage.InputTokens, t.Usage.OutputTokens); err != nil {
			return err
		}
		if t.Usage.CachedTokens > 0 {
			if _, err := fmt.Fprintf(w, " · %d cached", t.Usage.CachedTokens); err != nil {
				return err
			}
		}
		if t.Usage.ReasoningTokens > 0 {
			if _, err := fmt.Fprintf(w, " · %d reasoning", t.Usage.ReasoningTokens); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	if len(result.ChangedFiles) > 0 {
		if _, err := fmt.Fprintln(w, "CHANGED FILES"); err != nil {
			return err
		}
		for _, path := range result.ChangedFiles {
			if _, err := fmt.Fprintf(w, "%s\n", indent(renderText(path), "  ")); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderText(value string) string {
	value = commonRedactor.Redact(value)
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	var b strings.Builder
	for _, r := range value {
		if r == '\n' || r == '\t' || (r >= 0x20 && (r < 0x7f || r > 0x9f)) {
			b.WriteRune(r)
		}
	}
	runes := []rune(b.String())
	if len(runes) > maxRenderRunes {
		runes = append(runes[:maxRenderRunes], []rune("\n… replay output truncated …")...)
	}
	value = strings.TrimRight(string(runes), "\n")
	if !utf8.ValidString(value) {
		return strings.ToValidUTF8(value, "�")
	}
	return value
}

func inlineText(value string) string {
	value = renderText(value)
	value = strings.ReplaceAll(value, "\n", " ↵ ")
	value = strings.ReplaceAll(value, "\t", " ")
	return strings.TrimSpace(value)
}

func indent(value, prefix string) string {
	if value == "" {
		return ""
	}
	return prefix + strings.ReplaceAll(value, "\n", "\n"+prefix)
}
