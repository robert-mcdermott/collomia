// Package session provides Collomia's durable session store: an append-only
// JSONL log per session holding metadata, the full message transcript,
// runtime events, and compaction markers. Appends are single atomic writes
// to an O_APPEND file, and loading tolerates a torn final line, so a crash
// never corrupts accepted history. Mutating tool calls are never replayed on
// recovery — an interrupted call is recorded as interrupted instead.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

type Meta struct {
	ID         string    `json:"id"`
	Workspace  string    `json:"workspace"`
	Title      string    `json:"title,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	Model      string    `json:"model,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Archived   bool      `json:"archived,omitempty"`
	ForkedFrom string    `json:"forked_from,omitempty"`
	Turns      int       `json:"turns,omitempty"`
}

// RecordSchemaVersion identifies the durable session-log record shape.
// Records written before this field was introduced omit schema_version and
// are treated as version 1. Additive optional fields do not require a bump;
// incompatible record semantics do.
const RecordSchemaVersion = 1

// Record is one line of the session log. Type selects the payload.
type Record struct {
	SchemaVersion int               `json:"schema_version,omitempty"`
	Type          string            `json:"type"` // meta, message, event, compaction, plan
	Time          time.Time         `json:"time"`
	Meta          *Meta             `json:"meta,omitempty"`
	Message       *provider.Message `json:"message,omitempty"`
	Event         *event.Event      `json:"event,omitempty"`
	// Replaced is set on compaction records: how many active messages the
	// summary message replaces.
	Replaced int             `json:"replaced,omitempty"`
	Plan     json.RawMessage `json:"plan,omitempty"`
}

type Store struct {
	dir       string
	workspace string
}

// Open returns the store for a workspace under ~/.collomia/sessions (or the
// equivalent USERPROFILE path on Windows), never inside the workspace.
func Open(workspace string) (*Store, error) {
	base, err := userconfig.Path("sessions")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(workspace))
	dir := filepath.Join(base, filepath.Base(workspace)+"-"+hex.EncodeToString(sum[:6]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, workspace: workspace}, nil
}

// OpenAt uses an explicit directory; for tests.
func OpenAt(dir, workspace string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, workspace: workspace}, nil
}

func newID() string {
	buf := make([]byte, 3)
	_, _ = rand.Read(buf)
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buf)
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".jsonl") }
func (s *Store) artifactDir(id string) string {
	return filepath.Join(s.dir, id+".artifacts")
}
func (s *Store) attachmentDir(id string) string {
	return filepath.Join(s.dir, id+".attachments")
}

// New creates and opens a fresh session.
func (s *Store) New(providerName, model string) (*Session, error) {
	meta := Meta{ID: newID(), Workspace: s.workspace, Provider: providerName, Model: model, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	sess := &Session{Meta: meta, store: s}
	if err := sess.open(); err != nil {
		return nil, err
	}
	if err := sess.append(Record{Type: "meta", Meta: &sess.Meta}); err != nil {
		sess.Close()
		return nil, err
	}
	return sess, nil
}

// List returns session metadata, most recently updated first.
func (s *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var metas []Meta
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		sess, err := s.Load(id)
		if err != nil {
			continue
		}
		sess.Close()
		metas = append(metas, sess.Meta)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].UpdatedAt.After(metas[j].UpdatedAt) })
	return metas, nil
}

// Latest returns the most recently updated unarchived session ID.
func (s *Store) Latest() (string, error) {
	metas, err := s.List()
	if err != nil {
		return "", err
	}
	for _, meta := range metas {
		if !meta.Archived {
			return meta.ID, nil
		}
	}
	return "", errors.New("no sessions to continue")
}

// Load reads a session, tolerating a torn final line and marking dangling
// tool calls as interrupted (without ever re-running them).
func (s *Store) Load(id string) (*Session, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	sess := &Session{store: s}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			if i >= len(lines)-2 {
				// Torn tail from a crash mid-write; accepted history ends at
				// the previous record.
				continue
			}
			return nil, fmt.Errorf("session %s line %d is corrupt: %w", id, i+1, err)
		}
		if err := validateRecordVersion(id, i+1, record); err != nil {
			return nil, err
		}
		sess.replay(record)
	}
	if sess.Meta.ID == "" {
		return nil, fmt.Errorf("session %s has no metadata", id)
	}
	// The file name is authoritative: a forked session's copied records
	// still carry the source session's ID.
	sess.Meta.ID = id
	sess.markInterrupted()
	if err := sess.open(); err != nil {
		return nil, err
	}
	return sess, nil
}

