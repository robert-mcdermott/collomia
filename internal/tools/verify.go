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

// VerificationCommand is one repository-derived build, lint, or test command.
// Purpose is display-only; Command is still assessed and authorized through
// the ordinary run_command policy before execution.
type VerificationCommand struct {
	Purpose string `json:"purpose"`
	Command string `json:"command"`
}

func (t DetectVerificationTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "detect_verification", Description: "Inspect the workspace root for known project markers (Go, Node, Rust, Python including tox/poetry/uv/pipenv, Makefile, R, Ruby, Elixir, PHP, Swift, Gradle, Maven, Deno, Haskell, Bazel) and return the build, lint, and test commands conventionally used for this kind of project. Use this before proposing verification commands instead of guessing.", InputSchema: schema(`{"type":"object","properties":{},"additionalProperties":false}`)}
}

func (t DetectVerificationTool) Assess(json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "detect build/lint/test commands"}, nil
}

func (t DetectVerificationTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	found, suggestions := DetectVerificationCommands(t.Workspace)
	if len(found) == 0 {
		return "No known project markers were found at the workspace root (checked Go, Node, Rust, Python, Makefile, R, Ruby, Elixir, PHP, Swift, Gradle, Maven, Deno, Haskell, and Bazel markers). Ask the user how this project is built and tested, or inspect the repository further before proposing commands.", nil
	}

	var b strings.Builder
	b.WriteString("Detected: " + strings.Join(found, ", ") + "\n")
	b.WriteString("Suggested verification commands (confirm the actual result of each before trusting it):\n")
	for _, suggestion := range suggestions {
		fmt.Fprintf(&b, "- %s: %s\n", suggestion.Purpose, suggestion.Command)
	}
	return b.String(), nil
}

