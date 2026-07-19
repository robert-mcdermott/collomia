package mcpclient

import (
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func TestUntrustedServerIsNotStarted(t *testing.T) {
	registry := tools.NewRegistry()
	manager, warnings := ConnectAll(t.Context(), map[string]appconfig.MCPServer{
		"untrusted": {Transport: "stdio", Command: "command-that-must-not-run"},
	}, registry, testOpts(t))
	defer manager.Close()
	if len(warnings) != 1 {
		t.Fatalf("warnings=%v", warnings)
	}
	if len(manager.Servers()) != 0 {
		t.Fatalf("servers=%v", manager.Servers())
	}
}

func TestSanitizeToolName(t *testing.T) {
	if got := sanitize("mcp_docs/search web"); got != "mcp_docs_search_web" {
		t.Fatalf("sanitize=%q", got)
	}
}
