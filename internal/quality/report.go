package quality

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
)

func cell(value string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "`", "'").Replace(value)
}

func Report(result Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Collo quality scorecard\n\nSuite: `%s` · build: `%s` (`%s`) · model: `%s/%s`\n\nState: **%s**. Recorded %d of %d scheduled trials.\n", cell(result.Suite), cell(result.Version), cell(result.Commit), cell(result.Provider), cell(result.Model), cell(result.State), len(result.Trials), len(result.TaskIDs)*result.Settings.Trials)
	if result.StopReason != "" {
		fmt.Fprintf(&b, "\nStop reason: %s\n", cell(result.StopReason))
	}
	passed, falseDone, falseBlocked, reviewed, accepted := 0, 0, 0, 0, 0
	unnecessary, wasted, questionReviews, repeatReviews := 0, 0, 0, 0
	var tokens int
	cost := 0.0
	costKnown := len(result.Trials) > 0
	var duration int64
	for _, row := range result.Trials {
		if row.MachinePass {
			passed++
		}
		if row.FalseDone {
			falseDone++
		}
		if row.FalseBlockedCandidate {
			falseBlocked++
		}
		if row.Review != nil {
			reviewed++
			if row.Review.UnnecessaryQuestions != nil {
				unnecessary += *row.Review.UnnecessaryQuestions
				questionReviews++
			}
			if row.Review.WastedRepeats != nil {
				wasted += *row.Review.WastedRepeats
				repeatReviews++
			}
			if row.Review.Accepted {
				accepted++
			}
		}
		tokens += row.Usage.InputTokens + row.Usage.OutputTokens
		duration += row.DurationMS
		cost += row.Usage.CostUSD
		costKnown = costKnown && row.UsageComplete && row.Usage.CostAvailable
	}
	fmt.Fprintf(&b, "\nMachine-check passes: **%d/%d**. False-done results: **%d**. Possible false blockers: **%d** (review required). Human-reviewed acceptance: **%d/%d reviewed**, with %d pending.\n", passed, len(result.Trials), falseDone, falseBlocked, accepted, reviewed, len(result.Trials)-reviewed)
	if questionReviews > 0 {
		fmt.Fprintf(&b, "\nReviewed unnecessary questions: **%d across %d annotated trials**.\n", unnecessary, questionReviews)
	}
	if repeatReviews > 0 {
		fmt.Fprintf(&b, "\nReviewed wasted repeats: **%d across %d annotated trials**.\n", wasted, repeatReviews)
	}
	fmt.Fprintf(&b, "\nReported tokens: **%d**. Agent elapsed time: **%.1fs** (excludes grader time).", tokens, float64(duration)/1000)
	if costKnown {
		fmt.Fprintf(&b, " Estimated cost: **$%.4f**.", cost)
		if accepted > 0 && reviewed == len(result.Trials) {
			fmt.Fprintf(&b, " Estimated cost per accepted trial, including failed attempts: **$%.4f**.", cost/float64(accepted))
		}
	} else {
		b.WriteString(" Cost: **unavailable or incomplete**, not zero.")
	}
	b.WriteString("\n\nThese are fixture checks, not a SOTA ranking. Repeated calls can be valid retries or revalidation. Question counts do not establish that questions were unnecessary. Review each task's rubric and traces before accepting its result. Source-based writing uses local synthetic sources; context tasks cover corrections and a supplied handoff, not durable restart or forced compaction.\n\n")
	b.WriteString("| Task / trial | Mode | Outcome | Task pass | Seconds | Tokens | Calls / repeats | Ask-user calls | Controller / denials | Human acceptance |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, row := range result.Trials {
		checks := "fail"
		if row.MachinePass {
			checks = "pass"
		}
		review := "pending"
		if row.Review != nil {
			review = "rejected"
			if row.Review.Accepted {
				review = "accepted"
			}
		}
		fmt.Fprintf(&b, "| [%s / %d](%s/events.jsonl) | %s | %s | %s | %.1f | %d | %d / %d | %d | %d / %d | %s |\n", cell(row.Task), row.Trial, url.PathEscape(row.Directory), cell(row.Mode), cell(row.Outcome), checks, float64(row.DurationMS)/1000, row.Usage.InputTokens+row.Usage.OutputTokens, row.ToolCalls, row.RepeatedToolCalls, row.Questions, row.ControllerInterventions, row.PermissionDenials, review)
	}
	b.WriteString("\n## Per-task review\n")
	for _, row := range result.Trials {
		fmt.Fprintf(&b, "\n### %s / %d\n\n%s\n\n", cell(row.Task), row.Trial, cell(row.ReviewPrompt))
		for _, check := range row.Checks {
			mark := " "
			if check.Passed {
				mark = "x"
			}
			fmt.Fprintf(&b, "- [%s] %s\n", mark, cell(check.Name))
			if !check.Passed && check.Detail != "" {
				fmt.Fprintf(&b, "  %s\n", cell(check.Detail))
			}
		}
		if row.Error != "" {
			fmt.Fprintf(&b, "\nRuntime: %s\n", cell(row.Error))
		}
		if row.Review != nil {
			fmt.Fprintf(&b, "\nReviewer note: %s\n", cell(row.Review.Note))
		}
	}
	b.WriteString("\n## Reproducibility and limits\n\nThe harness runs the production Standard agent loop with a controlled built-in tool subset, a task-local plan, required command sandboxing, and no command networking. It does not load user hooks, MCP servers, global skills, project instructions, or agent profiles. Per-task token limits cover all turns; total tokens reserve a complete task allowance before starting it. Usage-based caps can overshoot by provider accounting differences. Missing usage stops the batch. Cost estimates require configured pricing and are not provider invoices. Read `results.json` for exact limits, build/suite identity, review decisions, and accounting completeness. Traces are local and redacted for configured secrets; inspect them before sharing.\n")
	return b.String()
}