// DetectVerificationCommands returns the same structured repository-derived
// suggestions used by detect_verification. It performs no execution and does
// not inspect outside the supplied workspace root.
func DetectVerificationCommands(workspace string) ([]string, []VerificationCommand) {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(workspace, name))
		return err == nil
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil {
			return ""
		}
		return string(data)
	}

	var found []string
	var suggestions []VerificationCommand

	if exists("go.mod") {
		found = append(found, "go.mod (Go module)")
		suggestions = append(suggestions,
			VerificationCommand{"build", "go build ./..."},
			VerificationCommand{"vet", "go vet ./..."},
			VerificationCommand{"test", "go test ./..."},
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
			suggestions = append(suggestions, VerificationCommand{"build", run + " build"})
		}
		if _, ok := pkg.Scripts["lint"]; ok {
			suggestions = append(suggestions, VerificationCommand{"lint", run + " lint"})
		}
		if _, ok := pkg.Scripts["test"]; ok {
			suggestions = append(suggestions, VerificationCommand{"test", run + " test"})
		} else {
			suggestions = append(suggestions, VerificationCommand{"test", pm + " test"})
		}
	}
	if exists("Cargo.toml") {
		found = append(found, "Cargo.toml (Rust)")
		suggestions = append(suggestions,
			VerificationCommand{"build", "cargo build"},
			VerificationCommand{"lint", "cargo clippy"},
			VerificationCommand{"test", "cargo test"},
		)
	}
	if exists("pyproject.toml") || exists("requirements.txt") || exists("setup.py") || exists("tox.ini") || exists("Pipfile") {
		found = append(found, "Python project")
		text := read("pyproject.toml")
		suggestions = append(suggestions, pythonVerificationCommand(exists, text))
		if strings.Contains(text, "ruff") {
			suggestions = append(suggestions, VerificationCommand{"lint", "ruff check ."})
		}
	}
	if exists("Makefile") {
		found = append(found, "Makefile")
		makefile := read("Makefile")
		for _, target := range []string{"build", "test", "lint", "vet", "check"} {
			if makeHasTarget(makefile, target) {
				suggestions = append(suggestions, VerificationCommand{target, "make " + target})
			}
		}
	}
	// Every ecosystem below contributes its test entry point only. A detected
	// command is one a delegated candidate must pass before it is eligible, so
	// breadth here must not turn into a longer suite per repository.
	if exists("DESCRIPTION") && (exists("tests") || exists("R")) {
		found = append(found, "DESCRIPTION (R package)")
		suggestions = append(suggestions, VerificationCommand{"test", `Rscript -e "testthat::test_local()"`})
	}
	if exists("Gemfile") || exists("Rakefile") || exists(".rspec") {
		found = append(found, "Ruby project")
		if exists("Gemfile") && (exists("spec") || exists(".rspec")) {
			suggestions = append(suggestions, VerificationCommand{"test", "bundle exec rspec"})
		} else if exists("Rakefile") {
			suggestions = append(suggestions, VerificationCommand{"test", "rake test"})
		}
	}
	if exists("mix.exs") {
		found = append(found, "mix.exs (Elixir)")
		suggestions = append(suggestions, VerificationCommand{"test", "mix test"})
	}
	if exists("composer.json") {
		found = append(found, "composer.json (PHP)")
		suggestions = append(suggestions, VerificationCommand{"test", "composer test"})
	}
	if exists("Package.swift") {
		found = append(found, "Package.swift (Swift)")
		suggestions = append(suggestions, VerificationCommand{"test", "swift test"})
	}
	if exists("build.gradle") || exists("build.gradle.kts") || exists("gradlew") {
		found = append(found, "Gradle project")
		command := "gradle test"
		if exists("gradlew") {
			command = "./gradlew test"
		}
		suggestions = append(suggestions, VerificationCommand{"test", command})
	}
	if exists("pom.xml") {
		found = append(found, "pom.xml (Maven)")
		suggestions = append(suggestions, VerificationCommand{"test", "mvn test"})
	}
	if exists("deno.json") || exists("deno.jsonc") {
		found = append(found, "Deno project")
		suggestions = append(suggestions, VerificationCommand{"test", "deno test"})
	}
	if exists("stack.yaml") {
		found = append(found, "stack.yaml (Haskell)")
		suggestions = append(suggestions, VerificationCommand{"test", "stack test"})
	}
	if exists("MODULE.bazel") || exists("WORKSPACE") || exists("WORKSPACE.bazel") {
		found = append(found, "Bazel workspace")
		suggestions = append(suggestions, VerificationCommand{"test", "bazel test //..."})
	}
	return dedupeVerificationCommands(found, suggestions)
}

// pythonVerificationCommand resolves the runner a Python repository actually
// uses. A bare `pytest` in a Poetry, Pipenv, uv, or tox project is the command
// that fails with "no module named pytest" and sends the model looking for
// wrappers, which is what the completion gate then rejects.
func pythonVerificationCommand(exists func(string) bool, pyproject string) VerificationCommand {
	switch {
	case exists("uv.lock"):
		return VerificationCommand{"test", "uv run pytest"}
	case exists("poetry.lock") || strings.Contains(pyproject, "[tool.poetry]"):
		return VerificationCommand{"test", "poetry run pytest"}
	case exists("Pipfile"):
		return VerificationCommand{"test", "pipenv run pytest"}
	case exists("tox.ini"):
		return VerificationCommand{"test", "tox"}
	case exists("noxfile.py"):
		return VerificationCommand{"test", "nox"}
	default:
		return VerificationCommand{"test", "pytest"}
	}
}

func dedupeVerificationCommands(found []string, suggestions []VerificationCommand) ([]string, []VerificationCommand) {
	// A repository can expose the same command through more than one marker.
	// Preserve discovery order but never ask the operator to run it twice.
	seen := make(map[string]bool, len(suggestions))
	unique := suggestions[:0]
	for _, suggestion := range suggestions {
		if seen[suggestion.Command] {
			continue
		}
		seen[suggestion.Command] = true
		unique = append(unique, suggestion)
	}
	return found, unique
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
