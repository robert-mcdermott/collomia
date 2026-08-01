// Package setup turns an unconfigured Collomia installation into a verified
// one.
//
// Its whole contract is one sentence: nothing is written until a real request
// to the endpoint being configured has succeeded. Everything else here exists
// to serve that. `collo init` writes a static file, `config.Defaults()`
// returns an assumption, and `collo doctor` inspects configuration and
// environment without making a network request — so before this package, the
// first thing that ever dialed a provider was the user's first prompt, and a
// dead port, a revoked key, a model the endpoint does not have, and a wrong
// context window all failed identically and late.
//
// The UI lives in internal/tui. Everything in this package is offline-testable
// against httptest servers and holds no terminal state.
package setup

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// Candidate is one local model runtime the wizard knows how to look for.
//
// The set is deliberately small and fixed. These are the runtimes whose
// default ports are stable enough that finding something on one is evidence
// rather than a guess; anything else is reached through the manual
// OpenAI-compatible path, which asks for the base URL.
type Candidate struct {
	// Name is what the user sees ("Ollama").
	Name string
	// Key is the provider name written into configuration ("ollama").
	Key string
	// BaseURL is the runtime's documented default endpoint.
	BaseURL string
	// Type is the Collomia provider type that speaks to it.
	Type string
	// Start is the command that starts the runtime, named in the probe result
	// when the port is closed. "Ollama is not running" is only useful next to
	// how to run it.
	Start string
}

// LocalCandidates returns the local runtimes probed on every wizard run.
func LocalCandidates() []Candidate {
	return []Candidate{
		{Name: "Ollama", Key: "ollama", BaseURL: "http://127.0.0.1:11434/v1", Type: "openai-compatible", Start: "ollama serve"},
		{Name: "LM Studio", Key: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Type: "openai-compatible", Start: "start the LM Studio local server"},
		{Name: "vLLM", Key: "vllm", BaseURL: "http://127.0.0.1:8000/v1", Type: "openai-compatible", Start: "vllm serve <model>"},
	}
}

// ProbeState is the outcome of looking for one local runtime.
//
// Three states rather than two: a port that accepts a connection but does not
// answer a catalog request is a different problem from a port with nothing on
// it, and telling a user "not running" when their server is running badly
// sends them to the wrong fix.
type ProbeState string

const (
	// ProbeReady means the endpoint answered a model catalog request.
	ProbeReady ProbeState = "ready"
	// ProbeListening means something accepted the connection but the catalog
	// request failed. Usually a different service on the same port.
	ProbeListening ProbeState = "listening"
	// ProbeAbsent means nothing is listening on the port.
	ProbeAbsent ProbeState = "absent"
)

// Probe is what one candidate answered.
type Probe struct {
	Candidate Candidate
	State     ProbeState
	Models    []provider.ModelInfo
	Err       error
	Elapsed   time.Duration
}

// Detail is the one-line explanation shown beside a probed runtime.
func (p Probe) Detail() string {
	switch p.State {
	case ProbeReady:
		switch len(p.Models) {
		case 0:
			// Reachable with an empty catalog is its own case: the server is
			// fine and has nothing to serve, which no amount of restarting fixes.
			return "running, but no models are installed"
		case 1:
			return "1 model"
		default:
			return itoa(len(p.Models)) + " models"
		}
	case ProbeListening:
		return "something is on the port, but it is not answering as an OpenAI-compatible API"
	default:
		if p.Candidate.Start != "" {
			return "not running — " + p.Candidate.Start
		}
		return "not running"
	}
}

// dialTimeout bounds the connection test. Every candidate is on loopback, so
// this is generous; the point is only to fail fast on a closed port rather
// than wait out a full request timeout three times in a row.
const dialTimeout = 400 * time.Millisecond

// catalogTimeout bounds the model catalog request. A cold Ollama that has to
// page a manifest in from disk is slower than the dial but not slow.
const catalogTimeout = 5 * time.Second

// ProbeLocal looks for every candidate concurrently and returns results in the
// candidates' own order, so the display is stable rather than ordered by
// whichever machine answered first.
func ProbeLocal(ctx context.Context, candidates []Candidate) []Probe {
	results := make([]Probe, len(candidates))
	var wg sync.WaitGroup
	for i, candidate := range candidates {
		wg.Add(1)
		go func(index int, c Candidate) {
			defer wg.Done()
			results[index] = probeOne(ctx, c)
		}(i, candidate)
	}
	wg.Wait()
	return results
}

func probeOne(ctx context.Context, c Candidate) Probe {
	started := time.Now()
	result := Probe{Candidate: c, State: ProbeAbsent}
	address, err := hostPort(c.BaseURL)
	if err != nil {
		result.Err = err
		result.Elapsed = time.Since(started)
		return result
	}
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		result.Err = err
		result.Elapsed = time.Since(started)
		return result
	}
	_ = conn.Close()
	result.State = ProbeListening

	models, err := Discover(ctx, c.Key, appconfig.Provider{Type: c.Type, BaseURL: c.BaseURL})
	if err != nil {
		result.Err = err
		result.Elapsed = time.Since(started)
		return result
	}
	result.State, result.Models = ProbeReady, models
	result.Elapsed = time.Since(started)
	return result
}

