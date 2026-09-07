package quality

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/robert-mcdermott/collomia/internal/provider"
)

type Settings struct {
	Trials         int     `json:"trials"`
	TokenBudget    int     `json:"token_budget_per_task"`
	TotalTokens    int     `json:"total_token_budget"`
	CostBudget     float64 `json:"cost_budget_per_task_usd"`
	TimeoutSeconds int     `json:"timeout_seconds_per_task"`
	MaxIterations  int     `json:"max_iterations_per_turn"`
}

func DefaultSettings() Settings {
	return Settings{Trials: 1, TokenBudget: 40000, TotalTokens: 480000, TimeoutSeconds: 180, MaxIterations: 16}
}
func (s Settings) Validate() error {
	if s.Trials < 1 || s.Trials > 10 || s.TokenBudget < 1000 || s.TokenBudget > 1000000 || s.TotalTokens < s.TokenBudget || s.TotalTokens > 10000000 || s.TimeoutSeconds < 1 || s.TimeoutSeconds > 1800 || s.MaxIterations < 1 || s.MaxIterations > 100 || math.IsNaN(s.CostBudget) || math.IsInf(s.CostBudget, 0) || s.CostBudget < 0 {
		return errors.New("invalid evaluation limits: trials 1–10, per-task tokens 1000–1000000, total tokens at least per-task and at most 10000000, timeout 1–1800s, iterations 1–100, finite nonnegative cost")
	}
	return nil
}

type CheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}
type Review struct {
	Accepted             bool      `json:"accepted"`
	UnnecessaryQuestions *int      `json:"unnecessary_questions,omitempty"`
	WastedRepeats        *int      `json:"wasted_repeats,omitempty"`
	Note                 string    `json:"note"`
	Time                 time.Time `json:"time"`
}
type Trial struct {
	Task                    string         `json:"task"`
	Trial                   int            `json:"trial"`
	Mode                    string         `json:"mode"`
	Category                string         `json:"category"`
	Expected                string         `json:"expected_outcome"`
	Outcome                 string         `json:"outcome"`
	Error                   string         `json:"error,omitempty"`
	Answer                  string         `json:"answer"`
	MachinePass             bool           `json:"machine_pass"`
	FalseDone               bool           `json:"false_done"`
	FalseBlockedCandidate   bool           `json:"false_blocked_candidate"`
	Checks                  []CheckResult  `json:"checks"`
	Usage                   provider.Usage `json:"usage"`
	UsageComplete           bool           `json:"usage_complete"`
	DurationMS              int64          `json:"duration_ms"`
	ProviderCalls           int            `json:"provider_calls"`
	ToolCalls               int            `json:"tool_calls"`
	RepeatedToolCalls       int            `json:"repeated_tool_calls"`
	Questions               int            `json:"questions"`
	ControllerInterventions int            `json:"controller_interventions"`
	PermissionDenials       int            `json:"permission_denials"`
	ReviewPrompt            string         `json:"review_prompt"`
	Review                  *Review        `json:"review,omitempty"`
	Directory               string         `json:"directory"`
}
type Result struct {
	Schema              int       `json:"schema_version"`
	Suite               string    `json:"suite_version"`
	SuiteDigest         string    `json:"suite_digest"`
	Version             string    `json:"collo_version"`
	Commit              string    `json:"collo_commit"`
	Provider            string    `json:"provider"`
	ProviderType        string    `json:"provider_type"`
	Model               string    `json:"model"`
	ModelSettingsDigest string    `json:"model_settings_digest"`
	Platform            string    `json:"platform"`
	Settings            Settings  `json:"settings"`
	TaskIDs             []string  `json:"task_ids"`
	Started             time.Time `json:"started"`
	State               string    `json:"state"`
	StopReason          string    `json:"stop_reason,omitempty"`
	Trials              []Trial   `json:"trials"`
}

func Save(directory string, result Result) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err = replaceFile(directory, "results.json", append(data, '\n')); err != nil {
		return err
	}
	return replaceFile(directory, "report.md", []byte(Report(result)))
}

func replaceFile(directory, name string, data []byte) error {
	file, err := os.CreateTemp(directory, ".quality-*")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(directory, name))
}

func Load(directory string) (Result, error) {
	var result Result
	file, err := os.Open(filepath.Join(directory, "results.json"))
	if err != nil {
		return result, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return result, err
	}
	if info.Size() > 16<<20 {
		return result, errors.New("results.json exceeds 16 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, (16<<20)+1))
	if err = decoder.Decode(&result); err != nil {
		return result, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return result, errors.New("results.json must contain exactly one JSON object")
	}
	if result.Schema != 1 || result.Suite == "" {
		return result, fmt.Errorf("unsupported evaluation result schema %d", result.Schema)
	}
	return result, nil
}
