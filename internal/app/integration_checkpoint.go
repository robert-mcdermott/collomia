package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/hooks"
	"github.com/robert-mcdermott/collomia/internal/session"
)

// beginIntegrationCheckpoint durably records what every target path held
// before publication changes it. An ephemeral runtime has no session to write
// to; it returns a zero checkpoint and the caller proceeds, because refusing
// integration outright there would remove a working capability to protect
// state that was never durable in the first place.
func (r *Runtime) beginIntegrationCheckpoint(id string, mutations []delegateIntegrationMutation) (session.IntegrationCheckpoint, error) {
	if r == nil || r.Session == nil || len(mutations) == 0 {
		return session.IntegrationCheckpoint{}, nil
	}
	files := make([]session.IntegrationFile, 0, len(mutations))
	for _, mutation := range mutations {
		file := session.IntegrationFile{Path: mutation.path, Mode: uint32(mutation.beforeMode.Perm())}
		if mutation.before != nil {
			content := *mutation.before
			file.Existed, file.Before = true, &content
		}
		files = append(files, file)
	}
	graphNode := 0
	if r.Team != nil {
		if status, ok := r.Team.Get(id); ok && status.GraphNode {
			graphNode = status.PlanStep
		}
	}
	checkpoint := session.NewIntegrationCheckpoint(newCheckpointID(), id, r.Workspace, graphNode, files, time.Now())
	if err := r.Session.AppendIntegrationCheckpoint(checkpoint); err != nil {
		return session.IntegrationCheckpoint{}, fmt.Errorf("record integration checkpoint: %w", err)
	}
	return checkpoint, nil
}

// finishIntegrationCheckpoint records the outcome. A failure to persist it is
// reported as a warning rather than failing the integration: the bytes are
// already published, and turning a completed publication into an error would
// describe the workspace less accurately than a stale pending record does.
func (r *Runtime) finishIntegrationCheckpoint(checkpoint session.IntegrationCheckpoint, state, detail string) {
	if r == nil || r.Session == nil || checkpoint.ID == "" {
		return
	}
	checkpoint.State, checkpoint.Ended = state, time.Now().UTC()
	if detail != "" {
		checkpoint.Detail = detail
	}
	if err := r.Session.AppendIntegrationCheckpoint(checkpoint); err != nil && r.Logger != nil {
		r.Logger.Warn("integration checkpoint outcome was not recorded", "checkpoint", checkpoint.ID, "state", state, "error", err.Error())
	}
}

func newCheckpointID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "checkpoint-" + fmt.Sprint(time.Now().UnixNano())
	}
	return "checkpoint-" + hex.EncodeToString(raw[:])
}

// InterruptedIntegrations reports publications that never recorded an outcome.
// Each one is a workspace that may hold some of a candidate's changes and not
// others, and nothing but this record can say which.
func (r *Runtime) InterruptedIntegrations() []session.IntegrationCheckpoint {
	if r == nil || r.Session == nil {
		return nil
	}
	var mine []session.IntegrationCheckpoint
	for _, checkpoint := range r.Session.PendingIntegrationCheckpoints() {
		// A checkpoint recorded against a different workspace belongs to that
		// workspace. Restoring its paths here would write bytes from one
		// repository into another.
		if checkpoint.Workspace == r.Workspace {
			mine = append(mine, checkpoint)
		}
	}
	return mine
}

// DescribeInterruptedIntegrations renders the operator-facing summary. It
// deliberately says the outcome is unknown rather than guessing from current
// file contents: a file that matches its recorded prior bytes may never have
// been written, or may have been written and then edited back.
func (r *Runtime) DescribeInterruptedIntegrations() string {
	pending := r.InterruptedIntegrations()
	if len(pending) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d integration(s) started but never recorded an outcome. The workspace may hold some of the published changes and not others.\n", len(pending))
	for _, checkpoint := range pending {
		fmt.Fprintf(&b, "- %s · candidate %s", checkpoint.ID, checkpoint.Delegate)
		if checkpoint.GraphNode > 0 {
			fmt.Fprintf(&b, " · Orchestrated Goal node %d", checkpoint.GraphNode)
		}
		fmt.Fprintf(&b, " · started %s\n", checkpoint.Started.Format(time.RFC3339))
		for _, file := range checkpoint.Files {
			state := "restorable"
			if !file.Restorable {
				state = "prior content not retained — reconcile by hand"
			}
			fmt.Fprintf(&b, "    %s (%s)\n", file.Path, state)
		}
		if checkpoint.Detail != "" {
			fmt.Fprintf(&b, "    %s\n", checkpoint.Detail)
		}
	}
	b.WriteString("\nRestore the recorded prior state with /restore integration <checkpoint-id>, or keep the workspace as it stands with /restore integration <checkpoint-id> keep.")
	return b.String()
}

