package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/hooks"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// DelegateVerificationPlan is a read-only snapshot of the standard commands
// detected in a retained child worktree. StateToken binds command results to
// the exact child bytes and modes, independently of the parent workspace.
type DelegateVerificationPlan struct {
	ID, Name, Worktree, Branch, BaseCommit string
	StateToken                             string
	Detected                               []string
	Commands                               []tools.VerificationCommand
}

// DelegateCandidateSummary is the bounded, deterministic comparison surface
// for completed delegated writers. It intentionally provides facts rather
// than automatically choosing or publishing a candidate.
type DelegateCandidateSummary struct {
	ID                 string                       `json:"id"`
	Name               string                       `json:"name"`
	Status             string                       `json:"status"`
	Readiness          string                       `json:"readiness"`
	ChangedFiles       int                          `json:"changed_files"`
	SelectableFiles    int                          `json:"selectable_files"`
	SelectableHunks    int                          `json:"selectable_hunks"`
	Conflicts          int                          `json:"conflicts"`
	VerificationStatus string                       `json:"verification_status,omitempty"`
	VerificationError  string                       `json:"verification_error,omitempty"`
	Verification       []agent.DelegateVerification `json:"verification,omitempty"`
	InputTokens        int                          `json:"input_tokens,omitempty"`
	OutputTokens       int                          `json:"output_tokens,omitempty"`
	Summary            string                       `json:"summary,omitempty"`
	Evidence           []string                     `json:"evidence,omitempty"`
}

// PrepareDelegateVerification validates the retained worktree, detects its
// repository-standard commands, and refreshes stale verification state.
func (r *Runtime) PrepareDelegateVerification(ctx context.Context, id string) (*DelegateVerificationPlan, error) {
	preview, err := r.PrepareDelegateIntegration(ctx, id)
	if err != nil {
		return nil, err
	}
	status, ok := r.Team.Get(id)
	if !ok {
		return nil, fmt.Errorf("unknown delegated agent %q", id)
	}
	switch status.Status {
	case agent.DelegateQueued, agent.DelegateRunning, agent.DelegateWaitingApproval, agent.DelegateCancelling:
		return nil, fmt.Errorf("delegated agent %q has not finished", id)
	}
	detected, commands := tools.DetectVerificationCommands(preview.Worktree)
	plan := &DelegateVerificationPlan{
		ID: preview.ID, Name: preview.Name, Worktree: preview.Worktree,
		Branch: preview.Branch, BaseCommit: preview.BaseCommit,
		StateToken: delegateVerificationStateToken(preview),
		Detected:   append([]string(nil), detected...),
		Commands:   append([]tools.VerificationCommand(nil), commands...),
	}
	r.refreshDelegateVerification(id, plan.StateToken)
	if len(plan.Commands) == 0 && status.VerificationStatus != "unavailable" {
		r.Team.MarkVerificationUnavailable(id, "no standard verification commands were detected in the delegated worktree")
	}
	return plan, nil
}

// PrepareDelegateVerificationAction returns the ordinary run_command action
// for one detected command. Callers must authorize this exact action before
// ExecuteDelegateVerificationCommand.
func (r *Runtime) PrepareDelegateVerificationAction(ctx context.Context, id, stateToken, command string) (*DelegateVerificationPlan, tools.VerificationCommand, tools.Action, error) {
	plan, err := r.PrepareDelegateVerification(ctx, id)
	if err != nil {
		return nil, tools.VerificationCommand{}, tools.Action{}, err
	}
	if stateToken == "" || stateToken != plan.StateToken {
		r.Team.MarkVerificationStale(id, "delegated changes changed; inspect them again before verification")
		return nil, tools.VerificationCommand{}, tools.Action{}, errors.New("delegated changes changed or were not inspected; call inspect_delegate_changes again")
	}
	selected, ok := findVerificationCommand(plan.Commands, command)
	if !ok {
		return nil, tools.VerificationCommand{}, tools.Action{}, fmt.Errorf("command %q is not in the detected verification suite", command)
	}
	runner, err := r.delegateCommandRunner(plan.Worktree)
	if err != nil {
		return nil, tools.VerificationCommand{}, tools.Action{}, err
	}
	raw, _ := json.Marshal(map[string]any{"command": selected.Command})
	action, err := runner.Assess(raw)
	if err != nil {
		return nil, tools.VerificationCommand{}, tools.Action{}, err
	}
	action.Summary = fmt.Sprintf("verify delegated agent %s (%s): %s", plan.Name, id, selected.Command)
	return plan, selected, action, nil
}

