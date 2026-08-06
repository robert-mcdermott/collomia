# Collomia

![Collomia terminal interface](docs/collo-screenshot.png)

Collomia is a local-first, provider-neutral coding agent for the terminal. It
combines a polished interactive interface with repository-aware tools,
language-server intelligence, durable sessions, governed sub-agents, MCP and
skills support, and a headless JSONL interface for automation. It ships as one
`collo` binary for macOS, Linux, and Windows.

The project is built around an unusually explicit trust boundary. Model-proposed
actions pass through layered permissions, command analysis, OS sandboxing,
network controls, audit, and evidence-based completion checks. Standard mode is
the permanent default. For work that benefits from a visible dependency graph,
Orchestrated Goal adds optional evidence-gated durable execution in which the
runtime—not model prose—owns readiness, evidence freshness, recovery, budgets,
and the terminal outcome.

## Why Collomia stands out

- **Native containment on every major desktop OS.** Commands can run under
  macOS Seatbelt, Linux Landlock, or Windows AppContainer plus Job Objects.
  Compatibility-first `auto` mode reports degradation; `require` fails closed
  when the requested boundary is unavailable.
- **Monotonic layered policy.** Defaults, user configuration, trusted project
  configuration, and environment overrides compose predictably. A repository
  may tighten containment but cannot weaken the user's security posture, and
  project configuration, instructions, skills, and MCP servers remain
  quarantined until the workspace is trusted.
- **Real network boundaries.** Every sandbox backend can deny command network
  access. macOS also supports host-scoped sandbox egress through a
  Collomia-owned broker; Linux and Windows retain their honest all-or-nothing
  controls where their kernels cannot enforce the same host boundary. Built-in
  web tools independently enforce a non-configurable public-internet-only
  address guard.
- **Security decisions match the action.** Ordered rules can name tools, paths,
  command operations, hosts, and MCP servers. Credential-store access and
  publishing, deploying, or pushing are protected decisions that autonomy mode
  and broad tool grants cannot silently cover.
- **Evidence-gated Standard mode.** A final-sounding response does not complete
  an active goal while plan work is open, verification is stale after a write,
  or a tool failure remains unresolved. Standard mode stays fast and
  model-directed while the runtime checks the evidence it can actually observe.
- **Orchestrated Goal.** An explicit TUI-only workflow turns a proposed
  dependency graph into durable runtime state. Supported end-to-end graphs can
  fan out up to two governed read-only workers, then return to the serial
  primary lane. Experimental isolated-writer waves use disjoint Git worktrees,
  child verification, explicit user integration, a recoverable publication
  checkpoint, and fresh combined-workspace verification or a clearly recorded
  user waiver.
- **Recoverable, inspectable work.** Sessions survive interruption, completed
  turns are flushed to stable storage, ambiguous mutations are never replayed,
  file changes are reviewable and undoable, and permission decisions and
  outcomes are written to an attributable, bounded audit ledger.
- **A complete coding loop in one binary.** Streaming provider support, atomic
  edits and patches, Git inspection and scoped commits, build/test detection,
  LSP diagnostics and navigation, background processes, image input, web
  search/fetch, MCP, skills, hooks, and governed multi-agent delegation all use
  the same permission and lifecycle machinery.

See the [feature overview](docs/FEATURES.md) for the complete list. The exact
generated implementation status is in the
[capability matrix](docs/CAPABILITIES.md); security guarantees and limits are
defined in the [security model](docs/SECURITY.md).

## Install

