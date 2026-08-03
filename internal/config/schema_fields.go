package config

// This file holds what reflection cannot supply: the prose a reader needs, the
// vocabulary each enumerated field draws from, the numeric bounds the
// validator enforces, and the one place where two positions of the same Go
// type mean different things.
//
// Only the prose is written by hand. Enumerations come from vocabulary.go and
// are therefore the same lists ValidateFields consults, which is the property
// that matters: an editor must never offer a value the loader will reject, and
// must never flag one it accepts.

// agentRuleDef names the narrower reading of Rule that a delegated agent's
// permissions use. The Go type is the same; the vocabulary is not, because a
// child profile may only tighten the parent's policy.
const agentRuleDef = "AgentRule"

// variantFor selects a narrower definition for a struct reached at a
// particular position.
func variantFor(key fieldKey) string {
	if key == (fieldKey{"AgentPermissions", "rules"}) {
		return agentRuleDef
	}
	return ""
}

// enumFor returns the vocabulary a field draws from, or nil when it is free
// text. The lists are the validator's own.
func enumFor(key fieldKey) []string {
	switch key {
	case fieldKey{"Permissions", "mode"}:
		return AutonomyModes()
	case fieldKey{"AgentPermissions", "mode"}:
		return AgentAutonomyModes()
	case fieldKey{"Permissions", "preset"}:
		return PresetNames()
	case fieldKey{"Permissions", "sandbox"}:
		return SandboxModes()
	case fieldKey{"Permissions", "network"}:
		return NetworkPostures()
	case fieldKey{"Permissions", "commands"}:
		return CommandPostures()
	case fieldKey{"Permissions", "sandbox_egress"}:
		return SandboxEgressModes()
	case fieldKey{"Permissions", "command_env"}:
		return CommandEnvModes()
	case fieldKey{"Permissions", "protect_credentials"}:
		return ProtectCredentialsSettings()
	case fieldKey{"Permissions", "publication"}:
		return PublicationSettings()
	case fieldKey{"Rule", "action"}:
		return RuleActions()
	case fieldKey{agentRuleDef, "action"}:
		return AgentRuleActions()
	case fieldKey{"Provider", "type"}:
		return ProviderTypes()
	case fieldKey{"Reasoning", "effort"}:
		return ReasoningEfforts()
	case fieldKey{"AgentDefinition", "availability"}:
		return AgentAvailabilities()
	case fieldKey{"Options", "agent_integration"}:
		return AgentIntegrationModes()
	case fieldKey{"Options", "notifications"}:
		return NotificationModes()
	case fieldKey{"MCPServer", "transport"}:
		return MCPTransports()
	}
	return nil
}

// boundFor returns the numeric constraints ValidateFields enforces.
//
// A field absent here is unbounded rather than unchecked — the bounds that
// exist are transcribed, and TestSchemaBoundsMatchTheValidator drives the
// validator with each stated edge to confirm the schema and the loader agree
// on where the boundary is.
func boundFor(key fieldKey) (map[string]any, bool) {
	switch key {
	case fieldKey{"Provider", "context_window"},
		fieldKey{"Provider", "max_tokens"},
		fieldKey{"Provider", "connect_timeout_seconds"},
		fieldKey{"Provider", "request_timeout_seconds"},
		fieldKey{"Provider", "stream_idle_timeout_seconds"},
		fieldKey{"MCPServer", "timeout_seconds"},
		fieldKey{"Hook", "timeout_seconds"},
		fieldKey{"AgentDefinition", "max_iterations"},
		fieldKey{"AgentDefinition", "token_budget"},
		fieldKey{"AgentDefinition", "cost_budget_usd"}:
		return map[string]any{"minimum": 0}, true
	case fieldKey{"AgentDefinition", "timeout_seconds"}:
		return map[string]any{"minimum": 0, "maximum": 3600}, true
	case fieldKey{"Options", "delegate_max_concurrency"}:
		return map[string]any{"minimum": 0, "maximum": 6}, true
	case fieldKey{"Pricing", "input_per_million"},
		fieldKey{"Pricing", "output_per_million"}:
		return map[string]any{"exclusiveMinimum": 0}, true
	case fieldKey{"Pricing", "cached_input_per_million"},
		fieldKey{"Pricing", "cache_write_per_million"}:
		return map[string]any{"minimum": 0}, true
	}
	return nil, false
}

