package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

type shortRecordFile struct {
	file  recordFile
	calls int
}

func (f *shortRecordFile) Write(data []byte) (int, error) {
	f.calls++
	if f.calls > 1 {
		return f.file.Write(data)
	}
	return f.file.Write(data[:max(1, len(data)/2)])
}

func (f *shortRecordFile) Close() error { return f.file.Close() }

func TestSessionLatchesShortWriteAndLeavesRecoverableTornTail(t *testing.T) {
	store, err := OpenAt(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	id := sess.Meta.ID
	short := &shortRecordFile{file: sess.file}
	sess.file = short
	sess.AppendMessage(provider.Message{Role: "user", Content: "must be durable"})
	if err := sess.Err(); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("latched error=%v", err)
	}
	// The latched failure prevents a later record from following the torn
	// line and turning it into non-tail corruption.
	sess.AppendMessage(provider.Message{Role: "assistant", Content: "not persisted"})
	if short.calls != 1 {
		t.Fatalf("writer called %d times after failure", short.calls)
	}
	sess.Close()

	recovered, err := store.Load(id)
	if err != nil {
		t.Fatalf("torn final record should be recoverable: %v", err)
	}
	defer recovered.Close()
	if len(recovered.TranscriptMessages()) != 0 {
		t.Fatalf("partial record became accepted history: %+v", recovered.TranscriptMessages())
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenAt(t.TempDir(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSessionRoundTrip(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("ollama", "qwen3-coder")
	if err != nil {
		t.Fatal(err)
	}
	sess.AppendMessage(provider.Message{Role: "user", Content: "hello"})
	sess.AppendMessage(provider.Message{Role: "assistant", Content: "hi there"})
	sess.AppendEvent(event.New(event.KindTurnEnd))
	id := sess.Meta.ID
	sess.Close()

	loaded, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	if len(loaded.Active()) != 2 {
		t.Fatalf("active=%d", len(loaded.Active()))
	}
	if loaded.Active()[1].Content != "hi there" {
		t.Fatalf("unexpected messages: %+v", loaded.Active())
	}
	if loaded.Meta.Turns != 1 || loaded.Meta.Provider != "ollama" {
		t.Fatalf("meta=%+v", loaded.Meta)
	}
}

func TestOpenUsesGlobalRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	store, err := Open(filepath.Join(home, "work", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(home, ".collomia", "sessions") + string(filepath.Separator)
	if !strings.HasPrefix(store.dir, wantRoot) {
		t.Fatalf("session dir=%q, want it below %q", store.dir, wantRoot)
	}
}

func TestCrashRecoveryDiscardsTornTailOnly(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	sess.AppendMessage(provider.Message{Role: "user", Content: "keep me"})
	id := sess.Meta.ID
	sess.Close()

	// Simulate a crash mid-write: torn partial JSON at the end of the file.
	f, err := os.OpenFile(store.path(id), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"message","message":{"role":"assistant","cont`)
	f.Close()

	loaded, err := store.Load(id)
	if err != nil {
		t.Fatalf("torn tail must not corrupt the session: %v", err)
	}
	defer loaded.Close()
	if len(loaded.Active()) != 1 || loaded.Active()[0].Content != "keep me" {
		t.Fatalf("accepted history lost: %+v", loaded.Active())
	}
}

func TestInterruptedToolCallIsMarkedNotReplayed(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	sess.AppendMessage(provider.Message{Role: "user", Content: "delete temp files"})
	sess.AppendMessage(provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "t1", Name: "run_command"}}})
	// Crash: no tool result recorded.
	id := sess.Meta.ID
	sess.Close()

	loaded, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	loaded.FlushInterrupted()
	active := loaded.Active()
	last := active[len(active)-1]
	if last.Role != "tool" || last.ToolCallID != "t1" || !strings.Contains(last.Content, "interrupted") {
		t.Fatalf("dangling tool call must be marked interrupted: %+v", last)
	}
}

func TestForkIsIndependent(t *testing.T) {
	store := testStore(t)
	original, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	original.AppendMessage(provider.Message{Role: "user", Content: "shared history"})
	forked, err := store.Fork(original.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if forked.Meta.ForkedFrom != original.Meta.ID {
		t.Fatalf("fork meta=%+v", forked.Meta)
	}
	forked.AppendMessage(provider.Message{Role: "user", Content: "fork only"})
	forked.Close()
	original.Close()

	reloaded, err := store.Load(original.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	for _, message := range reloaded.Active() {
		if message.Content == "fork only" {
			t.Fatal("fork mutated the original session")
		}
	}
}

func TestRewindCreatesIndependentCompletedTurnBranch(t *testing.T) {
	store := testStore(t)
	original, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	for turn := 1; turn <= 3; turn++ {
		original.AppendMessage(provider.Message{Role: "user", Content: fmt.Sprintf("prompt %d", turn)})
		original.AppendMessage(provider.Message{Role: "assistant", Content: fmt.Sprintf("answer %d", turn)})
		original.AppendEvent(event.New(event.KindTurnEnd))
	}
	originalID := original.Meta.ID
	original.Close()

	checkpoints, err := store.Checkpoints(originalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 3 || checkpoints[1].Turn != 2 || checkpoints[1].Prompt != "prompt 2" {
		t.Fatalf("checkpoints=%+v", checkpoints)
	}
	rewound, err := store.Rewind(originalID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rewound.Meta.ID == originalID || rewound.Meta.ForkedFrom != originalID || rewound.Meta.Turns != 1 {
		t.Fatalf("rewound meta=%+v", rewound.Meta)
	}
	if messages := rewound.TranscriptMessages(); len(messages) != 2 || messages[0].Content != "prompt 1" || messages[1].Content != "answer 1" {
		t.Fatalf("rewound transcript=%+v", messages)
	}
	rewound.AppendMessage(provider.Message{Role: "user", Content: "branch only"})
	rewound.Close()

	reloaded, err := store.Load(originalID)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if reloaded.Meta.Turns != 3 || len(reloaded.TranscriptMessages()) != 6 {
		t.Fatalf("source changed: meta=%+v transcript=%+v", reloaded.Meta, reloaded.TranscriptMessages())
	}
}

func TestRewindZeroStartsBeforeFirstTurn(t *testing.T) {
	store := testStore(t)
	original, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	original.AppendMessage(provider.Message{Role: "user", Content: "do not copy"})
	original.AppendMessage(provider.Message{Role: "assistant", Content: "done"})
	original.AppendEvent(event.New(event.KindTurnEnd))
	id := original.Meta.ID
	original.Close()

	rewound, err := store.Rewind(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rewound.Close()
	if rewound.Meta.Turns != 0 || len(rewound.TranscriptMessages()) != 0 {
		t.Fatalf("zero rewind meta=%+v transcript=%+v", rewound.Meta, rewound.TranscriptMessages())
	}
	if _, err := store.Rewind(id, 1); err == nil || !strings.Contains(err.Error(), "earlier") {
		t.Fatalf("rewinding to current turn should fail, got %v", err)
	}
}

func TestSessionArtifactsAreBoundedReadableAndFollowBranches(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewArtifactManager()
	manager.Use(sess)
	content := strings.Repeat("界", ArtifactResultLimit/3+10)
	ref, err := manager.SaveArtifact("run_command", content)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID == "" || ref.StoredBytes > ArtifactResultLimit || ref.ReturnedBytes != len(content) || ref.Complete {
		t.Fatalf("artifact ref=%+v", ref)
	}
	chunk, err := manager.ReadArtifact(ref.ID, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chunk, "begin untrusted tool output") || !strings.Contains(chunk, "next_offset=") {
		t.Fatalf("artifact chunk=%q", chunk)
	}
	tiny, err := manager.ReadArtifact(ref.ID, 0, 1)
	if err != nil || !strings.Contains(tiny, "bytes 0..3") {
		t.Fatalf("UTF-8 range did not make progress: %q err=%v", tiny, err)
	}
	if stats := manager.Stats(); stats.Count != 1 || stats.StoredBytes != ref.StoredBytes || stats.DiskBytes > ArtifactResultLimit {
		t.Fatalf("artifact stats=%+v ref=%+v", stats, ref)
	}

	sess.AppendMessage(provider.Message{Role: "user", Content: "turn"})
	sess.AppendMessage(provider.Message{Role: "assistant", Content: "done"})
	sess.AppendEvent(event.New(event.KindTurnEnd))
	id := sess.Meta.ID
	sess.Close()
	forked, err := store.Fork(id)
	if err != nil {
		t.Fatal(err)
	}
	manager.Use(forked)
	if _, err := manager.ReadArtifact(ref.ID, 0, 32); err != nil {
		t.Fatalf("fork lost referenced artifact: %v", err)
	}
	forkID := forked.Meta.ID
	forked.Close()
	if err := store.Delete(forkID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.artifactDir(forkID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact directory survived session delete: %v", err)
	}
}

func TestRewindCopiesOnlyArtifactsReferencedByRetainedTurns(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewArtifactManager()
	manager.Use(sess)
	first, err := manager.SaveArtifact("run_command", "first retained output")
	if err != nil {
		t.Fatal(err)
	}
	sess.AppendMessage(provider.Message{Role: "user", Content: "first"})
	sess.AppendMessage(provider.Message{Role: "tool", Content: "session artifact " + first.ID})
	sess.AppendEvent(event.New(event.KindTurnEnd))
	second, err := manager.SaveArtifact("run_command", "future discarded output")
	if err != nil {
		t.Fatal(err)
	}
	sess.AppendMessage(provider.Message{Role: "user", Content: "second"})
	sess.AppendMessage(provider.Message{Role: "tool", Content: "session artifact " + second.ID})
	sess.AppendEvent(event.New(event.KindTurnEnd))
	id := sess.Meta.ID
	sess.Close()

	rewound, err := store.Rewind(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rewound.Close()
	manager.Use(rewound)
	if _, err := manager.ReadArtifact(first.ID, 0, 32); err != nil {
		t.Fatalf("retained turn lost its artifact: %v", err)
	}
	if _, err := manager.ReadArtifact(second.ID, 0, 32); err == nil {
		t.Fatal("rewind copied an artifact belonging only to a discarded future turn")
	}
}

func TestSessionArtifactNormalizesInvalidUTF8(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	manager := NewArtifactManager()
	manager.Use(sess)
	ref, err := manager.SaveArtifact("binary-ish", string([]byte{0xff, 'x'}))
	if err != nil {
		t.Fatal(err)
	}
	if ref.ReturnedBytes != 2 || !ref.Complete || ref.Content != "�x" {
		t.Fatalf("normalized artifact=%+v", ref)
	}
	chunk, err := manager.ReadArtifact(ref.ID, 0, 32)
	if err != nil || !strings.Contains(chunk, "�x") {
		t.Fatalf("normalized read=%q err=%v", chunk, err)
	}
	escaped := strings.Repeat("\x00", 1<<20)
	escapedRef, err := manager.SaveArtifact("control-heavy", escaped)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(store.artifactDir(sess.Meta.ID), escapedRef.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > ArtifactResultLimit || escapedRef.StoredBytes >= len(escaped) {
		t.Fatalf("encoded artifact exceeded disk bound or was not clipped: ref=%+v size=%v", escapedRef, info.Size())
	}
}

func TestCompactionPreservesTranscriptAndShrinksActive(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		sess.AppendMessage(provider.Message{Role: "user", Content: "msg"})
	}
	summary := provider.Message{Role: "user", Content: "[Context summary] earlier discussion"}
	sess.AppendCompaction(summary, 4)
	if len(sess.Active()) != 3 {
		t.Fatalf("active=%d", len(sess.Active()))
	}
	id := sess.Meta.ID
	sess.Close()

	loaded, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	if len(loaded.Transcript) != 6 {
		t.Fatalf("full transcript must survive compaction, got %d", len(loaded.Transcript))
	}
	active := loaded.Active()
	if len(active) != 3 || !strings.Contains(active[0].Content, "summary") {
		t.Fatalf("active after reload=%+v", active)
	}
}

func TestListRenameArchiveDelete(t *testing.T) {
	store := testStore(t)
	a, _ := store.New("p", "m")
	a.AppendMessage(provider.Message{Role: "user", Content: "x"})
	a.Close()
	b, _ := store.New("p", "m")
	b.Close()

	if err := store.Rename(a.Meta.ID, "important work"); err != nil {
		t.Fatal(err)
	}
	if err := store.Archive(b.Meta.ID, true); err != nil {
		t.Fatal(err)
	}
	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("metas=%d", len(metas))
	}
	latest, err := store.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest != a.Meta.ID {
		t.Fatalf("latest should skip archived sessions, got %s", latest)
	}
	if err := store.Delete(b.Meta.ID); err != nil {
		t.Fatal(err)
	}
	metas, _ = store.List()
	if len(metas) != 1 || metas[0].Title != "important work" {
		t.Fatalf("metas=%+v", metas)
	}
}

func TestDelegatedAgentSnapshotsPersistLatestStateWithoutExecution(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	queued := event.New(event.KindDelegateUpdate)
	queued.Delegate = &event.DelegateStatus{ID: "d1", Name: "review", Status: "queued", Task: "inspect auth"}
	sess.AppendEvent(queued)
	done := event.New(event.KindDelegateUpdate)
	done.Delegate = &event.DelegateStatus{ID: "d1", Name: "review", Status: "done", Summary: "checked", ChangedFiles: []string{"auth.go"}, Usage: event.Usage{InputTokens: 12, OutputTokens: 3}}
	sess.AppendEvent(done)
	sess.Close()

	loaded, err := store.Load(sess.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	statuses := loaded.Delegates()
	if len(statuses) != 1 || statuses[0].Status != "done" || statuses[0].Summary != "checked" || statuses[0].Usage.InputTokens != 12 {
		t.Fatalf("delegates=%+v", statuses)
	}
}

func TestDelegatedAgentSnapshotsIgnoreOutOfOrderRevisions(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	newer := event.New(event.KindDelegateUpdate)
	newer.Delegate = &event.DelegateStatus{ID: "d1", Name: "review", Status: "running", CurrentAction: "reading", Revision: 3}
	sess.AppendEvent(newer)
	older := event.New(event.KindDelegateUpdate)
	older.Delegate = &event.DelegateStatus{ID: "d1", Name: "review", Status: "queued", Revision: 2}
	sess.AppendEvent(older)
	sess.Close()

	loaded, err := store.Load(sess.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	statuses := loaded.Delegates()
	if len(statuses) != 1 || statuses[0].Revision != 3 || statuses[0].CurrentAction != "reading" {
		t.Fatalf("delegate status regressed after out-of-order writes: %+v", statuses)
	}
}

func TestRecentEventsRestoreAndRemainBounded(t *testing.T) {
	store := testStore(t)
	sess, err := store.New("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < recentEventLimit+5; i++ {
		e := event.New(event.KindWarning)
		e.Text = fmt.Sprintf("warning-%d", i)
		sess.AppendEvent(e)
	}
	if got := sess.RecentEvents(); len(got) != recentEventLimit || got[0].Text != "warning-5" {
		t.Fatalf("live recent events: len=%d first=%q", len(got), got[0].Text)
	}
	id := sess.Meta.ID
	sess.Close()

	loaded, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	got := loaded.RecentEvents()
	if len(got) != recentEventLimit || got[0].Text != "warning-5" || got[len(got)-1].Text != fmt.Sprintf("warning-%d", recentEventLimit+4) {
		t.Fatalf("restored recent events: len=%d first=%q last=%q", len(got), got[0].Text, got[len(got)-1].Text)
	}
}
