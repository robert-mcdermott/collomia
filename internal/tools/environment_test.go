package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func TestInheritedRuntimePATHAcrossCommandSurfaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures; Windows keeps its existing cmd contract")
	}
	workspace := t.TempDir()
	bin := filepath.Join(workspace, "runtime bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf 'fixture-"+name+"\\n'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	path := bin + string(os.PathListSeparator) + "/usr/bin:/bin"
	t.Setenv("PATH", path)
	t.Setenv("COLLO_TEST_SECRET", "must-not-reach-child")
	out, err := (InspectEnvironmentTool{}).Execute(t.Context(), json.RawMessage(`{"executables":["node","npm","collo-nonexistent-runtime"]}`))
	if err != nil || !strings.Contains(out, filepath.Join(bin, "node")) || !strings.Contains(out, "not proof it is uninstalled") {
		t.Fatalf("discovery: %s %v", out, err)
	}
	for _, minimal := range []bool{false, true} {
		cfg := appconfig.Defaults()
		cfg.Permissions.Sandbox = "off"
		cfg.Permissions.CommandEnv = "inherit"
		if minimal {
			cfg.Permissions.CommandEnv = "minimal"
		}
		runner, err := ConfiguredRunCommandTool(workspace, cfg, 8192)
		if err != nil {
			t.Fatal(err)
		}
		// Same configured runner backs Standard modes, graph primary and
		// delegate/combined verification; no separate execution environment.
		for _, pty := range []bool{false, true} {
			command := `node --version && npm --version && printf 'path=%s\nsecret=%s\n' "$PATH" "$COLLO_TEST_SECRET"`
			raw, _ := json.Marshal(map[string]any{"command": command, "pty": pty})
			out, err := runner.Execute(t.Context(), raw)
			if err != nil || !strings.Contains(out, "fixture-node") || !strings.Contains(out, "fixture-npm") || !strings.Contains(out, "path="+path) {
				t.Fatalf("minimal=%v pty=%v: %s %v", minimal, pty, out, err)
			}
			if minimal && strings.Contains(out, "must-not-reach-child") {
				t.Fatal("minimal environment leaked secret")
			}
		}
		manager := NewProcessManager()
		t.Cleanup(manager.StopAll)
		p, err := manager.start(runner, `node --version && npm --version && printf 'path=%s\n' "$PATH"`)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-p.doneCh:
		case <-time.After(5 * time.Second):
			t.Fatal("background command did not exit")
		}
		p.mu.Lock()
		out = p.output.String()
		p.mu.Unlock()
		if !strings.Contains(out, "fixture-node") || !strings.Contains(out, "fixture-npm") || !strings.Contains(out, "path="+path) {
			t.Fatalf("background: %s", out)
		}
	}
}

func TestEnvironmentDiscoveryNeverExecutesOrReadsPrograms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	dir := t.TempDir()
	canary := filepath.Join(dir, "executed")
	if err := os.WriteFile(filepath.Join(dir, "probe"), []byte("#!/bin/sh\ntouch '"+canary+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	tool := InspectEnvironmentTool{}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"executables":["probe"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(canary); !os.IsNotExist(err) {
		t.Fatal("lookup executed the program")
	}
	for _, name := range []string{"../probe", "/bin/sh", "probe;touch x", "$(probe)", "node --version", "~/.ssh/id_rsa", ""} {
		raw, _ := json.Marshal(map[string]any{"executables": []string{name}})
		if _, err := tool.Assess(raw); err == nil {
			t.Fatalf("accepted command/path %q", name)
		}
		if _, err := tool.Execute(t.Context(), raw); err == nil {
			t.Fatalf("executed invalid name %q", name)
		}
	}
}
