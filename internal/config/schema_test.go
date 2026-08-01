package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// schemaDefinition is one generated $defs entry: the Go struct behind it and
// the JSON fields it publishes. The Go name is kept because a variant's
// descriptions fall back to the shared type's, which is how AgentRule inherits
// Rule's prose while overriding only the field whose meaning differs.
type schemaDefinition struct {
	goType string
	fields map[string]reflect.StructField
}

// schemaDefinitions walks the configuration type graph the way the generator
// does, yielding every (definition, field) pair the schema must cover.
func schemaDefinitions(t *testing.T) map[string]schemaDefinition {
	t.Helper()
	found := map[string]schemaDefinition{}
	var walk func(typ reflect.Type, defName string)
	walk = func(typ reflect.Type, defName string) {
		if _, seen := found[defName]; seen {
			return
		}
		fields := map[string]reflect.StructField{}
		found[defName] = schemaDefinition{goType: typ.Name(), fields: fields}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name, ok := jsonFieldName(field)
			if !ok {
				continue
			}
			fields[name] = field
			child := field.Type
			for child.Kind() == reflect.Pointer || child.Kind() == reflect.Slice ||
				child.Kind() == reflect.Array || child.Kind() == reflect.Map {
				child = child.Elem()
			}
			if child.Kind() == reflect.Struct {
				childName := child.Name()
				if variant := variantFor(fieldKey{defName, name}); variant != "" {
					childName = variant
				}
				walk(child, childName)
			}
		}
	}
	walk(reflect.TypeFor[Config](), "Config")
	return found
}

func TestEveryConfigurationFieldIsDescribed(t *testing.T) {
	// The whole reason this schema exists is that configuring Collomia meant
	// reading extensive documentation elsewhere. A field with no description
	// gives an editor a name and a type and nothing else, which puts that field
	// back where it started — so shipping one is a build failure, not a
	// documentation debt.
	for defName, definition := range schemaDefinitions(t) {
		for jsonName := range definition.fields {
			description := describeField(definition.goType, defName, jsonName)
			if strings.TrimSpace(description) == "" {
				t.Errorf("%s.%s has no description; add one to fieldDescriptions", defName, jsonName)
			}
		}
	}
}

func TestNoDescriptionSurvivesItsField(t *testing.T) {
	// The mirror of the test above: an entry for a field that no longer exists
	// is documentation for something a user cannot write, and it would sit
	// there indefinitely because nothing else reads it.
	definitions := schemaDefinitions(t)
	for key := range fieldDescriptions {
		definition, ok := definitions[key.def]
		if !ok {
			t.Errorf("fieldDescriptions describes definition %q, which no longer exists", key.def)
			continue
		}
		if _, ok := definition.fields[key.field]; !ok {
			t.Errorf("fieldDescriptions describes %s.%s, which no longer exists", key.def, key.field)
		}
	}
}

func TestSchemaEnumsAreTheValidatorsOwnVocabularies(t *testing.T) {
	// This is the property the whole vocabulary extraction was for. A schema
	// that lists a value the loader rejects makes an editor recommend a broken
	// configuration; a schema that omits one the loader accepts makes it
	// underline a correct one. Both are worse than no schema, because both are
	// confidently wrong.
	vocabularies := map[string][]string{
		"AutonomyModes":         AutonomyModes(),
		"SandboxModes":          SandboxModes(),
		"NetworkPostures":       NetworkPostures(),
		"CommandPostures":       CommandPostures(),
		"SandboxEgressModes":    SandboxEgressModes(),
		"CommandEnvModes":       CommandEnvModes(),
		"RuleActions":           RuleActions(),
		"AgentRuleActions":      AgentRuleActions(),
		"ProviderTypes":         ProviderTypes(),
		"AgentAvailabilities":   AgentAvailabilities(),
		"ReasoningEfforts":      ReasoningEfforts(),
		"AgentIntegrationModes": AgentIntegrationModes(),
		"NotificationModes":     NotificationModes(),
		"MCPTransports":         MCPTransports(),
		"PresetNames":           PresetNames(),
		"ProtectCredentials":    ProtectCredentialsSettings(),
		"PublicationSettings":   PublicationSettings(),
	}
	for defName, definition := range schemaDefinitions(t) {
		for jsonName := range definition.fields {
			enum := enumFor(fieldKey{defName, jsonName})
			if len(enum) == 0 {
				continue
			}
			matched := false
			for _, vocabulary := range vocabularies {
				if slicesEqual(enum, vocabulary) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("%s.%s enumerates %v, which is not one of the published vocabularies; the schema must not carry its own copy",
					defName, jsonName, enum)
			}
		}
	}
}

