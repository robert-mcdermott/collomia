package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/setup"
	"github.com/robert-mcdermott/collomia/internal/tui"
	"golang.org/x/term"
)

// runSetupCommand implements the reusable `collo setup` flow: probe, discover,
// verify, and only then write.
//
// With `--provider <name>` it re-enters the same path pointed at a provider the
// file already has, which makes setup useful whenever a model changes. The
// re-run is not a
// separate mode: it skips the provider scan and keeps the existing credential,
// and every step after that — catalog, model, two verification requests, limit
// resolution, confirmation, write — is the ordinary one.
//
// It is a separate verb from `collo init` on purpose. `collo init` has a
// defined contract — write a starter file, refuse if one already exists — that
// is documented, guarded by tests, and used from scripts. Probe-verify-write
// is a different operation with a different failure model, and giving one verb
// two contracts is how a documented behavior quietly becomes two.
func runSetupCommand(opts options) error {
	outcome, err := runProviderSetup(context.Background(), opts, false)
	if err != nil {
		return err
	}
	if !outcome.Wrote {
		fmt.Println("Setup cancelled; nothing was written.")
		return nil
	}
	printSetupOutcome(outcome)
	fmt.Println("Start a session with `collo`, or inspect everything with `collo doctor`.")
	return nil
}

// runProviderSetup is shared by the explicit command and by interactive
// startup. Keeping one path matters: a provider proved on first launch must be
// written, merged, and verified exactly like one added six months later.
func runProviderSetup(ctx context.Context, opts options, continueToSession bool) (tui.SetupOutcome, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return tui.SetupOutcome{}, errors.New("collo setup is interactive and needs a terminal; scripted use must write an explicit provider configuration, then use `collo config validate --strict`")
	}

	path, err := setup.GlobalPath()
	if err != nil {
		return tui.SetupOutcome{}, err
	}

	// What the file already contains is read from that one file, not from the
	// merged configuration. `appconfig.Load` composes defaults, user, and a
	// trusted project layer, so a provider defined in a repository's
	// `.collomia.json` would appear here — and setup writes the global file.
	// Warning about a collision with an entry setup will not touch is
	// misleading, and the project layer would shadow the write besides.
	existing, err := setup.ReadExisting(path)
	if err != nil {
		return tui.SetupOutcome{}, fmt.Errorf("read %s: %w\nFix or move that file before running setup; it is not being overwritten", path, err)
	}

	// `--provider` re-enters the wizard pointed at a provider that is already
	// configured, which is what makes setup a thing to run whenever rather than
	// only on a first run. An unknown name lists what the file actually
	// contains: the alternative is dropping the user into the full provider
	// scan, where the run they asked for silently becomes a different one.
	reconfigure, err := reconfigureTarget(opts.provider, existing, path)
	if err != nil {
		return tui.SetupOutcome{}, err
	}

	outcome, err := tui.RunSetup(ctx, tui.SetupOptions{
		ConfigPath: path,
		// The theme is deliberately taken from the merged configuration: it
		// governs how the wizard looks, not what it writes, and a project's
		// chosen theme should still apply.
		ThemeName:         themePreference(opts.cwd),
		Existing:          existing,
		Reconfigure:       reconfigure,
		ContinueToSession: continueToSession,
	})
	if err != nil {
		return tui.SetupOutcome{}, err
	}
	return outcome, nil
}

func printSetupOutcome(outcome tui.SetupOutcome) {
	fmt.Printf("Wrote %s\n", outcome.ConfigPath)
	fmt.Printf("  %s / %s — credential %s\n", outcome.Result.Name, outcome.Result.Model, outcome.Result.CredentialSummary())
	// The two numbers are printed because they are the two that used to be
	// written invisibly, and because a terminal scrollback is where someone
	// looks when an answer later stops short or a session runs out of context.
	fmt.Printf("  context_window %d, max_tokens %d — %s\n",
		outcome.Result.Provider.Context, outcome.Result.Provider.MaxTokens, outcome.Result.Limits.Describe())
	if outcome.Result.Credential == setup.CredentialManual {
		fmt.Printf("  Export %s before starting a session.\n", outcome.Result.EnvVar)
	}
}

// interactiveProviderSetupNeeded recognizes only the genuinely unconfigured
// state. A malformed selection is not repaired implicitly, and an explicit
// CLI/environment override is honored by letting normal startup report it.
func interactiveProviderSetupNeeded(opts options) (bool, error) {
	cfg, err := appconfig.Load(opts.cwd)
	if err != nil {
		return false, err
	}
	if len(cfg.Providers) != 0 {
		return false, nil
	}
	if !cfg.ProjectTrusted {
		return false, appconfig.ValidationError{Errors: []appconfig.FieldError{{
			Field:   "providers",
			Message: "no trusted provider is configured and project configuration is quarantined; run `collo trust` to review it, or `collo setup` to configure a user-level provider",
		}}}
	}
	if strings.TrimSpace(opts.provider) != "" || strings.TrimSpace(opts.model) != "" || cfg.EnvProvider != "" || cfg.EnvModel != "" {
		return false, nil
	}
	return true, nil
}

// reconfigureTarget resolves `--provider` against the file setup writes.
//
// An unknown name lists what the file actually contains rather than falling
// through to the full provider scan: dropping the user into a different run
// from the one they asked for is how a typo becomes a second provider.
func reconfigureTarget(requested string, existing setup.Existing, path string) (string, error) {
	name := strings.TrimSpace(requested)
	if name == "" || existing.Has(name) {
		return name, nil
	}
	configured := "none"
	if len(existing.Providers) > 0 {
		configured = strings.Join(existing.Providers, ", ")
	}
	return "", fmt.Errorf("no provider named %q in %s (configured: %s)\nRun `collo setup` with no --provider to add one", name, path, configured)
}

// themePreference reads the configured theme so provider setup matches the
// interface the user already chose. A machine with no configuration falls
// through to the default.
func themePreference(cwd string) string {
	cfg, err := appconfig.Load(cwd)
	if err != nil {
		return ""
	}
	return cfg.Options.Theme
}