// Fork copies a session's history into a new session that shares no future
// state with the original.
func (s *Store) Fork(id string) (*Session, error) {
	source, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	forked, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	forked.Close()
	meta := forked.Meta
	meta.ID = newID()
	meta.ForkedFrom = id
	meta.CreatedAt = time.Now().UTC()
	meta.UpdatedAt = time.Now().UTC()
	if err := os.WriteFile(s.path(meta.ID), source, 0o600); err != nil {
		return nil, err
	}
	sess, err := s.Load(meta.ID)
	if err != nil {
		return nil, err
	}
	sess.Meta = meta
	if err := sess.append(Record{Type: "meta", Meta: &meta}); err != nil {
		sess.Close()
		return nil, err
	}
	if err := copyArtifactDir(s.artifactDir(id), s.artifactDir(meta.ID), nil); err != nil {
		sess.Close()
		_ = os.Remove(s.path(meta.ID))
		_ = os.RemoveAll(s.artifactDir(meta.ID))
		return nil, fmt.Errorf("copy session artifacts: %w", err)
	}
	if err := copyAttachmentDir(s.attachmentDir(id), s.attachmentDir(meta.ID), nil); err != nil {
		sess.Close()
		_ = os.Remove(s.path(meta.ID))
		_ = os.RemoveAll(s.artifactDir(meta.ID))
		_ = os.RemoveAll(s.attachmentDir(meta.ID))
		return nil, fmt.Errorf("copy session attachments: %w", err)
	}
	return sess, nil
}

// Checkpoint is one completed conversational turn that can be used as a safe
// rewind target. Prompt is display-only context for pickers and listings.
type Checkpoint struct {
	Turn   int
	Prompt string
}

// Checkpoints returns completed turn boundaries without opening a provider or
// executing recorded tools. Failed or interrupted turns do not become rewind
// targets because they have no durable turn.end record.
func (s *Store) Checkpoints(id string) ([]Checkpoint, error) {
	records, err := s.readRecords(id)
	if err != nil {
		return nil, err
	}
	var checkpoints []Checkpoint
	prompt := ""
	for _, record := range records {
		if record.Type == "message" && record.Message != nil && record.Message.Role == "user" {
			prompt = record.Message.Content
		}
		if record.Type == "event" && record.Event != nil && record.Event.Kind == event.KindTurnEnd {
			checkpoints = append(checkpoints, Checkpoint{Turn: len(checkpoints) + 1, Prompt: prompt})
		}
	}
	return checkpoints, nil
}

// Rewind creates an independent session containing exactly the selected
// number of completed turns. The source log and workspace are untouched, and
// loading the result never replays any recorded tool call.
func (s *Store) Rewind(id string, turns int) (*Session, error) {
	if turns < 0 {
		return nil, errors.New("rewind turn must not be negative")
	}
	source, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	sourceMeta := source.Meta
	source.Close()
	if turns >= sourceMeta.Turns {
		return nil, fmt.Errorf("rewind turn must be earlier than the source session's %d completed turns", sourceMeta.Turns)
	}
	records, err := s.readRecords(id)
	if err != nil {
		return nil, err
	}
	selected := make([]Record, 0, len(records))
	completed := 0
	for _, record := range records {
		if turns == 0 && record.Type != "meta" {
			break
		}
		selected = append(selected, record)
		if record.Type == "event" && record.Event != nil && record.Event.Kind == event.KindTurnEnd {
			completed++
			if completed == turns {
				break
			}
		}
	}
	if turns > 0 && completed != turns {
		return nil, fmt.Errorf("session has only %d completed turn boundaries", completed)
	}
	newMeta := sourceMeta
	newMeta.ID = newID()
	newMeta.ForkedFrom = id
	newMeta.CreatedAt = time.Now().UTC()
	newMeta.UpdatedAt = newMeta.CreatedAt
	newMeta.Turns = turns
	path := s.path(newMeta.ID)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	writeErr := writeRecords(file, selected)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(path)
		return nil, writeErr
	}
	sess, err := s.Load(newMeta.ID)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	sess.Meta = newMeta
	if err := sess.append(Record{Type: "meta", Meta: &newMeta}); err != nil {
		sess.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := copyArtifactDir(s.artifactDir(id), s.artifactDir(newMeta.ID), referencedArtifactIDs(selected)); err != nil {
		sess.Close()
		_ = os.Remove(path)
		_ = os.RemoveAll(s.artifactDir(newMeta.ID))
		return nil, fmt.Errorf("copy session artifacts: %w", err)
	}
	if err := copyAttachmentDir(s.attachmentDir(id), s.attachmentDir(newMeta.ID), referencedAttachmentIDs(selected)); err != nil {
		sess.Close()
		_ = os.Remove(path)
		_ = os.RemoveAll(s.artifactDir(newMeta.ID))
		_ = os.RemoveAll(s.attachmentDir(newMeta.ID))
		return nil, fmt.Errorf("copy session attachments: %w", err)
	}
	return sess, nil
}

