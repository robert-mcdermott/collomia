package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/redact"
	"github.com/robert-mcdermott/collomia/internal/tui"
	"github.com/robert-mcdermott/collomia/internal/version"
	"github.com/robert-mcdermott/collomia/internal/webterminal"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var exitErr *webterminal.ExitError
		if errors.As(err, &exitErr) && exitErr.Code > 0 {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, "collo:", err)
		os.Exit(exitCode(err))
	}
}

const (
	exitFailure   = 1
	exitUsage     = 2
	exitCancelled = 130
)

// commandError carries the stable process exit code and final-result failure
// classification without changing the underlying human-readable error.
type commandError struct {
	code int
	kind event.FailureKind
	err  error
}

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }

func withCommandError(err error, code int, kind event.FailureKind) error {
	if err == nil {
		return nil
	}
	var existing *commandError
	if errors.As(err, &existing) {
		return err
	}
	return &commandError{code: code, kind: kind, err: err}
}

func classifyCommandError(err error) error {
	if err == nil {
		return nil
	}
	var existing *commandError
	if errors.As(err, &existing) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return withCommandError(err, exitCancelled, event.FailureCancelled)
	}
	var validation appconfig.ValidationError
	if errors.As(err, &validation) {
		return withCommandError(err, exitUsage, event.FailureConfiguration)
	}
	return err
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var commandErr *commandError
	if errors.As(err, &commandErr) && commandErr.code > 0 {
		return commandErr.code
	}
	if errors.Is(err, context.Canceled) {
		return exitCancelled
	}
	return exitFailure
}

type options struct {
	command, cwd, provider, model, autonomy       string
	output                                        string
	resume                                        string
	mcpURL                                        string
	webPort, mcpTimeout                           int
	plan, global, help, version, jsonl, ephemeral bool
	strict, revoke, status, debug, markdown, yes  bool
	check                                         bool
	includeLogs                                   bool
	cont, withReference, web, webPortSet, noOpen  bool
	mcpTimeoutSet                                 bool
	altScreen                                     *bool
	mcpEnv, mcpHeaders                            []string
	args                                          []string
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "__landlock" {
		return runLandlockShim(args[1:])
	}
	if len(args) > 0 && args[0] == "__appcontainer" {
		return runAppContainerShim(args[1:])
	}
	opts, err := parse(args)
	if err != nil {
		return withCommandError(err, exitUsage, event.FailureUsage)
	}
	runStarted := time.Now()
	if opts.help {
		fmt.Print(helpText)
		return nil
	}
	if opts.version || opts.command == "version" {
		fmt.Println(version.String())
		return nil
	}
	if opts.command == "replay" {
		return runReplayCommand(opts)
	}
	if opts.cwd == "" {
		opts.cwd, err = os.Getwd()
		if err != nil {
			return headlessStartupFailure(opts, classifyCommandError(err), runStarted)
		}
	}
	opts.cwd, err = filepath.Abs(opts.cwd)
	if err != nil {
		return headlessStartupFailure(opts, classifyCommandError(err), runStarted)
	}
	if opts.command == "init" {
		path := filepath.Join(opts.cwd, appconfig.ProjectFile)
		if opts.global {
			path, err = appconfig.GlobalPath()
			if err != nil {
				return err
			}
		}
		paths := []string{path}
		if opts.withReference {
			paths = append(paths, appconfig.ReferencePath(path))
		}
		for _, candidate := range paths {
			if _, statErr := os.Stat(candidate); statErr == nil {
				return fmt.Errorf("file already exists: %s", candidate)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
		}
		if err = appconfig.WriteStarter(path, opts.global); err != nil {
			return err
		}
		fmt.Println("Created", path)
		if opts.withReference {
			referencePath := appconfig.ReferencePath(path)
			if err = appconfig.WriteReference(referencePath); err != nil {
				return err
			}
			fmt.Println("Created", referencePath, "(reference only; not loaded)")
		}
		fmt.Println("Review every setting with `collo config reference`.")
		fmt.Println("Validate changes with `collo config validate --strict`.")
		if opts.global {
			fmt.Println("Set provider API keys through the environment; the starter includes Ollama and OpenRouter examples.")
		} else {
			fmt.Println("After reviewing project settings, run `collo trust` to enable them.")
		}
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
	case "support":
		return runSupportCommand(opts)
	case "policy":
		return runPolicyCommand(opts)
	case "sessions":
		return runSessionsCommand(opts)
	case "skills":
		return runSkillsCommand(opts)
	case "mcp":
		return runMCPCommand(opts)
	case "completion":
		return runCompletionCommand(opts)
	case "schema":
		if err := runSchemaCommand(opts); err != nil {
			return withCommandError(err, exitUsage, event.FailureUsage)
		}
		return nil
	}
	if opts.web {
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find collo executable: %w", err)
		}
		return webterminal.Run(context.Background(), webterminal.Options{
			Executable:  executable,
			Args:        tuiChildArgs(opts),
			Dir:         opts.cwd,
			Env:         os.Environ(),
			Port:        opts.webPort,
			OpenBrowser: !opts.noOpen,
			Stderr:      os.Stderr,
		})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if opts.command == "review" {
		ref, instructions := "", ""
		if len(opts.args) > 0 {
			ref = opts.args[0]
			instructions = strings.Join(opts.args[1:], " ")
		}
		opts.args = []string{app.ReviewPrompt(ref, instructions)}
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
	altScreen := runtime.Config.Options.AlternateScreen
	if opts.altScreen != nil {
		altScreen = *opts.altScreen
	}
	programOptions := []tea.ProgramOption{}
	if altScreen {
		programOptions = append(programOptions, tea.WithAltScreen())
	}
	program := tea.NewProgram(tui.New(runtime, broker, initial), programOptions...)
	_, err = program.Run()
	tui.ResetTerminalBackground()
	return err
}

// usagePtr converts session usage to the event payload shape, omitting the
// field entirely when the provider reported nothing.
func usagePtr(u provider.Usage) *event.Usage {
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return nil
	}
	return &event.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.CachedTokens, ReasoningTokens: u.ReasoningTokens}
}

