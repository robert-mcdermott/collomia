package session

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// countingRecordFile records how often the session asked for a flush, which is
// the only observable half of durability from inside a test: whether fsync
// actually reached the platter is not something a process can check.
type countingRecordFile struct {
	file   recordFile
	syncs  int
	writes int
	syncFn func() error
}

func (f *countingRecordFile) Write(data []byte) (int, error) {
	f.writes++
	return f.file.Write(data)
}

func (f *countingRecordFile) Sync() error {
	f.syncs++
	if f.syncFn != nil {
		return f.syncFn()
	}
	return f.file.Sync()
}

func (f *countingRecordFile) Close() error { return f.file.Close() }

func newCountedSession(t *testing.T) (*Session, *countingRecordFile) {
	t.Helper()
	store, err := OpenAt(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	counted := &countingRecordFile{file: sess.file}
	sess.file = counted
	return sess, counted
}

// The guarantee: a turn that finished is on disk. Without this, a power cut
// loses everything the operating system had not yet written back, which for a
// short session is the whole session.
func TestCompletedTurnIsFlushedToDisk(t *testing.T) {
	sess, counted := newCountedSession(t)
	sess.AppendMessage(provider.Message{Role: "user", Content: "hello"})
	sess.AppendEvent(event.Event{Kind: event.KindToolStart})
	if counted.syncs != 0 {
		t.Fatalf("session flushed %d times mid-turn; the boundary is the turn, not the record", counted.syncs)
	}
	sess.AppendEvent(event.Event{Kind: event.KindTurnEnd})
	if counted.syncs != 1 {
		t.Fatalf("completed turn produced %d flushes, want exactly 1", counted.syncs)
	}
}

// Per-record syncing was measured and rejected: about four milliseconds on a
// local SSD, more on network or encrypted storage, against tens of records a
// turn. This pins the decision so it is not quietly reversed by someone adding
// a flush to append.
func TestOrdinaryRecordsAreNotFlushedIndividually(t *testing.T) {
	sess, counted := newCountedSession(t)
	for i := 0; i < 25; i++ {
		sess.AppendEvent(event.Event{Kind: event.KindToolResult})
	}
	sess.AppendMessage(provider.Message{Role: "assistant", Content: "text"})
	if counted.syncs != 0 {
		t.Fatalf("26 records produced %d flushes, want 0 before the turn ends", counted.syncs)
	}
	if counted.writes == 0 {
		t.Fatal("no records were written at all")
	}
}

// Teardown is the other point the claim rests on. SIGHUP now reaches Close, and
// a Close that did not flush would still lose everything appended since the
// last turn boundary.
func TestCloseFlushesBeforeClosing(t *testing.T) {
	sess, counted := newCountedSession(t)
	sess.AppendMessage(provider.Message{Role: "user", Content: "written after the last turn ended"})
	sess.Close()
	if counted.syncs != 1 {
		t.Fatalf("Close produced %d flushes, want exactly 1", counted.syncs)
	}
}

// An fsync error is not a retryable inconvenience. On Linux the kernel may
// already have discarded the pages it could not write, so the records this
// flush was meant to protect can be gone. Continuing to append would keep
// producing a file that reads as complete.
func TestSyncFailureIsLatchedLikeAWriteFailure(t *testing.T) {
	sess, counted := newCountedSession(t)
	diskErr := errors.New("injected fsync failure")
	counted.syncFn = func() error { return diskErr }

	sess.AppendEvent(event.Event{Kind: event.KindTurnEnd})
	if err := sess.Err(); !errors.Is(err, diskErr) {
		t.Fatalf("session error after a failed flush = %v, want it to wrap the disk error", err)
	}
	writesBefore := counted.writes
	sess.AppendMessage(provider.Message{Role: "user", Content: "should not be appended"})
	if counted.writes != writesBefore {
		t.Error("the session kept appending after a failed flush")
	}
	if err := sess.Sync(); !errors.Is(err, diskErr) {
		t.Errorf("Sync after latching returned %v, want the latched error", err)
	}
}

func TestSyncOnClosedSessionReportsRatherThanPanics(t *testing.T) {
	sess, _ := newCountedSession(t)
	sess.Close()
	if err := sess.Sync(); err == nil {
		t.Fatal("syncing a closed session reported success")
	}
}

// Power loss does not tear one line politely. It loses whatever the operating
// system had not written back, which can be any suffix of the file, so the
// contract has to hold at every byte rather than at the record boundaries a
// hand-picked fixture would land on.
//
// The contract is not "always loads". A session whose metadata record never
// reached disk is not a damaged session, it is not a session: it has no id,
// provider, or model to resume. Refusing that clearly is right, and the sweep
// below is what distinguishes it from the case that matters — a truncation
// after the metadata, where every record that did reach disk must come back
// intact and no partially written record may be half-decoded into a message
// with missing fields.
func TestLoadHandlesEveryTruncationPoint(t *testing.T) {
	store, err := OpenAt(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	id := sess.Meta.ID
	const messages = 6
	for i := 0; i < messages; i++ {
		sess.AppendMessage(provider.Message{Role: "user", Content: strings.Repeat("m", 10+i)})
	}
	sess.Close()

	path := store.path(id)
	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The number of bytes of metadata JSON, not including its terminator. The
	// loader accepts a final line whose newline never reached disk — a complete
	// JSON object is complete whether or not the byte after it arrived — which
	// is exactly the tolerance power loss requires, so the boundary is the end
	// of the content and not the end of the line.
	metaEnd := strings.IndexByte(string(whole), '\n')
	if metaEnd <= 0 {
		t.Fatal("fixture has no metadata record")
	}

	recovered := 0
	for cut := 0; cut <= len(whole); cut++ {
		if err := os.WriteFile(path, whole[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := store.Load(id)
		if cut < metaEnd {
			// Below the metadata boundary there is nothing to resume, and
			// saying so is the correct answer rather than a fallback.
			if err == nil {
				loaded.Close()
				t.Fatalf("truncated to %d bytes (before the metadata ends at %d): Load succeeded with no metadata", cut, metaEnd)
			}
			continue
		}
		if err != nil {
			t.Fatalf("truncated to %d/%d bytes: Load failed after complete metadata: %v", cut, len(whole), err)
		}
		if loaded.Meta.ID != id {
			t.Fatalf("truncated to %d bytes: recovered session id %q, want %q", cut, loaded.Meta.ID, id)
		}
		for _, message := range loaded.Transcript {
			if message.Role == "" || message.Content == "" {
				t.Fatalf("truncated to %d bytes: recovered a half-decoded message %+v", cut, message)
			}
		}
		if len(loaded.Transcript) > messages {
			t.Fatalf("truncated to %d bytes: recovered %d messages, more than the %d written", cut, len(loaded.Transcript), messages)
		}
		recovered++
		loaded.Close()
	}
	if recovered == 0 {
		t.Fatal("no truncation point produced a loadable session; the sweep proved nothing")
	}
}