// ExecuteDelegateVerificationCommand runs one already-authorized detected
// command, streams bounded output, and records a machine-observed result.
func (r *Runtime) ExecuteDelegateVerificationCommand(ctx context.Context, id, stateToken, command string, onOutput func(string)) (agent.DelegateVerification, error) {
	plan, selected, _, err := r.PrepareDelegateVerificationAction(ctx, id, stateToken, command)
	if err != nil {
		return agent.DelegateVerification{}, err
	}
	required := verificationCommandNames(plan.Commands)
	started := time.Now().UTC()
	r.Team.MarkVerificationRunning(id, plan.StateToken, required, selected.Command)
	runner, err := r.delegateCommandRunner(plan.Worktree)
	if err != nil {
		return agent.DelegateVerification{}, err
	}
	raw, _ := json.Marshal(map[string]any{"command": selected.Command})
	output, runErr := runner.ExecuteStream(ctx, raw, onOutput)
	finished := time.Now().UTC()
	result := agent.DelegateVerification{
		Purpose: selected.Purpose, Command: selected.Command, Output: r.redactDelegateVerification(output),
		StateToken: plan.StateToken, Started: started, Finished: finished,
	}
	runErrorText := ""
	if runErr != nil {
		runErrorText = runErr.Error()
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		result.Status = "cancelled"
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || strings.Contains(runErrorText, "timed out"):
		result.Status = "timed_out"
	case runErr != nil:
		result.Status = "failed"
	default:
		result.Status = "passed"
	}
	if runErr != nil {
		result.Error = r.redactDelegateVerification(runErr.Error())
	}

	fresh, freshErr := r.PrepareDelegateIntegration(context.Background(), id)
	if freshErr != nil {
		result.Status = "stale"
		result.Error = r.redactDelegateVerification("could not revalidate delegated worktree after verification: " + freshErr.Error())
	} else if delegateVerificationStateToken(fresh) != plan.StateToken {
		result.Status = "stale"
		result.Error = "delegated source changed while verification ran; inspect and run the suite again"
	}
	r.Team.MarkVerificationResult(id, plan.StateToken, required, result)
	if result.Status == "stale" {
		r.Team.MarkVerificationStale(id, result.Error)
	}
	if runErr != nil {
		return result, runErr
	}
	if result.Status == "stale" {
		return result, errors.New(result.Error)
	}
	return result, nil
}

// VerifyDelegateCommand is the operator path: authorize and execute exactly
// one detected command using the same policy identity as run_command.
func (r *Runtime) VerifyDelegateCommand(ctx context.Context, id, stateToken, command string, onOutput func(string)) (agent.DelegateVerification, error) {
	plan, selected, action, err := r.PrepareDelegateVerificationAction(ctx, id, stateToken, command)
	if err != nil {
		return agent.DelegateVerification{}, err
	}
	if _, err = r.Permissions.Authorize(ctx, "run_command", action); err != nil {
		status := "blocked"
		if errors.Is(err, permission.ErrDenied) {
			status = "rejected"
		}
		result := agent.DelegateVerification{
			Purpose: selected.Purpose, Command: selected.Command, Status: status,
			Error: r.redactDelegateVerification(err.Error()), StateToken: plan.StateToken,
			Started: time.Now().UTC(), Finished: time.Now().UTC(),
		}
		r.Team.MarkVerificationResult(id, plan.StateToken, verificationCommandNames(plan.Commands), result)
		return result, err
	}
	raw, _ := json.Marshal(map[string]any{"command": selected.Command})
	if hookErr := r.Hooks.Gate(ctx, hooks.Payload{
		Event: "tool_start", Workspace: plan.Worktree, Subject: "run_command", Tool: "run_command",
		Summary: action.Summary, Args: raw,
	}); hookErr != nil {
		result := agent.DelegateVerification{
			Purpose: selected.Purpose, Command: selected.Command, Status: "blocked",
			Error: r.redactDelegateVerification("blocked by hook: " + hookErr.Error()), StateToken: plan.StateToken,
			Started: time.Now().UTC(), Finished: time.Now().UTC(),
		}
		r.Team.MarkVerificationResult(id, plan.StateToken, verificationCommandNames(plan.Commands), result)
		r.Permissions.RecordOutcome("run_command", action, hookErr)
		return result, hookErr
	}
	result, runErr := r.ExecuteDelegateVerificationCommand(ctx, id, stateToken, command, onOutput)
	r.Permissions.RecordOutcome("run_command", action, runErr)
	end := hooks.Payload{
		Event: "tool_end", Workspace: plan.Worktree, Subject: "run_command", Tool: "run_command",
		Summary: action.Summary, Detail: map[string]any{"output_bytes": len(result.Output)},
	}
	if runErr != nil {
		end.Error = r.redactDelegateVerification(runErr.Error())
	}
	r.Hooks.Fire(ctx, end)
	return result, runErr
}

