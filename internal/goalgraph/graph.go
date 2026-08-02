// Package goalgraph owns the durable operational state for an orchestrated
// goal. It deliberately does not turn plan.Plan into a scheduler database:
// logical intent can be proposed by a model, while readiness, attempts,
// evidence, freshness, recovery, and terminal acceptance remain runtime state.
package goalgraph

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion              = 1
	defaultMaxAttempts         = 2
	defaultMaxRevisions        = 2
	maxCompletionInterventions = 2
	maxGraphNodes              = 12
	maxGoalBytes               = 2048
	maxTitleBytes              = 512
	maxAcceptanceItems         = 8
	maxAcceptanceBytes         = 512
	defaultMaxReadConcurrency  = 2
	defaultMaxReadStarts       = 8
	defaultMaxReadTokens       = 64_000
	defaultMaxReadWallSeconds  = 15 * 60
	defaultReadTaskWallSeconds = 5 * 60
)

type Execution string

const (
	ExecutionPrimary  Execution = "primary"
	ExecutionReadOnly Execution = "read_only"
)

type NodeState string

const (
	NodeProposed        NodeState = "proposed"
	NodeReady           NodeState = "ready"
	NodeRunning         NodeState = "running"
	NodeRetryable       NodeState = "retryable"
	NodeStale           NodeState = "stale"
	NodeBlocked         NodeState = "blocked"
	NodeCancelled       NodeState = "cancelled"
	NodeBudgetExhausted NodeState = "budget_exhausted"
	NodeDone            NodeState = "done"
)

type AttemptState string

const (
	AttemptRunning         AttemptState = "running"
	AttemptRetryable       AttemptState = "retryable"
	AttemptFailed          AttemptState = "failed"
	AttemptBlocked         AttemptState = "blocked"
	AttemptCancelled       AttemptState = "cancelled"
	AttemptBudgetExhausted AttemptState = "budget_exhausted"
	AttemptAccepted        AttemptState = "accepted"
	AttemptInterrupted     AttemptState = "interrupted"
)

type Outcome string

const (
	OutcomeDone            Outcome = "done"
	OutcomeBlocked         Outcome = "blocked"
	OutcomeCancelled       Outcome = "cancelled"
	OutcomeBudgetExhausted Outcome = "budget_exhausted"
)

type FailureKind string

const (
	FailureTool              FailureKind = "tool_failed"
	FailurePermission        FailureKind = "permission_denied"
	FailureHook              FailureKind = "hook_blocked"
	FailureVerification      FailureKind = "verification_failed"
	FailureProvider          FailureKind = "provider_failed"
	FailureWorkspaceStale    FailureKind = "workspace_stale"
	FailureStateUnavailable  FailureKind = "workspace_state_unavailable"
	FailurePersistence       FailureKind = "persistence_failed"
	FailureInterruptedAction FailureKind = "interrupted_action"
)

type EvidenceKind string

const (
	EvidenceToolResult   EvidenceKind = "tool_result"
	EvidenceVerification EvidenceKind = "verification"
	EvidenceNodeResult   EvidenceKind = "node_result"
	EvidenceDelegateRead EvidenceKind = "delegate_read"
)

// Spec is one approved logical graph. Status and evidence are intentionally
// absent: the runtime derives those from attempts rather than trusting a plan
// replacement to describe machine state.
type Spec struct {
	Goal  string     `json:"goal"`
	Nodes []NodeSpec `json:"nodes"`
}

type NodeSpec struct {
	ID         int       `json:"id"`
	Title      string    `json:"title"`
	DependsOn  []int     `json:"depends_on,omitempty"`
	Acceptance []string  `json:"acceptance,omitempty"`
	Execution  Execution `json:"execution,omitempty"`
}

type Node struct {
	ID                int       `json:"id"`
	Position          int       `json:"position"`
	Title             string    `json:"title"`
	DependsOn         []int     `json:"depends_on,omitempty"`
	Acceptance        []string  `json:"acceptance,omitempty"`
	Execution         Execution `json:"execution"`
	State             NodeState `json:"state"`
	ActiveAttemptID   string    `json:"active_attempt_id,omitempty"`
	AcceptedAttemptID string    `json:"accepted_attempt_id,omitempty"`
	AttemptIDs        []string  `json:"attempt_ids,omitempty"`
	Reason            string    `json:"reason,omitempty"`
}

type PendingAction struct {
	ID                string    `json:"id"`
	Tool              string    `json:"tool"`
	Risk              string    `json:"risk"`
	Summary           string    `json:"summary,omitempty"`
	Command           string    `json:"command,omitempty"`
	PotentialMutation bool      `json:"potential_mutation,omitempty"`
	NonReplayable     bool      `json:"non_replayable,omitempty"`
	Started           time.Time `json:"started"`
}

type Failure struct {
	Kind      FailureKind `json:"kind"`
	Tool      string      `json:"tool,omitempty"`
	Risk      string      `json:"risk,omitempty"`
	Detail    string      `json:"detail"`
	Retryable bool        `json:"retryable,omitempty"`
	Resolved  bool        `json:"resolved,omitempty"`
	Time      time.Time   `json:"time"`
}

type Attempt struct {
	ID                      string         `json:"id"`
	NodeID                  int            `json:"node_id"`
	Number                  int            `json:"number"`
	State                   AttemptState   `json:"state"`
	GraphGeneration         uint64         `json:"graph_generation"`
	BaseWorkspaceToken      string         `json:"base_workspace_token,omitempty"`
	MutationGeneration      uint64         `json:"mutation_generation"`
	MayHaveMutated          bool           `json:"may_have_mutated,omitempty"`
	HasWorkspaceWrite       bool           `json:"has_workspace_write,omitempty"`
	MayHaveExternalEffects  bool           `json:"may_have_external_effects,omitempty"`
	CompletionInterventions int            `json:"completion_interventions,omitempty"`
	ToolSuccesses           int            `json:"tool_successes,omitempty"`
	PendingAction           *PendingAction `json:"pending_action,omitempty"`
	Failures                []Failure      `json:"failures,omitempty"`
	EvidenceIDs             []string       `json:"evidence_ids,omitempty"`
	Summary                 string         `json:"summary,omitempty"`
	WorkerID                string         `json:"worker_id,omitempty"`
	InputTokens             int            `json:"input_tokens,omitempty"`
	OutputTokens            int            `json:"output_tokens,omitempty"`
	CostUSD                 float64        `json:"cost_usd,omitempty"`
	CostAvailable           bool           `json:"cost_available,omitempty"`
	TokenBudget             int            `json:"token_budget,omitempty"`
	TimeoutSeconds          int            `json:"timeout_seconds,omitempty"`
	Started                 time.Time      `json:"started"`
	Finished                time.Time      `json:"finished,omitempty"`
}

type Evidence struct {
	ID                 string       `json:"id"`
	AttemptID          string       `json:"attempt_id"`
	NodeID             int          `json:"node_id"`
	Kind               EvidenceKind `json:"kind"`
	Tool               string       `json:"tool,omitempty"`
	Command            string       `json:"command,omitempty"`
	Status             string       `json:"status"`
	Summary            string       `json:"summary,omitempty"`
	WorkspaceToken     string       `json:"workspace_token,omitempty"`
	MutationGeneration uint64       `json:"mutation_generation"`
	Started            time.Time    `json:"started,omitempty"`
	Finished           time.Time    `json:"finished"`
}

type Revision struct {
	Generation uint64    `json:"generation"`
	Reason     string    `json:"reason"`
	Spec       Spec      `json:"spec"`
	Time       time.Time `json:"time"`
}

// Snapshot is the complete bounded durable graph record. The session appends
// a new snapshot on every meaningful transition; loading uses only the latest
// one and never reconstructs runtime truth from model messages.
type Snapshot struct {
	Schema             int        `json:"schema"`
	ID                 string     `json:"id"`
	LogicalRevision    uint64     `json:"logical_revision"`
	Generation         uint64     `json:"generation"`
	Goal               string     `json:"goal"`
	Nodes              []Node     `json:"nodes"`
	Attempts           []Attempt  `json:"attempts,omitempty"`
	Evidence           []Evidence `json:"evidence,omitempty"`
	Revisions          []Revision `json:"revisions,omitempty"`
	MutationGeneration uint64     `json:"mutation_generation"`
	WorkspaceToken     string     `json:"workspace_token,omitempty"`
	RevisionCount      int        `json:"revision_count,omitempty"`
	MaxAttemptsPerNode int        `json:"max_attempts_per_node"`
	MaxRevisions       int        `json:"max_revisions"`
	ReadFanout         ReadFanout `json:"read_fanout"`
	Outcome            Outcome    `json:"outcome,omitempty"`
	Reason             string     `json:"reason,omitempty"`
	Created            time.Time  `json:"created"`
	Updated            time.Time  `json:"updated"`
}

// ReadFanout is the durable aggregate envelope for automatic read workers.
// Bounds are fixed for the experimental slice and persisted so resume cannot
// silently acquire a larger budget from a newer process configuration.
type ReadFanout struct {
	MaxConcurrent  int       `json:"max_concurrent"`
	MaxStarts      int       `json:"max_starts"`
	Starts         int       `json:"starts"`
	MaxTokens      int       `json:"max_tokens"`
	UsedTokens     int       `json:"used_tokens"`
	MaxWallSeconds int       `json:"max_wall_seconds"`
	Started        time.Time `json:"started,omitempty"`
}

// Update is a bounded public lifecycle fact. It contains enough state for
// activity/headless surfaces without making the event stream the persistence
// format for the complete internal graph.
type Update struct {
	Time       time.Time
	GraphID    string
	Generation uint64
	NodeID     int
	AttemptID  string
	State      string
	Reason     string
	Ready      []int
	Outcome    Outcome
}

// PersistFunc writes one complete graph snapshot. durable asks the store to
// flush it to stable storage before returning; mutating actions never begin
// until their write-ahead snapshot has crossed that boundary.
type PersistFunc func(context.Context, Snapshot, bool) error

type Options struct {
	MaxAttemptsPerNode int
	MaxRevisions       int
	MaxReadConcurrency int
	MaxReadStarts      int
	MaxReadTokens      int
	MaxReadWallSeconds int
	Persist            PersistFunc
	Now                func() time.Time
	NewID              func(string) string
}

