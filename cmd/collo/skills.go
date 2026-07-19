package main

import (
	"fmt"
	"os"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/skills"
)

func runSkillsCommand(opts options) error {
	sub := "list"
	if len(opts.args) > 0 {
		sub = opts.args[0]
	}
	arg := func(n int, name string) (string, error) {
		if len(opts.args) <= n {
			return "", fmt.Errorf("skills %s requires %s", sub, name)
		}
		return opts.args[n], nil
	}
	// Lifecycle commands are explicit user file operations, so they see
	// project skills regardless of trust; only the agent's catalog is gated.
	discover := func() (skills.Catalog, error) { return skills.Discover(opts.cwd, true) }
	targetRoot := func() (string, error) {
		if opts.global {
			return skills.UserSkillsDir()
		}
		return skills.ProjectSkillsDir(opts.cwd), nil
	}
	switch sub {
	case "list":
		catalog, err := discover()
		if err != nil {
			return err
		}
		if len(catalog.Skills) == 0 && len(catalog.Disabled) == 0 {
			fmt.Println("No skills installed.")
			fmt.Println("Create one with `collo skills new <name>` (project) or `collo skills new <name> --global` (all workspaces).")
			return nil
		}
		for _, skill := range catalog.Skills {
			version := ""
			if skill.Version != "" {
				version = " v" + skill.Version
			}
			extra := ""
			if n := skill.BundleCount(); n > 0 {
				extra = fmt.Sprintf("  [%d bundled files]", n)
			}
			fmt.Printf("%-24s %-8s%s  %s%s\n", skill.Name, skill.Source, version, skill.Description, extra)
			for _, issue := range skill.Issues {
				fmt.Printf("  ⚠ %s\n", issue)
			}
		}
		for _, skill := range catalog.Disabled {
			fmt.Printf("%-24s %-8s  (disabled) %s\n", skill.Name, skill.Source, skill.Description)
		}
		for _, issue := range catalog.Issues {
			fmt.Printf("⚠ %s\n", issue)
		}
		if cfg, err := appconfig.Load(opts.cwd); err == nil && !cfg.ProjectTrusted {
			if _, statErr := os.Stat(skills.ProjectSkillsDir(opts.cwd)); statErr == nil {
				fmt.Println("\nNote: this workspace is not trusted, so the agent will not see project skills until you run `collo trust`.")
			}
		}
		return nil
	case "show":
		name, err := arg(1, "a skill name")
		if err != nil {
			return err
		}
		catalog, err := discover()
		if err != nil {
			return err
		}
		skill, ok := catalog.Find(name)
		if !ok {
			for _, s := range catalog.Disabled {
				if s.Name == name {
					skill, ok = s, true
				}
			}
		}
		if !ok {
			return fmt.Errorf("no skill named %q; `collo skills list` shows what is installed", name)
		}
		fmt.Print(skills.Describe(skill))
		return nil
	case "new":
		name, err := arg(1, "a skill name (lowercase letters, digits, hyphens)")
		if err != nil {
			return err
		}
		root, err := targetRoot()
		if err != nil {
			return err
		}
		dir, err := skills.Scaffold(root, name)
		if err != nil {
			return err
		}
		fmt.Println("Created", dir)
		fmt.Println("Edit SKILL.md — the description decides when the agent reaches for this skill.")
		fmt.Println("Add scripts/, references/, and assets/ directories beside it as the skill needs them.")
		if !opts.global {
			fmt.Println("Project skills are only visible to the agent in trusted workspaces (`collo trust --status`).")
		}
		return nil
	case "install", "update":
		src, err := arg(1, "a path to a skill directory containing SKILL.md")
		if err != nil {
			return err
		}
		root, err := targetRoot()
		if err != nil {
			return err
		}
		skill, err := skills.Install(src, root, opts.yes || sub == "update")
		if err != nil {
			return err
		}
		fmt.Printf("Installed %s (sha256 %s) at %s\n", skill.Name, skill.Hash[:12], skill.Dir)
		for _, issue := range skill.Issues {
			fmt.Printf("⚠ %s\n", issue)
		}
		if len(skill.Scripts) > 0 {
			fmt.Printf("This skill bundles %d executable script(s); the agent can only run them through the normal permission and sandbox rules.\n", len(skill.Scripts))
		}
		return nil
	case "remove":
		name, err := arg(1, "a skill name")
		if err != nil {
			return err
		}
		dir, err := skills.FindDir(opts.cwd, name)
		if err != nil {
			return err
		}
		if !opts.yes {
			return fmt.Errorf("this permanently deletes %s; re-run with --yes to confirm", dir)
		}
		if err := skills.Remove(dir); err != nil {
			return err
		}
		fmt.Println("Removed", dir)
		return nil
	case "enable", "disable":
		name, err := arg(1, "a skill name")
		if err != nil {
			return err
		}
		dir, err := skills.FindDir(opts.cwd, name)
		if err != nil {
			return err
		}
		if err := skills.SetDisabled(dir, sub == "disable"); err != nil {
			return err
		}
		if sub == "disable" {
			fmt.Printf("Disabled %s; it stays installed but the agent no longer sees it. `collo skills enable %s` restores it.\n", name, name)
		} else {
			fmt.Printf("Enabled %s.\n", name)
		}
		return nil
	default:
		return fmt.Errorf("unknown skills subcommand %q (list, show, new, install, update, remove, enable, disable)", sub)
	}
}
