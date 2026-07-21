package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestWriteProjectStarterIsMinimal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ProjectFile)
	if err := WriteStarter(path, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 2 || raw["schema_version"] == nil || raw["permissions"] == nil {
		t.Fatalf("project starter should contain only schema_version and permissions: %s", data)
	}
	var permissions map[string]json.RawMessage
	if err := json.Unmarshal(raw["permissions"], &permissions); err != nil {
		t.Fatal(err)
	}
	if len(permissions) != 1 || string(permissions["mode"]) != `"ask"` {
		t.Fatalf("project starter should set only permissions.mode=ask: %s", raw["permissions"])
	}
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions.Mode != "ask" || cfg.DefaultProvider != "ollama" {
		t.Fatalf("starter did not layer over defaults: mode=%q provider=%q", cfg.Permissions.Mode, cfg.DefaultProvider)
	}
}

func TestWriteGlobalStarterIncludesProvidersPermissionsAndOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := WriteStarter(path, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProvider != "ollama" || cfg.DefaultModel != "qwen3-coder" {
		t.Fatalf("selection=%s/%s", cfg.DefaultProvider, cfg.DefaultModel)
	}
	openrouter, ok := cfg.Providers["openrouter"]
	if !ok {
		t.Fatal("global starter is missing openrouter")
	}
	if openrouter.Type != "openai" || openrouter.BaseURL != "https://openrouter.ai/api/v1" || openrouter.APIKeyEnv != "OR_API_KEY" || openrouter.Model != "z-ai/glm-5.2" || openrouter.MaxTokens != 128000 || openrouter.Context != 500000 {
		t.Fatalf("openrouter=%+v", openrouter)
	}
	if openrouter.ConnectTimeoutSeconds != 10 || openrouter.RequestTimeoutSeconds != 1800 || openrouter.StreamIdleTimeoutSeconds != 300 {
		t.Fatalf("openrouter timeout defaults=%+v", openrouter)
	}
	var permissions map[string]json.RawMessage
	if err := json.Unmarshal(rawField(t, data, "permissions"), &permissions); err != nil {
		t.Fatal(err)
	}
	if len(permissions) != 5 || string(permissions["mode"]) != `"ask"` || string(permissions["allow_outside_workspace"]) != "false" || string(permissions["sandbox"]) != `"off"` || string(permissions["sandbox_allow_network"]) != "true" || string(permissions["sandbox_allow_read_outside_workspace"]) != "true" {
		t.Fatalf("global starter permissions should expose compatibility-friendly editable defaults: %s", rawField(t, data, "permissions"))
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(rawField(t, data, "options"), &options); err != nil {
		t.Fatal(err)
	}
	if len(options) != 2 || string(options["max_iterations"]) != "24" || string(options["max_tool_output_bytes"]) != "65536" {
		t.Fatalf("global starter options should expose runtime defaults: %s", rawField(t, data, "options"))
	}
}

func TestConfigReferenceIsCompleteSafeAndParseable(t *testing.T) {
	reference := ConfigReference()
	var uncommented strings.Builder
	for _, line := range strings.Split(reference, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		uncommented.WriteString(line)
		uncommented.WriteByte('\n')
	}
	var cfg Config
	if err := json.Unmarshal([]byte(uncommented.String()), &cfg); err != nil {
		t.Fatalf("reference should become valid JSON when full-line comments are removed: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("reference examples should validate: %v", err)
	}
	for name, server := range cfg.MCP {
		if !server.Disabled || server.Trusted {
			t.Fatalf("reference MCP server %q must be disabled and untrusted: %+v", name, server)
		}
	}
	fields := map[string]struct{}{}
	collectJSONFieldNames(reflect.TypeFor[Config](), map[reflect.Type]bool{}, fields)
	for field := range fields {
		if !strings.Contains(reference, strconv.Quote(field)) {
			t.Errorf("configuration reference is missing JSON field %q", field)
		}
	}
}

func TestReferencePath(t *testing.T) {
	for input, want := range map[string]string{
		"/work/.collomia.json":             "/work/.collomia.example.jsonc",
		"/home/user/.collomia/config.json": "/home/user/.collomia/config.example.jsonc",
	} {
		if got := ReferencePath(input); got != want {
			t.Errorf("ReferencePath(%q)=%q, want %q", input, got, want)
		}
	}
}

func rawField(t *testing.T, data []byte, name string) json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw[name]
}

func collectJSONFieldNames(typ reflect.Type, seen map[reflect.Type]bool, fields map[string]struct{}) {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || seen[typ] {
		return
	}
	seen[typ] = true
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields[name] = struct{}{}
		}
		collectJSONFieldNames(field.Type, seen, fields)
	}
}