// Limits is the fixed experimental controller envelope shown before approval.
// These are runtime bounds, not permission grants or user configuration.
type Limits struct {
	MaxNodes                   int
	MaxAttemptsPerNode         int
	MaxRevisions               int
	MaxAcceptanceItemsPerNode  int
	MaxCompletionInterventions int
	MaxReadConcurrency         int
	MaxReadStarts              int
	MaxReadTokens              int
	MaxReadWallSeconds         int
}

func DefaultLimits() Limits {
	return Limits{
		MaxNodes:                   maxGraphNodes,
		MaxAttemptsPerNode:         defaultMaxAttempts,
		MaxRevisions:               defaultMaxRevisions,
		MaxAcceptanceItemsPerNode:  maxAcceptanceItems,
		MaxCompletionInterventions: maxCompletionInterventions,
		MaxReadConcurrency:         defaultMaxReadConcurrency,
		MaxReadStarts:              defaultMaxReadStarts,
		MaxReadTokens:              defaultMaxReadTokens,
		MaxReadWallSeconds:         defaultMaxReadWallSeconds,
	}
}

type Graph struct {
	mu      sync.Mutex
	state   Snapshot
	persist PersistFunc
	now     func() time.Time
	newID   func(string) string
	updates []Update
}

var (
	ErrNoReadyNode       = errors.New("goal graph has no dependency-ready node")
	ErrReadFanoutReady   = errors.New("goal graph has dependency-ready read-only nodes")
	ErrGraphTerminal     = errors.New("goal graph is terminal")
	ErrWorkspaceState    = errors.New("goal graph requires a Git-backed workspace state token before potentially mutating work")
	ErrStaleRevision     = errors.New("goal graph revision was based on stale state")
	ErrRevisionExhausted = errors.New("goal graph revision budget exhausted")
)

func New(spec Spec, logicalRevision uint64, opts Options) (*Graph, error) {
	if err := ValidateSpec(spec); err != nil {
		return nil, err
	}
	opts = normalizedOptions(opts)
	now := opts.Now().UTC()
	g := &Graph{persist: opts.Persist, now: opts.Now, newID: opts.NewID}
	g.state = Snapshot{
		Schema: SchemaVersion, ID: opts.NewID("graph"), LogicalRevision: logicalRevision,
		Generation: 1, Goal: strings.TrimSpace(spec.Goal), MaxAttemptsPerNode: opts.MaxAttemptsPerNode,
		MaxRevisions: opts.MaxRevisions, Created: now, Updated: now,
		ReadFanout: ReadFanout{MaxConcurrent: opts.MaxReadConcurrency, MaxStarts: opts.MaxReadStarts, MaxTokens: opts.MaxReadTokens, MaxWallSeconds: opts.MaxReadWallSeconds},
		Revisions:  []Revision{{Generation: 1, Reason: "initial approved logical graph", Spec: cloneSpec(spec), Time: now}},
	}
	for position, node := range spec.Nodes {
		g.state.Nodes = append(g.state.Nodes, Node{ID: node.ID, Position: position, Title: strings.TrimSpace(node.Title), DependsOn: append([]int(nil), node.DependsOn...), Acceptance: append([]string(nil), node.Acceptance...), Execution: normalizeExecution(node.Execution), State: NodeProposed})
	}
	g.queueUpdateLocked(0, "", "created", "approved logical graph recorded")
	g.refreshReadyLocked("dependencies accepted")
	return g, nil
}

func Restore(snapshot Snapshot, opts Options) (*Graph, error) {
	normalizeSnapshot(&snapshot)
	if err := ValidateSnapshot(snapshot); err != nil {
		return nil, err
	}
	opts = normalizedOptions(opts)
	g := &Graph{state: cloneSnapshot(snapshot), persist: opts.Persist, now: opts.Now, newID: opts.NewID}
	// Stored bounds are authoritative. Options supply defaults only for new
	// graphs; changing process configuration must not make resume mean something
	// different from the run that created the graph.
	return g, nil
}

func normalizeSnapshot(snapshot *Snapshot) {
	if snapshot == nil {
		return
	}
	if snapshot.ReadFanout.MaxConcurrent == 0 {
		snapshot.ReadFanout.MaxConcurrent = defaultMaxReadConcurrency
	}
	if snapshot.ReadFanout.MaxStarts == 0 {
		snapshot.ReadFanout.MaxStarts = defaultMaxReadStarts
	}
	if snapshot.ReadFanout.MaxTokens == 0 {
		snapshot.ReadFanout.MaxTokens = defaultMaxReadTokens
	}
	if snapshot.ReadFanout.MaxWallSeconds == 0 {
		snapshot.ReadFanout.MaxWallSeconds = defaultMaxReadWallSeconds
	}
	for i := range snapshot.Nodes {
		snapshot.Nodes[i].Execution = normalizeExecution(snapshot.Nodes[i].Execution)
	}
}

func normalizeExecution(execution Execution) Execution {
	if execution == "" {
		return ExecutionPrimary
	}
	return execution
}

