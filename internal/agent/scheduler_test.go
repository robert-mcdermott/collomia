package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSchedulerEnforcesGlobalAndProviderLimits(t *testing.T) {
	scheduler := NewScheduler(2, map[string]int{"slow": 1})
	releaseSlow, err := scheduler.Acquire(t.Context(), "slow")
	if err != nil {
		t.Fatal(err)
	}
	releaseFast, err := scheduler.Acquire(t.Context(), "fast")
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan func(), 1)
	go func() {
		release, acquireErr := scheduler.Acquire(t.Context(), "slow")
		if acquireErr == nil {
			acquired <- release
		}
	}()
	select {
	case <-acquired:
		t.Fatal("second slow task bypassed provider limit")
	case <-time.After(20 * time.Millisecond):
	}
	releaseFast()
	select {
	case <-acquired:
		t.Fatal("strict FIFO/provider limit should still block the second slow task")
	case <-time.After(20 * time.Millisecond):
	}
	releaseSlow()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("queued task did not acquire after provider slot was released")
	}
}

func TestSchedulerCancellationRemovesQueuedTask(t *testing.T) {
	scheduler := NewScheduler(1, nil)
	release, err := scheduler.Acquire(t.Context(), "p")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, acquireErr := scheduler.Acquire(ctx, "p")
		done <- acquireErr
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation=%v", err)
	}
	release()
	if nextRelease, err := scheduler.Acquire(t.Context(), "p"); err != nil {
		t.Fatal(err)
	} else {
		nextRelease()
	}
}