// VerifyDelegateSuite runs every detected command sequentially. Each command
// receives its own policy decision and a failure stops the remaining suite.
func (r *Runtime) VerifyDelegateSuite(ctx context.Context, id string, onOutput func(string)) ([]agent.DelegateVerification, error) {
	plan, err := r.PrepareDelegateVerification(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(plan.Commands) == 0 {
		return nil, errors.New("no standard verification commands were detected in the delegated worktree")
	}
	results := make([]agent.DelegateVerification, 0, len(plan.Commands))
	for _, command := range plan.Commands {
		result, runErr := r.VerifyDelegateCommand(ctx, id, plan.StateToken, command.Command, onOutput)
		results = append(results, result)
		if runErr != nil {
			return results, runErr
		}
	}
	return results, nil
}

// CompareDelegateCandidates returns bounded, refreshed facts for two or more
// completed write candidates. It never executes or publishes anything.
func (r *Runtime) CompareDelegateCandidates(ctx context.Context, ids []string) ([]DelegateCandidateSummary, error) {
	if len(ids) < 2 {
		return nil, errors.New("compare at least two delegated agents")
	}
	if len(ids) > 6 {
		return nil, errors.New("compare at most six delegated agents")
	}
	seen := map[string]bool{}
	out := make([]DelegateCandidateSummary, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return nil, fmt.Errorf("delegated agent ids must be non-empty and unique")
		}
		seen[id] = true
		preview, err := r.PrepareDelegateIntegration(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		r.refreshDelegateVerification(id, delegateVerificationStateToken(preview))
		status, _ := r.Team.Get(id)
		item := DelegateCandidateSummary{
			ID: id, Name: status.Name, Status: status.Status,
			ChangedFiles: len(preview.Files), VerificationStatus: status.VerificationStatus,
			VerificationError: status.VerificationError, Verification: append([]agent.DelegateVerification(nil), status.VerificationResults...),
			InputTokens: status.Usage.InputTokens, OutputTokens: status.Usage.OutputTokens,
			Summary: status.Summary, Evidence: append([]string(nil), status.Evidence...),
		}
		for _, file := range preview.Files {
			switch {
			case file.Conflict != "":
				item.Conflicts++
			case !file.AlreadyApplied && file.Unified != "":
				item.SelectableFiles++
				if hunks, parseErr := parseDelegateHunks(file.Unified); parseErr == nil {
					item.SelectableHunks += hunks
				}
			}
		}
		switch {
		case item.Conflicts > 0 || item.SelectableFiles == 0:
			item.Readiness = "blocked"
		case item.VerificationStatus == "passed":
			item.Readiness = "verified"
		default:
			item.Readiness = "review"
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *Runtime) refreshDelegateVerification(id, currentToken string) {
	status, ok := r.Team.Get(id)
	if !ok || status.VerificationToken == "" || status.VerificationToken == currentToken || status.VerificationStatus == "stale" {
		return
	}
	r.Team.MarkVerificationStale(id, "delegated source changed after verification; run the suite again")
}

func (r *Runtime) delegateCommandRunner(worktree string) (*tools.RunCommandTool, error) {
	command, err := tools.ConfiguredRunCommandTool(worktree, r.Config, r.Config.Options.MaxToolOutputBytes)
	if err != nil {
		return nil, fmt.Errorf("delegated verification command policy: %w", err)
	}
	return command, nil
}

func (r *Runtime) redactDelegateVerification(value string) string {
	if r.Redactor != nil {
		value = r.Redactor.Redact(value)
	}
	return value
}

func delegateVerificationStateToken(preview *DelegateIntegration) string {
	var canonical bytes.Buffer
	fmt.Fprintf(&canonical, "%q|%q|%q|%q|", preview.ID, preview.Branch, preview.BaseCommit, preview.Worktree)
	for _, file := range preview.Files {
		fmt.Fprintf(&canonical, "%q|%o|", file.Path, file.AfterMode)
		if file.After == nil {
			canonical.WriteString("-1|")
			continue
		}
		fmt.Fprintf(&canonical, "%d:", len(*file.After))
		canonical.WriteString(*file.After)
		canonical.WriteByte('|')
	}
	sum := sha256.Sum256(canonical.Bytes())
	return "verify-" + hex.EncodeToString(sum[:])
}

func verificationCommandNames(commands []tools.VerificationCommand) []string {
	out := make([]string, len(commands))
	for i, command := range commands {
		out[i] = command.Command
	}
	return out
}

func findVerificationCommand(commands []tools.VerificationCommand, command string) (tools.VerificationCommand, bool) {
	command = strings.TrimSpace(command)
	for _, candidate := range commands {
		if candidate.Command == command {
			return candidate, true
		}
	}
	return tools.VerificationCommand{}, false
}

func parseDelegateHunks(unified string) (int, error) {
	hunks, err := diffmodel.ParseHunks(unified)
	return len(hunks), err
}
