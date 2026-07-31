package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// verifyTimeout bounds the proving request. A cold local model loading weights
// off disk is the slow case worth waiting for; a hosted endpoint that has not
// answered in this long has a problem the wizard should report rather than
// keep waiting on.
const verifyTimeout = 90 * time.Second

// verifyMaxTokens is small but not one. A single token is enough to prove the
// route answers, but some models emit a leading reasoning or whitespace token
// and would hit the limit before producing anything, turning a working
// endpoint into an empty reply that reads like a failure.
const verifyMaxTokens = 32

// verifyPrompt is deliberately trivial. This step is proving that the
// configured route answers, not evaluating the model.
const verifyPrompt = "Reply with the single word: ok"

// Verification is the outcome of the one request that decides whether
// anything gets written.
type Verification struct {
	Model string
	// OK means both requests succeeded: the endpoint answers, and it accepts
	// tool definitions. Only then is anything written.
	OK bool
	// ToolsOK records the second request specifically, so a failure can say
	// which of the two steps did not pass.
	ToolsOK bool
	Reply   string
	Usage   provider.Usage
	Elapsed time.Duration
	Err     error
	// Diagnosis is populated on failure and is the reason this step exists.
	Diagnosis Diagnosis
}

// toolProbe is the trivial tool definition sent by the second verification
// request. Nothing needs to call it: what is being tested is whether the
// endpoint accepts a request that carries tools at all.
//
// Requiring an actual tool call would be a worse test. A capable model may
// reasonably answer a trivial prompt with text instead of a call, which would
// fail a working configuration — while the failure that actually blocks a
// session is a hard rejection of the request.
var toolProbe = provider.ToolDefinition{
	Name:        "collomia_setup_probe",
	Description: "A no-op used once during setup to check that this endpoint accepts tool definitions.",
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
}

// Verify proves that the configuration about to be written will actually run a
// session, in two requests through the same adapter a session uses.
//
// This is not the same check as Discover, and one cannot stand in for the
// other. A catalog request and a completion request are different endpoints
// with different permissions: Azure lists models while requests address
// deployments, and Bedrock will happily list a model the account has no access
// to invoke. Discovery proves the host is reachable and tells the user what to
// choose. Only a completion proves the thing about to be written down answers.
//
// The second request is the one that earns this function its name. Collomia is
// a tool-calling agent and cannot run on a model that refuses tool
// definitions — and on a local runtime that is not an edge case, since every
// embedding, vision, and small chat model in the catalog is in that position.
// An earlier version of this sent no tools, on the reasoning that a single
// request isolates a single cause. It does, and it also let `gemma3:270m`
// verify cleanly and then fail the user's first real prompt with
// "does not support tools" — which is exactly the failure this whole package
// exists to move earlier. Two requests keep the cause isolated *and* keep the
// promise: the first says whether the endpoint answers, the second says
// whether it can be used.
func Verify(ctx context.Context, name string, p appconfig.Provider, model string, catalog []provider.ModelInfo) Verification {
	started := time.Now()
	result := Verification{Model: model}

	client, err := provider.New(name, p, model)
	if err != nil {
		result.Err = err
		result.Elapsed = time.Since(started)
		result.Diagnosis = Diagnose(p, model, catalog, err)
		return result
	}

	callCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	request := provider.Request{
		Model:     model,
		Messages:  []provider.Message{{Role: "user", Content: verifyPrompt}},
		MaxTokens: verifyMaxTokens,
	}
	var text strings.Builder
	response, err := client.Chat(callCtx, request, func(delta provider.Delta) {
		text.WriteString(delta.Text)
	})
	result.Elapsed = time.Since(started)
	if err != nil {
		result.Err = err
		result.Diagnosis = Diagnose(p, model, catalog, err)
		return result
	}

	reply := strings.TrimSpace(text.String())
	if reply == "" {
		// A non-streaming adapter returns the whole answer on the response
		// rather than through deltas, so both have to be consulted.
		reply = strings.TrimSpace(response.Content)
	}
	result.Reply, result.Usage = reply, response.Usage

	// An empty 200 is its own failure. Several compatible gateways answer a
	// request for a model they do not have with a well-formed, entirely empty
	// completion, and treating that as success would write a configuration
	// that produces silence at the first real prompt.
	if reply == "" {
		result.Err = errors.New("the endpoint answered successfully but returned no text")
		result.Diagnosis = Diagnosis{
			Summary: "The endpoint answered, but produced no output.",
			Detail:  "A well-formed but empty completion usually means the model name is accepted by the gateway and not actually served behind it.",
			Fixes:   modelFixes(p, model, catalog),
		}
		return result
	}

	// Second request: the same trivial prompt, now carrying a tool definition.
	toolCtx, toolCancel := context.WithTimeout(ctx, verifyTimeout)
	defer toolCancel()
	toolRequest := provider.Request{
		Model:     model,
		Messages:  []provider.Message{{Role: "user", Content: verifyPrompt}},
		MaxTokens: verifyMaxTokens,
		Tools:     []provider.ToolDefinition{toolProbe},
	}
	if _, err := client.Chat(toolCtx, toolRequest, func(provider.Delta) {}); err != nil {
		result.Elapsed = time.Since(started)
		result.Err = err
		result.Diagnosis = diagnoseTools(p, model, catalog, err)
		return result
	}

	result.ToolsOK = true
	result.OK = true
	result.Elapsed = time.Since(started)
	return result
}

