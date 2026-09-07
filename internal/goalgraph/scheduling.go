package goalgraph

import (
	"sort"
	"time"
)

// Older extensions grew aggregate resources only. They must not silently
// acquire wider worker leases merely by opening the session in a newer build.
func (g *Graph) workerEnvelopesLocked() int {
	if base := g.state.AggregateBudget.Grant.ReadStarts; base > 0 {
		return max(1, g.state.ReadFanout.MaxStarts/base)
	}
	return 1
}

func (g *Graph) hasReadyPrimaryLocked() bool {
	for _, node := range g.state.Nodes {
		if node.State == NodeReady && node.Execution == ExecutionPrimary {
			return true
		}
	}
	return false
}

// Read fan-out pays for the union of its execution windows. The gap between
// workers may contain primary work, user review or process downtime; charging
// that gap made a later read fail despite never spending its wall allowance.
// Deriving it from immutable attempts also repairs older snapshots without
// inventing counters or widening their stored limits.
func (g *Graph) readActiveWallLocked(now time.Time) time.Duration {
	// Revisions retain execution classes even after a node is retired. Its
	// already-spent time must not disappear with it.
	readAt := func(a Attempt) bool {
		for i := len(g.state.Revisions) - 1; i >= 0; i-- {
			r := g.state.Revisions[i]
			if r.Generation > a.GraphGeneration {
				continue
			}
			for _, n := range r.Spec.Nodes {
				if n.ID == a.NodeID {
					return n.Execution == ExecutionReadOnly
				}
			}
		}
		n := g.nodeLocked(a.NodeID)
		return n != nil && n.Execution == ExecutionReadOnly
	}
	type window struct{ start, end time.Time }
	var windows []window
	for _, a := range g.state.Attempts {
		if !readAt(a) || a.Started.IsZero() {
			continue
		}
		end := a.Finished
		if end.IsZero() {
			end = now
		}
		if end.After(a.Started) {
			windows = append(windows, window{a.Started, end})
		}
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].start.Before(windows[j].start) })
	var elapsed time.Duration
	var last time.Time
	for _, w := range windows {
		start := w.start
		if last.After(start) {
			start = last
		}
		if w.end.After(start) {
			elapsed += w.end.Sub(start)
		}
		if w.end.After(last) {
			last = w.end
		}
	}
	return elapsed
}
