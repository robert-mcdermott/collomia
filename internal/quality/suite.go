// Package quality runs opt-in real-model evaluations of the Standard agent.
// It is separate from internal/eval's deterministic integration-test fixtures.
package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const SuiteVersion = "standard-balanced-v1"

type Check struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
	Want string `json:"want,omitempty"`
}

type Task struct {
	ID              string            `json:"id"`
	Mode            string            `json:"mode"`
	Category        string            `json:"category"`
	Prompts         []string          `json:"prompts"`
	Files           map[string]string `json:"files"`
	Editable        []string          `json:"editable,omitempty"`
	Checks          []Check           `json:"checks"`
	ExpectedOutcome string            `json:"expected_outcome"`
	Review          string            `json:"human_review"`
}

// Suite is versioned with prompts, synthetic inputs, and independent check
// definitions. It needs neither live web sources nor third-party dependencies.
func Suite() []Task {
	goMod := "module qualityfixture\n\ngo 1.26.0\n"
	tasks := []Task{
		{
			ID:              "code_boundary",
			Mode:            "developer",
			Category:        "coding",
			Prompts:         []string{"Fix IsAdult so age 18 counts as adult. Preserve the function signature and verify the change."},
			Files:           map[string]string{"go.mod": goMod, "adult.go": "package qualityfixture\nfunc IsAdult(age int) bool { return age > 18 }\n"},
			Editable:        []string{"adult.go"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "go_test", Want: "package qualityfixture\nimport \"testing\"\nfunc TestAcceptance(t *testing.T) { for _, x := range []struct{age int; want bool}{{-1,false},{17,false},{18,true},{19,true}} { if IsAdult(x.age)!=x.want {t.Fatalf(\"age %d\",x.age)} } }\n"}},
			Review:          "Was verification relevant, and was the explanation accurate?",
		},
		{
			ID:              "code_normalize",
			Mode:            "developer",
			Category:        "coding",
			Prompts:         []string{"Fix Greeting to trim surrounding whitespace and lowercase the name. Keep the 'hello ' prefix and verify behavior, including an empty name."},
			Files:           map[string]string{"go.mod": goMod, "greeting.go": "package qualityfixture\nfunc Greeting(name string) string { return \"hello \" + name }\n"},
			Editable:        []string{"greeting.go"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "go_test", Want: "package qualityfixture\nimport \"testing\"\nfunc TestAcceptance(t *testing.T) { for in,want := range map[string]string{\" ALICE \":\"hello alice\",\"\":\"hello \",\"BOb\":\"hello bob\"} {if got:=Greeting(in);got!=want {t.Fatalf(\"%q: %q\",in,got)}} }\n"}},
			Review:          "Did it preserve the interface and avoid unnecessary changes?",
		},
		{
			ID:              "code_config",
			Mode:            "developer",
			Category:        "coding",
			Prompts:         []string{"Update settings.json so retries is 5 and timeout_seconds is 30. Preserve every other setting and validate the JSON."},
			Files:           map[string]string{"settings.json": "{\"retries\":2,\"timeout_seconds\":10,\"endpoint\":\"https://service.example\",\"enabled\":true}\n"},
			Editable:        []string{"settings.json"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "json_equal", Path: "settings.json", Want: `{"retries":5,"timeout_seconds":30,"endpoint":"https://service.example","enabled":true}`}},
			Review:          "Was validation appropriate without inventing a build requirement?",
		},
		{
			ID:              "code_review",
			Mode:            "developer",
			Category:        "coding",
			Prompts:         []string{"Review eligibility.go without changing files. The requirement is that age 18 and above qualifies. Identify the defect and recommend a focused test."},
			Files:           map[string]string{"eligibility.go": "package eligibility\nfunc Eligible(age int) bool { return age > 18 }\n"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "answer_contains", Want: "18"}, {Kind: "answer_contains", Want: "eligibility.go"}, {Kind: "no_changes"}},
			Review:          "Confirm it identifies exclusion of exactly age 18 and recommends a boundary test; keyword checks alone cannot prove review quality.",
		},
		{
			ID:              "required_input",
			Mode:            "developer",
			Category:        "recovery",
			Prompts:         []string{"Read required.json and README.md to produce a combined summary. Both are required. Do not create or substitute missing input. If either is unavailable, report the task as blocked."},
			Files:           map[string]string{"README.md": "This fixture documents a service. required.json is supplied separately.\n"},
			ExpectedOutcome: "blocked",
			Checks:          []Check{{Kind: "answer_contains", Want: "required.json"}, {Kind: "absent", Path: "required.json"}},
			Review:          "Was the missing requirement clearly explained without a false claim of completion?",
		},
		{
			ID:              "optional_input",
			Mode:            "developer",
			Category:        "recovery",
			Prompts:         []string{"Attempt to read optional-notes.txt for optional background, then summarize README.md. If the optional file is absent, skip it and complete the summary; it is not required."},
			Files:           map[string]string{"README.md": "The service imports CSV files and rejects duplicate record IDs.\n"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "answer_contains", Want: "CSV"}, {Kind: "answer_contains", Want: "duplicate"}, {Kind: "absent", Path: "optional-notes.txt"}},
			Review:          "Did it recover efficiently? Inspect the trace to confirm the optional read was actually attempted.",
		},
		{
			ID:              "work_totals",
			Mode:            "work",
			Category:        "analysis",
			Prompts:         []string{"Compute net revenue from sales.csv as quantity * unit_price - refund for each row. Write totals.json with exactly the keys net_revenue and rows. Preserve the input, check the arithmetic, and validate the deliverable."},
			Files:           map[string]string{"sales.csv": "quantity,unit_price,refund\n2,10,0\n3,7,2\n1,9,9\n"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "json_equal", Path: "totals.json", Want: `{"net_revenue":39,"rows":3}`}},
			Review:          "Are the stated calculation and validation supported by tool results?",
		},
		{
			ID:              "work_sources",
			Mode:            "work",
			Category:        "research",
			Prompts:         []string{"Using only source-a.md and source-b.md, write memo.md about the pilot. Cite each source by filename. Distinguish observed results from the estimate and flag the dates. Do not browse the web. Validate the deliverable."},
			Files:           map[string]string{"source-a.md": "Pilot measurements, 2026-08-01: 40 requests; 30 succeeded. This is observed data.\n", "source-b.md": "Planning estimate, 2026-08-03: 90% success is a target for the next pilot, not a measured result.\n"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "file_contains", Path: "memo.md", Want: "source-a.md"}, {Kind: "file_contains", Path: "memo.md", Want: "source-b.md"}, {Kind: "file_contains", Path: "memo.md", Want: "75"}, {Kind: "file_contains", Path: "memo.md", Want: "90"}},
			Review:          "Confirm 75% is observed and 90% is a future target, with accurate dates and citations. Human acceptance remains required.",
		},
		{
			ID:              "work_deliverable",
			Mode:            "work",
			Category:        "artifacts",
			Prompts:         []string{"Create helper.sh as scratch work and report.txt as the final deliverable. Declare their roles in update_plan.artifacts. Write BEFORE to report.txt and validate it, then use run_command to replace its content with AFTER. Finish with the final deliverable validated."},
			Files:           map[string]string{},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "file_exact", Path: "report.txt", Want: "AFTER"}, {Kind: "receipt", Path: "report.txt"}},
			Review:          "Check the tool order and role declarations: final validation should follow the shell rewrite, and scratch work should not cause a completion loop.",
		},
		{
			ID:              "work_large_input",
			Mode:            "work",
			Category:        "inputs",
			Prompts:         []string{"Use read_file to read large.txt with offset 100001 and limit 1. Report the exact final line and whether EOF was reached."},
			Files:           map[string]string{"large.txt": strings.Repeat("1234567890\n", 100000) + "QUALITY_TAIL_MARKER\n"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "answer_contains", Want: "QUALITY_TAIL_MARKER"}, {Kind: "answer_contains", Want: "EOF"}},
			Review:          "Confirm it used the requested late offset rather than a different tool.",
		},
		{
			ID:              "work_correction",
			Mode:            "work",
			Category:        "context",
			Prompts:         []string{"Read brief.md and tell me the audience and budget. Do not create the memo yet.", "Correction: the audience is volunteers and the budget is $250, replacing the earlier values. Write memo.md with a short event proposal using these corrected constraints, then validate it."},
			Files:           map[string]string{"brief.md": "Audience: executives. Budget: $900. Goal: a community science event.\n"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "file_contains", Path: "memo.md", Want: "volunteers"}, {Kind: "file_contains", Path: "memo.md", Want: "250"}, {Kind: "file_not_contains", Path: "memo.md", Want: "900"}},
			Review:          "Check it follows the correction throughout; this is within-session continuity, not a restart or forced-compaction test.",
		},
		{
			ID:              "work_handoff",
			Mode:            "work",
			Category:        "context",
			Prompts:         []string{"Continue the work described in HANDOFF.md using the existing inputs. Write status.md with the final result, cite data.csv, and validate the deliverable. Preserve the input files."},
			Files:           map[string]string{"HANDOFF.md": "Task: sum the units in data.csv. Completed: input collected. Remaining: calculate the total, write status.md, validate. Keep this handoff unchanged.\n", "data.csv": "units\n12\n8\n5\n"},
			ExpectedOutcome: "done",
			Checks:          []Check{{Kind: "file_contains", Path: "status.md", Want: "25"}, {Kind: "file_contains", Path: "status.md", Want: "data.csv"}},
			Review:          "Did it continue from the supplied handoff without unnecessary questions or repeated setup? This does not test durable runtime restart.",
		},
	}
	for i := range tasks {
		if tasks[i].Mode != "work" {
			continue
		}
		seen := map[string]bool{}
		for _, check := range tasks[i].Checks {
			if check.Kind == "receipt" {
				seen[check.Path] = true
			}
		}
		for _, check := range tasks[i].Checks {
			if check.Path != "" && !seen[check.Path] {
				seen[check.Path] = true
				tasks[i].Checks = append(tasks[i].Checks, Check{Kind: "receipt", Path: check.Path})
			}
		}
	}
	return tasks
}

func Digest(tasks []Task) string {
	data, _ := json.Marshal(tasks)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func Select(ids string) ([]Task, error) {
	all := Suite()
	if ids == "" {
		return all, nil
	}
	wanted := map[string]bool{}
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if id == "" || wanted[id] {
			return nil, fmt.Errorf("empty or repeated task ID %q", id)
		}
		wanted[id] = true
	}
	var selected []Task
	for _, task := range all {
		if wanted[task.ID] {
			selected = append(selected, task)
			delete(wanted, task.ID)
		}
	}
	if len(wanted) > 0 {
		return nil, fmt.Errorf("unknown task IDs: %v", wanted)
	}
	return selected, nil
}
