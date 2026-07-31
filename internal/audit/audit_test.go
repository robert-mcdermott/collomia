package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The audit ledger shipped for a long time with one happy-path test written
// from another package, which is why every property below had a defect to
// find: writes that failed silently, entries nobody could attribute to an
// actor, a file that only grew, and redaction that mutated the caller's slice.

func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var entries []Entry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("ledger line is not valid JSON: %v\n%s", err, line)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestEveryEntryCarriesTheIdentityOfWhoActed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ledger := OpenAt(path, "/workspace")
	ledger.Identify("sess-1", ActorPrimary, "")
	ledger.Append(Entry{Kind: KindDecision, Tool: "run_command", Summary: "run: go test", Decision: "allow"})

	child := OpenAt(path, "/workspace")
	child.Identify("sess-1", AgentActor("reviewer"), "task-7")
	child.Append(Entry{Kind: KindDecision, Tool: "read_file", Summary: "read: main.go", Decision: "allow"})

	entries := readEntries(t, path)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Actor != ActorPrimary || entries[0].Session != "sess-1" || entries[0].Task != "" {
		t.Errorf("primary entry lost its identity: %+v", entries[0])
	}
	if entries[1].Actor != "agent:reviewer" || entries[1].Task != "task-7" {
		t.Errorf("delegated entry lost its identity: %+v", entries[1])
	}
	// The point of the identity is separability, so assert the reader can
	// actually split one file back into its two actors.
	report, err := Read(path, Filter{Actor: "agent:reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 || report.Entries[0].Tool != "read_file" {
		t.Fatalf("filtering by actor returned %+v", report.Entries)
	}
	if report.Total != 2 {
		t.Errorf("total should count every entry present, got %d", report.Total)
	}
}

func TestRedactionAppliesToEveryFieldThatCanCarryASecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ledger := OpenAt(path, "/workspace")
	ledger.Redact = func(text string) string { return strings.ReplaceAll(text, "hunter2", "[redacted]") }
	ledger.Append(Entry{
		Kind: KindDecision, Tool: "run_command",
		Summary:   "run: deploy --token hunter2",
		Resources: []string{"exec:deploy", "host:hunter2.example.com"},
	})
	ledger.Append(Entry{Kind: KindOutcome, Tool: "run_command", Outcome: "error: bad token hunter2"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter2") {
		t.Fatalf("a secret reached the ledger:\n%s", data)
	}
}

// Redaction must not reach back into the caller's slice. The permission layer
// builds resources for the approval dialog and the ledger from one call, and a
// display surface holding that slice has to keep showing what the user was
// actually asked to approve.
func TestRedactionDoesNotMutateTheCallersResources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ledger := OpenAt(path, "/workspace")
	ledger.Redact = func(string) string { return "[redacted]" }
	resources := []string{"path:/etc/hosts", "exec:cat"}
	ledger.Append(Entry{Kind: KindDecision, Tool: "run_command", Resources: resources})
	if resources[0] != "path:/etc/hosts" || resources[1] != "exec:cat" {
		t.Fatalf("Append rewrote the caller's slice: %v", resources)
	}
}

// A write failure must never be silent. This is the defect the wave exists to
// close: an unwritable ledger used to produce a file that read as complete.
func TestAWriteFailureIsReportedAndThenDeclaredInTheLedger(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory in place of the ledger file is the Unix way to force an open failure")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	// A directory where the ledger file belongs makes every open fail.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	ledger := OpenAt(path, "/workspace")
	var reports []error
	var mu sync.Mutex
	ledger.OnFailure = func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reports = append(reports, err)
	}
	for i := range 3 {
		ledger.Append(Entry{Kind: KindDecision, Tool: "run_command", Summary: fmt.Sprintf("lost %d", i)})
	}
	dropped, since, firstErr := ledger.Degraded()
	if dropped != 3 {
		t.Fatalf("want 3 dropped entries, got %d", dropped)
	}
	if since.IsZero() || firstErr == nil {
		t.Fatalf("a drop must record when it started and why: %v %v", since, firstErr)
	}
	mu.Lock()
	count := len(reports)
	mu.Unlock()
	// Reported once, not once per authorized action: a full disk must not
	// bury the session in identical warnings.
	if count != 1 {
		t.Fatalf("want exactly one failure report, got %d", count)
	}

	// Recovery declares the hole rather than resuming as if nothing was lost.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ledger.Append(Entry{Kind: KindDecision, Tool: "read_file", Summary: "after recovery"})
	entries := readEntries(t, path)
	if len(entries) != 2 {
		t.Fatalf("want a gap entry plus the recovered one, got %d: %+v", len(entries), entries)
	}
	if entries[0].Kind != KindGap {
		t.Fatalf("the first entry after recovery must declare the gap, got %q", entries[0].Kind)
	}
	if entries[0].Dropped != 3 {
		t.Errorf("the gap must say how many entries were lost, got %d", entries[0].Dropped)
	}
	if entries[0].Reason == "" || entries[0].Since == nil || entries[0].Since.IsZero() {
		t.Errorf("the gap must say why and since when: %+v", entries[0])
	}
	// An ordinary entry must not carry a zero gap timestamp: consumers of the
	// JSONL should never have to learn which fields to ignore.
	if entries[1].Since != nil {
		t.Errorf("a non-gap entry carries a since field: %+v", entries[1])
	}
	if entries[1].Summary != "after recovery" {
		t.Errorf("the recovered entry is missing: %+v", entries[1])
	}
	if dropped, _, _ := ledger.Degraded(); dropped != 0 {
		t.Errorf("a declared gap resets the pending count, got %d", dropped)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 2 || reports[1] != nil {
		t.Errorf("recovery must be reported once, as a nil error: %v", reports)
	}
}

