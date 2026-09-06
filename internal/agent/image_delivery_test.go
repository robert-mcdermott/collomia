package agent

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type textOnlyImageClient struct{ *fakeClient }

func (*textOnlyImageClient) Capabilities() provider.Capabilities {
	return provider.Capabilities{Images: provider.CapabilityUnsupported}
}

func TestImageUnavailableIsExplicitInModelContext(t *testing.T) {
	client := &textOnlyImageClient{&fakeClient{}}
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "image_fixture", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead},
		RunResult: func(_ context.Context, _ json.RawMessage, _ func(string)) (tools.Result, error) {
			return tools.Result{Content: "Image loaded", Parts: []provider.ContentPart{{Type: provider.ContentImage, MediaType: "image/png", Data: []byte("pixels")}}}, nil
		},
	})
	client.chat = func(call int, req provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "image", Name: "image_fixture", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		if len(last.Parts) != 0 || !strings.Contains(last.Content, "No image pixels were delivered") {
			t.Fatalf("missing model-visible limitation: %+v", last)
		}
		return provider.Response{Content: "Please review the image."}, nil
	}
	a := New(Options{Client: client, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil)})
	if _, err := a.Run(t.Context(), "Inspect", nil); err != nil {
		t.Fatal(err)
	}
}

func TestWorkImageToolSendsPixelsAndRemainsReadOnly(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "page.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	guard, err := tools.NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := tools.ViewImageTool{Guard: guard}
	client := &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "view", Name: "view_image", Arguments: json.RawMessage(`{"path":"page.png"}`)}}}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		if len(last.Parts) != 1 || len(last.Parts[0].Data) == 0 {
			t.Fatalf("pixels missing: %+v", last)
		}
		return provider.Response{Content: "The image is blank."}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fixture", Model: "vision", Workspace: dir, Registry: tools.NewRegistry(tool), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), TaskMode: "work"})
	if _, err := a.Run(t.Context(), "Inspect the page", nil); err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 {
		t.Fatalf("unexpected completion intervention: %d calls", client.calls)
	}
	action, err := tool.Assess(json.RawMessage(`{"path":"page.png"}`))
	if err != nil {
		t.Fatal(err)
	}
	effects := executionEffects("view_image", action)
	if effects.Unknown || len(effects.Paths) != 0 || !planTool("view_image") {
		t.Fatalf("image load treated as mutation: %+v", effects)
	}
}