func normalizedOptions(opts Options) Options {
	if opts.MaxAttemptsPerNode <= 0 {
		opts.MaxAttemptsPerNode = defaultMaxAttempts
	}
	if opts.MaxRevisions <= 0 {
		opts.MaxRevisions = defaultMaxRevisions
	}
	if opts.MaxReadConcurrency <= 0 || opts.MaxReadConcurrency > defaultMaxReadConcurrency {
		opts.MaxReadConcurrency = defaultMaxReadConcurrency
	}
	if opts.MaxReadStarts <= 0 {
		opts.MaxReadStarts = defaultMaxReadStarts
	}
	if opts.MaxReadTokens <= 0 {
		opts.MaxReadTokens = defaultMaxReadTokens
	}
	if opts.MaxReadWallSeconds <= 0 {
		opts.MaxReadWallSeconds = defaultMaxReadWallSeconds
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewID == nil {
		opts.NewID = randomID
	}
	return opts
}

func randomID(prefix string) string {
	data := make([]byte, 8)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(data)
}

func ValidateSpec(spec Spec) error {
	if strings.TrimSpace(spec.Goal) == "" {
		return errors.New("goal graph goal must not be empty")
	}
	if len(spec.Goal) > maxGoalBytes {
		return fmt.Errorf("goal graph goal exceeds %d bytes", maxGoalBytes)
	}
	if len(spec.Nodes) == 0 {
		return errors.New("goal graph must include at least one node")
	}
	if len(spec.Nodes) > maxGraphNodes {
		return fmt.Errorf("goal graph may include at most %d nodes", maxGraphNodes)
	}
	seen := make(map[int]bool, len(spec.Nodes))
	for i, node := range spec.Nodes {
		if node.ID == 0 {
			return fmt.Errorf("nodes[%d] needs a non-zero id", i)
		}
		if strings.TrimSpace(node.Title) == "" {
			return fmt.Errorf("nodes[%d] needs a non-empty title", i)
		}
		if len(node.Title) > maxTitleBytes {
			return fmt.Errorf("nodes[%d] title exceeds %d bytes", i, maxTitleBytes)
		}
		if len(node.Acceptance) > maxAcceptanceItems {
			return fmt.Errorf("nodes[%d] may include at most %d acceptance criteria", i, maxAcceptanceItems)
		}
		for criterionIndex, criterion := range node.Acceptance {
			if strings.TrimSpace(criterion) == "" || len(criterion) > maxAcceptanceBytes {
				return fmt.Errorf("nodes[%d] acceptance[%d] must be non-empty and at most %d bytes", i, criterionIndex, maxAcceptanceBytes)
			}
		}
		if node.Execution != "" && node.Execution != ExecutionPrimary && node.Execution != ExecutionReadOnly {
			return fmt.Errorf("nodes[%d] execution must be primary or read_only", i)
		}
		if seen[node.ID] {
			return fmt.Errorf("duplicate node id %d", node.ID)
		}
		seen[node.ID] = true
	}
	for i, node := range spec.Nodes {
		dependencies := map[int]bool{}
		for _, dependency := range node.DependsOn {
			if !seen[dependency] {
				return fmt.Errorf("nodes[%d] depends on unknown node %d", i, dependency)
			}
			if dependency == node.ID {
				return fmt.Errorf("nodes[%d] cannot depend on itself", i)
			}
			if dependencies[dependency] {
				return fmt.Errorf("nodes[%d] repeats dependency %d", i, dependency)
			}
			dependencies[dependency] = true
		}
	}
	if cycle := dependencyCycle(spec.Nodes); cycle != 0 {
		return fmt.Errorf("goal graph dependencies contain a cycle through node %d", cycle)
	}
	return nil
}

func ValidateSnapshot(snapshot Snapshot) error {
	if snapshot.Schema != SchemaVersion {
		return fmt.Errorf("unsupported goal graph schema %d (this build supports %d)", snapshot.Schema, SchemaVersion)
	}
	if strings.TrimSpace(snapshot.ID) == "" || snapshot.Generation == 0 {
		return errors.New("goal graph snapshot needs an id and non-zero generation")
	}
	spec := Spec{Goal: snapshot.Goal}
	for _, node := range snapshot.Nodes {
		spec.Nodes = append(spec.Nodes, NodeSpec{ID: node.ID, Title: node.Title, DependsOn: node.DependsOn, Acceptance: node.Acceptance, Execution: node.Execution})
	}
	if err := ValidateSpec(spec); err != nil {
		return fmt.Errorf("invalid goal graph snapshot: %w", err)
	}
	if snapshot.MaxAttemptsPerNode <= 0 || snapshot.MaxRevisions <= 0 {
		return errors.New("goal graph snapshot has invalid attempt or revision bounds")
	}
	if snapshot.ReadFanout.MaxConcurrent <= 0 || snapshot.ReadFanout.MaxConcurrent > defaultMaxReadConcurrency || snapshot.ReadFanout.MaxStarts <= 0 || snapshot.ReadFanout.Starts < 0 || snapshot.ReadFanout.Starts > snapshot.ReadFanout.MaxStarts || snapshot.ReadFanout.MaxTokens <= 0 || snapshot.ReadFanout.UsedTokens < 0 || snapshot.ReadFanout.MaxWallSeconds <= 0 {
		return errors.New("goal graph snapshot has invalid read-fan-out bounds or usage")
	}
	nodes := make(map[int]Node, len(snapshot.Nodes))
	positions := make(map[int]bool, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if !validNodeState(node.State) {
			return fmt.Errorf("node %d has unknown state %q", node.ID, node.State)
		}
		if node.Position < 0 || node.Position >= len(snapshot.Nodes) || positions[node.Position] {
			return fmt.Errorf("node %d has invalid or duplicate position %d", node.ID, node.Position)
		}
		positions[node.Position] = true
		if node.State == NodeRunning && node.ActiveAttemptID == "" {
			return fmt.Errorf("running node %d has no active attempt", node.ID)
		}
		if node.State != NodeRunning && node.ActiveAttemptID != "" {
			return fmt.Errorf("non-running node %d retains active attempt %q", node.ID, node.ActiveAttemptID)
		}
		if node.State == NodeDone && node.AcceptedAttemptID == "" {
			return fmt.Errorf("done node %d has no accepted attempt", node.ID)
		}
		if node.State != NodeDone && node.AcceptedAttemptID != "" {
			return fmt.Errorf("non-done node %d retains accepted attempt %q", node.ID, node.AcceptedAttemptID)
		}
		nodes[node.ID] = node
	}
	attempts := make(map[string]Attempt, len(snapshot.Attempts))
	for _, attempt := range snapshot.Attempts {
		if attempt.ID == "" || nodes[attempt.NodeID].ID == 0 {
			return errors.New("goal graph snapshot has an attempt with an unknown node or empty id")
		}
		if _, duplicate := attempts[attempt.ID]; duplicate {
			return fmt.Errorf("goal graph snapshot repeats attempt %q", attempt.ID)
		}
		if !validAttemptState(attempt.State) || attempt.Number <= 0 || attempt.Number > snapshot.MaxAttemptsPerNode || attempt.GraphGeneration == 0 || attempt.GraphGeneration > snapshot.Generation || attempt.InputTokens < 0 || attempt.OutputTokens < 0 || attempt.CostUSD < 0 || attempt.TokenBudget < 0 || attempt.TimeoutSeconds < 0 {
			return fmt.Errorf("goal graph snapshot has invalid attempt %q state, number, or generation", attempt.ID)
		}
		if attempt.PendingAction != nil && attempt.State != AttemptRunning && attempt.State != AttemptInterrupted {
			return fmt.Errorf("attempt %q retains a pending action in state %q", attempt.ID, attempt.State)
		}
		attempts[attempt.ID] = attempt
	}
	evidence := make(map[string]Evidence, len(snapshot.Evidence))
	for _, item := range snapshot.Evidence {
		attempt, ok := attempts[item.AttemptID]
		if item.ID == "" || !ok || item.NodeID != attempt.NodeID {
			return errors.New("goal graph snapshot has evidence with an unknown attempt, mismatched node, or empty id")
		}
		if _, duplicate := evidence[item.ID]; duplicate {
			return fmt.Errorf("goal graph snapshot repeats evidence %q", item.ID)
		}
		if !validEvidence(item) || item.MutationGeneration > snapshot.MutationGeneration {
			return fmt.Errorf("goal graph snapshot has invalid evidence %q", item.ID)
		}
		evidence[item.ID] = item
	}
	runningPrimary, runningReads := 0, 0
	for _, node := range snapshot.Nodes {
		if len(node.AttemptIDs) > snapshot.MaxAttemptsPerNode {
			return fmt.Errorf("node %d exceeds its attempt bound", node.ID)
		}
		seenAttempts := map[string]bool{}
		for _, attemptID := range node.AttemptIDs {
			attempt, ok := attempts[attemptID]
			if !ok || attempt.NodeID != node.ID || seenAttempts[attemptID] {
				return fmt.Errorf("node %d has an unknown, mismatched, or duplicate attempt %q", node.ID, attemptID)
			}
			seenAttempts[attemptID] = true
		}
		if node.ActiveAttemptID != "" {
			if attempt, ok := attempts[node.ActiveAttemptID]; !ok || attempt.NodeID != node.ID {
				return fmt.Errorf("node %d references unknown active attempt %q", node.ID, node.ActiveAttemptID)
			}
			if attempts[node.ActiveAttemptID].State != AttemptRunning {
				return fmt.Errorf("node %d active attempt %q is not running", node.ID, node.ActiveAttemptID)
			}
			if node.Execution == ExecutionReadOnly {
				runningReads++
			} else {
				runningPrimary++
			}
		}
		if node.AcceptedAttemptID != "" {
			if attempt, ok := attempts[node.AcceptedAttemptID]; !ok || attempt.NodeID != node.ID || attempt.State != AttemptAccepted {
				return fmt.Errorf("node %d references invalid accepted attempt %q", node.ID, node.AcceptedAttemptID)
			}
		}
	}
	if runningPrimary > 1 || runningReads > snapshot.ReadFanout.MaxConcurrent || (runningPrimary > 0 && runningReads > 0) {
		return errors.New("goal graph snapshot exceeds its primary/read running-attempt bounds")
	}
	for _, attempt := range snapshot.Attempts {
		for _, evidenceID := range attempt.EvidenceIDs {
			item, ok := evidence[evidenceID]
			if !ok || item.AttemptID != attempt.ID {
				return fmt.Errorf("attempt %q references unknown or mismatched evidence %q", attempt.ID, evidenceID)
			}
		}
	}
	if !validOutcome(snapshot.Outcome) {
		return fmt.Errorf("goal graph snapshot has unknown outcome %q", snapshot.Outcome)
	}
	if snapshot.Outcome != "" && runningPrimary+runningReads > 0 {
		return errors.New("terminal goal graph snapshot retains a running attempt")
	}
	if snapshot.Outcome == OutcomeDone {
		for _, node := range snapshot.Nodes {
			if node.State != NodeDone {
				return fmt.Errorf("done goal graph retains node %d in state %q", node.ID, node.State)
			}
		}
	}
	if len(snapshot.Revisions) == 0 || snapshot.RevisionCount != len(snapshot.Revisions)-1 || snapshot.RevisionCount > snapshot.MaxRevisions {
		return errors.New("goal graph snapshot has inconsistent revision history or bounds")
	}
	for _, revision := range snapshot.Revisions {
		if revision.Generation == 0 || revision.Generation > snapshot.Generation {
			return errors.New("goal graph snapshot has a revision with an invalid generation")
		}
		if err := ValidateSpec(revision.Spec); err != nil {
			return fmt.Errorf("goal graph snapshot has invalid revision %d: %w", revision.Generation, err)
		}
	}
	latest := snapshot.Revisions[len(snapshot.Revisions)-1]
	if latest.Generation != snapshot.Generation || strings.TrimSpace(latest.Spec.Goal) != snapshot.Goal || len(latest.Spec.Nodes) != len(snapshot.Nodes) {
		return errors.New("goal graph snapshot does not match its latest logical revision")
	}
	for i, node := range snapshot.Nodes {
		if node.Position != i || !sameDefinition(node, latest.Spec.Nodes[i]) {
			return fmt.Errorf("node %d does not match the latest logical revision", node.ID)
		}
	}
	return nil
}

func validNodeState(state NodeState) bool {
	switch state {
	case NodeProposed, NodeReady, NodeRunning, NodeRetryable, NodeStale, NodeBlocked, NodeCancelled, NodeBudgetExhausted, NodeDone:
		return true
	default:
		return false
	}
}

func validAttemptState(state AttemptState) bool {
	switch state {
	case AttemptRunning, AttemptRetryable, AttemptFailed, AttemptBlocked, AttemptCancelled, AttemptBudgetExhausted, AttemptAccepted, AttemptInterrupted:
		return true
	default:
		return false
	}
}

func validEvidence(item Evidence) bool {
	switch item.Kind {
	case EvidenceToolResult, EvidenceVerification:
		return item.Status == "passed" || item.Status == "failed"
	case EvidenceNodeResult, EvidenceDelegateRead:
		return item.Status == "accepted"
	default:
		return false
	}
}

func validOutcome(outcome Outcome) bool {
	switch outcome {
	case "", OutcomeDone, OutcomeBlocked, OutcomeCancelled, OutcomeBudgetExhausted:
		return true
	default:
		return false
	}
}

func dependencyCycle(nodes []NodeSpec) int {
	edges := make(map[int][]int, len(nodes))
	for _, node := range nodes {
		edges[node.ID] = node.DependsOn
	}
	visiting, visited := map[int]bool{}, map[int]bool{}
	var visit func(int) int
	visit = func(id int) int {
		if visiting[id] {
			return id
		}
		if visited[id] {
			return 0
		}
		visiting[id] = true
		for _, dependency := range edges[id] {
			if cycle := visit(dependency); cycle != 0 {
				return cycle
			}
		}
		visiting[id] = false
		visited[id] = true
		return 0
	}
	for _, node := range nodes {
		if cycle := visit(node.ID); cycle != 0 {
			return cycle
		}
	}
	return 0
}

func (g *Graph) SetPersister(persist PersistFunc) {
	g.mu.Lock()
	g.persist = persist
	g.mu.Unlock()
}

func (g *Graph) Persist(ctx context.Context, durable bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.persistLocked(ctx, durable)
}

func (g *Graph) Snapshot() Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return cloneSnapshot(g.state)
}

func (g *Graph) DrainUpdates() []Update {
	g.mu.Lock()
	defer g.mu.Unlock()
	updates := append([]Update(nil), g.updates...)
	for i := range updates {
		updates[i].Ready = append([]int(nil), updates[i].Ready...)
	}
	g.updates = nil
	return updates
}

func (g *Graph) Active() (Node, Attempt, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, node := range g.state.Nodes {
		if node.State != NodeRunning || node.ActiveAttemptID == "" {
			continue
		}
		attempt := g.attemptLocked(node.ActiveAttemptID)
		if attempt != nil {
			return cloneNode(node), cloneAttempt(*attempt), true
		}
	}
	return Node{}, Attempt{}, false
}

// ReadClaim is one durable dependency-ready read assignment. The caller runs
// it through a read-only worker and must return exactly one ReadResult.
type ReadClaim struct {
	Node           Node
	Attempt        Attempt
	TokenBudget    int
	TimeoutSeconds int
}

// ReadResult is the bounded worker-inbox fact accepted by the graph. Status is
// one of done, error, cancelled, timed_out, or budget_exhausted. It carries
// evidence text, not a child transcript or scheduling authority.
type ReadResult struct {
	AttemptID      string
	WorkerID       string
	Status         string
	Summary        string
	Error          string
	Evidence       []string
	ToolSuccesses  int
	WorkspaceToken string
	InputTokens    int
	OutputTokens   int
	CostUSD        float64
	CostAvailable  bool
}