### macOS and Linux

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh | sh
```

This installs the latest stable release to `$HOME/.local/bin`, verifies its
SHA-256 checksum, and replaces an existing binary only after the download passes
its version check. It does not use `sudo` or modify `PATH`.

### Windows

Run in PowerShell:

```powershell
irm https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1 | iex
```

This installs to `%LocalAppData%\Programs\Collomia`, verifies the checksum and
binary, and adds that directory to the current user's `PATH`. It requires
neither elevation nor an execution-policy change; open a new terminal after the
install.

For version pinning, direct downloads, provenance verification, upgrades,
rollback, uninstall, and unsigned-binary guidance, see
[Installing Collomia](docs/INSTALLING.md).

## Configure and start

Run the setup wizard:

```sh
collo setup
```

The wizard finds running local model servers and recognized hosted-provider
credentials, lets you choose from available models, verifies both ordinary
completion and tool support, resolves model limits, and writes a user-level
configuration without storing an API key in the file. Running `collo` with no
configured provider opens the same flow and continues directly into the first
session after verification.

Then start Collomia in a repository:

```sh
cd /path/to/project
collo
```

Useful checks:

```sh
collo doctor
collo config show
collo capabilities
```

For a manual or scripted configuration, use `collo init --global
--with-reference`, edit the generated strict JSON with its generated editor
schema, and run `collo config validate --strict`. The
[user guide](docs/USER_GUIDE.md) documents every provider and setting.

Security-critical containment lives under `permissions`: `sandbox`,
`command_env`, `network`, `commands`, `sandbox_allow_network`,
`sandbox_egress`, `sandbox_allow_read_outside_workspace`,
`allow_outside_workspace`, `protect_credentials`, and `publication`. Their
platform-specific semantics and safe combinations are defined in the
[security model](docs/SECURITY.md), not duplicated in the starter file.

The top-level command surface is intentionally small:

| Purpose | Commands |
| --- | --- |
| Work and automate | `collo run`, `collo review`, `collo verify`, `collo replay` |
| Set up and inspect | `collo setup`, `collo init`, `collo config`, `collo doctor`, `collo capabilities` |
| Trust and security | `collo trust`, `collo policy`, `collo auth`, `collo audit` |
| Sessions and extensions | `collo sessions`, `collo skills`, `collo mcp` |
| Diagnostics and integration | `collo support`, `collo completion`, `collo schema` |

## Execution modes

**Standard mode** is the default: describe the task and Collomia works through
its ordinary governed tool loop.

**Orchestrated Goal** is an explicit per-session option for work where a durable,
inspectable graph and runtime-owned completion gates justify the additional
overhead:

```text
/orchestrate Implement the feature, update its documentation, and verify it
/orchestrate status
/orchestrate approve
```

Saved graphs remain inert until explicitly resumed. Graph bounds are visible,
user-configurable, and extendable by the user; they never weaken permission,
scope, verification, or publication gates. Read the
[Orchestrated Goal guide](docs/USER_GUIDE.md#orchestrated-goal)
for choosing the mode and operating it, and the
[architecture strategy](docs/ORCHESTRATION_STRATEGY.md) for its evidence,
authority, and recovery contracts.

## Documentation

| Topic | Documentation |
| --- | --- |
| Install, upgrade, rollback, uninstall | [Installing](docs/INSTALLING.md) |
| Setup, providers, configuration, TUI, tools, workflows | [User guide](docs/USER_GUIDE.md) |
| Security boundaries and platform sandbox details | [Security model](docs/SECURITY.md) |
| Current implementation status | [Capability matrix](docs/CAPABILITIES.md) |
| Product feature overview | [Features](docs/FEATURES.md) |
| Beta suitability and known limits | [Beta status](docs/BETA.md) |
| Headless JSONL and CI/cron examples | [Automation](docs/AUTOMATION.md) |
| Orchestrated Goal design and evidence | [Orchestration strategy](docs/ORCHESTRATION_STRATEGY.md) |
| Configuration, session, and event compatibility | [Compatibility policy](docs/COMPATIBILITY.md) |
| Linux Landlock setup | [Linux sandbox guide](docs/LINUX_SANDBOX.md) |
| MCP protocol coverage | [MCP protocol](docs/MCP_PROTOCOL.md) |
| Testing and evaluation | [Testing](docs/TESTING.md) |
| Release process and verification | [Releasing](docs/RELEASING.md) |
| Current priorities and implementation history | [Roadmap](ROADMAP.md) · [history](docs/ROADMAP_HISTORY.md) |

## Build from source

Building requires the Go version declared in `go.mod` (currently Go 1.26.5):

```sh
git clone https://github.com/robert-mcdermott/collomia.git
cd collomia
go test ./...
go build -o collo ./cmd/collo
./collo --version
```

Development and release checks are documented in [Testing](docs/TESTING.md)
and [Releasing](docs/RELEASING.md). Security issues should be reported through
the private process in [SECURITY.md](SECURITY.md).

## Project status

Collomia is in technical beta. It is suitable for interactive, reviewable
repository work with the documented limits; it is not advertised as safe for
unattended production changes. See the [changelog](CHANGELOG.md) and
[compatibility policy](docs/COMPATIBILITY.md) before upgrading.

## License

Apache License 2.0. See [LICENSE](LICENSE).
