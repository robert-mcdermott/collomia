package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/credstore"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// CredentialPlan is how a verified credential will be reachable after the
// wizard exits.
//
// The wizard holds a key in memory long enough to prove the endpoint answers,
// and then must put it somewhere it can be found again. What it must never do
// is write the value into the configuration file — that is the rule `collo
// auth` was built on, and a setup wizard is exactly the place it would be
// quietly broken for convenience.
type CredentialPlan string

const (
	// CredentialNone is a local endpoint that needs no credential.
	CredentialNone CredentialPlan = "none"
	// CredentialEnv points api_key_env at a variable the environment already
	// exports. The best outcome: the wizard records a name, never a value.
	CredentialEnv CredentialPlan = "env"
	// CredentialStore puts the value in the OS credential manager.
	CredentialStore CredentialPlan = "store"
	// CredentialManual records api_key_env for a variable the user has yet to
	// export. Used where there is no credential store, so the alternative
	// would be a secret in a file.
	CredentialManual CredentialPlan = "manual"
	// CredentialKeep leaves an already-working arrangement exactly as it is.
	//
	// It exists for `collo setup --provider`, which re-verifies a provider that
	// already authenticates. Without it the only way to express "this provider
	// already has a credential" would be CredentialStore with no secret to
	// store, which would overwrite the stored key with an empty string — a
	// re-verification that destroys the credential it just used.
	CredentialKeep CredentialPlan = "keep"
)

// Result is a fully verified provider, ready to be written down.
type Result struct {
	// Name is the provider key in configuration.
	Name string
	// Provider carries only fields the wizard verified or read from the
	// capability registry. It never carries APIKey.
	Provider appconfig.Provider
	// Model is the verified model.
	Model string
	// Credential says how the key will be found at run time.
	Credential CredentialPlan
	// EnvVar names the variable for the env and manual plans.
	EnvVar string
	// Secret is held only between verification and Apply, and only for the
	// store plan. It is never serialized.
	Secret string `json:"-"`
	// MakeDefault promotes this provider to default_provider/default_model.
	MakeDefault bool
	// Limits are the limits that were written down, carrying the source that
	// established them so the confirmation can distinguish a number the
	// endpoint stated from one this build assumed.
	Limits provider.Limits
	// ContextAssumed marks a context window nobody could establish, so the
	// confirmation can say so rather than presenting a guess as a measurement.
	ContextAssumed bool
}

// assumedContextWindow and assumedMaxOutput are written when neither the
// endpoint nor the published-limits table establishes anything.
//
// Leaving either field out would be the more honest-looking choice and is the
// wrong one, in two different ways. A zero window makes Agent.shouldCompact
// return false for the life of the session, so automatic compaction never runs
// and a long session ends at a provider context-length error with no recovery.
// An absent max_tokens is normalized to 8192 at load, which silently truncates
// every long answer — the same number, but reached without the user ever
// seeing it or knowing there was a field to change. A stated assumption the
// user can read and edit beats a silent capability loss.
//
// Both values match what `collo init` has always written, so this is the
// existing default made visible rather than a new number introduced here.
const (
	assumedContextWindow = 32768
	assumedMaxOutput     = 8192
)