// runObservation collects the small amount of cross-event state needed by
// run.result. Emit callbacks may arrive concurrently (for example, process
// stdout/stderr), so observation must be safe even though JSONLWriter already
// serializes the lines themselves.
type runObservation struct {
	mu             sync.Mutex
	streamedAnswer strings.Builder
	refused        bool
	progressed     bool
}

func (o *runObservation) Observe(e event.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch e.Kind {
	case event.KindTextDelta:
		o.streamedAnswer.WriteString(e.Text)
		o.progressed = true
	case event.KindReasoningDelta, event.KindToolCallDelta, event.KindToolStart, event.KindToolOutput, event.KindToolResult:
		o.progressed = true
	case event.KindPermissionDecision:
		o.progressed = true
		if e.Permission != nil && !e.Permission.Allowed {
			o.refused = true
		}
	}
}

func (o *runObservation) Snapshot() (streamedAnswer string, refused, progressed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.streamedAnswer.String(), o.refused, o.progressed
}

func runNonInteractive(ctx context.Context, opts options) (runErr error) {
	started := time.Now()
	var runtime *app.Runtime
	var answer string
	var observation runObservation
	var writer *event.JSONLWriter
	if opts.jsonl {
		writer = event.NewJSONLWriter(os.Stdout)
		writer.Redact = redact.New().Redact
	}
	defer func() {
		if writer != nil {
			streamedAnswer, refused, progressed := observation.Snapshot()
			if runErr != nil && answer == "" {
				answer = streamedAnswer
			}
			emitRunResult(writer, runtime, opts, answer, refused, progressed, runErr, started)
		}
		if runtime != nil {
			runtime.Close()
		}
	}()

	prompt := strings.TrimSpace(strings.Join(opts.args, " "))
	if prompt == "" {
		data, readErr := io.ReadAll(io.LimitReader(os.Stdin, 4*1024*1024))
		if readErr != nil {
			return classifyCommandError(readErr)
		}
		prompt = strings.TrimSpace(string(data))
	}
	if prompt == "" {
		return withCommandError(errors.New("run requires a prompt argument or stdin"), exitUsage, event.FailureUsage)
	}

	var err error
	runtime, err = app.New(ctx, app.Options{Workspace: opts.cwd, Provider: opts.provider, Model: opts.model, Autonomy: opts.autonomy, Plan: opts.plan, Debug: opts.debug, Ephemeral: opts.ephemeral, Resume: opts.resume, Continue: opts.cont})
	if err != nil {
		return classifyCommandError(err)
	}
	if opts.jsonl {
		writer.Redact = runtime.Redactor.Redact
		answer, err = runtime.Agent.Run(ctx, prompt, func(e event.Event) {
			runtime.LogEvent(e)
			observation.Observe(e)
			writer.Handle(e)
		})
		if persistenceErr := runtime.PersistenceError(); persistenceErr != nil {
			persistenceErr = fmt.Errorf("session persistence failed: %w", persistenceErr)
			if err == nil {
				err = persistenceErr
			} else {
				err = errors.Join(err, persistenceErr)
			}
		}
		return classifyCommandError(err)
	}
	answer, err = runtime.Agent.Run(ctx, prompt, func(e event.Event) {
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
	if persistenceErr := runtime.PersistenceError(); persistenceErr != nil {
		persistenceErr = fmt.Errorf("session persistence failed: %w", persistenceErr)
		if err == nil {
			err = persistenceErr
		} else {
			err = errors.Join(err, persistenceErr)
		}
	}
	if err == nil {
		fmt.Println()
	}
	return classifyCommandError(err)
}

func emitRunResult(writer *event.JSONLWriter, runtime *app.Runtime, opts options, answer string, refused, progressed bool, runErr error, started time.Time) {
	result := event.RunResult{Status: "ok", Answer: answer, Ephemeral: opts.ephemeral, Refused: refused, DurationMS: time.Since(started).Milliseconds()}
	var usage *event.Usage
	if runtime != nil {
		result.ChangedFiles = runtime.Changes.Changed()
		usage = usagePtr(runtime.Agent.Usage())
		if runtime.Session != nil {
			result.SessionID = runtime.Session.Meta.ID
		}
	}
	if runErr != nil {
		result.Status = "error"
		result.Error = runErr.Error()
		failure := failureFor(runErr)
		result.Failure = &failure
		if failure.Kind == event.FailureCancelled {
			result.Status = "cancelled"
		}
		result.Partial = progressed || strings.TrimSpace(answer) != "" || len(result.ChangedFiles) > 0
	}
	final := event.New(event.KindRunResult)
	final.Result = &result
	final.Usage = usage
	if runtime != nil {
		runtime.LogEvent(final)
	}
	writer.Handle(final)
}

func failureFor(err error) event.Failure {
	if providerErr, ok := provider.AsError(err); ok {
		kind := event.FailureProvider
		if providerErr.Kind == provider.ErrorCancelled {
			kind = event.FailureCancelled
		} else if providerErr.Kind == provider.ErrorTimeout {
			kind = event.FailureTimeout
		}
		return event.Failure{Kind: kind, Retryable: providerErr.Retryable, Provider: &event.ProviderFailure{
			Name: providerErr.Provider, Operation: providerErr.Operation, Kind: string(providerErr.Kind), StatusCode: providerErr.StatusCode,
			Retryable: providerErr.Retryable, RetryAfterMS: providerErr.RetryAfter.Milliseconds(), RequestID: providerErr.RequestID,
		}}
	}
	var commandErr *commandError
	if errors.As(err, &commandErr) && commandErr.kind != "" {
		return event.Failure{Kind: commandErr.kind}
	}
	if errors.Is(err, context.Canceled) {
		return event.Failure{Kind: event.FailureCancelled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return event.Failure{Kind: event.FailureTimeout, Retryable: true}
	}
	if errors.Is(err, permission.ErrDenied) {
		return event.Failure{Kind: event.FailurePermission}
	}
	var validation appconfig.ValidationError
	if errors.As(err, &validation) {
		return event.Failure{Kind: event.FailureConfiguration}
	}
	return event.Failure{Kind: event.FailureRuntime}
}

func headlessStartupFailure(opts options, err error, started time.Time) error {
	if opts.jsonl && (opts.command == "run" || opts.command == "review" || opts.command == "verify") {
		writer := event.NewJSONLWriter(os.Stdout)
		writer.Redact = redact.New().Redact
		emitRunResult(writer, nil, opts, "", false, false, err, started)
	}
	return err
}

func parse(args []string) (options, error) {
	opts := options{command: "tui"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if opts.command == "tui" && len(opts.args) == 0 && (arg == "tui" || arg == "run" || arg == "init" || arg == "version" || arg == "config" || arg == "trust" || arg == "doctor" || arg == "capabilities" || arg == "support" || arg == "policy" || arg == "sessions" || arg == "skills" || arg == "mcp" || arg == "review" || arg == "verify" || arg == "completion" || arg == "schema" || arg == "replay") {
			opts.command = arg
			continue
		}
		switch {
		case arg == "--":
			opts.args = append(opts.args, args[i+1:]...)
			i = len(args)
		case arg == "-" && opts.command == "replay":
			opts.args = append(opts.args, arg)
		case arg == "-h" || arg == "--help":
			opts.help = true
		case arg == "-v" || arg == "--version":
			opts.version = true
		case arg == "--plan":
			opts.plan = true
		case arg == "--jsonl":
			opts.jsonl = true
		case arg == "--ephemeral":
			opts.ephemeral = true
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
		case arg == "--check":
			opts.check = true
		case arg == "--include-logs":
			opts.includeLogs = true
		case strings.HasPrefix(arg, "--output="):
			opts.output = strings.TrimPrefix(arg, "--output=")
			if opts.output == "" {
				return opts, fmt.Errorf("--output requires a path")
			}
		case arg == "--output":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--output requires a path")
			}
			i++
			opts.output = args[i]
		case arg == "--continue":
			opts.cont = true
		case arg == "--web":
			opts.web = true
		case arg == "--no-open":
			opts.noOpen = true
		case arg == "--no-alt-screen":
			value := false
			opts.altScreen = &value
		case arg == "--alt-screen":
			value := true
			opts.altScreen = &value
		case strings.HasPrefix(arg, "--web-port="):
			value := strings.TrimPrefix(arg, "--web-port=")
			port, parseErr := strconv.Atoi(value)
			if parseErr != nil || port < 0 || port > 65535 {
				return opts, fmt.Errorf("--web-port requires a number between 0 and 65535")
			}
			opts.webPort, opts.webPortSet = port, true
		case arg == "--web-port":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--web-port requires a number between 0 and 65535")
			}
			i++
			port, parseErr := strconv.Atoi(args[i])
			if parseErr != nil || port < 0 || port > 65535 {
				return opts, fmt.Errorf("--web-port requires a number between 0 and 65535")
			}
			opts.webPort, opts.webPortSet = port, true
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
		case arg == "--with-reference":
			opts.withReference = true
		case strings.HasPrefix(arg, "--url="):
			opts.mcpURL = strings.TrimPrefix(arg, "--url=")
		case arg == "--url":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--url requires an MCP endpoint")
			}
			i++
			opts.mcpURL = args[i]
		case strings.HasPrefix(arg, "--timeout="):
			value := strings.TrimPrefix(arg, "--timeout=")
			timeout, parseErr := strconv.Atoi(value)
			if parseErr != nil || timeout <= 0 {
				return opts, fmt.Errorf("--timeout requires a positive number of seconds")
			}
			opts.mcpTimeout, opts.mcpTimeoutSet = timeout, true
		case arg == "--timeout":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--timeout requires a positive number of seconds")
			}
			i++
			timeout, parseErr := strconv.Atoi(args[i])
			if parseErr != nil || timeout <= 0 {
				return opts, fmt.Errorf("--timeout requires a positive number of seconds")
			}
			opts.mcpTimeout, opts.mcpTimeoutSet = timeout, true
		case strings.HasPrefix(arg, "--env="):
			opts.mcpEnv = append(opts.mcpEnv, strings.TrimPrefix(arg, "--env="))
		case arg == "--env":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--env requires KEY=VALUE")
			}
			i++
			opts.mcpEnv = append(opts.mcpEnv, args[i])
		case strings.HasPrefix(arg, "--header="):
			opts.mcpHeaders = append(opts.mcpHeaders, strings.TrimPrefix(arg, "--header="))
		case arg == "--header":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--header requires KEY=VALUE")
			}
			i++
			opts.mcpHeaders = append(opts.mcpHeaders, args[i])
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
	if opts.web && opts.command != "tui" {
		return opts, fmt.Errorf("--web is only available for the interactive TUI")
	}
	if !opts.web && (opts.webPortSet || opts.noOpen) {
		return opts, fmt.Errorf("--web-port and --no-open require --web")
	}
	if opts.command != "mcp" && (opts.mcpURL != "" || opts.mcpTimeoutSet || len(opts.mcpEnv) > 0 || len(opts.mcpHeaders) > 0) {
		return opts, fmt.Errorf("--url, --env, --header, and --timeout are only available for `collo mcp add`")
	}
	if opts.ephemeral && opts.command != "run" {
		return opts, fmt.Errorf("--ephemeral is only available for `collo run`")
	}
	if opts.ephemeral && (opts.resume != "" || opts.cont) {
		return opts, fmt.Errorf("--ephemeral cannot be combined with --resume or --continue")
	}
	if opts.check && opts.command != "replay" {
		return opts, fmt.Errorf("--check is only available for `collo replay`")
	}
	if opts.command != "support" && (opts.includeLogs || opts.output != "") {
		return opts, fmt.Errorf("--include-logs and --output are only available for `collo support bundle`")
	}
	return opts, nil
}