func (s *Store) Rename(id, title string) error {
	return s.amendMeta(id, func(m *Meta) { m.Title = title })
}
func (s *Store) Archive(id string, archived bool) error {
	return s.amendMeta(id, func(m *Meta) { m.Archived = archived })
}
func (s *Store) Delete(id string) error {
	if err := os.Remove(s.path(id)); err != nil {
		return err
	}
	if err := os.RemoveAll(s.artifactDir(id)); err != nil {
		return fmt.Errorf("remove session artifacts: %w", err)
	}
	if err := os.RemoveAll(s.attachmentDir(id)); err != nil {
		return fmt.Errorf("remove session attachments: %w", err)
	}
	return nil
}

// readRecords applies the same torn-tail rule as Load but does not open the
// session for appends or synthesize interrupted tool results.
func (s *Store) readRecords(id string) ([]Record, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var records []Record
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			if i >= len(lines)-2 {
				continue
			}
			return nil, fmt.Errorf("session %s line %d is corrupt: %w", id, i+1, err)
		}
		if err := validateRecordVersion(id, i+1, record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func validateRecordVersion(id string, line int, record Record) error {
	version := record.SchemaVersion
	if version == 0 {
		version = 1 // legacy records written before explicit versioning
	}
	if version != RecordSchemaVersion {
		return fmt.Errorf("session %s line %d uses unsupported schema_version %d (this build supports %d)", id, line, version, RecordSchemaVersion)
	}
	return nil
}

func writeRecords(w io.Writer, records []Record) error {
	for _, record := range records {
		if record.SchemaVersion == 0 {
			record.SchemaVersion = RecordSchemaVersion
		}
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		payload := append(data, '\n')
		written, err := w.Write(payload)
		if err == nil && written != len(payload) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return fmt.Errorf("write rewound session: %w", err)
		}
	}
	return nil
}

// referencedArtifactIDs finds opaque artifact IDs that remain reachable from
// a rewound record prefix. Rewind copies only these files, so discarded future
// turns do not leak their retained tool output into the new branch.
func referencedArtifactIDs(records []Record) map[string]bool {
	referenced := map[string]bool{}
	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			continue
		}
		for start := 0; start+24 <= len(data); start++ {
			candidate := string(data[start : start+24])
			if validArtifactID(candidate) {
				referenced[candidate] = true
				start += 23
			}
		}
	}
	return referenced
}

func referencedAttachmentIDs(records []Record) map[string]bool {
	referenced := map[string]bool{}
	for _, record := range records {
		if record.Message == nil {
			continue
		}
		for _, part := range record.Message.Parts {
			if part.Type == provider.ContentImage && validArtifactID(part.AttachmentID) {
				referenced[part.AttachmentID] = true
			}
		}
	}
	return referenced
}

// copyArtifactDir copies every valid artifact when allowed is nil (a normal
// fork), or only the named references for a conversation rewind.
func copyArtifactDir(source, target string, allowed map[string]bool) error {
	entries, err := os.ReadDir(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	created := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validArtifactID(id) || (allowed != nil && !allowed[id]) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > ArtifactResultLimit {
			return fmt.Errorf("invalid session artifact %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		if !created {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			created = true
		}
		path := filepath.Join(target, entry.Name())
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		written, writeErr := file.Write(data)
		if writeErr == nil && written != len(data) {
			writeErr = io.ErrShortWrite
		}
		if closeErr := file.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return writeErr
		}
	}
	return nil
}

// copyAttachmentDir copies bounded regular image blobs for a normal fork, or
// only references reachable from the retained record prefix for rewind.
func copyAttachmentDir(source, target string, allowed map[string]bool) error {
	entries, err := os.ReadDir(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	created := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".bin") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".bin")
		if !validArtifactID(id) || (allowed != nil && !allowed[id]) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > AttachmentFileLimit {
			return fmt.Errorf("invalid session attachment %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		if !created {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			created = true
		}
		path := filepath.Join(target, entry.Name())
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		written, writeErr := file.Write(data)
		if writeErr == nil && written != len(data) {
			writeErr = io.ErrShortWrite
		}
		if closeErr := file.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return writeErr
		}
	}
	return nil
}

