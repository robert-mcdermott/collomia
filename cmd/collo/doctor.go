package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/audit"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/credstore"
	"github.com/robert-mcdermott/collomia/internal/egress"
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
		for _, layer := range cfg.Layers {
			if layer.Applied && layer.Path != "" {
				if status, detail := schemaDiagnostic(layer.Path); detail != "" {
					add("editor schema", status, layer.Name+" layer: "+detail)
				}
			}
		}
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
			// Token limits are reported on the provider's own row rather than
			// as a second one. They are a property of this provider, and a
			// report that lists every provider twice is one people stop reading.
			if limitStatus, limitDetail := providerLimitsDiagnostic(p); limitDetail != "" {
				detail += "; " + limitDetail
				if limitStatus == "warn" && status == "ok" {
					status = "warn"
				}
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

	// Permission stance. Sandbox health below answers "is containment
	// working"; this answers "what was asked for", which is the question a
	// screenshot or a support bundle otherwise cannot settle.
	if err == nil {
		p := cfg.Permissions
		stance := "preset=" + orDefaultString(p.Preset, "none")
		stance += fmt.Sprintf("; autonomy=%s; network=%s; commands=%s; credentials=%s; publication=%s",
			orDefaultString(p.Mode, "ask"),
			orDefaultString(p.Network, "open"),
			orDefaultString(p.Commands, "open"),
			orDefaultString(p.ProtectCredentials, appconfig.ProtectCredentialsPrompt),
			orDefaultString(p.Publication, appconfig.PublicationPrompt))
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
				scoped := strings.EqualFold(strings.TrimSpace(cfg.Permissions.SandboxEgress), appconfig.SandboxEgressScoped)
				switch {
				case scoped:
					// The setting alone does not say what a command can reach:
					// scoped egress is enforceable only where the backend can
					// deny remote traffic while keeping loopback reachable.
					supported, why := egress.Supported()
					allowlist := egress.FromRules(cfg.Permissions.Rules)
					switch {
					case !supported:
						detail += "; sandbox_egress=scoped is not enforceable here (" + why + "), so command networking follows sandbox_allow_network"
						add("sandbox", warnOrFail(mode), detail)
					case allowlist.Empty():
						detail += "; command network is brokered but no allow rule names a host, so every outbound connection will be refused"
						add("sandbox", "warn", detail)
					default:
						detail += fmt.Sprintf("; command network is brokered to %d allowed host pattern(s): %s", len(allowlist.Patterns()), strings.Join(allowlist.Patterns(), ", "))
						add("sandbox", "ok", detail)
					}
				case allowNetwork:
					detail += "; command network is allowed"
					add("sandbox", "ok", detail)
				default:
					detail += "; command network is denied"
					add("sandbox", "ok", detail)
				}
			}
		}
	}

	// Audit ledger. Doctor used to check only that the directory could be
	// created, which answers "can a ledger be opened" and not "is the record
	// this workspace already has complete" — the question someone actually
	// brings to a permission trail.
	auditStatus, auditDetail := auditDiagnostic(opts.cwd)
	add("audit", auditStatus, auditDetail)

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

