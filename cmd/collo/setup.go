package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/setup"
	"github.com/robert-mcdermott/collomia/internal/tui"
	"golang.org/x/term"
)

// runSetupCommand implements `collo setup`: probe, discover, verify, and only
// then write.
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

	outcome, err := tui.RunSetup(context.Background(), tui.SetupOptions{
		ConfigPath: path,
		// The theme is deliberately taken from the merged configuration: it
		// governs how the wizard looks, not what it writes, and a project's
		// chosen theme should still apply.
		ThemeName: themePreference(opts.cwd),
		Existing:  existing,
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
	if outcome.Result.Credential == setup.CredentialManual {
		fmt.Printf("  Export %s before starting a session.\n", outcome.Result.EnvVar)
	}
	fmt.Println("Start a session with `collo`, or inspect everything with `collo doctor`.")
	return nil
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
