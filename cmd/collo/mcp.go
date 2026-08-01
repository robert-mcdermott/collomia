package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	mcpclient "github.com/robert-mcdermott/collomia/internal/mcp"
	"github.com/robert-mcdermott/collomia/internal/shutdown"
	"github.com/robert-mcdermott/collomia/internal/tools"
	"github.com/robert-mcdermott/collomia/internal/trust"
)

type mcpLayers struct {
	globalPath     string
	projectPath    string
	global         map[string]appconfig.MCPServer
	project        map[string]appconfig.MCPServer
	projectTrusted bool
}

func runMCPCommand(opts options) error {
	sub := "list"
	if len(opts.args) > 0 {
		sub = opts.args[0]
	}
	arg := func(n int, description string) (string, error) {
		if len(opts.args) <= n {
			return "", fmt.Errorf("mcp %s requires %s", sub, description)
		}
		return opts.args[n], nil
	}
	if sub != "add" && (opts.mcpURL != "" || opts.mcpTimeoutSet || len(opts.mcpEnv) > 0 || len(opts.mcpHeaders) > 0) {
		return fmt.Errorf("--url, --env, --header, and --timeout are only valid with `collo mcp add`")
	}
	layers, err := loadMCPLayers(opts.cwd)
	if err != nil {
		return err
	}

	switch sub {
	case "list":
		if len(opts.args) > 1 {
			return fmt.Errorf("mcp list does not accept positional arguments")
		}
		return printMCPList(layers, opts.global)
	case "show":
		name, err := arg(1, "a server name")
		if err != nil {
			return err
		}
		if len(opts.args) != 2 {
			return fmt.Errorf("mcp show accepts exactly one server name")
		}
		return printMCPDefinition(layers, name, opts.global)
	case "add":
		name, err := arg(1, "a server name")
		if err != nil {
			return err
		}
		server, warnings, err := mcpServerFromOptions(opts)
		if err != nil {
			return err
		}
		path, scope := mcpTarget(layers, opts.global)
		created, err := appconfig.PutMCPServer(path, name, server, opts.yes)
		if err != nil {
			return err
		}
		action := "Updated"
		if created {
			action = "Added"
		}
		fmt.Printf("%s MCP server %s in %s configuration: %s\n", action, name, scope, path)
		for _, warning := range warnings {
			fmt.Printf("Warning: %s\n", warning)
		}
		if !opts.global {
			printProjectTrustNextStep()
		}
		return nil
	case "remove":
		name, err := arg(1, "a server name")
		if err != nil {
			return err
		}
		if len(opts.args) != 2 {
			return fmt.Errorf("mcp remove accepts exactly one server name")
		}
		path, scope := mcpTarget(layers, opts.global)
		removed, err := appconfig.RemoveMCPServer(path, name)
		if err != nil {
			return err
		}
		if !removed {
			return missingMCPInScope(layers, name, scope, opts.global)
		}
		fmt.Printf("Removed MCP server %s from %s configuration: %s\n", name, scope, path)
		if !opts.global {
			printProjectTrustNextStep()
		}
		return nil
	case "enable", "disable":
		name, err := arg(1, "a server name")
		if err != nil {
			return err
		}
		if len(opts.args) != 2 {
			return fmt.Errorf("mcp %s accepts exactly one server name", sub)
		}
		path, scope := mcpTarget(layers, opts.global)
		changed, err := appconfig.SetMCPDisabled(path, name, sub == "disable")
		if err != nil {
			return err
		}
		if !changed {
			return missingMCPInScope(layers, name, scope, opts.global)
		}
		fmt.Printf("%sd MCP server %s in %s configuration: %s\n", titleCommand(sub), name, scope, path)
		if !opts.global {
			printProjectTrustNextStep()
		}
		return nil
	case "test":
		name, err := arg(1, "a server name")
		if err != nil {
			return err
		}
		if len(opts.args) != 2 {
			return fmt.Errorf("mcp test accepts exactly one server name")
		}
		return testMCPServer(opts.cwd, layers, name, opts.global)
	default:
		return fmt.Errorf("unknown mcp subcommand %q (list, show, add, remove, enable, disable, test)", sub)
	}
}

