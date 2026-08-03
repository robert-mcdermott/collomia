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
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/robert-mcdermott/collomia/internal/writescope"
)

const (
	SchemaVersion                = 1
	defaultMaxAttempts           = 2
	defaultMaxRevisions          = 2
	maxCompletionInterventions   = 2
	maxGraphNodes                = 12
	maxGoalBytes                 = 2048
	maxTitleBytes                = 512
	maxAcceptanceItems           = 8
	maxAcceptanceBytes           = 512
	defaultMaxReadConcurrency    = 2
	defaultMaxReadStarts         = 8
	defaultMaxReadTokens         = 64_000
	defaultMaxReadWallSeconds    = 15 * 60
	defaultReadTaskWallSeconds   = 5 * 60
	defaultReadTaskIterations    = 8
	defaultMaxWriterConcurrency  = 2
	defaultMaxWriterStarts       = 2
	defaultWriterTaskWallSeconds = 10 * 60
	defaultWriterTaskIterations  = 12
	maxWriterCandidateFiles      = 256
	maxWriterVerification        = 16
	defaultMaxGraphIterations    = 96
	defaultMaxGraphTokens        = 1_000_000
	defaultMaxGraphCostUSD       = 5.00
	defaultMaxActiveWallSeconds  = 30 * 60
)

type Execution string

const (
	ExecutionPrimary       Execution = "primary"
	ExecutionReadOnly      Execution = "read_only"
	ExecutionIsolatedWrite Execution = "isolated_write"
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
	AttemptCandidate       AttemptState = "candidate"
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
	EvidenceToolResult    EvidenceKind = "tool_result"
	EvidenceVerification  EvidenceKind = "verification"
	EvidenceNodeResult    EvidenceKind = "node_result"
	EvidenceDelegateRead  EvidenceKind = "delegate_read"
	EvidenceDelegateWrite EvidenceKind = "delegate_write_candidate"
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
	WritePaths []string  `json:"write_paths,omitempty"`
}

type Node struct {
	ID                int       `json:"id"`
	Position          int       `json:"position"`
	Title             string    `json:"title"`
	DependsOn         []int     `json:"depends_on,omitempty"`
	Acceptance        []string  `json:"acceptance,omitempty"`
	Execution         Execution `json:"execution"`
	WritePaths        []string  `json:"write_paths,omitempty"`
	State             NodeState `json:"state"`
	ActiveAttemptID   string    `json:"active_attempt_id,omitempty"`
	AcceptedAttemptID string    `json:"accepted_attempt_id,omitempty"`
	AttemptIDs        []string  `json:"attempt_ids,omitempty"`
	Reason            string    `json:"reason,omitempty"`
}

