package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectedOutputCeilingReadsTheProviderStatedNumber(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{
			name: "anthropic",
			body: `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 128000 > 64000, which is the maximum allowed number of output tokens for claude-sonnet-4-5-20250929"}}`,
			want: 64000,
		},
		{
			name: "openai",
			body: `{"error":{"message":"max_tokens is too large: 128000. This model supports at most 16384 completion tokens","type":"invalid_request_error","param":"max_tokens"}}`,
			want: 16384,
		},
		{
			name: "gateway phrasing",
			body: `{"error":{"message":"Invalid max_tokens for model qwen3-coder: maximum is 8192"}}`,
			want: 8192,
		},
		{
			name: "bare body",
			body: `max_tokens: 200000 > 32000, which is the maximum allowed`,
			want: 32000,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rejectedOutputCeiling(http.StatusBadRequest, []byte(tc.body)); got != tc.want {
				t.Errorf("ceiling = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRejectedOutputCeilingIgnoresDigitsInAModelName(t *testing.T) {
	// The failure this guards against is silent and severe. A message naming
	// claude-sonnet-4-5-20250929 contains 4, 5, and a date; anything that
	// scanned for "the smallest number in the message" would learn a ceiling of
	// four output tokens and remember it for the whole session. The patterns
	// are anchored on the phrasing that carries the number for exactly this
	// reason.
	body := `{"error":{"message":"Model claude-sonnet-4-5-20250929 is not available in region us-west-2"}}`
	if got := rejectedOutputCeiling(http.StatusBadRequest, []byte(body)); got != 0 {
		t.Errorf("an unrelated rejection must yield no ceiling, got %d", got)
	}
	tools := `{"error":{"message":"gpt-4o-mini does not support tools"}}`
	if got := rejectedOutputCeiling(http.StatusBadRequest, []byte(tools)); got != 0 {
		t.Errorf("an unrelated rejection must yield no ceiling, got %d", got)
	}
}

func TestRejectedOutputCeilingIgnoresAParameterRejection(t *testing.T) {
	// "max_tokens is unsupported, use max_completion_tokens" is the other
	// negotiation, and reading a ceiling out of it would clamp the request
	// instead of switching the field.
	body := `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","param":"max_tokens","code":"unsupported_parameter"}}`
	if got := rejectedOutputCeiling(http.StatusBadRequest, []byte(body)); got != 0 {
		t.Errorf("a parameter rejection must not be read as a ceiling, got %d", got)
	}
}

func TestRejectedOutputCeilingIgnoresNonBadRequest(t *testing.T) {
	body := `{"error":{"message":"max_tokens: 128000 > 64000, which is the maximum allowed"}}`
	if got := rejectedOutputCeiling(http.StatusInternalServerError, []byte(body)); got != 0 {
		t.Error("only an explicit 400 states a ceiling; a 500 saying anything is a server problem")
	}
}

func TestOpenAIClientRetriesUnderAStatedCeiling(t *testing.T) {
	var requested []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		payload, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(payload, &body)
		value, _ := body["max_tokens"].(float64)
		requested = append(requested, int(value))
		if int(value) > 16384 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"max_tokens is too large: 128000. This model supports at most 16384 completion tokens"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer server.Close()

	client := &OpenAIClient{Label: "test", BaseURL: server.URL, HTTP: server.Client()}
	var warnings []string
	response, err := client.Chat(context.Background(), Request{Model: "m", MaxTokens: 128000,
		Messages: []Message{{Role: "user", Content: "hi"}}}, func(d Delta) {
		if d.Warning != "" {
			warnings = append(warnings, d.Warning)
		}
	})
	if err != nil {
		t.Fatalf("a stated ceiling must correct the request, not fail the turn: %v", err)
	}
	if response.Content != "ok" {
		t.Errorf("content = %q", response.Content)
	}
	if len(requested) != 2 || requested[0] != 128000 || requested[1] != 16384 {
		t.Errorf("requested max_tokens sequence = %v, want [128000 16384]", requested)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "16384") {
		t.Errorf("the correction must be reported with the ceiling in it, got %v", warnings)
	}

	// The ceiling is remembered, so the wasted round trip happens once for the
	// life of the client rather than on every call in the turn.
	requested = nil
	if _, err := client.Chat(context.Background(), Request{Model: "m", MaxTokens: 128000,
		Messages: []Message{{Role: "user", Content: "again"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 1 || requested[0] != 16384 {
		t.Errorf("a learned ceiling must be applied before the request, got %v", requested)
	}
}

func TestOpenAIClientDoesNotRetryWhenTheCeilingIsNotSmaller(t *testing.T) {
	// A rejection naming a ceiling the request was already under means
	// something else is wrong. Retrying would send an identical body and fail
	// identically, turning one clear error into two.
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"max_tokens is too large: 4096. This model supports at most 8192 completion tokens"}}`))
	}))
	defer server.Close()

	client := &OpenAIClient{Label: "test", BaseURL: server.URL, HTTP: server.Client()}
	if _, err := client.Chat(context.Background(), Request{Model: "m", MaxTokens: 4096,
		Messages: []Message{{Role: "user", Content: "hi"}}}, nil); err == nil {
		t.Fatal("expected the provider error to surface")
	}
	if calls != 1 {
		t.Errorf("expected exactly one request, got %d", calls)
	}
}

func TestAnthropicClientRetriesUnderAStatedCeiling(t *testing.T) {
	var requested []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		payload, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(payload, &body)
		value, _ := body["max_tokens"].(float64)
		requested = append(requested, int(value))
		if int(value) > 64000 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 128000 > 64000, which is the maximum allowed number of output tokens for claude-sonnet-4-5-20250929"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	client := &AnthropicClient{Label: "test", BaseURL: server.URL, HTTP: server.Client()}
	response, err := client.Chat(context.Background(), Request{Model: "claude-sonnet-4-5-20250929", MaxTokens: 128000,
		Messages: []Message{{Role: "user", Content: "hi"}}}, nil)
	if err != nil {
		t.Fatalf("a stated ceiling must correct the request, not fail the turn: %v", err)
	}
	if response.Content != "ok" {
		t.Errorf("content = %q", response.Content)
	}
	if len(requested) != 2 || requested[1] != 64000 {
		t.Errorf("requested max_tokens sequence = %v, want the second to be 64000", requested)
	}
}