// diagnoseTools explains a failure that appeared only once tools were present,
// which is a different problem from an unreachable endpoint and needs a
// different answer: the endpoint is fine and the model is the wrong choice.
func diagnoseTools(p appconfig.Provider, model string, catalog []provider.ModelInfo, err error) Diagnosis {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) && (providerErr.Kind == provider.ErrorInvalidRequest || providerErr.Kind == provider.ErrorNotFound) {
		fixes := []string{"Choose a model that supports tool calling — on Ollama, the instruct and coder families do; embedding, vision, and the smallest chat models generally do not."}
		// Never suggest the model that just failed. Offering it back as an
		// alternative to itself is how a helpful list becomes noise.
		if others := firstN(without(ModelIDs(catalog), model), 8); len(others) > 0 {
			fixes = append(fixes, "This endpoint also reports: "+strings.Join(others, ", ")+".")
		}
		return Diagnosis{
			Summary: "This model answers, but cannot accept tools — Collomia cannot drive it.",
			Detail:  strings.TrimSpace(providerErr.Message),
			Fixes:   fixes,
		}
	}
	base := Diagnose(p, model, catalog, err)
	if base.Summary != "" {
		base.Summary = "The endpoint answered a plain request but failed once tools were included. " + base.Summary
	}
	return base
}

func firstN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

func without(values []string, exclude string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if value != exclude {
			kept = append(kept, value)
		}
	}
	return kept
}

// Diagnosis turns a provider failure into something a user can act on.
//
// This is the wizard's actual product. Moving a failure from the first prompt
// to the setup step is worth nothing on its own — the value is that at the
// setup step the wizard knows the provider, the model, the catalog it just
// read, and the credential source, so it can say which of those is wrong. A
// wizard that reports "request failed: HTTP 404" has only made the same
// failure happen earlier.
type Diagnosis struct {
	// Summary is one sentence naming what went wrong.
	Summary string
	// Detail is what the endpoint itself said, when that adds anything.
	Detail string
	// Fixes are concrete next steps, most likely first.
	Fixes []string
}

// Empty reports whether there is nothing to show.
func (d Diagnosis) Empty() bool {
	return d.Summary == "" && d.Detail == "" && len(d.Fixes) == 0
}