func tuiChildArgs(opts options) []string {
	args := []string{"tui", "--cwd", opts.cwd}
	if opts.provider != "" {
		args = append(args, "--provider", opts.provider)
	}
	if opts.model != "" {
		args = append(args, "--model", opts.model)
	}
	if opts.autonomy != "" {
		args = append(args, "--autonomy", opts.autonomy)
	}
	if opts.plan {
		args = append(args, "--plan")
	}
	if opts.resume != "" {
		args = append(args, "--resume", opts.resume)
	}
	if opts.cont {
		args = append(args, "--continue")
	}
	if opts.debug {
		args = append(args, "--debug")
	}
	if opts.altScreen != nil {
		if *opts.altScreen {
			args = append(args, "--alt-screen")
		} else {
			args = append(args, "--no-alt-screen")
		}
	}
	if len(opts.args) > 0 {
		args = append(args, "--")
		args = append(args, opts.args...)
	}
	return args
}

const helpText = `Collomia — a safe, multi-provider terminal coding agent

Usage:
  collo [flags] [initial prompt]      start the interactive TUI
  collo --web [flags] [initial prompt]  open the interactive TUI in a local browser
  collo run [flags] <prompt>          run once (or read the prompt from stdin)
  collo init [--with-reference]       write project .collomia.json
  collo init --global [--with-reference]  write the user-wide .collomia/config.json
  collo config validate [--strict]    validate configuration with field-level errors
  collo config show                   print the effective configuration and its layers
  collo config reference              print the exhaustive annotated configuration reference
  collo trust [--status|--revoke]     review and trust this workspace's project config
  collo doctor [--strict]             diagnose config, terminal, git, providers, MCP, sandbox
  collo capabilities [--markdown]     print the product capability matrix
  collo support bundle [--output path] [--include-logs]  create a privacy-conscious diagnostic archive
  collo policy check <command…>       evaluate a command against permission rules without running it
  collo review [ref] [instructions…]  review pending changes ('-' = uncommitted) with optional focus, headlessly
  collo verify [focus]                detect and run this project's build/lint/test commands headlessly
  collo sessions [list|show|fork|rewind|rename|archive|unarchive|delete]  manage saved sessions
  collo skills [list|show|new|install|update|remove|enable|disable]  manage agent skills (project and --global scopes)
  collo mcp [list|show|add|remove|enable|disable|test]  manage persistent MCP servers (project and --global scopes)
  collo completion bash|zsh|fish|powershell  generate shell completion
  collo schema events                 print the embedded JSON Schema for JSONL events
  collo replay [--check] <trace|->    validate and safely render a completed JSONL run trace
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
  --web                                serve the TUI in an authenticated local browser terminal (macOS/Linux)
  --web-port <port>                    local browser-terminal port (default: random available port)
  --no-open                            (web) print the URL without opening the default browser
  --alt-screen                         force the interactive TUI to use the alternate screen
  --no-alt-screen                      keep the final TUI frame in terminal scrollback
  --jsonl                              (run) emit schema-versioned JSONL events on stdout; the final line is a run.result summary (status ok|error|cancelled)
  --ephemeral                          (run) do not create or update a durable conversation session; audit and workspace changes still apply
  --check                              (replay) validate the trace and print only its summary
  --output <path>                      (support bundle) archive path (default: timestamped ZIP under ~/.collomia/support)
  --include-logs                       (support bundle) include up to five recent bounded, redacted debug logs
  --debug                              write a redacted debug log (see collo doctor for path)
  --global                             target the user-wide config for init, skills, or MCP management
  --url <endpoint>                     (mcp add) configure a Streamable HTTP server
  --env KEY=VALUE                      (mcp add) add a stdio environment value; repeatable
  --header KEY=VALUE                   (mcp add) add an HTTP header; repeatable
  --timeout <seconds>                  (mcp add) connection and catalog timeout (default: 30)
  --with-reference                     (init) also write the non-loaded annotated JSONC reference
  -h, --help                           show help
  -v, --version                        show version

Environment:
  COLLO_PROVIDER / COLLO_MODEL         override provider and model selection

Inside the TUI, use /help to see slash commands.
`
