package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/provider"
)

// DetectVerificationTool inspects the workspace root for known project
// files and reports the build/lint/test commands conventionally used for
// that kind of project, so the agent can propose real commands instead of
// guessing at them.
type DetectVerificationTool struct{ Workspace string }

func (t DetectVerificationTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "detect_verification", Description: "Inspect the workspace root for known project files (go.mod, package.json, Cargo.toml, pyproject.toml/requirements.txt, Makefile) and return the build, lint, and test commands conventionally used for this kind of project. Use this before proposing verification commands instead of guessing.", InputSchema: schema(`{"type":"object","properties":{},"additionalProperties":false}`)}
}

func (t DetectVerificationTool) Assess(json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "detect build/lint/test commands"}, nil
}

func (t DetectVerificationTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	type suggestion struct{ purpose, command string }
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(t.Workspace, name))
		return err == nil
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(t.Workspace, name))
		if err != nil {
			return ""
		}
		return string(data)
	}

	var found []string
	var suggestions []suggestion

	if exists("go.mod") {
		found = append(found, "go.mod (Go module)")
		suggestions = append(suggestions,
			suggestion{"build", "go build ./..."},
			suggestion{"vet", "go vet ./..."},
			suggestion{"test", "go test ./..."},
		)
	}
	if exists("package.json") {
		found = append(found, "package.json (Node)")
		pm, run := "npm", "npm run"
		switch {
		case exists("pnpm-lock.yaml"):
			pm, run = "pnpm", "pnpm run"
		case exists("yarn.lock"):
			pm, run = "yarn", "yarn"
		}
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		_ = json.Unmarshal([]byte(read("package.json")), &pkg)
		if _, ok := pkg.Scripts["build"]; ok {
			suggestions = append(suggestions, suggestion{"build", run + " build"})
		}
		if _, ok := pkg.Scripts["lint"]; ok {
			suggestions = append(suggestions, suggestion{"lint", run + " lint"})
		}
		if _, ok := pkg.Scripts["test"]; ok {
			suggestions = append(suggestions, suggestion{"test", run + " test"})
		} else {
			suggestions = append(suggestions, suggestion{"test", pm + " test"})
		}
	}
	if exists("Cargo.toml") {
		found = append(found, "Cargo.toml (Rust)")
		suggestions = append(suggestions,
			suggestion{"build", "cargo build"},
			suggestion{"lint", "cargo clippy"},
			suggestion{"test", "cargo test"},
		)
	}
	if exists("pyproject.toml") || exists("requirements.txt") || exists("setup.py") {
		found = append(found, "Python project")
		suggestions = append(suggestions, suggestion{"test", "pytest"})
		text := read("pyproject.toml")
		if strings.Contains(text, "ruff") {
			suggestions = append(suggestions, suggestion{"lint", "ruff check ."})
		}
	}
	if exists("Makefile") {
		found = append(found, "Makefile")
		makefile := read("Makefile")
		for _, target := range []string{"build", "test", "lint", "vet", "check"} {
			if makeHasTarget(makefile, target) {
				suggestions = append(suggestions, suggestion{target, "make " + target})
			}
		}
	}

	if len(found) == 0 {
		return "No known project files were found at the workspace root (checked go.mod, package.json, Cargo.toml, pyproject.toml/requirements.txt, Makefile). Ask the user how this project is built and tested, or inspect the repository further before proposing commands.", nil
	}

	var b strings.Builder
	b.WriteString("Detected: " + strings.Join(found, ", ") + "\n")
	b.WriteString("Suggested verification commands (confirm the actual result of each before trusting it):\n")
	for _, s := range suggestions {
		fmt.Fprintf(&b, "- %s: %s\n", s.purpose, s.command)
	}
	return b.String(), nil
}

// makeHasTarget does a line-oriented scan for a "target:" rule, tolerating
// prerequisites after the colon (e.g. "test: build").
func makeHasTarget(makefile, target string) bool {
	for _, line := range strings.Split(makefile, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, target+":") {
			return true
		}
	}
	return false
}
