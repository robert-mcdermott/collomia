package agent

import (
	"sync"
	"time"
)

// DelegateStatus is a snapshot of one delegated (sub-agent) task, for the
// TUI's Agents panel and headless observability. It survives after the
// task finishes so the user can review outcomes once the run scrolls by.
type DelegateStatus struct {
	ID       string
	Name     string
	Task     string
	Write    bool
	Status   string // running, done, error
	Summary  string
	Changed  []string
	Worktree string
	Branch   string
	Started  time.Time
	Finished time.Time
}

// Team tracks the delegated tasks spawned by a session's delegate tool,
// including any still running concurrently. It is the "parent inbox":
// structured status per child rather than raw child transcripts.
type Team struct {
	mu    sync.RWMutex
	items map[string]*DelegateStatus
	order []string
}

func NewTeam() *Team { return &Team{items: map[string]*DelegateStatus{}} }

// Start registers a newly launched delegated task.
func (t *Team) Start(id, name, task string, write bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.items[id] = &DelegateStatus{ID: id, Name: name, Task: task, Write: write, Status: "running", Started: time.Now()}
	t.order = append(t.order, id)
}

// Finish records the outcome of a delegated task.
func (t *Team) Finish(id, summary string, changed []string, worktree, branch string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.items[id]
	if !ok {
		return
	}
	s.Finished = time.Now()
	s.Summary = summary
	s.Changed = changed
	s.Worktree = worktree
	s.Branch = branch
	if err != nil {
		s.Status = "error"
		s.Summary = err.Error()
	} else {
		s.Status = "done"
	}
}

// Snapshot returns every tracked task, oldest first.
func (t *Team) Snapshot() []DelegateStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]DelegateStatus, 0, len(t.order))
	for _, id := range t.order {
		out = append(out, *t.items[id])
	}
	return out
}

// Active reports how many delegated tasks are currently running.
func (t *Team) Active() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := 0
	for _, id := range t.order {
		if t.items[id].Status == "running" {
			n++
		}
	}
	return n
}
