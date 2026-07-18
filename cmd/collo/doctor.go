package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/logging"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
	"github.com/robert-mcdermott/collomia/internal/trust"
	"github.com/robert-mcdermott/collomia/internal/version"
	"golang.org/x/term"
)

type check struct {
	name   string
	status string // ok, warn, fail
	detail string
}

func runDoctorCommand(opts options) error {
	var checks []check
	add := func(name, status, detail string) { checks = append(checks, check{name, status, detail}) }

	add("version", "ok", version.String())
	add("platform", "ok", appconfig.RuntimeSummary())

	// Configuration and layering.
	cfg, err := appconfig.LoadWithOptions(opts.cwd, appconfig.LoadOptions{Strict: opts.strict})
	if err != nil {
		add("configuration", "fail", err.Error())
	} else {
		var applied []string
		for _, layer := range cfg.Layers {
			if layer.Applied {
				applied = append(applied, layer.Name)
			}
		}
		add("configuration", "ok", "valid; layers: "+strings.Join(applied, " → "))
		if !cfg.ProjectTrusted {
			add("workspace trust", "warn", "project configuration is quarantined; review it and run `collo trust`")
		} else if store, terr := trust.Load(); terr == nil {
			data, _ := os.ReadFile(filepath.Join(opts.cwd, appconfig.ProjectFile))
			if len(data) > 0 && store.Check(opts.cwd, data) == trust.StatusTrusted {
				add("workspace trust", "ok", "project configuration is trusted")
			}
		}
	}

	// Terminal.
	if term.IsTerminal(int(os.Stdout.Fd())) {
		add("terminal", "ok", fmt.Sprintf("interactive; TERM=%s", os.Getenv("TERM")))
	} else {
		add("terminal", "warn", "stdout is not a TTY; the TUI needs an interactive terminal")
	}

	// Git.
	if path, lookErr := exec.LookPath("git"); lookErr != nil {
		add("git", "warn", "git not found in PATH; repository tools are limited")
	} else {
		detail := path
		cmd := exec.Command("git", "-C", opts.cwd, "rev-parse", "--is-inside-work-tree")
		if out, gitErr := cmd.Output(); gitErr == nil && strings.TrimSpace(string(out)) == "true" {
			detail += "; workspace is a git repository"
		} else {
			detail += "; workspace is not a git repository"
		}
		add("git", "ok", detail)
	}

	// Providers.
	if err == nil {
		for _, name := range cfg.ProviderNames() {
			p := cfg.Providers[name]
			detail := p.Type
			status := "ok"
			switch {
			case p.Type == "bedrock":
				detail += "; uses AWS credential chain"
			case p.APIKey != "":
				detail += "; credential resolved"
			case p.APIKeyEnv != "":
				status = "warn"
				detail += fmt.Sprintf("; credential env %s is not set", p.APIKeyEnv)
			default:
				detail += "; no credential configured (fine for local endpoints)"
			}
			add("provider "+name, status, detail)
		}
		// MCP.
		if len(cfg.MCP) == 0 {
			add("mcp", "ok", "no servers configured")
		} else {
			for name, server := range cfg.MCP {
				switch {
				case server.Disabled:
					add("mcp "+name, "ok", "disabled")
				case !server.Trusted:
					add("mcp "+name, "warn", "not marked trusted; it will not start")
				default:
					add("mcp "+name, "ok", server.Transport)
				}
			}
		}
	}

	// Sandbox readiness.
	backend := sandbox.ForPlatform()
	mode := "off"
	if err == nil && cfg.Permissions.Sandbox != "" {
		mode = cfg.Permissions.Sandbox
	}
	if availErr := backend.Available(); availErr != nil {
		status := "warn"
		if mode == string(sandbox.ModeRequire) {
			status = "fail"
		}
		add("sandbox", status, fmt.Sprintf("mode=%s; %v", mode, availErr))
	} else {
		add("sandbox", "ok", fmt.Sprintf("mode=%s; backend %s available", mode, backend.Name()))
	}

	// Log directory.
	if dir, logErr := logging.Dir(); logErr != nil {
		add("logs", "warn", logErr.Error())
	} else if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		add("logs", "warn", mkErr.Error())
	} else {
		add("logs", "ok", dir)
	}

	failed := false
	for _, c := range checks {
		marker := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[c.status]
		fmt.Printf("%s %-18s %s\n", marker, c.name, c.detail)
		if c.status == "fail" {
			failed = true
		}
	}
	if failed {
		return errors.New("doctor found failing checks")
	}
	return nil
}