// StartReadyReads durably claims at most the fixed automatic fan-out bound in
// stable plan order. Primary and read attempts never overlap in this slice.
func (g *Graph) StartReadyReads(ctx context.Context, workspaceToken string, limit int) ([]ReadClaim, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return nil, ErrGraphTerminal
	}
	if g.activeAttemptLocked() != nil {
		return nil, errors.New("goal graph already has a running attempt")
	}
	if g.state.WorkspaceToken != "" && workspaceToken != "" && g.state.WorkspaceToken != workspaceToken {
		g.invalidateAllDoneLocked("combined workspace changed outside the recorded graph state")
	}
	if g.state.WorkspaceToken == "" && workspaceToken != "" {
		g.state.WorkspaceToken = workspaceToken
	}
	g.refreshReadyLocked("dependencies accepted")
	var ready []*Node
	for i := range g.state.Nodes {
		node := &g.state.Nodes[i]
		if node.State == NodeReady && node.Execution == ExecutionReadOnly {
			ready = append(ready, node)
		}
	}
	if len(ready) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > g.state.ReadFanout.MaxConcurrent {
		limit = g.state.ReadFanout.MaxConcurrent
	}
	remainingStarts := g.state.ReadFanout.MaxStarts - g.state.ReadFanout.Starts
	remainingTokens := g.state.ReadFanout.MaxTokens - g.state.ReadFanout.UsedTokens
	now := g.now().UTC()
	if g.state.ReadFanout.Started.IsZero() {
		g.state.ReadFanout.Started = now
	}
	remainingWall := g.state.ReadFanout.MaxWallSeconds - int(now.Sub(g.state.ReadFanout.Started).Seconds())
	if remainingStarts <= 0 || remainingTokens <= 0 || remainingWall <= 0 {
		reason := fmt.Sprintf("automatic read fan-out budget exhausted: starts %d/%d, tokens %d/%d, wall %ds/%ds", g.state.ReadFanout.Starts, g.state.ReadFanout.MaxStarts, g.state.ReadFanout.UsedTokens, g.state.ReadFanout.MaxTokens, max(0, g.state.ReadFanout.MaxWallSeconds-remainingWall), g.state.ReadFanout.MaxWallSeconds)
		g.exhaustReadyReadLocked(ready[0], reason)
		if err := g.persistLocked(ctx, true); err != nil {
			return nil, err
		}
		return nil, ErrGraphTerminal
	}
	count := min(len(ready), limit, remainingStarts)
	perTaskTokens := max(1, remainingTokens/count)
	timeout := min(defaultReadTaskWallSeconds, remainingWall)
	claims := make([]ReadClaim, 0, count)
	for index, node := range ready[:count] {
		number := len(node.AttemptIDs) + 1
		if number > g.state.MaxAttemptsPerNode {
			g.blockNodeLocked(node, "read-only node attempt budget exhausted")
			continue
		}
		attempt := Attempt{
			ID: g.newID("attempt"), NodeID: node.ID, Number: number, State: AttemptRunning,
			GraphGeneration: g.state.Generation, BaseWorkspaceToken: workspaceToken,
			MutationGeneration: g.state.MutationGeneration, TokenBudget: perTaskTokens,
			TimeoutSeconds: timeout, Started: now,
		}
		g.state.Attempts = append(g.state.Attempts, attempt)
		node.State, node.ActiveAttemptID = NodeRunning, attempt.ID
		node.AttemptIDs = append(node.AttemptIDs, attempt.ID)
		g.state.ReadFanout.Starts++
		reason := fmt.Sprintf("automatically delegated: approved read_only node is dependency-ready (slot %d/%d)", index+1, g.state.ReadFanout.MaxConcurrent)
		node.Reason = reason
		g.queueUpdateLocked(node.ID, attempt.ID, "delegated_read", reason)
		claims = append(claims, ReadClaim{Node: cloneNode(*node), Attempt: cloneAttempt(attempt), TokenBudget: perTaskTokens, TimeoutSeconds: timeout})
	}
	if len(claims) == 0 {
		g.reduceOutcomeLocked()
	}
	if err := g.persistLocked(ctx, true); err != nil {
		return nil, err
	}
	return claims, nil
}

// FinishRead records a bounded child result against its immutable attempt.
// Success requires grounded tool evidence and a fresh workspace token when one
// is available; child prose alone cannot accept a logical node.
func (g *Graph) FinishRead(ctx context.Context, result ReadResult) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return ErrGraphTerminal
	}
	attempt := g.attemptLocked(result.AttemptID)
	if attempt == nil || attempt.State != AttemptRunning {
		return fmt.Errorf("goal graph read attempt %q is not running", result.AttemptID)
	}
	node := g.nodeLocked(attempt.NodeID)
	if node == nil || node.Execution != ExecutionReadOnly {
		return fmt.Errorf("goal graph attempt %q is not a read_only node", result.AttemptID)
	}
	attempt.WorkerID = bounded(result.WorkerID, 256)
	attempt.InputTokens, attempt.OutputTokens = max(0, result.InputTokens), max(0, result.OutputTokens)
	attempt.CostUSD, attempt.CostAvailable = max(0, result.CostUSD), result.CostAvailable
	g.state.ReadFanout.UsedTokens += attempt.InputTokens + attempt.OutputTokens
	now := g.now().UTC()
	// A claim made without a workspace token is necessarily best-effort (for
	// example, outside a Git worktree). Once a base token exists, however, the
	// worker must return the same token; a missing result token cannot silently
	// weaken the freshness gate.
	fresh := attempt.BaseWorkspaceToken == "" || (result.WorkspaceToken != "" && attempt.BaseWorkspaceToken == result.WorkspaceToken)
	summary := boundedSummary(result.Summary)
	grounded := result.ToolSuccesses > 0
	if result.Status == "done" && summary != "" && grounded && fresh {
		attempt.State, attempt.Finished, attempt.Summary = AttemptAccepted, now, summary
		attempt.ToolSuccesses = result.ToolSuccesses
		evidenceSummary := boundedSummary(strings.Join(result.Evidence, "\n"))
		g.addEvidenceLocked(attempt, Evidence{Kind: EvidenceDelegateRead, Tool: "automatic_read_delegate", Status: "accepted", Summary: evidenceSummary, WorkspaceToken: result.WorkspaceToken, MutationGeneration: g.state.MutationGeneration, Finished: now})
		node.State, node.ActiveAttemptID, node.AcceptedAttemptID, node.Reason = NodeDone, "", attempt.ID, ""
		g.refreshReadyLocked("delegated read evidence accepted")
		g.reduceOutcomeLocked()
		g.queueUpdateLocked(node.ID, attempt.ID, string(NodeDone), "bounded delegated read evidence accepted")
		return g.persistLocked(ctx, true)
	}

	detail := strings.TrimSpace(result.Error)
	if detail == "" {
		switch {
		case !fresh:
			detail = "workspace changed while the automatic read worker was running"
		case !grounded:
			detail = "automatic read worker returned no grounded tool evidence"
		case summary == "":
			detail = "automatic read worker returned no bounded result summary"
		default:
			detail = "automatic read worker ended with status " + result.Status
		}
	}
	failureKind := FailureProvider
	if !fresh {
		failureKind = FailureWorkspaceStale
	} else if !grounded {
		failureKind = FailureTool
	}
	attempt.Failures = append(attempt.Failures, Failure{Kind: failureKind, Tool: "automatic_read_delegate", Detail: boundedReason(detail), Retryable: result.Status == "error" || result.Status == "timed_out" || !fresh || !grounded, Time: now})
	attempt.Summary, attempt.Finished = boundedSummary(detail), now
	node.ActiveAttemptID = ""
	switch result.Status {
	case "budget_exhausted":
		attempt.State, node.State, node.Reason = AttemptBudgetExhausted, NodeBudgetExhausted, detail
	case "cancelled":
		attempt.State, node.State, node.Reason = AttemptCancelled, NodeBlocked, detail
	default:
		if len(node.AttemptIDs) < g.state.MaxAttemptsPerNode {
			attempt.State, node.State, node.Reason = AttemptRetryable, NodeRetryable, detail
			g.refreshReadyLocked("automatic read failure has remaining attempt budget")
		} else {
			attempt.State, node.State, node.Reason = AttemptBlocked, NodeBlocked, detail
		}
	}
	g.queueUpdateLocked(node.ID, attempt.ID, string(node.State), node.Reason)
	g.reduceOutcomeLocked()
	return g.persistLocked(ctx, true)
}

func (g *Graph) exhaustReadyReadLocked(node *Node, reason string) {
	node.State, node.Reason = NodeBudgetExhausted, strings.TrimSpace(reason)
	g.state.Outcome, g.state.Reason = OutcomeBudgetExhausted, node.Reason
	g.queueUpdateLocked(node.ID, "", string(NodeBudgetExhausted), node.Reason)
	g.queueUpdateLocked(0, "", string(OutcomeBudgetExhausted), g.state.Reason)
}

