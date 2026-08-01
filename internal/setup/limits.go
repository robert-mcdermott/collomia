package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// limitsClient is deliberately not http.DefaultClient. These requests go to a
// local runtime, and an inherited HTTP_PROXY meant for the corporate network
// would route a request for 127.0.0.1 through a proxy that cannot answer it —
// turning a working local probe into a silent absence of limits. The web tools
// drop inherited proxy variables for a stronger reason; this is the same
// reasoning applied where the cost of being wrong is only a lost annotation.
var limitsClient = &http.Client{Transport: &http.Transport{Proxy: nil}}

// limitsTimeout bounds a native limits request.
//
// Every one of these is optional enrichment on top of a catalog that already
// answered, so the deadline is short on purpose: a runtime that is slow to
// describe a model must delay the wizard by a moment, not by a request timeout.
const limitsTimeout = 3 * time.Second

// CatalogLimits annotates a discovered catalog with each model's own limits,
// where the runtime publishes them somewhere other than the OpenAI-compatible
// route.
//
// LM Studio is the case this exists for: its `/v1/models` returns nothing but
// ids, while its native `/api/v0/models` returns `max_context_length` and the
// `loaded_context_length` the server is actually serving — one request for the
// whole catalog, which is what makes annotating a picker affordable.
//
// Ollama is deliberately not handled here. Its native description endpoint
// answers one model per request, so annotating a fifty-model catalog would mean
// fifty requests before the list could be drawn; it is resolved for the chosen
// model instead, in ModelLimits.
func CatalogLimits(ctx context.Context, p appconfig.Provider, models []provider.ModelInfo) []provider.ModelInfo {
	if len(models) == 0 || !nativeProbeApplies(p) {
		return models
	}
	windows := lmStudioContextLengths(ctx, p.BaseURL)
	if len(windows) == 0 {
		return models
	}
	annotated := make([]provider.ModelInfo, len(models))
	copy(annotated, models)
	for i := range annotated {
		// Never overwrite what the catalog itself reported: this is a fallback
		// for endpoints that published nothing, not a second opinion.
		if annotated[i].Limits.ContextWindow > 0 {
			continue
		}
		if window, ok := windows[annotated[i].ID]; ok && window > 0 {
			annotated[i].Limits.ContextWindow = window
			annotated[i].Limits.ContextSource = provider.LimitsEndpoint
		}
	}
	return annotated
}

// ModelLimits resolves the limits for one chosen model, in the order the
// sources deserve: what the catalog already reported, then what the runtime
// answers when asked about that model specifically, then the published-limits
// table, then nothing.
//
// The per-model native request happens here rather than during discovery
// because it costs one round trip per model, and by this point exactly one
// model has been chosen.
func ModelLimits(ctx context.Context, p appconfig.Provider, model string, catalog []provider.ModelInfo) provider.Limits {
	reported := provider.Limits{}
	for _, entry := range catalog {
		if entry.ID == model {
			reported = entry.Limits
			break
		}
	}
	if reported.ContextWindow <= 0 && nativeProbeApplies(p) {
		if window := ollamaContextLength(ctx, p.BaseURL, model); window > 0 {
			reported.ContextWindow = window
			reported.ContextSource = provider.LimitsEndpoint
		}
	}
	return provider.ResolveLimits(model, reported)
}

// nativeProbeApplies keeps the native requests to the provider type that can
// plausibly be a local runtime.
//
// An OpenAI-compatible base URL is the only shape Ollama, LM Studio, and vLLM
// are configured as, and the probes below are two requests to paths outside the
// OpenAI surface. Sending those at api.openai.com would be two guaranteed 404s
// on every setup run; withholding them from a runtime on a LAN address because
// it is not loopback would withhold the feature from the people most likely to
// need it.
func nativeProbeApplies(p appconfig.Provider) bool {
	return p.Type == "openai-compatible" && strings.TrimSpace(p.BaseURL) != ""
}

// nativeRoot converts an OpenAI-compatible base URL into the server root the
// native APIs hang off, by dropping the trailing `/v1`.
func nativeRoot(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return strings.TrimSuffix(trimmed, "/v1")
}

// lmStudioContextLengths reads LM Studio's native catalog.
//
// `loaded_context_length` wins over `max_context_length` where both are
// present: a model loaded with a 8k window is serving 8k whatever its weights
// support, and writing the larger number down would disable compaction exactly
// where it is needed soonest.
func lmStudioContextLengths(ctx context.Context, baseURL string) map[string]int {
	root := nativeRoot(baseURL)
	if root == "" {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, limitsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, root+"/api/v0/models", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")
	resp, err := limitsClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var payload struct {
		Data []struct {
			ID                  string `json:"id"`
			MaxContextLength    int    `json:"max_context_length"`
			LoadedContextLength int    `json:"loaded_context_length"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil
	}
	windows := make(map[string]int, len(payload.Data))
	for _, entry := range payload.Data {
		switch {
		case entry.LoadedContextLength > 0:
			windows[entry.ID] = entry.LoadedContextLength
		case entry.MaxContextLength > 0:
			windows[entry.ID] = entry.MaxContextLength
		}
	}
	return windows
}

// ollamaContextLength asks Ollama to describe one model.
//
// The window lives under an architecture-qualified key in `model_info` —
// `qwen3.context_length`, `llama.context_length`, and so on — so the
// architecture has to be read from the same document rather than assumed, and
// the fallback scan exists because a new architecture naming its key anything
// else would otherwise silently return nothing.
func ollamaContextLength(ctx context.Context, baseURL, model string) int {
	root := nativeRoot(baseURL)
	if root == "" || strings.TrimSpace(model) == "" {
		return 0
	}
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return 0
	}
	callCtx, cancel := context.WithTimeout(ctx, limitsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, root+"/api/show", strings.NewReader(string(body)))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := limitsClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var payload struct {
		ModelInfo map[string]json.RawMessage `json:"model_info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0
	}
	architecture := ""
	if raw, ok := payload.ModelInfo["general.architecture"]; ok {
		_ = json.Unmarshal(raw, &architecture)
	}
	if architecture != "" {
		if window := intFrom(payload.ModelInfo[architecture+".context_length"]); window > 0 {
			return window
		}
	}
	for key, raw := range payload.ModelInfo {
		if strings.HasSuffix(key, ".context_length") {
			if window := intFrom(raw); window > 0 {
				return window
			}
		}
	}
	return 0
}

func intFrom(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0
	}
	return value
}