// Diagnose maps a provider error onto a named cause and a fix.
func Diagnose(p appconfig.Provider, model string, catalog []provider.ModelInfo, err error) Diagnosis {
	if err == nil {
		return Diagnosis{}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Diagnosis{
			Summary: "The endpoint did not answer in time.",
			Detail:  fmt.Sprintf("No response within %s.", verifyTimeout),
			Fixes: []string{
				"A local model loading for the first time can exceed this; try again once it is warm.",
				"Check that " + orPlaceholder(p.BaseURL, "the endpoint") + " is reachable from this machine.",
			},
		}
	}
	if errors.Is(err, context.Canceled) {
		return Diagnosis{Summary: "Cancelled."}
	}

	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		return Diagnosis{
			Summary: "The request could not be completed.",
			Detail:  err.Error(),
			Fixes:   []string{"Check that " + orPlaceholder(p.BaseURL, "the endpoint") + " is correct and reachable."},
		}
	}

	detail := strings.TrimSpace(providerErr.Message)
	switch providerErr.Kind {
	case provider.ErrorAuthentication:
		return Diagnosis{
			Summary: "The endpoint rejected the credential.",
			Detail:  detail,
			Fixes:   credentialFixes(p),
		}
	case provider.ErrorPermission:
		return Diagnosis{
			Summary: "The credential is valid, but not allowed to use this model.",
			Detail:  detail,
			Fixes: append([]string{
				"For Bedrock, request model access for " + orPlaceholder(model, "this model") + " in the console for region " + orPlaceholder(p.Region, "your region") + ".",
				"For a hosted API, check the project or organization the key belongs to.",
			}, credentialFixes(p)...),
		}
	case provider.ErrorNotFound:
		return Diagnosis{
			Summary: "The endpoint is reachable, but does not have that model.",
			Detail:  detail,
			Fixes:   modelFixes(p, model, catalog),
		}
	case provider.ErrorRateLimit:
		return Diagnosis{
			Summary: "The endpoint is rate limiting this credential.",
			Detail:  detail,
			Fixes:   []string{"Wait and try again — this says the credential works, not that it is wrong."},
		}
	case provider.ErrorInvalidRequest:
		return Diagnosis{
			Summary: "The endpoint refused the request.",
			Detail:  detail,
			Fixes: []string{
				"If this is an Azure endpoint, the model field wants the deployment name you created, not the model's published name.",
				"Check the API version and the base URL against the provider's documentation.",
			},
		}
	case provider.ErrorTimeout:
		return Diagnosis{
			Summary: "The endpoint timed out.",
			Detail:  detail,
			Fixes:   []string{"Try again; if it persists, the endpoint or the network path is the problem rather than the configuration."},
		}
	case provider.ErrorUnavailable:
		return Diagnosis{
			Summary: "Could not reach the endpoint.",
			Detail:  detail,
			Fixes:   reachabilityFixes(p),
		}
	case provider.ErrorProtocol:
		return Diagnosis{
			Summary: "The endpoint answered, but not in a shape this adapter understands.",
			Detail:  detail,
			Fixes: []string{
				"Check that " + orPlaceholder(p.BaseURL, "the base URL") + " points at the API root, including any /v1 suffix the provider expects.",
				"Check that the provider type matches the API the endpoint actually speaks.",
			},
		}
	}

	if providerErr.StatusCode > 0 {
		return Diagnosis{
			Summary: fmt.Sprintf("The endpoint returned HTTP %d.", providerErr.StatusCode),
			Detail:  detail,
			Fixes:   reachabilityFixes(p),
		}
	}
	return Diagnosis{
		Summary: "The request failed.",
		Detail:  orPlaceholder(detail, err.Error()),
		Fixes:   reachabilityFixes(p),
	}
}

// modelFixes answers a model-not-found by naming what the catalog actually
// held. Printing the list the wizard just read is the whole difference between
// a 404 and an answer.
func modelFixes(p appconfig.Provider, model string, catalog []provider.ModelInfo) []string {
	fixes := make([]string, 0, 3)
	if len(catalog) > 0 {
		ids := ModelIDs(catalog)
		shown := ids
		suffix := ""
		if len(shown) > 8 {
			shown, suffix = shown[:8], fmt.Sprintf(", and %d more", len(ids)-8)
		}
		fixes = append(fixes, "This endpoint reports: "+strings.Join(shown, ", ")+suffix+".")
	}
	if isAzure(p.Type) {
		fixes = append(fixes, "Azure addresses deployments, not models: use the deployment name from your Azure resource.")
	}
	if p.Type == "openai-compatible" && strings.Contains(p.BaseURL, "11434") {
		fixes = append(fixes, "For Ollama, pull it first: ollama pull "+orPlaceholder(model, "<model>")+".")
	}
	return fixes
}

func credentialFixes(p appconfig.Provider) []string {
	fixes := make([]string, 0, 3)
	switch {
	case p.CredentialSource != "":
		fixes = append(fixes, "The credential came from "+p.CredentialSource+"; check that value.")
	case p.APIKeyEnv != "":
		fixes = append(fixes, "The credential came from $"+p.APIKeyEnv+"; check that value.")
	}
	fixes = append(fixes,
		"Confirm the key has not been revoked and belongs to the right organization or project.",
		"A key pasted with surrounding whitespace or quotes is the common cause.",
	)
	return fixes
}

func reachabilityFixes(p appconfig.Provider) []string {
	fixes := []string{"Check that " + orPlaceholder(p.BaseURL, "the endpoint") + " is correct and reachable from this machine."}
	if strings.Contains(p.BaseURL, "127.0.0.1") || strings.Contains(p.BaseURL, "localhost") {
		fixes = append(fixes, "This is a local endpoint: confirm the runtime is actually running and listening on that port.")
	} else {
		fixes = append(fixes, "If this machine uses an HTTP proxy or a corporate TLS interception root, confirm it applies to this host.")
	}
	return fixes
}

func isAzure(providerType string) bool {
	return providerType == "azure-openai" || providerType == "azure-foundry" || providerType == "azure-foundry-anthropic"
}

func orPlaceholder(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
