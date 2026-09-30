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
	// Secret is held only between verification and Apply. The store plan writes
	// it to the OS credential manager; the automatic startup path may expose a
	// manual-plan value just long enough for app.New to construct the verified
	// provider client. It is never serialized.
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
	// Effort is the reasoning effort chosen for this model on the effort
	// screen; "" means none of its own. EffortChosen says the screen was
	// shown, which is what lets setup write or remove the model's entry.
	Effort       string
	EffortChosen bool
	// EntryOnly writes the verified model as an entry in the provider's
	// `models` map — its limits and effort — without changing the provider's
	// own model or anything else in the block ("Add a model").
	EntryOnly bool
	// KeepSettings keeps the entry's other settings even when its endpoint
	// changes. It is set only when the user is editing this provider's
	// connection, where the settings are theirs to keep; a new provider that
	// happens to reuse a name still gets nothing carried over.
	KeepSettings bool
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

// mergeIntoFile adds or updates the verified provider without disturbing
// anything else in the file (see editConfigFile and mergeProvider).
func mergeIntoFile(path string, result Result) error {
	return editConfigFile(path, func(document, providers map[string]json.RawMessage) error {
		encoded, _, err := mergeProvider(providers[result.Name], result)
		if err != nil {
			return err
		}
		providers[result.Name] = encoded
		if result.MakeDefault {
			if document["default_provider"], err = json.Marshal(result.Name); err != nil {
				return err
			}
			if document["default_model"], err = json.Marshal(result.Model); err != nil {
				return err
			}
		}
		return nil
	})
}

