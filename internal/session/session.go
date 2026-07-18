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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
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

// Record is one line of the session log. Type selects the payload.
type Record struct {
	Type    string            `json:"type"` // meta, message, event, compaction, plan
	Time    time.Time         `json:"time"`
	Meta    *Meta             `json:"meta,omitempty"`
	Message *provider.Message `json:"message,omitempty"`
	Event   *event.Event      `json:"event,omitempty"`
	// Replaced is set on compaction records: how many active messages the
	// summary message replaces.
	Replaced int             `json:"replaced,omitempty"`
	Plan     json.RawMessage `json:"plan,omitempty"`
}

type Store struct {
	dir       string
	workspace string
}

// Open returns the store for a workspace, under the user configuration
// directory (never inside the workspace). COLLO_STATE_DIR overrides the
// base location; tests use it to stay out of the real user directory.
func Open(workspace string) (*Store, error) {
	base := os.Getenv("COLLO_STATE_DIR")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return nil, err
		}
	}
	sum := sha256.Sum256([]byte(workspace))
	dir := filepath.Join(base, "collomia", "sessions", filepath.Base(workspace)+"-"+hex.EncodeToString(sum[:6]))
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

// New creates and opens a fresh session.
func (s *Store) New(providerName, model string) (*Session, error) {
	meta := Meta{ID: newID(), Workspace: s.workspace, Provider: providerName, Model: model, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	sess := &Session{Meta: meta, store: s}
	if err := sess.open(); err != nil {
		return nil, err
	}
	if err := sess.append(Record{Type: "meta", Meta: &sess.Meta}); err != nil {
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
	return sess, nil
}

func (s *Store) Rename(id, title string) error {
	return s.amendMeta(id, func(m *Meta) { m.Title = title })
}
func (s *Store) Archive(id string, archived bool) error {
	return s.amendMeta(id, func(m *Meta) { m.Archived = archived })
}
func (s *Store) Delete(id string) error { return os.Remove(s.path(id)) }

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

	store              *Store
	mu                 sync.Mutex
	file               *os.File
	pendingInterrupted []provider.Message
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
	}
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
	_, err = sess.file.Write(append(data, '\n'))
	return err
}

// Active returns the model-visible message context.
func (sess *Session) Active() []provider.Message {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return append([]provider.Message(nil), sess.active...)
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
	if e.Kind == event.KindTextDelta || e.Kind == event.KindToolOutput {
		return // deltas and streamed chunks are reconstructable from results
	}
	if e.Kind == event.KindTurnEnd {
		sess.mu.Lock()
		sess.Meta.Turns++
		sess.Meta.UpdatedAt = time.Now().UTC()
		meta := sess.Meta
		sess.mu.Unlock()
		_ = sess.append(Record{Type: "meta", Meta: &meta})
	}
	_ = sess.append(Record{Type: "event", Event: &e})
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
