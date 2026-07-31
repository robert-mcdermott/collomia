package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

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

	// The existing provider names are read for one purpose: so the
	// confirmation screen can say that writing replaces something, rather than
	// the user discovering it afterwards. A configuration that cannot be
	// loaded is not fatal here — an unconfigured machine is the expected case,
	// and setup is also the reasonable thing to reach for when a file is
	// broken.
	existing := existingProviderNames(opts.cwd)

	outcome, err := tui.RunSetup(context.Background(), tui.SetupOptions{
		ConfigPath: path,
		ThemeName:  themePreference(opts.cwd),
		Existing:   existing,
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

func existingProviderNames(cwd string) []string {
	cfg, err := appconfig.Load(cwd)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
