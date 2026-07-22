package provider

import (
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func TestEveryProviderTypeDeclaresCapabilities(t *testing.T) {
	types := []string{
		"openai", "openai-compatible", "anthropic", "anthropic-compatible",
		"bedrock", "bedrock-mantle", "azure-openai", "azure-foundry",
		"azure-foundry-anthropic",
	}
	for _, providerType := range types {
		t.Run(providerType, func(t *testing.T) {
			capabilities, err := CapabilitiesFor(providerType, "fixture-model", 128_000)
			if err != nil {
				t.Fatal(err)
			}
			if capabilities.ProviderType != providerType || capabilities.Model != "fixture-model" {
				t.Fatalf("identity=%+v", capabilities)
			}
			if capabilities.Tools == "" || capabilities.Streaming == "" || capabilities.Reasoning == "" || capabilities.Images == "" || capabilities.StructuredOutput == "" || capabilities.TokenCounting == "" || capabilities.PromptCaching == "" || capabilities.ParallelToolCalls == "" || capabilities.ModelDiscovery == "" {
				t.Fatalf("declaration has an empty state: %+v", capabilities)
			}
			if capabilities.ContextWindow != 128_000 {
				t.Fatalf("context=%d", capabilities.ContextWindow)
			}
			if (providerType == "bedrock" || providerType == "bedrock-mantle") && capabilities.Streaming != CapabilitySupported {
				t.Fatalf("streaming=%s", capabilities.Streaming)
			}
		})
	}
}

func TestFactoryClientsReportTheirEffectiveDeclaration(t *testing.T) {
	types := []string{
		"openai", "openai-compatible", "anthropic", "anthropic-compatible",
		"bedrock", "bedrock-mantle", "azure-openai", "azure-foundry",
		"azure-foundry-anthropic",
	}
	for _, providerType := range types {
		t.Run(providerType, func(t *testing.T) {
			client, err := New("fixture", appconfig.Provider{Type: providerType, BaseURL: "https://example.invalid", Context: 32_000}, "fixture-model")
			if err != nil {
				t.Fatal(err)
			}
			reporter, ok := client.(CapabilityReporter)
			if !ok {
				t.Fatalf("%T does not report capabilities", client)
			}
			capabilities := reporter.Capabilities()
			if capabilities.ProviderType != providerType || capabilities.Model != "fixture-model" || capabilities.ContextWindow != 32_000 {
				t.Fatalf("capabilities=%+v", capabilities)
			}
		})
	}
}

func TestCapabilityDeclarationsPreserveImportantDifferences(t *testing.T) {
	openAI, _ := CapabilitiesFor("openai", "gpt", 100_000)
	bedrock, _ := CapabilitiesFor("bedrock", "claude", 200_000)
	if openAI.Streaming != CapabilitySupported || openAI.ModelDiscovery != CapabilitySupported {
		t.Fatalf("openai=%+v", openAI)
	}
	if bedrock.Streaming != CapabilitySupported || bedrock.ModelDiscovery != CapabilityUnsupported {
		t.Fatalf("bedrock=%+v", bedrock)
	}
	if bedrock.Reasoning != CapabilityPartial {
		t.Fatalf("bedrock reasoning=%s", bedrock.Reasoning)
	}
	if openAI.Images != CapabilityPartial || bedrock.Images != CapabilityPartial {
		t.Fatal("image-capable adapters must preserve model-specific uncertainty")
	}
}

func TestValidateRequestRejectsKnownContradictions(t *testing.T) {
	request := Request{Model: "text-only", Tools: []ToolDefinition{{Name: "read_file"}}}
	capabilities := Capabilities{ProviderType: "fixture", Model: "text-only", Tools: CapabilityUnsupported}
	if err := ValidateRequest(capabilities, request); err == nil {
		t.Fatal("expected unsupported tool request to be rejected")
	}

	capabilities = Capabilities{ProviderType: "fixture", Model: "small", Tools: CapabilitySupported, ContextWindow: 100}
	if err := ValidateRequest(capabilities, Request{MaxTokens: 101}); err == nil {
		t.Fatal("expected output limit larger than the context window to be rejected")
	}
	if err := ValidateRequest(capabilities, Request{MaxTokens: 100}); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestCapabilitySummariesExposePartialAndUnknownStates(t *testing.T) {
	capabilities := Capabilities{
		Tools: CapabilitySupported, Streaming: CapabilityUnknown,
		PromptCaching: CapabilityPartial, Images: CapabilityUnsupported,
		ContextWindow: 128_000,
	}
	detail := capabilities.DetailSummary()
	for _, want := range []string{"supported: tools", "partial: prompt caching", "unavailable: images", "unknown: streaming", "configured context: 128000"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail %q missing %q", detail, want)
		}
	}
}