// requiredFields names the fields a definition cannot be written without.
//
// The root object is deliberately absent, and that is the substantive finding
// of generating this schema at all: **a configuration file is one layer of a
// merge, not the configuration.** `providers`, `default_provider`, and
// `permissions.mode` are all required of the *merged* result and none of them
// is required of any particular file — a project `.collomia.json` that sets
// two rules and nothing else is correct, and a schema that marked the root's
// fields required would have put a red underline under every project file in
// existence.
//
// The definitions below can state requirements because of how the loader
// merges. A struct field is decoded onto the value inherited from the previous
// layer, so `permissions` and `options` accumulate across files and nothing in
// them is ever required. A map or slice element is decoded into a fresh value,
// so a `providers.<name>` block replaces its predecessor wholesale and must
// stand on its own — which is why `type` is required there and `mode` is not
// required in `permissions`.
//
// base_url is intentionally not listed: it is required for every provider type
// except bedrock, and a conditional requirement expressed as an unconditional
// one would flag correct Bedrock configurations. ValidateFields still reports
// it, with the provider type in the message.
var requiredFields = map[string][]string{
	"Provider":   {"type"},
	"Rule":       {"action"},
	agentRuleDef: {"action"},
	"Hook":       {"command"},
	"Pricing":    {"input_per_million", "output_per_million"},
	"Reasoning":  {"effort"},
}

// describeField returns the documentation for one field, falling back from the
// definition name to the Go type name so a variant inherits the prose it
// shares and overrides only what differs.
func describeField(typeName, defName, jsonName string) string {
	if description, ok := fieldDescriptions[fieldKey{defName, jsonName}]; ok {
		return description
	}
	return fieldDescriptions[fieldKey{typeName, jsonName}]
}