func loadMCPLayers(workspace string) (mcpLayers, error) {
	globalPath, err := appconfig.GlobalPath()
	if err != nil {
		return mcpLayers{}, err
	}
	layers := mcpLayers{
		globalPath:     globalPath,
		projectPath:    filepath.Join(workspace, appconfig.ProjectFile),
		global:         map[string]appconfig.MCPServer{},
		project:        map[string]appconfig.MCPServer{},
		projectTrusted: true,
	}
	if entries, _, err := appconfig.ReadMCPFile(layers.globalPath); err != nil {
		return layers, err
	} else if entries != nil {
		layers.global = entries
	}
	entries, exists, err := appconfig.ReadMCPFile(layers.projectPath)
	if err != nil {
		return layers, err
	}
	if entries != nil {
		layers.project = entries
	}
	if exists {
		data, err := os.ReadFile(layers.projectPath)
		if err != nil {
			return layers, err
		}
		if store, err := trust.Load(); err == nil {
			layers.projectTrusted = store.Check(workspace, data) == trust.StatusTrusted
		}
	}
	return layers, nil
}

func mcpTarget(layers mcpLayers, global bool) (path, scope string) {
	if global {
		return layers.globalPath, "global"
	}
	return layers.projectPath, "project"
}

func printMCPList(layers mcpLayers, globalOnly bool) error {
	if globalOnly {
		if len(layers.global) == 0 {
			fmt.Println("No MCP servers in the global configuration.")
			return nil
		}
		for _, name := range appconfig.MCPNames(layers.global) {
			fmt.Printf("%-24s %-16s %-8s %s\n", name, mcpTransport(layers.global[name]), "global", mcpFlags(layers.global[name], "configured"))
		}
		return nil
	}
	names := map[string]bool{}
	for name := range layers.global {
		names[name] = true
	}
	for name := range layers.project {
		names[name] = true
	}
	if len(names) == 0 {
		fmt.Println("No persistent MCP servers configured.")
		fmt.Println("Add one with `collo mcp add <name> -- <command> [args...]` or use `--global` for every workspace.")
		return nil
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		project, hasProject := layers.project[name]
		global, hasGlobal := layers.global[name]
		if hasProject {
			state := "effective"
			if !layers.projectTrusted {
				state = "quarantined; run collo trust"
			}
			fmt.Printf("%-24s %-16s %-8s %s\n", name, mcpTransport(project), "project", mcpFlags(project, state))
		}
		if hasGlobal {
			state := "effective"
			if hasProject && layers.projectTrusted {
				state = "shadowed by project"
			}
			fmt.Printf("%-24s %-16s %-8s %s\n", name, mcpTransport(global), "global", mcpFlags(global, state))
		}
	}
	return nil
}

func mcpTransport(server appconfig.MCPServer) string {
	if strings.TrimSpace(server.Transport) == "" {
		return "stdio"
	}
	return strings.ToLower(server.Transport)
}

func mcpFlags(server appconfig.MCPServer, state string) string {
	flags := []string{state}
	if server.Disabled {
		flags = append(flags, "disabled")
	} else {
		flags = append(flags, "enabled")
	}
	if server.Trusted {
		flags = append(flags, "trusted")
	} else {
		flags = append(flags, "untrusted")
	}
	return strings.Join(flags, ", ")
}

func printMCPDefinition(layers mcpLayers, name string, globalOnly bool) error {
	printed := false
	printOne := func(scope, state string, server appconfig.MCPServer) error {
		data, err := json.MarshalIndent(redactMCPServer(server), "", "  ")
		if err != nil {
			return err
		}
		if printed {
			fmt.Println()
		}
		fmt.Printf("%s (%s, %s)\n%s\n", name, scope, state, data)
		printed = true
		return nil
	}
	if globalOnly {
		server, ok := layers.global[name]
		if !ok {
			return fmt.Errorf("no MCP server %q in global configuration", name)
		}
		return printOne("global", "configured", server)
	}
	if server, ok := layers.project[name]; ok {
		state := "effective"
		if !layers.projectTrusted {
			state = "quarantined; run collo trust"
		}
		if err := printOne("project", state, server); err != nil {
			return err
		}
	}
	if server, ok := layers.global[name]; ok {
		state := "effective"
		if _, project := layers.project[name]; project && layers.projectTrusted {
			state = "shadowed by project"
		}
		if err := printOne("global", state, server); err != nil {
			return err
		}
	}
	if !printed {
		return fmt.Errorf("no persistent MCP server named %q", name)
	}
	return nil
}

