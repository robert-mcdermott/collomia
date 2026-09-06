package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

// ArtifactValidationRequirement retains bounded identities, not source text or
// validation receipts. It permits a stronger corrected native check to recover
// a failed attempt without relaxing another tool's exact-operation contract.
type ArtifactValidationRequirement struct {
	PathHash   string   `json:"path_hash"`
	Format     string   `json:"format"`
	MinBytes   int64    `json:"min_bytes,omitempty"`
	TextHashes []string `json:"text_hashes,omitempty"`
}

func validationHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func (t ValidateArtifactTool) ValidationRequirement(raw json.RawMessage) *ArtifactValidationRequirement {
	if len(raw) > 32<<10 || t.Guard == nil {
		return nil
	}
	var args validateArtifactArgs
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&args) != nil {
		return nil
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return nil
	}
	// Unsupported format names have no executable checks. Retain the native
	// inferred format's requirements, so a typo cannot waive JSON/Office parsing.
	format := strings.ToLower(strings.TrimSpace(args.Format))
	switch format {
	case "", "auto", "text", "html", "markdown", "json", "csv", "xlsx", "docx", "pptx", "pdf", "binary":
	default:
		args.Format = "auto"
	}
	checked, _ := json.Marshal(args)
	if _, err := parseValidateArtifactArgs(checked); err != nil {
		return nil
	}
	p := args.Path
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.Guard.Workspace, p)
	}
	p = filepath.Clean(p)
	req := &ArtifactValidationRequirement{PathHash: validationHash(p), Format: artifactFormat(args.Format, p), MinBytes: args.MinBytes}
	for _, text := range args.RequiredText {
		req.TextHashes = append(req.TextHashes, validationHash(text))
	}
	slices.Sort(req.TextHashes)
	req.TextHashes = slices.Compact(req.TextHashes)
	return req
}

func (r *ArtifactValidationRequirement) Covers(prior *ArtifactValidationRequirement) bool {
	if r == nil || prior == nil || r.PathHash == "" || r.PathHash != prior.PathHash || r.MinBytes < prior.MinBytes {
		return false
	}
	// A binary check asserts only existence/size/digest. Parsing a format is
	// stronger; the reverse, or replacing one parser with another, is not.
	if r.Format != prior.Format && prior.Format != "binary" {
		return false
	}
	for _, hash := range prior.TextHashes {
		if !slices.Contains(r.TextHashes, hash) {
			return false
		}
	}
	return true
}
