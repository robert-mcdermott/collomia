package setup

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestCatalogLimitsReadsLMStudioNativeCatalog(t *testing.T) {
	// LM Studio's OpenAI-compatible route publishes ids and nothing else, while
	// its native route publishes the window it is actually serving. One request
	// covers the whole catalog, which is what makes annotating a picker
	// affordable at all.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"qwen3-coder","max_context_length":262144,"loaded_context_length":32768},
			{"id":"gemma3","max_context_length":131072}
		]}`))
	}))
	defer server.Close()

	models := []provider.ModelInfo{{ID: "qwen3-coder"}, {ID: "gemma3"}, {ID: "unlisted"}}
	annotated := CatalogLimits(t.Context(), appconfig.Provider{Type: "openai-compatible", BaseURL: server.URL + "/v1"}, models)

	if got := annotated[0].Limits.ContextWindow; got != 32768 {
		t.Errorf("a loaded window must win over the maximum the weights allow, got %d", got)
	}
	if got := annotated[1].Limits.ContextWindow; got != 131072 {
		t.Errorf("gemma3 window = %d", got)
	}
	if annotated[2].Limits.Known() {
		t.Error("a model the native catalog does not list must gain nothing")
	}
}

func TestCatalogLimitsNeverOverwritesWhatTheCatalogReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m","max_context_length":4096}]}`))
	}))
	defer server.Close()
	models := []provider.ModelInfo{{ID: "m", Limits: provider.Limits{ContextWindow: 65536, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsEndpoint}}}
	annotated := CatalogLimits(t.Context(), appconfig.Provider{Type: "openai-compatible", BaseURL: server.URL + "/v1"}, models)
	if annotated[0].Limits.ContextWindow != 65536 {
		t.Error("this is a fallback for endpoints that published nothing, not a second opinion")
	}
}

func TestCatalogLimitsLeavesNonLocalProvidersAlone(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	models := []provider.ModelInfo{{ID: "gpt-4o"}}
	CatalogLimits(t.Context(), appconfig.Provider{Type: "openai", BaseURL: server.URL + "/v1"}, models)
	if called {
		t.Error("the native probes are for local runtimes; a hosted provider must not be asked for paths it does not have")
	}
}

func TestModelLimitsAsksOllamaAboutTheChosenModel(t *testing.T) {
	// Ollama answers one model per request, so this happens after a model is
	// chosen rather than across a catalog. The window lives under an
	// architecture-qualified key, which has to be read rather than assumed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]string
		payload, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(payload, &body)
		if body["model"] != "qwen3-coder:30b" {
			t.Errorf("asked about %q", body["model"])
		}
		_, _ = w.Write([]byte(`{"model_info":{"general.architecture":"qwen3moe","qwen3moe.context_length":262144,"qwen3moe.block_count":48}}`))
	}))
	defer server.Close()

	limits := ModelLimits(t.Context(), appconfig.Provider{Type: "openai-compatible", BaseURL: server.URL + "/v1"}, "qwen3-coder:30b", nil)
	if limits.ContextWindow != 262144 {
		t.Errorf("context window = %d, want the runtime's own answer", limits.ContextWindow)
	}
	if limits.ContextSource != provider.LimitsEndpoint {
		t.Errorf("source = %q, want the endpoint", limits.ContextSource)
	}
	if limits.MaxOutput <= 0 {
		t.Error("a runtime that states a window but no output cap must still get one from the table")
	}
}

func TestModelLimitsFindsAWindowUnderAnUnknownArchitecture(t *testing.T) {
	// A new architecture naming its key anything else would otherwise return
	// nothing at all, silently.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model_info":{"brandnew.context_length":40960}}`))
	}))
	defer server.Close()
	limits := ModelLimits(t.Context(), appconfig.Provider{Type: "openai-compatible", BaseURL: server.URL + "/v1"}, "m", nil)
	if limits.ContextWindow != 40960 {
		t.Errorf("context window = %d", limits.ContextWindow)
	}
}

func TestModelLimitsFallsBackToTheTableWhenNothingAnswers(t *testing.T) {
	// A closed port, a 404, and a runtime that describes nothing are all the
	// same case: the table is consulted and the result is labelled as published
	// rather than measured.
	limits := ModelLimits(t.Context(), appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1"}, "llama-3.3-70b", nil)
	if limits.ContextSource != provider.LimitsTable || limits.ContextWindow <= 0 {
		t.Errorf("limits = %+v, want a table fallback", limits)
	}
}

func TestModelLimitsPrefersTheCatalogEntryOverANativeRequest(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	catalog := []provider.ModelInfo{{ID: "m", Limits: provider.Limits{ContextWindow: 65536, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsEndpoint}}}
	limits := ModelLimits(t.Context(), appconfig.Provider{Type: "openai-compatible", BaseURL: server.URL + "/v1"}, "m", catalog)
	if limits.ContextWindow != 65536 {
		t.Errorf("context window = %d", limits.ContextWindow)
	}
	if called {
		t.Error("a catalog that already answered must not cost a second request")
	}
}

func TestNativeRootDropsTheOpenAISuffix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://127.0.0.1:11434/v1", "http://127.0.0.1:11434"},
		{"http://127.0.0.1:1234/v1/", "http://127.0.0.1:1234"},
		{"http://host:8000", "http://host:8000"},
		{"", ""},
	} {
		if got := nativeRoot(tc.in); got != tc.want {
			t.Errorf("nativeRoot(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
