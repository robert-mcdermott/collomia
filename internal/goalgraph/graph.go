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
	// maxAttemptToolEvidence bounds the tool-result evidence one attempt keeps.
	// Every transition rewrites the complete snapshot into the durable session,
	// so unbounded evidence makes persistence cost grow with the square of a
	// node's tool calls. Verification and node results are never pruned; only
	// the oldest ordinary tool results past this bound are, and the attempt
	// records how many so the record stays honest about what it dropped.
	maxAttemptToolEvidence      = 40
	maxWriterVerification       = 16
	defaultMaxGraphIterations   = 96
	defaultMaxGraphTokens       = 1_000_000
	defaultMaxGraphCostUSD      = 5.00
	defaultMaxActiveWallSeconds = 30 * 60
	// The absolute maxima below are sanity bounds, not policy. Policy is the
	// configured envelope; these only reject a value no honest configuration
	// would hold, so a corrupted or hand-edited number cannot be scheduled.
	maxConfigurableGraphIterations   = 10_000
	maxConfigurableGraphTokens       = 100_000_000
	maxConfigurableGraphCostUSD      = 1_000.00
	maxConfigurableActiveWallSeconds = 24 * 60 * 60
)

type Execution string

const (
	ExecutionPrimary       Execution = "primary"
	ExecutionReadOnly      Execution = "read_only"
	ExecutionIsolatedWrite Execution = "isolated_write"
)

type NodeState string

const (
	NodeProposed  NodeState = "proposed"
	NodeReady     NodeState = "ready"
	NodeRunning   NodeState = "running"
	NodeRetryable NodeState = "retryable"
	NodeStale     NodeState = "stale"
	NodeBlocked   NodeState = "blocked"
	// NodeAwaitingReview is the successful terminal state of an isolated-writer
	// node: a verified candidate is retained and reviewed integration is the
	// next step. It is deliberately not "blocked", which would report the
	// feature working as the feature failing.
	NodeAwaitingReview NodeState = "awaiting_review"
	// NodeIntegrated is a candidate whose bytes are now in the parent
	// workspace and whose combined result nothing has verified yet. It is
	// deliberately distinct from done: the child's verification passed against
	// its own isolated tree, and that says nothing about the parent it has now
	// been merged into. Treating a child pass as a combined pass is precisely
	// what OG-4's exit gate forbids.
	NodeIntegrated      NodeState = "integrated"
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
	// OutcomeAwaitingReview means every required node either passed its
	// acceptance gates or produced a verified candidate for reviewed
	// integration. Nothing failed; the graph stops because selecting and
	// integrating a candidate is user authority this milestone does not hold.
	OutcomeAwaitingReview Outcome = "awaiting_review"
	// OutcomeAwaitingVerification means a candidate's bytes are now in the
	// parent workspace and nothing has verified the combined result. It is
	// separate from awaiting_review because the two ask the user for different
	// things, and reporting a published workspace as merely awaiting review
	// would understate what has already changed on disk.
	OutcomeAwaitingVerification Outcome = "awaiting_verification"
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

// GapKind is one typed unmet acceptance gate. The controller's remediation
// lease renews only on evidence capable of changing a recorded gap, so these
// are durable operational state rather than presentation: matching prose to
// decide whether the model made progress made a reworded sentence able to
// silently strand a productive attempt.
type GapKind string

const (
	GapNoToolEvidence      GapKind = "no_tool_evidence"
	GapNoStateToken        GapKind = "no_state_token"
	GapNoOpWrite           GapKind = "no_op_write"
	GapNoFreshVerification GapKind = "no_fresh_verification"
)

func validGapKind(kind GapKind) bool {
	switch kind {
	case GapNoToolEvidence, GapNoStateToken, GapNoOpWrite, GapNoFreshVerification:
		return true
	default:
		return false
	}
}

// gapDescription is the operator- and model-facing sentence for one gap. It is
// derived from the kind, never parsed back out of it.
func gapDescription(kind GapKind) string {
	switch kind {
	case GapNoToolEvidence:
		return "the attempt has no successful bounded tool result"
	case GapNoStateToken:
		return "the combined workspace has no state token"
	case GapNoOpWrite:
		return "the successful write action produced no combined-workspace change"
	case GapNoFreshVerification:
		return "potentially mutating work has no successful recognized verification bound to the current combined workspace"
	default:
		return string(kind)
	}
}

func gapDescriptions(kinds []GapKind) []string {
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, gapDescription(kind))
	}
	return out
}

