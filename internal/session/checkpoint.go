package session

import (
	"encoding/json"
	"time"
)

// Integration writes candidate bytes into the user's own workspace, and until
// now the only record of what those bytes replaced lived in memory. A process
// that stopped between the first file and the last left a workspace that was
// neither the parent it had been nor the candidate it was becoming, with
// nothing on disk able to say which files had already moved or what they had
// held. This record is that missing write-ahead half.
//
// It is deliberately written *before* the first mutation and marked complete
// afterwards, so the durable evidence of an interrupted integration is its
// absence of completion rather than an inference from file contents.

const (
	// IntegrationPending means the bytes may or may not have been written. It
	// is the only genuinely ambiguous state and never resolves itself.
	IntegrationPending = "pending"
	// IntegrationApplied means every mutation succeeded.
	IntegrationApplied = "applied"
	// IntegrationReverted means the in-process rollback restored every file it
	// had already changed.
	IntegrationReverted = "reverted"
	// IntegrationRestored means a person explicitly restored the recorded prior
	// bytes after an interruption.
	IntegrationRestored = "restored"
)

const (
	// maxCheckpointFileBytes bounds one file's retained prior content. Past it
	// the checkpoint keeps identity and drops content rather than refusing the
	// integration or writing an unbounded session record: naming a file it
	// cannot restore is far more useful than not recording it at all.
	maxCheckpointFileBytes = 1 << 20
	// maxCheckpointTotalBytes bounds one checkpoint the same way.
	maxCheckpointTotalBytes = 8 << 20
)

// IntegrationFile is one target path's state before integration touched it.
type IntegrationFile struct {
	Path string `json:"path"`
	// Existed distinguishes a file that was absent from one that was empty.
	// Restoring the two differently is the whole reason it is recorded.
	Existed bool `json:"existed"`
	// Before is the exact prior content, omitted when the file did not exist
	// or when it exceeded the retention bound.
	Before *string `json:"before,omitempty"`
	Mode   uint32  `json:"mode,omitempty"`
	// Restorable is false when content was dropped for size. The path is still
	// named so a person knows exactly what they have to reconcile by hand.
	Restorable bool `json:"restorable"`
	// Bytes records the prior size even when the content was not retained.
	Bytes int `json:"bytes,omitempty"`
}

// IntegrationCheckpoint is the durable pre-integration record for one publish.
type IntegrationCheckpoint struct {
	ID string `json:"id"`
	// Delegate is the candidate whose changes were being published.
	Delegate string `json:"delegate"`
	// GraphNode is non-zero when the candidate belonged to an Orchestrated Goal
	// node, so recovery can say which plan node an interrupted publish affects.
	GraphNode int               `json:"graph_node,omitempty"`
	Workspace string            `json:"workspace"`
	State     string            `json:"state"`
	Files     []IntegrationFile `json:"files"`
	// Detail explains a non-pending state, or names what could not be retained.
	Detail  string    `json:"detail,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
}

// Restorable reports whether every recorded file can be put back exactly. A
// checkpoint that dropped content for size can still be reported, but it
// cannot promise restoration, and it must not pretend otherwise.
func (c IntegrationCheckpoint) Restorable() bool {
	for _, file := range c.Files {
		if !file.Restorable {
			return false
		}
	}
	return len(c.Files) > 0
}

// NewIntegrationCheckpoint builds a bounded checkpoint from the prior state of
// each target path. Content past the retention bounds is dropped file by file,
// newest first, and the affected paths are named in Detail.
func NewIntegrationCheckpoint(id, delegate, workspace string, graphNode int, files []IntegrationFile, now time.Time) IntegrationCheckpoint {
	total := 0
	var dropped []string
	bounded := make([]IntegrationFile, 0, len(files))
	for _, file := range files {
		file.Restorable = true
		if file.Before != nil {
			file.Bytes = len(*file.Before)
			if file.Bytes > maxCheckpointFileBytes || total+file.Bytes > maxCheckpointTotalBytes {
				file.Before = nil
				file.Restorable = false
				dropped = append(dropped, file.Path)
			} else {
				total += file.Bytes
			}
		}
		bounded = append(bounded, file)
	}
	checkpoint := IntegrationCheckpoint{
		ID: id, Delegate: delegate, GraphNode: graphNode, Workspace: workspace,
		State: IntegrationPending, Files: bounded, Started: now.UTC(),
	}
	if len(dropped) > 0 {
		checkpoint.Detail = "prior content was too large to retain for: " + joinBounded(dropped, 8)
	}
	return checkpoint
}

func joinBounded(values []string, limit int) string {
	out := ""
	for i, value := range values {
		if i >= limit {
			out += ", and more"
			break
		}
		if i > 0 {
			out += ", "
		}
		out += value
	}
	return out
}

// AppendIntegrationCheckpoint persists one checkpoint state. It always flushes:
// a checkpoint that is still in the write buffer when the process stops is
// exactly the record that was needed and is not there.
func (sess *Session) AppendIntegrationCheckpoint(checkpoint IntegrationCheckpoint) error {
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	sess.applyIntegrationCheckpointLocked(checkpoint)
	sess.mu.Unlock()
	if err := sess.append(Record{Type: "integration_checkpoint", IntegrationCheckpoint: data}); err != nil {
		return err
	}
	return sess.Sync()
}

// PendingIntegrationCheckpoints returns the checkpoints whose integration
// never reported an outcome, in the order they were started. Each one is a
// workspace that may be half-published and that nothing else can resolve.
func (sess *Session) PendingIntegrationCheckpoints() []IntegrationCheckpoint {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	var pending []IntegrationCheckpoint
	for _, id := range sess.checkpointOrder {
		if checkpoint, ok := sess.checkpoints[id]; ok && checkpoint.State == IntegrationPending {
			pending = append(pending, checkpoint)
		}
	}
	return pending
}

// AllIntegrationCheckpoints returns every recorded checkpoint in the order it
// was first written, whatever its state. Resolved records are kept because
// they are the account of what was published into the workspace and when.
func (sess *Session) AllIntegrationCheckpoints() []IntegrationCheckpoint {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	out := make([]IntegrationCheckpoint, 0, len(sess.checkpointOrder))
	for _, id := range sess.checkpointOrder {
		if checkpoint, ok := sess.checkpoints[id]; ok {
			out = append(out, checkpoint)
		}
	}
	return out
}

// IntegrationCheckpointByID returns one recorded checkpoint.
func (sess *Session) IntegrationCheckpointByID(id string) (IntegrationCheckpoint, bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	checkpoint, ok := sess.checkpoints[id]
	return checkpoint, ok
}

func (sess *Session) applyIntegrationCheckpointLocked(checkpoint IntegrationCheckpoint) {
	if checkpoint.ID == "" {
		return
	}
	if sess.checkpoints == nil {
		sess.checkpoints = map[string]IntegrationCheckpoint{}
	}
	if _, seen := sess.checkpoints[checkpoint.ID]; !seen {
		sess.checkpointOrder = append(sess.checkpointOrder, checkpoint.ID)
	}
	sess.checkpoints[checkpoint.ID] = checkpoint
}