func mcpServerFromOptions(opts options) (appconfig.MCPServer, []string, error) {
	timeout := 30
	if opts.mcpTimeoutSet {
		timeout = opts.mcpTimeout
	}
	env, err := keyValueOptions(opts.mcpEnv, "--env")
	if err != nil {
		return appconfig.MCPServer{}, nil, err
	}
	headers, err := keyValueOptions(opts.mcpHeaders, "--header")
	if err != nil {
		return appconfig.MCPServer{}, nil, err
	}
	server := appconfig.MCPServer{Trusted: true, Timeout: timeout}
	var warnings []string
	if opts.mcpURL != "" {
		if len(opts.args) > 2 {
			return server, nil, fmt.Errorf("HTTP MCP add does not accept a command; use only --url, --header, and --timeout")
		}
		if len(env) > 0 {
			return server, nil, fmt.Errorf("--env is only valid for a stdio MCP server")
		}
		server.Transport, server.URL, server.Headers = "streamable-http", opts.mcpURL, headers
	} else {
		if len(headers) > 0 {
			return server, nil, fmt.Errorf("--header requires --url")
		}
		if len(opts.args) < 3 {
			return server, nil, fmt.Errorf("mcp add requires a command after the server name; use `-- <command> [args...]`")
		}
		server.Transport, server.Command, server.Args, server.Env = "stdio", opts.args[2], append([]string(nil), opts.args[3:]...), env
	}
	for key, value := range env {
		if sensitiveName(key) && !hasEnvironmentReference(value) {
			warnings = append(warnings, fmt.Sprintf("%s contains a literal sensitive value; prefer '${ENV_VAR}' so the secret is not stored in JSON", key))
		}
	}
	for key, value := range headers {
		if sensitiveName(key) && !hasEnvironmentReference(value) {
			warnings = append(warnings, fmt.Sprintf("header %s contains a literal sensitive value; prefer an environment reference such as 'Bearer ${TOKEN}'", key))
		}
	}
	if parsed, err := url.Parse(server.URL); err == nil {
		for key, values := range parsed.Query() {
			for _, value := range values {
				if sensitiveName(key) && !hasEnvironmentReference(value) {
					warnings = append(warnings, fmt.Sprintf("URL query parameter %s contains a literal sensitive value; prefer an environment reference", key))
					break
				}
			}
		}
	}
	sort.Strings(warnings)
	return server, warnings, nil
}

func keyValueOptions(values []string, flag string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("%s requires KEY=VALUE, got %q", flag, value)
		}
		result[key] = item
	}
	return result, nil
}

func sensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{"authorization", "api-key", "api_key", "apikey", "token", "secret", "password", "passwd", "cookie", "credential"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func hasEnvironmentReference(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '$' || i+1 >= len(value) {
			continue
		}
		next := rune(value[i+1])
		if next == '{' || next == '_' || unicode.IsLetter(next) {
			return true
		}
	}
	return false
}

