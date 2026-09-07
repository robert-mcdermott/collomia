package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Supply a default scope before assessment/authorization/execution, never
// upgrade an old successful command into evidence. Explicit scopes win.
// Only unambiguous project-wide builds qualify; selectors, wrappers, pipelines
// and effectful preparation retain the ordinary explicit-verification path.
func automaticProjectVerification(raw json.RawMessage, workspace string) json.RawMessage {
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil || args == nil {
		return raw
	}
	if _, explicit := args["verification"]; explicit {
		return raw
	}
	var command string
	if json.Unmarshal(args["command"], &command) != nil {
		return raw
	}
	if _, refusal := scopedVerificationChain(command, workspace); refusal != "" {
		return raw
	}
	parts := strings.Split(command, "&&")
	cwd := workspace
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		// Quoted && or a shell program must never change what we infer.
		if !strings.HasPrefix(part, "cd ") {
			return raw
		}
		next, ok := verificationDirectory(part, cwd, workspace)
		if !ok {
			return raw
		}
		cwd = next
	}
	build := strings.Join(strings.Fields(parts[len(parts)-1]), " ")
	manifest := ""
	switch build {
	case "npm run build", "pnpm build", "pnpm run build", "yarn build", "yarn run build", "bun run build":
		manifest = "package.json"
	case "go build ./...":
		manifest = "go.mod"
	case "cargo build":
		manifest = "Cargo.toml"
	default:
		return raw
	}
	// Existence/type metadata only. The normal guard and child-path permission
	// checks must authorize all actual reads before snapshotProject hashes them.
	info, err := os.Lstat(filepath.Join(cwd, manifest))
	if err != nil || !info.Mode().IsRegular() {
		return raw
	}
	args["verification"], _ = json.Marshal(map[string]any{
		"paths":   []string{cwd},
		"purpose": "Project build: " + build + " (automatic project-input scope)",
	})
	out, err := json.Marshal(args)
	if err != nil {
		return raw
	}
	return out
}
