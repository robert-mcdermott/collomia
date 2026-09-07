package plan

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Artifact struct {
	Path string `json:"path"`
	Role string `json:"role"`
}

func validateArtifacts(artifacts []Artifact) error {
	if len(artifacts) > 64 {
		return fmt.Errorf("artifacts accepts at most 64 entries")
	}
	seen := map[string]bool{}
	for i, artifact := range artifacts {
		path := strings.TrimSpace(artifact.Path)
		if path == "" || len(path) > 1024 || strings.ContainsAny(path, "\x00\r\n") || filepath.Clean(path) == "." {
			return fmt.Errorf("artifacts[%d] needs a file path of 1–1024 bytes", i)
		}
		if artifact.Role != "deliverable" && artifact.Role != "scratch" {
			return fmt.Errorf("artifacts[%d].role must be deliverable or scratch", i)
		}
		key := filepath.Clean(path)
		if seen[key] {
			return fmt.Errorf("artifacts repeats path %q", path)
		}
		seen[key] = true
	}
	return nil
}
