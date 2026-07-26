package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/credstore"
	"github.com/robert-mcdermott/collomia/internal/logging"
	"github.com/robert-mcdermott/collomia/internal/provider"
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

	// Credential store. Reported before providers because it explains where a
	// provider credential below came from.
	storeStatus, storeDetail := credentialStoreDiagnostic()
	add("credential store", storeStatus, storeDetail)

	// Providers.
	if err == nil {
		for _, name := range cfg.ProviderNames() {
			p := cfg.Providers[name]
			status, detail := providerDiagnostic(p)
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

	// Permission stance. Sandbox health below answers "is containment
	// working"; this answers "what was asked for", which is the question a
	// screenshot or a support bundle otherwise cannot settle.
	if err == nil {
		p := cfg.Permissions
		stance := "preset=" + orDefaultString(p.Preset, "none")
		stance += fmt.Sprintf("; autonomy=%s; network=%s; commands=%s; credentials=%s",
			orDefaultString(p.Mode, "ask"),
			orDefaultString(p.Network, "open"),
			orDefaultString(p.Commands, "open"),
			orDefaultString(p.ProtectCredentials, appconfig.ProtectCredentialsPrompt))
		stance += fmt.Sprintf("; %d rule(s)", len(p.Rules))
		status := "ok"
		if len(cfg.Clamped) > 0 {
			// A refused project setting is the one stance detail a user is
			// most likely to be surprised by, so it is not merely informational.
			status = "warn"
			refused := make([]string, 0, len(cfg.Clamped))
			for _, note := range cfg.Clamped {
				refused = append(refused, note.Field)
			}
			stance += "; refused project weakening of " + strings.Join(refused, ", ")
		}
		add("permissions", status, stance)
	}

	// Sandbox readiness.
	backend := sandbox.ForPlatform()
	mode := "off"
	allowNetwork := false
	constrainReads := false
	if err == nil && cfg.Permissions.Sandbox != "" {
		mode = cfg.Permissions.Sandbox
		allowNetwork = cfg.Permissions.SandboxAllowNetwork
		constrainReads = !cfg.Permissions.SandboxAllowReadOutsideWorkspace
	}
	if availErr := backend.Available(); availErr != nil {
		status := "warn"
		if mode == string(sandbox.ModeRequire) {
			status = "fail"
		}
		add("sandbox", status, fmt.Sprintf("mode=%s; %v", mode, availErr))
	} else {
		caps := backend.Capabilities()
		policy := sandbox.Policy{WorkspaceRoot: opts.cwd, AllowNetwork: allowNetwork, ConstrainReads: constrainReads}
		detail := fmt.Sprintf("mode=%s; backend %s available; %s", mode, backend.Name(), caps.Summary())
		if mode == string(sandbox.ModeOff) {
			detail += "; OS enforcement is disabled"
			add("sandbox", "ok", detail)
		} else {
			detail += "; command user-data reads are " + caps.ReadPolicySummary(policy)
			missing := caps.Missing(policy)
			if len(missing) > 0 {
				status := "warn"
				if mode == string(sandbox.ModeRequire) {
					status = "fail"
				}
				add("sandbox", status, detail+"; requested policy is missing "+strings.Join(missing, " and "))
			} else {
				if allowNetwork {
					detail += "; command network is allowed"
				} else {
					detail += "; command network is denied"
				}
				add("sandbox", "ok", detail)
			}
		}
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

// credentialStoreDiagnostic reports the optional OS credential store. An
// unavailable store is not a problem to fix — environment variables are a
// fully supported way to hold a key — so it is reported as ok with the reason.
func credentialStoreDiagnostic() (status, detail string) {
	names, err := credstore.List()
	if err != nil {
		return "warn", "credential index unreadable: " + err.Error()
	}
	if !credstore.Available() {
		if len(names) > 0 {
			return "warn", fmt.Sprintf("unavailable on this platform; %d recorded entr%s cannot be read here — providers use api_key_env", len(names), plural(len(names), "y", "ies"))
		}
		return "ok", "unavailable on this platform; providers use api_key, api_key_env, or their own environment variable"
	}
	if len(names) == 0 {
		return "ok", credstore.Backend() + "; no stored credentials (nothing is read from it)"
	}
	missing := 0
	for _, name := range names {
		if present, verifyErr := credstore.Verify(name); verifyErr == nil && !present {
			missing++
		}
	}
	if missing > 0 {
		return "warn", fmt.Sprintf("%s; %d of %d entries are recorded but no longer present — run `collo auth list`", credstore.Backend(), missing, len(names))
	}
	return "ok", fmt.Sprintf("%s; %d stored credential%s", credstore.Backend(), len(names), plural(len(names), "", "s"))
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func providerDiagnostic(p appconfig.Provider) (status, detail string) {
	status, detail = "ok", p.Type+providerTimeoutDiagnostic(p)
	if isAzureProvider(p.Type) && p.Auth == "entra" {
		scope := provider.AzureEntraScope(p.Type, p.EntraScope)
		tenant := p.EntraTenantID
		if tenant == "" {
			tenant = os.Getenv("AZURE_TENANT_ID")
		}
		if tenant == "" {
			tenant = "credential default"
		}
		authority := p.EntraAuthorityHost
		if authority == "" {
			authority = os.Getenv("AZURE_AUTHORITY_HOST")
		}
		if authority == "" {
			authority = "Azure Public Cloud"
		}
		chain := os.Getenv("AZURE_TOKEN_CREDENTIALS")
		if chain == "" {
			chain = "default chain: environment, workload identity, managed identity, Azure CLI, azd, Azure PowerShell"
		} else {
			chain = "AZURE_TOKEN_CREDENTIALS=" + chain
		}
		role := "Cognitive Services User"
		if p.Type == "azure-openai" {
			role = "Cognitive Services OpenAI User"
		}
		return status, fmt.Sprintf("%s; Microsoft Entra via DefaultAzureCredential (%s; scope %s; tenant %s; authority %s); requests refresh tokens automatically; required data-plane role: %s", detail, chain, scope, tenant, authority, role)
	}
	if p.Type == "bedrock" {
		auth := strings.ToLower(strings.TrimSpace(p.Auth))
		if auth == "" {
			auth = "auto"
		}
		bearerAvailable := p.APIKey != "" || os.Getenv(provider.BedrockBearerTokenEnv) != ""
		if p.APIKeyEnv != "" && p.APIKey == "" {
			if auth == "bearer" || auth == "auto" {
				return "warn", fmt.Sprintf("%s; bearer credential env %s is not set", detail, p.APIKeyEnv)
			}
		}
		if auth == "bearer" || (auth == "auto" && (bearerAvailable || p.APIKeyEnv != "")) {
			source := provider.BedrockBearerTokenEnv
			switch {
			case p.CredentialSource != "":
				source = p.CredentialSource
			case p.APIKeyEnv != "":
				source = p.APIKeyEnv
			}
			if !bearerAvailable {
				return "warn", fmt.Sprintf("%s; bearer auth selected but %s is not set", detail, source)
			}
			return status, fmt.Sprintf("%s; Bedrock bearer API key resolved from %s", detail, source)
		}
		profile := "default chain"
		if p.Profile != "" {
			profile = "profile " + p.Profile
		}
		return status, fmt.Sprintf("%s; SigV4 via AWS credential chain (%s; access/secret/session, SSO, roles, or workload identity)", detail, profile)
	}
	switch {
	case p.APIKey != "":
		detail += "; credential resolved from " + p.CredentialSource
	case p.APIKeyEnv != "":
		status = "warn"
		detail += fmt.Sprintf("; credential env %s is not set", p.APIKeyEnv)
	default:
		detail += "; no credential configured (fine for local endpoints)"
	}
	if isAzureProvider(p.Type) && p.Auth == "bearer" {
		detail += "; caller-supplied bearer token; Collomia cannot refresh it (prefer auth=entra)"
	}
	return status, detail
}

func isAzureProvider(providerType string) bool {
	return providerType == "azure-openai" || providerType == "azure-foundry" || providerType == "azure-foundry-anthropic"
}

func providerTimeoutDiagnostic(p appconfig.Provider) string {
	connect, request, idle := p.ConnectTimeoutSeconds, p.RequestTimeoutSeconds, p.StreamIdleTimeoutSeconds
	if connect <= 0 {
		connect = 10
	}
	if request <= 0 {
		request = 30 * 60
	}
	if idle <= 0 {
		idle = 5 * 60
	}
	return fmt.Sprintf("; timeouts connect=%ds request=%ds idle=%ds", connect, request, idle)
}

// orDefaultString names the effective value when a field was left empty, so
// the report never shows a blank where a default is in force.
func orDefaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
