package main

import (
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

func TestProviderLimitsDiagnosticWarnsAboutAnAbsentContextWindow(t *testing.T) {
	// This is the silent one: a zero window makes automatic compaction
	// unreachable for the life of every session, and nothing anywhere said so.
	status, detail := providerLimitsDiagnostic(appconfig.Provider{
		Type: "anthropic", Model: "claude-sonnet-4-5-20250929", MaxTokens: 32000,
	})
	if status != "warn" {
		t.Errorf("status = %q, want warn", status)
	}
	if !strings.Contains(detail, "compaction") {
		t.Errorf("the report must name the consequence, not the field: %q", detail)
	}
	if !strings.Contains(detail, "200000") {
		t.Errorf("a documented window for this model should be suggested: %q", detail)
	}
}

func TestProviderLimitsDiagnosticWarnsAboutADefaultedOutputCap(t *testing.T) {
	// The case found in a real configuration on the first run of this check: a
	// frontier model whose every answer was being cut off at 8192 tokens
	// because the field was absent rather than wrong.
	status, detail := providerLimitsDiagnostic(appconfig.Provider{
		Type: "bedrock", Model: "us.anthropic.claude-opus-4-1", Context: 200000,
		MaxTokens: appconfig.DefaultMaxTokens, MaxTokensDefaulted: true,
	})
	if status != "warn" {
		t.Errorf("status = %q, want warn", status)
	}
	if !strings.Contains(detail, "8192") {
		t.Errorf("the report must name the cap actually in force: %q", detail)
	}
}

func TestProviderLimitsDiagnosticStaysQuietWhenBothAreSet(t *testing.T) {
	status, detail := providerLimitsDiagnostic(appconfig.Provider{
		Type: "openai-compatible", Model: "qwen3-coder", Context: 131072, MaxTokens: 16384,
	})
	if status != "ok" {
		t.Errorf("a configuration that states both limits must not warn: %q / %q", status, detail)
	}
	if !strings.Contains(detail, "131072") || !strings.Contains(detail, "16384") {
		t.Errorf("both numbers must still be reported: %q", detail)
	}
}

func TestProviderLimitsDiagnosticNeverContradictsAConfiguredValue(t *testing.T) {
	// The table understates by design and a gateway may genuinely serve more,
	// so a larger configured window is reported, never corrected.
	status, detail := providerLimitsDiagnostic(appconfig.Provider{
		Type: "anthropic", Model: "claude-sonnet-4-5-20250929", Context: 1000000, MaxTokens: 64000,
	})
	if status != "ok" {
		t.Errorf("a window larger than the documented one is not an error: %q", status)
	}
	if !strings.Contains(detail, "1000000") {
		t.Errorf("the configured value must be what is reported: %q", detail)
	}
}

func TestReconfigureTargetRejectsAnUnknownProvider(t *testing.T) {
	// Falling through to the full provider scan would turn a typo into the
	// wrong run: the user asked to re-verify one provider and would instead be
	// walked through adding another.
	existing := setup.Existing{Path: "/tmp/config.json", Providers: []string{"ollama", "bedrock"}}
	if _, err := reconfigureTarget("olama", existing, existing.Path); err == nil {
		t.Fatal("expected an unknown provider name to be refused")
	} else if !strings.Contains(err.Error(), "ollama, bedrock") {
		t.Errorf("the error must list what is actually configured: %v", err)
	}

	name, err := reconfigureTarget("  ollama  ", existing, existing.Path)
	if err != nil || name != "ollama" {
		t.Errorf("a configured provider must resolve: %q / %v", name, err)
	}
	if name, err := reconfigureTarget("", existing, existing.Path); err != nil || name != "" {
		t.Errorf("no --provider means the ordinary run: %q / %v", name, err)
	}
}