// Build assembles a Result from a verified selection, writing both token
// limits from the best source that established them.
//
// Every earlier version of this wrote 32768 for the context window of every
// locally discovered model and no max_tokens at all — a guess about someone
// else's hardware, and a field left to a default nobody could see. The order
// here is fixed and stated on the confirmation screen: what the endpoint
// published about this model, then the published-limits table, then an
// assumption that is labelled as one.
func Build(name string, p appconfig.Provider, model string, credential CredentialPlan, envVar, secret string, limits provider.Limits) Result {
	written := appconfig.Provider{
		Type:    p.Type,
		BaseURL: p.BaseURL,
		Model:   model,
		// Azure and Bedrock identity fields are part of what was verified, so
		// they travel with it.
		Region:             p.Region,
		Profile:            p.Profile,
		Deployment:         p.Deployment,
		APIVersion:         p.APIVersion,
		Auth:               p.Auth,
		EntraScope:         p.EntraScope,
		EntraTenantID:      p.EntraTenantID,
		EntraAuthorityHost: p.EntraAuthorityHost,
	}
	// Resolving again here rather than trusting the caller is deliberate: Build
	// is the last gate before a file is written, and a caller that skipped
	// resolution must not be the reason a provider is recorded with no limits
	// at all.
	resolved := limits
	if !resolved.Known() {
		resolved = provider.ResolveLimits(model, provider.Limits{})
	}
	switch {
	case resolved.ContextWindow > 0:
	case p.Context > 0:
		// A window the user typed into the manual form is theirs, and outranks
		// an assumption even though it outranks nothing else.
		resolved.ContextWindow, resolved.ContextSource = p.Context, provider.LimitsConfigured
	default:
		resolved.ContextWindow, resolved.ContextSource = assumedContextWindow, provider.LimitsAssumed
	}
	if resolved.MaxOutput <= 0 {
		resolved.MaxOutput, resolved.OutputSource = assumedMaxOutput, provider.LimitsAssumed
	}
	// An output cap at or above the window is a configuration the provider will
	// reject outright, and the table is deliberately conservative rather than
	// coordinated, so the two can meet on an unusual model. Clamping here keeps
	// the wizard from writing a file that `collo config validate` would refuse
	// — and the number that results was established by nobody, so it says so.
	if resolved.MaxOutput >= resolved.ContextWindow {
		resolved.MaxOutput, resolved.OutputSource = resolved.ContextWindow/2, provider.LimitsAssumed
	}
	written.Context, written.MaxTokens = resolved.ContextWindow, resolved.MaxOutput

	if credential == CredentialEnv || credential == CredentialManual {
		written.APIKeyEnv = envVar
	}
	if credential == CredentialKeep {
		// Carry the existing arrangement forward verbatim. Dropping api_key_env
		// here would turn a re-verification into a provider that no longer
		// knows where its credential lives.
		written.APIKeyEnv = p.APIKeyEnv
	}
	return Result{
		Name:           name,
		Provider:       written,
		Model:          model,
		Credential:     credential,
		EnvVar:         envVar,
		Secret:         secret,
		Limits:         resolved,
		ContextAssumed: resolved.ContextSource == provider.LimitsAssumed,
	}
}

// ErrSecretInConfig guards the one rule this package cannot get wrong.
var ErrSecretInConfig = errors.New("setup refuses to write an API key into a configuration file")

// Apply stores the credential where the plan says, then merges the provider
// into the configuration at path.
//
// Order matters: the credential is stored first. A configuration naming a
// credential that was never stored sends the user to a provider that cannot
// authenticate, while a stored credential with no configuration naming it is
// inert and harmless.
func Apply(path string, result Result) error {
	if strings.TrimSpace(result.Provider.APIKey) != "" {
		return ErrSecretInConfig
	}
	if result.Credential == CredentialStore {
		if !credstore.Available() {
			return fmt.Errorf("no credential store on this platform; export %s instead", orPlaceholder(result.EnvVar, "an API key variable"))
		}
		if err := credstore.Set(result.Name, result.Secret); err != nil {
			return fmt.Errorf("store credential for %s: %w", result.Name, err)
		}
	}
	return mergeIntoFile(path, result)
}