type PendingAction struct {
	ID                     string    `json:"id"`
	Tool                   string    `json:"tool"`
	Risk                   string    `json:"risk"`
	Summary                string    `json:"summary,omitempty"`
	Command                string    `json:"command,omitempty"`
	PotentialMutation      bool      `json:"potential_mutation,omitempty"`
	NonReplayable          bool      `json:"non_replayable,omitempty"`
	BaseWorkspaceToken     string    `json:"base_workspace_token,omitempty"`
	BaseMutationGeneration uint64    `json:"base_mutation_generation,omitempty"`
	Started                time.Time `json:"started"`
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

// CandidateVerification is one machine-observed command result bound to the
// retained writer worktree state. It proves child state only and grants no
// authority to publish into the parent workspace.
type CandidateVerification struct {
	Command    string `json:"command"`
	Status     string `json:"status"`
	StateToken string `json:"state_token"`
}

// WriterCandidate is the bounded durable handoff produced by an automatic
// isolated writer. The worktree is retained for review; the graph never
// selects or integrates it in OG-3A.
type WriterCandidate struct {
	WorkerID          string                  `json:"worker_id"`
	Worktree          string                  `json:"worktree"`
	Branch            string                  `json:"branch"`
	BaseCommit        string                  `json:"base_commit"`
	WritePaths        []string                `json:"write_paths"`
	ChangedFiles      []string                `json:"changed_files,omitempty"`
	ScopeViolations   []string                `json:"scope_violations,omitempty"`
	VerificationState string                  `json:"verification_state,omitempty"`
	VerificationToken string                  `json:"verification_token,omitempty"`
	Verification      []CandidateVerification `json:"verification,omitempty"`
}

type Attempt struct {
	ID                      string           `json:"id"`
	NodeID                  int              `json:"node_id"`
	Number                  int              `json:"number"`
	State                   AttemptState     `json:"state"`
	GraphGeneration         uint64           `json:"graph_generation"`
	BaseWorkspaceToken      string           `json:"base_workspace_token,omitempty"`
	BaseCommit              string           `json:"base_commit,omitempty"`
	MutationGeneration      uint64           `json:"mutation_generation"`
	MayHaveMutated          bool             `json:"may_have_mutated,omitempty"`
	HasWorkspaceWrite       bool             `json:"has_workspace_write,omitempty"`
	MayHaveExternalEffects  bool             `json:"may_have_external_effects,omitempty"`
	CompletionInterventions int              `json:"completion_interventions,omitempty"`
	CompletionGap           string           `json:"completion_gap,omitempty"`
	CompletionGapIteration  int              `json:"completion_gap_iteration,omitempty"`
	ToolSuccesses           int              `json:"tool_successes,omitempty"`
	PendingAction           *PendingAction   `json:"pending_action,omitempty"`
	Failures                []Failure        `json:"failures,omitempty"`
	EvidenceIDs             []string         `json:"evidence_ids,omitempty"`
	Summary                 string           `json:"summary,omitempty"`
	WorkerID                string           `json:"worker_id,omitempty"`
	UsageRecorded           bool             `json:"usage_recorded,omitempty"`
	Iterations              int              `json:"iterations,omitempty"`
	LastProgressIteration   int              `json:"last_progress_iteration,omitempty"`
	InputTokens             int              `json:"input_tokens,omitempty"`
	OutputTokens            int              `json:"output_tokens,omitempty"`
	CostUSD                 float64          `json:"cost_usd,omitempty"`
	CostAvailable           bool             `json:"cost_available,omitempty"`
	CostEstimated           bool             `json:"cost_estimated,omitempty"`
	TokenBudget             int              `json:"token_budget,omitempty"`
	CostBudgetUSD           float64          `json:"cost_budget_usd,omitempty"`
	IterationBudget         int              `json:"iteration_budget,omitempty"`
	TimeoutSeconds          int              `json:"timeout_seconds,omitempty"`
	Candidate               *WriterCandidate `json:"candidate,omitempty"`
	Started                 time.Time        `json:"started"`
	Finished                time.Time        `json:"finished,omitempty"`
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
	Schema             int             `json:"schema"`
	ID                 string          `json:"id"`
	LogicalRevision    uint64          `json:"logical_revision"`
	Generation         uint64          `json:"generation"`
	Goal               string          `json:"goal"`
	Nodes              []Node          `json:"nodes"`
	Attempts           []Attempt       `json:"attempts,omitempty"`
	Evidence           []Evidence      `json:"evidence,omitempty"`
	Revisions          []Revision      `json:"revisions,omitempty"`
	MutationGeneration uint64          `json:"mutation_generation"`
	WorkspaceToken     string          `json:"workspace_token,omitempty"`
	RevisionCount      int             `json:"revision_count,omitempty"`
	MaxAttemptsPerNode int             `json:"max_attempts_per_node"`
	MaxRevisions       int             `json:"max_revisions"`
	ReadFanout         ReadFanout      `json:"read_fanout"`
	WriterFanout       WriterFanout    `json:"writer_fanout"`
	Accounting         Accounting      `json:"accounting"`
	AggregateBudget    AggregateBudget `json:"aggregate_budget"`
	PauseRequested     bool            `json:"pause_requested,omitempty"`
	PauseReached       bool            `json:"pause_reached,omitempty"`
	PauseReason        string          `json:"pause_reason,omitempty"`
	Outcome            Outcome         `json:"outcome,omitempty"`
	Reason             string          `json:"reason,omitempty"`
	Created            time.Time       `json:"created"`
	Updated            time.Time       `json:"updated"`
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

// WriterFanout is the fixed OG-3A envelope for one retained candidate wave.
// Aggregate model-work bounds remain the outer limit.
type WriterFanout struct {
	MaxConcurrent int `json:"max_concurrent"`
	MaxStarts     int `json:"max_starts"`
	Starts        int `json:"starts"`
}

// WorkUsage is machine-observed model work for one runtime lane. Input token
// counts include cached input when the provider reports it that way, matching
// the public usage contract. CostAvailable means every token-bearing record in
// this lane had user-configured pricing; an unavailable cost is never rendered
// as a reassuring zero.
type WorkUsage struct {
	Iterations    int     `json:"iterations"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	CostUSD       float64 `json:"cost_usd,omitempty"`
	CostAvailable bool    `json:"cost_available,omitempty"`
	CostEstimated bool    `json:"cost_estimated,omitempty"`
}

// Accounting is the durable primary-plus-worker measurement record. Started
// is the beginning of the explicit proposal turn, so comparison does not hide
// the extra model call needed to create an Orchestrated Goal graph.
type Accounting struct {
	Started          time.Time     `json:"started"`
	Primary          WorkUsage     `json:"primary"`
	AutomaticReads   WorkUsage     `json:"automatic_reads"`
	AutomaticWriters WorkUsage     `json:"automatic_writers"`
	ActiveElapsed    time.Duration `json:"active_elapsed,omitempty"`
	ActiveSince      time.Time     `json:"active_since,omitempty"`
}

// AggregateBudget is the fixed runtime-owned envelope across proposal,
// primary, and automatic-worker work. Cost is enforced only while every
// token-bearing contribution has user-configured pricing; token, iteration,
// and active-wall limits always remain enforceable.
type AggregateBudget struct {
	MaxIterations        int     `json:"max_iterations"`
	MaxTokens            int     `json:"max_tokens"`
	MaxCostUSD           float64 `json:"max_cost_usd"`
	MaxActiveWallSeconds int     `json:"max_active_wall_seconds"`
}

// UsageSummary is the operator-facing aggregate derived from Accounting. The
// elapsed duration is live for an active graph and freezes at its final durable
// transition for a terminal graph.
type UsageSummary struct {
	Primary          WorkUsage
	AutomaticReads   WorkUsage
	AutomaticWriters WorkUsage
	Total            WorkUsage
	Elapsed          time.Duration
	ActiveElapsed    time.Duration
}

// BudgetStatus is a read-only view used by provider admission and status
// presentation. Remaining values are clamped to zero.
type BudgetStatus struct {
	Limits              AggregateBudget
	Usage               WorkUsage
	ActiveElapsed       time.Duration
	RemainingIterations int
	RemainingTokens     int
	RemainingCostUSD    float64
	RemainingActiveWall time.Duration
	CostEnforceable     bool
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
	MaxAttemptsPerNode     int
	MaxRevisions           int
	MaxReadConcurrency     int
	MaxReadStarts          int
	MaxReadTokens          int
	MaxReadWallSeconds     int
	MaxWriterConcurrency   int
	MaxWriterStarts        int
	MaxAggregateIterations int
	MaxAggregateTokens     int
	MaxAggregateCostUSD    float64
	MaxActiveWallSeconds   int
	AccountingStarted      time.Time
	InitialPrimary         WorkUsage
	Persist                PersistFunc
	Now                    func() time.Time
	NewID                  func(string) string
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
	MaxWriterConcurrency       int
	MaxWriterStarts            int
	MaxAggregateIterations     int
	MaxAggregateTokens         int
	MaxAggregateCostUSD        float64
	MaxActiveWallSeconds       int
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
		MaxWriterConcurrency:       defaultMaxWriterConcurrency,
		MaxWriterStarts:            defaultMaxWriterStarts,
		MaxAggregateIterations:     defaultMaxGraphIterations,
		MaxAggregateTokens:         defaultMaxGraphTokens,
		MaxAggregateCostUSD:        defaultMaxGraphCostUSD,
		MaxActiveWallSeconds:       defaultMaxActiveWallSeconds,
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
	ErrGraphPaused       = errors.New("goal graph scheduling is paused")
	ErrUnsafeNodeRetry   = errors.New("goal graph node cannot be retried safely")
	ErrAggregateBudget   = errors.New("goal graph aggregate budget exhausted")
)

func New(spec Spec, logicalRevision uint64, opts Options) (*Graph, error) {
	if err := ValidateSpec(spec); err != nil {
		return nil, err
	}
	opts = normalizedOptions(opts)
	if !validWorkUsage(opts.InitialPrimary) {
		return nil, errors.New("goal graph initial primary accounting is invalid")
	}
	now := opts.Now().UTC()
	accountingStarted := opts.AccountingStarted.UTC()
	if accountingStarted.IsZero() {
		accountingStarted = now
	}
	g := &Graph{persist: opts.Persist, now: opts.Now, newID: opts.NewID}
	g.state = Snapshot{
		Schema: SchemaVersion, ID: opts.NewID("graph"), LogicalRevision: logicalRevision,
		Generation: 1, Goal: strings.TrimSpace(spec.Goal), MaxAttemptsPerNode: opts.MaxAttemptsPerNode,
		MaxRevisions: opts.MaxRevisions, Created: now, Updated: now,
		ReadFanout:      ReadFanout{MaxConcurrent: opts.MaxReadConcurrency, MaxStarts: opts.MaxReadStarts, MaxTokens: opts.MaxReadTokens, MaxWallSeconds: opts.MaxReadWallSeconds},
		WriterFanout:    WriterFanout{MaxConcurrent: opts.MaxWriterConcurrency, MaxStarts: opts.MaxWriterStarts},
		Accounting:      Accounting{Started: accountingStarted, Primary: opts.InitialPrimary, ActiveSince: now},
		AggregateBudget: AggregateBudget{MaxIterations: opts.MaxAggregateIterations, MaxTokens: opts.MaxAggregateTokens, MaxCostUSD: opts.MaxAggregateCostUSD, MaxActiveWallSeconds: opts.MaxActiveWallSeconds},
		Revisions:       []Revision{{Generation: 1, Reason: "initial approved logical graph", Spec: cloneSpec(spec), Time: now}},
	}
	for position, node := range spec.Nodes {
		writePaths, _ := writescope.Normalize(node.WritePaths, node.Execution == ExecutionIsolatedWrite)
		g.state.Nodes = append(g.state.Nodes, Node{ID: node.ID, Position: position, Title: strings.TrimSpace(node.Title), DependsOn: append([]int(nil), node.DependsOn...), Acceptance: append([]string(nil), node.Acceptance...), Execution: normalizeExecution(node.Execution), WritePaths: writePaths, State: NodeProposed})
	}
	g.queueUpdateLocked(0, "", "created", "approved logical graph recorded")
	g.refreshReadyLocked("dependencies accepted")
	_ = g.enforceAggregateBudgetLocked(now, false)
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
	if snapshot.WriterFanout.MaxConcurrent == 0 {
		snapshot.WriterFanout.MaxConcurrent = defaultMaxWriterConcurrency
	}
	if snapshot.WriterFanout.MaxStarts == 0 {
		snapshot.WriterFanout.MaxStarts = defaultMaxWriterStarts
	}
	hadAggregateBudget := snapshot.AggregateBudget.MaxIterations != 0 || snapshot.AggregateBudget.MaxTokens != 0 || snapshot.AggregateBudget.MaxCostUSD != 0 || snapshot.AggregateBudget.MaxActiveWallSeconds != 0
	if snapshot.AggregateBudget.MaxIterations == 0 {
		snapshot.AggregateBudget.MaxIterations = defaultMaxGraphIterations
	}
	if snapshot.AggregateBudget.MaxTokens == 0 {
		snapshot.AggregateBudget.MaxTokens = defaultMaxGraphTokens
	}
	if snapshot.AggregateBudget.MaxCostUSD == 0 {
		snapshot.AggregateBudget.MaxCostUSD = defaultMaxGraphCostUSD
	}
	if snapshot.AggregateBudget.MaxActiveWallSeconds == 0 {
		snapshot.AggregateBudget.MaxActiveWallSeconds = defaultMaxActiveWallSeconds
	}
	if snapshot.Accounting.Started.IsZero() {
		// Pre-accounting schema-1 snapshots reconstruct what their immutable
		// attempts can prove. Primary proposal/iteration counts were not stored,
		// so they remain visibly zero instead of being guessed.
		snapshot.Accounting.Started = snapshot.Created
		nodes := make(map[int]Execution, len(snapshot.Nodes))
		for _, node := range snapshot.Nodes {
			nodes[node.ID] = normalizeExecution(node.Execution)
		}
		for index := range snapshot.Attempts {
			attempt := &snapshot.Attempts[index]
			usage := WorkUsage{Iterations: attempt.Iterations, InputTokens: attempt.InputTokens, OutputTokens: attempt.OutputTokens, CostUSD: attempt.CostUSD, CostAvailable: attempt.CostAvailable, CostEstimated: attempt.CostEstimated}
			switch nodes[attempt.NodeID] {
			case ExecutionReadOnly:
				addWorkUsage(&snapshot.Accounting.AutomaticReads, usage)
				attempt.UsageRecorded = usage != (WorkUsage{})
			case ExecutionIsolatedWrite:
				addWorkUsage(&snapshot.Accounting.AutomaticWriters, usage)
				attempt.UsageRecorded = usage != (WorkUsage{})
			default:
				addWorkUsage(&snapshot.Accounting.Primary, usage)
			}
		}
	}
	if !hadAggregateBudget && snapshot.Accounting.ActiveElapsed == 0 && snapshot.Accounting.ActiveSince.IsZero() {
		// OG-2B2b1 snapshots did not distinguish active execution time. Their
		// creation/update timestamps prove only the elapsed execution observed at
		// durable boundaries, which is the conservative value restored here.
		if elapsed := snapshot.Updated.Sub(snapshot.Created); elapsed > 0 {
			snapshot.Accounting.ActiveElapsed = elapsed
		}
	}
	if !snapshot.Accounting.ActiveSince.IsZero() {
		// Restore is inert until an explicit user resume. Count only time through
		// the last durable transition, never downtime after it.
		if elapsed := snapshot.Updated.Sub(snapshot.Accounting.ActiveSince); elapsed > 0 {
			snapshot.Accounting.ActiveElapsed += elapsed
		}
		snapshot.Accounting.ActiveSince = time.Time{}
	}
	for i := range snapshot.Nodes {
		snapshot.Nodes[i].Execution = normalizeExecution(snapshot.Nodes[i].Execution)
		if snapshot.Nodes[i].Execution == ExecutionIsolatedWrite {
			snapshot.Nodes[i].WritePaths, _ = writescope.Normalize(snapshot.Nodes[i].WritePaths, true)
		}
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
	if opts.MaxWriterConcurrency <= 0 || opts.MaxWriterConcurrency > defaultMaxWriterConcurrency {
		opts.MaxWriterConcurrency = defaultMaxWriterConcurrency
	}
	if opts.MaxWriterStarts <= 0 || opts.MaxWriterStarts > defaultMaxWriterStarts {
		opts.MaxWriterStarts = defaultMaxWriterStarts
	}
	if opts.MaxAggregateIterations <= 0 || opts.MaxAggregateIterations > defaultMaxGraphIterations {
		opts.MaxAggregateIterations = defaultMaxGraphIterations
	}
	if opts.MaxAggregateTokens <= 0 || opts.MaxAggregateTokens > defaultMaxGraphTokens {
		opts.MaxAggregateTokens = defaultMaxGraphTokens
	}
	if opts.MaxAggregateCostUSD <= 0 || opts.MaxAggregateCostUSD > defaultMaxGraphCostUSD || math.IsNaN(opts.MaxAggregateCostUSD) || math.IsInf(opts.MaxAggregateCostUSD, 0) {
		opts.MaxAggregateCostUSD = defaultMaxGraphCostUSD
	}
	if opts.MaxActiveWallSeconds <= 0 || opts.MaxActiveWallSeconds > defaultMaxActiveWallSeconds {
		opts.MaxActiveWallSeconds = defaultMaxActiveWallSeconds
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
		switch node.Execution {
		case "", ExecutionPrimary, ExecutionReadOnly:
			if _, err := writescope.Normalize(node.WritePaths, false); err != nil {
				return fmt.Errorf("nodes[%d]: %w", i, err)
			}
		case ExecutionIsolatedWrite:
			if len(node.WritePaths) == 0 {
				return fmt.Errorf("nodes[%d] isolated_write requires explicit write_paths", i)
			}
			normalized, err := writescope.Normalize(node.WritePaths, true)
			if err != nil {
				return fmt.Errorf("nodes[%d]: %w", i, err)
			}
			if len(normalized) == 1 && normalized[0] == writescope.Workspace {
				return fmt.Errorf("nodes[%d] isolated_write scope must be narrower than the whole workspace", i)
			}
		default:
			return fmt.Errorf("nodes[%d] execution must be primary, read_only, or isolated_write", i)
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

// ValidateExecutableSpec applies the schedulability contract of the current
// experimental controller. Isolated writers produce retained candidates and
// deliberately stop for review; OG-3A cannot select or integrate a candidate.
// Consequently they are valid only in an explicitly candidate-only graph and
// must be terminal leaves. End-to-end change graphs must use the primary lane.
// ValidateSpec remains the durable schema validator so older snapshots and
// lower-level controller fixtures retain their original compatibility.
func ValidateExecutableSpec(spec Spec) error {
	if err := ValidateSpec(spec); err != nil {
		return err
	}
	if !HasIsolatedWriters(spec) {
		return nil
	}
	isolated := make(map[int]bool)
	for _, node := range spec.Nodes {
		switch normalizeExecution(node.Execution) {
		case ExecutionPrimary:
			return fmt.Errorf("isolated_write is a retained-candidate preview and cannot be mixed with primary execution; use primary nodes for an end-to-end goal, or use a candidate-only graph for manual review")
		case ExecutionIsolatedWrite:
			isolated[node.ID] = true
		}
	}
	for _, node := range spec.Nodes {
		for _, dependency := range node.DependsOn {
			if isolated[dependency] {
				return fmt.Errorf("isolated_write node %d must be a terminal leaf: node %d depends on it, but retained candidates cannot unlock dependent work before reviewed integration", dependency, node.ID)
			}
		}
	}
	return nil
}

// HasIsolatedWriters reports whether a proposal needs the candidate-preview
// stable-base preflight before approval.
func HasIsolatedWriters(spec Spec) bool {
	for _, node := range spec.Nodes {
		if normalizeExecution(node.Execution) == ExecutionIsolatedWrite {
			return true
		}
	}
	return false
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
		spec.Nodes = append(spec.Nodes, NodeSpec{ID: node.ID, Title: node.Title, DependsOn: node.DependsOn, Acceptance: node.Acceptance, Execution: node.Execution, WritePaths: node.WritePaths})
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
	if snapshot.WriterFanout.MaxConcurrent <= 0 || snapshot.WriterFanout.MaxConcurrent > defaultMaxWriterConcurrency || snapshot.WriterFanout.MaxStarts <= 0 || snapshot.WriterFanout.MaxStarts > defaultMaxWriterStarts || snapshot.WriterFanout.Starts < 0 || snapshot.WriterFanout.Starts > snapshot.WriterFanout.MaxStarts {
		return errors.New("goal graph snapshot has invalid writer-fan-out bounds or usage")
	}
	if snapshot.Accounting.Started.IsZero() || !validWorkUsage(snapshot.Accounting.Primary) || !validWorkUsage(snapshot.Accounting.AutomaticReads) || !validWorkUsage(snapshot.Accounting.AutomaticWriters) {
		return errors.New("goal graph snapshot has invalid aggregate accounting")
	}
	budget := snapshot.AggregateBudget
	if budget.MaxIterations <= 0 || budget.MaxIterations > defaultMaxGraphIterations || budget.MaxTokens <= 0 || budget.MaxTokens > defaultMaxGraphTokens || budget.MaxCostUSD <= 0 || budget.MaxCostUSD > defaultMaxGraphCostUSD || math.IsNaN(budget.MaxCostUSD) || math.IsInf(budget.MaxCostUSD, 0) || budget.MaxActiveWallSeconds <= 0 || budget.MaxActiveWallSeconds > defaultMaxActiveWallSeconds || snapshot.Accounting.ActiveElapsed < 0 {
		return errors.New("goal graph snapshot has invalid aggregate budget or active time")
	}
	if snapshot.Outcome != "" && !snapshot.Accounting.ActiveSince.IsZero() {
		return errors.New("terminal goal graph snapshot retains an active wall clock")
	}
	if snapshot.PauseReached && !snapshot.Accounting.ActiveSince.IsZero() {
		return errors.New("paused goal graph snapshot retains an active wall clock")
	}
	if snapshot.PauseReached && !snapshot.PauseRequested {
		return errors.New("goal graph snapshot reached pause without a pause request")
	}
	if snapshot.PauseRequested && strings.TrimSpace(snapshot.PauseReason) == "" {
		return errors.New("goal graph snapshot has a pause request without a reason")
	}
	if !snapshot.PauseRequested && strings.TrimSpace(snapshot.PauseReason) != "" {
		return errors.New("goal graph snapshot has a pause reason without a pause request")
	}
	if snapshot.Outcome != "" && snapshot.PauseRequested {
		return errors.New("terminal goal graph snapshot retains a pause request")
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
		if !validAttemptState(attempt.State) || attempt.Number <= 0 || attempt.Number > snapshot.MaxAttemptsPerNode || attempt.GraphGeneration == 0 || attempt.GraphGeneration > snapshot.Generation || attempt.Iterations < 0 || attempt.LastProgressIteration < 0 || attempt.LastProgressIteration > attempt.Iterations || attempt.CompletionGapIteration < 0 || attempt.CompletionGapIteration > attempt.Iterations || len(attempt.CompletionGap) > 4<<10 || (attempt.CompletionGap == "" && attempt.CompletionGapIteration != 0) || attempt.InputTokens < 0 || attempt.OutputTokens < 0 || attempt.CostUSD < 0 || attempt.TokenBudget < 0 || attempt.CostBudgetUSD < 0 || math.IsNaN(attempt.CostBudgetUSD) || math.IsInf(attempt.CostBudgetUSD, 0) || attempt.IterationBudget < 0 || attempt.TimeoutSeconds < 0 {
			return fmt.Errorf("goal graph snapshot has invalid attempt %q state, number, or generation", attempt.ID)
		}
		if attempt.MutationGeneration > snapshot.MutationGeneration {
			return fmt.Errorf("goal graph snapshot attempt %q has a future mutation generation", attempt.ID)
		}
		if attempt.PendingAction != nil && attempt.PendingAction.BaseMutationGeneration > snapshot.MutationGeneration {
			return fmt.Errorf("goal graph snapshot attempt %q has a pending action with a future base generation", attempt.ID)
		}
		if attempt.PendingAction != nil && attempt.State != AttemptRunning && attempt.State != AttemptInterrupted {
			return fmt.Errorf("attempt %q retains a pending action in state %q", attempt.ID, attempt.State)
		}
		if attempt.State == AttemptCandidate && attempt.Candidate == nil {
			return fmt.Errorf("candidate attempt %q has no retained writer candidate", attempt.ID)
		}
		if attempt.State == AttemptCandidate && len(attempt.Candidate.ScopeViolations) > 0 {
			return fmt.Errorf("candidate attempt %q retains write-scope violations", attempt.ID)
		}
		if attempt.State == AttemptCandidate && nodes[attempt.NodeID].State != NodeBlocked {
			return fmt.Errorf("candidate attempt %q is not attached to a review-blocked node", attempt.ID)
		}
		if nodes[attempt.NodeID].Execution == ExecutionIsolatedWrite && attempt.State == AttemptRunning && (strings.TrimSpace(attempt.BaseWorkspaceToken) == "" || strings.TrimSpace(attempt.BaseCommit) == "") {
			return fmt.Errorf("running isolated writer attempt %q lacks a stable parent base", attempt.ID)
		}
		if attempt.Candidate != nil {
			if err := validateWriterCandidate(*attempt.Candidate); err != nil {
				return fmt.Errorf("attempt %q has invalid writer candidate: %w", attempt.ID, err)
			}
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
	runningPrimary, runningReads, runningWriters := 0, 0, 0
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
			switch node.Execution {
			case ExecutionReadOnly:
				runningReads++
			case ExecutionIsolatedWrite:
				runningWriters++
			default:
				runningPrimary++
			}
		}
		if node.AcceptedAttemptID != "" {
			if attempt, ok := attempts[node.AcceptedAttemptID]; !ok || attempt.NodeID != node.ID || attempt.State != AttemptAccepted {
				return fmt.Errorf("node %d references invalid accepted attempt %q", node.ID, node.AcceptedAttemptID)
			}
		}
	}
	if runningPrimary > 1 || runningReads > snapshot.ReadFanout.MaxConcurrent || runningWriters > snapshot.WriterFanout.MaxConcurrent || (runningPrimary > 0 && runningReads+runningWriters > 0) || (runningReads > 0 && runningWriters > 0) {
		return errors.New("goal graph snapshot exceeds its primary/read/writer running-attempt bounds")
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
	if snapshot.Outcome != "" && runningPrimary+runningReads+runningWriters > 0 {
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
	case AttemptRunning, AttemptRetryable, AttemptFailed, AttemptBlocked, AttemptCancelled, AttemptBudgetExhausted, AttemptAccepted, AttemptCandidate, AttemptInterrupted:
		return true
	default:
		return false
	}
}

func validEvidence(item Evidence) bool {
	switch item.Kind {
	case EvidenceToolResult, EvidenceVerification:
		return item.Status == "passed" || item.Status == "failed"
	case EvidenceNodeResult, EvidenceDelegateRead, EvidenceDelegateWrite:
		return item.Status == "accepted"
	default:
		return false
	}
}

func validateWriterCandidate(candidate WriterCandidate) error {
	if strings.TrimSpace(candidate.WorkerID) == "" || strings.TrimSpace(candidate.Worktree) == "" || strings.TrimSpace(candidate.Branch) == "" || strings.TrimSpace(candidate.BaseCommit) == "" {
		return errors.New("candidate needs worker, worktree, branch, and base commit identity")
	}
	if len(candidate.WorkerID) > 256 || len(candidate.Worktree) > 4096 || len(candidate.Branch) > 512 || len(candidate.BaseCommit) > 256 || len(candidate.VerificationState) > 64 || len(candidate.VerificationToken) > 512 {
		return errors.New("candidate identity or verification metadata exceeds its bound")
	}
	if len(candidate.WritePaths) == 0 || len(candidate.ChangedFiles) == 0 || len(candidate.ChangedFiles) > maxWriterCandidateFiles || len(candidate.ScopeViolations) > maxWriterCandidateFiles || len(candidate.Verification) > maxWriterVerification {
		return errors.New("candidate has invalid scope, changed-file, violation, or verification bounds")
	}
	normalized, err := writescope.Normalize(candidate.WritePaths, true)
	if err != nil || len(normalized) == 1 && normalized[0] == writescope.Workspace {
		return errors.New("candidate write scope is invalid or workspace-wide")
	}
	if !equalStrings(candidate.WritePaths, normalized) {
		return errors.New("candidate write scope is not canonical")
	}
	for _, value := range append(append([]string(nil), candidate.ChangedFiles...), candidate.ScopeViolations...) {
		if strings.TrimSpace(value) == "" || len(value) > 4096 {
			return errors.New("candidate changed-file or violation path is empty or exceeds its bound")
		}
	}
	for _, violation := range writescope.Violations(normalized, candidate.ChangedFiles) {
		if !containsString(candidate.ScopeViolations, violation) {
			return fmt.Errorf("candidate omits observed scope violation %q", violation)
		}
	}
	switch candidate.VerificationState {
	case "", "passed", "failed", "partial", "stale", "unavailable", "blocked", "cancelled", "timed_out", "rejected", "running":
	default:
		return fmt.Errorf("candidate has invalid verification state %q", candidate.VerificationState)
	}
	for _, result := range candidate.Verification {
		if strings.TrimSpace(result.Command) == "" || strings.TrimSpace(result.StateToken) == "" || len(result.Command) > 4096 || len(result.StateToken) > 512 {
			return errors.New("candidate verification lacks command or state token")
		}
		switch result.Status {
		case "passed", "failed", "blocked", "rejected", "cancelled", "timed_out", "stale":
		default:
			return fmt.Errorf("candidate verification has invalid status %q", result.Status)
		}
	}
	if candidate.VerificationState == "passed" {
		if candidate.VerificationToken == "" || len(candidate.Verification) == 0 {
			return errors.New("passed candidate lacks verification token or results")
		}
		for _, result := range candidate.Verification {
			if result.Status != "passed" || result.StateToken != candidate.VerificationToken {
				return errors.New("passed candidate contains failed or stale verification")
			}
		}
	}
	return nil
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
	CostBudgetUSD  float64
	MaxIterations  int
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
	Iterations     int
	InputTokens    int
	OutputTokens   int
	CostUSD        float64
	CostAvailable  bool
	CostEstimated  bool
}

// WriterBase is the stable parent-workspace identity shared by every writer
// in one candidate wave. Clean is observed by the runtime immediately before
// claims are persisted; child work never starts from an uncommitted parent.
type WriterBase struct {
	WorkspaceToken string
	Commit         string
	Clean          bool
	DirtyPaths     []string
	DirtyCount     int
	Problem        string
}

// ValidateWriterBase names the exact precondition that prevents a retained
// candidate from starting. It is shared by approval preflight and the
// scheduler recheck so the operator sees the same actionable diagnosis at
// both boundaries.
func ValidateWriterBase(base WriterBase) error {
	if problem := strings.TrimSpace(base.Problem); problem != "" {
		return errors.New(problem)
	}
	if strings.TrimSpace(base.WorkspaceToken) == "" {
		return errors.New("isolated-writer parent workspace has no observed state token")
	}
	if strings.TrimSpace(base.Commit) == "" {
		return errors.New("isolated-writer parent workspace has no Git base commit")
	}
	if !base.Clean {
		if base.DirtyCount > 0 || len(base.DirtyPaths) > 0 {
			count := base.DirtyCount
			if count == 0 {
				count = len(base.DirtyPaths)
			}
			detail := strings.Join(base.DirtyPaths, ", ")
			if detail == "" {
				detail = "paths unavailable"
			}
			return fmt.Errorf("isolated-writer parent workspace is dirty (%d paths: %s); commit or reconcile it before approving terminal candidates", count, detail)
		}
		return errors.New("isolated-writer parent workspace changed while its stable base was inspected; retry after the workspace is stable")
	}
	return nil
}

// WriterClaim is one durable dependency-ready isolated-writer assignment.
// Every claim in a wave has the same Git base and a pairwise-disjoint scope.
type WriterClaim struct {
	Node           Node
	Attempt        Attempt
	WritePaths     []string
	TokenBudget    int
	CostBudgetUSD  float64
	MaxIterations  int
	TimeoutSeconds int
}

// WriterResult is the bounded machine inbox for an isolated writer. A passed
// result becomes a retained candidate for review, never an accepted node or a
// parent-workspace mutation in this slice.
type WriterResult struct {
	AttemptID            string
	WorkerID             string
	Status               string
	FailureKind          FailureKind
	Summary              string
	Error                string
	Evidence             []string
	WritePaths           []string
	ChangedFiles         []string
	ScopeViolations      []string
	Worktree             string
	Branch               string
	BaseCommit           string
	ParentWorkspaceToken string
	VerificationState    string
	VerificationToken    string
	Verification         []CandidateVerification
	Iterations           int
	InputTokens          int
	OutputTokens         int
	CostUSD              float64
	CostAvailable        bool
	CostEstimated        bool
}

// HasReadyWriters reports whether stable-base discovery could produce work.
// It is intentionally advisory; StartReadyWriters rechecks under the lock.
func (g *Graph) HasReadyWriters() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" || g.state.PauseRequested || g.activeAttemptLocked() != nil {
		return false
	}
	for _, node := range g.state.Nodes {
		if node.State == NodeReady && node.Execution == ExecutionIsolatedWrite {
			return true
		}
	}
	return false
}

// StartReadyWriters durably claims one bounded wave of pairwise-disjoint
// isolated writers from a single clean, stable Git base.
func (g *Graph) StartReadyWriters(ctx context.Context, base WriterBase, limit int) ([]WriterClaim, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return nil, ErrGraphTerminal
	}
	if g.state.PauseRequested {
		return nil, ErrGraphPaused
	}
	if g.activeAttemptLocked() != nil {
		return nil, errors.New("goal graph already has a running attempt")
	}
	if err := g.enforceAggregateBudgetLocked(g.now().UTC(), true); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return nil, persistErr
		}
		return nil, err
	}
	if g.state.WorkspaceToken != "" && base.WorkspaceToken != "" && g.state.WorkspaceToken != base.WorkspaceToken {
		g.invalidateAllDoneLocked("combined workspace changed outside the recorded graph state")
	}
	if g.state.WorkspaceToken == "" && base.WorkspaceToken != "" {
		g.state.WorkspaceToken = base.WorkspaceToken
	}
	g.refreshReadyLocked("dependencies accepted")
	var ready []*Node
	for i := range g.state.Nodes {
		node := &g.state.Nodes[i]
		if node.State == NodeReady && node.Execution == ExecutionIsolatedWrite {
			ready = append(ready, node)
		}
	}
	if len(ready) == 0 {
		return nil, nil
	}
	if baseErr := ValidateWriterBase(base); baseErr != nil {
		reason := baseErr.Error()
		g.blockNodeLocked(ready[0], reason)
		if err := g.persistLocked(ctx, true); err != nil {
			return nil, err
		}
		return nil, ErrGraphTerminal
	}
	if limit <= 0 || limit > g.state.WriterFanout.MaxConcurrent {
		limit = g.state.WriterFanout.MaxConcurrent
	}
	remainingStarts := g.state.WriterFanout.MaxStarts - g.state.WriterFanout.Starts
	status := g.budgetStatusLocked(g.now().UTC())
	if remainingStarts <= 0 || status.RemainingIterations <= 0 || status.RemainingTokens <= 0 || status.RemainingActiveWall <= 0 {
		reason := fmt.Sprintf("automatic isolated-writer budget exhausted: starts %d/%d", g.state.WriterFanout.Starts, g.state.WriterFanout.MaxStarts)
		g.exhaustReadyWriterLocked(ready[0], reason)
		if err := g.persistLocked(ctx, true); err != nil {
			return nil, err
		}
		return nil, ErrGraphTerminal
	}
	selected := make([]*Node, 0, min(limit, remainingStarts))
	for _, node := range ready {
		if len(selected) >= limit || len(selected) >= remainingStarts || len(selected) >= status.RemainingIterations || len(selected) >= status.RemainingTokens {
			break
		}
		overlaps := false
		for _, prior := range selected {
			if writescope.Overlap(prior.WritePaths, node.WritePaths) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			selected = append(selected, node)
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	perTaskTokens := max(1, status.RemainingTokens/len(selected))
	perTaskIterations := min(defaultWriterTaskIterations, max(1, status.RemainingIterations/len(selected)))
	perTaskCost := 0.0
	if status.CostEnforceable && status.Usage.InputTokens+status.Usage.OutputTokens > 0 {
		perTaskCost = status.RemainingCostUSD / float64(len(selected))
	}
	timeout := min(defaultWriterTaskWallSeconds, max(1, int(math.Ceil(status.RemainingActiveWall.Seconds()))))
	now := g.now().UTC()
	claims := make([]WriterClaim, 0, len(selected))
	for index, node := range selected {
		number := len(node.AttemptIDs) + 1
		attempt := Attempt{
			ID: g.newID("attempt"), NodeID: node.ID, Number: number, State: AttemptRunning,
			GraphGeneration: g.state.Generation, BaseWorkspaceToken: base.WorkspaceToken, BaseCommit: base.Commit,
			MutationGeneration: g.state.MutationGeneration, TokenBudget: perTaskTokens,
			CostBudgetUSD: perTaskCost, IterationBudget: perTaskIterations,
			TimeoutSeconds: timeout, Started: now,
		}
		g.state.Attempts = append(g.state.Attempts, attempt)
		node.State, node.ActiveAttemptID = NodeRunning, attempt.ID
		node.AttemptIDs = append(node.AttemptIDs, attempt.ID)
		g.state.WriterFanout.Starts++
		reason := fmt.Sprintf("automatically delegated: approved isolated_write node is dependency-ready (slot %d/%d)", index+1, g.state.WriterFanout.MaxConcurrent)
		node.Reason = reason
		g.queueUpdateLocked(node.ID, attempt.ID, "delegated_write", reason)
		claims = append(claims, WriterClaim{Node: cloneNode(*node), Attempt: cloneAttempt(attempt), WritePaths: append([]string(nil), node.WritePaths...), TokenBudget: perTaskTokens, CostBudgetUSD: perTaskCost, MaxIterations: perTaskIterations, TimeoutSeconds: timeout})
	}
	if err := g.persistLocked(ctx, true); err != nil {
		return nil, err
	}
	return claims, nil
}

// StartReadyReads durably claims at most the fixed automatic fan-out bound in
// stable plan order. Primary and read attempts never overlap in this slice.
func (g *Graph) StartReadyReads(ctx context.Context, workspaceToken string, limit int) ([]ReadClaim, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return nil, ErrGraphTerminal
	}
	if g.state.PauseRequested {
		return nil, ErrGraphPaused
	}
	if g.activeAttemptLocked() != nil {
		return nil, errors.New("goal graph already has a running attempt")
	}
	if err := g.enforceAggregateBudgetLocked(g.now().UTC(), true); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return nil, persistErr
		}
		return nil, err
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
	now := g.now().UTC()
	aggregate := g.budgetStatusLocked(now)
	remainingStarts := g.state.ReadFanout.MaxStarts - g.state.ReadFanout.Starts
	remainingTokens := min(g.state.ReadFanout.MaxTokens-g.state.ReadFanout.UsedTokens, aggregate.RemainingTokens)
	remainingIterations := aggregate.RemainingIterations
	if g.state.ReadFanout.Started.IsZero() {
		g.state.ReadFanout.Started = now
	}
	remainingWall := min(g.state.ReadFanout.MaxWallSeconds-int(now.Sub(g.state.ReadFanout.Started).Seconds()), max(0, int(math.Ceil(aggregate.RemainingActiveWall.Seconds()))))
	if remainingStarts <= 0 || remainingTokens <= 0 || remainingIterations <= 0 || remainingWall <= 0 {
		reason := fmt.Sprintf("automatic read fan-out budget exhausted: starts %d/%d, tokens %d/%d, wall %ds/%ds", g.state.ReadFanout.Starts, g.state.ReadFanout.MaxStarts, g.state.ReadFanout.UsedTokens, g.state.ReadFanout.MaxTokens, max(0, g.state.ReadFanout.MaxWallSeconds-remainingWall), g.state.ReadFanout.MaxWallSeconds)
		g.exhaustReadyReadLocked(ready[0], reason)
		if err := g.persistLocked(ctx, true); err != nil {
			return nil, err
		}
		return nil, ErrGraphTerminal
	}
	count := min(len(ready), limit, remainingStarts, remainingIterations, remainingTokens)
	perTaskTokens := max(1, remainingTokens/count)
	perTaskIterations := min(defaultReadTaskIterations, max(1, remainingIterations/count))
	perTaskCost := 0.0
	if aggregate.CostEnforceable && aggregate.Usage.InputTokens+aggregate.Usage.OutputTokens > 0 {
		perTaskCost = aggregate.RemainingCostUSD / float64(count)
	}
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
			CostBudgetUSD: perTaskCost, IterationBudget: perTaskIterations,
			TimeoutSeconds: timeout, Started: now,
		}
		g.state.Attempts = append(g.state.Attempts, attempt)
		node.State, node.ActiveAttemptID = NodeRunning, attempt.ID
		node.AttemptIDs = append(node.AttemptIDs, attempt.ID)
		g.state.ReadFanout.Starts++
		reason := fmt.Sprintf("automatically delegated: approved read_only node is dependency-ready (slot %d/%d)", index+1, g.state.ReadFanout.MaxConcurrent)
		node.Reason = reason
		g.queueUpdateLocked(node.ID, attempt.ID, "delegated_read", reason)
		claims = append(claims, ReadClaim{Node: cloneNode(*node), Attempt: cloneAttempt(attempt), TokenBudget: perTaskTokens, CostBudgetUSD: perTaskCost, MaxIterations: perTaskIterations, TimeoutSeconds: timeout})
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
	if err := g.recordReadUsageLocked(attempt, result); err != nil {
		return err
	}
	if err := g.enforceAggregateBudgetLocked(g.now().UTC(), false); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
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

// RecordReadUsage durably retains a completed automatic worker's accounting
// before a graph-level cancellation makes its attempt terminal. It does not
// interpret the worker result or advance node state; FinishRead remains the
// only path that can accept or fail a read node.
func (g *Graph) RecordReadUsage(ctx context.Context, result ReadResult) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	terminalBudget := g.state.Outcome == OutcomeBudgetExhausted
	if g.state.Outcome != "" && !terminalBudget {
		return ErrGraphTerminal
	}
	attempt := g.attemptLocked(result.AttemptID)
	if attempt == nil || (attempt.State != AttemptRunning && !(terminalBudget && attempt.State == AttemptBudgetExhausted)) {
		return fmt.Errorf("goal graph read attempt %q is not running", result.AttemptID)
	}
	node := g.nodeLocked(attempt.NodeID)
	if node == nil || node.Execution != ExecutionReadOnly {
		return fmt.Errorf("goal graph attempt %q is not a read_only node", result.AttemptID)
	}
	if err := g.recordReadUsageLocked(attempt, result); err != nil {
		return err
	}
	if terminalBudget {
		// A parallel wave may already have crossed the graph limit while this
		// worker was in flight. Its completed work still belongs in the durable
		// accounting record even though it cannot change terminal graph state.
		if err := g.persistLocked(ctx, false); err != nil {
			return err
		}
		return ErrAggregateBudget
	}
	if err := g.enforceAggregateBudgetLocked(g.now().UTC(), false); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
	return g.persistLocked(ctx, false)
}

func (g *Graph) recordReadUsageLocked(attempt *Attempt, result ReadResult) error {
	usage := WorkUsage{
		Iterations: result.Iterations, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens,
		CostUSD: result.CostUSD, CostAvailable: result.CostAvailable, CostEstimated: result.CostEstimated,
	}
	if !validWorkUsage(usage) {
		return errors.New("goal graph read result has invalid usage")
	}
	workerID := bounded(result.WorkerID, 256)
	if attempt.UsageRecorded {
		recorded := WorkUsage{
			Iterations: attempt.Iterations, InputTokens: attempt.InputTokens, OutputTokens: attempt.OutputTokens,
			CostUSD: attempt.CostUSD, CostAvailable: attempt.CostAvailable, CostEstimated: attempt.CostEstimated,
		}
		if recorded != usage || attempt.WorkerID != workerID {
			return fmt.Errorf("goal graph read attempt %q already has different usage", attempt.ID)
		}
		return nil
	}
	attempt.WorkerID = workerID
	attempt.UsageRecorded = true
	attempt.Iterations = usage.Iterations
	attempt.InputTokens, attempt.OutputTokens = usage.InputTokens, usage.OutputTokens
	attempt.CostUSD, attempt.CostAvailable = usage.CostUSD, usage.CostAvailable
	attempt.CostEstimated = usage.CostEstimated
	g.state.ReadFanout.UsedTokens += usage.InputTokens + usage.OutputTokens
	addWorkUsage(&g.state.Accounting.AutomaticReads, usage)
	return nil
}

// RecordWriterUsage retains a completed writer's provider accounting before
// one result can make the graph terminal. This preserves every sibling in a
// parallel wave even when aggregate limits are crossed by the combined work.
func (g *Graph) RecordWriterUsage(ctx context.Context, result WriterResult) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	terminalBudget := g.state.Outcome == OutcomeBudgetExhausted
	if g.state.Outcome != "" && !terminalBudget {
		return ErrGraphTerminal
	}
	attempt := g.attemptLocked(result.AttemptID)
	if attempt == nil || (attempt.State != AttemptRunning && !(terminalBudget && attempt.State == AttemptBudgetExhausted)) {
		return fmt.Errorf("goal graph writer attempt %q is not running", result.AttemptID)
	}
	node := g.nodeLocked(attempt.NodeID)
	if node == nil || node.Execution != ExecutionIsolatedWrite {
		return fmt.Errorf("goal graph attempt %q is not an isolated_write node", result.AttemptID)
	}
	if err := g.recordWriterUsageLocked(attempt, result); err != nil {
		return err
	}
	if terminalBudget {
		if err := g.persistLocked(ctx, false); err != nil {
			return err
		}
		return ErrAggregateBudget
	}
	if err := g.enforceAggregateBudgetLocked(g.now().UTC(), false); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
	return g.persistLocked(ctx, false)
}

func (g *Graph) recordWriterUsageLocked(attempt *Attempt, result WriterResult) error {
	usage := WorkUsage{
		Iterations: result.Iterations, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens,
		CostUSD: result.CostUSD, CostAvailable: result.CostAvailable, CostEstimated: result.CostEstimated,
	}
	if !validWorkUsage(usage) {
		return errors.New("goal graph writer result has invalid usage")
	}
	workerID := bounded(result.WorkerID, 256)
	if attempt.UsageRecorded {
		recorded := WorkUsage{
			Iterations: attempt.Iterations, InputTokens: attempt.InputTokens, OutputTokens: attempt.OutputTokens,
			CostUSD: attempt.CostUSD, CostAvailable: attempt.CostAvailable, CostEstimated: attempt.CostEstimated,
		}
		if recorded != usage || attempt.WorkerID != workerID {
			return fmt.Errorf("goal graph writer attempt %q already has different usage", attempt.ID)
		}
		return nil
	}
	attempt.WorkerID, attempt.UsageRecorded = workerID, true
	attempt.Iterations, attempt.InputTokens, attempt.OutputTokens = usage.Iterations, usage.InputTokens, usage.OutputTokens
	attempt.CostUSD, attempt.CostAvailable, attempt.CostEstimated = usage.CostUSD, usage.CostAvailable, usage.CostEstimated
	addWorkUsage(&g.state.Accounting.AutomaticWriters, usage)
	return nil
}

// FinishWriter validates a retained child candidate against the immutable
// claim. Even a fully verified candidate stops at review: OG-3A never marks
// the node done, selects a winner, or mutates the parent workspace.
func (g *Graph) FinishWriter(ctx context.Context, result WriterResult) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return ErrGraphTerminal
	}
	attempt := g.attemptLocked(result.AttemptID)
	if attempt == nil || attempt.State != AttemptRunning {
		return fmt.Errorf("goal graph writer attempt %q is not running", result.AttemptID)
	}
	node := g.nodeLocked(attempt.NodeID)
	if node == nil || node.Execution != ExecutionIsolatedWrite {
		return fmt.Errorf("goal graph attempt %q is not an isolated_write node", result.AttemptID)
	}
	if err := g.recordWriterUsageLocked(attempt, result); err != nil {
		return err
	}
	if err := g.enforceAggregateBudgetLocked(g.now().UTC(), false); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
	now := g.now().UTC()
	normalizedScope, scopeErr := writescope.Normalize(result.WritePaths, true)
	changed := boundedStrings(result.ChangedFiles, maxWriterCandidateFiles, 4096)
	violations := boundedStrings(result.ScopeViolations, maxWriterCandidateFiles, 4096)
	if scopeErr == nil {
		for _, violation := range writescope.Violations(node.WritePaths, changed) {
			if !containsString(violations, violation) {
				violations = append(violations, violation)
			}
		}
	}
	candidate := &WriterCandidate{
		WorkerID: bounded(result.WorkerID, 256), Worktree: bounded(result.Worktree, 4096),
		Branch: bounded(result.Branch, 512), BaseCommit: bounded(result.BaseCommit, 256),
		WritePaths: append([]string(nil), normalizedScope...), ChangedFiles: changed,
		ScopeViolations: violations, VerificationState: bounded(result.VerificationState, 64),
		VerificationToken: bounded(result.VerificationToken, 512),
		Verification:      cloneCandidateVerification(result.Verification),
	}
	if candidate.WorkerID != "" && candidate.Worktree != "" && candidate.Branch != "" && candidate.BaseCommit != "" && len(candidate.ChangedFiles) > 0 && validateWriterCandidate(*candidate) == nil {
		attempt.Candidate = candidate
	}
	freshParent := result.ParentWorkspaceToken != "" && result.ParentWorkspaceToken == attempt.BaseWorkspaceToken
	identityMatches := result.BaseCommit != "" && result.BaseCommit == attempt.BaseCommit
	scopeMatches := scopeErr == nil && equalStrings(normalizedScope, node.WritePaths)
	verificationFresh := candidate.VerificationState == "passed" && candidate.VerificationToken != "" && len(candidate.Verification) > 0
	if verificationFresh {
		for _, verification := range candidate.Verification {
			if verification.Status != "passed" || verification.StateToken != candidate.VerificationToken {
				verificationFresh = false
				break
			}
		}
	}
	valid := result.Status == "done" && strings.TrimSpace(result.Summary) != "" && attempt.Candidate != nil && freshParent && identityMatches && scopeMatches && len(violations) == 0 && verificationFresh
	if valid {
		attempt.State, attempt.Finished, attempt.Summary = AttemptCandidate, now, boundedSummary(result.Summary)
		node.State, node.ActiveAttemptID = NodeBlocked, ""
		node.Reason = fmt.Sprintf("verified isolated writer candidate %s retained at %s; reviewed integration is required before this node can complete", candidate.WorkerID, candidate.Worktree)
		g.addEvidenceLocked(attempt, Evidence{Kind: EvidenceDelegateWrite, Tool: "automatic_write_delegate", Status: "accepted", Summary: boundedSummary(strings.Join(result.Evidence, "\n")), WorkspaceToken: candidate.VerificationToken, MutationGeneration: g.state.MutationGeneration, Finished: now})
		g.queueUpdateLocked(node.ID, attempt.ID, "candidate_ready", node.Reason)
		g.reduceOutcomeLocked()
		return g.persistLocked(ctx, true)
	}

	detail := strings.TrimSpace(result.Error)
	failureKind := FailureProvider
	switch {
	case result.FailureKind != "":
		failureKind = result.FailureKind
		if detail == "" {
			detail = "isolated writer was blocked before execution"
		}
	case !freshParent || !identityMatches:
		failureKind = FailureWorkspaceStale
		if detail == "" {
			detail = "parent workspace or candidate Git base changed while the isolated writer was running"
		}
	case !scopeMatches || len(violations) > 0:
		failureKind = FailureTool
		if detail == "" {
			detail = "isolated writer changed files outside its declared write scope"
		}
	case !verificationFresh:
		failureKind = FailureVerification
		if detail == "" {
			detail = "isolated writer candidate lacks fresh passing machine-observed verification"
		}
	case attempt.Candidate == nil:
		failureKind = FailureTool
		if detail == "" {
			detail = "isolated writer produced no bounded retained candidate"
		}
	}
	if detail == "" {
		detail = "isolated writer ended with status " + result.Status
	}
	attempt.Failures = append(attempt.Failures, Failure{Kind: failureKind, Tool: "automatic_write_delegate", Detail: boundedReason(detail), Time: now})
	attempt.Summary, attempt.Finished = boundedSummary(detail), now
	node.ActiveAttemptID, node.State, node.Reason = "", NodeBlocked, boundedReason(detail)
	switch result.Status {
	case "budget_exhausted":
		attempt.State, node.State = AttemptBudgetExhausted, NodeBudgetExhausted
	case "cancelled":
		attempt.State = AttemptCancelled
	default:
		attempt.State = AttemptBlocked
	}
	g.queueUpdateLocked(node.ID, attempt.ID, string(node.State), node.Reason)
	g.reduceOutcomeLocked()
	return g.persistLocked(ctx, true)
}

// RecordPrimaryUsage durably accounts for one or more primary-lane provider
// iterations. An active primary attempt receives the same bounded counters for
// node inspection; proposal and compaction work may legitimately have no
// active node and remains visible only in the graph aggregate.
func (g *Graph) RecordPrimaryUsage(ctx context.Context, usage WorkUsage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !validWorkUsage(usage) {
		return errors.New("goal graph primary usage is invalid")
	}
	if usage == (WorkUsage{}) {
		return nil
	}
	if g.state.Outcome != "" {
		// A concurrent explicit cancellation owns the terminal snapshot. A late
		// provider return must not reopen or rewrite it merely for accounting.
		return nil
	}
	attempt := g.activeAttemptLocked()
	if attempt != nil {
		node := g.nodeLocked(attempt.NodeID)
		if node == nil || node.Execution != ExecutionPrimary {
			return errors.New("goal graph primary usage arrived while a read-only attempt was active")
		}
	}
	addWorkUsage(&g.state.Accounting.Primary, usage)
	if attempt != nil {
		// Snapshots written before progress-aware leases did not carry an exact
		// progress iteration. A recorded successful tool result is enough to
		// grant one fresh lease on resume; the fixed whole-graph envelope remains
		// the outer bound.
		if attempt.LastProgressIteration == 0 && attempt.ToolSuccesses > 0 && attempt.Iterations > 0 {
			attempt.LastProgressIteration = attempt.Iterations
		}
		attemptUsage := WorkUsage{
			Iterations: attempt.Iterations, InputTokens: attempt.InputTokens, OutputTokens: attempt.OutputTokens,
			CostUSD: attempt.CostUSD, CostAvailable: attempt.CostAvailable, CostEstimated: attempt.CostEstimated,
		}
		addWorkUsage(&attemptUsage, usage)
		attempt.Iterations, attempt.InputTokens, attempt.OutputTokens = attemptUsage.Iterations, attemptUsage.InputTokens, attemptUsage.OutputTokens
		attempt.CostUSD, attempt.CostAvailable, attempt.CostEstimated = attemptUsage.CostUSD, attemptUsage.CostAvailable, attemptUsage.CostEstimated
	}
	if err := g.enforceAggregateBudgetLocked(g.now().UTC(), false); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
	return g.persistLocked(ctx, false)
}

func (g *Graph) exhaustReadyReadLocked(node *Node, reason string) {
	node.State, node.Reason = NodeBudgetExhausted, strings.TrimSpace(reason)
	g.state.Outcome, g.state.Reason = OutcomeBudgetExhausted, node.Reason
	g.stopActiveLocked(g.now().UTC())
	g.queueUpdateLocked(node.ID, "", string(NodeBudgetExhausted), node.Reason)
	g.queueUpdateLocked(0, "", string(OutcomeBudgetExhausted), g.state.Reason)
}

func (g *Graph) exhaustReadyWriterLocked(node *Node, reason string) {
	node.State, node.Reason = NodeBudgetExhausted, strings.TrimSpace(reason)
	g.state.Outcome, g.state.Reason = OutcomeBudgetExhausted, node.Reason
	g.stopActiveLocked(g.now().UTC())
	g.queueUpdateLocked(node.ID, "", string(NodeBudgetExhausted), node.Reason)
	g.queueUpdateLocked(0, "", string(OutcomeBudgetExhausted), g.state.Reason)
}

func validWorkUsage(usage WorkUsage) bool {
	return usage.Iterations >= 0 && usage.InputTokens >= 0 && usage.OutputTokens >= 0 && usage.CostUSD >= 0 && !math.IsNaN(usage.CostUSD) && !math.IsInf(usage.CostUSD, 0)
}

func addWorkUsage(total *WorkUsage, addition WorkUsage) {
	if total == nil {
		return
	}
	totalHasPricedWork := total.InputTokens+total.OutputTokens > 0 || total.CostUSD > 0
	additionHasPricedWork := addition.InputTokens+addition.OutputTokens > 0 || addition.CostUSD > 0
	total.Iterations += addition.Iterations
	total.InputTokens += addition.InputTokens
	total.OutputTokens += addition.OutputTokens
	total.CostUSD += addition.CostUSD
	if additionHasPricedWork {
		if totalHasPricedWork {
			total.CostAvailable = total.CostAvailable && addition.CostAvailable
		} else {
			total.CostAvailable = addition.CostAvailable
		}
	}
	total.CostEstimated = total.CostEstimated || addition.CostEstimated
}

func (g *Graph) StartNext(ctx context.Context, workspaceToken string) (Node, Attempt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return Node{}, Attempt{}, ErrGraphTerminal
	}
	if g.state.PauseRequested {
		return Node{}, Attempt{}, ErrGraphPaused
	}
	if g.activeAttemptLocked() != nil {
		return Node{}, Attempt{}, errors.New("goal graph already has a running attempt")
	}
	if err := g.enforceAggregateBudgetLocked(g.now().UTC(), true); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return Node{}, Attempt{}, persistErr
		}
		return Node{}, Attempt{}, err
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
		PotentialMutation: action.PotentialMutation, NonReplayable: action.NonReplayable,
		BaseWorkspaceToken: strings.TrimSpace(workspaceToken), BaseMutationGeneration: attempt.MutationGeneration,
		Started: now,
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

// completionGapAdvanced recognizes only evidence capable of repairing or
// changing the currently recorded acceptance gate. While a gap is open, a
// novel command string or different read result is still useful evidence, but
// it must not renew the controller's remediation lease on its own.
func completionGapAdvanced(attempt *Attempt, pending *PendingAction, result ToolResult) bool {
	if attempt == nil || attempt.CompletionGap == "" {
		return false
	}
	// A machine-observed repository change is concrete repair progress even
	// before the next verifier runs. This gives the model enough bounded room to
	// add or fix a focused test after a useful verification failure.
	if completionGapWorkspaceAdvanced(pending, result) {
		return true
	}
	gap := attempt.CompletionGap
	if strings.Contains(gap, "no successful bounded tool result") {
		return true
	}
	if strings.Contains(gap, "combined workspace has no state token") && strings.TrimSpace(result.WorkspaceToken) != "" {
		return true
	}
	if strings.Contains(gap, "successful write action produced no combined-workspace change") && strings.TrimSpace(result.WorkspaceToken) != "" && result.WorkspaceToken != attempt.BaseWorkspaceToken {
		return true
	}
	if strings.Contains(gap, "no successful recognized verification bound to the current combined workspace") && result.Verification && strings.TrimSpace(result.WorkspaceToken) != "" {
		return true
	}
	// A successful retry or alternative is handled independently by
	// resolveFailuresLocked.
	return false
}

func completionGapWorkspaceAdvanced(pending *PendingAction, result ToolResult) bool {
	return pending != nil && pending.PotentialMutation && strings.TrimSpace(result.WorkspaceToken) != "" && result.WorkspaceToken != pending.BaseWorkspaceToken
}

func (g *Graph) hasEquivalentVerificationFailureLocked(attempt *Attempt, candidate Evidence) bool {
	candidate.Summary = boundedSummary(candidate.Summary)
	for _, id := range attempt.EvidenceIDs {
		evidence := g.evidenceLocked(id)
		// Command spelling is deliberately excluded. Re-running the same failing
		// verifier through a different executable or flag is not new diagnostic
		// progress unless its machine output or workspace state changed.
		if evidence != nil && evidence.Kind == EvidenceVerification && evidence.Status == "failed" && evidence.Summary == candidate.Summary && evidence.WorkspaceToken == candidate.WorkspaceToken && evidence.MutationGeneration == candidate.MutationGeneration {
			return true
		}
	}
	return false
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
	// BeginTool advances the global generation before every action that might
	// mutate state so an interrupted action remains conservatively ambiguous.
	// Once the action returns, an unchanged machine-observed workspace token
	// proves that it did not change the repository. Restore this attempt's
	// observed workspace generation while leaving the global write-ahead
	// generation monotonic for recovery and audit history.
	if pending != nil && pending.PotentialMutation && result.WorkspaceToken != "" && result.WorkspaceToken == pending.BaseWorkspaceToken {
		attempt.MutationGeneration = pending.BaseMutationGeneration
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
			summary := strings.TrimSpace(result.Summary)
			if summary == "" {
				summary = detail
			}
			evidence := Evidence{Kind: EvidenceVerification, Tool: result.Tool, Command: result.Command, Status: "failed", Summary: summary, WorkspaceToken: result.WorkspaceToken, MutationGeneration: attempt.MutationGeneration, Started: result.Started, Finished: result.Finished}
			if attempt.CompletionGap != "" && (!g.hasEquivalentVerificationFailureLocked(attempt, evidence) || completionGapWorkspaceAdvanced(pending, result)) {
				attempt.LastProgressIteration = attempt.Iterations
				attempt.CompletionGapIteration = attempt.Iterations
			}
			g.addEvidenceLocked(attempt, evidence)
		} else if attempt.CompletionGap != "" && completionGapWorkspaceAdvanced(pending, result) {
			attempt.LastProgressIteration = attempt.Iterations
			attempt.CompletionGapIteration = attempt.Iterations
		}
		g.queueUpdateLocked(attempt.NodeID, attempt.ID, "action_failed", detail)
	} else {
		attempt.ToolSuccesses++
		resolvedFailure := g.resolveFailuresLocked(attempt, result.Tool, result.Risk)
		kind := EvidenceToolResult
		if result.Verification {
			kind = EvidenceVerification
		}
		evidence := Evidence{Kind: kind, Tool: result.Tool, Command: result.Command, Status: "passed", Summary: strings.TrimSpace(result.Summary), WorkspaceToken: result.WorkspaceToken, MutationGeneration: attempt.MutationGeneration, Started: result.Started, Finished: result.Finished}
		if resolvedFailure || completionGapAdvanced(attempt, pending, result) || (attempt.CompletionGap == "" && !g.hasEquivalentEvidenceLocked(attempt, evidence)) {
			attempt.LastProgressIteration = attempt.Iterations
			if attempt.CompletionGap != "" {
				attempt.CompletionGapIteration = attempt.Iterations
			}
		}
		g.addEvidenceLocked(attempt, evidence)
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
		gap := strings.Join(issues, "; ")
		if attempt.CompletionGap != gap {
			attempt.CompletionGap = gap
			attempt.CompletionGapIteration = attempt.Iterations
		}
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
	attempt.CompletionGap, attempt.CompletionGapIteration = "", 0
	attempt.State, attempt.Finished, attempt.Summary = AttemptAccepted, now, boundedSummary(summary)
	resultEvidence := Evidence{Kind: EvidenceNodeResult, Status: "accepted", Summary: attempt.Summary, WorkspaceToken: workspaceToken, MutationGeneration: attempt.MutationGeneration, Finished: now}
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
	b.WriteString("Continue this node with ordinary tools. Obtain proportionate recognized verification after the last potentially mutating action, repair a failure, propose a bounded graph revision, or explicitly block the node with an exact reason. If the detected verifier reports that no tests exist, add a focused smoke test for this node's changed behavior when that is within scope, then run the verifier directly. Do not repeat output wrappers or an unchanged failing command. This notice changes no permission or user scope.")
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
	if err := ValidateExecutableSpec(spec); err != nil {
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
		writePaths, _ := writescope.Normalize(proposed.WritePaths, proposed.Execution == ExecutionIsolatedWrite)
		node := Node{ID: proposed.ID, Position: position, Title: strings.TrimSpace(proposed.Title), DependsOn: append([]int(nil), proposed.DependsOn...), Acceptance: append([]string(nil), proposed.Acceptance...), Execution: normalizeExecution(proposed.Execution), WritePaths: writePaths, State: NodeProposed}
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
		if node != nil && node.Execution == ExecutionIsolatedWrite {
			reason := "an isolated writer may have changed its retained worktree before the session stopped; inspect delegated candidates and reconcile explicitly before continuing"
			attempt.Failures = append(attempt.Failures, Failure{Kind: FailureInterruptedAction, Tool: "automatic_write_delegate", Risk: "write", Detail: reason, Time: now})
			node.State, node.ActiveAttemptID, node.Reason = NodeBlocked, "", reason
			g.queueUpdateLocked(node.ID, attempt.ID, string(NodeBlocked), reason)
			continue
		}
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

// RequestPause durably prevents the graph from starting another provider or
// scheduler iteration. It is cooperative: an iteration already in flight may
// finish, and the agent acknowledges the safe boundary with ReachPause.
func (g *Graph) RequestPause(ctx context.Context, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return ErrGraphTerminal
	}
	if g.state.PauseRequested {
		return nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "paused explicitly by the user"
	}
	g.state.PauseRequested = true
	g.state.PauseReached = g.activeAttemptLocked() == nil
	g.state.PauseReason = reason
	if g.state.PauseReached {
		g.stopActiveLocked(g.now().UTC())
	}
	state := "pause_requested"
	detail := reason + "; the current iteration may finish before scheduling stops"
	if g.state.PauseReached {
		state = "paused"
		detail = reason + "; no new work will start until explicit resume"
	}
	g.queueUpdateLocked(0, "", state, detail)
	return g.persistLocked(ctx, true)
}

// ReachPause records that the running agent reached a boundary at which no
// provider request, automatic read wave, or primary action is in flight.
func (g *Graph) ReachPause(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.state.PauseRequested {
		return errors.New("goal graph has no pause request")
	}
	if g.state.Outcome != "" {
		return ErrGraphTerminal
	}
	if g.state.PauseReached {
		return nil
	}
	for i := range g.state.Attempts {
		attempt := &g.state.Attempts[i]
		if attempt.State == AttemptRunning && attempt.PendingAction != nil {
			return errors.New("goal graph cannot reach a pause boundary while an action is pending")
		}
	}
	g.state.PauseReached = true
	g.stopActiveLocked(g.now().UTC())
	g.queueUpdateLocked(0, "", "paused", g.state.PauseReason+"; safe scheduling boundary reached")
	return g.persistLocked(ctx, true)
}

// Resume clears a durable pause without changing attempts, evidence, bounds,
// permissions, or terminal state. The caller must explicitly start the next
// agent turn after this transition.
func (g *Graph) Resume(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return ErrGraphTerminal
	}
	if !g.state.PauseRequested {
		return errors.New("goal graph is not paused")
	}
	reason := g.state.PauseReason
	g.state.PauseRequested, g.state.PauseReached, g.state.PauseReason = false, false, ""
	now := g.now().UTC()
	g.startActiveLocked(now)
	if err := g.enforceAggregateBudgetLocked(now, true); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
	g.queueUpdateLocked(0, "", "resumed", reason)
	return g.persistLocked(ctx, true)
}

// PauseState returns the durable operator-control state without activating or
// scheduling the graph.
func (g *Graph) PauseState() (requested, reached bool, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state.PauseRequested, g.state.PauseReached, g.state.PauseReason
}

// RetryNode reopens one safely retryable blocker as a fresh bounded attempt.
// It preserves the blocked attempt and evidence. An ambiguous non-replayable
// action is never converted into retryable work by an operator command.
func (g *Graph) RetryNode(ctx context.Context, nodeID int, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != OutcomeBlocked {
		return errors.New("goal graph is not blocked")
	}
	node := g.nodeLocked(nodeID)
	if node == nil {
		return fmt.Errorf("goal graph has no node %d", nodeID)
	}
	if node.State != NodeBlocked {
		return fmt.Errorf("goal graph node %d is %s, not blocked", nodeID, node.State)
	}
	if len(node.AttemptIDs) >= g.state.MaxAttemptsPerNode {
		return fmt.Errorf("goal graph node %d exhausted its %d-attempt bound", nodeID, g.state.MaxAttemptsPerNode)
	}
	if len(node.AttemptIDs) == 0 {
		return fmt.Errorf("goal graph node %d has no blocked attempt to retry", nodeID)
	}
	blocked := g.attemptLocked(node.AttemptIDs[len(node.AttemptIDs)-1])
	if blocked == nil || (blocked.State != AttemptBlocked && blocked.State != AttemptInterrupted) {
		return fmt.Errorf("goal graph node %d has no immutable blocked attempt", nodeID)
	}
	if blocked.PendingAction != nil && blocked.PendingAction.NonReplayable {
		return fmt.Errorf("%w: node %d retains ambiguous action %s", ErrUnsafeNodeRetry, nodeID, blocked.PendingAction.Tool)
	}
	for _, failure := range blocked.Failures {
		if !failure.Resolved && failure.Kind == FailureInterruptedAction {
			return fmt.Errorf("%w: node %d has an interrupted action requiring reconciliation", ErrUnsafeNodeRetry, nodeID)
		}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "retry requested explicitly by the user"
	}
	g.state.Outcome, g.state.Reason = "", ""
	now := g.now().UTC()
	g.startActiveLocked(now)
	if err := g.enforceAggregateBudgetLocked(now, true); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
	node.State, node.ActiveAttemptID, node.Reason = NodeRetryable, "", reason
	g.queueUpdateLocked(node.ID, blocked.ID, "retry_requested", reason)
	g.refreshReadyLocked("operator-approved retry is dependency-ready")
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
	g.stopActiveLocked(now)
	g.clearPauseLocked()
	g.queueUpdateLocked(0, "", string(OutcomeCancelled), g.state.Reason)
	return g.persistLocked(ctx, true)
}

func (g *Graph) ExhaustBudget(ctx context.Context, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return nil
	}
	g.exhaustAggregateLocked(g.now().UTC(), reason)
	return g.persistLocked(ctx, true)
}

// EnforceAggregateBudget is the admission gate called before scheduling or a
// provider boundary. Hitting an exact limit prevents more work; a response
// that crosses a limit is handled by the recording paths below.
func (g *Graph) EnforceAggregateBudget(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		if g.state.Outcome == OutcomeBudgetExhausted {
			return ErrAggregateBudget
		}
		return ErrGraphTerminal
	}
	if err := g.enforceAggregateBudgetLocked(g.now().UTC(), true); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
	return nil
}

// Activate starts the active execution clock after an inert restore. Saved
// bytes never call this method; only the explicit application resume path does.
func (g *Graph) Activate(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != "" {
		return ErrGraphTerminal
	}
	if g.state.PauseRequested {
		return ErrGraphPaused
	}
	now := g.now().UTC()
	g.startActiveLocked(now)
	if err := g.enforceAggregateBudgetLocked(now, true); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
	g.queueUpdateLocked(0, "", "resumed", "explicitly reattached after durable restore")
	return g.persistLocked(ctx, true)
}

// BudgetStatus returns the immutable limits and their current machine-observed
// consumption without changing graph state.
func (g *Graph) BudgetStatus(now time.Time) BudgetStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.budgetStatusLocked(now)
}

// UsageTotals returns durable per-lane counters plus live/frozen elapsed time.
// It does not infer missing provider usage or pricing.
func (g *Graph) UsageTotals(now time.Time) UsageSummary {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.usageSummaryLocked(now)
}

func (g *Graph) usageSummaryLocked(now time.Time) UsageSummary {
	primary := g.state.Accounting.Primary
	reads := g.state.Accounting.AutomaticReads
	writers := g.state.Accounting.AutomaticWriters
	total := WorkUsage{}
	addWorkUsage(&total, primary)
	addWorkUsage(&total, reads)
	addWorkUsage(&total, writers)
	if now.IsZero() {
		now = g.now()
	}
	end := now.UTC()
	if g.state.Outcome != "" {
		end = g.state.Updated
	}
	elapsed := end.Sub(g.state.Accounting.Started)
	if elapsed < 0 {
		elapsed = 0
	}
	return UsageSummary{Primary: primary, AutomaticReads: reads, AutomaticWriters: writers, Total: total, Elapsed: elapsed, ActiveElapsed: g.activeElapsedLocked(now)}
}

func (g *Graph) budgetStatusLocked(now time.Time) BudgetStatus {
	usage := g.usageSummaryLocked(now)
	limits := g.state.AggregateBudget
	remainingWall := time.Duration(limits.MaxActiveWallSeconds)*time.Second - usage.ActiveElapsed
	if remainingWall < 0 {
		remainingWall = 0
	}
	costEnforceable := usage.Total.InputTokens+usage.Total.OutputTokens == 0 || usage.Total.CostAvailable
	return BudgetStatus{
		Limits: limits, Usage: usage.Total, ActiveElapsed: usage.ActiveElapsed,
		RemainingIterations: max(0, limits.MaxIterations-usage.Total.Iterations),
		RemainingTokens:     max(0, limits.MaxTokens-usage.Total.InputTokens-usage.Total.OutputTokens),
		RemainingCostUSD:    math.Max(0, limits.MaxCostUSD-usage.Total.CostUSD),
		RemainingActiveWall: remainingWall, CostEnforceable: costEnforceable,
	}
}

func (g *Graph) activeElapsedLocked(now time.Time) time.Duration {
	elapsed := g.state.Accounting.ActiveElapsed
	if !g.state.Accounting.ActiveSince.IsZero() {
		if now.IsZero() {
			now = g.now()
		}
		if current := now.UTC().Sub(g.state.Accounting.ActiveSince); current > 0 {
			elapsed += current
		}
	}
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func (g *Graph) startActiveLocked(now time.Time) {
	if g.state.Outcome == "" && !g.state.PauseReached && g.state.Accounting.ActiveSince.IsZero() {
		g.state.Accounting.ActiveSince = now.UTC()
	}
}

func (g *Graph) stopActiveLocked(now time.Time) {
	if g.state.Accounting.ActiveSince.IsZero() {
		return
	}
	if elapsed := now.UTC().Sub(g.state.Accounting.ActiveSince); elapsed > 0 {
		g.state.Accounting.ActiveElapsed += elapsed
	}
	g.state.Accounting.ActiveSince = time.Time{}
}

func (g *Graph) enforceAggregateBudgetLocked(now time.Time, atBoundary bool) error {
	status := g.budgetStatusLocked(now)
	exhausted := func(used float64, limit float64) bool {
		if atBoundary {
			return used >= limit
		}
		return used > limit
	}
	reason := ""
	switch {
	case exhausted(float64(status.Usage.Iterations), float64(status.Limits.MaxIterations)):
		reason = fmt.Sprintf("aggregate provider-iteration budget exhausted: %d/%d", status.Usage.Iterations, status.Limits.MaxIterations)
	case exhausted(float64(status.Usage.InputTokens+status.Usage.OutputTokens), float64(status.Limits.MaxTokens)):
		reason = fmt.Sprintf("aggregate token budget exhausted: %d/%d", status.Usage.InputTokens+status.Usage.OutputTokens, status.Limits.MaxTokens)
	case status.CostEnforceable && exhausted(status.Usage.CostUSD, status.Limits.MaxCostUSD):
		reason = fmt.Sprintf("aggregate estimated-cost budget exhausted: $%.6f/$%.6f", status.Usage.CostUSD, status.Limits.MaxCostUSD)
	case exhausted(status.ActiveElapsed.Seconds(), float64(status.Limits.MaxActiveWallSeconds)):
		reason = fmt.Sprintf("aggregate active-wall budget exhausted: %s/%s", formatGraphElapsed(status.ActiveElapsed), formatGraphElapsed(time.Duration(status.Limits.MaxActiveWallSeconds)*time.Second))
	}
	if reason == "" {
		return nil
	}
	g.exhaustAggregateLocked(now, reason)
	return ErrAggregateBudget
}

func (g *Graph) exhaustAggregateLocked(now time.Time, reason string) {
	reason = strings.TrimSpace(reason)
	for i := range g.state.Attempts {
		attempt := &g.state.Attempts[i]
		if attempt.State == AttemptRunning {
			attempt.State, attempt.Finished = AttemptBudgetExhausted, now
		}
	}
	for i := range g.state.Nodes {
		node := &g.state.Nodes[i]
		if node.State == NodeDone {
			continue
		}
		attemptID := node.ActiveAttemptID
		node.State, node.ActiveAttemptID, node.Reason = NodeBudgetExhausted, "", reason
		g.queueUpdateLocked(node.ID, attemptID, string(NodeBudgetExhausted), reason)
	}
	g.stopActiveLocked(now)
	g.state.Outcome, g.state.Reason = OutcomeBudgetExhausted, reason
	g.clearPauseLocked()
	g.queueUpdateLocked(0, "", string(OutcomeBudgetExhausted), reason)
}

func formatWorkUsage(usage WorkUsage) string {
	cost := "cost unavailable"
	if usage.CostAvailable {
		qualifier := "recorded"
		if usage.CostEstimated {
			qualifier = "estimated"
		}
		cost = fmt.Sprintf("$%.6f %s", usage.CostUSD, qualifier)
	}
	return fmt.Sprintf("%d input + %d output tokens · %d provider iterations · %s", usage.InputTokens, usage.OutputTokens, usage.Iterations, cost)
}

func formatGraphElapsed(elapsed time.Duration) string {
	return elapsed.Round(time.Second).String()
}

func (g *Graph) Render() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "Runtime-owned goal graph %s · generation %d\nGoal: %s\n", g.state.ID, g.state.Generation, g.state.Goal)
	if g.state.PauseRequested {
		state := "requested; current iteration may still be finishing"
		if g.state.PauseReached {
			state = "reached; no new work will start"
		}
		fmt.Fprintf(&b, "Scheduling pause: %s — %s\n", state, g.state.PauseReason)
	}
	marks := map[NodeState]string{NodeProposed: "[ ]", NodeReady: "[>]", NodeRunning: "[~]", NodeRetryable: "[r]", NodeStale: "[s]", NodeBlocked: "[!]", NodeCancelled: "[-]", NodeBudgetExhausted: "[$]", NodeDone: "[x]"}
	for _, node := range g.state.Nodes {
		fmt.Fprintf(&b, "%s %d. %s · %s · %s", marks[node.State], node.ID, node.Title, node.State, node.Execution)
		if len(node.DependsOn) > 0 {
			fmt.Fprintf(&b, " · after %s", joinInts(node.DependsOn))
		}
		if len(node.WritePaths) > 0 {
			fmt.Fprintf(&b, " · writes %s", strings.Join(node.WritePaths, ", "))
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
		if node.AcceptedAttemptID != "" {
			if attempt := g.attemptLocked(node.AcceptedAttemptID); attempt != nil && attempt.Summary != "" {
				label := "accepted result"
				if node.Execution == ExecutionReadOnly {
					label = "delegated result"
				}
				fmt.Fprintf(&b, "    %s: %s\n", label, bounded(attempt.Summary, 2400))
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
	b.WriteString("NODE BOUNDARY — the runtime owns node state and evidence, and graph state grants no tool permission. Work only on the node marked running. After that node's final successful verifier, return a tool-free completion proposal immediately; do not begin a later node until the runtime selects it. Use propose_goal_graph_revision for a bounded replan or block_goal_node for an exact blocker. Model prose cannot mark a node done by itself.")
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
	if g.state.PauseRequested {
		state := "requested · current iteration may still be finishing"
		if g.state.PauseReached {
			state = "paused · no new work will start"
		}
		fmt.Fprintf(&b, "Scheduling: %s — %s\n", state, bounded(g.state.PauseReason, 600))
	} else {
		b.WriteString("Scheduling: active\n")
	}
	fmt.Fprintf(&b, "Bounds: %d nodes · %d attempts/node · %d revisions\n", maxGraphNodes, g.state.MaxAttemptsPerNode, g.state.MaxRevisions)
	fmt.Fprintf(&b, "Read fan-out: %d/%d starts · %d/%d tokens · at most %d concurrent · %ds wall bound\n", g.state.ReadFanout.Starts, g.state.ReadFanout.MaxStarts, g.state.ReadFanout.UsedTokens, g.state.ReadFanout.MaxTokens, g.state.ReadFanout.MaxConcurrent, g.state.ReadFanout.MaxWallSeconds)
	fmt.Fprintf(&b, "Isolated-writer wave: %d/%d starts · at most %d concurrent retained candidates\n", g.state.WriterFanout.Starts, g.state.WriterFanout.MaxStarts, g.state.WriterFanout.MaxConcurrent)
	usage := g.usageSummaryLocked(g.now().UTC())
	budget := g.budgetStatusLocked(g.now().UTC())
	fmt.Fprintf(&b, "Aggregate model work: %s · %s active (%s elapsed)\n", formatWorkUsage(usage.Total), formatGraphElapsed(usage.ActiveElapsed), formatGraphElapsed(usage.Elapsed))
	fmt.Fprintf(&b, "  Primary (proposal + serial lane): %s\n", formatWorkUsage(usage.Primary))
	fmt.Fprintf(&b, "  Automatic reads: %s\n", formatWorkUsage(usage.AutomaticReads))
	fmt.Fprintf(&b, "  Automatic isolated writers: %s\n", formatWorkUsage(usage.AutomaticWriters))
	costBound := fmt.Sprintf("$%.2f when pricing is complete", budget.Limits.MaxCostUSD)
	if budget.CostEnforceable {
		costBound = fmt.Sprintf("$%.6f/$%.2f", budget.Usage.CostUSD, budget.Limits.MaxCostUSD)
	}
	fmt.Fprintf(&b, "Aggregate envelope: %d/%d provider iterations · %d/%d tokens · %s · %s/%s active wall\n", budget.Usage.Iterations, budget.Limits.MaxIterations, budget.Usage.InputTokens+budget.Usage.OutputTokens, budget.Limits.MaxTokens, costBound, formatGraphElapsed(budget.ActiveElapsed), formatGraphElapsed(time.Duration(budget.Limits.MaxActiveWallSeconds)*time.Second))
	b.WriteString("Execution: end-to-end graphs use one serial primary lane for every parent-workspace write, with bounded automatic read_only workers; an explicitly candidate-only graph may instead use one bounded pairwise-disjoint terminal isolated_write wave.\n")
	b.WriteString("Write scope: isolated writers require explicit narrow scopes and a common clean Git base; no candidate is selected, integrated, or allowed to unlock dependents automatically.\n")
	b.WriteString("Authority: every action still uses ordinary permissions; approval grants no publication or additional tool access.\n")
	b.WriteString("Completion: changed workspace state requires fresh machine-observed verification.\n\n")
	marks := map[NodeState]string{NodeProposed: "[ ]", NodeReady: "[>]", NodeRunning: "[~]", NodeRetryable: "[r]", NodeStale: "[s]", NodeBlocked: "[!]", NodeCancelled: "[-]", NodeBudgetExhausted: "[$]", NodeDone: "[x]"}
	for _, node := range g.state.Nodes {
		fmt.Fprintf(&b, "%s %d. %s · %s · %s", marks[node.State], node.ID, node.Title, node.State, node.Execution)
		if len(node.DependsOn) > 0 {
			fmt.Fprintf(&b, " · after %s", joinInts(node.DependsOn))
		}
		if len(node.WritePaths) > 0 {
			fmt.Fprintf(&b, " · writes %s", strings.Join(node.WritePaths, ", "))
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
		if attempt.Iterations > 0 {
			fmt.Fprintf(&b, " · %d provider iterations", attempt.Iterations)
		}
		if attempt.LastProgressIteration > 0 {
			fmt.Fprintf(&b, " · novel evidence at iteration %d", attempt.LastProgressIteration)
		}
		if attempt.CostAvailable {
			fmt.Fprintf(&b, " · $%.6f", attempt.CostUSD)
		}
		if attempt.Summary != "" {
			fmt.Fprintf(&b, " — %s", bounded(strings.TrimSpace(attempt.Summary), 600))
		}
		b.WriteByte('\n')
		if attempt.Candidate != nil {
			fmt.Fprintf(&b, "    candidate: %s · %s · base %s · verification %s\n", attempt.Candidate.Worktree, attempt.Candidate.Branch, bounded(attempt.Candidate.BaseCommit, 16), attempt.Candidate.VerificationState)
			fmt.Fprintf(&b, "    changed: %s\n", strings.Join(attempt.Candidate.ChangedFiles, ", "))
		}
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
		g.stopActiveLocked(g.now().UTC())
		g.clearPauseLocked()
		g.queueUpdateLocked(0, "", string(OutcomeDone), g.state.Reason)
		return
	}
	if len(blockers) > 0 && !running {
		// Every node is required in OG-1. Continuing independent work after one
		// is materially blocked cannot make the approved goal complete and only
		// spends authority/budget after the truthful terminal state is known.
		g.state.Outcome, g.state.Reason = OutcomeBlocked, strings.Join(blockers, "; ")
		g.stopActiveLocked(g.now().UTC())
		g.clearPauseLocked()
		g.queueUpdateLocked(0, "", string(OutcomeBlocked), g.state.Reason)
		return
	}
	if len(exhausted) > 0 && !running {
		g.state.Outcome, g.state.Reason = OutcomeBudgetExhausted, strings.Join(exhausted, "; ")
		g.stopActiveLocked(g.now().UTC())
		g.clearPauseLocked()
		g.queueUpdateLocked(0, "", string(OutcomeBudgetExhausted), g.state.Reason)
	}
}

func (g *Graph) clearPauseLocked() {
	g.state.PauseRequested, g.state.PauseReached, g.state.PauseReason = false, false, ""
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
		if evidence != nil && evidence.Kind == EvidenceVerification && evidence.Status == "passed" && evidence.MutationGeneration == attempt.MutationGeneration && evidence.WorkspaceToken == token {
			return true
		}
	}
	return false
}

func (g *Graph) hasEquivalentEvidenceLocked(attempt *Attempt, candidate Evidence) bool {
	candidate.Summary = boundedSummary(candidate.Summary)
	for _, id := range attempt.EvidenceIDs {
		evidence := g.evidenceLocked(id)
		if evidence != nil && evidence.Kind == candidate.Kind && evidence.Tool == candidate.Tool && evidence.Command == candidate.Command && evidence.Status == candidate.Status && evidence.Summary == candidate.Summary && evidence.WorkspaceToken == candidate.WorkspaceToken && evidence.MutationGeneration == candidate.MutationGeneration {
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

func (g *Graph) resolveFailuresLocked(attempt *Attempt, tool, risk string) bool {
	resolved := false
	for i := range attempt.Failures {
		failure := &attempt.Failures[i]
		if failure.Resolved {
			continue
		}
		if failure.Tool == tool || (failure.Retryable && failure.Risk != "" && failure.Risk == risk) {
			failure.Resolved = true
			resolved = true
		}
	}
	return resolved
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
	writePaths, _ := writescope.Normalize(spec.WritePaths, spec.Execution == ExecutionIsolatedWrite)
	if node.ID != spec.ID || node.Title != strings.TrimSpace(spec.Title) || node.Execution != normalizeExecution(spec.Execution) || !equalInts(node.DependsOn, spec.DependsOn) || !equalStrings(node.WritePaths, writePaths) || len(node.Acceptance) != len(spec.Acceptance) {
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

func equalStrings(a, b []string) bool {
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

func containsString(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func boundedStrings(values []string, limit, byteLimit int) []string {
	if len(values) > limit {
		values = values[:limit]
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, bounded(value, byteLimit))
	}
	return result
}

func cloneCandidateVerification(values []CandidateVerification) []CandidateVerification {
	if len(values) > maxWriterVerification {
		values = values[:maxWriterVerification]
	}
	result := make([]CandidateVerification, 0, len(values))
	for _, value := range values {
		result = append(result, CandidateVerification{
			Command: bounded(strings.TrimSpace(value.Command), 4096), Status: bounded(strings.TrimSpace(value.Status), 64), StateToken: bounded(strings.TrimSpace(value.StateToken), 512),
		})
	}
	return result
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
	node.WritePaths = append([]string(nil), node.WritePaths...)
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
	if attempt.Candidate != nil {
		candidate := *attempt.Candidate
		candidate.WritePaths = append([]string(nil), attempt.Candidate.WritePaths...)
		candidate.ChangedFiles = append([]string(nil), attempt.Candidate.ChangedFiles...)
		candidate.ScopeViolations = append([]string(nil), attempt.Candidate.ScopeViolations...)
		candidate.Verification = append([]CandidateVerification(nil), attempt.Candidate.Verification...)
		attempt.Candidate = &candidate
	}
	return attempt
}

func cloneSpec(spec Spec) Spec {
	clone := Spec{Goal: spec.Goal, Nodes: make([]NodeSpec, len(spec.Nodes))}
	for i, node := range spec.Nodes {
		clone.Nodes[i] = node
		clone.Nodes[i].DependsOn = append([]int(nil), node.DependsOn...)
		clone.Nodes[i].Acceptance = append([]string(nil), node.Acceptance...)
		clone.Nodes[i].WritePaths = append([]string(nil), node.WritePaths...)
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
