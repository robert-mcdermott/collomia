package diffmodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/safefile"
)

// Bounds apply to the current recoverable checkpoint, not the append-only log.
const checkpointBytes = 8 << 20
const checkpointFileBytes = 1 << 20
const checkpointEntries = 256

type checkpointEntry struct {
	Path, Op                    string
	Before, After               []byte
	ExistedBefore, ExistedAfter bool
	BeforeMode, AfterMode       uint32
	Turn                        int
	Unavailable                 bool
}
type checkpointState struct {
	Schema     int               `json:"schema_version"`
	Root       string            `json:"root_identity"`
	Floor      int               `json:"oldest_restorable_turn"`
	Pending    bool              `json:"restore_interrupted,omitempty"`
	KeptReason string            `json:"kept_reason,omitempty"`
	Entries    []checkpointEntry `json:"entries,omitempty"`
}

// BindCheckpoint replaces history, preventing cross-session leakage. A legacy
// session has no durable history and cannot claim restoration before its resume.
func (t *Tracker) BindCheckpoint(raw json.RawMessage, turns int, save func(json.RawMessage) error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.history = nil
	t.base = map[string]*string{}
	t.checkpointSave = save
	t.checkpointErr = nil
	t.checkpointFloor = 0
	t.checkpointPending = false
	t.completedTurns = turns
	state := checkpointState{Schema: 1, Floor: turns}
	current, err := safefile.PersistentRootID(t.root)
	if err != nil {
		t.checkpointErr = err
		return err
	}
	t.checkpointRoot = current
	if len(raw) > 0 {
		if len(raw) > 16<<20 {
			return t.checkpointError(errors.New("checkpoint record too large"))
		}
		if err := json.Unmarshal(raw, &state); err != nil {
			return t.checkpointError(err)
		}
		if state.Schema != 1 || state.Floor < 0 || len(state.Entries) > checkpointEntries {
			return t.checkpointError(errors.New("unsupported or oversized workspace checkpoint"))
		}
		if state.Root != current {
			return t.checkpointError(errors.New("workspace directory identity changed since checkpoint; refusing recovery into a replacement directory"))
		}
	}
	total := 0
	for _, entry := range state.Entries {
		if filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path || entry.Path == "." || entry.Path == ".." || strings.HasPrefix(entry.Path, ".."+string(filepath.Separator)) || entry.Turn < 1 || entry.BeforeMode > 0777 || entry.AfterMode > 0777 {
			return t.checkpointError(errors.New("invalid workspace checkpoint entry"))
		}
		total += len(entry.Before) + len(entry.After)
		if len(entry.Before) > checkpointFileBytes || len(entry.After) > checkpointFileBytes || total > checkpointBytes {
			return t.checkpointError(errors.New("checkpoint content exceeds retention bounds"))
		}
		snapshot := Snapshot{Path: filepath.Join(t.root, entry.Path), Op: entry.Op, BeforeMode: os.FileMode(entry.BeforeMode), AfterMode: os.FileMode(entry.AfterMode), Turn: entry.Turn, Unavailable: entry.Unavailable}
		if entry.ExistedBefore {
			v := string(entry.Before)
			snapshot.Before = &v
		}
		if entry.ExistedAfter {
			v := string(entry.After)
			snapshot.After = &v
		}
		t.history = append(t.history, snapshot)
		if _, ok := t.base[snapshot.Path]; !ok {
			t.base[snapshot.Path] = snapshot.Before
		}
	}
	t.checkpointFloor = state.Floor
	t.checkpointPending = state.Pending
	t.checkpointReason = state.KeptReason
	if len(raw) == 0 {
		return t.persistCheckpointLocked()
	}
	return nil
}
func (t *Tracker) checkpointError(err error) error { t.checkpointErr = err; return err }
func (t *Tracker) CheckpointError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.checkpointErr != nil {
		return t.checkpointErr
	}
	if t.checkpointPending {
		return errors.New("workspace restore was interrupted; inspect /recovery, then /recovery keep REASON before continuing")
	}
	return nil
}