// legacyGapKinds recovers typed kinds from a snapshot written before the gap
// was typed. It is the only place prose is read as state, and it runs once at
// restore rather than on every tool result.
func legacyGapKinds(gap string) []GapKind {
	var kinds []GapKind
	for _, kind := range []GapKind{GapNoToolEvidence, GapNoStateToken, GapNoOpWrite, GapNoFreshVerification} {
		if strings.Contains(gap, gapDescription(kind)) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

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
	CompletionGapKinds      []GapKind        `json:"completion_gap_kinds,omitempty"`
	CompletionGapIteration  int              `json:"completion_gap_iteration,omitempty"`
	ToolSuccesses           int              `json:"tool_successes,omitempty"`
	EvidencePruned          int              `json:"evidence_pruned,omitempty"`
	PendingAction           *PendingAction   `json:"pending_action,omitempty"`
	Failures                []Failure        `json:"failures,omitempty"`
	EvidenceIDs             []string         `json:"evidence_ids,omitempty"`
	Summary                 string           `json:"summary,omitempty"`
	WorkerID                string           `json:"worker_id,omitempty"`
	UsageRecorded           bool             `json:"usage_recorded,omitempty"`
	Iterations              int              `json:"iterations,omitempty"`
	LastProgressIteration   int              `json:"last_progress_iteration,omitempty"`
	InputTokens             int              `json:"input_tokens,omitempty"`
	CachedTokens            int              `json:"cached_tokens,omitempty"`
	OutputTokens            int              `json:"output_tokens,omitempty"`
	CostUSD                 float64          `json:"cost_usd,omitempty"`
	CostAvailable           bool             `json:"cost_available,omitempty"`
	CostEstimated           bool             `json:"cost_estimated,omitempty"`
	TokenBudget             int              `json:"token_budget,omitempty"`
	CostBudgetUSD           float64          `json:"cost_budget_usd,omitempty"`
	IterationBudget         int              `json:"iteration_budget,omitempty"`
	TimeoutSeconds          int              `json:"timeout_seconds,omitempty"`
	Candidate               *WriterCandidate `json:"candidate,omitempty"`
	// Worktree and Branch are the write-ahead identity of an isolated writer's
	// working tree, recorded durably the moment Git creates it and before the
	// child can change anything in it. A process boundary mid-wave otherwise
	// leaves a real directory on disk that no attempt can be traced to.
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
	// Disposition is what the runtime last observed about that directory, and
	// Reconciled is when it looked. Identity alone answers "where is it"; only
	// an observation answers "is it still there, and does it still hold work".
	Disposition       WorktreeDisposition `json:"worktree_disposition,omitempty"`
	DispositionDetail string              `json:"worktree_disposition_detail,omitempty"`
	Reconciled        time.Time           `json:"worktree_reconciled,omitempty"`
	Started           time.Time           `json:"started"`
	Finished          time.Time           `json:"finished,omitempty"`
}

// WorktreeDisposition is the runtime's observed answer about one retained
// worktree. It is deliberately a small closed vocabulary rather than prose:
// the operator's next action differs for each, and a graph that cannot name
// which case it is in cannot claim to have reconciled anything.
//
// An empty disposition means the tree has never been observed. That is the
// honest state for a directory the runtime created and then stopped watching,
// and it is what a reconcile pass exists to replace.
type WorktreeDisposition string

const (
	// DispositionPresent — the tree is registered with Git and still holds
	// changes. Nothing may reuse it before reviewed integration.
	DispositionPresent WorktreeDisposition = "present"
	// DispositionEmpty — the tree is registered but holds no changes, so
	// discarding it loses nothing.
	DispositionEmpty WorktreeDisposition = "empty"
	// DispositionMissing — the directory is gone. Temporary directories are
	// swept by the operating system, so this is the ordinary fate of a tree
	// left by an interrupted session, not an error.
	DispositionMissing WorktreeDisposition = "missing"
	// DispositionOrphaned — the directory exists but Git no longer registers
	// it as a worktree of this repository, so only a person can decide what
	// its contents are worth.
	DispositionOrphaned WorktreeDisposition = "orphaned"
	// DispositionBaseUnreachable — the tree exists but the commit it was
	// branched from is no longer in the parent repository, so its changes
	// cannot be diffed against the base the claim recorded.
	DispositionBaseUnreachable WorktreeDisposition = "base_unreachable"
	// DispositionDiscarded — a person explicitly asked the runtime to remove
	// this tree and it did. The identity is kept: the record of what was
	// removed, from which node and attempt, is the point.
	DispositionDiscarded WorktreeDisposition = "discarded"
)

func validDisposition(disposition WorktreeDisposition) bool {
	switch disposition {
	case "", DispositionPresent, DispositionEmpty, DispositionMissing,
		DispositionOrphaned, DispositionBaseUnreachable, DispositionDiscarded:
		return true
	}
	return false
}

// OnDisk reports whether this disposition describes a directory that still
// exists. It is the test for whether an operator still has something to
// decide about, and deliberately treats the never-observed empty disposition
// as "assume it is there" — the conservative direction.
func (d WorktreeDisposition) OnDisk() bool {
	return d != DispositionMissing && d != DispositionDiscarded
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

// RetiredNode records a node a revision removed before it completed.
//
// Dropping a node is legitimate replanning — work genuinely turns out to be
// unnecessary — but it is also the one move that can turn a graph the model
// cannot finish into one it can. Without this record the two are
// indistinguishable afterwards: the node is simply gone, and a graph that
// reports done says every required node passed its gates, which about a
// deleted node is false. So the removal is kept, and the terminal state has to
// account for it.
//
// A node removed while already `done` is not retired. Its work happened and
// its evidence stands; only unfinished removals are recorded here.
type RetiredNode struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	// State is what the node was when the revision removed it, which is the
	// difference between abandoning ready work and abandoning a blocker.
	State      NodeState `json:"state"`
	Reason     string    `json:"reason"`
	Generation uint64    `json:"generation"`
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
	RetiredNodes       []RetiredNode   `json:"retired_nodes,omitempty"`
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
	Iterations  int `json:"iterations"`
	InputTokens int `json:"input_tokens"`
	// CachedTokens is the subset of InputTokens the provider served from its
	// prompt cache. It is recorded so the aggregate ceiling can charge new
	// work rather than the same context re-presented on every iteration.
	CachedTokens  int     `json:"cached_tokens,omitempty"`
	OutputTokens  int     `json:"output_tokens"`
	CostUSD       float64 `json:"cost_usd,omitempty"`
	CostAvailable bool    `json:"cost_available,omitempty"`
	CostEstimated bool    `json:"cost_estimated,omitempty"`
}

// BillableTokens is what the aggregate token envelope measures: prompt tokens
// the provider had to read anew, plus everything generated.
//
// An agentic node resends its whole active prompt on every iteration, so
// counting cache reads at full weight makes the ceiling a function of context
// length times iteration count — it grows with the square of a node's tool
// calls while the actual new content grows linearly. A cache read is the same
// context the graph already paid for, and providers price it at a fraction.
// A provider that reports no cache counters charges everything, exactly as
// before.
func (u WorkUsage) BillableTokens() int {
	cached := min(max(u.CachedTokens, 0), max(u.InputTokens, 0))
	return max(u.InputTokens, 0) - cached + max(u.OutputTokens, 0)
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
	// Extensions counts the envelopes a person explicitly granted after
	// exhaustion. It is a record of decisions taken, not a remaining quota.
	Extensions int `json:"extensions,omitempty"`
	// Grant is one envelope: the limits this graph was created with. Each user
	// grant adds exactly this much, so a session configured for a larger
	// envelope extends by that larger amount rather than by a build constant.
	Grant AggregateGrant `json:"grant,omitzero"`
}

// AggregateGrant is the size of a single execution envelope.
type AggregateGrant struct {
	Iterations        int     `json:"iterations,omitempty"`
	Tokens            int     `json:"tokens,omitempty"`
	CostUSD           float64 `json:"cost_usd,omitempty"`
	ActiveWallSeconds int     `json:"active_wall_seconds,omitempty"`
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

// LimitsWithEnvelope returns the default limits with the whole-graph execution
// envelope replaced by the values a session was configured with. A zero field
// keeps that default.
func LimitsWithEnvelope(iterations, tokens int, costUSD float64, activeWallSeconds int) Limits {
	limits := DefaultLimits()
	if iterations > 0 {
		limits.MaxAggregateIterations = min(iterations, maxConfigurableGraphIterations)
	}
	if tokens > 0 {
		limits.MaxAggregateTokens = min(tokens, maxConfigurableGraphTokens)
	}
	if costUSD > 0 && !math.IsNaN(costUSD) && !math.IsInf(costUSD, 0) {
		limits.MaxAggregateCostUSD = min(costUSD, maxConfigurableGraphCostUSD)
	}
	if activeWallSeconds > 0 {
		limits.MaxActiveWallSeconds = min(activeWallSeconds, maxConfigurableActiveWallSeconds)
	}
	return limits
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
		ReadFanout:   ReadFanout{MaxConcurrent: opts.MaxReadConcurrency, MaxStarts: opts.MaxReadStarts, MaxTokens: opts.MaxReadTokens, MaxWallSeconds: opts.MaxReadWallSeconds},
		WriterFanout: WriterFanout{MaxConcurrent: opts.MaxWriterConcurrency, MaxStarts: opts.MaxWriterStarts},
		Accounting:   Accounting{Started: accountingStarted, Primary: opts.InitialPrimary, ActiveSince: now},
		AggregateBudget: AggregateBudget{
			MaxIterations: opts.MaxAggregateIterations, MaxTokens: opts.MaxAggregateTokens,
			MaxCostUSD: opts.MaxAggregateCostUSD, MaxActiveWallSeconds: opts.MaxActiveWallSeconds,
			// One envelope is what this session was configured for, recorded so a
			// later user grant adds that amount rather than a build constant.
			Grant: AggregateGrant{
				Iterations: opts.MaxAggregateIterations, Tokens: opts.MaxAggregateTokens,
				CostUSD: opts.MaxAggregateCostUSD, ActiveWallSeconds: opts.MaxActiveWallSeconds,
			},
		},
		Revisions: []Revision{{Generation: 1, Reason: "initial approved logical graph", Spec: cloneSpec(spec), Time: now}},
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
	if snapshot.AggregateBudget.Grant == (AggregateGrant{}) {
		// A snapshot written before the envelope was recorded separately still
		// has its limits, and no such snapshot can carry a grant history: one
		// envelope is exactly what it has.
		envelopes := snapshot.AggregateBudget.Extensions + 1
		snapshot.AggregateBudget.Grant = AggregateGrant{
			Iterations:        snapshot.AggregateBudget.MaxIterations / envelopes,
			Tokens:            snapshot.AggregateBudget.MaxTokens / envelopes,
			CostUSD:           snapshot.AggregateBudget.MaxCostUSD / float64(envelopes),
			ActiveWallSeconds: snapshot.AggregateBudget.MaxActiveWallSeconds / envelopes,
		}
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
			usage := WorkUsage{Iterations: attempt.Iterations, InputTokens: attempt.InputTokens, CachedTokens: attempt.CachedTokens, OutputTokens: attempt.OutputTokens, CostUSD: attempt.CostUSD, CostAvailable: attempt.CostAvailable, CostEstimated: attempt.CostEstimated}
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
	candidateNodes := make(map[int]bool)
	for _, attempt := range snapshot.Attempts {
		if attempt.State == AttemptCandidate {
			candidateNodes[attempt.NodeID] = true
		}
	}
	for i := range snapshot.Nodes {
		// Snapshots written before awaiting_review existed recorded a retained
		// verified candidate as a blocked node. The candidate is unchanged; only
		// the state's honesty about it is.
		if snapshot.Nodes[i].State == NodeBlocked && candidateNodes[snapshot.Nodes[i].ID] {
			snapshot.Nodes[i].State = NodeAwaitingReview
		}
	}
	for i := range snapshot.Attempts {
		// Snapshots written before the gap was typed carry only the rendered
		// sentence. Recover the kinds once here so the running controller never
		// has to interpret prose.
		attempt := &snapshot.Attempts[i]
		if attempt.CompletionGap != "" && len(attempt.CompletionGapKinds) == 0 {
			attempt.CompletionGapKinds = legacyGapKinds(attempt.CompletionGap)
			if len(attempt.CompletionGapKinds) == 0 {
				// An unrecognizable legacy sentence cannot bound remediation
				// honestly, so the gap is cleared rather than left unenforceable.
				attempt.CompletionGap, attempt.CompletionGapIteration = "", 0
			}
		}
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
	// A configured envelope is the user's decision; only an implausible value
	// is refused. Zero means "use the default", not "unbounded".
	if opts.MaxAggregateIterations <= 0 {
		opts.MaxAggregateIterations = defaultMaxGraphIterations
	}
	opts.MaxAggregateIterations = min(opts.MaxAggregateIterations, maxConfigurableGraphIterations)
	if opts.MaxAggregateTokens <= 0 {
		opts.MaxAggregateTokens = defaultMaxGraphTokens
	}
	opts.MaxAggregateTokens = min(opts.MaxAggregateTokens, maxConfigurableGraphTokens)
	if opts.MaxAggregateCostUSD <= 0 || math.IsNaN(opts.MaxAggregateCostUSD) || math.IsInf(opts.MaxAggregateCostUSD, 0) {
		opts.MaxAggregateCostUSD = defaultMaxGraphCostUSD
	}
	opts.MaxAggregateCostUSD = min(opts.MaxAggregateCostUSD, maxConfigurableGraphCostUSD)
	if opts.MaxActiveWallSeconds <= 0 {
		opts.MaxActiveWallSeconds = defaultMaxActiveWallSeconds
	}
	opts.MaxActiveWallSeconds = min(opts.MaxActiveWallSeconds, maxConfigurableActiveWallSeconds)
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
	// A retirement is the record of work the approved plan lost. A snapshot
	// claiming one for a node the graph still contains, or one that names a
	// node with no identity or no reason, is describing something that did not
	// happen — and this record exists precisely so a terminal state cannot
	// overstate what passed.
	present := make(map[int]bool, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		present[node.ID] = true
	}
	for _, retired := range snapshot.RetiredNodes {
		if retired.ID <= 0 || strings.TrimSpace(retired.Title) == "" || strings.TrimSpace(retired.Reason) == "" {
			return errors.New("goal graph snapshot has an unattributable retired node")
		}
		if present[retired.ID] {
			return fmt.Errorf("goal graph snapshot retires node %d while still containing it", retired.ID)
		}
		if retired.State == NodeDone {
			return fmt.Errorf("goal graph snapshot retires completed node %d; finished work is not a retirement", retired.ID)
		}
		if retired.Generation == 0 || retired.Time.IsZero() {
			return fmt.Errorf("goal graph snapshot retires node %d without saying when", retired.ID)
		}
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
	// The stored envelope is the user's to choose, so validation is an
	// integrity check rather than a policy one: reject values no honest
	// configuration or grant could have produced, and honour the rest.
	if budget.Extensions < 0 || budget.Extensions > maxConfigurableGraphIterations {
		return errors.New("goal graph snapshot has an implausible budget extension count")
	}
	envelopes := budget.Extensions + 1
	if budget.MaxIterations <= 0 || budget.MaxIterations > maxConfigurableGraphIterations*envelopes || budget.MaxTokens <= 0 || budget.MaxTokens > maxConfigurableGraphTokens*envelopes || budget.MaxCostUSD <= 0 || budget.MaxCostUSD > maxConfigurableGraphCostUSD*float64(envelopes) || math.IsNaN(budget.MaxCostUSD) || math.IsInf(budget.MaxCostUSD, 0) || budget.MaxActiveWallSeconds <= 0 || budget.MaxActiveWallSeconds > maxConfigurableActiveWallSeconds*envelopes || snapshot.Accounting.ActiveElapsed < 0 {
		return errors.New("goal graph snapshot has invalid aggregate budget or active time")
	}
	grant := budget.Grant
	if grant.Iterations < 0 || grant.Tokens < 0 || grant.ActiveWallSeconds < 0 || grant.CostUSD < 0 || math.IsNaN(grant.CostUSD) || math.IsInf(grant.CostUSD, 0) ||
		grant.Iterations > budget.MaxIterations || grant.Tokens > budget.MaxTokens || grant.ActiveWallSeconds > budget.MaxActiveWallSeconds || grant.CostUSD > budget.MaxCostUSD {
		return errors.New("goal graph snapshot has an invalid single-envelope grant")
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
		if !validAttemptState(attempt.State) || attempt.Number <= 0 || attempt.Number > snapshot.MaxAttemptsPerNode || attempt.GraphGeneration == 0 || attempt.GraphGeneration > snapshot.Generation || attempt.Iterations < 0 || attempt.LastProgressIteration < 0 || attempt.LastProgressIteration > attempt.Iterations || attempt.CompletionGapIteration < 0 || attempt.CompletionGapIteration > attempt.Iterations || len(attempt.CompletionGap) > 4<<10 || (attempt.CompletionGap == "" && attempt.CompletionGapIteration != 0) || attempt.InputTokens < 0 || attempt.CachedTokens < 0 || attempt.CachedTokens > attempt.InputTokens || attempt.OutputTokens < 0 || attempt.CostUSD < 0 || attempt.TokenBudget < 0 || attempt.CostBudgetUSD < 0 || math.IsNaN(attempt.CostBudgetUSD) || math.IsInf(attempt.CostBudgetUSD, 0) || attempt.IterationBudget < 0 || attempt.TimeoutSeconds < 0 || attempt.EvidencePruned < 0 || len(attempt.Worktree) > 4096 || len(attempt.Branch) > 512 || (attempt.Worktree == "") != (attempt.Branch == "") {
			return fmt.Errorf("goal graph snapshot has invalid attempt %q state, number, or generation", attempt.ID)
		}
		if (len(attempt.CompletionGapKinds) == 0) != (attempt.CompletionGap == "") {
			return fmt.Errorf("goal graph snapshot attempt %q has a completion gap without typed kinds", attempt.ID)
		}
		if !validDisposition(attempt.Disposition) || len(attempt.DispositionDetail) > 512 {
			return fmt.Errorf("goal graph snapshot attempt %q has an unknown retained-worktree disposition", attempt.ID)
		}
		// A disposition without a tree to describe, or without the time it was
		// observed, is a claim about nothing. Reject it rather than render it.
		if attempt.Disposition != "" && (attempt.Worktree == "" && attempt.Candidate == nil || attempt.Reconciled.IsZero()) {
			return fmt.Errorf("goal graph snapshot attempt %q records a disposition without an observed worktree", attempt.ID)
		}
		if attempt.Disposition == "" && (attempt.DispositionDetail != "" || !attempt.Reconciled.IsZero()) {
			return fmt.Errorf("goal graph snapshot attempt %q records a reconciliation without a disposition", attempt.ID)
		}
		for _, kind := range attempt.CompletionGapKinds {
			if !validGapKind(kind) {
				return fmt.Errorf("goal graph snapshot attempt %q has unknown completion gap kind %q", attempt.ID, kind)
			}
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
		if attempt.State == AttemptCandidate && nodes[attempt.NodeID].State != NodeAwaitingReview {
			return fmt.Errorf("candidate attempt %q is not attached to an awaiting-review node", attempt.ID)
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
	case NodeProposed, NodeReady, NodeRunning, NodeRetryable, NodeStale, NodeBlocked, NodeAwaitingReview, NodeIntegrated, NodeCancelled, NodeBudgetExhausted, NodeDone:
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
	case "", OutcomeDone, OutcomeBlocked, OutcomeAwaitingReview, OutcomeAwaitingVerification, OutcomeCancelled, OutcomeBudgetExhausted:
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
	CachedTokens   int
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
	CachedTokens         int
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
		Iterations: result.Iterations, InputTokens: result.InputTokens, CachedTokens: result.CachedTokens, OutputTokens: result.OutputTokens,
		CostUSD: result.CostUSD, CostAvailable: result.CostAvailable, CostEstimated: result.CostEstimated,
	}
	if !validWorkUsage(usage) {
		return errors.New("goal graph read result has invalid usage")
	}
	workerID := bounded(result.WorkerID, 256)
	if attempt.UsageRecorded {
		recorded := WorkUsage{
			Iterations: attempt.Iterations, InputTokens: attempt.InputTokens, CachedTokens: attempt.CachedTokens, OutputTokens: attempt.OutputTokens,
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
	g.state.ReadFanout.UsedTokens += usage.BillableTokens()
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
		Iterations: result.Iterations, InputTokens: result.InputTokens, CachedTokens: result.CachedTokens, OutputTokens: result.OutputTokens,
		CostUSD: result.CostUSD, CostAvailable: result.CostAvailable, CostEstimated: result.CostEstimated,
	}
	if !validWorkUsage(usage) {
		return errors.New("goal graph writer result has invalid usage")
	}
	workerID := bounded(result.WorkerID, 256)
	if attempt.UsageRecorded {
		recorded := WorkUsage{
			Iterations: attempt.Iterations, InputTokens: attempt.InputTokens, CachedTokens: attempt.CachedTokens, OutputTokens: attempt.OutputTokens,
			CostUSD: attempt.CostUSD, CostAvailable: attempt.CostAvailable, CostEstimated: attempt.CostEstimated,
		}
		if recorded != usage || attempt.WorkerID != workerID {
			return fmt.Errorf("goal graph writer attempt %q already has different usage", attempt.ID)
		}
		return nil
	}
	attempt.WorkerID, attempt.UsageRecorded = workerID, true
	attempt.Iterations, attempt.InputTokens, attempt.CachedTokens, attempt.OutputTokens = usage.Iterations, usage.InputTokens, usage.CachedTokens, usage.OutputTokens
	attempt.CostUSD, attempt.CostAvailable, attempt.CostEstimated = usage.CostUSD, usage.CostAvailable, usage.CostEstimated
	addWorkUsage(&g.state.Accounting.AutomaticWriters, usage)
	return nil
}

// RecordWriterWorktree durably binds a newly created isolated worktree to the
// attempt that caused it, before the child has run. It grants nothing and
// changes no scheduling state; it exists so that a session that stops mid-wave
// still leaves every directory attributable to a plan node and attempt.
func (g *Graph) RecordWriterWorktree(ctx context.Context, attemptID, worktree, branch string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	attempt := g.attemptLocked(attemptID)
	if attempt == nil || attempt.State != AttemptRunning {
		return fmt.Errorf("goal graph writer attempt %q is not running", attemptID)
	}
	node := g.nodeLocked(attempt.NodeID)
	if node == nil || node.Execution != ExecutionIsolatedWrite {
		return fmt.Errorf("goal graph attempt %q is not an isolated_write node", attemptID)
	}
	if strings.TrimSpace(worktree) == "" || strings.TrimSpace(branch) == "" {
		return errors.New("goal graph writer worktree needs a path and a branch")
	}
	attempt.Worktree, attempt.Branch = bounded(worktree, 4096), bounded(branch, 512)
	return g.persistLocked(ctx, true)
}

// CandidateIntegration is what the application publishes and the graph
// records. It carries no authority: permission, freshness, and the durable
// pre-integration checkpoint are the application's to enforce before calling,
// and the graph's job is to say what the publication means for the plan.
type CandidateIntegration struct {
	AttemptID string
	// Files is every path published. A candidate is always published whole:
	// the child's verification passed against its entire tree, so publishing a
	// subset would put bytes into the parent that no verification ever covered.
	Files []string
	// ParentWorkspaceToken is the machine-observed state of the parent *after*
	// publication. It becomes the token every later verification is judged
	// against.
	ParentWorkspaceToken string
	// AlreadyIdentical names candidate files whose bytes already matched the
	// parent, so publishing them was a no-op. Recording them separately is
	// what lets the graph insist the whole candidate was accounted for without
	// mistaking an unchanged file for an excluded one.
	AlreadyIdentical []string
	// CheckpointID names the durable record that can undo this publication.
	CheckpointID string
}

// PrepareCandidateIntegration reports the candidate a node would publish, and
// refuses when the node is not in a state where publishing means anything. It
// mutates nothing, so a caller can use it to decide whether to ask the user at
// all.
func (g *Graph) PrepareCandidateIntegration(nodeID int) (WriterCandidate, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	node := g.nodeLocked(nodeID)
	if node == nil {
		return WriterCandidate{}, "", fmt.Errorf("goal graph has no node %d", nodeID)
	}
	if node.State == NodeIntegrated {
		return WriterCandidate{}, "", fmt.Errorf("goal graph node %d is already integrated and awaiting combined verification", nodeID)
	}
	if node.State != NodeAwaitingReview {
		return WriterCandidate{}, "", fmt.Errorf("goal graph node %d is %s, not awaiting review; only a verified retained candidate can be integrated", nodeID, node.State)
	}
	for i := len(node.AttemptIDs) - 1; i >= 0; i-- {
		attempt := g.attemptLocked(node.AttemptIDs[i])
		if attempt == nil || attempt.State != AttemptCandidate || attempt.Candidate == nil {
			continue
		}
		// The graph only ever reached awaiting_review through a passing
		// verification, but say so explicitly rather than relying on that
		// history: this is the last point before the parent changes.
		if attempt.Candidate.VerificationState != "passed" {
			return WriterCandidate{}, "", fmt.Errorf("goal graph node %d holds a candidate whose verification is %q, not passed", nodeID, attempt.Candidate.VerificationState)
		}
		if len(attempt.Candidate.ScopeViolations) > 0 {
			return WriterCandidate{}, "", fmt.Errorf("goal graph node %d holds a candidate that wrote outside its declared scope (%s)", nodeID, strings.Join(attempt.Candidate.ScopeViolations, ", "))
		}
		return *attempt.Candidate, attempt.ID, nil
	}
	return WriterCandidate{}, "", fmt.Errorf("goal graph node %d has no retained candidate attempt", nodeID)
}

// IntegrateCandidate records that a node's whole verified candidate is now in
// the parent workspace. It deliberately does not mark the node done. The
// child's verification passed against its own isolated tree and says nothing
// about the parent it has just been merged into, so the node moves to
// `integrated` and the graph reports that the combined result is unverified.
//
// Publication changes the parent, so every earlier accepted node's evidence is
// invalidated here for the same reason a manual edit invalidates it: the
// workspace those nodes were judged against no longer exists.
func (g *Graph) IntegrateCandidate(ctx context.Context, nodeID int, integration CandidateIntegration) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	node := g.nodeLocked(nodeID)
	if node == nil {
		return fmt.Errorf("goal graph has no node %d", nodeID)
	}
	if node.State != NodeAwaitingReview {
		return fmt.Errorf("goal graph node %d is %s, not awaiting review", nodeID, node.State)
	}
	attempt := g.attemptLocked(integration.AttemptID)
	if attempt == nil || attempt.NodeID != nodeID || attempt.State != AttemptCandidate || attempt.Candidate == nil {
		return fmt.Errorf("goal graph attempt %q is not node %d's retained candidate", integration.AttemptID, nodeID)
	}
	if len(integration.Files) == 0 {
		return errors.New("a candidate integration must name the files it published")
	}
	if strings.TrimSpace(integration.ParentWorkspaceToken) == "" {
		return errors.New("a candidate integration must record the parent workspace state it produced")
	}
	// Publishing a subset would leave bytes in the parent that the child's
	// verification never covered, so the recorded set must be the whole
	// candidate.
	accounted := sortedCopy(append(append([]string(nil), integration.Files...), integration.AlreadyIdentical...))
	if !equalStrings(accounted, sortedCopy(attempt.Candidate.ChangedFiles)) {
		return fmt.Errorf("candidate integration accounted for %d of the candidate's %d changed file(s); a candidate is published whole or not at all", len(accounted), len(attempt.Candidate.ChangedFiles))
	}

	now := g.now().UTC()
	g.state.MutationGeneration++
	attempt.MutationGeneration = g.state.MutationGeneration
	attempt.State, attempt.Finished = AttemptAccepted, now
	// Prior accepted work was judged against a workspace that no longer
	// exists. Staling it is the same treatment an external edit receives.
	g.invalidateAllDoneLocked(fmt.Sprintf("node %d's candidate was integrated into the combined workspace", nodeID))
	g.state.WorkspaceToken = integration.ParentWorkspaceToken
	reason := fmt.Sprintf("candidate %s was integrated into the parent workspace (%d file(s)); the combined result is unverified, and this node cannot complete until fresh combined-workspace verification passes",
		attempt.Candidate.WorkerID, len(integration.Files))
	if integration.CheckpointID != "" {
		reason += fmt.Sprintf("; checkpoint %s can restore the state from before it", integration.CheckpointID)
	}
	node.State, node.ActiveAttemptID, node.Reason = NodeIntegrated, "", reason
	g.addEvidenceLocked(attempt, Evidence{
		Kind: EvidenceDelegateWrite, Tool: "integrate_candidate", Status: "integrated",
		Summary: boundedSummary(reason), WorkspaceToken: integration.ParentWorkspaceToken,
		MutationGeneration: g.state.MutationGeneration, Finished: now,
	})
	g.queueUpdateLocked(node.ID, attempt.ID, string(NodeIntegrated), reason)
	g.reduceOutcomeLocked()
	return g.persistLocked(ctx, true)
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// CombinedVerification is machine-observed evidence about the parent
// workspace after one or more candidates were integrated into it. It is the
// only thing that can complete an integrated node: a child's pass was about
// its own isolated tree, and the combined result is a different question.
type CombinedVerification struct {
	// Commands is what actually ran against the combined workspace.
	Commands []CandidateVerification
	// WorkspaceToken is the machine-observed parent state the commands ran
	// against. It must still be the graph's current state, or the evidence
	// describes a workspace that has since moved.
	WorkspaceToken string
	// Waiver is a user-authored reason accepted in place of automated
	// evidence. It is recorded as explicitly user-authored rather than
	// machine-observed, because a person's judgement and a passing test are
	// different kinds of claim and the record must not blur them.
	Waiver string
}

// AcceptIntegratedNodes completes every node whose candidate is in the parent
// workspace, given fresh evidence about that combined workspace. It refuses
// stale evidence, refuses evidence that did not pass, and refuses to invent a
// waiver: a node that cannot be verified stays integrated and unfinished until
// a person says why that is acceptable.
func (g *Graph) AcceptIntegratedNodes(ctx context.Context, verification CombinedVerification) ([]int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var pending []*Node
	for i := range g.state.Nodes {
		if g.state.Nodes[i].State == NodeIntegrated {
			pending = append(pending, &g.state.Nodes[i])
		}
	}
	if len(pending) == 0 {
		return nil, errors.New("no integrated node is waiting for combined-workspace verification")
	}
	token := strings.TrimSpace(verification.WorkspaceToken)
	if token == "" {
		return nil, errors.New("combined verification must name the workspace state it observed")
	}
	// Deliberately no comparison against the token recorded at integration.
	// Whether this evidence describes the workspace as it is *now* is a
	// filesystem question, and the graph never observes the filesystem — the
	// application observes it before and after running the commands and passes
	// the settled token. Requiring equality with the integration-time token
	// would instead freeze the workspace: any edit the user made after
	// integrating would make every node permanently unfinishable.
	waiver := strings.TrimSpace(verification.Waiver)
	if waiver == "" {
		if len(verification.Commands) == 0 {
			return nil, errors.New("combined verification produced no evidence; run the repository's checks against the workspace, or record an explicit waiver saying why none applies")
		}
		for _, command := range verification.Commands {
			if command.Status != "passed" {
				return nil, fmt.Errorf("combined verification command %q reported %q; an integrated node cannot complete on failing evidence", command.Command, command.Status)
			}
			if command.StateToken != token {
				return nil, fmt.Errorf("combined verification command %q ran against a different workspace state than the one being accepted", command.Command)
			}
		}
	}

	now := g.now().UTC()
	summary := fmt.Sprintf("combined-workspace verification passed against %d command(s)", len(verification.Commands))
	status := "passed"
	if waiver != "" {
		// Say what this is every time it is rendered. A waiver that reads like
		// a test result is the one way this record could mislead.
		summary = "user-authored waiver, not machine-observed verification: " + boundedReason(waiver)
		status = "waived"
	}
	// This graph is terminal at awaiting_verification, and accepting nodes is
	// exactly the event that can end that. Clear the outcome so it is derived
	// again from the states below rather than left at the one being resolved.
	g.state.Outcome, g.state.Reason = "", ""
	g.state.WorkspaceToken = token
	accepted := make([]int, 0, len(pending))
	for _, node := range pending {
		attemptID := ""
		for i := len(node.AttemptIDs) - 1; i >= 0; i-- {
			if attempt := g.attemptLocked(node.AttemptIDs[i]); attempt != nil && attempt.State == AttemptAccepted {
				attemptID = attempt.ID
				g.addEvidenceLocked(attempt, Evidence{
					Kind: EvidenceVerification, Tool: "verify_combined_workspace", Status: status,
					Summary: boundedSummary(summary), WorkspaceToken: token,
					MutationGeneration: g.state.MutationGeneration, Finished: now,
				})
				break
			}
		}
		node.State, node.AcceptedAttemptID, node.ActiveAttemptID = NodeDone, attemptID, ""
		node.Reason = summary
		g.queueUpdateLocked(node.ID, attemptID, string(NodeDone), summary)
		accepted = append(accepted, node.ID)
	}
	g.refreshReadyLocked("combined-workspace verification accepted an integrated node")
	g.reduceOutcomeLocked()
	if err := g.persistLocked(ctx, true); err != nil {
		return nil, err
	}
	return accepted, nil
}

// IntegratedNodes lists nodes whose candidates are in the parent workspace and
// whose combined result nothing has verified.
func (g *Graph) IntegratedNodes() []Node {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Node
	for _, node := range g.state.Nodes {
		if node.State == NodeIntegrated {
			out = append(out, cloneNode(node))
		}
	}
	return out
}

// RetainedWorktree is one directory the graph still points at, with whatever
// the runtime last observed about it. It is a read-only projection: reviewed
// integration, selection, and reuse remain out of scope here.
type RetainedWorktree struct {
	AttemptID    string
	NodeID       int
	NodeTitle    string
	NodeState    NodeState
	Worktree     string
	Branch       string
	BaseCommit   string
	ChangedFiles int
	Verification string
	Disposition  WorktreeDisposition
	Detail       string
	Reconciled   time.Time
}

// WorktreeObservation is what the runtime saw when it looked at one retained
// directory. The graph never performs the observation itself: filesystem and
// Git access belong to the application layer, and the graph owns only the
// durable record of the answer.
type WorktreeObservation struct {
	AttemptID   string
	Disposition WorktreeDisposition
	Detail      string
}

// RetainedWorktrees lists every tree the graph still points at, in stable
// attempt order. It is available on a terminal graph by design: awaiting
// review, cancelled, and budget-exhausted are exactly the states in which
// directories are left on disk and a person has to decide about them.
func (g *Graph) RetainedWorktrees() []RetainedWorktree {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.retainedWorktreesLocked()
}

func (g *Graph) retainedWorktreesLocked() []RetainedWorktree {
	var trees []RetainedWorktree
	for _, attempt := range g.state.Attempts {
		worktree, branch := attempt.Worktree, attempt.Branch
		if worktree == "" && attempt.Candidate != nil {
			worktree, branch = attempt.Candidate.Worktree, attempt.Candidate.Branch
		}
		if worktree == "" {
			continue
		}
		tree := RetainedWorktree{
			AttemptID: attempt.ID, NodeID: attempt.NodeID, Worktree: worktree, Branch: branch,
			BaseCommit: attempt.BaseCommit, Disposition: attempt.Disposition,
			Detail: attempt.DispositionDetail, Reconciled: attempt.Reconciled,
		}
		if node := g.nodeLocked(attempt.NodeID); node != nil {
			tree.NodeState, tree.NodeTitle = node.State, node.Title
		}
		if attempt.Candidate != nil {
			tree.BaseCommit = attempt.Candidate.BaseCommit
			tree.ChangedFiles = len(attempt.Candidate.ChangedFiles)
			tree.Verification = attempt.Candidate.VerificationState
			if tree.Verification == "" {
				tree.Verification = "unverified"
			}
		}
		trees = append(trees, tree)
	}
	return trees
}

// RecordWorktreeDispositions durably stores what the runtime observed about
// the retained trees. It grants nothing, unblocks nothing, and reopens no
// attempt: a graph that has stopped stays stopped, and this only replaces
// "there is a directory somewhere" with a specific answer about each one.
func (g *Graph) RecordWorktreeDispositions(ctx context.Context, observations []WorktreeObservation) error {
	if len(observations) == 0 {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now().UTC()
	changed := false
	for _, observation := range observations {
		if !validDisposition(observation.Disposition) || observation.Disposition == "" {
			return fmt.Errorf("unknown retained-worktree disposition %q", observation.Disposition)
		}
		attempt := g.attemptLocked(observation.AttemptID)
		if attempt == nil {
			return fmt.Errorf("goal graph attempt %q is unknown", observation.AttemptID)
		}
		if attempt.Worktree == "" && attempt.Candidate == nil {
			return fmt.Errorf("goal graph attempt %q has no retained worktree to reconcile", observation.AttemptID)
		}
		attempt.Disposition = observation.Disposition
		attempt.DispositionDetail = bounded(strings.TrimSpace(observation.Detail), 512)
		attempt.Reconciled = now
		changed = true
	}
	if !changed {
		return nil
	}
	return g.persistLocked(ctx, true)
}

// UnreconciledWorktrees names the retained trees that have never been
// observed. Releasing the graph while any of these exist would discard the
// only record of a real directory, so archiving asks this first.
func (g *Graph) UnreconciledWorktrees() []RetainedWorktree {
	g.mu.Lock()
	defer g.mu.Unlock()
	var pending []RetainedWorktree
	for _, tree := range g.retainedWorktreesLocked() {
		if tree.Disposition == "" {
			pending = append(pending, tree)
		}
	}
	return pending
}

// writerFinishable reports whether this attempt may still record the facts of
// the worktree it left behind. A terminal graph accepts only the attempt states
// its own terminal transition produced, so a late or duplicate result can add
// identity but never revive a finished wave.
func writerFinishable(state AttemptState, retainOnly bool) bool {
	if state == AttemptRunning {
		return true
	}
	return retainOnly && (state == AttemptBudgetExhausted || state == AttemptCancelled)
}

func retainedWriterSummary(outcome Outcome) string {
	if outcome == OutcomeCancelled {
		return "isolated writer stopped when the wave was cancelled; its retained worktree is recorded for inspection"
	}
	return "isolated writer finished after the aggregate budget was exhausted; its retained worktree is recorded for inspection"
}

// FinishWriter validates a retained child candidate against the immutable
// claim. Even a fully verified candidate stops at review: OG-3A never marks
// the node done, selects a winner, or mutates the parent workspace.
func (g *Graph) FinishWriter(ctx context.Context, result WriterResult) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	// A sibling's usage may already have exhausted the aggregate budget, or the
	// user may have cancelled the turn, while this writer was in flight. Its
	// worktree exists either way, so the facts needed to find and review it are
	// still recorded; only scheduling state stays terminal.
	retainOnly := g.state.Outcome == OutcomeBudgetExhausted || g.state.Outcome == OutcomeCancelled
	if g.state.Outcome != "" && !retainOnly {
		return ErrGraphTerminal
	}
	attempt := g.attemptLocked(result.AttemptID)
	if attempt == nil || !writerFinishable(attempt.State, retainOnly) {
		return fmt.Errorf("goal graph writer attempt %q is not running", result.AttemptID)
	}
	node := g.nodeLocked(attempt.NodeID)
	if node == nil || node.Execution != ExecutionIsolatedWrite {
		return fmt.Errorf("goal graph attempt %q is not an isolated_write node", result.AttemptID)
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
	// A child that changed nothing has its worktree removed, so the write-ahead
	// record is cleared rather than left pointing at a directory that is gone.
	if candidate.Worktree != "" && candidate.Branch != "" {
		attempt.Worktree, attempt.Branch = candidate.Worktree, candidate.Branch
	} else if len(result.ChangedFiles) == 0 {
		attempt.Worktree, attempt.Branch = "", ""
	}
	// Identity is recorded before accounting. A graph that cannot say where a
	// retained worktree is cannot honour its promise to retain it, and that must
	// not depend on whether the child's usage counters were well formed.
	if err := g.recordWriterUsageLocked(attempt, result); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
	}
	if retainOnly {
		if attempt.Summary == "" {
			attempt.Summary = boundedSummary(retainedWriterSummary(g.state.Outcome))
		}
		if err := g.persistLocked(ctx, true); err != nil {
			return err
		}
		if g.state.Outcome == OutcomeBudgetExhausted {
			return ErrAggregateBudget
		}
		return ErrGraphTerminal
	}
	// The aggregate budget is checked only once this attempt's retained facts
	// are durable. A wave that crossed the limit still leaves real worktrees on
	// disk, and a graph that forgets where they are cannot honour its promise
	// to retain them for inspection.
	if err := g.enforceAggregateBudgetLocked(now, false); err != nil {
		if persistErr := g.persistLocked(ctx, true); persistErr != nil {
			return persistErr
		}
		return err
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
		node.State, node.ActiveAttemptID = NodeAwaitingReview, ""
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
		// The child's raw error is something like "command failed: exit status
		// 1", which names neither the command nor the fact that it was the
		// candidate's own verification that rejected it. The graph holds both,
		// so it leads with the diagnosis and keeps the raw error as detail
		// rather than letting the raw error stand alone.
		detail = candidateVerificationFailureDetail(candidate, detail)
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
		// grant one fresh lease on resume; the whole-graph envelope remains the
		// outer bound.
		if attempt.LastProgressIteration == 0 && attempt.ToolSuccesses > 0 && attempt.Iterations > 0 {
			attempt.LastProgressIteration = attempt.Iterations
		}
		attemptUsage := WorkUsage{
			Iterations: attempt.Iterations, InputTokens: attempt.InputTokens, CachedTokens: attempt.CachedTokens, OutputTokens: attempt.OutputTokens,
			CostUSD: attempt.CostUSD, CostAvailable: attempt.CostAvailable, CostEstimated: attempt.CostEstimated,
		}
		addWorkUsage(&attemptUsage, usage)
		attempt.Iterations, attempt.InputTokens, attempt.CachedTokens, attempt.OutputTokens = attemptUsage.Iterations, attemptUsage.InputTokens, attemptUsage.CachedTokens, attemptUsage.OutputTokens
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
	return usage.Iterations >= 0 && usage.InputTokens >= 0 && usage.OutputTokens >= 0 && usage.CachedTokens >= 0 && usage.CachedTokens <= usage.InputTokens && usage.CostUSD >= 0 && !math.IsNaN(usage.CostUSD) && !math.IsInf(usage.CostUSD, 0)
}

func addWorkUsage(total *WorkUsage, addition WorkUsage) {
	if total == nil {
		return
	}
	totalHasPricedWork := total.InputTokens+total.OutputTokens > 0 || total.CostUSD > 0
	additionHasPricedWork := addition.InputTokens+addition.OutputTokens > 0 || addition.CostUSD > 0
	total.Iterations += addition.Iterations
	total.InputTokens += addition.InputTokens
	total.CachedTokens += addition.CachedTokens
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
	if attempt == nil || len(attempt.CompletionGapKinds) == 0 {
		return false
	}
	// A machine-observed repository change is concrete repair progress even
	// before the next verifier runs. This gives the model enough bounded room to
	// add or fix a focused test after a useful verification failure.
	if completionGapWorkspaceAdvanced(pending, result) {
		return true
	}
	token := strings.TrimSpace(result.WorkspaceToken)
	for _, kind := range attempt.CompletionGapKinds {
		switch kind {
		case GapNoToolEvidence:
			return true
		case GapNoStateToken:
			if token != "" {
				return true
			}
		case GapNoOpWrite:
			if token != "" && token != attempt.BaseWorkspaceToken {
				return true
			}
		case GapNoFreshVerification:
			if result.Verification && token != "" {
				return true
			}
		}
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
			if len(attempt.CompletionGapKinds) > 0 && (!g.hasEquivalentVerificationFailureLocked(attempt, evidence) || completionGapWorkspaceAdvanced(pending, result)) {
				attempt.LastProgressIteration = attempt.Iterations
				attempt.CompletionGapIteration = attempt.Iterations
			}
			g.addEvidenceLocked(attempt, evidence)
		} else if len(attempt.CompletionGapKinds) > 0 && completionGapWorkspaceAdvanced(pending, result) {
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
		if resolvedFailure || completionGapAdvanced(attempt, pending, result) || (len(attempt.CompletionGapKinds) == 0 && !g.hasEquivalentEvidenceLocked(attempt, evidence)) {
			attempt.LastProgressIteration = attempt.Iterations
			if len(attempt.CompletionGapKinds) > 0 {
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

	var gaps []GapKind
	if attempt.ToolSuccesses == 0 {
		gaps = append(gaps, GapNoToolEvidence)
	}
	if attempt.MayHaveMutated {
		if workspaceToken == "" {
			gaps = append(gaps, GapNoStateToken)
		} else if attempt.HasWorkspaceWrite && attempt.BaseWorkspaceToken == workspaceToken {
			gaps = append(gaps, GapNoOpWrite)
		} else if !g.hasFreshVerificationLocked(attempt, workspaceToken) {
			gaps = append(gaps, GapNoFreshVerification)
		}
	}
	if len(gaps) > 0 {
		issues := gapDescriptions(gaps)
		gap := strings.Join(issues, "; ")
		if !equalGapKinds(attempt.CompletionGapKinds, gaps) {
			attempt.CompletionGapKinds = append([]GapKind(nil), gaps...)
			attempt.CompletionGapIteration = attempt.Iterations
		}
		attempt.CompletionGap = gap
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
	attempt.CompletionGap, attempt.CompletionGapKinds, attempt.CompletionGapIteration = "", nil, 0
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
	// Account for what the revision removed. A node the proposal simply omits
	// disappears from the graph, and the completion reducer below counts only
	// the nodes that remain — so without this record a graph could report that
	// every required node passed its gates while one of them had been deleted
	// precisely because it could not pass them.
	retained := make(map[int]bool, len(spec.Nodes))
	for _, proposed := range spec.Nodes {
		retained[proposed.ID] = true
	}
	dropped := make([]int, 0, len(old))
	for id := range old {
		if !retained[id] {
			dropped = append(dropped, id)
		}
	}
	sort.Ints(dropped)
	for _, id := range dropped {
		prior := old[id]
		// Removing finished work is not a retirement. The node ran, its
		// evidence stands, and the plan simply no longer lists it.
		if prior.State == NodeDone {
			continue
		}
		g.state.RetiredNodes = append(g.state.RetiredNodes, RetiredNode{
			ID: prior.ID, Title: prior.Title, State: prior.State,
			Reason: reason, Generation: g.state.Generation, Time: now,
		})
		g.queueUpdateLocked(prior.ID, "", "retired", fmt.Sprintf("removed by graph revision while %s: %s", prior.State, reason))
	}
	g.state.Revisions = append(g.state.Revisions, Revision{Generation: g.state.Generation, Reason: reason, Spec: cloneSpec(spec), Time: now})
	g.refreshReadyLocked("revised dependencies accepted")
	g.queueUpdateLocked(0, "", "revised", reason)
	return g.persistLocked(ctx, true)
}

// SupersededVerification names one done node whose verification evidence is
// bound to a workspace state a later node changed.
type SupersededVerification struct {
	NodeID  int    `json:"node_id"`
	Title   string `json:"title"`
	Command string `json:"command,omitempty"`
	// Token is the state the check actually ran against, which is no longer
	// the state the graph is finishing in.
	Token string `json:"token"`
}

// supersededVerificationsLocked reports done nodes whose passing checks ran
// against a workspace that later nodes have since changed.
//
// A node's gate is evaluated against the state it completed in, which is the
// only state it could be evaluated against. Nothing re-runs it afterwards, and
// nothing should: staling every finished node on each mutation would make a
// multi-node plan unable to converge. But it means a later node with a
// narrower check can break an earlier node's work and still let the graph
// finish — feature B's suite passing says nothing about feature A.
//
// The runtime cannot tell a repository-wide check from a narrow one without
// interpreting commands, which is exactly the kind of judgement it declines to
// fake. So it does not claim the earlier work still holds, and it does not
// claim it is broken either. It says which checks ran against a state that is
// no longer current, and leaves the conclusion to the person reading it.
func (g *Graph) supersededVerificationsLocked() []SupersededVerification {
	final := g.state.WorkspaceToken
	if final == "" {
		return nil
	}
	var superseded []SupersededVerification
	for i := range g.state.Nodes {
		node := &g.state.Nodes[i]
		if node.State != NodeDone || node.AcceptedAttemptID == "" {
			continue
		}
		var stale *Evidence
		for j := range g.state.Evidence {
			evidence := &g.state.Evidence[j]
			if evidence.NodeID != node.ID || evidence.Kind != EvidenceVerification || evidence.Status != "passed" {
				continue
			}
			if evidence.WorkspaceToken == "" || evidence.WorkspaceToken == final {
				// A check bound to the final state still describes it, so this
				// node is accounted for however many others are not.
				stale = nil
				break
			}
			stale = evidence
		}
		if stale != nil {
			superseded = append(superseded, SupersededVerification{
				NodeID: node.ID, Title: node.Title, Command: stale.Command, Token: stale.WorkspaceToken,
			})
		}
	}
	return superseded
}

// SupersededVerifications reports done nodes whose passing checks ran against
// a workspace state that later work has changed.
func (g *Graph) SupersededVerifications() []SupersededVerification {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.supersededVerificationsLocked()
}

func supersededSummary(superseded []SupersededVerification) string {
	if len(superseded) == 0 {
		return ""
	}
	parts := make([]string, 0, len(superseded))
	for _, item := range superseded {
		if item.Command != "" {
			parts = append(parts, fmt.Sprintf("node %d (%s) passed %q against an earlier workspace state", item.NodeID, item.Title, item.Command))
			continue
		}
		parts = append(parts, fmt.Sprintf("node %d (%s) was verified against an earlier workspace state", item.NodeID, item.Title))
	}
	return strings.Join(parts, "; ")
}

// RetiredNodes returns the nodes revisions removed before they completed.
func (g *Graph) RetiredNodes() []RetiredNode {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]RetiredNode(nil), g.state.RetiredNodes...)
}

// retiredSummaryLocked renders the retirement account for a terminal reason.
func (g *Graph) retiredSummaryLocked() string {
	if len(g.state.RetiredNodes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(g.state.RetiredNodes))
	for _, retired := range g.state.RetiredNodes {
		parts = append(parts, fmt.Sprintf("node %d (%s) was removed by revision while %s: %s", retired.ID, retired.Title, retired.State, retired.Reason))
	}
	return strings.Join(parts, "; ")
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
			if attempt.Worktree != "" {
				// The write-ahead record is what makes this actionable: the tree is
				// named exactly, rather than described as something to go find.
				reason = fmt.Sprintf("an isolated writer may have changed retained worktree %s (branch %s) before the session stopped; inspect and reconcile it explicitly before continuing", attempt.Worktree, attempt.Branch)
			}
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
	if g.state.Outcome == OutcomeAwaitingReview {
		return errors.New("goal graph is awaiting review of its retained candidates, not blocked; inspect them with /agents and integrate explicitly rather than retrying")
	}
	if g.state.Outcome != OutcomeBlocked {
		return errors.New("goal graph is not blocked")
	}
	node := g.nodeLocked(nodeID)
	if node == nil {
		return fmt.Errorf("goal graph has no node %d", nodeID)
	}
	if node.State == NodeAwaitingReview {
		return fmt.Errorf("goal graph node %d holds a verified candidate awaiting review; retrying would discard it", nodeID)
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

// ExtendBudget grants an exhausted graph one more fixed envelope and returns
// it to work. Only a person can call this path: exhaustion is otherwise
// terminal, and the model, configuration, repository text, hooks, and skills
// still cannot widen the ceiling by any route.
//
// The grant is deliberately not a resume. Every attempt the exhaustion ended
// stays immutable and terminal, and each unfinished node starts a new attempt,
// so nothing that was in flight when the ceiling hit is replayed. Finished
// nodes and retained candidates are untouched.
func (g *Graph) ExtendBudget(ctx context.Context, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Outcome != OutcomeBudgetExhausted {
		return fmt.Errorf("goal graph is %q, not budget exhausted", g.state.Outcome)
	}

	restored := 0
	for i := range g.state.Nodes {
		node := &g.state.Nodes[i]
		if node.State != NodeBudgetExhausted {
			continue
		}
		// A node that already spent its attempt bound cannot honestly be made
		// ready again by adding tokens, so it stays blocked with that reason.
		if len(node.AttemptIDs) >= g.state.MaxAttemptsPerNode {
			node.State, node.Reason = NodeBlocked, "node exhausted its attempt bound before the aggregate budget was extended"
			g.queueUpdateLocked(node.ID, "", string(NodeBlocked), node.Reason)
			continue
		}
		node.State, node.ActiveAttemptID, node.Reason = NodeProposed, "", "aggregate budget extended by explicit user grant"
		restored++
		g.queueUpdateLocked(node.ID, "", string(NodeProposed), node.Reason)
	}
	if restored == 0 {
		return errors.New("no node can resume: every unfinished node has exhausted its attempt bound")
	}
	grant := g.state.AggregateBudget.Grant
	g.state.AggregateBudget.Extensions++
	g.state.AggregateBudget.MaxIterations += grant.Iterations
	g.state.AggregateBudget.MaxTokens += grant.Tokens
	g.state.AggregateBudget.MaxCostUSD += grant.CostUSD
	g.state.AggregateBudget.MaxActiveWallSeconds += grant.ActiveWallSeconds
	g.state.Outcome, g.state.Reason = "", ""
	g.refreshReadyLocked("aggregate budget extended by explicit user grant")
	g.queueUpdateLocked(0, "", "budget_extended", boundedReason(strings.TrimSpace(reason)))
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
		RemainingTokens:     max(0, limits.MaxTokens-usage.Total.BillableTokens()),
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
	case exhausted(float64(status.Usage.BillableTokens()), float64(status.Limits.MaxTokens)):
		reason = fmt.Sprintf("aggregate token budget exhausted: %d/%d (%d prompt tokens were served from the provider cache and are not charged)", status.Usage.BillableTokens(), status.Limits.MaxTokens, status.Usage.CachedTokens)
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
		// A finished node's recorded outcome is a fact. Exhausting the budget
		// afterwards must not overwrite an accepted result or a retained
		// candidate the operator still has to review.
		if node.State == NodeDone || node.State == NodeAwaitingReview {
			continue
		}
		attemptID := node.ActiveAttemptID
		node.State, node.ActiveAttemptID, node.Reason = NodeBudgetExhausted, "", reason
		g.queueUpdateLocked(node.ID, attemptID, string(NodeBudgetExhausted), reason)
	}
	g.stopActiveLocked(now)
	// Exhaustion costs a decision, not the graph. Accepted nodes and retained
	// candidates survive, so the way forward should be stated where the
	// operator reads the failure rather than left to be discovered.
	outcomeReason := reason + "; completed nodes are kept — run /orchestrate extend to grant another bounded envelope and continue, or /orchestrate cancel to stop here"
	g.state.Outcome, g.state.Reason = OutcomeBudgetExhausted, boundedReason(outcomeReason)
	g.clearPauseLocked()
	g.queueUpdateLocked(0, "", string(OutcomeBudgetExhausted), g.state.Reason)
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
	cached := ""
	if usage.CachedTokens > 0 {
		cached = fmt.Sprintf(" (%d cached, not charged)", usage.CachedTokens)
	}
	return fmt.Sprintf("%d input%s + %d output tokens · %d provider iterations · %s", usage.InputTokens, cached, usage.OutputTokens, usage.Iterations, cost)
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
	marks := map[NodeState]string{NodeProposed: "[ ]", NodeReady: "[>]", NodeRunning: "[~]", NodeRetryable: "[r]", NodeStale: "[s]", NodeBlocked: "[!]", NodeAwaitingReview: "[?]", NodeCancelled: "[-]", NodeBudgetExhausted: "[$]", NodeDone: "[x]"}
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
	fmt.Fprintf(&b, "Aggregate envelope: %d/%d provider iterations · %d/%d charged tokens · %s · %s/%s active wall\n", budget.Usage.Iterations, budget.Limits.MaxIterations, budget.Usage.BillableTokens(), budget.Limits.MaxTokens, costBound, formatGraphElapsed(budget.ActiveElapsed), formatGraphElapsed(time.Duration(budget.Limits.MaxActiveWallSeconds)*time.Second))
	b.WriteString("Execution: end-to-end graphs use one serial primary lane for every parent-workspace write, with bounded automatic read_only workers; an explicitly candidate-only graph may instead use one bounded pairwise-disjoint terminal isolated_write wave.\n")
	b.WriteString("Write scope: isolated writers require explicit narrow scopes and a common clean Git base; no candidate is selected, integrated, or allowed to unlock dependents automatically.\n")
	b.WriteString("Authority: every action still uses ordinary permissions; approval grants no publication or additional tool access.\n")
	b.WriteString("Completion: changed workspace state requires fresh machine-observed verification.\n\n")
	marks := map[NodeState]string{NodeProposed: "[ ]", NodeReady: "[>]", NodeRunning: "[~]", NodeRetryable: "[r]", NodeStale: "[s]", NodeBlocked: "[!]", NodeAwaitingReview: "[?]", NodeCancelled: "[-]", NodeBudgetExhausted: "[$]", NodeDone: "[x]"}
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
	if superseded := g.supersededVerificationsLocked(); len(superseded) > 0 {
		b.WriteString("\nVerified against an earlier workspace state (not re-run since)\n")
		for _, item := range superseded {
			if item.Command != "" {
				fmt.Fprintf(&b, "  node %d (%s) · %s\n", item.NodeID, item.Title, bounded(item.Command, 120))
				continue
			}
			fmt.Fprintf(&b, "  node %d (%s)\n", item.NodeID, item.Title)
		}
	}
	// A node the plan no longer contains cannot appear in the node list above,
	// so this is the only place an operator can see that the approved plan was
	// reduced and what left it.
	if len(g.state.RetiredNodes) > 0 {
		b.WriteString("\nRetired by revision (removed before completing; no evidence was produced)\n")
		for _, retired := range g.state.RetiredNodes {
			fmt.Fprintf(&b, "  node %d (%s) · was %s · generation %d · %s\n",
				retired.ID, retired.Title, retired.State, retired.Generation, bounded(strings.TrimSpace(retired.Reason), 200))
		}
	}
	// Retained worktrees are listed for the whole graph, not just the node being
	// inspected. A wave that ended in review, cancellation, or budget exhaustion
	// leaves real directories on disk, and an operator should not have to guess
	// node identifiers to find out what is still there.
	if retained := g.retainedCandidatesLocked(); len(retained) > 0 {
		b.WriteString("\nRetained candidates (nothing is selected, integrated, or removed automatically)\n")
		for _, line := range retained {
			b.WriteString("- " + line + "\n")
		}
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
		if attempt.EvidencePruned > 0 {
			fmt.Fprintf(&b, " · %d older tool results pruned (full transcript retained in the session log)", attempt.EvidencePruned)
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

// retainedCandidatesLocked renders one bounded line per worktree the graph
// still points at, in stable attempt order. A candidate whose child never
// reached verification is reported as unverified rather than omitted: the
// directory exists either way, and hiding it is what loses it.
func (g *Graph) retainedCandidatesLocked() []string {
	var lines []string
	for _, tree := range g.retainedWorktreesLocked() {
		state := "unknown"
		if tree.NodeState != "" {
			state = string(tree.NodeState)
		}
		// An attempt that stopped before its result was recorded has a directory
		// but no examined contents. Saying so is the point: that is precisely the
		// tree an operator has to reconcile by hand.
		detail := "candidate never recorded · contents never inspected by the runtime"
		if tree.Verification != "" {
			detail = fmt.Sprintf("verification %s · %d changed file(s) · base %s",
				tree.Verification, tree.ChangedFiles, bounded(tree.BaseCommit, 12))
		}
		// The disposition is what the runtime actually observed on disk, so it
		// leads: a path printed without it reads as a promise that the directory
		// is still there, which is the claim this slice exists to stop making.
		disposition := "unreconciled — run /orchestrate reconcile"
		if tree.Disposition != "" {
			disposition = string(tree.Disposition)
			if tree.Detail != "" {
				disposition += " (" + bounded(tree.Detail, 200) + ")"
			}
		}
		lines = append(lines, fmt.Sprintf("node %d (%s) · attempt %s · %s · %s · %s · branch %s",
			tree.NodeID, state, tree.AttemptID, disposition, detail, tree.Worktree, tree.Branch))
	}
	return lines
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
	var blockers, exhausted, review, integrated []string
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
		if node.State == NodeAwaitingReview {
			review = append(review, fmt.Sprintf("node %d (%s): %s", node.ID, node.Title, node.Reason))
		}
		if node.State == NodeBudgetExhausted {
			exhausted = append(exhausted, fmt.Sprintf("node %d (%s): %s", node.ID, node.Title, node.Reason))
		}
		if node.State == NodeIntegrated {
			integrated = append(integrated, fmt.Sprintf("node %d (%s): %s", node.ID, node.Title, node.Reason))
		}
	}
	if allDone {
		g.state.Outcome = OutcomeDone
		g.state.Reason = "all required nodes passed runtime acceptance gates"
		// Say what was dropped. "Every required node passed" is true only of
		// the nodes still in the graph, and a reader deciding whether the goal
		// was met needs to know the set shrank and which work left with it.
		if retired := g.retiredSummaryLocked(); retired != "" {
			g.state.Reason = "every remaining required node passed runtime acceptance gates, but the approved plan was reduced first — " + retired
		}
		// Each gate was evaluated against the state its node completed in, and
		// a later node may have changed that state since. Saying which checks
		// no longer describe the workspace is the difference between an
		// accurate completion and an overstated one.
		if superseded := supersededSummary(g.supersededVerificationsLocked()); superseded != "" {
			g.state.Reason += "; checks that have not been re-run against the final workspace: " + superseded
		}
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
		return
	}
	// Nothing failed here: every required node either passed its gates or left
	// a verified candidate. Reporting that as blocked would make the writer
	// wave's success indistinguishable from its failure. Like a material
	// blocker, a retained candidate ends the required graph: it cannot unlock a
	// dependent node before a human integrates it.
	// A published candidate outranks a merely retained one when both are
	// present: bytes are already in the user's workspace, and that is the more
	// urgent thing to say.
	if len(integrated) > 0 && !running {
		g.state.Outcome, g.state.Reason = OutcomeAwaitingVerification, strings.Join(integrated, "; ")
		g.stopActiveLocked(g.now().UTC())
		g.clearPauseLocked()
		g.queueUpdateLocked(0, "", string(OutcomeAwaitingVerification), g.state.Reason)
		return
	}
	if len(review) > 0 && !running {
		g.state.Outcome, g.state.Reason = OutcomeAwaitingReview, strings.Join(review, "; ")
		g.stopActiveLocked(g.now().UTC())
		g.clearPauseLocked()
		g.queueUpdateLocked(0, "", string(OutcomeAwaitingReview), g.state.Reason)
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
	g.pruneAttemptEvidenceLocked(attempt)
}

// pruneAttemptEvidenceLocked drops the oldest ordinary tool results once an
// attempt holds more than the bound. Acceptance depends on verification and
// node-result evidence, and on the newest results a completion gap is assessed
// against, so neither is eligible; the middle of a long tool loop is.
func (g *Graph) pruneAttemptEvidenceLocked(attempt *Attempt) {
	prunable := 0
	for _, id := range attempt.EvidenceIDs {
		if item := g.evidenceLocked(id); item != nil && item.Kind == EvidenceToolResult {
			prunable++
		}
	}
	if prunable <= maxAttemptToolEvidence {
		return
	}
	drop := make(map[string]bool, prunable-maxAttemptToolEvidence)
	remaining := prunable - maxAttemptToolEvidence
	for _, id := range attempt.EvidenceIDs {
		if remaining == 0 {
			break
		}
		if item := g.evidenceLocked(id); item != nil && item.Kind == EvidenceToolResult {
			drop[id] = true
			remaining--
		}
	}
	kept := attempt.EvidenceIDs[:0]
	for _, id := range attempt.EvidenceIDs {
		if !drop[id] {
			kept = append(kept, id)
		}
	}
	attempt.EvidenceIDs = kept
	retained := g.state.Evidence[:0]
	for _, item := range g.state.Evidence {
		if !drop[item.ID] {
			retained = append(retained, item)
		}
	}
	g.state.Evidence = retained
	attempt.EvidencePruned += len(drop)
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

// candidateVerificationFailureDetail explains why a candidate was rejected in
// terms an operator can act on: which check failed, run against what.
//
// A blocked node is the graph declining to use work, and the reason is the only
// account of why. "command failed: exit status 1" describes an exit code; it
// does not say that the candidate's own verification rejected it, nor which
// command did. Both are already recorded on the candidate.
func candidateVerificationFailureDetail(candidate *WriterCandidate, raw string) string {
	if candidate == nil {
		if raw != "" {
			return "isolated writer produced no candidate to verify: " + raw
		}
		return "isolated writer produced no candidate to verify"
	}
	var failed []string
	for _, verification := range candidate.Verification {
		if verification.Status != "passed" {
			failed = append(failed, verification.Command)
		}
	}
	switch {
	case len(failed) > 0:
		detail := fmt.Sprintf("isolated writer candidate failed its own verification: %s", strings.Join(failed, ", "))
		if raw != "" {
			detail += " (" + raw + ")"
		}
		return detail
	case len(candidate.Verification) == 0:
		detail := "isolated writer candidate produced no machine-observed verification, so nothing established that its changes work"
		if raw != "" {
			detail += " (" + raw + ")"
		}
		return detail
	case candidate.VerificationState != "passed":
		detail := fmt.Sprintf("isolated writer candidate verification is %q rather than passed", candidate.VerificationState)
		if raw != "" {
			detail += " (" + raw + ")"
		}
		return detail
	default:
		// Every recorded command passed but the evidence is not bound to one
		// settled candidate state, which means the tree moved under the checks.
		detail := "isolated writer candidate verification is not bound to a single settled state, so its passing checks do not describe the retained tree"
		if raw != "" {
			detail += " (" + raw + ")"
		}
		return detail
	}
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
	clone.RetiredNodes = append([]RetiredNode(nil), snapshot.RetiredNodes...)
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

func equalGapKinds(a, b []GapKind) bool {
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

func cloneAttempt(attempt Attempt) Attempt {
	attempt.Failures = append([]Failure(nil), attempt.Failures...)
	attempt.EvidenceIDs = append([]string(nil), attempt.EvidenceIDs...)
	attempt.CompletionGapKinds = append([]GapKind(nil), attempt.CompletionGapKinds...)
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
