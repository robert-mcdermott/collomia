package tools

import (
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