func (t *Tracker) CheckpointAvailability(turn int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.checkpointRestoreGuard(turn)
}
func (t *Tracker) persistCheckpointLocked() error {
	if t.checkpointSave == nil {
		return nil
	}
	if t.checkpointErr != nil {
		return t.checkpointErr
	}
	state := checkpointState{Schema: 1, Root: t.checkpointRoot, Floor: t.checkpointFloor, Pending: t.checkpointPending, KeptReason: t.checkpointReason}
	start := max(0, len(t.history)-checkpointEntries)
	for _, s := range t.history[:start] {
		state.Floor = max(state.Floor, s.Turn)
	}
	total := 0
	for i, s := range t.history[start:] {
		path := s.Path
		if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			path = filepath.Join(parent, filepath.Base(path))
		}
		root := t.root
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			state.Floor = max(state.Floor, s.Turn)
			continue // authorized outside-workspace writes are not durable workspace checkpoints
		}
		entry := checkpointEntry{Path: rel, Op: s.Op, Turn: s.Turn, BeforeMode: uint32(s.BeforeMode.Perm()), AfterMode: uint32(s.AfterMode.Perm()), Unavailable: s.Unavailable, ExistedBefore: s.Before != nil, ExistedAfter: s.After != nil}
		size := 0
		if s.Before != nil {
			size += len(*s.Before)
		}
		if s.After != nil {
			size += len(*s.After)
		}
		if (s.Before != nil && len(*s.Before) > checkpointFileBytes) || (s.After != nil && len(*s.After) > checkpointFileBytes) || total+size > checkpointBytes {
			entry.Unavailable = true
		}
		if !entry.Unavailable {
			if s.Before != nil {
				entry.Before = []byte(*s.Before)
			}
			if s.After != nil {
				entry.After = []byte(*s.After)
			}
			total += size
		}
		t.history[start+i].Unavailable = entry.Unavailable
		state.Entries = append(state.Entries, entry)
	}
	t.checkpointFloor = state.Floor
	data, err := json.Marshal(state)
	if err != nil {
		return t.checkpointError(err)
	}
	if len(data) > 16<<20 {
		return t.checkpointError(errors.New("checkpoint serialization exceeds limit"))
	}
	if err := t.checkpointSave(data); err != nil {
		return t.checkpointError(err)
	}
	return nil
}
func (t *Tracker) checkpointRestoreGuard(turn int) error {
	if t.checkpointErr != nil {
		return t.checkpointErr
	}
	if t.checkpointPending {
		return errors.New("a workspace restore was interrupted; inspect the workspace and use /recovery keep REASON to retain current files and discard uncertain checkpoint history")
	}
	if turn < t.checkpointFloor {
		return fmt.Errorf("checkpoint history before turn %d was not retained; /rewind changes only the conversation", t.checkpointFloor)
	}
	for _, s := range t.history {
		if s.Turn > turn && s.Unavailable {
			return fmt.Errorf("checkpoint content for %s was not retained; cannot restore this turn", s.Path)
		}
	}
	return nil
}
func (t *Tracker) CheckpointStatus() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	unavailable := 0
	for _, s := range t.history {
		if s.Unavailable {
			unavailable++
		}
	}
	return fmt.Sprintf("Workspace checkpoints: %d mutations; oldest restorable turn %d; %d unavailable entries; interrupted restore: %t", len(t.history), t.checkpointFloor, unavailable, t.checkpointPending)
}

// KeepCheckpoint is user-only reconciliation, not a grant to execute anything.
// Dropping history explicitly avoids claiming a rewind can reverse unknown work.
func (t *Tracker) KeepCheckpoint(reason string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.history = nil
	t.base = map[string]*string{}
	t.checkpointReason = reason
	t.checkpointFloor = t.completedTurns + 1
	t.checkpointPending = false
	return t.persistCheckpointLocked()
}

// PersistCheckpoint allows a branched restore to bind its complete pre-restore
// history to the new branch before the first workspace byte is reversed.
func (t *Tracker) PersistCheckpoint() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.persistCheckpointLocked()
}
