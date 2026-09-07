package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/safefile"
)

func ValidArtifactDigest(digest string) bool {
	if !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	bytes, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return err == nil && len(bytes) == sha256.Size
}

// RecheckArtifactDigest reads only the exact target previously authorized for
// validation. The original spelling must still resolve there. os.Root prevents
// a last-component symlink swap escaping the authorized parent while opening.
// This is a bounded point-in-time byte check, not a filesystem lock or a new
// structural, factual, visual, or remote-state validation.
func RecheckArtifactDigest(ctx context.Context, path, target string, expectedRoot ...safefile.RootIdentity) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("artifact is missing or inaccessible")
	}
	if filepath.Clean(resolved) != filepath.Clean(target) {
		return "", fmt.Errorf("artifact path now resolves to a different target")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("artifact is missing or is not a regular file")
	}
	root, err := safefile.Open(filepath.Dir(target), target)
	if err != nil {
		return "", fmt.Errorf("artifact parent is inaccessible")
	}
	defer root.Close()
	if len(expectedRoot) > 0 {
		identity, err := root.RootIdentity()
		if err != nil || !expectedRoot[0].Same(identity) {
			return "", fmt.Errorf("artifact parent changed since validation")
		}
	}
	file, err := root.OpenFile()
	if err != nil {
		return "", fmt.Errorf("artifact is inaccessible")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxArtifactValidationBytes {
		return "", fmt.Errorf("artifact must remain a regular file of at most %d bytes", maxArtifactValidationBytes)
	}
	hash := sha256.New()
	reader := io.LimitReader(file, maxArtifactValidationBytes+1)
	buffer := make([]byte, 64<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := reader.Read(buffer)
		size += int64(n)
		if size > maxArtifactValidationBytes {
			return "", fmt.Errorf("artifact grew beyond the validation size limit")
		}
		_, _ = hash.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("artifact digest read failed")
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

type artifactContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r artifactContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
