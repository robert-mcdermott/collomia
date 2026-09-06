package tools

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestViewImageReturnsPixelsWithoutValidationReceipt(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "page.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 20, 10))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	guard, err := NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := ViewImageTool{Guard: guard}
	raw := json.RawMessage(`{"path":"page.png"}`)
	result, err := tool.ExecuteResultStream(t.Context(), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Evidence != nil || len(result.Parts) != 1 || result.Parts[0].MediaType != "image/png" || len(result.Parts[0].Data) == 0 || len(result.Parts[0].SHA256) != 64 {
		t.Fatalf("unexpected result: %+v", result)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := tool.Execute(ctx, raw); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := tool.Assess(json.RawMessage(`{"path":"../outside.png"}`)); err == nil {
		t.Fatal("outside read allowed")
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.png"), []byte("not pixels"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"path":"bad.png"}`)); err == nil {
		t.Fatal("invalid image accepted")
	}
}
