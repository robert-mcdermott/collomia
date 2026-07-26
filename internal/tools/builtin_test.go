package tools

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

func TestBuiltinsUseCompatibilityFirstSandboxDefault(t *testing.T) {
	registry, _, _, err := Builtins(t.TempDir(), appconfig.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := registry.Get("run_command")
	if !ok {
		t.Fatal("run_command is not registered")
	}
	command, ok := raw.(*RunCommandTool)
	if !ok {
		t.Fatalf("run_command type=%T", raw)
	}
	if command.SandboxMode != sandbox.ModeAuto {
		t.Fatalf("sandbox mode=%q, want auto", command.SandboxMode)
	}
	if !command.AllowNetwork || !command.AllowReadOutsideWorkspace {
		t.Fatalf("compatibility switches: network=%t broad_reads=%t", command.AllowNetwork, command.AllowReadOutsideWorkspace)
	}
	if !command.MinimalEnv {
		t.Fatal("sandboxed commands should use the minimal environment when command_env is omitted")
	}
}

func TestBuiltinsHonorExplicitSandboxOff(t *testing.T) {
	cfg := appconfig.Defaults()
	cfg.Permissions.Sandbox = "off"
	registry, _, _, err := Builtins(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := registry.Get("run_command")
	if !ok {
		t.Fatal("run_command is not registered")
	}
	command, ok := raw.(*RunCommandTool)
	if !ok {
		t.Fatalf("run_command type=%T", raw)
	}
	if command.SandboxMode != sandbox.ModeOff {
		t.Fatalf("sandbox mode=%q, want off", command.SandboxMode)
	}
	if command.MinimalEnv {
		t.Fatal("explicit sandbox off should preserve the full environment when command_env is omitted")
	}
}

// TestRunCommandToolHasOneConfigurationSite fails when a second caller builds
// a command runner directly.
//
// This is not style enforcement. Delegated verification used to configure its
// own runner, so every containment field added afterwards had to be
// remembered in two places — and the one that was forgotten would apply in the
// primary session and be silently absent for delegated agents. Scoped egress
// is exactly such a field. A new caller belongs in ConfiguredRunCommandTool.
func TestRunCommandToolHasOneConfigurationSite(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		// The definition itself, and the single configuration site.
		filepath.Join("internal", "tools", "command.go"): true,
		filepath.Join("internal", "tools", "builtin.go"): true,
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "dist" || name == "collo" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(body), "NewRunCommandTool(") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if !allowed[rel] {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("NewRunCommandTool is called outside ConfiguredRunCommandTool in %v; route it through ConfiguredRunCommandTool so containment settings cannot drift", offenders)
	}
}
