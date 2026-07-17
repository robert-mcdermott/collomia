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
	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
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
	command, cwd, provider, model, autonomy string
	plan, global, help, version             bool
	args                                    []string
}

func run(args []string) error {
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if opts.command == "run" {
		return runNonInteractive(ctx, opts)
	}
	broker := tui.NewApprovalBroker()
	runtime, err := app.New(ctx, app.Options{Workspace: opts.cwd, Provider: opts.provider, Model: opts.model, Autonomy: opts.autonomy, Plan: opts.plan, Approver: broker.Approve})
	if err != nil {
		return err
	}
	defer runtime.Close()
	initial := strings.Join(opts.args, " ")
	program := tea.NewProgram(tui.New(runtime, broker, initial), tea.WithAltScreen())
	_, err = program.Run()
	return err
}

func runNonInteractive(ctx context.Context, opts options) error {
	runtime, err := app.New(ctx, app.Options{Workspace: opts.cwd, Provider: opts.provider, Model: opts.model, Autonomy: opts.autonomy, Plan: opts.plan})
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
	_, err = runtime.Agent.Run(ctx, prompt, func(event agent.Event) {
		switch event.Kind {
		case agent.EventDelta:
			fmt.Print(event.Text)
		case agent.EventToolStart:
			fmt.Fprintf(os.Stderr, "\n◆ %s  %s\n", event.Tool, event.Text)
		case agent.EventToolResult:
			if event.Err != nil {
				fmt.Fprintf(os.Stderr, "  %v\n", event.Err)
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
		if opts.command == "tui" && len(opts.args) == 0 && (arg == "run" || arg == "init" || arg == "version") {
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
  collo version                       print build information

Flags:
  --cwd <path>                         workspace (default: current directory)
  --provider <name>                    configured provider name
  --model <id>                         model or deployment ID
  --autonomy ask|workspace|autopilot   permission policy
  --autopilot                          shorthand for --autonomy autopilot
  --plan                               start in read-only planning mode
  -h, --help                           show help
  -v, --version                        show version

Inside the TUI, use /help to see slash commands.
`
