// Package audit persists every privileged-action decision and outcome as
// JSONL, outside the workspace so agent-writable files cannot alter the
// record. Each session appends to a per-workspace ledger; entries carry the
// requested action, normalized resources, decision source, matched rule,
// and eventual outcome, so any privileged action is reconstructable.
//
// Three properties make the file a record rather than a best-effort log.
//
// Every entry names who acted. One workspace ledger receives writes from the
// primary agent, from every concurrently scheduled delegated agent, and from
// any other Collomia process open on the same directory. Without the session
// and actor identity attached at Identify time, those streams interleave into
// something no reader can separate again.
//
// A gap is declared rather than hidden. Append cannot fail the agent loop —
// an unwritable ledger must not stop work the user authorized — but it must
// not produce a file that reads as complete either. Failures are counted, the
// first one is reported through OnFailure, and the next successful write is
// preceded by a "gap" entry stating how many entries were lost and when the
// loss began. A reader that never sees a gap entry can trust what it read.
//
// Growth is bounded, and the bound admits what it discards. The ledger
// rotates at MaxBytes into a single retained previous generation; the fresh
// file opens with a "rotation" entry, which says explicitly when an older
// generation was dropped to make room.
package audit

import (
	"bufio"
	"bytes"
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

	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

// Entry kinds. Decision and outcome are the record itself; gap and rotation
// are the ledger describing its own completeness.
const (
	KindDecision = "decision"
	KindOutcome  = "outcome"
	KindGap      = "gap"
	KindRotation = "rotation"
)

// ActorPrimary marks entries written by the session's own agent. Delegated
// agents use "agent:<profile>" and carry the task id.
const ActorPrimary = "primary"

// DefaultMaxBytes bounds one ledger generation. Two generations are retained,
// so a workspace's audit history occupies at most twice this on disk.
const DefaultMaxBytes int64 = 64 << 20

type Entry struct {
	Time      time.Time `json:"time"`
	Kind      string    `json:"kind"`
	Workspace string    `json:"workspace"`
	// Session is the durable session id, empty for an ephemeral run. Actor is
	// ActorPrimary or "agent:<profile>"; Task is the delegated task id. These
	// are what let one file hold concurrent actors and still be read back.
	Session   string   `json:"session,omitempty"`
	Actor     string   `json:"actor,omitempty"`
	Task      string   `json:"task,omitempty"`
	Tool      string   `json:"tool,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Risk      string   `json:"risk,omitempty"`
	Resources []string `json:"resources,omitempty"`
	Decision  string   `json:"decision,omitempty"` // allow or deny
	// Source is what decided. The permission layer owns the complete set;
	// TestGuideDocumentsEveryAuditDecisionSource keeps the user guide's table
	// in step with it, because a stale comment here is what let that table
	// ship listing six of the thirteen.
	Source  string `json:"source,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Outcome string `json:"outcome,omitempty"` // ok or error text
	// Dropped and Since describe a gap entry: how many entries could not be
	// written, and when the first of them was attempted. Since is a pointer
	// because omitempty does not omit a zero time.Time, and a zero timestamp
	// on every ordinary entry is noise a consumer would have to learn to
	// ignore.
	Dropped int        `json:"dropped,omitempty"`
	Since   *time.Time `json:"since,omitempty"`
	// Reason explains a gap or a rotation in one line.
	Reason string `json:"reason,omitempty"`
	// Discarded is set on a rotation entry when an older generation had to be
	// removed to make room, so a shortened history says so itself.
	Discarded bool `json:"discarded,omitempty"`
}

// Denied reports whether the entry records a refused action.
func (e Entry) Denied() bool { return e.Kind == KindDecision && e.Decision == "deny" }

// Failed reports whether the entry records an execution that did not succeed.
func (e Entry) Failed() bool {
	return e.Kind == KindOutcome && e.Outcome != "" && e.Outcome != "ok"
}

type Ledger struct {
	mu        sync.Mutex
	path      string
	workspace string
	session   string
	actor     string
	task      string
	// Redact scrubs secrets before entries reach disk.
	Redact func(string) string
	// OnFailure reports the first write failure, and the recovery that
	// follows it. It is called without the ledger lock held so a handler may
	// route the report back through the runtime's event funnel.
	OnFailure func(error)
	// MaxBytes bounds one generation. Zero means DefaultMaxBytes.
	MaxBytes int64

	dropped   int
	firstDrop time.Time
	firstErr  error
	reported  bool
}

// Dir returns the ledger directory under the per-user Collomia root.
func Dir() (string, error) {
	return userconfig.Path("audit")
}

// FileName is the ledger file for a workspace path. The hash keeps two
// directories with the same base name apart.
func FileName(workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return filepath.Base(workspace) + "-" + hex.EncodeToString(sum[:6]) + ".jsonl"
}

// Open returns the ledger for a workspace, creating its directory.
func Open(workspace string) (*Ledger, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Ledger{path: filepath.Join(dir, FileName(workspace)), workspace: workspace}, nil
}

// OpenAt returns a ledger at an explicit path, for tests and for tools that
// read a ledger the caller already located.
func OpenAt(path, workspace string) *Ledger { return &Ledger{path: path, workspace: workspace} }

func (l *Ledger) Path() string { return l.path }

// Identify names who is writing. Every entry carries it, so one workspace
// file can hold the primary agent and several concurrent delegated agents and
// still be separable afterwards. Call it before the first Append.
func (l *Ledger) Identify(session, actor, task string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.session, l.actor, l.task = session, actor, task
	l.mu.Unlock()
}

// AgentActor renders the actor string for a delegated agent profile.
func AgentActor(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = "unnamed"
	}
	return "agent:" + profile
}