func redactMCPServer(server appconfig.MCPServer) appconfig.MCPServer {
	copyServer := server
	copyServer.Env = cloneStringMap(server.Env)
	copyServer.Headers = cloneStringMap(server.Headers)
	for key, value := range copyServer.Env {
		if sensitiveName(key) && !hasEnvironmentReference(value) {
			copyServer.Env[key] = "[redacted]"
		}
	}
	for key, value := range copyServer.Headers {
		if sensitiveName(key) && !hasEnvironmentReference(value) {
			copyServer.Headers[key] = "[redacted]"
		}
	}
	if parsed, err := url.Parse(copyServer.URL); err == nil {
		if parsed.User != nil {
			parsed.User = url.User("[redacted]")
		}
		query := parsed.Query()
		queryChanged := false
		for key, values := range query {
			if !sensitiveName(key) {
				continue
			}
			for i, value := range values {
				if !hasEnvironmentReference(value) {
					values[i] = "[redacted]"
					queryChanged = true
				}
			}
			query[key] = values
		}
		if queryChanged {
			parsed.RawQuery = query.Encode()
		}
		copyServer.URL = parsed.String()
	}
	return copyServer
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func missingMCPInScope(layers mcpLayers, name, scope string, global bool) error {
	otherScope := "global"
	_, inOther := layers.global[name]
	if global {
		otherScope = "project"
		_, inOther = layers.project[name]
	}
	if inOther {
		hint := " (add --global to target it)"
		if global {
			hint = " (omit --global to target it)"
		}
		return fmt.Errorf("no MCP server %q in %s configuration; it exists in %s configuration%s", name, scope, otherScope, hint)
	}
	return fmt.Errorf("no MCP server %q in %s configuration", name, scope)
}

func printProjectTrustNextStep() {
	fmt.Println("Project configuration changed and is not active until its current contents are trusted.")
	fmt.Println("Review .collomia.json, then run `collo trust` to activate project MCP definitions.")
}

func testMCPServer(workspace string, layers mcpLayers, name string, globalOnly bool) error {
	var server appconfig.MCPServer
	if globalOnly {
		var ok bool
		server, ok = layers.global[name]
		if !ok {
			return fmt.Errorf("no MCP server %q in global configuration", name)
		}
	} else if project, ok := layers.project[name]; ok && layers.projectTrusted {
		server = project
	} else if global, ok := layers.global[name]; ok {
		server = global
	} else if _, quarantined := layers.project[name]; quarantined {
		return fmt.Errorf("project MCP server %q is quarantined; review .collomia.json and run `collo trust` before testing the effective definition", name)
	} else {
		return fmt.Errorf("no effective MCP server named %q", name)
	}
	if server.Disabled {
		return fmt.Errorf("MCP server %q is disabled in the selected configuration", name)
	}
	if !server.Trusted {
		return fmt.Errorf("MCP server %q is untrusted; review it and set trusted to true before testing", name)
	}
	server = appconfig.ResolveMCPServer(server)
	if errs := appconfig.ValidateMCPServer(name, server); len(errs) > 0 {
		return appconfig.ValidationError{Errors: errs}
	}
	ctx, stop := shutdown.NotifyContext(context.Background())
	defer stop()
	manager, connectErrors := mcpclient.ConnectAll(ctx, map[string]appconfig.MCPServer{name: server}, tools.NewRegistry(), mcpclient.Options{Workspace: workspace, DisablePinning: true})
	defer manager.Close()
	statuses := manager.Statuses()
	if len(statuses) != 1 || statuses[0].Status != mcpclient.StatusConnected {
		if len(connectErrors) > 0 {
			return errors.Join(connectErrors...)
		}
		return fmt.Errorf("MCP server %q did not connect", name)
	}
	status := statuses[0]
	if err := manager.Ping(ctx, name); err != nil {
		return err
	}
	resourceCount, promptCount := "unsupported", "unsupported"
	for _, capability := range status.Capabilities {
		switch capability {
		case "resources":
			resources, err := manager.Resources(ctx, name)
			if err != nil {
				return fmt.Errorf("list resources: %w", err)
			}
			resourceCount = fmt.Sprintf("%d", len(resources))
		case "prompts":
			prompts, err := manager.Prompts(ctx, name)
			if err != nil {
				return fmt.Errorf("list prompts: %w", err)
			}
			promptCount = fmt.Sprintf("%d", len(prompts))
		}
	}
	identity := strings.TrimSpace(status.ServerName + " " + status.ServerVersion)
	if identity == "" {
		identity = "not reported"
	}
	capabilities := strings.Join(status.Capabilities, ", ")
	if capabilities == "" {
		capabilities = "none reported"
	}
	fmt.Printf("Connected %s successfully.\n", name)
	fmt.Printf("  transport:    %s\n", status.Transport)
	fmt.Printf("  server:       %s\n", identity)
	fmt.Printf("  protocol:     %s\n", status.Protocol)
	fmt.Printf("  capabilities: %s\n", capabilities)
	fmt.Printf("  tools:        %d\n", len(status.Tools))
	fmt.Printf("  resources:    %s\n", resourceCount)
	fmt.Printf("  prompts:      %s\n", promptCount)
	fmt.Println("Connection and catalogs validated; no MCP tool was invoked.")
	return nil
}

// titleCommand avoids locale-dependent formatting for the two fixed verbs.
func titleCommand(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