// interruptedIntegrationRefusal reports why no further Orchestrated Goal
// integration step may proceed while a publication into this workspace never
// recorded an outcome.
//
// Every remaining step in the milestone reasons about the combined workspace:
// integrating a second candidate diffs against it, combined verification runs
// the repository's checks against it, and a waiver is a person's statement
// about it. A workspace that may hold some of a candidate's files and not
// others is not a state any of those three claims can be made about, and the
// runtime has a durable record saying exactly that. Refusing is not caution
// about an unlikely case — it is declining to build evidence on top of bytes
// the runtime has already written down as unknown.
func (r *Runtime) interruptedIntegrationRefusal(step string) error {
	pending := r.InterruptedIntegrations()
	if len(pending) == 0 {
		return nil
	}
	ids := make([]string, 0, len(pending))
	for _, checkpoint := range pending {
		id := checkpoint.ID
		if checkpoint.GraphNode > 0 {
			id = fmt.Sprintf("%s (node %d)", id, checkpoint.GraphNode)
		}
		ids = append(ids, id)
	}
	return fmt.Errorf("cannot %s: %d earlier publication(s) into this workspace never recorded an outcome, so the workspace may hold some of a candidate's files and not others (%s). Resolve each one first with /restore integration <id> to put the prior bytes back, or /restore integration <id> keep to accept the workspace as it stands",
		step, len(pending), strings.Join(ids, "; "))
}

// AcceptIntegrationCheckpoint records that a person inspected an interrupted
// publication and chose to keep the workspace as it is. It writes no file.
//
// It is the other half of restoration rather than a way to dismiss a warning.
// An interrupted publication is ambiguous, and the runtime cannot resolve the
// ambiguity by looking — a file matching its recorded prior bytes may never
// have been written, or may have been written and edited back. Only a person
// can end it, and both of their answers have to be sayable.
func (r *Runtime) AcceptIntegrationCheckpoint(id string) error {
	if r == nil || r.Session == nil {
		return errors.New("durable session state is unavailable")
	}
	checkpoint, ok := r.Session.IntegrationCheckpointByID(id)
	if !ok {
		return fmt.Errorf("unknown integration checkpoint %q", id)
	}
	if checkpoint.Workspace != r.Workspace {
		return fmt.Errorf("integration checkpoint %q belongs to another workspace", id)
	}
	if checkpoint.State != session.IntegrationPending {
		return fmt.Errorf("integration checkpoint %q is already %s", id, checkpoint.State)
	}
	checkpoint.State, checkpoint.Ended = session.IntegrationAccepted, time.Now().UTC()
	accepted := "accepted by the user; the workspace was kept as the interrupted publication left it and no bytes were changed"
	// A checkpoint that could not retain some file's prior content says so in
	// Detail, and that stays true after acceptance — it is the record of what
	// this session could never have put back.
	if checkpoint.Detail != "" {
		accepted = checkpoint.Detail + "; " + accepted
	}
	checkpoint.Detail = accepted
	// Unlike a completed publication, nothing here has happened yet that a
	// failed write would contradict, so this one is reported rather than warned
	// past: an acceptance that was not recorded has not been made.
	if err := r.Session.AppendIntegrationCheckpoint(checkpoint); err != nil {
		return fmt.Errorf("record acceptance of integration checkpoint %s: %w", id, err)
	}
	return nil
}

// RestoreIntegrationCheckpoint puts every recorded path back exactly as it was
// before the interrupted publication started. It is explicit and never
// automatic: an interrupted integration is ambiguous, and choosing between the
// prior state and the partly-published one is the user's decision, not the
// runtime's.
//
// It restores rather than re-publishes. Completing a half-finished integration
// would repeat a mutation whose effect is unknown, which is the replay this
// program refuses everywhere else.
func (r *Runtime) RestoreIntegrationCheckpoint(ctx context.Context, id string) ([]string, error) {
	if r == nil || r.Session == nil {
		return nil, errors.New("durable session state is unavailable")
	}
	checkpoint, ok := r.Session.IntegrationCheckpointByID(id)
	if !ok {
		return nil, fmt.Errorf("unknown integration checkpoint %q", id)
	}
	if checkpoint.Workspace != r.Workspace {
		return nil, fmt.Errorf("integration checkpoint %q belongs to another workspace", id)
	}
	if checkpoint.State != session.IntegrationPending {
		return nil, fmt.Errorf("integration checkpoint %q is already %s", id, checkpoint.State)
	}
	if !checkpoint.Restorable() {
		return nil, fmt.Errorf("integration checkpoint %q did not retain the prior content of every file (%s); reconcile those paths by hand", id, checkpoint.Detail)
	}
	restored := make([]string, 0, len(checkpoint.Files))
	for _, file := range checkpoint.Files {
		mode := os.FileMode(file.Mode).Perm()
		if mode == 0 {
			mode = 0o644
		}
		var before *string
		if file.Existed {
			before = file.Before
		}
		if err := replaceRooted(r.Workspace, file.Path, before, mode); err != nil {
			// Report exactly how far restoration reached. Rolling the rollback
			// back would be another ambiguous mutation on an already ambiguous
			// workspace.
			return restored, fmt.Errorf("restore %s: %w (restored %d of %d file(s); the checkpoint is kept)", file.Path, err, len(restored), len(checkpoint.Files))
		}
		restored = append(restored, file.Path)
		if r.Hooks != nil {
			r.Hooks.Fire(ctx, hooks.Payload{Event: "file_change", Workspace: r.Workspace, Subject: "restore_integration", Tool: "restore_integration", Paths: []string{file.Path}})
		}
	}
	r.finishIntegrationCheckpoint(checkpoint, session.IntegrationRestored, fmt.Sprintf("restored %d file(s) to the state recorded before the interrupted publication", len(restored)))
	return restored, nil
}
