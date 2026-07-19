package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/trust"
)

func runConfigCommand(opts options) error {
	sub := "show"
	if len(opts.args) > 0 {
		sub = opts.args[0]
	}
	switch sub {
	case "reference":
		fmt.Print(appconfig.ConfigReference())
		return nil
	case "validate":
		// Validation must inspect the project file even before the user trusts
		// it. Parsing and field validation do not activate any capabilities.
		cfg, err := appconfig.LoadWithOptions(opts.cwd, appconfig.LoadOptions{
			Strict: opts.strict,
			TrustStatus: func(string, []byte) trust.Status {
				return trust.StatusTrusted
			},
		})
		if err != nil {
			return err
		}
		fmt.Println("Configuration is valid.")
		fmt.Print(cfg.LayerReport())
		return nil
	case "show":
		cfg, err := appconfig.Load(opts.cwd)
		if err != nil {
			return err
		}
		redactor := app.NewRedactor(cfg)
		display := cfg
		display.Providers = map[string]appconfig.Provider{}
		for name, p := range cfg.Providers {
			if p.APIKey != "" {
				p.APIKey = "[redacted]"
			}
			for key := range p.Headers {
				p.Headers[key] = "[redacted]"
			}
			display.Providers[name] = p
		}
		data, err := json.MarshalIndent(display, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(redactor.Redact(string(data)))
		fmt.Println()
		fmt.Print(cfg.LayerReport())
		return nil
	default:
		return fmt.Errorf("unknown config subcommand %q (expected show, validate, or reference)", sub)
	}
}

func runSessionsCommand(opts options) error {
	store, err := session.Open(opts.cwd)
	if err != nil {
		return err
	}
	sub := "list"
	if len(opts.args) > 0 {
		sub = opts.args[0]
	}
	arg := func(n int, name string) (string, error) {
		if len(opts.args) <= n {
			return "", fmt.Errorf("sessions %s requires %s", sub, name)
		}
		return opts.args[n], nil
	}
	switch sub {
	case "list":
		metas, err := store.List()
		if err != nil {
			return err
		}
		if len(metas) == 0 {
			fmt.Println("No sessions yet. Start one with `collo`, resume with `collo --continue`.")
			return nil
		}
		for _, meta := range metas {
			marker := " "
			if meta.Archived {
				marker = "A"
			}
			title := meta.Title
			if title == "" {
				title = "(untitled)"
			}
			fmt.Printf("%s %s  %-28s %2d turns  %s/%s  updated %s\n", marker, meta.ID, title, meta.Turns, meta.Provider, meta.Model, meta.UpdatedAt.Local().Format("2006-01-02 15:04"))
		}
		return nil
	case "show":
		id, err := arg(1, "a session id")
		if err != nil {
			return err
		}
		sess, err := store.Load(id)
		if err != nil {
			return err
		}
		defer sess.Close()
		fmt.Printf("id: %s\ntitle: %s\nprovider: %s/%s\nturns: %d\ncreated: %s\nupdated: %s\n\n", sess.Meta.ID, sess.Meta.Title, sess.Meta.Provider, sess.Meta.Model, sess.Meta.Turns, sess.Meta.CreatedAt.Local().Format(time.RFC3339), sess.Meta.UpdatedAt.Local().Format(time.RFC3339))
		for _, message := range sess.Transcript {
			text := message.Content
			if len(text) > 400 {
				text = text[:400] + " …"
			}
			fmt.Printf("--- %s ---\n%s\n", message.Role, text)
		}
		return nil
	case "fork":
		id, err := arg(1, "a session id")
		if err != nil {
			return err
		}
		forked, err := store.Fork(id)
		if err != nil {
			return err
		}
		forked.Close()
		fmt.Printf("Forked %s → %s\nResume it with: collo --resume %s\n", id, forked.Meta.ID, forked.Meta.ID)
		return nil
	case "rename":
		id, err := arg(1, "a session id")
		if err != nil {
			return err
		}
		if _, err := arg(2, "a new title"); err != nil {
			return err
		}
		return store.Rename(id, strings.Join(opts.args[2:], " "))
	case "archive", "unarchive":
		id, err := arg(1, "a session id")
		if err != nil {
			return err
		}
		return store.Archive(id, sub == "archive")
	case "delete":
		id, err := arg(1, "a session id")
		if err != nil {
			return err
		}
		if !opts.yes {
			return fmt.Errorf("deleting a session is permanent; re-run with --yes to confirm: collo sessions delete %s --yes", id)
		}
		return store.Delete(id)
	default:
		return fmt.Errorf("unknown sessions subcommand %q (list, show, fork, rename, archive, unarchive, delete)", sub)
	}
}

func runTrustCommand(opts options) error {
	store, err := trust.Load()
	if err != nil {
		return err
	}
	projectPath := filepath.Join(opts.cwd, appconfig.ProjectFile)
	data, readErr := os.ReadFile(projectPath)
	if errors.Is(readErr, os.ErrNotExist) {
		data = nil
	} else if readErr != nil {
		return readErr
	}
	status := store.Check(opts.cwd, data)
	switch {
	case opts.status:
		fmt.Printf("workspace: %s\n", opts.cwd)
		if data == nil {
			fmt.Println("project configuration: none (trust is not required)")
			return nil
		}
		fmt.Printf("project configuration: %s\n", projectPath)
		fmt.Printf("trust status: %s\n", status)
		return nil
	case opts.revoke:
		if err := store.Revoke(opts.cwd); err != nil {
			return err
		}
		fmt.Println("Trust revoked for", opts.cwd)
		return nil
	default:
		if data == nil {
			fmt.Println("No project configuration found; nothing to trust.")
			return nil
		}
		if status == trust.StatusTrusted {
			fmt.Println("Workspace is already trusted.")
			return nil
		}
		fmt.Printf("Project configuration %s:\n\n%s\n", projectPath, string(data))
		fmt.Print("Trusting it enables project-provided MCP servers, permissions, skills, and instructions.\nTrust this workspace? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "y" && answer != "yes" {
			fmt.Println("Not trusted.")
			return nil
		}
		if err := store.Trust(opts.cwd, data); err != nil {
			return err
		}
		fmt.Println("Workspace trusted. Trust is revoked automatically if the project configuration changes.")
		return nil
	}
}