func (g *Graph) StartNext(ctx context.Context, workspaceToken string) (Node, Attempt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return Node{}, Attempt{}, ErrGraphTerminal
	}
	if g.activeAttemptLocked() != nil {
		return Node{}, Attempt{}, errors.New("goal graph already has a running attempt")
	}
	if g.state.WorkspaceToken != "" && workspaceToken != "" && g.state.WorkspaceToken != workspaceToken {
		g.invalidateAllDoneLocked("combined workspace changed outside the recorded graph state")
	}
	if g.state.WorkspaceToken == "" && workspaceToken != "" {
		g.state.WorkspaceToken = workspaceToken
	}
	g.refreshReadyLocked("dependencies accepted")
	for i := range g.state.Nodes {
		if g.state.Nodes[i].State == NodeReady && g.state.Nodes[i].Execution == ExecutionReadOnly {
			return Node{}, Attempt{}, ErrReadFanoutReady
		}
	}
	var selected *Node
	for i := range g.state.Nodes {
		if g.state.Nodes[i].State == NodeReady && g.state.Nodes[i].Execution == ExecutionPrimary {
			selected = &g.state.Nodes[i]
			break
		}
	}
	if selected == nil {
		g.reduceOutcomeLocked()
		if g.state.Outcome != "" {
			if err := g.persistLocked(ctx, true); err != nil {
				return Node{}, Attempt{}, err
			}
			return Node{}, Attempt{}, ErrGraphTerminal
		}
		return Node{}, Attempt{}, ErrNoReadyNode
	}
	number := len(selected.AttemptIDs) + 1
	if number > g.state.MaxAttemptsPerNode {
		g.blockNodeLocked(selected, "node attempt budget exhausted")
		if err := g.persistLocked(ctx, true); err != nil {
			return Node{}, Attempt{}, err
		}
		return Node{}, Attempt{}, ErrGraphTerminal
	}
	now := g.now().UTC()
	attempt := Attempt{
		ID: g.newID("attempt"), NodeID: selected.ID, Number: number, State: AttemptRunning,
		GraphGeneration: g.state.Generation, BaseWorkspaceToken: workspaceToken,
		MutationGeneration: g.state.MutationGeneration, Started: now,
	}
	g.state.Attempts = append(g.state.Attempts, attempt)
	selected.State = NodeRunning
	selected.ActiveAttemptID = attempt.ID
	selected.AttemptIDs = append(selected.AttemptIDs, attempt.ID)
	selected.Reason = ""
	g.queueUpdateLocked(selected.ID, attempt.ID, string(NodeRunning), fmt.Sprintf("attempt %d started", number))
	if err := g.persistLocked(ctx, true); err != nil {
		return Node{}, Attempt{}, err
	}
	return cloneNode(*selected), cloneAttempt(attempt), nil
}

type ToolAction struct {
	Tool              string
	Risk              string
	Summary           string
	Command           string
	PotentialMutation bool
	NonReplayable     bool
}

// BeginTool records a write-ahead action. A non-replayable action is flushed
// before the caller may execute it, making an interrupted action explicit on
// resume even if the process dies before its result can be recorded.
func (g *Graph) BeginTool(ctx context.Context, attemptID string, action ToolAction, workspaceToken string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	attempt := g.attemptLocked(attemptID)
	if attempt == nil || attempt.State != AttemptRunning {
		return fmt.Errorf("goal graph attempt %q is not running", attemptID)
	}
	if attempt.PendingAction != nil {
		return fmt.Errorf("goal graph attempt %q already has a pending action", attemptID)
	}
	if action.PotentialMutation && strings.TrimSpace(workspaceToken) == "" {
		failure := Failure{Kind: FailureStateUnavailable, Tool: action.Tool, Risk: action.Risk, Detail: ErrWorkspaceState.Error(), Time: g.now().UTC()}
		attempt.Failures = append(attempt.Failures, failure)
		g.queueUpdateLocked(attempt.NodeID, attempt.ID, "blocked_action", failure.Detail)
		if err := g.persistLocked(ctx, true); err != nil {
			return err
		}
		return ErrWorkspaceState
	}
	now := g.now().UTC()
	attempt.PendingAction = &PendingAction{
		ID: g.newID("action"), Tool: action.Tool, Risk: action.Risk,
		Summary: strings.TrimSpace(action.Summary), Command: action.Command,
		PotentialMutation: action.PotentialMutation, NonReplayable: action.NonReplayable, Started: now,
	}
	if action.PotentialMutation {
		g.state.MutationGeneration++
		attempt.MutationGeneration = g.state.MutationGeneration
		attempt.MayHaveMutated = true
	}
	if action.Risk == "write" {
		attempt.HasWorkspaceWrite = true
	}
	if action.NonReplayable {
		attempt.MayHaveExternalEffects = true
	}
	g.queueUpdateLocked(attempt.NodeID, attempt.ID, "action_started", action.Tool+": "+strings.TrimSpace(action.Summary))
	return g.persistLocked(ctx, action.NonReplayable)
}

type ToolResult struct {
	Tool           string
	Risk           string
	Summary        string
	Command        string
	Failed         bool
	FailureKind    FailureKind
	FailureDetail  string
	Retryable      bool
	Verification   bool
	WorkspaceToken string
	Started        time.Time
	Finished       time.Time
}

func (g *Graph) FinishTool(ctx context.Context, attemptID string, result ToolResult) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	attempt := g.attemptLocked(attemptID)
	if attempt == nil || attempt.State != AttemptRunning {
		return fmt.Errorf("goal graph attempt %q is not running", attemptID)
	}
	pending := attempt.PendingAction
	attempt.PendingAction = nil
	if result.Finished.IsZero() {
		result.Finished = g.now().UTC()
	}
	if result.Failed {
		kind := result.FailureKind
		if kind == "" {
			kind = FailureTool
		}
		detail := strings.TrimSpace(result.FailureDetail)
		if detail == "" {
			detail = strings.TrimSpace(result.Summary)
		}
		attempt.Failures = append(attempt.Failures, Failure{Kind: kind, Tool: result.Tool, Risk: result.Risk, Detail: detail, Retryable: result.Retryable, Time: result.Finished})
		if result.Verification {
			g.addEvidenceLocked(attempt, Evidence{Kind: EvidenceVerification, Tool: result.Tool, Command: result.Command, Status: "failed", Summary: detail, WorkspaceToken: result.WorkspaceToken, MutationGeneration: g.state.MutationGeneration, Started: result.Started, Finished: result.Finished})
		}
		g.queueUpdateLocked(attempt.NodeID, attempt.ID, "action_failed", detail)
	} else {
		attempt.ToolSuccesses++
		g.resolveFailuresLocked(attempt, result.Tool, result.Risk)
		kind := EvidenceToolResult
		if result.Verification {
			kind = EvidenceVerification
		}
		g.addEvidenceLocked(attempt, Evidence{Kind: kind, Tool: result.Tool, Command: result.Command, Status: "passed", Summary: strings.TrimSpace(result.Summary), WorkspaceToken: result.WorkspaceToken, MutationGeneration: g.state.MutationGeneration, Started: result.Started, Finished: result.Finished})
		g.queueUpdateLocked(attempt.NodeID, attempt.ID, "action_completed", result.Tool+": "+strings.TrimSpace(result.Summary))
	}
	if result.WorkspaceToken != "" && (pending == nil || pending.PotentialMutation || result.Verification) {
		g.state.WorkspaceToken = result.WorkspaceToken
	}
	durable := pending != nil && pending.NonReplayable
	return g.persistLocked(ctx, durable)
}

// RecordFailure attaches a failure that happened before tool execution, such
// as permission denial, schema rejection, or a hook refusal.
func (g *Graph) RecordFailure(ctx context.Context, attemptID string, failure Failure) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	attempt := g.attemptLocked(attemptID)
	if attempt == nil || attempt.State != AttemptRunning {
		return fmt.Errorf("goal graph attempt %q is not running", attemptID)
	}
	if failure.Time.IsZero() {
		failure.Time = g.now().UTC()
	}
	failure.Detail = strings.TrimSpace(failure.Detail)
	attempt.Failures = append(attempt.Failures, failure)
	g.queueUpdateLocked(attempt.NodeID, attempt.ID, "action_failed", failure.Detail)
	return g.persistLocked(ctx, true)
}

type DecisionKind string

const (
	DecisionContinue DecisionKind = "continue"
	DecisionAccepted DecisionKind = "accepted"
	DecisionRetry    DecisionKind = "retry"
	DecisionDone     DecisionKind = "done"
	DecisionBlocked  DecisionKind = "blocked"
)

type Decision struct {
	Kind    DecisionKind
	NodeID  int
	Notice  string
	Reason  string
	Outcome Outcome
}