func TestEveryVocabularyValueIsAcceptedByTheValidator(t *testing.T) {
	// Drives the loader with each published value rather than comparing two
	// lists to each other, so a vocabulary that drifted away from the switch it
	// replaced fails here instead of at a user's first edit.
	for _, mode := range AutonomyModes() {
		assertValid(t, func(c *Config) { c.Permissions.Mode = mode }, "permissions.mode="+mode)
	}
	for _, value := range SandboxModes() {
		assertValid(t, func(c *Config) { c.Permissions.Sandbox = value }, "permissions.sandbox="+value)
	}
	for _, value := range NetworkPostures() {
		assertValid(t, func(c *Config) { c.Permissions.Network = value }, "permissions.network="+value)
	}
	for _, value := range CommandPostures() {
		assertValid(t, func(c *Config) { c.Permissions.Commands = value }, "permissions.commands="+value)
	}
	for _, value := range SandboxEgressModes() {
		assertValid(t, func(c *Config) { c.Permissions.SandboxEgress = value }, "permissions.sandbox_egress="+value)
	}
	for _, value := range CommandEnvModes() {
		assertValid(t, func(c *Config) { c.Permissions.CommandEnv = value }, "permissions.command_env="+value)
	}
	for _, value := range ProtectCredentialsSettings() {
		assertValid(t, func(c *Config) { c.Permissions.ProtectCredentials = value }, "permissions.protect_credentials="+value)
	}
	for _, value := range PublicationSettings() {
		assertValid(t, func(c *Config) { c.Permissions.Publication = value }, "permissions.publication="+value)
	}
	for _, value := range PresetNames() {
		assertValid(t, func(c *Config) { c.Permissions.Preset = value }, "permissions.preset="+value)
	}
	for _, value := range RuleActions() {
		assertValid(t, func(c *Config) {
			c.Permissions.Rules = []Rule{{Action: value, Tool: "read_file"}}
		}, "rules.action="+value)
	}
	for _, value := range AgentRuleActions() {
		assertValid(t, func(c *Config) {
			c.Agents = map[string]AgentDefinition{"r": {Permissions: AgentPermissions{
				Rules: []Rule{{Action: value, Tool: "read_file"}},
			}}}
		}, "agents.permissions.rules.action="+value)
	}
	for _, value := range AgentAvailabilities() {
		assertValid(t, func(c *Config) {
			c.Agents = map[string]AgentDefinition{"r": {Availability: value}}
		}, "agents.availability="+value)
	}
	for _, value := range ReasoningEfforts() {
		assertValid(t, func(c *Config) {
			p := c.Providers["p"]
			p.Reasoning = &Reasoning{Effort: value}
			c.Providers["p"] = p
		}, "reasoning.effort="+value)
	}
	for _, value := range AgentIntegrationModes() {
		assertValid(t, func(c *Config) { c.Options.AgentIntegration = value }, "options.agent_integration="+value)
	}
	for _, value := range NotificationModes() {
		assertValid(t, func(c *Config) { c.Options.Notifications = value }, "options.notifications="+value)
	}
	for _, value := range ProviderTypes() {
		assertValid(t, func(c *Config) {
			p := c.Providers["p"]
			p.Type, p.BaseURL, p.Region = value, "https://example.test/v1", "us-west-2"
			c.Providers["p"] = p
		}, "providers.type="+value)
	}
}

func TestEveryMCPTransportIsDispatched(t *testing.T) {
	// ValidateMCPServer switches on transport to run per-transport checks, so
	// the vocabulary and the dispatch are two places rather than one. A
	// transport added to the list without a case would be reported as invalid
	// while the schema offered it.
	for _, transport := range MCPTransports() {
		server := MCPServer{Transport: transport, Command: "x", URL: "https://example.test/mcp"}
		for _, err := range ValidateMCPServer("s", server) {
			if strings.HasSuffix(err.Field, ".transport") {
				t.Errorf("transport %q is published but not dispatched: %s", transport, err.Message)
			}
		}
	}
}

