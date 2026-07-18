package session

import (
	"os"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

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