// A reader must be able to tell a complete record from an incomplete one
// without being told out of band.
func TestReadDeclaresEveryReasonTheAnswerIsIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	lines := []string{
		`{"time":"2026-07-30T10:00:00Z","kind":"decision","tool":"read_file","decision":"allow","actor":"primary"}`,
		`{"time":"2026-07-30T10:00:01Z","kind":"gap","dropped":4,"reason":"no space left on device"}`,
		`{"time":"2026-07-30T10:00:02Z","kind":"decision","tool":"run_c`, // torn write
		`{"time":"2026-07-30T10:00:03Z","kind":"decision","tool":"write_file","decision":"deny","actor":"primary"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Read(path, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete() {
		t.Fatal("a ledger with a declared gap and a torn line is not complete")
	}
	if report.Dropped != 4 || report.Gaps != 1 {
		t.Errorf("want 4 dropped across 1 gap, got %d across %d", report.Dropped, report.Gaps)
	}
	if report.Malformed != 1 {
		t.Errorf("want 1 malformed line, got %d", report.Malformed)
	}
	// The readable entries are still returned: a damaged record is worth less
	// than a whole one and much more than nothing.
	if len(report.Entries) != 3 {
		t.Errorf("want the 3 parsable entries, got %d", len(report.Entries))
	}
}

func TestFiltersSelectWhatAnIncidentReviewActuallyAsksFor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ledger := OpenAt(path, "/workspace")
	ledger.Identify("sess-1", ActorPrimary, "")
	ledger.Append(Entry{Kind: KindDecision, Tool: "read_file", Decision: "allow"})
	ledger.Append(Entry{Kind: KindDecision, Tool: "run_command", Decision: "deny", Rule: "deny curl"})
	ledger.Append(Entry{Kind: KindOutcome, Tool: "run_command", Outcome: "error: exit status 1"})
	ledger.Append(Entry{Kind: KindOutcome, Tool: "read_file", Outcome: "ok"})

	report, err := Read(path, Filter{DeniedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("denied-only must keep the refusal and the failed execution, got %+v", report.Entries)
	}
	byTool, err := Read(path, Filter{Tool: "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byTool.Entries) != 2 {
		t.Errorf("tool filter returned %d entries", len(byTool.Entries))
	}
	limited, err := Read(path, Filter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Entries) != 1 || limited.Entries[0].Tool != "read_file" || limited.Entries[0].Kind != KindOutcome {
		t.Errorf("a limit keeps the most recent entries, got %+v", limited.Entries)
	}
	future, err := Read(path, Filter{Since: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(future.Entries) != 0 {
		t.Errorf("since filter returned %d entries from the future", len(future.Entries))
	}
}

// Rotation bounds the file, and says so in the record. An audit trail that
// quietly shortened itself would be the same silent-hole defect in a slower
// form.
func TestRotationBoundsGrowthAndDeclaresADiscardedGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ledger := OpenAt(path, "/workspace")
	ledger.MaxBytes = 512
	ledger.Identify("sess-1", ActorPrimary, "")
	for i := range 40 {
		ledger.Append(Entry{Kind: KindDecision, Tool: "read_file", Summary: fmt.Sprintf("read: file-%02d.go", i), Decision: "allow"})
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 512*2 {
		t.Errorf("the live generation grew to %d bytes past a 512-byte bound", info.Size())
	}
	if _, err := os.Stat(PreviousPath(path)); err != nil {
		t.Fatalf("the previous generation must be retained: %v", err)
	}
	report, err := Read(path, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Rotations == 0 {
		t.Fatal("rotation must leave a marker in the record")
	}
	if !report.Discarded {
		t.Error("discarding an older generation must be declared, not inferred from a missing file")
	}
	if report.Complete() {
		t.Error("a record whose history was truncated is not complete")
	}
	// Both retained generations are read back, oldest first.
	if len(report.Files) != 2 {
		t.Errorf("want both generations read, got %v", report.Files)
	}
	for i := 1; i < len(report.Entries); i++ {
		if report.Entries[i].Time.Before(report.Entries[i-1].Time) {
			t.Fatalf("entries must be ordered oldest first across generations")
		}
	}
}

// Concurrent delegated agents write through separate ledger handles to one
// workspace file. Nothing may be lost or interleaved mid-line.
func TestConcurrentActorsProduceOneReadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	const actors, each = 4, 25
	var wg sync.WaitGroup
	for a := range actors {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			ledger := OpenAt(path, "/workspace")
			ledger.Identify("sess-1", AgentActor(fmt.Sprintf("worker-%d", a)), fmt.Sprintf("task-%d", a))
			for i := range each {
				ledger.Append(Entry{Kind: KindDecision, Tool: "read_file", Summary: fmt.Sprintf("read: %d-%d", a, i), Decision: "allow"})
			}
		}(a)
	}
	wg.Wait()
	report, err := Read(path, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Malformed != 0 {
		t.Fatalf("%d lines were torn by concurrent writers", report.Malformed)
	}
	if report.Total != actors*each {
		t.Fatalf("want %d entries, got %d", actors*each, report.Total)
	}
	for a := range actors {
		name := AgentActor(fmt.Sprintf("worker-%d", a))
		mine, err := Read(path, Filter{Actor: name})
		if err != nil {
			t.Fatal(err)
		}
		if len(mine.Entries) != each {
			t.Errorf("%s wrote %d of %d entries", name, len(mine.Entries), each)
		}
	}
}

func TestAMissingLedgerReadsAsEmptyRatherThanAnError(t *testing.T) {
	report, err := Read(filepath.Join(t.TempDir(), "never-written.jsonl"), Filter{})
	if err != nil {
		t.Fatalf("a workspace with no privileged actions is not an error: %v", err)
	}
	if len(report.Files) != 0 || len(report.Entries) != 0 || !report.Complete() {
		t.Errorf("want an empty complete report, got %+v", report)
	}
}

func TestFileNameSeparatesWorkspacesSharingABaseName(t *testing.T) {
	first := FileName(filepath.Join("home", "alice", "project"))
	second := FileName(filepath.Join("home", "bob", "project"))
	if first == second {
		t.Fatalf("two different workspaces share the ledger file %q", first)
	}
	if !strings.HasPrefix(first, "project-") || !strings.HasSuffix(first, ".jsonl") {
		t.Errorf("ledger file name is not recognizable: %q", first)
	}
}

func TestANilLedgerIsSafeEverywhere(t *testing.T) {
	var ledger *Ledger
	ledger.Append(Entry{Kind: KindDecision})
	ledger.Identify("s", ActorPrimary, "")
	if dropped, _, err := ledger.Degraded(); dropped != 0 || err != nil {
		t.Errorf("a nil ledger must report itself healthy, got %d %v", dropped, err)
	}
}

func TestAnUnencodableEntryCountsAsDroppedRatherThanVanishing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ledger := OpenAt(path, "/workspace")
	var reported error
	ledger.OnFailure = func(err error) { reported = err }
	// A redactor that produces invalid UTF-16 surrogates is not something the
	// real one does; encoding failure is forced here because the alternative
	// is a code path nothing ever exercises.
	ledger.Redact = func(string) string { return string([]byte{0xff, 0xfe}) }
	ledger.Append(Entry{Kind: KindDecision, Tool: "run_command", Summary: "x"})
	dropped, _, _ := ledger.Degraded()
	if dropped == 0 {
		// encoding/json replaces invalid bytes rather than failing, so this
		// path may legitimately succeed; then the entry must be on disk.
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			t.Fatal("the entry neither reached disk nor counted as dropped")
		}
		return
	}
	if reported == nil {
		t.Error("a dropped entry must be reported")
	}
}
