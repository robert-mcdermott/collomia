package setup

import (
	"os"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// Live limit discovery against local runtimes that are actually running.
//
// The offline suite proves the parsers handle the documents this package
// expects. It cannot prove those are the documents the runtimes send, and that
// is the half that breaks: a native API is not a stable contract, and a key
// renamed upstream would silently return the wave to writing assumptions while
// every unit test still passed.
//
// Skipped unless explicitly requested, because it needs a runtime on a known
// port:
//
//	COLLO_LIVE_LIMIT_TESTS=1 go test ./internal/setup -run Live -v
const liveLimitTestsEnv = "COLLO_LIVE_LIMIT_TESTS"

func requireLiveLimitTests(t *testing.T) {
	t.Helper()
	if os.Getenv(liveLimitTestsEnv) != "1" {
		t.Skip("set COLLO_LIVE_LIMIT_TESTS=1 to run limit discovery against local runtimes")
	}
}

func TestLiveLocalRuntimesReportTheirOwnLimits(t *testing.T) {
	requireLiveLimitTests(t)

	for _, candidate := range LocalCandidates() {
		t.Run(candidate.Key, func(t *testing.T) {
			p := appconfig.Provider{Type: candidate.Type, BaseURL: candidate.BaseURL}
			catalog, err := Discover(t.Context(), candidate.Key, p)
			if err != nil {
				t.Skipf("%s is not answering on %s: %v", candidate.Name, candidate.BaseURL, err)
			}
			if len(catalog) == 0 {
				t.Skipf("%s is running with no models installed", candidate.Name)
			}

			// Resolving the first model must produce both limits, whatever the
			// runtime publishes: the table backstops a runtime that describes
			// nothing, so the only unacceptable outcome is a zero.
			model := catalog[0].ID
			limits := ModelLimits(t.Context(), p, model, catalog)
			t.Logf("%s: %d models; %s -> %s", candidate.Name, len(catalog), model, limits.Describe())
			result := Build(candidate.Key, p, model, CredentialNone, "", "", limits)
			if result.Provider.Context <= 0 || result.Provider.MaxTokens <= 0 {
				t.Errorf("both limits must always be written, got context %d max_tokens %d",
					result.Provider.Context, result.Provider.MaxTokens)
			}
			if result.Provider.MaxTokens >= result.Provider.Context {
				t.Errorf("max_tokens %d must stay below the context window %d",
					result.Provider.MaxTokens, result.Provider.Context)
			}
		})
	}
}

func TestLiveOllamaDescribesAModel(t *testing.T) {
	requireLiveLimitTests(t)

	p := appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	catalog, err := Discover(t.Context(), "ollama", p)
	if err != nil || len(catalog) == 0 {
		t.Skipf("Ollama is not answering with a catalog: %v", err)
	}
	// The per-model native request is the one path the OpenAI-compatible route
	// cannot cover, and the architecture-qualified key is the part most likely
	// to change without notice.
	found := ""
	for _, entry := range catalog {
		if window := ollamaContextLength(t.Context(), p.BaseURL, entry.ID); window > 0 {
			found = entry.ID
			t.Logf("ollama /api/show: %s reports a %d-token window", entry.ID, window)
			break
		}
	}
	if found == "" {
		t.Errorf("no model in a %d-entry Ollama catalog reported a context length; the model_info key may have changed", len(catalog))
	}
}

func TestLiveCatalogAnnotationIsPerModel(t *testing.T) {
	requireLiveLimitTests(t)

	// The defect this wave fixed showed up here: every entry carried the same
	// number, because the annotation came from the provider being assembled
	// rather than from the model. Distinct windows across a mixed catalog are
	// the observable signature of a real per-model answer.
	p := appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:1234/v1"}
	catalog, err := Discover(t.Context(), "lmstudio", p)
	if err != nil || len(catalog) < 2 {
		t.Skipf("LM Studio is not answering with a catalog of at least two models: %v", err)
	}
	windows := map[int]int{}
	annotated := 0
	for _, entry := range catalog {
		if entry.Limits.ContextWindow > 0 {
			annotated++
			windows[entry.Limits.ContextWindow]++
		}
		if entry.Limits.ContextWindow > 0 && entry.Limits.ContextSource != provider.LimitsEndpoint {
			t.Errorf("%s: a window read from the runtime must be marked as endpoint-reported, got %q", entry.ID, entry.Limits.ContextSource)
		}
	}
	t.Logf("LM Studio: %d of %d models annotated across %d distinct windows", annotated, len(catalog), len(windows))
	if annotated == 0 {
		t.Error("LM Studio's native catalog reported no windows at all; /api/v0/models may have changed")
	}
}
