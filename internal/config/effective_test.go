package config

import (
	"strings"
	"testing"
)

func settingFor(settings []Setting, key string) (Setting, bool) {
	for _, setting := range settings {
		if setting.Key == key {
			return setting, true
		}
	}
	return Setting{}, false
}

func TestEffectiveSettingsNeverPrintAResolvedCredential(t *testing.T) {
	// This is the one that matters. resolveProviderEnvironment copies a
	// credential out of the environment or the OS keychain into
	// Provider.APIKey, so the *merged* configuration holds a secret that
	// appears in no file on disk. A panel that rendered the effective
	// configuration verbatim would print it into the session transcript —
	// where the user never put it and would never think to look for it.
	cfg := Defaults()
	cfg.Providers = map[string]Provider{"p": {
		Type:    "openai",
		BaseURL: "https://example.test/v1",
		APIKey:  "sk-live-do-not-print-this",
		Headers: map[string]string{"Authorization": "Bearer also-secret"},
	}}
	cfg.MCP = map[string]MCPServer{"s": {
		Transport: "stdio", Command: "x",
		Env: map[string]string{"TOKEN": "secret-token"},
	}}
	settings := cfg.EffectiveSettings()
	rendered := ""
	for _, setting := range settings {
		rendered += setting.Key + "=" + setting.Value + "\n"
	}
	for _, secret := range []string{"sk-live-do-not-print-this", "also-secret", "secret-token"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("a secret reached the rendered settings: %q", secret)
		}
	}
	if setting, ok := settingFor(settings, "providers.p.api_key"); !ok || !setting.Redacted {
		t.Errorf("providers.p.api_key = %+v, want a redacted entry", setting)
	}
	if setting, ok := settingFor(settings, "providers.p.headers.Authorization"); !ok || !setting.Redacted {
		t.Errorf("an authorization header must be redacted, got %+v", setting)
	}
	if setting, ok := settingFor(settings, "mcp.s.env.TOKEN"); !ok || !setting.Redacted {
		t.Errorf("an MCP server's environment must be redacted, got %+v", setting)
	}
}

func TestRedactionIsStructuralRatherThanPatternBased(t *testing.T) {
	// A pattern matcher has to recognize a secret to protect it, so the one it
	// does not recognize is printed in full. Deciding by position protects a
	// credential whose shape nobody has seen — which is the whole point, since
	// the endpoint may be anything a user pointed Collomia at.
	for _, path := range []string{
		"providers.anything.api_key",
		"providers.x.headers.x-api-key",
		"mcp.server.headers.Authorization",
		"mcp.server.env.ANY_NAME",
	} {
		if !isCredentialPath(path) {
			t.Errorf("%s must be treated as a credential position", path)
		}
	}
	for _, path := range []string{
		"providers.x.api_key_env", // names a variable; it is not the secret
		"providers.x.base_url",
		"permissions.mode",
		"options.headers", // a leaf named headers holds no value of its own
	} {
		if isCredentialPath(path) {
			t.Errorf("%s is not a credential position and must be shown", path)
		}
	}
}

func TestEffectiveSettingsAttributeEachKeyToItsLayer(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStarter(dir+"/"+ProjectFile, false); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	settings := cfg.EffectiveSettings()
	mode, ok := settingFor(settings, "permissions.mode")
	if !ok || mode.Value != "ask" {
		t.Fatalf("permissions.mode = %+v", mode)
	}
	if mode.Origin != "project" {
		t.Errorf("permissions.mode origin = %q; the project starter is what set it", mode.Origin)
	}
	// A setting no file mentions is the case a user cannot answer by reading
	// their own configuration, and the one this whole view exists for.
	publication, ok := settingFor(settings, "permissions.publication")
	if !ok {
		t.Fatal("permissions.publication must appear even though no file sets it")
	}
	if publication.Value != PublicationPrompt {
		t.Errorf("permissions.publication = %q, want the default in force", publication.Value)
	}
	if publication.Origin == "project" {
		t.Error("a default must not be attributed to a file that never mentioned it")
	}
}

func TestSafetySettingsCoverEveryClampedField(t *testing.T) {
	// Reading ContainmentFields rather than naming the settings again is what
	// keeps the stance display from going one field out of date the next time
	// a containment setting is added.
	cfg := Defaults()
	settings := cfg.SafetySettings()
	shown := map[string]bool{}
	for _, setting := range settings {
		shown[setting.Key] = true
	}
	for _, field := range ContainmentFields() {
		if !shown["permissions."+field] {
			t.Errorf("clamped field permissions.%s is missing from the safety stance", field)
		}
	}
	if !shown["permissions.mode"] {
		t.Error("the autonomy mode must be shown beside the clamped settings")
	}
}

func TestEveryContainmentSettingNamesItsUnsetBehavior(t *testing.T) {
	// A containment setting rendered as blank tells a reader nothing they can
	// act on, and these are the settings where "nothing is set" and "this is
	// what is in force" are most easily confused.
	cfg := Defaults()
	cfg.Permissions = Permissions{Mode: "ask"}
	for _, setting := range cfg.SafetySettings() {
		if setting.Value == "(unset)" {
			t.Errorf("%s renders as blank; add its unset behavior to containmentWhenUnset", setting.Key)
		}
	}
}

func TestSafetySettingsShowAContainmentSettingThatWasTurnedOff(t *testing.T) {
	// The defect this reflection replaced: these two fields carry omitempty, so
	// reading the stance out of the marshalled configuration dropped them at
	// exactly the moment they were tightened. A boundary that is switched on
	// must not be invisible in the display that reports boundaries.
	cfg := Defaults()
	cfg.Permissions.SandboxAllowNetwork = false
	cfg.Permissions.SandboxAllowReadOutsideWorkspace = false
	shown := map[string]string{}
	for _, setting := range cfg.SafetySettings() {
		shown[setting.Key] = setting.Value
	}
	if shown["permissions.sandbox_allow_network"] != "false" {
		t.Errorf("sandbox_allow_network = %q, want the tightened value", shown["permissions.sandbox_allow_network"])
	}
	if shown["permissions.sandbox_allow_read_outside_workspace"] != "false" {
		t.Errorf("sandbox_allow_read_outside_workspace = %q, want the tightened value", shown["permissions.sandbox_allow_read_outside_workspace"])
	}
}
