package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/failureid"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestTeamStopCancelsOnlySelectedTask(t *testing.T) {
	firstCtx, firstCancel := context.WithCancel(t.Context())
	secondCtx, secondCancel := context.WithCancel(t.Context())
	defer firstCancel()
	defer secondCancel()
	team := NewTeam()
	team.Enqueue(DelegateStart{ID: "one", Name: "first", Cancel: firstCancel})
	team.Enqueue(DelegateStart{ID: "two", Name: "second", Cancel: secondCancel})
	team.MarkRunning("one")
	team.MarkRunning("two")
	if err := team.Stop("one"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstCtx.Done():
	default:
		t.Fatal("selected task context was not cancelled")
	}
	select {
	case <-secondCtx.Done():
		t.Fatal("stopping one task cancelled its sibling")
	default:
	}
	status, _ := team.Get("one")
	if status.Status != DelegateCancelling {
		t.Fatalf("status=%s", status.Status)
	}
	team.SetAction("one", "late provider update")
	team.SetWaitingApproval("one", "late approval update")
	team.MarkRunning("one")
	status, _ = team.Get("one")
	if status.Status != DelegateCancelling {
		t.Fatalf("late updates revived cancelling task: %+v", status)
	}
}

func TestTeamStopAllCancelsOnlyActiveTasks(t *testing.T) {
	team := NewTeam()
	firstCancelled := make(chan struct{}, 1)
	secondCancelled := make(chan struct{}, 1)
	team.Enqueue(DelegateStart{ID: "first", Name: "first", Cancel: func() { firstCancelled <- struct{}{} }})
	team.Enqueue(DelegateStart{ID: "second", Name: "second", Cancel: func() { secondCancelled <- struct{}{} }})
	team.Enqueue(DelegateStart{ID: "done", Name: "done"})
	team.Finish("done", "complete", nil, "", "", nil)

	team.StopAll()
	for name, cancelled := range map[string]<-chan struct{}{
		"first":  firstCancelled,
		"second": secondCancelled,
	} {
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatalf("%s task was not cancelled", name)
		}
	}
	if status, _ := team.Get("done"); status.Status != DelegateDone {
		t.Fatalf("completed status changed: %+v", status)
	}
}

func TestTeamCancellationRaceCannotReviveTask(t *testing.T) {
	for attempt := 0; attempt < 64; attempt++ {
		team := NewTeam()
		ctx, cancel := context.WithCancel(t.Context())
		team.Enqueue(DelegateStart{ID: "race", Name: "worker", Cancel: cancel})
		team.MarkRunning("race")
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = team.Stop("race")
		}()
		go func() {
			defer wg.Done()
			<-start
			team.SetAction("race", "late provider boundary")
			team.SetWaitingApproval("race", "late approval")
			team.MarkRunning("race")
		}()
		close(start)
		wg.Wait()
		select {
		case <-ctx.Done():
		default:
			t.Fatalf("attempt %d did not cancel task context", attempt)
		}
		status, _ := team.Get("race")
		if status.Status != DelegateCancelling {
			t.Fatalf("attempt %d revived task: %+v", attempt, status)
		}
	}
}

func TestTeamRestoreNeverRestartsActiveState(t *testing.T) {
	team := NewTeam()
	team.Restore([]DelegateStatus{{ID: "old", Name: "old task", Status: DelegateRunning, Usage: provider.Usage{InputTokens: 10}}})
	status, ok := team.Get("old")
	if !ok || status.Status != DelegateInterrupted || status.Error == "" || !failureid.Valid(status.FailureID) {
		t.Fatalf("restored=%+v", status)
	}
	if team.Active() != 0 {
		t.Fatal("interrupted restored task must not count as active")
	}
}

func TestTeamBoundsDurableDelegatePayloads(t *testing.T) {
	team := NewTeam()
	team.Start("d1", "bounded", strings.Repeat("task", 5000), false)
	evidence := make([]string, maxDelegateEvidence+3)
	changed := make([]string, maxDelegateChangedFiles+3)
	for i := range evidence {
		evidence[i] = strings.Repeat("e", 2048)
	}
	for i := range changed {
		changed[i] = strings.Repeat("p", 2048)
	}
	team.FinishDetailed("d1", strings.Repeat("s", 20<<10), evidence, changed, "", "", "", provider.Usage{}, nil)
	status, _ := team.Get("d1")
	if len(status.Evidence) != maxDelegateEvidence || len(status.Changed) != maxDelegateChangedFiles {
		t.Fatalf("unbounded collections: evidence=%d changed=%d", len(status.Evidence), len(status.Changed))
	}
	if len(status.Summary) > 16<<10 || len(status.Task) > 16<<10 || len(status.Evidence[0]) > 1024 || len(status.Changed[0]) > 1024 {
		t.Fatalf("unbounded strings: task=%d summary=%d evidence=%d changed=%d", len(status.Task), len(status.Summary), len(status.Evidence[0]), len(status.Changed[0]))
	}
}