func (s *Store) amendMeta(id string, mutate func(*Meta)) error {
	sess, err := s.Load(id)
	if err != nil {
		return err
	}
	defer sess.Close()
	mutate(&sess.Meta)
	sess.Meta.UpdatedAt = time.Now().UTC()
	return sess.append(Record{Type: "meta", Meta: &sess.Meta})
}

// Session is an open, appendable session.
type Session struct {
	Meta Meta
	// Transcript is the complete stored message history.
	Transcript []provider.Message
	// active is the model-visible context: compactions replace old messages
	// with their summary while Transcript keeps everything.
	active []provider.Message
	// PlanRaw is the latest persisted structured plan, if any.
	PlanRaw json.RawMessage
	// delegates retains the latest parent-inbox snapshot for each delegated
	// task. Stored updates are inert data and are never scheduled during load.
	delegates     map[string]event.DelegateStatus
	delegateOrder []string
	// recentEvents is a bounded in-memory projection source for operator UIs.
	// The append-only JSONL remains the complete durable event history.
	recentEvents []event.Event

	store              *Store
	mu                 sync.Mutex
	file               recordFile
	writeErr           error
	pendingInterrupted []provider.Message
}

const recentEventLimit = 2048

type recordFile interface {
	Write([]byte) (int, error)
	Close() error
}

func (sess *Session) open() error {
	file, err := os.OpenFile(sess.store.path(sess.Meta.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	sess.file = file
	return nil
}

func (sess *Session) replay(record Record) {
	switch record.Type {
	case "meta":
		if record.Meta != nil {
			sess.Meta = *record.Meta
		}
	case "message":
		if record.Message != nil {
			sess.Transcript = append(sess.Transcript, *record.Message)
			sess.active = append(sess.active, *record.Message)
		}
	case "compaction":
		if record.Message != nil && record.Replaced > 0 && record.Replaced <= len(sess.active) {
			sess.active = append([]provider.Message{*record.Message}, sess.active[record.Replaced:]...)
		}
	case "plan":
		sess.PlanRaw = record.Plan
	case "event":
		if record.Event != nil {
			sess.retainEvent(*record.Event)
			if record.Event.Kind == event.KindDelegateUpdate && record.Event.Delegate != nil {
				sess.applyDelegate(*record.Event.Delegate)
			}
		}
	}
}

func (sess *Session) retainEvent(e event.Event) {
	if len(sess.recentEvents) >= recentEventLimit {
		copy(sess.recentEvents, sess.recentEvents[len(sess.recentEvents)-recentEventLimit+1:])
		sess.recentEvents = sess.recentEvents[:recentEventLimit]
		sess.recentEvents[recentEventLimit-1] = e
		return
	}
	sess.recentEvents = append(sess.recentEvents, e)
}

func (sess *Session) applyDelegate(status event.DelegateStatus) {
	if status.ID == "" {
		return
	}
	if sess.delegates == nil {
		sess.delegates = map[string]event.DelegateStatus{}
	}
	current, exists := sess.delegates[status.ID]
	if exists && current.Revision > status.Revision {
		return
	}
	if !exists {
		sess.delegateOrder = append(sess.delegateOrder, status.ID)
	}
	status.Evidence = append([]string(nil), status.Evidence...)
	status.ChangedFiles = append([]string(nil), status.ChangedFiles...)
	sess.delegates[status.ID] = status
}

// AppendPlan persists the latest structured plan state.
func (sess *Session) AppendPlan(data json.RawMessage) {
	sess.mu.Lock()
	sess.PlanRaw = data
	sess.mu.Unlock()
	_ = sess.append(Record{Type: "plan", Plan: data})
}

// markInterrupted appends synthetic tool results for tool calls that never
// completed, so a resumed model knows the call may or may not have run and
// nothing is silently replayed.
func (sess *Session) markInterrupted() {
	if len(sess.active) == 0 {
		return
	}
	last := sess.active[len(sess.active)-1]
	if last.Role != "assistant" || len(last.ToolCalls) == 0 {
		return
	}
	for _, call := range last.ToolCalls {
		note := provider.Message{Role: "tool", ToolCallID: call.ID, Content: "Tool call interrupted: the session ended before a result was recorded. The call may or may not have taken effect; verify before repeating any mutating operation."}
		sess.Transcript = append(sess.Transcript, note)
		sess.active = append(sess.active, note)
		sess.pendingInterrupted = append(sess.pendingInterrupted, note)
	}
}

func (sess *Session) append(record Record) error {
	record.SchemaVersion = RecordSchemaVersion
	record.Time = time.Now().UTC()
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.file == nil {
		return errors.New("session is closed")
	}
	if sess.writeErr != nil {
		return sess.writeErr
	}
	payload := append(data, '\n')
	written, err := sess.file.Write(payload)
	if err == nil && written != len(payload) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// Once a record is partially written, later records would turn the torn
		// tail into corruption in the middle of the file. Latch the failure and
		// stop appending; Load can safely discard the final torn line.
		sess.writeErr = fmt.Errorf("append durable session record: %w", err)
		return sess.writeErr
	}
	return nil
}

// Err reports the first durable-write failure observed by this session. The
// error is latched so callers can fail visibly instead of claiming a turn was
// persisted when the disk returned an error or short write.
func (sess *Session) Err() error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.writeErr
}

