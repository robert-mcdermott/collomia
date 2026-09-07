package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/quality"
)

const evalHelp = `Usage:
  collo eval list
  collo eval run --live --provider NAME --output NEW_DIRECTORY [options]
  collo eval report DIRECTORY
  collo eval compare BEFORE_DIRECTORY AFTER_DIRECTORY
  collo eval review DIRECTORY --task ID --trial 1 --accepted true --note TEXT
  collo schema eval

Run options:
  --cwd PATH             load provider configuration here (default: current directory)
  --model ID             override the configured model
  --tasks ID,ID          select tasks (default: all 12 balanced tasks)
  --trials N             repetitions per task (default: 1; maximum: 10)
  --token-budget N       cumulative per-task input + output tokens (default: 40000)
  --total-token-budget N total batch tokens (default: 480000)
  --cost-budget USD      estimated per-task cost cap; requires configured pricing
  --timeout SECONDS      per-task agent timeout (default: 180)
  --max-iterations N     provider turns per task turn (default: 16)

Only run --live makes provider requests. Each trial uses a fresh synthetic
workspace and required OS command containment. No configured hooks, MCP, skills,
or agent profiles run. Report/compare/review never execute traces or model code.
`

func runEvalCommand(args []string) error { return evalCommand(args, os.Stdout, os.Stderr) }
func evalCommand(args []string, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, evalHelp)
		return nil
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("eval list takes no arguments")
		}
		fmt.Fprintf(out, "%s\n", quality.SuiteVersion)
		for _, task := range quality.Suite() {
			fmt.Fprintf(out, "%-22s %-10s %s\n", task.ID, task.Mode, task.Category)
		}
		return nil
	case "report":
		if len(args) != 2 {
			return errors.New("eval report requires a directory")
		}
		r, err := quality.Load(args[1])
		if err != nil {
			return err
		}
		fmt.Fprint(out, quality.Report(r))
		return nil
	case "compare":
		if len(args) != 3 {
			return errors.New("eval compare requires two directories")
		}
		before, err := quality.Load(args[1])
		if err != nil {
			return err
		}
		after, err := quality.Load(args[2])
		if err != nil {
			return err
		}
		report, err := quality.Compare(before, after)
		if err == nil {
			fmt.Fprint(out, report)
		}
		return err
	case "review":
		return evalReview(args[1:], out, errOut)
	case "run":
		flags := flag.NewFlagSet("eval run", flag.ContinueOnError)
		flags.SetOutput(errOut)
		var cwd, name, model, directory, ids string
		var live bool
		settings := quality.DefaultSettings()
		flags.StringVar(&cwd, "cwd", "", "provider configuration directory")
		flags.StringVar(&name, "provider", "", "configured provider name")
		flags.StringVar(&model, "model", "", "model override")
		flags.StringVar(&directory, "output", "", "new output directory")
		flags.StringVar(&ids, "tasks", "", "comma-separated task IDs")
		flags.BoolVar(&live, "live", false, "explicitly allow provider requests")
		flags.IntVar(&settings.Trials, "trials", settings.Trials, "trials per task")
		flags.IntVar(&settings.TokenBudget, "token-budget", settings.TokenBudget, "tokens per task")
		flags.IntVar(&settings.TotalTokens, "total-token-budget", settings.TotalTokens, "total tokens")
		flags.IntVar(&settings.TimeoutSeconds, "timeout", settings.TimeoutSeconds, "seconds per task")
		flags.IntVar(&settings.MaxIterations, "max-iterations", settings.MaxIterations, "iterations per task turn")
		flags.Float64Var(&settings.CostBudget, "cost-budget", 0, "estimated USD per task")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected eval run positional arguments")
		}
		if !live || name == "" || directory == "" {
			return errors.New("eval run requires --live, --provider, and --output; use eval list to inspect tasks without model calls")
		}
		if err := settings.Validate(); err != nil {
			return err
		}
		tasks, err := quality.Select(ids)
		if err != nil {
			return err
		}
		if cwd == "" {
			cwd, err = os.Getwd()
			if err != nil {
				return err
			}
		}
		directory, err = filepath.Abs(directory)
		if err != nil {
			return err
		}
		cfg, err := appconfig.Load(cwd)
		if err != nil {
			return err
		}
		name, p, model, err := cfg.Selected(name, model)
		if err != nil {
			return err
		}
		redactor := app.NewRedactor(cfg)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		fmt.Fprintf(out, "Running %d tasks x %d trial(s) with %s/%s; per-task %d tokens, %ds; total %d tokens.\n", len(tasks), settings.Trials, name, model, settings.TokenBudget, settings.TimeoutSeconds, settings.TotalTokens)
		result, err := quality.Run(ctx, quality.Options{Directory: directory, Provider: name, Model: model, ProviderConfig: p, Settings: settings, Tasks: tasks, Redact: redactor.Redact, Progress: func(message string) { fmt.Fprintln(out, message) }})
		if err != nil {
			return errors.New(redactor.Redact(err.Error()))
		}
		fmt.Fprintf(out, "Scorecard: %s\n", filepath.Join(directory, "report.md"))
		if result.State != "complete" {
			return fmt.Errorf("evaluation stopped: %s", result.StopReason)
		}
		return nil
	default:
		return fmt.Errorf("unknown eval subcommand %q", args[0])
	}
}

func evalReview(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("eval review requires a directory")
	}
	directory := args[0]
	flags := flag.NewFlagSet("eval review", flag.ContinueOnError)
	flags.SetOutput(errOut)
	var id, accepted, note string
	trial, questions, repeats := 1, -1, -1
	flags.StringVar(&id, "task", "", "task ID")
	flags.IntVar(&trial, "trial", 1, "trial number")
	flags.StringVar(&accepted, "accepted", "", "true or false")
	flags.StringVar(&note, "note", "", "review evidence")
	flags.IntVar(&questions, "unnecessary-questions", -1, "reviewed unnecessary question count")
	flags.IntVar(&repeats, "wasted-repeats", -1, "reviewed wasted repeat count")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() > 0 || id == "" || strings.TrimSpace(note) == "" || len(note) > 4000 || questions < -1 || repeats < -1 {
		return errors.New("review requires --task, --accepted, and --note (at most 4000 bytes); counts must be nonnegative when supplied")
	}
	decision, err := strconv.ParseBool(accepted)
	if err != nil {
		return errors.New("--accepted must be true or false")
	}
	r, err := quality.Load(directory)
	if err != nil {
		return err
	}
	if r.State == "running" {
		return errors.New("wait for the evaluation to finish or stop before recording a review")
	}
	for i := range r.Trials {
		row := &r.Trials[i]
		if row.Task != id || row.Trial != trial {
			continue
		}
		if questions > 100 || repeats > row.RepeatedToolCalls {
			return errors.New("reviewed questions must be at most 100; wasted repeats cannot exceed observed repeated tool calls")
		}
		review := &quality.Review{Accepted: decision, Note: note, Time: time.Now().UTC()}
		if questions >= 0 {
			review.UnnecessaryQuestions = &questions
		}
		if repeats >= 0 {
			review.WastedRepeats = &repeats
		}
		row.Review = review
		if err := quality.Save(directory, r); err != nil {
			return err
		}
		fmt.Fprintln(out, "Review recorded; report.md updated.")
		return nil
	}
	return errors.New("task/trial not found")
}
