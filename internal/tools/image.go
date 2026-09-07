package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"path/filepath"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/safefile"
)

// ViewImageTool uses the same typed content and session attachment retention as
// MCP images. Loading pixels is never a receipt for visual quality.
type ViewImageTool struct{ Guard *PathGuard }

func (t ViewImageTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "view_image", Description: "Load one local PNG/JPEG/GIF image for visual inspection by an image-capable model. Use for images, screenshots, diagrams, charts, or rendered pages relevant to the task. Maximum 5 MiB and 16 million pixels. Loading does not establish visual quality; text-only models need human review.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string","minLength":1}},"required":["path"],"additionalProperties":false}`)}
}

func (t ViewImageTool) Assess(raw json.RawMessage) (Action, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Action{}, err
	}
	if args.Path == "" {
		return Action{}, errors.New("path is required")
	}
	path, outside, err := t.Guard.ResolveRead(args.Path)
	return Action{Risk: RiskRead, Summary: "view image " + path, Outside: outside, Paths: []string{path}}, err
}

func (t ViewImageTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	r, err := t.ExecuteResultStream(ctx, raw, nil)
	return r.Content, err
}

func (t ViewImageTool) ExecuteResultStream(ctx context.Context, raw json.RawMessage, _ func(string)) (Result, error) {
	action, err := t.Assess(raw)
	if err != nil {
		return Result{}, err
	}
	path := action.Paths[0]
	root := t.Guard.Workspace
	if action.Outside {
		root = filepath.Dir(path)
	}
	target, err := safefile.Open(root, path)
	if err != nil {
		return Result{}, err
	}
	defer target.Close()
	f, err := target.OpenFile()
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 5<<20 {
		return Result{}, errors.New("image must be a nonempty regular file of at most 5 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(artifactContextReader{ctx, f}, (5<<20)+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > 5<<20 {
		return Result{}, errors.New("image exceeds 5 MiB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("decode image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
		return Result{}, errors.New("image exceeds 16 million pixels")
	}
	// Decode once as well: a valid header alone does not prove usable pixels.
	if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
		return Result{}, fmt.Errorf("decode pixels: %w", err)
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	return Result{Content: fmt.Sprintf("Image loaded: %s (%dx%d); sha256:%s. Pixels are supplied when the provider supports images. Visual quality is not machine-validated; inspect the page or request human review.", path, cfg.Width, cfg.Height, hash), Parts: []provider.ContentPart{{Type: provider.ContentImage, Name: filepath.Base(path), MediaType: "image/" + format, Size: len(data), SHA256: hash, Data: data}}}, nil
}