// Active returns the model-visible message context.
func (sess *Session) Active() []provider.Message {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return append([]provider.Message(nil), sess.active...)
}

// TranscriptMessages returns a snapshot of the complete durable conversation.
// Unlike Active, it is never shortened by context compaction. Presentation
// layers use this copy so a resumed session can show the user's full history
// without racing an in-progress append or exposing the backing slice.
func (sess *Session) TranscriptMessages() []provider.Message {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return append([]provider.Message(nil), sess.Transcript...)
}

// AppendMessage persists one transcript message.
func (sess *Session) AppendMessage(message provider.Message) {
	sess.mu.Lock()
	sess.Transcript = append(sess.Transcript, message)
	sess.active = append(sess.active, message)
	sess.Meta.UpdatedAt = time.Now().UTC()
	sess.mu.Unlock()
	_ = sess.append(Record{Type: "message", Message: &message})
}

// AppendCompaction records that `replaced` leading active messages were
// replaced by the summary message. The stored transcript keeps everything.
func (sess *Session) AppendCompaction(summary provider.Message, replaced int) {
	sess.mu.Lock()
	if replaced > 0 && replaced <= len(sess.active) {
		sess.active = append([]provider.Message{summary}, sess.active[replaced:]...)
	}
	sess.mu.Unlock()
	_ = sess.append(Record{Type: "compaction", Message: &summary, Replaced: replaced})
}

// AppendEvent persists a runtime event for replay/audit.
func (sess *Session) AppendEvent(e event.Event) {
	if e.Kind == event.KindTextDelta || e.Kind == event.KindToolCallDelta || e.Kind == event.KindToolOutput {
		return // deltas and streamed chunks are reconstructable from results
	}
	sess.mu.Lock()
	sess.retainEvent(e)
	if e.Kind == event.KindTurnEnd {
		sess.Meta.Turns++
		sess.Meta.UpdatedAt = time.Now().UTC()
	}
	if e.Kind == event.KindDelegateUpdate && e.Delegate != nil {
		sess.applyDelegate(*e.Delegate)
	}
	meta := sess.Meta
	sess.mu.Unlock()
	if e.Kind == event.KindTurnEnd {
		_ = sess.append(Record{Type: "meta", Meta: &meta})
	}
	_ = sess.append(Record{Type: "event", Event: &e})
}

// RecentEvents returns the newest durable runtime events retained in memory.
// It is intended for read-only operator projections; the session JSONL is the
// complete source when an archival audit needs more history.
func (sess *Session) RecentEvents() []event.Event {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return append([]event.Event(nil), sess.recentEvents...)
}

// Delegates returns the latest inert delegated-task snapshots in creation
// order. Resuming code decides how to label non-terminal snapshots; it never
// turns them back into executable work.
func (sess *Session) Delegates() []event.DelegateStatus {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	out := make([]event.DelegateStatus, 0, len(sess.delegateOrder))
	for _, id := range sess.delegateOrder {
		status := sess.delegates[id]
		status.Evidence = append([]string(nil), status.Evidence...)
		status.ChangedFiles = append([]string(nil), status.ChangedFiles...)
		out = append(out, status)
	}
	return out
}

// FlushInterrupted persists interruption notes discovered during load.
func (sess *Session) FlushInterrupted() {
	for _, note := range sess.pendingInterrupted {
		note := note
		_ = sess.append(Record{Type: "message", Message: &note})
	}
	sess.pendingInterrupted = nil
}

func (sess *Session) Close() {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.file != nil {
		_ = sess.file.Close()
		sess.file = nil
	}
}