// hostPort extracts a dialable address from a base URL, supplying the scheme's
// default port when the URL omits one.
func hostPort(base string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	if parsed.Host == "" {
		return "", errors.New("base URL has no host: " + base)
	}
	if parsed.Port() != "" {
		return parsed.Host, nil
	}
	if parsed.Scheme == "https" {
		return net.JoinHostPort(parsed.Hostname(), "443"), nil
	}
	return net.JoinHostPort(parsed.Hostname(), "80"), nil
}

// Hosted is a hosted provider family the wizard can offer without asking the
// user to know its base URL or its environment variable.
type Hosted struct {
	Name     string
	Key      string
	Type     string
	BaseURL  string
	EnvVar   string
	KeyHint  string
	NeedsKey bool
}

// HostedCandidates returns the hosted families offered by name.
//
// Azure and Bedrock are deliberately absent: neither is configurable from a
// name and a key. Azure needs a resource endpoint, a deployment name, and an
// API version, and Bedrock needs a region and a credential chain — those get
// the manual path, where the wizard can ask for each field and say what it is
// for, rather than a list entry that dead-ends.
func HostedCandidates() []Hosted {
	return []Hosted{
		{Name: "Anthropic", Key: "anthropic", Type: "anthropic", BaseURL: "https://api.anthropic.com", EnvVar: "ANTHROPIC_API_KEY", KeyHint: "sk-ant-…", NeedsKey: true},
		{Name: "OpenAI", Key: "openai", Type: "openai", BaseURL: "https://api.openai.com/v1", EnvVar: "OPENAI_API_KEY", KeyHint: "sk-…", NeedsKey: true},
		{Name: "OpenRouter", Key: "openrouter", Type: "openai", BaseURL: "https://openrouter.ai/api/v1", EnvVar: "OPENROUTER_API_KEY", KeyHint: "sk-or-…", NeedsKey: true},
	}
}

// EnvKey reports a hosted family's API key if the environment already carries
// it, along with the variable it came from.
//
// This is the best outcome the wizard has: a credential it can point at
// without ever handling, storing, or writing the value. It is checked before
// anything is asked for.
func (h Hosted) EnvKey() (key, variable string, ok bool) {
	for _, name := range h.envVars() {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value, name, true
		}
	}
	return "", "", false
}

func (h Hosted) envVars() []string {
	names := []string{h.EnvVar}
	if implicit := appconfig.ImplicitCredentialEnv(h.Type); implicit != "" && implicit != h.EnvVar {
		names = append(names, implicit)
	}
	return names
}

// Discover asks a provider for its model catalog.
//
// It mirrors app.Runtime.ListModels, which cannot be reused directly: that one
// resolves a provider out of a loaded configuration, and the whole point here
// is to interrogate a provider that is not in any configuration yet.
func Discover(ctx context.Context, name string, p appconfig.Provider) ([]provider.ModelInfo, error) {
	capabilities, err := provider.CapabilitiesFor(p.Type, p.Model, p.Context)
	if err != nil {
		return nil, err
	}
	if capabilities.ModelDiscovery == provider.CapabilityUnsupported {
		return nil, ErrNoDiscovery
	}
	client, err := provider.New(name, p, p.Model)
	if err != nil {
		return nil, err
	}
	lister, ok := client.(provider.ModelLister)
	if !ok {
		return nil, ErrNoDiscovery
	}
	listCtx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	models, err := lister.ListModels(listCtx)
	if err != nil {
		return nil, err
	}
	// Enrich before declaring capabilities, so a per-model window reaches the
	// declaration rather than arriving after it.
	models = CatalogLimits(ctx, p, models)
	for i := range models {
		// The window declared for a model is that model's own, not the one
		// being assembled for the provider. Passing p.Context here — which is
		// what this did — annotated every entry in the catalog with the same
		// number, so a picker that looked like per-model discovery was showing
		// one constant repeated down the list.
		models[i].Capabilities, err = provider.CapabilitiesFor(p.Type, models[i].ID, models[i].Limits.ContextWindow)
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// ErrNoDiscovery marks a provider whose adapter exposes no catalog. It is not
// a failure: Bedrock has no model list endpoint, and the wizard asks for a
// model name instead of reporting a problem that does not exist.
var ErrNoDiscovery = errors.New("this provider does not publish a model catalog")

// ModelIDs is the plain list of catalog identifiers, for pickers and for the
// "it has these instead" line in a not-found diagnosis.
func ModelIDs(models []provider.ModelInfo) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// itoa avoids pulling strconv in for two call sites in display code.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