func TestTeamSteeringIsBoundedDurableAndDrainedOnce(t *testing.T) {
	team := NewTeam()
	team.Enqueue(DelegateStart{ID: "d1", Name: "review"})
	team.MarkRunning("d1")
	if err := team.Steer("d1", "focus on the parser boundary"); err != nil {
		t.Fatal(err)
	}
	status, _ := team.Get("d1")
	if status.PendingGuidance != 1 || len(status.Guidance) != 1 || status.Guidance[0] != "focus on the parser boundary" {
		t.Fatalf("queued steering=%+v", status)
	}
	guidance := team.TakeSteering("d1")
	if len(guidance) != 1 || guidance[0] != "focus on the parser boundary" {
		t.Fatalf("drained=%q", guidance)
	}
	if again := team.TakeSteering("d1"); len(again) != 0 {
		t.Fatalf("guidance delivered twice: %q", again)
	}
	status, _ = team.Get("d1")
	if status.PendingGuidance != 0 || len(status.Guidance) != 1 {
		t.Fatalf("durable guidance history lost: %+v", status)
	}
}

func TestTeamRecentOutputKeepsOnlyBoundedTail(t *testing.T) {
	team := NewTeam()
	team.Start("d1", "review", "review", false)
	team.AppendOutput("d1", strings.Repeat("x", maxDelegateRecentOutput+200))
	status, _ := team.Get("d1")
	if len(status.RecentOutput) > maxDelegateRecentOutput+len("…") || !strings.HasPrefix(status.RecentOutput, "…") {
		t.Fatalf("recent output was not a bounded tail: bytes=%d prefix=%q", len(status.RecentOutput), status.RecentOutput[:min(4, len(status.RecentOutput))])
	}
}

func TestDelegateStatusEventRoundTripPreservesOperatorMetadata(t *testing.T) {
	original := DelegateStatus{
		ID: "d1", Name: "writer", Task: "implement", Write: true, PlanStep: 3,
		Status: DelegateDone, RecentOutput: "tests passed", Guidance: []string{"run the focused test"},
		Summary: "done", FailureID: "err-0123456789abcdef", Changed: []string{"a.go"}, Integrated: []string{"a.go"},
		Worktree: "/tmp/worktree", Branch: "collomia/writer", BaseCommit: "abcdef",
		IntegrationStatus: "partial", IntegrationError: "one hunk remains",
		VerificationStatus: "passed", VerificationToken: "verify-abc", VerificationRequired: []string{"go test ./..."},
		VerificationResults: []DelegateVerification{{Purpose: "test", Command: "go test ./...", Status: "passed", Output: "ok", StateToken: "verify-abc"}},
	}
	restored := DelegateStatusFromEvent(original.Event())
	if restored.PlanStep != 3 || restored.RecentOutput != "tests passed" || restored.FailureID != original.FailureID || len(restored.Guidance) != 1 || restored.BaseCommit != "abcdef" || len(restored.Integrated) != 1 || restored.IntegrationStatus != "partial" || restored.IntegrationError != "one hunk remains" || restored.VerificationStatus != "passed" || len(restored.VerificationResults) != 1 || restored.VerificationResults[0].Command != "go test ./..." {
		t.Fatalf("restored=%+v", restored)
	}
}

func TestTeamVerificationRequiresCompleteFreshSuite(t *testing.T) {
	team := NewTeam()
	team.Start("d1", "writer", "write", true)
	team.Finish("d1", "done", []string{"a.go"}, "/tmp/worktree", "collomia/writer", nil)
	required := []string{"go vet ./...", "go test ./..."}
	team.MarkVerificationRunning("d1", "verify-one", required, required[0])
	team.MarkVerificationResult("d1", "verify-one", required, DelegateVerification{Command: required[0], Status: "passed", StateToken: "verify-one"})
	status, _ := team.Get("d1")
	if status.VerificationStatus != "partial" {
		t.Fatalf("one command must not verify the suite: %+v", status)
	}
	team.MarkVerificationResult("d1", "verify-one", required, DelegateVerification{Command: required[1], Status: "passed", StateToken: "verify-one"})
	status, _ = team.Get("d1")
	if status.VerificationStatus != "passed" {
		t.Fatalf("complete suite=%+v", status)
	}
	team.MarkVerificationStale("d1", "child changed")
	status, _ = team.Get("d1")
	if status.VerificationStatus != "stale" || len(status.VerificationResults) != 2 {
		t.Fatalf("stale evidence was not retained: %+v", status)
	}
	team.MarkVerificationRunning("d1", "verify-two", required, required[0])
	status, _ = team.Get("d1")
	if status.VerificationStatus != "running" || len(status.VerificationResults) != 0 {
		t.Fatalf("new child state retained old results: %+v", status)
	}
}

func TestTeamTracksDelegateIntegrationDisposition(t *testing.T) {
	team := NewTeam()
	team.Start("d1", "writer", "write", true)
	team.Finish("d1", "done", []string{"a.go"}, "/tmp/worktree", "collomia/writer", nil)
	team.MarkIntegrationReview("d1", "reviewed", "")
	team.MarkIntegrationOutcome("d1", "partial", []string{"a.go"}, "one hunk remains")
	status, _ := team.Get("d1")
	if status.IntegrationStatus != "partial" || status.IntegrationError != "one hunk remains" || len(status.Integrated) != 1 {
		t.Fatalf("status=%+v", status)
	}
}

func TestTeamFailureCarriesCorrelationID(t *testing.T) {
	team := NewTeam()
	team.Start("d1", "writer", "write", true)
	team.Finish("d1", "", nil, "", "", context.Canceled)
	status, ok := team.Get("d1")
	if !ok || status.Status != DelegateCancelled || !failureid.Valid(status.FailureID) {
		t.Fatalf("status=%+v", status)
	}
	restored := DelegateStatusFromEvent(status.Event())
	if restored.FailureID != status.FailureID {
		t.Fatalf("restored failure id=%q want %q", restored.FailureID, status.FailureID)
	}
}
