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
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func TestValidationFormatCorrectionsNeedNoControllerIntervention(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "game.html"), []byte("<canvas>ASTEROIDS</canvas>"), 0600); err != nil {
		t.Fatal(err)
	}
	guard, _ := tools.NewPathGuard(dir, false)
	client := &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
		formats := []string{"html5", "binary", "text"}
		if call <= len(formats) {
			return graphToolResponse(formats[call-1], "validate_artifact", `{"path":"game.html","format":"`+formats[call-1]+`","required_text":["canvas","ASTEROIDS"]}`), nil
		}
		return provider.Response{Content: "File text checked; gameplay not tested."}, nil
	}}
	a := New(Options{Client: client, Workspace: dir, Registry: tools.NewRegistry(tools.ValidateArtifactTool{Guard: guard}), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), TaskMode: taskmode.Work, CompletionPlan: plan.NewBoard()})
	a.SetCompletionStore(&memoryCompletionStore{})
	notices := 0
	_, err := a.Run(t.Context(), "Check my game file", func(e event.Event) {
		if event.CompletionNoticeSummary(e.Text) != "" {
			notices++
		}
	})
	if err != nil || client.calls != 4 || notices != 0 {
		t.Fatalf("calls=%d notices=%d err=%v", client.calls, notices, err)
	}
}

func TestValidationRequirementsSurviveResumeWithoutReceipts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "game.html"), []byte("<canvas>"), 0600); err != nil {
		t.Fatal(err)
	}
	guard, _ := tools.NewPathGuard(dir, false)
	store := &memoryCompletionStore{}
	client := &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
		if call == 1 {
			return graphToolResponse("bad", "validate_artifact", `{"path":"game.html","format":"html5","required_text":["canvas"]}`), nil
		}
		return provider.Response{}, errors.New("fixture connection lost")
	}}
	makeAgent := func(client *fakeClient) *Agent {
		a := New(Options{Client: client, Workspace: dir, Registry: tools.NewRegistry(tools.ValidateArtifactTool{Guard: guard}), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), TaskMode: taskmode.Work, CompletionPlan: plan.NewBoard()})
		a.SetCompletionStore(store)
		return a
	}
	if _, err := makeAgent(client).Run(t.Context(), "Check", nil); err == nil {
		t.Fatal("expected interrupted provider")
	}
	if !strings.Contains(string(store.raw), "validation") || strings.Contains(string(store.raw), "canvas") || strings.Contains(string(store.raw), "digest") {
		t.Fatalf("bad retained requirements: %s", store.raw)
	}
	client = &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
		if call == 1 {
			return graphToolResponse("fixed", "validate_artifact", `{"path":"./game.html","format":"text","required_text":["canvas"]}`), nil
		}
		return provider.Response{Content: "Checked."}, nil
	}}
	if _, err := makeAgent(client).Run(t.Context(), "Continue checking", nil); err != nil || client.calls != 2 {
		t.Fatalf("resume: calls=%d err=%v", client.calls, err)
	}
	var state completionState
	if err := json.Unmarshal(store.raw, &state); err != nil || len(state.Failures) != 0 {
		t.Fatalf("failure not recovered: %s", store.raw)
	}
}

func TestWeakerValidationCannotEraseFailedRequirement(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "game.html"), []byte("<canvas>"), 0600); err != nil {
		t.Fatal(err)
	}
	guard, _ := tools.NewPathGuard(dir, false)
	store := &memoryCompletionStore{}
	client := &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
		if call == 1 {
			return graphToolResponse("missing", "validate_artifact", `{"path":"game.html","format":"text","required_text":["canvas","MISSING_REQUIREMENT"]}`), nil
		}
		if call == 2 {
			return graphToolResponse("weaker", "validate_artifact", `{"path":"game.html","format":"auto","required_text":["canvas"]}`), nil
		}
		return provider.Response{Content: "Done."}, nil
	}}
	a := New(Options{Client: client, Workspace: dir, Registry: tools.NewRegistry(tools.ValidateArtifactTool{Guard: guard}), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), TaskMode: taskmode.Work, CompletionPlan: plan.NewBoard(), CompletionStore: store})
	_, err := a.Run(t.Context(), "Check required content", nil)
	if !errors.Is(err, ErrGoalBlocked) {
		t.Fatalf("weaker check completed: %v", err)
	}
	state, err := decodeCompletion(store.raw)
	if err != nil || len(state.Failures) != 1 || state.Failures[0].ID != "missing" {
		t.Fatalf("failure lost: %s %v", store.raw, err)
	}
}

func TestOptionalValidationIdentityCannotOverflowRecovery(t *testing.T) {
	dir := t.TempDir()
	guard, _ := tools.NewPathGuard(dir, false)
	tool := tools.ValidateArtifactTool{Guard: guard}
	texts := make([]string, 32)
	for i := range texts {
		texts[i] = fmt.Sprintf("requirement-%d", i)
	}
	raw, _ := json.Marshal(map[string]any{"path": "file.txt", "required_text": texts})
	c := newCompletionController(plan.NewBoard(), dir, false, taskmode.Work)
	store := &memoryCompletionStore{}
	c.store = store
	for i := 0; i < 64; i++ {
		c.observe(toolObservation{CallID: fmt.Sprint(i), Name: "validate_artifact", Failed: true, Action: tools.Action{Summary: strings.Repeat("x", 512)}, ValidationRequest: tool.ValidationRequirement(raw)})
	}
	if err := c.saveRecovery(false); err != nil {
		t.Fatal(err)
	}
	state, err := decodeCompletion(store.raw)
	if err != nil || len(state.Failures) != 64 {
		t.Fatalf("obligations lost or oversized: %d bytes: %v", len(store.raw), err)
	}
	if state.Failures[0].Validation != nil {
		t.Fatal("expected optional identities dropped to stay bounded")
	}
}
