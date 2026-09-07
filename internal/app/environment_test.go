package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestRuntimeEnvironmentDiscoveryAcrossModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	for _, mode := range []string{"developer", "work", "planning", "graph"} {
		t.Run(mode, func(t *testing.T) {
			isolateGlobalFiles(t)
			dir := t.TempDir()
			bin := filepath.Join(dir, "runtime")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\nprintf 'fixture-node\\n'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			opts := Options{Workspace: dir, Ephemeral: true, Autonomy: "autopilot"}
			if mode == "work" {
				opts.TaskMode = "work"
			}
			if mode == "graph" {
				opts.Ephemeral = false
				opts.OrchestratedGoal = &plan.Plan{Goal: "discover runtime", Steps: []plan.Step{{ID: 1, Title: "locate runtime", Acceptance: []string{"native executable lookup reports the runtime path"}}}}
			}
			r, err := New(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if mode == "planning" {
				r.Agent.SetPlan(true)
			}
			client := &scriptedClient{steps: []provider.Response{
				{ToolCalls: []provider.ToolCall{{ID: "lookup", Name: "inspect_environment", Arguments: json.RawMessage(`{"executables":["node"]}`)}}},
				{Content: "The runtime is present on PATH; its version has not been executed."},
			}}
			r.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, client)
			if _, err := r.Agent.Run(t.Context(), "Locate node without executing it", nil); err != nil {
				t.Fatal(err)
			}
			if len(client.requests) != 2 {
				t.Fatalf("unexpected controller churn: %d requests", len(client.requests))
			}
			found := false
			for _, m := range client.requests[1].Messages {
				if m.Role == "tool" && strings.Contains(m.Content, filepath.Join(bin, "node")) {
					found = true
				}
			}
			if !found {
				t.Fatal("native lookup result did not reach the model")
			}
		})
	}
}
