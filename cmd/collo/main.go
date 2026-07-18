package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/tui"
	"github.com/robert-mcdermott/collomia/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "collo:", err)
		os.Exit(1)
	}
}

type options struct {
	command, cwd, provider, model, autonomy      string
	resume                                       string
	plan, global, help, version, jsonl           bool
	strict, revoke, status, debug, markdown, yes bool
	cont                                         bool
	args                                         []string
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "__landlock" {
		return runLandlockShim(args[1:])
	}
	opts, err := parse(args)
	if err != nil {
		return err
	}
	if opts.help {
		fmt.Print(helpText)
		return nil
	}
	if opts.version || opts.command == "version" {
		fmt.Println(version.String())
		return nil
	}
	if opts.cwd == "" {
		opts.cwd, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	opts.cwd, err = filepath.Abs(opts.cwd)
	if err != nil {
		return err
	}
	if opts.command == "init" {
		path := filepath.Join(opts.cwd, appconfig.ProjectFile)
		if opts.global {
			path, err = appconfig.GlobalPath()
			if err != nil {
				return err
			}
		}
		if _, statErr := os.Stat(path); statErr == nil {
			return fmt.Errorf("configuration already exists: %s", path)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if err = appconfig.WriteExample(path); err != nil {
			return err
		}
		fmt.Println("Created", path)
		fmt.Println("Edit provider endpoints and use environment variables for API keys.")
		return nil
	}
	switch opts.command {
	case "config":
		return runConfigCommand(opts)
	case "trust":
		return runTrustCommand(opts)
	case "doctor":
		return runDoctorCommand(opts)
	case "capabilities":
		return runCapabilitiesCommand(opts)
	case "policy":
		return runPolicyCommand(opts)
	case "sessions":
		return runSessionsCommand(opts)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if opts.command == "review" {
		ref := strings.Join(opts.args, " ")
		opts.args = []string{app.ReviewPrompt(strings.TrimSpace(ref))}
		return runNonInteractive(ctx, opts)
	}
	if opts.command == "verify" {
		focus := strings.Join(opts.args, " ")
		opts.args = []string{app.VerifyPrompt(strings.TrimSpace(focus))}
		return runNonInteractive(ctx, opts)
	}
	if opts.command == "run" {
		return runNonInteractive(ctx, opts)
	}
	broker := tui.NewApprovalBroker()
	runtime, err := app.New(ctx, app.Options{Workspace: opts.cwd, Provider: opts.provider, Model: opts.model, Autonomy: opts.autonomy, Plan: opts.plan, Debug: opts.debug, Resume: opts.resume, Continue: opts.cont, Approver: broker.Approve, Asker: func(ctx context.Context, question string, options []string) (string, error) {
		return broker.Ask(ctx, tui.Question{Text: question, Options: options})
	}})
	if err != nil {
		return err
	}
	defer runtime.Close()
	initial := strings.Join(opts.args, " ")
	program := tea.NewProgram(tui.New(runtime, broker, initial), tea.WithAltScreen())
	_, err = program.Run()
	tui.ResetTerminalBackground()
	return err
}

func runNonInteractive(ctx context.Context, opts options) error {
	runtime, err := app.New(ctx, app.Options{Workspace: opts.cwd, Provider: opts.provider, Model: opts.model, Autonomy: opts.autonomy, Plan: opts.plan, Debug: opts.debug, Resume: opts.resume, Continue: opts.cont})
	if err != nil {
		return err
	}
	defer runtime.Close()
	prompt := strings.TrimSpace(strings.Join(opts.args, " "))
	if prompt == "" {
		data, readErr := io.ReadAll(io.LimitReader(os.Stdin, 4*1024*1024))
		if readErr != nil {
			return readErr
		}
		prompt = strings.TrimSpace(string(data))
	}
	if prompt == "" {
		return errors.New("run requires a prompt argument or stdin")
	}
	if opts.jsonl {
		writer := event.NewJSONLWriter(os.Stdout)
		writer.Redact = runtime.Redactor.Redact
		_, err = runtime.Agent.Run(ctx, prompt, func(e event.Event) {
			runtime.LogEvent(e)
			writer.Handle(e)
		})
		return err
	}
	_, err = runtime.Agent.Run(ctx, prompt, func(e event.Event) {
		runtime.LogEvent(e)
		switch e.Kind {
		case event.KindTextDelta:
			fmt.Print(e.Text)
		case event.KindToolStart:
			if e.Tool != nil {
				fmt.Fprintf(os.Stderr, "\n◆ %s  %s\n", e.Tool.Name, e.Tool.Summary)
			}
		case event.KindToolOutput:
			if e.Tool != nil {
				fmt.Fprint(os.Stderr, e.Tool.Output)
			}
		case event.KindToolResult:
			if e.Tool != nil && e.Tool.IsError {
				fmt.Fprintf(os.Stderr, "  ✗ %s failed\n", e.Tool.Name)
			}
		}
	})
	if err == nil {
		fmt.Println()
	}
	return err
}

func parse(args []string) (options, error) {
	opts := options{command: "tui"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if opts.command == "tui" && len(opts.args) == 0 && (arg == "run" || arg == "init" || arg == "version" || arg == "config" || arg == "trust" || arg == "doctor" || arg == "capabilities" || arg == "policy" || arg == "sessions" || arg == "review" || arg == "verify") {
			opts.command = arg
			continue
		}
		switch {
		case arg == "--":
			opts.args = append(opts.args, args[i+1:]...)
			i = len(args)
		case arg == "-h" || arg == "--help":
			opts.help = true
		case arg == "-v" || arg == "--version":
			opts.version = true
		case arg == "--plan":
			opts.plan = true
		case arg == "--jsonl":
			opts.jsonl = true
		case arg == "--strict":
			opts.strict = true
		case arg == "--revoke":
			opts.revoke = true
		case arg == "--status":
			opts.status = true
		case arg == "--debug":
			opts.debug = true
		case arg == "--markdown":
			opts.markdown = true
		case arg == "--yes":
			opts.yes = true
		case arg == "--continue":
			opts.cont = true
		case strings.HasPrefix(arg, "--resume="):
			opts.resume = strings.TrimPrefix(arg, "--resume=")
		case arg == "--resume":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--resume requires a session id")
			}
			i++
			opts.resume = args[i]
		case arg == "--autopilot":
			opts.autonomy = "autopilot"
		case arg == "--workspace":
			opts.autonomy = "workspace"
		case arg == "--global":
			opts.global = true
		case strings.HasPrefix(arg, "--cwd="):
			opts.cwd = strings.TrimPrefix(arg, "--cwd=")
		case strings.HasPrefix(arg, "--provider="):
			opts.provider = strings.TrimPrefix(arg, "--provider=")
		case strings.HasPrefix(arg, "--model="):
			opts.model = strings.TrimPrefix(arg, "--model=")
		case strings.HasPrefix(arg, "--autonomy="):
			opts.autonomy = strings.TrimPrefix(arg, "--autonomy=")
		case arg == "--cwd" || arg == "--provider" || arg == "--model" || arg == "--autonomy":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a value", arg)
			}
			i++
			value := args[i]
			switch arg {
			case "--cwd":
				opts.cwd = value
			case "--provider":
				opts.provider = value
			case "--model":
				opts.model = value
			case "--autonomy":
				opts.autonomy = value
			}
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown option %s", arg)
		default:
			opts.args = append(opts.args, arg)
		}
	}
	return opts, nil
}

const helpText = `Collomia — a safe, multi-provider terminal coding agent

Usage:
  collo [flags] [initial prompt]      start the interactive TUI
  collo run [flags] <prompt>          run once (or read the prompt from stdin)
  collo init [--global]               write an example configuration
  collo config validate [--strict]    validate configuration with field-level errors
  collo config show                   print the effective configuration and its layers
  collo trust [--status|--revoke]     review and trust this workspace's project config
  collo doctor [--strict]             diagnose config, terminal, git, providers, MCP, sandbox
  collo capabilities [--markdown]     print the product capability matrix
  collo policy check <command…>       evaluate a command against permission rules without running it
  collo review [ref]                  review pending changes (or changes vs a ref) headlessly
  collo verify [focus]                detect and run this project's build/lint/test commands headlessly
  collo sessions [list|show|fork|rename|archive|unarchive|delete]  manage saved sessions
  collo version                       print build information

Flags:
  --cwd <path>                         workspace (default: current directory)
  --provider <name>                    configured provider name
  --model <id>                         model or deployment ID
  --autonomy ask|workspace|autopilot   permission policy
  --autopilot                          shorthand for --autonomy autopilot
  --plan                               start in read-only planning mode
  --resume <id>                        resume a saved session
  --continue                           resume the most recent session
  --jsonl                              (run) emit schema-versioned JSONL events on stdout
  --debug                              write a redacted debug log (see collo doctor for path)
  -h, --help                           show help
  -v, --version                        show version

Environment:
  COLLO_PROVIDER / COLLO_MODEL         override provider and model selection

Inside the TUI, use /help to see slash commands.
`