func TestSchemaBoundsMatchTheValidator(t *testing.T) {
	// A bound stated in the schema and not enforced by the loader would let an
	// editor pass a configuration the loader then refuses.
	assertInvalid(t, func(c *Config) { c.Options.DelegateMaxConcurrency = 7 }, "delegate_max_concurrency above the stated maximum")
	assertValid(t, func(c *Config) { c.Options.DelegateMaxConcurrency = 6 }, "delegate_max_concurrency at the stated maximum")
	assertInvalid(t, func(c *Config) {
		c.Agents = map[string]AgentDefinition{"r": {TimeoutSeconds: 3601}}
	}, "agent timeout above the stated maximum")
	assertValid(t, func(c *Config) {
		c.Agents = map[string]AgentDefinition{"r": {TimeoutSeconds: 3600}}
	}, "agent timeout at the stated maximum")
	assertInvalid(t, func(c *Config) {
		p := c.Providers["p"]
		p.MaxTokens = -1
		c.Providers["p"] = p
	}, "negative max_tokens below the stated minimum")
	assertInvalid(t, func(c *Config) {
		p := c.Providers["p"]
		p.Pricing = &Pricing{InputPerMillion: 0, OutputPerMillion: 1}
		c.Providers["p"] = p
	}, "zero input price at the stated exclusive minimum")
}

func TestGeneratedSchemaIsWellFormedAndStable(t *testing.T) {
	first := JSONSchema()
	if !json.Valid(first) {
		t.Fatal("the generated schema is not valid JSON")
	}
	// Go randomizes map iteration, so a generator that leaked iteration order
	// into its output would produce a file that changes on every run — and
	// `collo doctor` compares the on-disk schema byte for byte to decide
	// whether it is stale.
	for i := 0; i < 5; i++ {
		if string(JSONSchema()) != string(first) {
			t.Fatal("the generated schema is not byte-stable across runs")
		}
	}
	var document map[string]any
	if err := json.Unmarshal(first, &document); err != nil {
		t.Fatal(err)
	}
	if document["$schema"] != schemaDialect {
		t.Errorf("dialect = %v", document["$schema"])
	}
	if _, ok := document["required"]; ok {
		t.Error("the root must state no required fields: a configuration file is one layer of a merge, " +
			"so a project file setting two rules and nothing else is correct")
	}
	properties, _ := document["properties"].(map[string]any)
	if _, ok := properties["$schema"]; !ok {
		t.Error("the schema must permit the $schema key it tells people to write, " +
			"or `collo config validate --strict` rejects a file this build produced")
	}
}

func TestStrictLoadAcceptsTheSchemaKeyItWrites(t *testing.T) {
	// LoadOptions.Strict turns on DisallowUnknownFields. Before $schema was a
	// declared field, a configuration carrying the key that makes editing
	// bearable would have loaded normally and failed --strict — a validator
	// rejecting a file the tool's own `collo init` had just written.
	dir := t.TempDir()
	if err := WriteStarter(dir+"/"+ProjectFile, false); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithOptions(dir, LoadOptions{Strict: true, TrustStatus: trustAll}); err != nil {
		t.Fatalf("strict load rejected the file collo init wrote: %v", err)
	}
}

func TestSchemaKeyConfiguresNothing(t *testing.T) {
	// $schema describes the file; it must never reach a decision. If it ever
	// gained meaning, a user pointing at a different schema would be changing
	// behavior by editing an editor hint.
	base := validConfig()
	base.Schema = "./" + SchemaFileName
	other := validConfig()
	other.Schema = "https://example.test/other.json"
	if len(base.ValidateFields()) != 0 || len(other.ValidateFields()) != 0 {
		t.Fatal("$schema must not participate in validation")
	}
}

func validConfig() Config {
	cfg := Defaults()
	cfg.Providers = map[string]Provider{"p": {
		Type: "openai-compatible", BaseURL: "https://example.test/v1", Model: "m",
	}}
	cfg.DefaultProvider, cfg.DefaultModel = "p", "m"
	return cfg
}

func assertValid(t *testing.T, mutate func(*Config), label string) {
	t.Helper()
	cfg := validConfig()
	mutate(&cfg)
	if errs := cfg.ValidateFields(); len(errs) != 0 {
		t.Errorf("%s: published as valid but the loader refused it: %v", label, errs)
	}
}

func assertInvalid(t *testing.T, mutate func(*Config), label string) {
	t.Helper()
	cfg := validConfig()
	mutate(&cfg)
	if errs := cfg.ValidateFields(); len(errs) == 0 {
		t.Errorf("%s: the schema states this bound but the loader accepts it", label)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