// mergeIntoFile adds the provider to an existing configuration without
// disturbing anything else in it.
//
// It edits the decoded document rather than re-serializing a typed Config,
// because a typed round trip would rewrite every field the user had left
// unset to its zero value and silently convert their sparse file into an
// exhaustive one. Settings this build does not know about survive untouched
// for the same reason.
func mergeIntoFile(path string, result Result) error {
	document := map[string]json.RawMessage{}
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(strings.TrimSpace(string(existing))) > 0 {
			if err := json.Unmarshal(existing, &document); err != nil {
				return fmt.Errorf("%s is not valid JSON; fix or move it before running setup: %w", path, err)
			}
		}
	case errors.Is(err, os.ErrNotExist):
		// A fresh file is the common case on a first run.
	default:
		return err
	}

	if _, ok := document["schema_version"]; !ok {
		version, err := json.Marshal(appconfig.CurrentSchemaVersion)
		if err != nil {
			return err
		}
		document["schema_version"] = version
	}
	// Point the file at its schema, but only when it does not already say. A
	// user who pointed theirs at a shared URL made that choice deliberately,
	// and quietly redirecting it to a local file would be setup reaching past
	// the provider block it was asked to write.
	if _, ok := document["$schema"]; !ok {
		reference, err := json.Marshal(appconfig.SchemaReference)
		if err != nil {
			return err
		}
		document["$schema"] = reference
	}

	providers := map[string]json.RawMessage{}
	if raw, ok := document["providers"]; ok {
		if err := json.Unmarshal(raw, &providers); err != nil {
			return fmt.Errorf("providers in %s is not an object: %w", path, err)
		}
	}
	encoded, err := json.Marshal(result.Provider)
	if err != nil {
		return err
	}
	providers[result.Name] = encoded
	if document["providers"], err = json.Marshal(providers); err != nil {
		return err
	}

	if result.MakeDefault {
		if document["default_provider"], err = json.Marshal(result.Name); err != nil {
			return err
		}
		if document["default_model"], err = json.Marshal(result.Model); err != nil {
			return err
		}
	}

	data, err := marshalStable(document)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	// Refresh the sibling schema on every run, not only when creating the
	// file. Setup is the one command that is always executed by the binary
	// whose fields the schema describes, so it is the cheapest place to keep a
	// schema written by an older build from going stale.
	_, err = appconfig.WriteSchema(path)
	return err
}

// marshalStable renders the document with the keys a reader expects to find
// first actually first. encoding/json sorts map keys alphabetically, which
// would bury default_provider under agents and put schema_version near the
// end of the file.
func marshalStable(document map[string]json.RawMessage) ([]byte, error) {
	lead := []string{"$schema", "schema_version", "default_provider", "default_model", "providers"}
	ordered := make([]string, 0, len(document))
	seen := map[string]bool{}
	for _, key := range lead {
		if _, ok := document[key]; ok {
			ordered, seen[key] = append(ordered, key), true
		}
	}
	rest := make([]string, 0, len(document))
	for key := range document {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sortStrings(rest)
	ordered = append(ordered, rest...)

	var out strings.Builder
	out.WriteString("{\n")
	for i, key := range ordered {
		name, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		var indented strings.Builder
		if err := indentJSON(&indented, document[key]); err != nil {
			return nil, err
		}
		out.WriteString("  " + string(name) + ": " + indented.String())
		if i < len(ordered)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("}\n")
	return []byte(out.String()), nil
}

func indentJSON(out *strings.Builder, raw json.RawMessage) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(value, "  ", "  ")
	if err != nil {
		return err
	}
	out.Write(encoded)
	return nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// GlobalPath is where the wizard writes.
func GlobalPath() (string, error) { return appconfig.GlobalPath() }

// CredentialSummary describes, in one line, how the credential will be found
// at run time — shown on the confirmation screen so the user agrees to the
// storage decision rather than discovering it.
func (r Result) CredentialSummary() string {
	switch r.Credential {
	case CredentialNone:
		return "none required"
	case CredentialEnv:
		return "read from $" + r.EnvVar + " (already exported; the value is not stored by Collomia)"
	case CredentialStore:
		return "stored in " + credstore.Backend()
	case CredentialManual:
		return "read from $" + r.EnvVar + " — export it before starting a session"
	case CredentialKeep:
		if r.EnvVar != "" {
			return "unchanged — read from $" + r.EnvVar + " as before"
		}
		return "unchanged — the arrangement that already authenticates is kept"
	default:
		return string(r.Credential)
	}
}
