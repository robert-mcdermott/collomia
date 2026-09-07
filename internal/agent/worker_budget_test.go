package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func TestGraphWorkerHonorsItsRecordedIterationLease(t *testing.T) {
	for _, tc := range []struct{ grant, profile, want int }{{3, 0, 3}, {40, 0, 40}, {40, 4, 4}} {
		t.Run(fmt.Sprintf("grant%d-profile%d", tc.grant, tc.profile), func(t *testing.T) {
			a, _, dir := scopedFixture(t, taskmode.Developer)
			guard, guardErr := tools.NewPathGuard(dir, false)
			if guardErr != nil {
				t.Fatal(guardErr)
			}
			a.registry.Add(tools.ReadFileTool{Guard: guard})
			var content strings.Builder
			for i := 1; i <= 100; i++ {
				fmt.Fprintf(&content, "fact %d\n", i)
			}
			if err := os.WriteFile(filepath.Join(dir, "facts.txt"), []byte(content.String()), 0600); err != nil {
				t.Fatal(err)
			}
			client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
				return provider.Response{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("read-%d", call), Name: "read_file", Arguments: json.RawMessage(fmt.Sprintf(`{"path":"facts.txt","offset":%d,"limit":1}`, call))}}}, nil
			}}
			a.client = client
			cfg := appconfig.Defaults()
			cfg.Permissions.Mode = "autopilot"
			_, _, _, _, _, _, _, iterations, successes, _, err := a.runDelegateTask(t.Context(), "worker", DelegateTask{Name: "read", Task: "inspect facts", GraphNode: true, MaxIterationsOverride: tc.grant}, appconfig.AgentDefinition{MaxIterations: tc.profile}, cfg, nil, nil)
			if !errors.Is(err, ErrIterationBudgetExceeded) || iterations != tc.want || successes != tc.want || client.calls != tc.want {
				t.Fatalf("lease %d profile %d: calls=%d iterations=%d tools=%d err=%v", tc.grant, tc.profile, client.calls, iterations, successes, err)
			}
			if strings.Contains(err.Error(), "/limits") || !strings.Contains(err.Error(), "/orchestrate extend") {
				t.Fatal("worker stop gave the wrong continuation command", err)
			}
		})
	}
}

func TestGraphWorkerCompactionSharesRequestLease(t *testing.T) {
	a, _, _ := scopedFixture(t, taskmode.Developer)
	a.graphWorker, a.subagent, a.maxTurnIterations = true, true, 2
	client := &fakeClient{chat: func(_ int, _ provider.Request) (provider.Response, error) {
		return provider.Response{Content: "Summary of inspected facts."}, nil
	}}
	a.client = client
	if _, err := a.Run(t.Context(), "Summarize the task", nil); err != nil {
		t.Fatal(err)
	}
	history := make([]provider.Message, compactKeepRecent+3)
	for i := range history {
		history[i] = provider.Message{Role: "user", Content: "Previously inspected fact."}
	}
	a.SetMessages(history)
	if _, err := a.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	a.SetMessages(history)
	if _, err := a.Compact(t.Context(), ""); !errors.Is(err, ErrIterationBudgetExceeded) {
		t.Fatalf("compaction escaped worker lease: %v", err)
	}
	if _, err := a.Run(t.Context(), "Continue", nil); !errors.Is(err, ErrIterationBudgetExceeded) {
		t.Fatalf("generation escaped worker lease: %v", err)
	}
	if client.calls != 2 || a.ProviderIterations() != 2 {
		t.Fatalf("calls=%d accounting=%d; want 2", client.calls, a.ProviderIterations())
	}
}
