package provider

import (
	"errors"
	"strings"
)

type Termination string

const (
	TerminationCompleted Termination = "completed"
	TerminationTools     Termination = "requires_tools"
	TerminationTruncated Termination = "truncated"
	TerminationRefused   Termination = "refused"
	TerminationFailed    Termination = "failed"
)

var (
	ErrResponseTruncated  = errors.New("provider response truncated")
	ErrResponseRefused    = errors.New("provider response refused")
	ErrResponseIncomplete = errors.New("provider response did not complete")
)

// Termination interprets machine-provided status only, never answer prose.
// Legacy compatible endpoints may omit a stop reason; keep that behavior for
// nonempty answers/tool calls. Explicit unknown or incomplete states fail closed.
func (r Response) Termination() Termination {
	if r.Refused {
		return TerminationRefused
	}
	stop := strings.ToLower(strings.TrimSpace(r.Stop))
	if stop == "incomplete" && r.StopDetail != "" {
		stop = strings.ToLower(strings.TrimSpace(r.StopDetail))
	}
	switch stop {
	case "length", "max_tokens", "max_output_tokens", "model_context_window_exceeded":
		return TerminationTruncated
	case "refusal", "content_filter", "guardrail_intervened":
		return TerminationRefused
	case "tool_calls", "function_call", "tool_use":
		if len(r.ToolCalls) > 0 {
			return TerminationTools
		}
		return TerminationFailed
	case "", "stop", "end_turn", "stop_sequence", "completed":
		if len(r.ToolCalls) > 0 {
			return TerminationTools
		}
		if strings.TrimSpace(r.Content) != "" {
			return TerminationCompleted
		}
	}
	return TerminationFailed
}

// CompletionError never requests an automatic retry: a partial response may
// follow real tool effects. The caller retains accounting and partial text,
// but must neither execute this response's calls nor accept it as a final answer.
func (r Response) CompletionError(name string) error {
	var cause error
	var message string
	switch r.Termination() {
	case TerminationCompleted, TerminationTools:
		return nil
	case TerminationTruncated:
		cause = ErrResponseTruncated
		message = "the response reached an output or context limit; partial output was retained, but this response's tools were not executed and the task is not complete. Review the partial work and request a shorter response or adjust the model's token limits before continuing"
	case TerminationRefused:
		cause = ErrResponseRefused
		message = "the provider refused or filtered the response; this response's tools were not executed and the task is not complete"
	default:
		cause = ErrResponseIncomplete
		message = "the provider did not report a usable completed response; this response's tools were not executed and the task is not complete"
	}
	if stop := sanitizeProviderText(r.Stop, 128); stop != "" {
		message += " (stop: " + stop + ")"
	}
	return &Error{Provider: name, Operation: "completion", Kind: ErrorProtocol, Message: message, Err: cause}
}

// Tool arguments may be incomplete precisely because the response was cut off.
// Check its status before decoding accumulated arguments, so a JSON error does
// not discard partial prose/accounting or hide the actual truncation reason.
func (r Response) acceptsToolCalls() bool {
	r.ToolCalls = []ToolCall{{}}
	return r.Termination() == TerminationTools
}