// ProposeCompletion interprets a tool-free model response as a proposal. The
// response supplies a bounded result summary; only recorded runtime evidence
// can satisfy the structural and verification gates.
func (g *Graph) ProposeCompletion(ctx context.Context, summary, workspaceToken string) (Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return Decision{Kind: decisionForOutcome(g.state.Outcome), Reason: g.state.Reason, Outcome: g.state.Outcome}, nil
	}
	attempt := g.activeAttemptLocked()
	if attempt == nil {
		return Decision{}, errors.New("goal graph has no running attempt")
	}
	node := g.nodeLocked(attempt.NodeID)
	if node == nil {
		return Decision{}, errors.New("goal graph running attempt references an unknown node")
	}
	if g.state.WorkspaceToken != "" && workspaceToken != "" && g.state.WorkspaceToken != workspaceToken {
		failure := Failure{Kind: FailureWorkspaceStale, Detail: "combined workspace changed after the last recorded graph action", Retryable: true, Time: g.now().UTC()}
		attempt.Failures = append(attempt.Failures, failure)
	}
	unresolved := unresolvedFailures(attempt.Failures)
	if len(unresolved) > 0 {
		if hasFailureKind(unresolved, FailurePermission, FailureHook, FailureStateUnavailable, FailureInterruptedAction) {
			reason := joinFailureDetails(unresolved)
			g.finishBlockedLocked(node, attempt, reason)
			if err := g.persistLocked(ctx, true); err != nil {
				return Decision{}, err
			}
			return Decision{Kind: DecisionBlocked, NodeID: node.ID, Reason: reason, Outcome: OutcomeBlocked}, nil
		}
		if allRetryable(unresolved) && len(node.AttemptIDs) < g.state.MaxAttemptsPerNode {
			now := g.now().UTC()
			attempt.State, attempt.Finished = AttemptRetryable, now
			attempt.Summary = boundedSummary(summary)
			node.State, node.ActiveAttemptID = NodeRetryable, ""
			node.Reason = joinFailureDetails(unresolved)
			g.refreshReadyLocked("retryable failure has remaining attempt budget")
			g.queueUpdateLocked(node.ID, attempt.ID, string(NodeRetryable), node.Reason)
			if err := g.persistLocked(ctx, true); err != nil {
				return Decision{}, err
			}
			return Decision{Kind: DecisionRetry, NodeID: node.ID, Notice: "The runtime recorded a recoverable failure and opened a fresh bounded attempt for this node. Change the failed assumption, retry safely, or revise the graph; repeating an unchanged failed operation is not progress."}, nil
		}
		reason := joinFailureDetails(unresolved)
		g.finishBlockedLocked(node, attempt, reason)
		if err := g.persistLocked(ctx, true); err != nil {
			return Decision{}, err
		}
		return Decision{Kind: DecisionBlocked, NodeID: node.ID, Reason: reason, Outcome: OutcomeBlocked}, nil
	}

	var issues []string
	if attempt.ToolSuccesses == 0 {
		issues = append(issues, "the attempt has no successful bounded tool result")
	}
	if attempt.MayHaveMutated {
		if workspaceToken == "" {
			issues = append(issues, "the combined workspace has no state token")
		} else if attempt.HasWorkspaceWrite && attempt.BaseWorkspaceToken == workspaceToken {
			issues = append(issues, "the successful write action produced no combined-workspace change")
		} else if !g.hasFreshVerificationLocked(attempt, workspaceToken) {
			issues = append(issues, "potentially mutating work has no successful recognized verification bound to the current combined workspace")
		}
	}
	if len(issues) > 0 {
		attempt.CompletionInterventions++
		if attempt.CompletionInterventions > maxCompletionInterventions {
			reason := "node completion remained unproven after two controller interventions: " + strings.Join(issues, "; ")
			g.finishBlockedLocked(node, attempt, reason)
			if err := g.persistLocked(ctx, true); err != nil {
				return Decision{}, err
			}
			return Decision{Kind: DecisionBlocked, NodeID: node.ID, Reason: reason, Outcome: OutcomeBlocked}, nil
		}
		g.queueUpdateLocked(node.ID, attempt.ID, "completion_deferred", strings.Join(issues, "; "))
		if err := g.persistLocked(ctx, false); err != nil {
			return Decision{}, err
		}
		return Decision{Kind: DecisionContinue, NodeID: node.ID, Notice: completionNotice(node, attempt, issues)}, nil
	}

	now := g.now().UTC()
	attempt.State, attempt.Finished, attempt.Summary = AttemptAccepted, now, boundedSummary(summary)
	resultEvidence := Evidence{Kind: EvidenceNodeResult, Status: "accepted", Summary: attempt.Summary, WorkspaceToken: workspaceToken, MutationGeneration: g.state.MutationGeneration, Finished: now}
	g.addEvidenceLocked(attempt, resultEvidence)
	node.State, node.ActiveAttemptID, node.AcceptedAttemptID, node.Reason = NodeDone, "", attempt.ID, ""
	if workspaceToken != "" {
		g.state.WorkspaceToken = workspaceToken
	}
	if attempt.MayHaveMutated {
		g.invalidateUnconsumedReadsLocked(node.ID)
	}
	g.refreshReadyLocked("dependencies accepted")
	g.reduceOutcomeLocked()
	g.queueUpdateLocked(node.ID, attempt.ID, string(NodeDone), "runtime acceptance gates passed")
	if err := g.persistLocked(ctx, true); err != nil {
		return Decision{}, err
	}
	if g.state.Outcome == OutcomeDone {
		return Decision{Kind: DecisionDone, NodeID: node.ID, Outcome: OutcomeDone}, nil
	}
	return Decision{Kind: DecisionAccepted, NodeID: node.ID, Notice: fmt.Sprintf("The runtime accepted node %d (%s). Continue with the next dependency-ready node selected by the runtime.", node.ID, node.Title)}, nil
}

func decisionForOutcome(outcome Outcome) DecisionKind {
	if outcome == OutcomeDone {
		return DecisionDone
	}
	return DecisionBlocked
}

func completionNotice(node *Node, attempt *Attempt, issues []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Collomia goal graph controller: node %d (%s), attempt %d, cannot be accepted yet.\nRecorded gaps:\n", node.ID, node.Title, attempt.Number)
	for _, issue := range issues {
		b.WriteString("- " + issue + "\n")
	}
	b.WriteString("Continue this node with ordinary tools. Obtain proportionate recognized verification after the last potentially mutating action, repair a failure, propose a bounded graph revision, or explicitly block the node with an exact reason. This notice changes no permission or user scope.")
	return b.String()
}

func (g *Graph) BlockActive(ctx context.Context, attemptID, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	attempt := g.attemptLocked(attemptID)
	if attempt == nil || attempt.State != AttemptRunning {
		return fmt.Errorf("goal graph attempt %q is not running", attemptID)
	}
	node := g.nodeLocked(attempt.NodeID)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("blocked node reason must not be empty")
	}
	if len(reason) > 4<<10 {
		return errors.New("blocked node reason exceeds 4096 bytes")
	}
	g.finishBlockedLocked(node, attempt, reason)
	return g.persistLocked(ctx, true)
}

func (g *Graph) Revise(ctx context.Context, baseGeneration uint64, reason string, spec Spec) error {
	if err := ValidateSpec(spec); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return ErrGraphTerminal
	}
	if baseGeneration != g.state.Generation {
		return fmt.Errorf("%w: proposal is based on generation %d, current generation is %d", ErrStaleRevision, baseGeneration, g.state.Generation)
	}
	if g.state.RevisionCount >= g.state.MaxRevisions {
		return ErrRevisionExhausted
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("goal graph revision reason must not be empty")
	}
	if len(reason) > 4<<10 {
		return errors.New("goal graph revision reason exceeds 4096 bytes")
	}
	now := g.now().UTC()
	for _, active := range g.runningAttemptsLocked() {
		active.State, active.Finished = AttemptFailed, now
		active.Summary = "superseded by graph revision: " + reason
		if node := g.nodeLocked(active.NodeID); node != nil {
			node.ActiveAttemptID = ""
		}
	}
	old := make(map[int]Node, len(g.state.Nodes))
	for _, node := range g.state.Nodes {
		old[node.ID] = cloneNode(node)
	}
	g.state.Generation++
	g.state.RevisionCount++
	g.state.Goal = strings.TrimSpace(spec.Goal)
	g.state.Nodes = nil
	changed := map[int]bool{}
	for position, proposed := range spec.Nodes {
		node := Node{ID: proposed.ID, Position: position, Title: strings.TrimSpace(proposed.Title), DependsOn: append([]int(nil), proposed.DependsOn...), Acceptance: append([]string(nil), proposed.Acceptance...), Execution: normalizeExecution(proposed.Execution), State: NodeProposed}
		if prior, ok := old[proposed.ID]; ok && sameDefinition(prior, proposed) && prior.State == NodeDone {
			node.State, node.AcceptedAttemptID = NodeDone, prior.AcceptedAttemptID
			node.AttemptIDs = append([]string(nil), prior.AttemptIDs...)
		} else {
			changed[proposed.ID] = true
			if prior, ok := old[proposed.ID]; ok {
				node.AttemptIDs = append([]string(nil), prior.AttemptIDs...)
			}
		}
		g.state.Nodes = append(g.state.Nodes, node)
	}
	// A preserved node depending directly or transitively on a changed node is
	// stale even when its own title did not change.
	for {
		grew := false
		for i := range g.state.Nodes {
			node := &g.state.Nodes[i]
			if changed[node.ID] {
				continue
			}
			for _, dependency := range node.DependsOn {
				if changed[dependency] {
					changed[node.ID], grew = true, true
					node.State, node.AcceptedAttemptID = NodeStale, ""
					node.Reason = "dependency changed in graph revision"
					break
				}
			}
		}
		if !grew {
			break
		}
	}
	g.state.Revisions = append(g.state.Revisions, Revision{Generation: g.state.Generation, Reason: reason, Spec: cloneSpec(spec), Time: now})
	g.refreshReadyLocked("revised dependencies accepted")
	g.queueUpdateLocked(0, "", "revised", reason)
	return g.persistLocked(ctx, true)
}

// Recover converts a stored running attempt into a safe restart or an
// explicit blocker. It never executes anything. A pending non-replayable
// action is ambiguous even when no mutation ultimately occurred, so safety
// takes the false-positive block rather than risking a duplicate effect.
func (g *Graph) Recover(ctx context.Context, workspaceToken string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return nil
	}
	changed := false
	for i := range g.state.Attempts {
		attempt := &g.state.Attempts[i]
		if attempt.State != AttemptRunning {
			continue
		}
		changed = true
		node := g.nodeLocked(attempt.NodeID)
		now := g.now().UTC()
		attempt.State, attempt.Finished = AttemptInterrupted, now
		if attempt.PendingAction != nil && attempt.PendingAction.NonReplayable {
			reason := fmt.Sprintf("action %s may have taken effect before the session stopped; inspect and reconcile it before continuing", attempt.PendingAction.Tool)
			attempt.Failures = append(attempt.Failures, Failure{Kind: FailureInterruptedAction, Tool: attempt.PendingAction.Tool, Risk: attempt.PendingAction.Risk, Detail: reason, Time: now})
			node.State, node.ActiveAttemptID, node.Reason = NodeBlocked, "", reason
			g.queueUpdateLocked(node.ID, attempt.ID, string(NodeBlocked), reason)
			continue
		}
		node.ActiveAttemptID = ""
		if len(node.AttemptIDs) < g.state.MaxAttemptsPerNode {
			node.State, node.Reason = NodeRetryable, "interrupted replay-safe attempt may be recomputed in a new attempt"
			g.queueUpdateLocked(node.ID, attempt.ID, string(NodeRetryable), node.Reason)
		} else {
			node.State, node.Reason = NodeBlocked, "interrupted attempt exhausted the node attempt budget"
			g.queueUpdateLocked(node.ID, attempt.ID, string(NodeBlocked), node.Reason)
		}
	}
	if g.state.WorkspaceToken != "" && workspaceToken != "" && g.state.WorkspaceToken != workspaceToken {
		g.invalidateAllDoneLocked("combined workspace changed while the goal graph was not running")
		changed = true
	}
	if !changed {
		return nil
	}
	g.refreshReadyLocked("recovery made a safe attempt ready")
	g.reduceOutcomeLocked()
	return g.persistLocked(ctx, true)
}

func (g *Graph) Cancel(ctx context.Context, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return nil
	}
	now := g.now().UTC()
	for _, attempt := range g.runningAttemptsLocked() {
		attempt.State, attempt.Finished = AttemptCancelled, now
		if node := g.nodeLocked(attempt.NodeID); node != nil {
			node.State, node.ActiveAttemptID, node.Reason = NodeCancelled, "", strings.TrimSpace(reason)
			g.queueUpdateLocked(node.ID, attempt.ID, string(NodeCancelled), node.Reason)
		}
	}
	g.state.Outcome, g.state.Reason = OutcomeCancelled, strings.TrimSpace(reason)
	g.queueUpdateLocked(0, "", string(OutcomeCancelled), g.state.Reason)
	return g.persistLocked(ctx, true)
}

