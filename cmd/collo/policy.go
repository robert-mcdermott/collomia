package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/egress"
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
	action := tools.ActionFromAnalysis("run: "+command, command, analysis)
	manager := permission.New(cfg.Permissions, nil)
	if opts.autonomy != "" {
		if err := manager.SetMode(opts.autonomy); err != nil {
			return err
		}
	}
	grant, outcome := manager.Evaluate("run_command", action)

	fmt.Printf("command:      %s\n", command)
	fmt.Printf("autonomy:     %s\n", manager.Mode())
	fmt.Printf("postures:     network=%s commands=%s protect_credentials=%s sandbox_egress=%s\n", cfg.Permissions.Network, cfg.Permissions.Commands, cfg.Permissions.ProtectCredentials, orDefaultString(cfg.Permissions.SandboxEgress, appconfig.SandboxEgressOff))
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
		// The permission decision below and the broker's decision are separate
		// gates: an approved command still reaches only what the allowlist
		// covers, so both answers belong here.
		if strings.EqualFold(strings.TrimSpace(cfg.Permissions.SandboxEgress), appconfig.SandboxEgressScoped) {
			fmt.Printf("egress:       %s\n", egressForecast(cfg, analysis))
		}
	}
	if analysis.Inspectable {
		fmt.Println("analysis:     inspectable")
	} else {
		fmt.Printf("analysis:     UNINSPECTABLE (%s) — interactive approval always required\n", strings.Join(analysis.Reasons, "; "))
	}
	if len(analysis.CredentialTargets) > 0 {
		fmt.Printf("credentials:  %s\n", strings.Join(analysis.CredentialTargets, "; "))
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

// egressForecast answers what the broker would do with this command's declared
// endpoints. It is deliberately separate from the permission decision: a
// command can be approved and still reach nothing, because the allowlist is a
// second gate applied at connection time rather than at approval time.
func egressForecast(cfg appconfig.Config, analysis shell.Analysis) string {
	if supported, why := egress.Supported(); !supported {
		return "not enforceable on this platform (" + why + ")"
	}
	allowlist := egress.FromRules(cfg.Permissions.Rules)
	var allowed, refused []string
	for _, host := range analysis.Hosts {
		if allowlist.Permits(host) {
			allowed = append(allowed, host)
		} else {
			refused = append(refused, host)
		}
	}
	var parts []string
	if len(allowed) > 0 {
		parts = append(parts, "brokered: "+strings.Join(allowed, ", "))
	}
	if len(refused) > 0 {
		parts = append(parts, "REFUSED: "+strings.Join(refused, ", ")+" (no host-scoped allow rule)")
	}
	if analysis.UndeterminedHosts {
		// An endpoint the analyzer could not read is still checked, just at
		// connection time rather than here, so this is not a gap in the
		// control — only a limit on what can be predicted.
		parts = append(parts, "undetermined endpoints are checked against the allowlist when dialed")
	}
	if len(parts) == 0 {
		return "no declared endpoints"
	}
	return strings.Join(parts, "; ")
}
