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

// runSetupCommand implements `collo setup`: probe, discover, verify, and only
// then write.
//
// With `--provider <name>` it re-enters the same path pointed at a provider the
// file already has, which is what turns setup from a first-run wizard into
// something worth running whenever a model changes. The re-run is not a
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
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("collo setup is interactive and needs a terminal; configure providers with `collo init --global` and `collo auth set` in a script")
	}

	path, err := setup.GlobalPath()
	if err != nil {
		return err
	}

	// What the file already contains is read from that one file, not from the
	// merged configuration. `appconfig.Load` composes defaults, user, and a
	// trusted project layer, so a provider defined in a repository's
	// `.collomia.json` would appear here — and setup writes the global file.
	// Warning about a collision with an entry setup will not touch is
	// misleading, and the project layer would shadow the write besides.
	existing, err := setup.ReadExisting(path)
	if err != nil {
		return fmt.Errorf("read %s: %w\nFix or move that file before running setup; it is not being overwritten", path, err)
	}

	// `--provider` re-enters the wizard pointed at a provider that is already
	// configured, which is what makes setup a thing to run whenever rather than
	// only on a first run. An unknown name lists what the file actually
	// contains: the alternative is dropping the user into the full provider
	// scan, where the run they asked for silently becomes a different one.
	reconfigure, err := reconfigureTarget(opts.provider, existing, path)
	if err != nil {
		return err
	}

	outcome, err := tui.RunSetup(context.Background(), tui.SetupOptions{
		ConfigPath: path,
		// The theme is deliberately taken from the merged configuration: it
		// governs how the wizard looks, not what it writes, and a project's
		// chosen theme should still apply.
		ThemeName:   themePreference(opts.cwd),
		Existing:    existing,
		Reconfigure: reconfigure,
	})
	if err != nil {
		return err
	}
	if !outcome.Wrote {
		fmt.Println("Setup cancelled; nothing was written.")
		return nil
	}
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
	fmt.Println("Start a session with `collo`, or inspect everything with `collo doctor`.")
	return nil
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

// themePreference reads the configured theme so the wizard matches the
// interface the user already chose. A machine with no configuration falls
// through to the default, which is the common case on a first run.
func themePreference(cwd string) string {
	cfg, err := appconfig.Load(cwd)
	if err != nil {
		return ""
	}
	return cfg.Options.Theme
}