func (g *Graph) ExhaustBudget(ctx context.Context, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return nil
	}
	now := g.now().UTC()
	for _, attempt := range g.runningAttemptsLocked() {
		attempt.State, attempt.Finished = AttemptBudgetExhausted, now
		if node := g.nodeLocked(attempt.NodeID); node != nil {
			node.State, node.ActiveAttemptID, node.Reason = NodeBudgetExhausted, "", strings.TrimSpace(reason)
			g.queueUpdateLocked(node.ID, attempt.ID, string(NodeBudgetExhausted), node.Reason)
		}
	}
	g.state.Outcome, g.state.Reason = OutcomeBudgetExhausted, strings.TrimSpace(reason)
	g.queueUpdateLocked(0, "", string(OutcomeBudgetExhausted), g.state.Reason)
	return g.persistLocked(ctx, true)
}

func (g *Graph) Render() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "Runtime-owned goal graph %s · generation %d\nGoal: %s\n", g.state.ID, g.state.Generation, g.state.Goal)
	marks := map[NodeState]string{NodeProposed: "[ ]", NodeReady: "[>]", NodeRunning: "[~]", NodeRetryable: "[r]", NodeStale: "[s]", NodeBlocked: "[!]", NodeCancelled: "[-]", NodeBudgetExhausted: "[$]", NodeDone: "[x]"}
	for _, node := range g.state.Nodes {
		fmt.Fprintf(&b, "%s %d. %s · %s · %s", marks[node.State], node.ID, node.Title, node.State, node.Execution)
		if len(node.DependsOn) > 0 {
			fmt.Fprintf(&b, " · after %s", joinInts(node.DependsOn))
		}
		if node.ActiveAttemptID != "" {
			fmt.Fprintf(&b, " · attempt %s", node.ActiveAttemptID)
		}
		if node.Reason != "" {
			fmt.Fprintf(&b, " — %s", node.Reason)
		}
		b.WriteByte('\n')
		for _, criterion := range node.Acceptance {
			fmt.Fprintf(&b, "    acceptance: %s\n", criterion)
		}
		if node.Execution == ExecutionReadOnly && node.AcceptedAttemptID != "" {
			if attempt := g.attemptLocked(node.AcceptedAttemptID); attempt != nil && attempt.Summary != "" {
				fmt.Fprintf(&b, "    delegated result: %s\n", bounded(attempt.Summary, 2400))
			}
		}
	}
	if g.state.Outcome != "" {
		fmt.Fprintf(&b, "Graph outcome: %s", g.state.Outcome)
		if g.state.Reason != "" {
			fmt.Fprintf(&b, " — %s", g.state.Reason)
		}
		b.WriteByte('\n')
	}
	b.WriteString("The runtime owns node state and evidence. Graph state grants no tool permission. Work only on the running node; use propose_goal_graph_revision for a bounded replan or block_goal_node for an exact blocker. A tool-free response proposes completion but cannot mark a node done by itself.")
	return b.String()
}

// Inspect renders bounded operator-facing runtime truth. A zero nodeID shows
// the graph overview; a non-zero nodeID adds that node's immutable attempts,
// failures, and evidence. It deliberately does not infer state from model
// prose or expose raw unbounded tool output.
func (g *Graph) Inspect(nodeID int) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if nodeID != 0 && g.nodeLocked(nodeID) == nil {
		return "", fmt.Errorf("goal graph has no node %d", nodeID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Experimental Orchestrated Goal\nGraph: %s · generation %d\nGoal: %s\n", g.state.ID, g.state.Generation, g.state.Goal)
	fmt.Fprintf(&b, "Bounds: %d nodes · %d attempts/node · %d revisions\n", maxGraphNodes, g.state.MaxAttemptsPerNode, g.state.MaxRevisions)
	fmt.Fprintf(&b, "Read fan-out: %d/%d starts · %d/%d tokens · at most %d concurrent · %ds wall bound\n", g.state.ReadFanout.Starts, g.state.ReadFanout.MaxStarts, g.state.ReadFanout.UsedTokens, g.state.ReadFanout.MaxTokens, g.state.ReadFanout.MaxConcurrent, g.state.ReadFanout.MaxWallSeconds)
	b.WriteString("Execution: dependency-ready read_only nodes may use bounded automatic workers; one serial primary lane owns primary work and every parent-workspace write.\n")
	b.WriteString("Write scope: the primary workspace only; every concrete path is assessed by ordinary permissions when the action is proposed.\n")
	b.WriteString("Authority: every action still uses ordinary permissions; approval grants no publication or additional tool access.\n")
	b.WriteString("Completion: changed workspace state requires fresh machine-observed verification.\n\n")
	marks := map[NodeState]string{NodeProposed: "[ ]", NodeReady: "[>]", NodeRunning: "[~]", NodeRetryable: "[r]", NodeStale: "[s]", NodeBlocked: "[!]", NodeCancelled: "[-]", NodeBudgetExhausted: "[$]", NodeDone: "[x]"}
	for _, node := range g.state.Nodes {
		fmt.Fprintf(&b, "%s %d. %s · %s · %s", marks[node.State], node.ID, node.Title, node.State, node.Execution)
		if len(node.DependsOn) > 0 {
			fmt.Fprintf(&b, " · after %s", joinInts(node.DependsOn))
		}
		if node.Reason != "" {
			fmt.Fprintf(&b, " — %s", bounded(strings.TrimSpace(node.Reason), 600))
		}
		b.WriteByte('\n')
		for _, criterion := range node.Acceptance {
			fmt.Fprintf(&b, "    acceptance: %s\n", criterion)
		}
	}
	if g.state.Outcome != "" {
		fmt.Fprintf(&b, "\nOutcome: %s", g.state.Outcome)
		if g.state.Reason != "" {
			fmt.Fprintf(&b, " — %s", bounded(strings.TrimSpace(g.state.Reason), 600))
		}
		b.WriteByte('\n')
	}
	if nodeID == 0 {
		b.WriteString("\n/orchestrate status <node-id> shows that node's attempts and evidence.")
		return b.String(), nil
	}
	b.WriteString("\nAttempts and evidence\n")
	for _, attempt := range g.state.Attempts {
		if attempt.NodeID != nodeID {
			continue
		}
		fmt.Fprintf(&b, "- %s · attempt %d · %s", attempt.ID, attempt.Number, attempt.State)
		if attempt.WorkerID != "" {
			fmt.Fprintf(&b, " · worker %s", attempt.WorkerID)
		}
		if attempt.InputTokens+attempt.OutputTokens > 0 {
			fmt.Fprintf(&b, " · %d tokens", attempt.InputTokens+attempt.OutputTokens)
		}
		if attempt.Summary != "" {
			fmt.Fprintf(&b, " — %s", bounded(strings.TrimSpace(attempt.Summary), 600))
		}
		b.WriteByte('\n')
		for _, failure := range attempt.Failures {
			resolution := "unresolved"
			if failure.Resolved {
				resolution = "resolved"
			}
			fmt.Fprintf(&b, "    failure: %s · %s — %s\n", failure.Kind, resolution, bounded(strings.TrimSpace(failure.Detail), 600))
		}
		for _, evidenceID := range attempt.EvidenceIDs {
			for _, evidence := range g.state.Evidence {
				if evidence.ID != evidenceID {
					continue
				}
				fmt.Fprintf(&b, "    evidence: %s · %s · %s", evidence.Kind, evidence.Status, bounded(strings.TrimSpace(evidence.Summary), 600))
				if evidence.Command != "" {
					fmt.Fprintf(&b, " · command %s", bounded(strings.TrimSpace(evidence.Command), 300))
				}
				b.WriteByte('\n')
			}
		}
	}
	return b.String(), nil
}

func (g *Graph) Generation() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state.Generation
}

func (g *Graph) Outcome() (Outcome, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state.Outcome, g.state.Reason
}

func (g *Graph) persistLocked(ctx context.Context, durable bool) error {
	g.state.Updated = g.now().UTC()
	if g.persist == nil {
		return nil
	}
	if err := g.persist(ctx, cloneSnapshot(g.state), durable); err != nil {
		return fmt.Errorf("persist goal graph: %w", err)
	}
	return nil
}

func (g *Graph) queueUpdateLocked(nodeID int, attemptID, state, reason string) {
	g.updates = append(g.updates, Update{Time: g.now().UTC(), GraphID: g.state.ID, Generation: g.state.Generation, NodeID: nodeID, AttemptID: attemptID, State: state, Reason: boundedReason(reason), Ready: g.readyIDsLocked(), Outcome: g.state.Outcome})
}

func (g *Graph) readyIDsLocked() []int {
	var ready []int
	for _, node := range g.state.Nodes {
		if node.State == NodeReady {
			ready = append(ready, node.ID)
		}
	}
	return ready
}

func (g *Graph) refreshReadyLocked(reason string) {
	states := make(map[int]NodeState, len(g.state.Nodes))
	for _, node := range g.state.Nodes {
		states[node.ID] = node.State
	}
	for i := range g.state.Nodes {
		node := &g.state.Nodes[i]
		if node.State != NodeProposed && node.State != NodeRetryable && node.State != NodeStale {
			continue
		}
		ready := true
		for _, dependency := range node.DependsOn {
			if states[dependency] != NodeDone {
				ready = false
				break
			}
		}
		if ready && len(node.AttemptIDs) < g.state.MaxAttemptsPerNode {
			node.State, node.Reason = NodeReady, ""
			g.queueUpdateLocked(node.ID, "", string(NodeReady), reason)
		}
	}
}

func (g *Graph) reduceOutcomeLocked() {
	if g.state.Outcome != "" {
		return
	}
	allDone, running := true, false
	var blockers, exhausted []string
	for _, node := range g.state.Nodes {
		if node.State != NodeDone {
			allDone = false
		}
		if node.State == NodeRunning {
			running = true
		}
		if node.State == NodeBlocked {
			blockers = append(blockers, fmt.Sprintf("node %d (%s): %s", node.ID, node.Title, node.Reason))
		}
		if node.State == NodeBudgetExhausted {
			exhausted = append(exhausted, fmt.Sprintf("node %d (%s): %s", node.ID, node.Title, node.Reason))
		}
	}
	if allDone {
		g.state.Outcome = OutcomeDone
		g.state.Reason = "all required nodes passed runtime acceptance gates"
		g.queueUpdateLocked(0, "", string(OutcomeDone), g.state.Reason)
		return
	}
	if len(blockers) > 0 && !running {
		// Every node is required in OG-1. Continuing independent work after one
		// is materially blocked cannot make the approved goal complete and only
		// spends authority/budget after the truthful terminal state is known.
		g.state.Outcome, g.state.Reason = OutcomeBlocked, strings.Join(blockers, "; ")
		g.queueUpdateLocked(0, "", string(OutcomeBlocked), g.state.Reason)
		return
	}
	if len(exhausted) > 0 && !running {
		g.state.Outcome, g.state.Reason = OutcomeBudgetExhausted, strings.Join(exhausted, "; ")
		g.queueUpdateLocked(0, "", string(OutcomeBudgetExhausted), g.state.Reason)
	}
}

