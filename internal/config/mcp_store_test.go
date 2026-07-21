package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPStoreLifecyclePreservesUnrelatedAndUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{
  "schema_version": 1,
  "future_top_level": {"kept": true},
  "mcp": {
    "existing": {
      "transport": "stdio",
      "trusted": true,
      "command": "existing-server",
      "future_server_field": "keep-me"
    }
  }
}
`
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	created, err := PutMCPServer(path, "time", MCPServer{
		Transport: "stdio", Trusted: true, Command: "uvx", Args: []string{"mcp-server-time"}, Timeout: 30,
	}, false)
	if err != nil || !created {
		t.Fatalf("PutMCPServer created=%v err=%v", created, err)
	}
	if _, err := PutMCPServer(path, "time", MCPServer{Transport: "stdio", Command: "other"}, false); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("duplicate add error=%v", err)
	}
	changed, err := SetMCPDisabled(path, "existing", true)
	if err != nil || !changed {
		t.Fatalf("SetMCPDisabled changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(root["future_top_level"]), `"kept": true`) {
		t.Fatalf("future top-level field was lost: %s", data)
	}
	var rawServers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(root["mcp"], &rawServers); err != nil {
		t.Fatal(err)
	}
	if string(rawServers["existing"]["future_server_field"]) != `"keep-me"` || string(rawServers["existing"]["disabled"]) != "true" {
		t.Fatalf("existing entry not preserved/disabled: %s", rawServers["existing"])
	}
	entries, exists, err := ReadMCPFile(path)
	if err != nil || !exists || entries["time"].Command != "uvx" || !entries["time"].Trusted {
		t.Fatalf("ReadMCPFile exists=%v entries=%+v err=%v", exists, entries, err)
	}
	removed, err := RemoveMCPServer(path, "time")
	if err != nil || !removed {
		t.Fatalf("RemoveMCPServer removed=%v err=%v", removed, err)
	}
	removed, err = RemoveMCPServer(path, "missing")
	if err != nil || removed {
		t.Fatalf("missing removal removed=%v err=%v", removed, err)
	}
	assertConfigFileMode(t, path, 0o640)
}

func TestMCPStoreCreatesMinimalConfigAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	created, err := PutMCPServer(path, "docs", MCPServer{
		Transport: "streamable-http", Trusted: true, URL: "https://example.com/mcp",
	}, false)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		SchemaVersion int                  `json:"schema_version"`
		MCP           map[string]MCPServer `json:"mcp"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if root.SchemaVersion != CurrentSchemaVersion || root.MCP["docs"].URL != "https://example.com/mcp" {
		t.Fatalf("config=%+v", root)
	}
	assertConfigFileMode(t, path, 0o600)
}

func TestMCPStoreRefusesNewerSchemaWithoutChangingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"schema_version":99,"future":"value"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PutMCPServer(path, "time", MCPServer{Command: "server"}, false); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("error=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("newer config changed: %q", after)
	}
}

func TestValidateMCPServer(t *testing.T) {
	valid := []MCPServer{
		{Command: "server"},
		{Transport: "stdio", Command: "server", Env: map[string]string{"TOKEN": "${TOKEN}"}},
		{Transport: "http", URL: "http://127.0.0.1:3000/mcp"},
		{Transport: "streamable-http", URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}},
	}
	for i, server := range valid {
		if errs := ValidateMCPServer("valid-name", server); len(errs) > 0 {
			t.Errorf("valid[%d] errors=%v", i, errs)
		}
	}
	invalid := []struct {
		name   string
		server MCPServer
	}{
		{"bad name", MCPServer{Command: "server"}},
		{"missing", MCPServer{Transport: "stdio"}},
		{"remote", MCPServer{Transport: "http", URL: "ftp://example.com/mcp"}},
		{"remote", MCPServer{Transport: "http", URL: "https://user:pass@example.com/mcp"}},
		{"bad-env", MCPServer{Command: "server", Env: map[string]string{"BAD KEY": "x"}}},
		{"negative", MCPServer{Command: "server", Timeout: -1}},
	}
	for i, test := range invalid {
		if errs := ValidateMCPServer(test.name, test.server); len(errs) == 0 {
			t.Errorf("invalid[%d] unexpectedly valid: %+v", i, test)
		}
	}
	cfg := Defaults()
	cfg.MCP["invalid"] = MCPServer{Transport: "stdio"}
	if errs := cfg.ValidateFields(); len(errs) == 0 {
		t.Fatal("Config.ValidateFields did not include MCP validation")
	}
}
