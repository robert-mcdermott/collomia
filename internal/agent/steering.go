package agent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Steering bounds. A steering update is a correction, not a second prompt:
// the size limit keeps a pasted file from arriving as guidance, and the depth
// limit keeps a held enter key from filling the conversation with duplicates
// the model then has to reconcile.
const (
	maxPendingSteering    = 8
	maxSteeringTextLength = 4096
)

// ErrSteeringFull is returned when the queue already holds the maximum number
// of undelivered updates. It is a distinct error because the caller's remedy
// is to wait for the next iteration rather than to shorten the text.
var ErrSteeringFull = fmt.Errorf("at most %d steering updates can be waiting at once", maxPendingSteering)

// SteeringQueue holds guidance for a running agent until it reaches an
// iteration boundary.
//
// The primary session and delegated children reach the same Agent hook by
// different routes: a child's guidance is durable team state that survives a
// status snapshot, while the user's own guidance for the primary agent is
// process-local and only has to live until the next provider call. This is
// the second one.
//
// Delivery is deliberately not immediate. Guidance cannot interrupt an
// in-flight provider call, an executing tool, or a pending permission
// decision — it is installed between iterations, where the conversation is
// consistent and the agent is about to decide what to do next. Anything else
// would mean mutating the message list underneath a request.
type SteeringQueue struct {
	mu      sync.Mutex
	pending []string
	// onChange reports the new depth after every add and drain, so a UI can
	// show what is waiting without polling.
	onChange func(int)
}

func NewSteeringQueue() *SteeringQueue { return &SteeringQueue{} }

// Observe registers the depth callback. It fires outside the lock so an
// observer may call back into the queue.
func (q *SteeringQueue) Observe(fn func(int)) {
	q.mu.Lock()
	q.onChange = fn
	q.mu.Unlock()
}

// Add queues one update. It reports an error rather than silently dropping:
// guidance the user believes was delivered, and was not, is worse than a
// refusal they can see.
func (q *SteeringQueue) Add(guidance string) error {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return errors.New("steering guidance is empty")
	}
	if len(guidance) > maxSteeringTextLength {
		return fmt.Errorf("steering guidance is %d characters; the limit is %d — send it as a new prompt after this turn instead", len(guidance), maxSteeringTextLength)
	}
	q.mu.Lock()
	if len(q.pending) >= maxPendingSteering {
		q.mu.Unlock()
		return ErrSteeringFull
	}
	q.pending = append(q.pending, guidance)
	depth, observer := len(q.pending), q.onChange
	q.mu.Unlock()
	if observer != nil {
		observer(depth)
	}
	return nil
}

// Take drains every pending update exactly once. Draining rather than reading
// is what keeps guidance from being appended to the conversation on every
// subsequent iteration of the same turn.
func (q *SteeringQueue) Take() []string {
	q.mu.Lock()
	if len(q.pending) == 0 {
		q.mu.Unlock()
		return nil
	}
	out := q.pending
	q.pending = nil
	observer := q.onChange
	q.mu.Unlock()
	if observer != nil {
		observer(0)
	}
	return out
}

// Pending reports how many updates are waiting for the next iteration.
func (q *SteeringQueue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Clear discards undelivered guidance. A cancelled or abandoned turn must not
// leave steering behind to surface in an unrelated later one.
func (q *SteeringQueue) Clear() {
	q.mu.Lock()
	had := len(q.pending) > 0
	q.pending = nil
	observer := q.onChange
	q.mu.Unlock()
	if had && observer != nil {
		observer(0)
	}
}