func (g *Graph) blockNodeLocked(node *Node, reason string) {
	node.State, node.ActiveAttemptID, node.Reason = NodeBlocked, "", strings.TrimSpace(reason)
	g.queueUpdateLocked(node.ID, "", string(NodeBlocked), node.Reason)
	g.reduceOutcomeLocked()
}

func (g *Graph) finishBlockedLocked(node *Node, attempt *Attempt, reason string) {
	now := g.now().UTC()
	attempt.State, attempt.Finished = AttemptBlocked, now
	attempt.Summary = boundedSummary(reason)
	node.State, node.ActiveAttemptID, node.Reason = NodeBlocked, "", strings.TrimSpace(reason)
	g.queueUpdateLocked(node.ID, attempt.ID, string(NodeBlocked), node.Reason)
	g.reduceOutcomeLocked()
}

func (g *Graph) hasFreshVerificationLocked(attempt *Attempt, token string) bool {
	for _, id := range attempt.EvidenceIDs {
		evidence := g.evidenceLocked(id)
		if evidence != nil && evidence.Kind == EvidenceVerification && evidence.Status == "passed" && evidence.MutationGeneration == g.state.MutationGeneration && evidence.WorkspaceToken == token {
			return true
		}
	}
	return false
}

func (g *Graph) addEvidenceLocked(attempt *Attempt, evidence Evidence) {
	evidence.ID = g.newID("evidence")
	evidence.AttemptID, evidence.NodeID = attempt.ID, attempt.NodeID
	if evidence.Finished.IsZero() {
		evidence.Finished = g.now().UTC()
	}
	evidence.Summary = boundedSummary(evidence.Summary)
	g.state.Evidence = append(g.state.Evidence, evidence)
	attempt.EvidenceIDs = append(attempt.EvidenceIDs, evidence.ID)
}

func (g *Graph) resolveFailuresLocked(attempt *Attempt, tool, risk string) {
	for i := range attempt.Failures {
		failure := &attempt.Failures[i]
		if failure.Resolved {
			continue
		}
		if failure.Tool == tool || (failure.Retryable && failure.Risk != "" && failure.Risk == risk) {
			failure.Resolved = true
		}
	}
}

func unresolvedFailures(failures []Failure) []Failure {
	var unresolved []Failure
	for _, failure := range failures {
		if !failure.Resolved {
			unresolved = append(unresolved, failure)
		}
	}
	return unresolved
}

func allRetryable(failures []Failure) bool {
	if len(failures) == 0 {
		return false
	}
	for _, failure := range failures {
		if !failure.Retryable {
			return false
		}
	}
	return true
}

func hasFailureKind(failures []Failure, kinds ...FailureKind) bool {
	for _, failure := range failures {
		for _, kind := range kinds {
			if failure.Kind == kind {
				return true
			}
		}
	}
	return false
}

func joinFailureDetails(failures []Failure) string {
	details := make([]string, 0, len(failures))
	for _, failure := range failures {
		detail := strings.TrimSpace(failure.Detail)
		if detail == "" {
			detail = string(failure.Kind)
		}
		details = append(details, detail)
	}
	return strings.Join(details, "; ")
}

func (g *Graph) invalidateAllDoneLocked(reason string) {
	for i := range g.state.Nodes {
		node := &g.state.Nodes[i]
		if node.State != NodeDone {
			continue
		}
		node.State, node.AcceptedAttemptID, node.Reason = NodeStale, "", reason
		g.queueUpdateLocked(node.ID, "", string(NodeStale), reason)
	}
	g.state.Outcome, g.state.Reason = "", ""
}

// invalidateUnconsumedReadsLocked stales completed read-only nodes that were
// not ancestors of the mutating node. Ancestors were already consumed by the
// accepted attempt; unrelated old research must not silently feed a later
// consumer after the workspace generation changes.
func (g *Graph) invalidateUnconsumedReadsLocked(mutatingNodeID int) {
	ancestors := map[int]bool{}
	var addAncestors func(int)
	addAncestors = func(id int) {
		node := g.nodeLocked(id)
		if node == nil {
			return
		}
		for _, dependency := range node.DependsOn {
			if ancestors[dependency] {
				continue
			}
			ancestors[dependency] = true
			addAncestors(dependency)
		}
	}
	addAncestors(mutatingNodeID)
	for i := range g.state.Nodes {
		node := &g.state.Nodes[i]
		if node.ID == mutatingNodeID || ancestors[node.ID] || node.State != NodeDone {
			continue
		}
		accepted := g.attemptLocked(node.AcceptedAttemptID)
		if accepted == nil || accepted.MayHaveMutated {
			continue
		}
		node.State, node.AcceptedAttemptID = NodeStale, ""
		node.Reason = "unconsumed read result predates a controlled workspace mutation"
		g.queueUpdateLocked(node.ID, "", string(NodeStale), node.Reason)
		g.invalidateDependentsLocked(node.ID, node.Reason)
	}
}

func (g *Graph) invalidateDependentsLocked(id int, reason string) {
	changed := true
	invalid := map[int]bool{id: true}
	for changed {
		changed = false
		for i := range g.state.Nodes {
			node := &g.state.Nodes[i]
			if invalid[node.ID] {
				continue
			}
			for _, dependency := range node.DependsOn {
				if !invalid[dependency] {
					continue
				}
				invalid[node.ID], changed = true, true
				if node.State == NodeDone || node.State == NodeReady || node.State == NodeProposed {
					node.State, node.AcceptedAttemptID = NodeStale, ""
					node.Reason = reason
					g.queueUpdateLocked(node.ID, "", string(NodeStale), reason)
				}
				break
			}
		}
	}
}

func (g *Graph) activeAttemptLocked() *Attempt {
	for i := range g.state.Attempts {
		if g.state.Attempts[i].State == AttemptRunning {
			return &g.state.Attempts[i]
		}
	}
	return nil
}

func (g *Graph) runningAttemptsLocked() []*Attempt {
	var attempts []*Attempt
	for i := range g.state.Attempts {
		if g.state.Attempts[i].State == AttemptRunning {
			attempts = append(attempts, &g.state.Attempts[i])
		}
	}
	return attempts
}

func (g *Graph) attemptLocked(id string) *Attempt {
	for i := range g.state.Attempts {
		if g.state.Attempts[i].ID == id {
			return &g.state.Attempts[i]
		}
	}
	return nil
}

func (g *Graph) evidenceLocked(id string) *Evidence {
	for i := range g.state.Evidence {
		if g.state.Evidence[i].ID == id {
			return &g.state.Evidence[i]
		}
	}
	return nil
}

func (g *Graph) nodeLocked(id int) *Node {
	for i := range g.state.Nodes {
		if g.state.Nodes[i].ID == id {
			return &g.state.Nodes[i]
		}
	}
	return nil
}

func sameDefinition(node Node, spec NodeSpec) bool {
	if node.ID != spec.ID || node.Title != strings.TrimSpace(spec.Title) || node.Execution != normalizeExecution(spec.Execution) || !equalInts(node.DependsOn, spec.DependsOn) || len(node.Acceptance) != len(spec.Acceptance) {
		return false
	}
	for i := range node.Acceptance {
		if node.Acceptance[i] != spec.Acceptance[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	clone := snapshot
	clone.Nodes = make([]Node, len(snapshot.Nodes))
	for i, node := range snapshot.Nodes {
		clone.Nodes[i] = cloneNode(node)
	}
	clone.Attempts = make([]Attempt, len(snapshot.Attempts))
	for i, attempt := range snapshot.Attempts {
		clone.Attempts[i] = cloneAttempt(attempt)
	}
	clone.Evidence = append([]Evidence(nil), snapshot.Evidence...)
	clone.Revisions = make([]Revision, len(snapshot.Revisions))
	for i, revision := range snapshot.Revisions {
		clone.Revisions[i] = revision
		clone.Revisions[i].Spec = cloneSpec(revision.Spec)
	}
	return clone
}

func cloneNode(node Node) Node {
	node.DependsOn = append([]int(nil), node.DependsOn...)
	node.Acceptance = append([]string(nil), node.Acceptance...)
	node.AttemptIDs = append([]string(nil), node.AttemptIDs...)
	return node
}

func cloneAttempt(attempt Attempt) Attempt {
	attempt.Failures = append([]Failure(nil), attempt.Failures...)
	attempt.EvidenceIDs = append([]string(nil), attempt.EvidenceIDs...)
	if attempt.PendingAction != nil {
		pending := *attempt.PendingAction
		attempt.PendingAction = &pending
	}
	return attempt
}

func cloneSpec(spec Spec) Spec {
	clone := Spec{Goal: spec.Goal, Nodes: make([]NodeSpec, len(spec.Nodes))}
	for i, node := range spec.Nodes {
		clone.Nodes[i] = node
		clone.Nodes[i].DependsOn = append([]int(nil), node.DependsOn...)
		clone.Nodes[i].Acceptance = append([]string(nil), node.Acceptance...)
	}
	return clone
}

func boundedSummary(value string) string { return bounded(value, 16<<10) }
func boundedReason(value string) string  { return bounded(value, 4<<10) }

func bounded(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit] + "…"
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprint(value)
	}
	return strings.Join(parts, ",")
}

// StableNodeIDs returns current node ids in logical plan order. It is useful
// to evaluation code and deliberately does not expose mutable internal slices.
func (g *Graph) StableNodeIDs() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	nodes := append([]Node(nil), g.state.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Position < nodes[j].Position })
	ids := make([]int, len(nodes))
	for i, node := range nodes {
		ids[i] = node.ID
	}
	return ids
}