// fieldDescriptions is the user-facing documentation for every configuration
// field, keyed by definition and JSON name.
//
// These are deliberately shorter and more imperative than the Go doc comments
// beside the structs: those explain to a maintainer why a field exists, while
// these appear in an editor's hover while someone is typing a value.
// TestEveryConfigurationFieldIsDescribed fails when a field has no entry, so a
// new setting cannot ship undocumented.
var fieldDescriptions = map[fieldKey]string{
	// Config
	{"Config", "$schema"}:          "Path or URL of the JSON Schema describing this file. Written by `collo init` and `collo setup`; regenerate with `collo schema config` after upgrading.",
	{"Config", "schema_version"}:   "Configuration format version. Collomia refuses a file newer than the build reading it.",
	{"Config", "default_provider"}: "Name of the entry in `providers` used when nothing else selects one. Overridden by COLLO_PROVIDER.",
	{"Config", "default_model"}:    "Model used when the selected provider names none of its own. Overridden by COLLO_MODEL.",
	{"Config", "default_agent"}:    "Named profile from `agents` that the primary conversation starts under. The profile must be available to the primary role.",
	{"Config", "providers"}:        "Provider endpoints, keyed by the name you refer to them by. At least one is required.",
	{"Config", "permissions"}:      "What the agent may do without asking, and what it may never do.",
	{"Config", "mcp"}:              "Model Context Protocol servers, keyed by name. Project-configured servers require `collo trust`.",
	{"Config", "options"}:          "Runtime and interface settings that carry no safety meaning.",
	{"Config", "agents"}:           "Named agent profiles, selectable for delegated tasks and — with availability primary or both — for the main conversation.",
	{"Config", "lsp"}:              "Language server command per language id, e.g. {\"go\": [\"gopls\"]}. Common servers on PATH are detected when unset. A project-level map requires `collo trust`.",
	{"Config", "hooks"}:            "Commands run at lifecycle events, keyed by event name. Hooks are trusted code and can only tighten decisions, never bypass the permission engine.",

	// Provider
	{"Provider", "type"}:                        "Which adapter speaks to this endpoint.",
	{"Provider", "base_url"}:                    "Endpoint root. Required for every type except bedrock. OpenAI-compatible runtimes normally end in /v1.",
	{"Provider", "api_key"}:                     "Literal credential. Prefer api_key_env or `collo auth` — a secret in a configuration file is a secret in backups, diffs, and screenshots.",
	{"Provider", "api_key_env"}:                 "Environment variable holding the credential. Checked before the OS credential store, and always before a provider-family default variable.",
	{"Provider", "model"}:                       "Model this provider answers with, overriding default_model.",
	{"Provider", "region"}:                      "AWS region for Bedrock.",
	{"Provider", "profile"}:                     "AWS named profile for Bedrock SigV4 credentials. Not used with auth=bearer.",
	{"Provider", "deployment"}:                  "Azure OpenAI deployment name. Requests address deployments, not model names — this is the field an Azure 404 usually means.",
	{"Provider", "api_version"}:                 "Azure OpenAI API version query parameter.",
	{"Provider", "auth"}:                        "Authentication mode. Bedrock accepts auto, sigv4, or bearer; the Azure family accepts api_key, bearer, or entra.",
	{"Provider", "entra_scope"}:                 "Microsoft Entra token audience, when the family default is wrong for this endpoint. Azure OpenAI and Foundry document different defaults.",
	{"Provider", "entra_tenant_id"}:             "Entra tenant for developer CLI and workload identity credentials. EnvironmentCredential still honours AZURE_TENANT_ID.",
	{"Provider", "entra_authority_host"}:        "Sovereign or private Entra authority. Empty uses Azure Public Cloud.",
	{"Provider", "headers"}:                     "Extra HTTP headers sent with every request. Values may reference ${ENV_VAR}.",
	{"Provider", "max_tokens"}:                  "Largest completion this model may produce. Omitting it applies a small default that truncates long answers with no message; `collo setup` writes a discovered value.",
	{"Provider", "context_window"}:              "Total prompt-plus-completion budget. Omitting it disables automatic compaction for the whole session, so a long conversation ends at a provider context-length error.",
	{"Provider", "temperature"}:                 "Sampling temperature. Omitted leaves the provider's own default in place.",
	{"Provider", "reasoning"}:                   "Opt-in reasoning controls. Omitted sends no reasoning field at all.",
	{"Provider", "pricing"}:                     "Token rates used for cost estimates. User-supplied because prices differ by account, region, and gateway; Collomia hardcodes none.",
	{"Provider", "connect_timeout_seconds"}:     "Connection timeout. Zero uses the built-in default.",
	{"Provider", "request_timeout_seconds"}:     "Whole-request timeout. Zero uses the built-in default.",
	{"Provider", "stream_idle_timeout_seconds"}: "How long a stream may go silent before it is treated as stalled. Zero uses the built-in default.",

	// Reasoning
	{"Reasoning", "effort"}: "Provider-neutral reasoning level. Adapters translate it only where the model supports one.",

	// Pricing
	{"Pricing", "input_per_million"}:        "USD per million prompt tokens.",
	{"Pricing", "output_per_million"}:       "USD per million completion tokens.",
	{"Pricing", "cached_input_per_million"}: "USD per million tokens read from the provider's prompt cache. Defaults to the ordinary input rate, which over-estimates rather than under.",
	{"Pricing", "cache_write_per_million"}:  "USD per million tokens written to the prompt cache, normally charged above the input rate. Defaults to the input rate.",

	// Permissions
	{"Permissions", "preset"}:                               "Named containment bundle applied as a starting point. An explicit field in the same layer always wins, and a preset can never loosen a stricter inherited layer. No preset sets autonomy mode.",
	{"Permissions", "mode"}:                                 "Autonomy stance: ask confirms every action, workspace auto-approves reads and in-workspace writes, autopilot auto-approves anything not separately gated.",
	{"Permissions", "allow_outside_workspace"}:              "Permit actions on paths outside the workspace root.",
	{"Permissions", "allowed_tools"}:                        "When non-empty, only these tools may run. Names may use globs.",
	{"Permissions", "denied_tools"}:                         "Tools that may never run, whatever else permits them.",
	{"Permissions", "denied_commands"}:                      "Regular expressions matched against whole command lines and always refused. Additive across layers: a layer may add patterns and can never remove one.",
	{"Permissions", "rules"}:                                "Ordered allow/prompt/deny decisions evaluated before the autonomy mode's defaults. The first matching rule wins.",
	{"Permissions", "network"}:                              "Posture for actions that reach the network. Under scoped, a network-bearing action is never auto-approved unless a rule or grant covers every endpoint it declares. A policy layer, not egress enforcement.",
	{"Permissions", "commands"}:                             "Posture for command execution. Under allowlist, a command is never auto-approved unless a rule or grant covers every executable it runs.",
	{"Permissions", "sandbox"}:                              "OS sandbox enforcement for commands: auto degrades visibly where a backend is unavailable, require fails closed instead.",
	{"Permissions", "sandbox_allow_network"}:                "Permit network egress from inside the sandbox. All-or-nothing; see sandbox_egress for the per-host form.",
	{"Permissions", "sandbox_egress"}:                       "How a sandboxed command reaches the network. scoped denies direct remote egress and routes through a loopback broker that dials only hosts named by host-scoped allow rules. Enforceable on macOS only.",
	{"Permissions", "sandbox_allow_read_outside_workspace"}: "Keep the compatibility default of broad filesystem reads inside the sandbox. Set false to confine reads to the workspace, system paths, temp directories, and sandbox_readable_roots.",
	{"Permissions", "sandbox_readable_roots"}:               "Extra roots a sandboxed command may read when outside-workspace reads are confined. Relative paths resolve from the workspace.",
	{"Permissions", "sandbox_writable_roots"}:               "Extra roots a sandboxed command may write, such as a package-manager cache. Writable roots are always readable.",
	{"Permissions", "protect_credentials"}:                  "What happens when an action reaches a well-known credential store — an SSH or GPG private key, a cloud token cache, a registry auth file, an environment file. Under prompt this is not coverable by a blanket allow rule, a tool-wide grant, or autopilot; a rule naming the path still is.",
	{"Permissions", "publication"}:                          "What happens when an action puts something outside this machine — a package version, a container image, a pull request, a release, an infrastructure apply, a push, a command on another host. Under prompt this is not coverable by autopilot or a tool-wide grant; a rule naming the operation is.",
	{"Permissions", "command_env"}:                          "Environment passed to agent commands: full inherits everything, minimal passes PATH, HOME, and other basics only, keeping parent secrets out of child processes.",
	{"Permissions", "reviewer_command"}:                     "Command run before any non-read action is auto-approved. It receives the request as JSON on stdin; replying {\"decision\":\"deny\"} or exiting non-zero escalates to an interactive prompt rather than silently allowing.",

	// Rule
	{"Rule", "action"}:  "What to do when every populated matcher below matches.",
	{"Rule", "tool"}:    "Tool name glob, e.g. run_command or mcp_*.",
	{"Rule", "path"}:    "Resolved path glob.",
	{"Rule", "command"}: "Executable glob (\"git\"), or — when it contains a space — an operation glob (\"npm publish\"). Only an operation pattern can cover a publication-gated action.",
	{"Rule", "host"}:    "Host or domain glob for network-bearing actions. An allow rule here never covers an endpoint Collomia could not determine.",
	{"Rule", "server"}:  "MCP server name glob.",
	{"Rule", "reason"}:  "Shown to the user when this rule decides an action.",

	// AgentRule — inherits Rule's prose except where the vocabulary narrows.
	{agentRuleDef, "action"}: "What to do when every populated matcher below matches. A delegated profile may only tighten the parent's policy, so allow is not available here.",

	// MCPServer
	{"MCPServer", "transport"}:       "How Collomia reaches the server.",
	{"MCPServer", "trusted"}:         "Skip the external-data framing applied to this server's results. Set it only for a server you control.",
	{"MCPServer", "command"}:         "Executable for stdio transport.",
	{"MCPServer", "args"}:            "Arguments for the stdio command. No shell is involved.",
	{"MCPServer", "url"}:             "Absolute HTTP(S) endpoint for http or streamable-http transport. Embedded credentials are rejected; use headers.",
	{"MCPServer", "env"}:             "Environment variables for the stdio command. Values may reference ${ENV_VAR}.",
	{"MCPServer", "headers"}:         "HTTP headers for an HTTP transport. Values may reference ${ENV_VAR}.",
	{"MCPServer", "disabled"}:        "Keep the entry without starting the server.",
	{"MCPServer", "timeout_seconds"}: "Per-request timeout. Zero uses thirty seconds.",

	// AgentDefinition
	{"AgentDefinition", "availability"}:    "Where this profile can be selected: delegate (the default), primary, or both.",
	{"AgentDefinition", "model"}:           "Model for this profile, on the same provider as the parent.",
	{"AgentDefinition", "reasoning"}:       "Reasoning controls for this profile, overriding the provider's.",
	{"AgentDefinition", "instructions"}:    "Prepended to the system prompt to fix the profile's role.",
	{"AgentDefinition", "tools"}:           "Restrict the profile to these tool names. Empty inherits every tool the parent has enabled.",
	{"AgentDefinition", "skills"}:          "Restrict the profile's model-visible skill catalog. Empty inherits the parent's.",
	{"AgentDefinition", "max_iterations"}:  "Iteration budget for a delegated task. Zero uses the default.",
	{"AgentDefinition", "token_budget"}:    "Bound on provider-reported input plus output tokens across the task. Zero leaves it bounded by iterations and timeout.",
	{"AgentDefinition", "cost_budget_usd"}: "Bound on estimated spend, using the provider's explicitly configured pricing. Zero disables this bound.",
	{"AgentDefinition", "timeout_seconds"}: "Bound on queueing plus execution. Zero uses ten minutes.",
	{"AgentDefinition", "permissions"}:     "Restrictions applied on top of the parent's policy. It can only tighten.",

	// AgentPermissions
	{"AgentPermissions", "mode"}:            "Autonomy stance requested for this profile, intersected with the parent's effective mode. It can never exceed it.",
	{"AgentPermissions", "denied_tools"}:    "Tools this profile may never run, added to the parent's denials.",
	{"AgentPermissions", "denied_commands"}: "Regular expressions this profile may never run, added to the parent's denials.",
	{"AgentPermissions", "rules"}:           "Additional prompt or deny decisions for this profile. Allow rules are not accepted here.",

	// Hook
	{"Hook", "command"}:         "Executable to run. No shell is involved.",
	{"Hook", "args"}:            "Arguments for the command.",
	{"Hook", "matcher"}:         "Regular expression tested against the event's subject — the tool name for tool events, the event name otherwise. Empty matches everything.",
	{"Hook", "timeout_seconds"}: "Bound on the hook run. Zero uses ten seconds.",

	// Options
	{"Options", "max_iterations"}:                "Provider/model response cycles one Standard turn may take, or consecutive cycles an Orchestrated Goal primary attempt may take without novel durable progress; this is not a tool-call count.",
	{"Options", "max_tool_output_bytes"}:         "Largest tool result passed to the model. Longer output is truncated with a marker.",
	{"Options", "delegate_max_concurrency"}:      "Session-wide limit on concurrent delegated tasks. Zero uses four.",
	{"Options", "delegate_provider_concurrency"}: "Tighter concurrency limit for named providers. Providers omitted here inherit the session-wide limit.",
	{"Options", "agent_integration"}:             "Who may publish retained delegated-worktree changes into the parent workspace: manual exposes only /agents apply, reviewed additionally gives the primary agent bounded inspect and apply tools.",
	{"Options", "disabled_tools"}:                "Built-in tools removed from the model's catalog entirely.",
	{"Options", "transcript_directory"}:          "Where /transcript writes. Defaults to the workspace.",
	{"Options", "theme"}:                         "TUI colour theme. NO_COLOR selects the plain theme whatever this says.",
	{"Options", "alternate_screen"}:              "Use the terminal's alternate screen buffer. Disabling it leaves the final screen in native scrollback.",
	{"Options", "mouse"}:                         "Request mouse reporting for wheel scrolling and click-to-select tabs. Turning it on means the terminal routes drags to Collomia instead of its own selection; alt+m releases and reclaims it mid-session.",
	{"Options", "keybindings"}:                   "Overrides for named global TUI actions. Modal safety decisions keep fixed keys so a remap cannot make an approval ambiguous.",
	{"Options", "notifications"}:                 "How the TUI gets your attention for approvals, questions, and finished long turns: on is bell plus desktop notification, bell is the bell alone.",
	{"Options", "reduced_motion"}:                "Replace decorative progress animation with a static marker. Never changes input, cancellation, or controls.",
	{"Options", "dim_background"}:                "Drop colour from the screen behind a modal so the dialog is plainly focused. The cleared gutter around the dialog stays either way.",
	{"Options", "editor"}:                        "External editor for the user-initiated action in diff review.",
	{"Options", "debug"}:                         "Write a verbose local debug log.",

	// EditorOptions
	{"EditorOptions", "command"}: "Editor executable, run directly without a shell.",
	{"EditorOptions", "args"}:    "Editor arguments. May use the {file}, {line}, and {column} placeholders.",
}
