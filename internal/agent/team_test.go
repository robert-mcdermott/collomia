package agent

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestTeamRestoreNeverRestartsActiveState(t *testing.T) {
	team := NewTeam()
	team.Restore([]DelegateStatus{{ID: "old", Name: "old task", Status: DelegateRunning, Usage: provider.Usage{InputTokens: 10}}})
	status, ok := team.Get("old")
	if !ok || status.Status != DelegateInterrupted || status.Error == "" {
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
	team.FinishDetailed("d1", strings.Repeat("s", 20<<10), evidence, changed, "", "", provider.Usage{}, nil)
	status, _ := team.Get("d1")
	if len(status.Evidence) != maxDelegateEvidence || len(status.Changed) != maxDelegateChangedFiles {
		t.Fatalf("unbounded collections: evidence=%d changed=%d", len(status.Evidence), len(status.Changed))
	}
	if len(status.Summary) > 16<<10 || len(status.Task) > 16<<10 || len(status.Evidence[0]) > 1024 || len(status.Changed[0]) > 1024 {
		t.Fatalf("unbounded strings: task=%d summary=%d evidence=%d changed=%d", len(status.Task), len(status.Summary), len(status.Evidence[0]), len(status.Changed[0]))
	}
}
