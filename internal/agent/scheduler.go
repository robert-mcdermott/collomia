package agent

import (
	"context"
	"sync"
)

// Scheduler is a session-wide FIFO admission controller for delegated tasks.
// A provider-specific limit can tighten, but never exceed, the global bound.
// Queue time is part of each task's context deadline.
type Scheduler struct {
	mu               sync.Mutex
	max              int
	perProvider      map[string]int
	active           int
	activeByProvider map[string]int
	queue            []*scheduleWaiter
}

type scheduleWaiter struct {
	provider string
	ready    chan struct{}
	started  bool
	released bool
}

func NewScheduler(maxConcurrent int, perProvider map[string]int) *Scheduler {
	if maxConcurrent <= 0 || maxConcurrent > maxDelegateTasks {
		maxConcurrent = maxDelegateConcurrency
	}
	limits := make(map[string]int, len(perProvider))
	for provider, limit := range perProvider {
		if limit > 0 {
			limits[provider] = min(limit, maxConcurrent)
		}
	}
	return &Scheduler{max: maxConcurrent, perProvider: limits, activeByProvider: map[string]int{}}
}

// Acquire waits for a global/provider slot. The returned release function is
// idempotent and must be called when the task finishes.
func (s *Scheduler) Acquire(ctx context.Context, provider string) (func(), error) {
	waiter := &scheduleWaiter{provider: provider, ready: make(chan struct{})}
	s.mu.Lock()
	s.queue = append(s.queue, waiter)
	s.dispatchLocked()
	s.mu.Unlock()

	select {
	case <-waiter.ready:
		return func() { s.release(waiter) }, nil
	case <-ctx.Done():
		s.mu.Lock()
		if waiter.started && !waiter.released {
			waiter.released = true
			s.active--
			s.activeByProvider[provider]--
		} else if !waiter.started {
			for i, queued := range s.queue {
				if queued == waiter {
					s.queue = append(s.queue[:i], s.queue[i+1:]...)
					break
				}
			}
		}
		s.dispatchLocked()
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *Scheduler) release(waiter *scheduleWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !waiter.started || waiter.released {
		return
	}
	waiter.released = true
	s.active--
	s.activeByProvider[waiter.provider]--
	s.dispatchLocked()
}

func (s *Scheduler) dispatchLocked() {
	for len(s.queue) > 0 && s.active < s.max {
		waiter := s.queue[0]
		limit := s.max
		if configured := s.perProvider[waiter.provider]; configured > 0 {
			limit = configured
		}
		// Strict FIFO: a saturated provider keeps later requests queued rather
		// than letting one caller repeatedly jump ahead of another.
		if s.activeByProvider[waiter.provider] >= limit {
			return
		}
		s.queue = s.queue[1:]
		waiter.started = true
		s.active++
		s.activeByProvider[waiter.provider]++
		close(waiter.ready)
	}
}
