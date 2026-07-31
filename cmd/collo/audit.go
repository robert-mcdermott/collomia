package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/audit"
)

// runAuditCommand implements `collo audit`: reading back the ledger of
// privileged decisions and outcomes for a workspace.
//
// The ledger existed for a long time with no reader at all, which made
// "every privileged action is reconstructable" a claim about a file rather
// than about anything a person could do. Reconstruction is the point, so this
// command is deliberately blunt: it prints what happened, in order, and it
// leads with any reason the answer is incomplete rather than burying it.
func runAuditCommand(opts options) error {
	subcommand := "show"
	if len(opts.args) > 0 {
		subcommand = opts.args[0]
	}
	switch subcommand {
	case "show":
		return runAuditShow(opts)
	case "path":
		path, err := auditPath(opts.cwd)
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	default:
		return fmt.Errorf("unknown audit subcommand %q (expected show or path)", subcommand)
	}
}

func auditPath(workspace string) (string, error) {
	dir, err := audit.Dir()
	if err != nil {
		return "", err
	}
	return dir + string(os.PathSeparator) + audit.FileName(workspace), nil
}

func runAuditShow(opts options) error {
	path, err := auditPath(opts.cwd)
	if err != nil {
		return err
	}
	filter := audit.Filter{
		Session:    opts.auditSession,
		Actor:      opts.auditActor,
		Tool:       opts.auditTool,
		DeniedOnly: opts.denied,
		Limit:      opts.auditLimit,
	}
	if opts.auditSince != "" {
		window, parseErr := time.ParseDuration(opts.auditSince)
		if parseErr != nil {
			return fmt.Errorf("--since requires a duration such as 24h or 30m: %w", parseErr)
		}
		if window <= 0 {
			return errors.New("--since requires a positive duration")
		}
		filter.Since = time.Now().UTC().Add(-window)
	}
	report, err := audit.Read(path, filter)
	if err != nil {
		return err
	}
	if opts.jsonl {
		encoder := json.NewEncoder(os.Stdout)
		for _, entry := range report.Entries {
			if err := encoder.Encode(entry); err != nil {
				return err
			}
		}
		return nil
	}
	fmt.Printf("workspace:  %s\n", opts.cwd)
	fmt.Printf("ledger:     %s\n", path)
	if len(report.Files) == 0 {
		fmt.Println("entries:    none — no privileged action has been recorded for this workspace")
		return nil
	}
	fmt.Printf("entries:    %d recorded, %d shown\n", report.Total, len(report.Entries))
	// Completeness is printed before the entries, not after. Someone
	// reconstructing an incident has to know the record has holes before they
	// start drawing conclusions from what is in it.
	if report.Complete() {
		fmt.Println("integrity:  complete — no declared gaps, no unreadable lines")
	} else {
		fmt.Println("integrity:  INCOMPLETE")
		if report.Dropped > 0 {
			fmt.Printf("            %d %s lost across %d declared %s (the ledger could not be written)\n",
				report.Dropped, plural(report.Dropped, "entry was", "entries were"), report.Gaps, plural(report.Gaps, "gap", "gaps"))
		}
		if report.Malformed > 0 {
			fmt.Printf("            %d %s not be parsed; a torn write or an edited file\n",
				report.Malformed, plural(report.Malformed, "line could", "lines could"))
		}
		if report.Discarded {
			fmt.Println("            an older generation was discarded at rotation; history before it is gone")
		}
	}
	if len(report.Entries) == 0 {
		fmt.Println("\nno entries match the filter")
		return nil
	}
	fmt.Println()
	for _, entry := range report.Entries {
		fmt.Println(formatAuditEntry(entry))
	}
	fmt.Println()
	fmt.Println(auditSummary(report))
	return nil
}

func formatAuditEntry(entry audit.Entry) string {
	stamp := entry.Time.Local().Format("2006-01-02 15:04:05")
	actor := entry.Actor
	if actor == "" {
		actor = "unattributed"
	}
	if entry.Task != "" {
		actor += "/" + entry.Task
	}
	switch entry.Kind {
	case audit.KindGap:
		since := "an unrecorded time"
		if entry.Since != nil {
			since = entry.Since.Local().Format("15:04:05")
		}
		return fmt.Sprintf("%s  %-22s GAP        %d %s lost since %s (%s)", stamp, actor, entry.Dropped, plural(entry.Dropped, "entry", "entries"), since, entry.Reason)
	case audit.KindRotation:
		note := entry.Reason
		if entry.Discarded {
			note += "; an older generation was discarded"
		}
		return fmt.Sprintf("%s  %-22s ROTATED    %s", stamp, actor, note)
	case audit.KindOutcome:
		return fmt.Sprintf("%s  %-22s outcome    %s: %s — %s", stamp, actor, entry.Tool, entry.Summary, entry.Outcome)
	default:
		decision := strings.ToUpper(entry.Decision)
		detail := entry.Source
		if entry.Rule != "" {
			detail += " (" + entry.Rule + ")"
		}
		line := fmt.Sprintf("%s  %-22s %-10s %s: %s [%s]", stamp, actor, decision, entry.Tool, entry.Summary, detail)
		if len(entry.Resources) > 0 {
			line += "\n" + strings.Repeat(" ", 21) + "reaches: " + strings.Join(entry.Resources, ", ")
		}
		return line
	}
}

// auditSummary counts what the shown entries add up to, because the question
// people bring to an audit trail is usually "what was refused" or "what did
// this agent touch" rather than "show me everything".
func auditSummary(report audit.Report) string {
	allowed, denied, failed := 0, 0, 0
	actors := map[string]int{}
	for _, entry := range report.Entries {
		switch {
		case entry.Denied():
			denied++
		case entry.Kind == audit.KindDecision:
			allowed++
		}
		if entry.Failed() {
			failed++
		}
		if entry.Kind == audit.KindDecision {
			actor := entry.Actor
			if actor == "" {
				actor = "unattributed"
			}
			actors[actor]++
		}
	}
	names := make([]string, 0, len(actors))
	for name := range actors {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, actors[name]))
	}
	line := fmt.Sprintf("%d allowed, %d denied, %d failed executions", allowed, denied, failed)
	if len(parts) > 0 {
		line += "; decisions by actor: " + strings.Join(parts, " ")
	}
	return line
}
