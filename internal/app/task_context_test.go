package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestTaskContextAcrossRuntimeCompactionAndRestart(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("ORIGINAL_EVIDENCE: 30 successes of 40 requests"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := New(t.Context(), Options{Workspace: workspace, TaskMode: "work"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	client := &scriptedClient{steps: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "read", Name: "read_file", Arguments: json.RawMessage(`{"path":"source.txt"}`)}}},
		{ToolCalls: []provider.ToolCall{{ID: "remember", Name: "update_task_context", Arguments: json.RawMessage(`{"expected_revision":0,"objective":"Write a report","sources":[{"reference":"m3","note":"original measured counts"}],"constraints":["Budget 900"],"next_action":"Draft report"}`)}}},
		{Content: "Evidence recorded."},
	}}
	r.Agent.SetProvider("fixture", "model", appconfig.Provider{MaxTokens: 100}, client)
	if _, err := r.Agent.Run(t.Context(), "Use the source to prepare a report for executives, budget 900.", r.LogEvent); err != nil {
		t.Fatal(err)
	}
	if r.Session.TaskContext().Revision != 1 {
		t.Fatal("model context tool not wired")
	}
	// Intentionally lossy summaries cannot replace the durable working record
	// or its separately recorded genuine user corrections.
	for round := 0; round < 2; round++ {
		for i := 0; i < 8; i++ {
			c := &scriptedClient{steps: []provider.Response{{Content: "ack"}}}
			r.Agent.SetProvider("fixture", "model", appconfig.Provider{MaxTokens: 100}, c)
			prompt := "Continue discussion without changing files."
			if round == 0 && i == 0 {
				prompt = "Correction: the audience is volunteers and the budget is 250."
			}
			if _, err := r.Agent.Run(t.Context(), prompt, r.LogEvent); err != nil {
				t.Fatal(err)
			}
		}
		c := &scriptedClient{steps: []provider.Response{{Content: "Summary intentionally omits all source details."}}}
		r.Agent.SetProvider("fixture", "model", appconfig.Provider{MaxTokens: 100}, c)
		if _, err := r.Agent.CompactWithEmit(t.Context(), "", r.LogEvent); err != nil {
			t.Fatal(err)
		}
	}
	id := r.Session.Meta.ID
	r.Close()
	resumed, err := New(t.Context(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	follow := &scriptedClient{steps: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "find", Name: "search_session", Arguments: json.RawMessage(`{"query":"Correction: the audience"}`)}}},
		{ToolCalls: []provider.ToolCall{{ID: "original", Name: "read_session", Arguments: json.RawMessage(`{"id":"m3"}`)}}},
		{Content: "Original evidence recovered."},
	}}
	resumed.Agent.SetProvider("fixture", "model", appconfig.Provider{MaxTokens: 100}, follow)
	if _, err := resumed.Agent.Run(t.Context(), "Retrieve the original evidence and my earlier audience correction.", resumed.LogEvent); err != nil {
		t.Fatal(err)
	}
	sawEvidence, sawCorrection, sawPinned := false, false, false
	for _, req := range follow.requests {
		for _, msg := range req.Messages {
			if msg.Role == "tool" && strings.Contains(msg.Content, "ORIGINAL_EVIDENCE") {
				sawEvidence = true
			}
			if msg.Role == "tool" && strings.Contains(msg.Content, "volunteers") {
				sawCorrection = true
			}
			if msg.Volatile && strings.Contains(msg.Content, "model-authored") && strings.Contains(msg.Content, "Draft report") {
				sawPinned = true
			}
		}
	}
	if !sawEvidence || !sawCorrection || !sawPinned {
		t.Fatalf("evidence=%v correction=%v pinned=%v", sawEvidence, sawCorrection, sawPinned)
	}
	if err := resumed.NewSession(); err != nil {
		t.Fatal(err)
	}
	if resumed.Context.Pinned() != "" {
		t.Fatal("new session leaked previous context")
	}
	if err := resumed.SwitchSession(id); err != nil {
		t.Fatal(err)
	}
	if resumed.Session.TaskContext().Revision != 1 {
		t.Fatal("switch failed to restore notes")
	}
	// Model-authored old budget is retained as a claim, not promoted above
	// the actual correction retrievable from original user history.
	if !strings.Contains(resumed.Context.Pinned(), "take precedence") {
		t.Fatal("missing provenance guard")
	}
}

func TestEphemeralRuntimeOmitsContextTools(t *testing.T) {
	isolateGlobalFiles(t)
	r, err := New(t.Context(), Options{Workspace: t.TempDir(), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, name := range []string{"read_task_context", "update_task_context", "read_session", "search_session"} {
		if _, ok := r.Registry.Get(name); ok {
			t.Fatalf("ephemeral exposed %s", name)
		}
	}
}
