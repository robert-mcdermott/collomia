package agent

import "fmt"

// ExecutionLimits are response cycles, not tool calls or accumulated tokens.
func (a *Agent) ExecutionLimits() (noProgress, total int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.maxIterations, a.maxTurnIterations
}

// SetExecutionLimits is a user control. The next Standard cycle sees changes;
// graph budgets and delegated execution retain their own authority.
func (a *Agent) SetExecutionLimits(noProgress, total int) error {
	if a.graphEnabled() {
		return fmt.Errorf("use Orchestrated Goal budget controls for an active goal")
	}
	if noProgress < 1 || noProgress > 10000 || total < 1 || total > 10000 {
		return fmt.Errorf("execution limits must be between 1 and 10000")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.maxIterations, a.maxTurnIterations = noProgress, total
	return nil
}
