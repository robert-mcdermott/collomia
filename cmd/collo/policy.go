package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/shell"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// runPolicyCommand implements `collo policy check <command…>`: it evaluates a
// shell command against the configured rules and autonomy mode without
// executing anything, and explains the decision.
func runPolicyCommand(opts options) error {
	if len(opts.args) == 0 || opts.args[0] != "check" {
		return errors.New("usage: collo policy check <command…>")
	}
	command := strings.TrimSpace(strings.Join(opts.args[1:], " "))
	if command == "" {
		return errors.New("policy check requires a command to evaluate")
	}
	cfg, err := appconfig.Load(opts.cwd)
	if err != nil {
		return err
	}
	analysis := shell.AnalyzeInWorkspace(command, opts.cwd)
	action := tools.Action{
		Risk: tools.RiskExecute, Summary: "run: " + command,
		Executables: analysis.Executables, Uninspectable: !analysis.Inspectable, AnalysisReasons: analysis.Reasons,
		Hosts: analysis.Hosts, Network: analysis.NetworkCommand,
		HostsUndetermined: analysis.UndeterminedHosts, HostReasons: analysis.HostReasons,
		HardDenyReasons: analysis.HardDenyReasons, ConfirmReasons: analysis.ConfirmReasons,
	}
	manager := permission.New(cfg.Permissions, nil)
	if opts.autonomy != "" {
		if err := manager.SetMode(opts.autonomy); err != nil {
			return err
		}
	}
	grant, outcome := manager.Evaluate("run_command", action)

	fmt.Printf("command:      %s\n", command)
	fmt.Printf("autonomy:     %s\n", manager.Mode())
	fmt.Printf("postures:     network=%s commands=%s\n", cfg.Permissions.Network, cfg.Permissions.Commands)
	if len(analysis.Executables) > 0 {
		fmt.Printf("executables:  %s\n", strings.Join(analysis.Executables, ", "))
	}
	if analysis.NetworkCommand {
		endpoints := strings.Join(analysis.Hosts, ", ")
		if analysis.UndeterminedHosts {
			if endpoints != "" {
				endpoints += ", "
			}
			endpoints += "UNDETERMINED (" + strings.Join(analysis.HostReasons, "; ") + ")"
		}
		fmt.Printf("endpoints:    %s\n", endpoints)
	}
	if analysis.Inspectable {
		fmt.Println("analysis:     inspectable")
	} else {
		fmt.Printf("analysis:     UNINSPECTABLE (%s) — interactive approval always required\n", strings.Join(analysis.Reasons, "; "))
	}
	if len(analysis.HardDenyReasons) > 0 {
		fmt.Printf("safety:       catastrophic (%s)\n", strings.Join(analysis.HardDenyReasons, "; "))
	} else if len(analysis.ConfirmReasons) > 0 {
		fmt.Printf("safety:       one-time confirmation (%s)\n", strings.Join(analysis.ConfirmReasons, "; "))
	}
	for _, re := range cfg.Permissions.DeniedCommands {
		if matched, _ := regexpMatch(re, command); matched {
			fmt.Printf("decision:     deny (hard-denied by permissions.denied_commands pattern %s)\n", re)
			return nil
		}
	}
	fmt.Printf("decision:     %s (source: %s", outcome, grant.Source)
	if grant.Rule != "" {
		fmt.Printf("; %s", grant.Rule)
	}
	fmt.Println(")")
	return nil
}

func regexpMatch(pattern, value string) (bool, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(value), nil
}