func Compare(before, after Result) (string, error) {
	if before.Schema != after.Schema || before.Suite != after.Suite || before.SuiteDigest != after.SuiteDigest || before.Provider != after.Provider || before.ProviderType != after.ProviderType || before.Model != after.Model || before.ModelSettingsDigest != after.ModelSettingsDigest || before.Platform != after.Platform || !reflect.DeepEqual(before.Settings, after.Settings) || !reflect.DeepEqual(before.TaskIDs, after.TaskIDs) {
		return "", fmt.Errorf("comparison requires matching suite, task selection, trials, model, recorded model settings, platform, and budgets")
	}
	if before.State != "complete" || after.State != "complete" {
		return "", fmt.Errorf("comparison requires two complete runs; inspect partial scorecards separately")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Collo quality comparison\n\nBefore: `%s` · after: `%s` · model: `%s/%s`\n\n", cell(before.Commit), cell(after.Commit), cell(after.Provider), cell(after.Model))
	b.WriteString("| Task / trial | Before | After | Time delta (s) | Token delta |\n| --- | --- | --- | --- | --- |\n")
	if len(before.Trials) != len(after.Trials) {
		return "", fmt.Errorf("trial counts differ")
	}
	for i, a := range before.Trials {
		z := after.Trials[i]
		if a.Task != z.Task || a.Trial != z.Trial {
			return "", fmt.Errorf("trial order differs")
		}
		fmt.Fprintf(&b, "| %s / %d | %t | %t | %+.1f | %+d |\n", cell(a.Task), a.Trial, a.MachinePass, z.MachinePass, float64(z.DurationMS-a.DurationMS)/1000, z.Usage.InputTokens+z.Usage.OutputTokens-a.Usage.InputTokens-a.Usage.OutputTokens)
	}
	b.WriteString("\nPass values are machine checks, not human acceptance. With few trials, differences are descriptive and may reflect model variance or caching; no statistical significance is claimed. Backend/model changes behind an unchanged provider name cannot be ruled out.\n")
	return b.String(), nil
}