// Degraded reports how many entries have been lost and why, so a status
// surface can say the record is incomplete while the session is still
// running. A zero count means every entry reached disk.
func (l *Ledger) Degraded() (dropped int, since time.Time, err error) {
	if l == nil {
		return 0, time.Time{}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dropped, l.firstDrop, l.firstErr
}

// Append writes one entry. A write failure never breaks the agent loop, but
// it is counted, reported once through OnFailure, and declared in the file by
// a gap entry as soon as writing succeeds again.
func (l *Ledger) Append(entry Entry) {
	if l == nil {
		return
	}
	l.mu.Lock()
	entry.Time = time.Now().UTC()
	entry.Workspace = l.workspace
	entry.Session, entry.Actor, entry.Task = l.session, l.actor, l.task
	if l.Redact != nil {
		entry.Summary = l.Redact(entry.Summary)
		// Copy rather than redact in place: Resources is the caller's slice,
		// and a display surface holding the same backing array must keep
		// showing what the user was actually asked to approve.
		if len(entry.Resources) > 0 {
			scrubbed := make([]string, len(entry.Resources))
			for i, resource := range entry.Resources {
				scrubbed[i] = l.Redact(resource)
			}
			entry.Resources = scrubbed
		}
		if entry.Outcome != "" {
			entry.Outcome = l.Redact(entry.Outcome)
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		// An entry that cannot be encoded is as lost as one that cannot be
		// written, and is counted the same way.
		l.noteFailureLocked(err)
		report, failure := l.pendingReportLocked()
		l.mu.Unlock()
		if report {
			l.OnFailure(failure)
		}
		return
	}
	pending := l.gapRecordLocked()
	writeErr := l.writeLocked(append(pending, append(data, '\n')...))
	if writeErr != nil {
		l.noteFailureLocked(writeErr)
	} else {
		l.dropped, l.firstDrop, l.firstErr = 0, time.Time{}, nil
	}
	report, failure := l.pendingReportLocked()
	l.mu.Unlock()
	if report {
		l.OnFailure(failure)
	}
}

// gapRecordLocked renders the marker that declares entries lost since the last
// successful write. It is prepended to the next entry so the declaration and
// the resumption land in one write: a gap that itself failed to persist would
// leave exactly the silent hole this exists to prevent.
func (l *Ledger) gapRecordLocked() []byte {
	if l.dropped == 0 {
		return nil
	}
	reason := "unknown write failure"
	if l.firstErr != nil {
		reason = l.firstErr.Error()
	}
	since := l.firstDrop
	gap := Entry{
		Time: time.Now().UTC(), Kind: KindGap, Workspace: l.workspace,
		Session: l.session, Actor: l.actor, Task: l.task,
		Dropped: l.dropped, Since: &since, Reason: reason,
	}
	data, err := json.Marshal(gap)
	if err != nil {
		return nil
	}
	return append(data, '\n')
}

func (l *Ledger) noteFailureLocked(err error) {
	if l.dropped == 0 {
		l.firstDrop = time.Now().UTC()
		l.firstErr = err
	}
	l.dropped++
}

// pendingReportLocked decides whether OnFailure owes the caller a report. The
// first failure is reported; recovery is reported once; a continuing failure
// is not repeated, because a full disk would otherwise emit a warning per
// authorized action.
func (l *Ledger) pendingReportLocked() (bool, error) {
	if l.OnFailure == nil {
		return false, nil
	}
	switch {
	case l.dropped > 0 && !l.reported:
		l.reported = true
		return true, fmt.Errorf("audit ledger write failed, %s is now an incomplete record: %w", l.path, l.firstErr)
	case l.dropped == 0 && l.reported:
		l.reported = false
		return true, nil
	}
	return false, nil
}

func (l *Ledger) writeLocked(data []byte) error {
	if err := l.rotateLocked(int64(len(data))); err != nil {
		return err
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	written, err := file.Write(data)
	if err == nil && written < len(data) {
		// A short write leaves a torn line. Report it so the entry counts as
		// dropped and the reader's own malformed-line accounting agrees.
		err = io.ErrShortWrite
	}
	// The ledger is flushed per entry, where the session is flushed per turn.
	// The asymmetry is deliberate and follows from what each file is for. A
	// session is the user's own conversation, and losing the tail of an
	// interrupted turn costs them a turn they watched fail. This file is the
	// record of what an agent was permitted to do, read after the fact by
	// someone reconstructing an incident and by the independent review that
	// gates 1.0 — and a record that quietly loses its last entries during the
	// event worth investigating is not one of those. A flush costs about four
	// milliseconds against roughly two entries per privileged action, which is
	// the price of the claim the README already makes for it.
	//
	// A failed flush is returned like a failed write, so it is counted as a
	// dropped entry and declared as a gap rather than passing silently.
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// rotateLocked moves the current generation aside when it would exceed
// MaxBytes, keeping exactly one previous generation. The new file opens with a
// rotation entry so a shortened history is visible in the record rather than
// inferred from a missing file.
func (l *Ledger) rotateLocked(incoming int64) error {
	limit := l.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	info, err := os.Stat(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Size()+incoming <= limit {
		return nil
	}
	previous := PreviousPath(l.path)
	discarded := false
	if _, err := os.Stat(previous); err == nil {
		discarded = true
	}
	if err := os.Rename(l.path, previous); err != nil {
		return err
	}
	marker := Entry{
		Time: time.Now().UTC(), Kind: KindRotation, Workspace: l.workspace,
		Session: l.session, Actor: l.actor, Task: l.task,
		Reason:    fmt.Sprintf("rotated at %d bytes; previous generation is %s", info.Size(), filepath.Base(previous)),
		Discarded: discarded,
	}
	data, marshalErr := json.Marshal(marker)
	if marshalErr != nil {
		return nil
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(data, '\n'))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// PreviousPath is the retained older generation for a ledger path.
func PreviousPath(path string) string {
	return strings.TrimSuffix(path, ".jsonl") + ".1.jsonl"
}

// Filter selects entries when reading a ledger back.
type Filter struct {
	Session string
	Actor   string
	Tool    string
	Since   time.Time
	// DeniedOnly keeps refused decisions and failed outcomes — the two things
	// someone reconstructing an incident is usually looking for.
	DeniedOnly bool
	// Limit keeps the most recent N matching entries. Zero means all.
	Limit int
}

func (f Filter) match(entry Entry) bool {
	if f.Session != "" && entry.Session != f.Session {
		return false
	}
	if f.Actor != "" && entry.Actor != f.Actor {
		return false
	}
	if f.Tool != "" && entry.Tool != f.Tool {
		return false
	}
	if !f.Since.IsZero() && entry.Time.Before(f.Since) {
		return false
	}
	if f.DeniedOnly && !entry.Denied() && !entry.Failed() {
		return false
	}
	return true
}

// Report is what a ledger says about itself: the matching entries, and every
// reason the answer might be incomplete.
type Report struct {
	Entries []Entry
	// Files read, oldest generation first.
	Files []string
	// Total entries present before Filter and Limit were applied.
	Total int
	// Dropped sums every declared gap; Gaps is the count of gap markers.
	Dropped int
	Gaps    int
	// Malformed counts lines that did not parse — a torn write, or a file
	// truncated by something other than Collomia.
	Malformed int
	// Rotations counts retained rotation markers; Discarded is true when one
	// of them says an older generation was removed to make room.
	Rotations int
	Discarded bool
}

// Complete reports whether the ledger has no declared or detected holes.
func (r Report) Complete() bool { return r.Dropped == 0 && r.Malformed == 0 && !r.Discarded }

// Read parses a ledger and its retained previous generation, oldest first.
// A missing ledger is an empty report rather than an error: a workspace where
// no privileged action has been taken has nothing to record.
func Read(path string, filter Filter) (Report, error) {
	report := Report{}
	for _, candidate := range []string{PreviousPath(path), path} {
		file, err := os.Open(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return report, err
		}
		report.Files = append(report.Files, candidate)
		err = scan(file, filter, &report)
		file.Close()
		if err != nil {
			return report, err
		}
	}
	sort.SliceStable(report.Entries, func(i, j int) bool {
		return report.Entries[i].Time.Before(report.Entries[j].Time)
	})
	if filter.Limit > 0 && len(report.Entries) > filter.Limit {
		report.Entries = report.Entries[len(report.Entries)-filter.Limit:]
	}
	return report, nil
}

func scan(reader io.Reader, filter Filter, report *Report) error {
	scanner := bufio.NewScanner(reader)
	// Summaries and resource lists are bounded but not short; a command line
	// plus its normalized resources can exceed the default 64 KiB token.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			report.Malformed++
			continue
		}
		report.Total++
		switch entry.Kind {
		case KindGap:
			report.Gaps++
			report.Dropped += entry.Dropped
		case KindRotation:
			report.Rotations++
			if entry.Discarded {
				report.Discarded = true
			}
		}
		if filter.match(entry) {
			report.Entries = append(report.Entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		// A line longer than the buffer is a damaged file, not a fatal error
		// for the reader: report what parsed and count the rest.
		if errors.Is(err, bufio.ErrTooLong) {
			report.Malformed++
			return nil
		}
		return err
	}
	return nil
}
