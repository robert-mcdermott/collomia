package setup

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func ollamaShowServer(t *testing.T, show string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(show))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestModelReasoningReadsOllamasThinkingMetadata(t *testing.T) {
	// Ollama applies an advertised name exactly and silently uses the model
	// default for anything else, so what the model advertises is the only
	// reliable statement of what will take effect.
	cases := []struct {
		name, show string
		levels     []string
		def        string
		known      bool
	}{
		{"named levels", `{"capabilities":["completion","thinking"],"thinking":{"values":["low","high","max"],"default":"max"}}`, []string{"low", "high", "max"}, "max", true},
		{"on/off only", `{"capabilities":["completion","thinking"],"thinking":{"values":[false,true],"default":true}}`, []string{"none"}, "", true},
		{"no thinking", `{"capabilities":["completion"],"thinking":{"values":[false],"default":false}}`, nil, "", true},
		{"capability absent", `{"capabilities":["completion","tools"]}`, nil, "", true},
		{"no metadata", `{"model_info":{}}`, nil, "", false},
	}
	for _, c := range cases {
		server := ollamaShowServer(t, c.show)
		support := ModelReasoning(t.Context(), appconfig.Provider{Type: "openai-compatible", BaseURL: server.URL + "/v1"}, "some-local-model", nil)
		if support.Known() != c.known || !reflect.DeepEqual(nilIfEmpty(support.Levels), c.levels) || support.Default != c.def {
			t.Errorf("%s: %+v", c.name, support)
		}
	}
}

func TestModelReasoningPrefersTheCatalog(t *testing.T) {
	catalog := []provider.ModelInfo{{ID: "claude-x", Reasoning: provider.ReasoningSupport{Levels: []string{"low"}, Source: provider.LimitsEndpoint}}}
	support := ModelReasoning(t.Context(), appconfig.Provider{Type: "anthropic"}, "claude-x", catalog)
	if !reflect.DeepEqual(support.Levels, []string{"low"}) {
		t.Errorf("support = %+v", support)
	}
}

func TestEffortChoicesKeepReasoningOptIn(t *testing.T) {
	known := provider.ReasoningSupport{Levels: []string{"low", "medium", "high"}, Default: "medium", Source: provider.LimitsTable}
	choices := EffortChoices(known, "")
	if choices[0].Level != "" || !strings.Contains(choices[0].Detail, "medium") {
		t.Errorf("the first row must send nothing and say what the model does then, got %+v", choices[0])
	}
	if len(choices) != 4 || choices[2].Detail != "the model's own default" {
		t.Errorf("choices = %+v", choices)
	}
	if first := EffortChoices(known, "high")[0]; first.Label != "Provider setting" || !strings.Contains(first.Detail, "high") {
		t.Errorf("with a provider-level effort the first row inherits it and says so, got %+v", first)
	}
	unknown := EffortChoices(provider.ReasoningSupport{}, "")
	if len(unknown) != len(provider.EffortLevels)+1 || !strings.Contains(unknown[1].Detail, "untested") || !strings.Contains(unknown[2].Detail, "untested") {
		t.Errorf("an unknown model is offered every level, marked untested: %+v", unknown)
	}
	if Offered(provider.ReasoningSupport{Source: provider.LimitsEndpoint}) {
		t.Error("a model known to have no effort control must not be asked about it")
	}
}

func TestSetupWritesEffortForTheChosenModelOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{"providers": {"p": {"type": "openai-compatible", "base_url": "http://p.test/v1", "model": "m",
	  "reasoning": {"effort": "high"}, "models": {"other": {"reasoning": {"effort": "low"}}}}}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	base := Build("p", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://p.test/v1"}, "m", CredentialNone, "", "", provider.Limits{})
	if err := Apply(path, base.WithEffort("max")); err != nil {
		t.Fatal(err)
	}
	written := readProvider(t, path, "p")
	models := written["models"].(map[string]any)
	if !reflect.DeepEqual(models["m"], map[string]any{"reasoning": map[string]any{"effort": "max"}}) {
		t.Errorf("m = %v", models["m"])
	}
	if !reflect.DeepEqual(models["other"], map[string]any{"reasoning": map[string]any{"effort": "low"}}) {
		t.Error("another model's entry is not setup's to change")
	}
	if !reflect.DeepEqual(written["reasoning"], map[string]any{"effort": "high"}) {
		t.Error("the provider-level effort is the user's and must be untouched")
	}

	// Choosing the default removes the model's own effort again.
	if err := Apply(path, base.WithEffort("")); err != nil {
		t.Fatal(err)
	}
	models = readProvider(t, path, "p")["models"].(map[string]any)
	if _, ok := models["m"]; ok {
		t.Errorf("the default must remove the model's own effort, got %v", models["m"])
	}

	// A brand-new provider gets the effort in its own model entry.
	fresh := filepath.Join(t.TempDir(), "config.json")
	if err := Apply(fresh, base.WithEffort("low")); err != nil {
		t.Fatal(err)
	}
	if got := readProvider(t, fresh, "p")["models"]; !reflect.DeepEqual(got, map[string]any{"m": map[string]any{"reasoning": map[string]any{"effort": "low"}}}) {
		t.Errorf("new provider models = %v", got)
	}
}

func TestVerifyEffortReportsARejectedLevel(t *testing.T) {
	// Adapters recover from a refused effort by retrying without it, which
	// keeps a session alive but would let setup save a level that is dropped
	// on every request. The recovery warning is the signal.
	var sent []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		_ = json.Unmarshal(body, &request)
		effort, _ := request["reasoning_effort"].(string)
		sent = append(sent, effort)
		if effort == "xhigh" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value: 'reasoning_effort' does not support 'xhigh' with this model.","param":"reasoning_effort","code":"unsupported_value"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	p := appconfig.Provider{Type: "openai-compatible", BaseURL: server.URL, MaxTokens: 1024}

	if accepted, detail := VerifyEffort(t.Context(), "p", p, "m", "xhigh"); accepted || !strings.Contains(detail, "reasoning effort") {
		t.Errorf("a refused level must be reported, got accepted=%v detail=%q (sent %v)", accepted, detail, sent)
	}
	if accepted, detail := VerifyEffort(t.Context(), "p", p, "m", "high"); !accepted {
		t.Errorf("an accepted level must pass, got %q", detail)
	}
}

func nilIfEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}
