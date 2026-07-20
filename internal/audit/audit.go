// Package audit persists every privileged-action decision and outcome as
// JSONL, outside the workspace so agent-writable files cannot alter the
// record. Each session appends to a per-workspace ledger; entries carry the
// requested action, normalized resources, decision source, matched rule,
// and eventual outcome, so any privileged action is reconstructable.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

type Entry struct {
	Time      time.Time `json:"time"`
	Kind      string    `json:"kind"` // decision or outcome
	Workspace string    `json:"workspace"`
	Tool      string    `json:"tool"`
	Summary   string    `json:"summary"`
	Risk      string    `json:"risk,omitempty"`
	Resources []string  `json:"resources,omitempty"`
	Decision  string    `json:"decision,omitempty"` // allow or deny
	Source    string    `json:"source,omitempty"`   // rule, mode, session, interactive, denied-tool, analysis
	Rule      string    `json:"rule,omitempty"`
	Outcome   string    `json:"outcome,omitempty"` // ok or error text
}

type Ledger struct {
	mu        sync.Mutex
	path      string
	workspace string
	// Redact scrubs secrets before entries reach disk.
	Redact func(string) string
}

// Dir returns the ledger directory under the per-user Collomia root.
func Dir() (string, error) {
	return userconfig.Path("audit")
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
	sum := sha256.Sum256([]byte(workspace))
	name := filepath.Base(workspace) + "-" + hex.EncodeToString(sum[:6]) + ".jsonl"
	return &Ledger{path: filepath.Join(dir, name), workspace: workspace}, nil
}

// OpenAt returns a ledger at an explicit path; used by tests.
func OpenAt(path, workspace string) *Ledger { return &Ledger{path: path, workspace: workspace} }

func (l *Ledger) Path() string { return l.path }

// Append writes one entry. Failures are deliberately swallowed after a best
// effort: the ledger must never break the agent loop, and doctor reports an
// unwritable audit directory.
func (l *Ledger) Append(entry Entry) {
	if l == nil {
		return
	}
	entry.Time = time.Now().UTC()
	entry.Workspace = l.workspace
	if l.Redact != nil {
		entry.Summary = l.Redact(entry.Summary)
		for i, r := range entry.Resources {
			entry.Resources[i] = l.Redact(r)
		}
		if entry.Outcome != "" {
			entry.Outcome = l.Redact(entry.Outcome)
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(data, '\n'))
}