// editConfigFile is the one path every setup edit takes: read the file as a
// document, let mutate change it, and write it back with the leading keys in
// place and the sibling schema refreshed.
//
// It edits the decoded document rather than re-serializing a typed Config,
// because a typed round trip would rewrite every field the user had left unset
// to its zero value and silently convert a sparse file into an exhaustive one.
// Settings this build does not know about survive untouched for the same
// reason. A file that is not valid JSON is refused rather than treated as
// empty, which would let an edit destroy settings the user still has.
func editConfigFile(path string, mutate func(document, providers map[string]json.RawMessage) error) error {
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
	if err := mutate(document, providers); err != nil {
		return err
	}
	if document["providers"], err = json.Marshal(providers); err != nil {
		return err
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

// ownedKeys are the provider fields setup writes: what it verified, the limits
// it resolved or was given, and where the credential lives. Build sets nothing
// else, and TestOwnedKeysCoverEverythingBuildWrites holds the two together.
var ownedKeys = map[string]bool{
	"type": true, "base_url": true, "model": true,
	"region": true, "profile": true, "deployment": true, "api_version": true,
	"auth": true, "entra_scope": true, "entra_tenant_id": true, "entra_authority_host": true,
	"max_tokens": true, "context_window": true, "api_key_env": true,
}

// ProviderUpdate describes what writing a Result does to an entry the file
// already has under the same name, so the confirmation can state it before it
// happens.
type ProviderUpdate struct {
	// Exists reports that the file has an entry under this name at all.
	Exists bool
	// Replaced means the entry pointed at a different endpoint, so it is
	// replaced whole and Dropped names the settings that go with it.
	Replaced bool
	// Kept names the entry's own settings that survive an update — every key
	// setup does not write.
	Kept []string
	// Dropped names the settings a replacement removes.
	Dropped []string
	// EndpointChanged reports that a connection edit points the entry at a
	// different endpoint while keeping its settings, so the confirmation can
	// say the kept headers will now be sent there.
	EndpointChanged bool
	// MovedLimitsFor names the model the entry used to run with when its
	// provider-level limits move into that model's own `models` entry, so a
	// later /model switch back to it still has them.
	MovedLimitsFor string
}

// Update reports what writing result would do to this file's entry of the
// same name, making the same decision mergeIntoFile makes when it writes.
func (e Existing) Update(result Result) ProviderUpdate {
	if !e.Has(result.Name) {
		return ProviderUpdate{}
	}
	raw, ok := e.Raw[result.Name]
	if !ok {
		// Without the entry's text nothing proves it is the same endpoint, so
		// the conservative description is the true one for the worst case.
		return ProviderUpdate{Exists: true, Replaced: true}
	}
	_, update, err := mergeProvider(raw, result)
	if err != nil {
		return ProviderUpdate{Exists: true, Replaced: true}
	}
	return update
}

// mergeProvider combines the entry already in the file with what setup
// verified.
//
// Every earlier version replaced the entry whole, so re-verifying a provider —
// to change its model, say — silently deleted its headers, temperature,
// reasoning, pricing, and timeouts: settings setup never asks about and had no
// business removing. Now setup replaces only the fields it owns and everything
// else the user wrote survives, including keys this build does not know.
//
// The exception is a name that now points somewhere else. Headers commonly
// carry a gateway's credentials, and moving them to a different host would be
// a leak rather than a convenience, so a changed type or base URL replaces the
// entry whole — and the confirmation names what that drops.
func mergeProvider(existing json.RawMessage, result Result) (json.RawMessage, ProviderUpdate, error) {
	written, err := json.Marshal(result.Provider)
	if err != nil {
		return nil, ProviderUpdate{}, err
	}
	var fresh map[string]json.RawMessage
	if err := json.Unmarshal(written, &fresh); err != nil {
		return nil, ProviderUpdate{}, err
	}
	whole := func(update ProviderUpdate) (json.RawMessage, ProviderUpdate, error) {
		if err := applyEffort(fresh, result); err != nil {
			return nil, ProviderUpdate{}, err
		}
		encoded, err := json.Marshal(fresh)
		return encoded, update, err
	}
	if len(strings.TrimSpace(string(existing))) == 0 || strings.TrimSpace(string(existing)) == "null" {
		return whole(ProviderUpdate{})
	}
	update := ProviderUpdate{Exists: true}
	var old map[string]json.RawMessage
	if err := json.Unmarshal(existing, &old); err != nil {
		update.Replaced = true
		return whole(update)
	}

	owned := func(key string) bool {
		// A literal key is the credential arrangement too. Keeping it beside a
		// newly stored or exported one would leave the old key winning.
		if key == "api_key" {
			return result.Credential != CredentialKeep
		}
		return ownedKeys[key]
	}
	var userKeys []string
	for key := range old {
		if !owned(key) {
			userKeys = append(userKeys, key)
		}
	}
	sortStrings(userKeys)

	if result.EntryOnly {
		if err := writeModelEntry(old, result); err != nil {
			return nil, ProviderUpdate{}, err
		}
		encoded, err := json.Marshal(old)
		update.Kept = userKeys
		return encoded, update, err
	}
	if !result.KeepSettings && !sameEndpoint(old, result.Provider) {
		update.Replaced, update.Dropped = true, userKeys
		return whole(update)
	}
	if result.KeepSettings && !sameEndpoint(old, result.Provider) {
		update.EndpointChanged = true
	}

	merged := make(map[string]json.RawMessage, len(old)+len(fresh))
	for key, value := range old {
		if !owned(key) {
			merged[key] = value
		}
	}
	for key, value := range fresh {
		merged[key] = keepReference(old[key], value)
	}
	moved, err := keepModelLimits(merged, old, result.Provider.Model)
	if err != nil {
		return nil, ProviderUpdate{}, err
	}
	if err := applyEffort(merged, result); err != nil {
		return nil, ProviderUpdate{}, err
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return nil, ProviderUpdate{}, err
	}
	update.Kept, update.MovedLimitsFor = userKeys, moved
	return encoded, update, nil
}

// keepModelLimits keeps per-model limits consistent when setup changes which
// model a provider runs with.
//
// Provider-level limits describe the provider's own model. Before per-model
// settings, choosing a different model overwrote them, so switching back with
// /model ran the old model on the new one's numbers. Now the old model's
// limits move into its own `models` entry (without overriding one it already
// has), and the newly chosen model's entry loses any limit the provider level
// now states for it, since an entry would otherwise shadow what was just
// verified. Its reasoning and pricing stay.
func keepModelLimits(merged, old map[string]json.RawMessage, newModel string) (string, error) {
	models := map[string]map[string]json.RawMessage{}
	if raw, ok := merged["models"]; ok {
		if err := json.Unmarshal(raw, &models); err != nil {
			// Not an object of objects: leave it for the loader to report
			// rather than rewriting something setup does not understand.
			return "", nil
		}
	}
	var oldModel string
	_ = json.Unmarshal(old["model"], &oldModel)
	moved := ""
	if oldModel != "" && oldModel != newModel {
		for _, key := range []string{"context_window", "max_tokens"} {
			value, ok := old[key]
			if !ok {
				continue
			}
			entry := models[oldModel]
			if entry == nil {
				entry = map[string]json.RawMessage{}
			}
			if _, has := entry[key]; !has {
				entry[key] = value
				moved = oldModel
			}
			models[oldModel] = entry
		}
	}
	if entry, ok := models[newModel]; ok {
		delete(entry, "context_window")
		delete(entry, "max_tokens")
		if len(entry) == 0 {
			delete(models, newModel)
		}
	}
	if len(models) == 0 {
		delete(merged, "models")
		return moved, nil
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return "", err
	}
	merged["models"] = encoded
	return moved, nil
}

// sameEndpoint reports whether an existing entry and a verified provider
// address the same thing. The existing base URL is expanded first, because
// provider resolution expanded it before verification.
func sameEndpoint(old map[string]json.RawMessage, p appconfig.Provider) bool {
	var oldType, oldURL string
	_ = json.Unmarshal(old["type"], &oldType)
	_ = json.Unmarshal(old["base_url"], &oldURL)
	normalize := func(url string) string { return strings.TrimRight(strings.TrimSpace(url), "/") }
	return strings.TrimSpace(oldType) == p.Type && normalize(appconfig.ExpandEnv(oldURL)) == normalize(p.BaseURL)
}

// keepReference keeps the file's own spelling of a value when it expands to
// what setup is writing. Provider resolution expands `${GATEWAY}/v1` before
// verification, and writing the expansion back would quietly turn a
// reference the user maintains into a literal they no longer control.
func keepReference(old, fresh json.RawMessage) json.RawMessage {
	var before, after string
	if json.Unmarshal(old, &before) != nil || json.Unmarshal(fresh, &after) != nil {
		return fresh
	}
	if before == after || !strings.Contains(before, "$") {
		return fresh
	}
	if strings.TrimRight(strings.TrimSpace(appconfig.ExpandEnv(before)), "/") == strings.TrimRight(strings.TrimSpace(after), "/") {
		return old
	}
	return fresh
}

// writeModelEntry records a verified model's limits and effort in the
// provider's `models` map, leaving the provider's own fields untouched.
func writeModelEntry(block map[string]json.RawMessage, result Result) error {
	models := map[string]map[string]json.RawMessage{}
	if raw, ok := block["models"]; ok {
		if err := json.Unmarshal(raw, &models); err != nil {
			return fmt.Errorf("models is not an object of objects: %w", err)
		}
	}
	entry := models[result.Model]
	if entry == nil {
		entry = map[string]json.RawMessage{}
	}
	for key, value := range map[string]int{"context_window": result.Provider.Context, "max_tokens": result.Provider.MaxTokens} {
		if value <= 0 {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		entry[key] = encoded
	}
	models[result.Model] = entry
	encoded, err := json.Marshal(models)
	if err != nil {
		return err
	}
	block["models"] = encoded
	return applyEffort(block, result)
}

// WithLimits replaces the token limits a Result will write with the ones the
// user accepted or typed on the limits screen. The model's own maximum, a
// display-only fact about the endpoint, is kept from discovery.
func (r Result) WithLimits(l provider.Limits) Result {
	if l.ModelMaximum == 0 {
		l.ModelMaximum = r.Limits.ModelMaximum
	}
	r.Provider.Context, r.Provider.MaxTokens = l.ContextWindow, l.MaxOutput
	r.Limits = l
	r.ContextAssumed = l.ContextSource == provider.LimitsAssumed
	return r
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