// auditDiagnostic reports whether this workspace's permission record can be
// written and whether what is already on disk is complete. A declared gap is a
// warning rather than a failure: the actions it covers were still governed by
// the permission pipeline, and only the record of them was lost.
func auditDiagnostic(workspace string) (status, detail string) {
	dir, err := audit.Dir()
	if err != nil {
		return "warn", "ledger directory unavailable: " + err.Error()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "fail", "ledger directory is not writable, so privileged actions will go unrecorded: " + err.Error()
	}
	path := filepath.Join(dir, audit.FileName(workspace))
	report, err := audit.Read(path, audit.Filter{})
	if err != nil {
		return "warn", "ledger unreadable: " + err.Error()
	}
	if len(report.Files) == 0 {
		return "ok", path + " (no privileged action recorded yet)"
	}
	summary := fmt.Sprintf("%s; %d %s", path, report.Total, plural(report.Total, "entry", "entries"))
	if report.Complete() {
		return "ok", summary + "; complete"
	}
	var problems []string
	if report.Dropped > 0 {
		problems = append(problems, fmt.Sprintf("%d %s lost across %d declared %s", report.Dropped, plural(report.Dropped, "entry", "entries"), report.Gaps, plural(report.Gaps, "gap", "gaps")))
	}
	if report.Malformed > 0 {
		problems = append(problems, fmt.Sprintf("%d unreadable %s", report.Malformed, plural(report.Malformed, "line", "lines")))
	}
	if report.Discarded {
		problems = append(problems, "an older generation was discarded at rotation")
	}
	return "warn", summary + "; INCOMPLETE: " + strings.Join(problems, ", ") + " — inspect with `collo audit`"
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

// providerLimitsDiagnostic reports the two token fields that fail silently.
//
// Neither is validated at load and neither has ever appeared in a diagnostic,
// so a configuration written by hand — which, before `collo setup`, was the
// only kind — could disable automatic compaction and cap every answer at 8192
// tokens without anything ever saying so. Both cases are warnings rather than
// failures: they are legal configurations that behave worse than their author
// intended, which is exactly what a doctor is for.
//
// The published-limits table is used only to *suggest*. It is a deliberate
// understatement assembled from vendor documentation, so it may not contradict
// a number the user chose, and a suggestion names the model it is about so a
// reader can judge it.
func providerLimitsDiagnostic(p appconfig.Provider) (status, detail string) {
	known, hasKnown := provider.KnownLimits(p.Model)
	suggestion := func(value int, field string) string {
		if value <= 0 {
			return ""
		}
		return fmt.Sprintf(" (published limits for %s suggest %s %d)", orDefaultString(p.Model, "this model"), field, value)
	}

	switch {
	case p.Context <= 0 && p.MaxTokensDefaulted:
		return "warn", fmt.Sprintf("no context_window and no max_tokens: automatic compaction is disabled, so a long session ends at a provider context-length error, and answers stop at %d tokens%s%s",
			appconfig.DefaultMaxTokens,
			suggestion(known.ContextWindow, "context_window"),
			suggestion(known.MaxOutput, "max_tokens"))
	case p.Context <= 0:
		return "warn", fmt.Sprintf("max_tokens=%d but no context_window: automatic compaction is disabled, so a long session ends at a provider context-length error rather than compacting%s",
			p.MaxTokens, suggestion(known.ContextWindow, "context_window"))
	case p.MaxTokensDefaulted:
		return "warn", fmt.Sprintf("context_window=%d but no max_tokens: every answer stops at the %d-token default, with no message%s",
			p.Context, appconfig.DefaultMaxTokens, suggestion(known.MaxOutput, "max_tokens"))
	default:
		detail = fmt.Sprintf("limits context_window=%d max_tokens=%d", p.Context, p.MaxTokens)
		if hasKnown && known.ContextWindow > 0 && p.Context > known.ContextWindow*2 {
			// Not an error — the table understates by design and a gateway may
			// genuinely serve more — but a window several times the documented
			// one is worth seeing, because it is what a session runs out of
			// context against without compacting first.
			detail += fmt.Sprintf(" (published limits for %s suggest context_window %d)", p.Model, known.ContextWindow)
		}
		return "ok", detail
	}
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

// warnOrFail escalates a degraded sandbox report to a failure under require,
// which is the mode that promises to fail closed rather than continue with
// less containment than was asked for.
func warnOrFail(mode string) string {
	if mode == string(sandbox.ModeRequire) {
		return "fail"
	}
	return "warn"
}

// schemaDiagnostic reports whether a configuration file's editor schema is
// present and current.
//
// A `$schema` key is what makes a 121-field JSON file editable without reading
// the manual, and there are two ways it stops working silently. The reference
// can point at a sibling that is not there — a broken link, which every editor
// handles by simply offering nothing, so the failure looks exactly like never
// having had a schema. And the sibling can have been generated by an older
// build, in which case it describes fields that have since changed, which is
// worse than absent because it is confidently wrong.
//
// A file with no `$schema` at all gets a suggestion rather than a warning:
// hand-writing configuration is still supported, it is just harder than it
// needs to be.
func schemaDiagnostic(configPath string) (status, detail string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", ""
	}
	var document struct {
		Schema string `json:"$schema"`
	}
	if json.Unmarshal(data, &document) != nil {
		return "", ""
	}
	reference := strings.TrimSpace(document.Schema)
	if reference == "" {
		return "ok", "no $schema key; run `collo schema config` and add " +
			strconv.Quote(appconfig.SchemaReference) + " for completion and inline validation while editing"
	}
	// Only a local sibling can be checked. A URL is the user's deliberate
	// choice and this build has no business fetching it to grade it.
	if strings.Contains(reference, "://") {
		return "ok", "$schema points at " + reference
	}
	resolved := reference
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(configPath), reference)
	}
	onDisk, err := os.ReadFile(resolved)
	if err != nil {
		return "warn", "$schema points at " + reference + ", which is missing; editors will silently offer nothing (`collo schema config > " + reference + "` writes it)"
	}
	if !bytes.Equal(bytes.TrimSpace(onDisk), bytes.TrimSpace(appconfig.JSONSchema())) {
		return "warn", reference + " was generated by a different build; it describes fields this one may not have (`collo schema config > " + reference + "` refreshes it)"
	}
	return "ok", reference + " is current"
}
