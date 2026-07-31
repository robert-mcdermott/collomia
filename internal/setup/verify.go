package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

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

// SanitizeSecret cleans a pasted credential.
//
// The diagnosis text has always said that a key pasted with surrounding
// whitespace or quotes is the common cause; handling it is better than warning
// about it. Every credential Collomia accepts is base64 or an alphanumeric
// token, so no interior whitespace is ever meaningful — and a key copied out of
// a console or a wrapped terminal line arrives with newlines in the middle of
// it, which is invisible in a field that does not echo.
func SanitizeSecret(value string) string {
	var cleaned strings.Builder
	for _, r := range value {
		if unicode.IsSpace(r) {
			continue
		}
		cleaned.WriteRune(r)
	}
	trimmed := cleaned.String()
	for _, quote := range []string{`"`, `'`, "`"} {
		if len(trimmed) >= 2 && strings.HasPrefix(trimmed, quote) && strings.HasSuffix(trimmed, quote) {
			trimmed = trimmed[1 : len(trimmed)-1]
		}
	}
	return trimmed
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
		if usesAWSChain(p) {
			// Distinguished from a rejected key: an unresolved chain never
			// reached the endpoint at all, so saying the endpoint rejected
			// something would be wrong about where the failure happened.
			return Diagnosis{
				Summary: "No AWS credentials could be resolved for SigV4 signing.",
				Detail:  detail,
				Fixes:   awsChainFixes(p),
			}
		}
		return Diagnosis{
			Summary: "The endpoint rejected the credential.",
			Detail:  detail,
			Fixes:   credentialFixes(p),
		}
	case provider.ErrorPermission:
		// A 403 has two quite different causes, and guessing the wrong one
		// sends the user to the wrong console. When the endpoint's own message
		// is about the credential itself, saying "the credential is valid"
		// above that message is not merely unhelpful — it contradicts the
		// evidence printed directly beneath it.
		if mentionsCredentialShape(detail) {
			return Diagnosis{
				Summary: "The endpoint rejected the credential itself, not your access to the model.",
				Detail:  detail,
				Fixes:   append(credentialShapeFixes(p), credentialFixes(p)...),
			}
		}
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

// mentionsCredentialShape reports whether an endpoint's own message is about
// the credential rather than about entitlement to a model.
func mentionsCredentialShape(detail string) bool {
	lowered := strings.ToLower(detail)
	for _, marker := range []string{"api key", "apikey", "api-key", "token", "signature", "credential"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// credentialShapeFixes answers a credential the endpoint could not parse.
//
// Truncation leads, because it is invisible: the field does not echo, so a key
// that was cut off looks exactly like one that was not, and the resulting AWS
// message ("Missing required parameters in the API Key") names the key without
// suggesting that it is incomplete.
func credentialShapeFixes(p appconfig.Provider) []string {
	fixes := []string{
		"The key may be incomplete. Re-copy it in full and paste it again — a partial key produces this rather than a plain rejection.",
	}
	if p.Type == "bedrock" {
		fixes = append(fixes,
			"A Bedrock short-term API key is base64-encoded session credentials and can run to well over a thousand characters; confirm the whole value arrived.",
			"Bedrock API keys are issued per region. A key generated for another region fails here even when model access is granted in "+orPlaceholder(p.Region, "this region")+".",
			"As an alternative, export AWS_BEARER_TOKEN_BEDROCK and run `collo setup` again — it uses the exported value and never passes through a text field.",
		)
	}
	return fixes
}

// usesAWSChain reports whether this provider authenticates through the AWS
// credential chain rather than a pasted key.
//
// It matters for diagnosis: the two Bedrock credential families fail for
// completely different reasons, and advice about a revoked API key is useless
// to someone whose real problem is an unset AWS_ACCESS_KEY_ID or an expired SSO
// session. `auto` is included, because with no token present it resolves to
// SigV4 — so the user never chose the word "sigv4" and would not connect the
// failure to it.
func usesAWSChain(p appconfig.Provider) bool {
	if p.Type != "bedrock" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(p.Auth)) {
	case "sigv4":
		return true
	case "bearer":
		return false
	}
	return strings.TrimSpace(p.APIKey) == "" && strings.TrimSpace(p.APIKeyEnv) == ""
}

// awsChainFixes answers a SigV4 failure by naming the credential sources the
// AWS SDK actually consults, in the order it consults them.
func awsChainFixes(p appconfig.Provider) []string {
	fixes := []string{
		"SigV4 signs with ordinary IAM credentials; Collomia does not hold them, the AWS SDK resolves them.",
		"Export AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, adding AWS_SESSION_TOKEN if they are temporary credentials.",
	}
	if profile := strings.TrimSpace(p.Profile); profile != "" {
		fixes = append(fixes, "This provider names profile "+profile+"; confirm it exists in ~/.aws/credentials or ~/.aws/config.")
	} else {
		fixes = append(fixes, "Or configure a profile with `aws configure` and name it in the profile field.")
	}
	fixes = append(fixes,
		"For IAM Identity Center, an expired session is the usual cause — run `aws sso login` and try again.",
		"`aws sts get-caller-identity` confirms whether the chain resolves outside Collomia.",
		"To use a Bedrock API key instead, choose bearer authentication.",
	)
	return fixes
}

func credentialFixes(p appconfig.Provider) []string {
	// A provider signing through the AWS chain has no key to check, so the
	// ordinary credential advice would send the user looking for one.
	if usesAWSChain(p) {
		return awsChainFixes(p)
	}
	fixes := make([]string, 0, 3)
	switch {
	case p.CredentialSource != "":
		fixes = append(fixes, "The credential came from "+p.CredentialSource+"; check that value.")
	case p.APIKeyEnv != "":
		fixes = append(fixes, "The credential came from $"+p.APIKeyEnv+"; check that value.")
	}
	// Deliberately no longer advises checking for surrounding whitespace or
	// quotes: SanitizeSecret removes those before the request is made, and
	// telling someone to check what the tool already handled sends them to
	// look at the one thing that cannot be wrong.
	fixes = append(fixes, "Confirm the key has not been revoked and belongs to the right organization or project.")
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
