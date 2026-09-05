package app

import (
	"errors"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/agent"
)

func (r *Runtime) RecoveryStatus() (string, error) {
	if r.Session == nil {
		return "Recovery is unavailable in ephemeral mode.", nil
	}
	status, err := agent.CompletionRecoveryStatus(r.Session)
	if err != nil {
		return "", err
	}
	if r.Changes != nil {
		status += "\n\n" + r.Changes.CheckpointStatus()
	}
	return r.Redactor.Redact(status), nil
}

// ReconcileRecovery is operator-only. It cannot validate outputs or grant an
// action permission. Missing checkpoint coverage is discarded explicitly.
func (r *Runtime) ReconcileRecovery(acknowledge bool, reason string) error {
	if r.OrchestratedGoalPhase() != "" {
		return errors.New("Standard recovery reconciliation is unavailable while an Orchestrated Goal is attached")
	}
	if r.Session == nil || r.Changes == nil {
		return errors.New("recovery persistence unavailable")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1024 {
		return errors.New("provide an inspection reason of 1–1024 bytes")
	}
	if acknowledge {
		uncertain, err := agent.CompletionUncertain(r.Session)
		if err != nil {
			return err
		}
		if !uncertain {
			return errors.New("no uncertain action to acknowledge")
		}
	}
	if err := r.Changes.KeepCheckpoint(reason); err != nil {
		return err
	}
	if acknowledge {
		return agent.AcknowledgeCompletion(r.Session, reason)
	}
	return nil
}
