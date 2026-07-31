# Collomia security model

This document states what each control actually guarantees, what it does
not, and where the boundaries are. It is the "documentation truth pass"
required before advertising any unattended use.

## The honest summary

Collomia's permission prompts and rules are **in-process policy checks**, not
an operating-system security boundary, unless the OS sandbox is enabled. A
command approved by you — or auto-approved by autopilot mode — still has your
normal account's authority unless the OS sandbox removes it. Collomia now
requests that enforcement by default with `permissions.sandbox: auto` on
macOS, Linux, and Windows 11, but `auto` visibly degrades when a backend is
unavailable; use `require` when degraded execution is unacceptable. The
Windows backend uses only inbox AppContainer and Job Object APIs; it does not
require Windows Sandbox, Hyper-V, an administrator-installed driver, or
another runtime.

Do not point autopilot mode at untrusted code or untrusted instructions and
walk away, on any platform, without the sandbox in `require` mode — and even
then, understand the limitations listed below. This applies equally to
background processes started with `start_process`: they run under the same
policy and sandbox as `run_command`, just detached from the turn that
started them.

## Autonomy modes: exact properties

| Mode | Reads (workspace) | Writes (workspace) | Commands | Outside workspace | Network |
| --- | --- | --- | --- | --- | --- |
| `ask` | auto-allowed | prompt | prompt | prompt + `allow_outside_workspace` | uncontrolled once a command runs |
| `workspace` | auto-allowed | auto-allowed | prompt | prompt + `allow_outside_workspace` | uncontrolled once a command runs |
| `autopilot` | auto-allowed | auto-allowed | auto-allowed¹ | auto-allowed only with `allow_outside_workspace` | uncontrolled unless sandboxed |

¹ With safety constraints that hold in every mode:

- Commands matching `permissions.denied_commands` are always refused.
- Commands with a classified catastrophic outcome are always refused.
- Destructive commands classified for one-time confirmation always prompt.
- Commands classified as publication always prompt (or are refused) under
  `permissions.publication`, whose default is `prompt`.
- Commands the static analyzer cannot fully read (substitutions, `eval`,
  inline interpreter payloads, variable commands) always require an
  interactive approval.

Allow rules, tool/session grants, autopilot, and an interactive "always allow"
choice cannot bypass these constraints. "Always allow" never sticks for an
uninspectable or one-time-confirmation command.

Command denials are monotonic across configuration scopes. Built-in
catastrophic-command patterns are mandatory, global patterns append to them,
and trusted project patterns append to the combined set. A lower scope cannot
remove inherited patterns, including by specifying an empty list.

MCP tool calls (`external` risk) always prompt unless a rule or session
grant allows them; they never ride along with autopilot.

### What the checks can and cannot stop

- **Path containment** (`internal/tools/path.go`) canonicalizes paths and
  resolves symlinks before comparing against the workspace root. It governs
  the built-in file tools only. A shell command is *not* path-checked — `cat
  /etc/passwd` runs if the command itself is approved. This is why command
  approval exists, and why the sandbox matters.
- **Rooted file mutation** re-checks the approved target through an
  operating-system directory root when `write_file`, `edit_file`,
  `apply_patch`, or `/undo` performs its final operation. Parent traversal and
  a parent symlink swapped during the operation cannot redirect that mutation
  outside the authorized root. Replacements are written to a private file in
  the same directory, synced, and atomically renamed; Collomia does not
  truncate the old inode, so an existing hard link cannot turn a workspace
  edit into an edit of another name. Deletes remove only the rooted directory
  entry. The original permission mode is preserved across edits and undo where
  the operating system exposes POSIX-style mode bits.
- **Command analysis** (`internal/shell`) is conservative by design: it
  identifies common catastrophic outcomes, requires confirmation for known
  destructive operations, and prompts when it cannot resolve an effect. It is
  an accident-prevention policy aid, not a shell security boundary. A novel or
  maliciously obfuscated command is still bounded only by what the OS allows
  your user to do.
- **Denied-command regexes** are an additive defense-in-depth layer for local
  policy. Regexes cannot enumerate harm; the built-in structural checks do not
  depend on users maintaining regex syntax.

Rooted mutation protects Collomia's structured file tools; it does not make an
approved shell command use those primitives. `apply_patch` validates the whole
change set before publishing and attempts rooted rollback if a later publish
fails. It is not a filesystem transaction against unrelated concurrent
writers: another program changing an approved file at the same time can still
win before or after one atomic replacement. Keep important work in version
control, inspect `/diff`, and use the OS sandbox when commands themselves are
untrusted. Atomic publication deliberately creates a new inode: it preserves
content and portable permission bits, but breaks hard-link identity and may not
preserve platform-specific ACLs, extended attributes, or special ownership.
Use a reviewed command or a metadata-aware external tool when those attributes
are part of the file's contract.

## Command safety tiers

The same analyzer and execution-time check cover `run_command`, PTY commands,
and `start_process`. Transparent wrappers such as `sudo`, `doas`, `env`,
`command`, `timeout`, and `nohup` are unwrapped. Literal `sh -c`, `cmd /c`, and
PowerShell `-Command` payloads are inspected recursively. Relative paths are
resolved from the workspace while tracking straightforward `cd` segments.

### Tier 1: non-overridable catastrophic denials

These are refused without showing an approval dialog:

- Recursive deletion or recursive permission/ownership changes aimed at a
  protected root: filesystem/drive roots, the user's home, the workspace
  root, the repository `.git` state, `~/.collomia`, important OS roots, and
  detected mount/volume roots. Broad forms such as `/*`, `$HOME/*`, and a
  workspace-root `*` are included.
- Direct destruction or overwrite of Collomia safety configuration,
  repository metadata, critical account files, `/proc/sysrq-trigger`, or raw
  kernel-memory targets.
- Filesystem creation, wiping, partitioning, raw copying, redirection, or
  similar destructive writes aimed at physical block devices, macOS disk
  identifiers, or Windows physical drives/volumes. Ordinary disk-image files
  inside the workspace are not classified as physical devices.
- Any command matching an effective `permissions.denied_commands` regex.

There is intentionally no approval flag or lower-scope override for this tier.
When a user genuinely intends to administer a physical disk or destroy a
protected root, they must run that operation themselves outside Collomia.

### Tier 2: mandatory one-time confirmation

These can be legitimate, so they remain available, but every invocation needs
a new human decision—even in autopilot and even when an allow rule matches:

- Destructive Git operations such as hard reset/restore/clean, forced push or
  worktree removal, history rewriting, stash/reflog deletion, and aggressive
  pruning. Dry-run Git cleanup remains routine.
- Shutdown/reboot and similar machine-lifecycle operations.
- Bulk or high-impact Terraform, Pulumi, Kubernetes, Helm, Docker/Podman,
  AWS, Azure, Google Cloud, SQL database, and logical-storage deletions.
- Recursive deletion whose target contains unresolved variables or other
  dynamic path syntax, plus `find`-driven dynamic deletion.
- Commands whose complete effect the analyzer cannot inspect, including an
  interpreter that reads its program from a pipe (`curl … | sh`): the code
  that will run is not in the command text and does not exist yet.

The approval dialog does not offer a persistent grant for this tier. In a
headless run, the operation fails closed because no human approver is present.

### Publication sits alongside tier 2

Tier 2 above is a taxonomy of *destruction*, and for a long time that was the
whole of the risk model. Every deletion in the cloud and packaging tools
required a fresh decision even under autopilot — `terraform destroy`,
`kubectl delete`, `helm uninstall`, `aws s3 rm --recursive`, forced push,
`git reset --hard` — while none of their publishing counterparts did. On a
stock configuration under `autopilot`, `npm publish`, `cargo publish`,
`twine upload`, `docker push`, `gh pr create`, `gh pr merge`, `gh release
create`, `kubectl apply`, `helm upgrade`, `terraform apply -auto-approve`,
`aws lambda update-function-code`, `git push origin main`, and `ssh prod
"systemctl restart app"` were all approved silently.

That asymmetry did not reflect reversibility. A published package version is
harder to take back than a Kubernetes deployment a controller will recreate.

`permissions.publication` (`off`, `prompt` by default, `deny`) governs an
action that puts something outside this machine, in six categories: package
registry, container registry, source remote, code forge, infrastructure, and
remote host. The complete catalogue, the read verbs that stay ordinary, and
the rehearsal switches that suppress it are documented under
[Publishing outside this machine](USER_GUIDE.md#publishing-outside-this-machine).

Under the default it behaves like tier 2 in the way that matters: a blanket
allow rule naming only an executable, a tool-wide "always allow", and
`autopilot` all decline to cover it, and a headless run with no approver fails
closed. It differs in the same three respects credential protection does. A
rule that names the *operation* is honored, so an intentional exception stays
expressible and written down; the setting can be raised to `deny`, which the
`hardened` preset selects; and the approval dialog offers one narrow session
grant scoped to the exact operation shown.

That grant covers that operation and nothing else — never the tool, never the
executable, never a sibling operation of the same tool, and never past this
process. Raising the setting to `deny` mid-session invalidates a grant handed
out while it was `prompt`.

**Rules and grants are deliberately not equivalent.** An `allow` rule naming
the operation outranks even `publication: "deny"`, because a rule is written
down, inspectable, reviewable, and survives the session. An interactive grant
never outranks `deny`, because it is a decision made under time pressure with
the work half-finished. The same asymmetry already governs
`protect_credentials`, where a rule naming a path outranks `deny` and a grant
does not.

**What this is not.** Like host rules, it is a policy layer and not
enforcement. It reads what a command's text says it will do; a build script
that uploads an artifact without naming the operation on a command line is
invisible to it, and preventing that traffic is the OS sandbox's job. It also
classifies operations rather than consequences: `kubectl apply` against a local
cluster and against production are the same string, so the prompt names the
operation and the person supplies the context. Finally, the catalogue is
finite — a publishing tool Collomia does not recognize is not classified, and
`permissions.denied_commands` and `reviewer_command` remain the answer for
anything specific to one organization.

### Credential stores sit alongside tier 2

Reaching a well-known credential store — an SSH or GPG private key, a cloud
CLI token cache, a registry authentication file, a `.env` — is governed by
`permissions.protect_credentials` (`off`, `prompt` by default, or `deny`).

Under the default it behaves like tier 2 in the way that matters: a blanket
allow rule, a tool-wide "always allow", and `autopilot` all decline to cover
it. It differs from tier 2 in three respects. A rule that names the path is
honored, so an intentional exception is expressible and stays written down —
and it is honored even under `deny`, for the same reason a rule outranks
`publication: "deny"`: it is written down and reviewable in a way an
interactive answer is not. The setting can be raised to `deny`, which the
`hardened` preset selects; and the approval dialog offers one narrow session
grant, scoped to the exact credential target shown.

That grant covers that target and nothing else — never the tool, never the
directory, never a sibling file that classifies the same way, and never past
this process. An action reaching one granted and one ungranted store still
prompts. Under `deny` no grant is offered at all, and raising the setting to
`deny` mid-session invalidates a grant handed out while it was `prompt`.

The grant exists because a control with no durable answer is a control people
switch off. A project whose tests read its own `.env` would otherwise face the
same prompt on every read, and a prompt answered dozens of times is a prompt
nobody reads. The approval dialog also shows the rule that ends the asking
permanently, so the session grant is the convenient answer and the
configuration rule is the durable one.

The threat is specific. Redaction runs on Collomia's transcript, audit ledger,
and events — it does not sit between a tool result and the provider, because
an agent has to see the files it was asked to work on. A credential a command
reads therefore reaches the model. Keeping it out is a permission decision, and
this is that decision.

Two limits follow from how it works. Recognition is by conventional location,
not by inspecting contents, so a key stored somewhere unusual is not covered.
And it describes what a command's text names, not what the process opens —
confining reads at the OS level is the sandbox's job. The complete list of
protected and deliberately exempt locations is in the
[user guide](USER_GUIDE.md#credential-files), and a test fails if that list and
the implementation drift apart.

### Tier 3: normal permission flow

Scoped cleanup and ordinary development operations continue through the
configured `ask`, `workspace`, or `autopilot` flow. Examples include:

```sh
rm -r directory
rm -rf node_modules
rm -rf /tmp/example
cd build && rm -rf -- *
git clean --dry-run
mkfs.ext4 ./test-disk.img
dd if=/dev/zero of=./test-disk.img count=1
```

The `*` in the `build` directory is recognized as scoped because Collomia
tracks the preceding `cd`. Test a decision without executing it:

```sh
collo policy check 'rm -rf /*'          # deny, source: safety
collo policy check 'git reset --hard'   # prompt, source: safety
collo --autonomy autopilot policy check 'rm -rf node_modules'  # allow
```

## Containment presets

`permissions.preset` bundles the containment switches so a coherent policy is
one line rather than eight decisions. It is sugar over the same fields, never
a hidden mode:

| | `frictionless` | `standard` (default) | `hardened` |
| --- | --- | --- | --- |
| `sandbox` | `off` | `auto` | `require` |
| `sandbox_allow_read_outside_workspace` | `true` | `true` | `false` |
| `network` | `open` | `open` | `scoped` |
| `commands` | `open` | `open` | `allowlist` |
| `command_env` | `full` | `minimal` | `minimal` |

- Fields you set explicitly always win over the preset in the same layer,
  whichever is stricter.
- **A repository can tighten containment but never weaken it.** This is one
  rule with no exceptions, covering `sandbox`, `sandbox_allow_network`,
  `sandbox_egress`, `sandbox_allow_read_outside_workspace`, `command_env`,
  `network`, `commands`, and `allow_outside_workspace`, and applying identically to an
  explicit field and to a preset. A project file's `frictionless` cannot undo
  a global `hardened`, and neither can a project file's explicit
  `"sandbox": "off"`. Refusals are listed by `collo config show` and
  `collo config validate` rather than applied silently, so an ignored setting
  never looks like a bug. Trust decides whether the project layer is read at
  all; this rule decides what it may do once trusted.
- Your own global configuration is not restricted this way — a built-in
  default is not a choice you made, so `"sandbox": "off"` and
  `"preset": "frictionless"` work as written there. That is where the
  compatibility escape hatch lives.
- No preset sets `mode`. A bundle that quietly selected autopilot would be the
  exact surprise presets exist to avoid.
- `frictionless` removes OS containment, not the permission engine. Prompts,
  catastrophic-command denials, one-time confirmations, and the audit ledger
  are unchanged. It is never a default, and only the machine owner's global
  configuration can select it.

The effective stance is always visible: the TUI's autonomy badge carries `⛨`
when OS containment is configured, `⛉` when it is not, and `⛨!` when the
platform applied less than was requested. The Session tab lists the full
picture including session grants.

## Declared network endpoints

Collomia reads the endpoints a command's own text names — a `curl` or `wget`
URL, an `ssh`/`scp`/`rsync` destination, a Git remote given as a URL — and the
configured endpoint of an HTTP-transport MCP server. Those endpoints feed the
`host` matcher in `permissions.rules` and appear in the audit ledger.

**What this is not.** It is not egress enforcement and not a network boundary.
It describes what a command *says* it will contact. Any approved program can
open a socket to anywhere your account can reach without ever naming it on a
command line, and this layer will not see it. Outbound access for sandboxed
commands is governed by the OS sandbox: the all-or-nothing
`permissions.sandbox_allow_network` by default, or the per-host broker
described under [Scoped egress](#scoped-egress-macos-only), which is
enforcement on macOS only.

Three properties keep the policy layer honest:

- An endpoint the analyzer cannot read is reported as **undetermined**, never
  as "no endpoints". `git push origin`, `npm install`, and `curl -K file` all
  resolve their endpoints elsewhere, so they are undetermined.
- An `allow` rule scoped to a host never matches an action with undetermined
  endpoints, exactly as it never matches a command the analyzer could not
  read. A `deny` or `prompt` rule still fires on the endpoints that were
  readable.
- A session grant can only ever cover values the user was shown, so an
  undetermined endpoint cannot be granted at all.

A `deny` rule therefore blocks only endpoints a command names. With
`{"action":"deny","host":"*.evil.com"}` configured, `curl https://drop.evil.com/x`
is denied, while `curl -K endpoints.txt` and `npm install` are not — their
endpoints are undetermined and the rule has nothing to match against. Setting
`permissions.network: "scoped"` closes that path by turning every unnamed
endpoint into a prompt instead of an approval. Neither is a substitute for
`sandbox_allow_network: false` when traffic must actually be prevented.

`permissions.network: "scoped"` additionally withholds automatic approval from
every network-bearing action that no rule or grant covers. It can only escalate
to a prompt; it never allows, denies, or blocks a socket. `permissions.commands:
"allowlist"` does the same for executables. Both default to `open`, which is
the behavior of earlier releases, and both are monotonic across configuration
layers: a project file can tighten them but cannot loosen them.

### Rules can name an operation

A rule's `command` matcher has two forms. Without a space it is an
executable-name glob (`npm`, `git`, `g*`), matched against every `argv[0]` the
command runs. With a space it is an **operation** glob (`npm publish`,
`git push`, `gh pr create`, `ssh build-host`), matched against the executable
plus the leading words that decide what it does.

The second form exists because an executable name cannot distinguish
installing a dependency from publishing a package — both are `npm` — so the
only expressible policies were "allow the package manager entirely" or "prompt
for every use of it". Neither is a policy anyone keeps.

Two properties keep this honest, both learned from the `host` matcher shipping
inert:

- An operation pattern never falls back to matching an executable. `{"action":
  "deny", "command": "npm publish"}` does not deny `npm install`.
- A pattern that could match neither form — leading, trailing, or repeated
  spaces — is rejected by `collo config validate`. Before operations existed,
  a `command` value containing a space was matched against `argv[0]`, matched
  nothing, and validated clean: a rule that read as protection and was inert.
  That failure mode is now a validation error rather than a silent one.

Run `collo policy check '<command>'` to see the exact operation string a
command produces. Do not guess it.

### Built-in web tools: a real address boundary

`web_search` and `web_fetch` are the exception to the paragraph above. They do
not describe an endpoint and hope; they enforce one, because Collomia opens
these connections itself rather than handing a command line to another program.

The guard is installed on the dialer's `Control` hook, so it runs against the
**resolved IP immediately before the socket is opened**, not against the
hostname. Three consequences follow, and they are the reason for this design:

- A hostname that resolves to a public address on the first lookup and a
  private one on the second (DNS rebinding) is refused on the second.
- Every redirect hop opens its own connection and is checked identically. No
  redirect chain can walk from a public host to an internal one.
- No name-based bypass exists. A CNAME to `localhost`, a public DNS record
  pointing at `10.0.0.5`, and a literal `http://[::ffff:127.0.0.1]/` all fail
  at the same place, on the address.

Refused: loopback, the unspecified address, RFC 1918 and IPv6 unique-local
ranges, link-local (which is where the cloud instance metadata services at
`169.254.169.254` and `fd00:ec2::254` live), interface-local and ordinary
multicast, carrier-grade NAT (`100.64.0.0/10`), IETF protocol assignments,
the TEST-NET documentation ranges, the RFC 2544 benchmark range, `240.0.0.0/4`,
the IPv6 documentation range, and any of the above reached through an
IPv4-mapped or NAT64-translated address.

**There is no configuration key that disables this.** The package exposes an
option to permit private addresses; it is set only by that package's own tests
and is not reachable from configuration, the command line, or the environment.
A setting to turn this guard off is precisely the setting a prompt injection
would try to talk a user into adding. To reach a development server or an
intranet host, use `run_command` with `curl`, where command permission,
shell-safety analysis, and the OS sandbox all apply.

Three further properties bound what these tools can do:

- **No inherited proxy.** The transport ignores `HTTP_PROXY`/`HTTPS_PROXY` and
  friends. An inherited proxy would carry a model-chosen request to a host the
  guard never inspected, routing straight around it.
- **No credentials, no state.** URL userinfo (`https://user:pass@host/`) is
  stripped before the request. There is no cookie jar, so nothing one fetch
  receives is replayed to anything else. The transport is not shared with the
  provider client, so no connection state or header from credential-bearing
  traffic can reach a host the model chose.
- **Bounded input.** 5 MiB per response (refused up front on an oversized
  declared `Content-Length`), 30 seconds per retrieval, 1 MiB of extracted
  text, and 5 same-site redirects. Non-text responses are refused with their
  type and size rather than inlined.

A redirect leaving the requested site is reported to the model and not
followed. This is a permission property: `web_fetch` declares the host of the
URL it was given, and a rule or session grant approving that host must not
become approval for wherever a redirector points. `web_search` symmetrically
declares *every* endpoint it may fail over to, so a host-scoped `allow` rule
that covers only the primary endpoint does not cover the action at all.

Both tools carry external risk, so autopilot never approves them silently, and
both are subject to `permissions.network: "scoped"` like any other
network-bearing action. `options.disabled_tools` removes them entirely.

### Configuration denials remain additive

`permissions.denied_commands` augments the classifier with organization- or
project-specific regular expressions. Built-in regexes are always present;
global patterns append to them, and trusted project patterns append to that
combined set. Empty or `null` lists cannot clear inherited denials, and exact
duplicates are collapsed. A project can therefore tighten global policy but
cannot weaken it.

## The OS sandbox

`permissions.sandbox: "auto" | "require"` wraps every agent command —
including background processes started with `start_process`, and commands
run under a pseudo-terminal (`run_command` with `pty: true`) — in the
platform's containment mechanism.

Sandboxing defaults to `auto`: Collomia uses the platform backend when it is
available and emits a visible warning before continuing with normal user
privileges when it is not. `require` fails closed instead, while `off` is an
explicit compatibility escape hatch selectable only in your global
configuration. An existing global file containing `off` remains off and is
never rewritten. A project file containing `off` is refused and reported, and
the inherited mode is kept — a repository can tighten containment but never
weaken it.
`sandbox_allow_network` and `sandbox_allow_read_outside_workspace` both
default to `true`, preserving the network and dependency reads used by package
installation and developer toolchains. Write confinement can still require a
narrow external cache grant, and the sandbox's implicit minimal command
environment can require a deliberate environment override. Users opt into
network denial or user-data read confinement by setting the corresponding
value to `false` explicitly.
These switches control only `run_command`, PTY commands, and `start_process`.
Provider HTTP, remote MCP, hooks, and language servers run in the Collomia
process and are not blocked by command-sandbox read/network policy.

**macOS: Seatbelt** (`sandbox-exec`):

- File writes are confined to the workspace, the temp directories, and
  `/dev`; everything else is deny-by-default.
- With `sandbox_allow_read_outside_workspace: false`, file-content reads from
  user homes and mounted volumes are denied except for the workspace,
  temporary directories, executable directories from `PATH`, explicit
  `sandbox_readable_roots`, and writable roots. Public operating-system
  runtime paths remain readable. File metadata remains visible so a shell can
  resolve paths and report a normal permission failure without leaking file
  contents. This is a user-data boundary, not an attempt to hide public system
  configuration.
- Network egress is denied unless `permissions.sandbox_allow_network` is
  true. Loopback connect, bind, and inbound operations stay open for local
  model and development servers; those exceptions are operation-specific so
  matching a local ephemeral address cannot accidentally reopen remote egress.
- `sandbox-exec` is deprecated by Apple but functional; treated as
  best-effort OS enforcement, tested in `internal/tools/command_test.go`.

#### Scoped egress (macOS only)

`permissions.sandbox_egress: "scoped"` narrows the all-or-nothing switch to a
per-host allowlist. Under it, the sandbox denies direct remote egress while
leaving loopback reachable, and the command is pointed at a Collomia-owned
proxy on `127.0.0.1` that dials only the hosts named by `allow` rules carrying
a `host`. The allowlist is the same rule list the policy layer evaluates, so
there is no second place to describe reachable hosts.

Exact properties:

- **The destination comes from the proxy request itself** — the CONNECT
  authority or an absolute-form request URI — and the broker dials exactly that
  host. A client cannot name one destination and reach another, which is why no
  SNI inspection is involved.
- **No TLS interception.** An approved tunnel is spliced byte for byte. No
  certificate is substituted and nothing decrypts the traffic.
- **An unreadable destination is refused, not guessed.** The broker normalizes
  authorities through the same function the policy layer uses, and refuses
  anything it cannot read exactly.
- **Background processes are brokered on the same terms**, with the broker's
  lifetime following the process rather than the tool call that started it.
- **Inherited proxy variables are dropped** before the broker's are set, so a
  `NO_PROXY` in the parent environment cannot route a command around it.
- **Refusals are reported**, naming the host and the rule that would permit it,
  in the command's output and the audit ledger.

This is enforcement only in combination with a sandbox that denies direct
remote traffic, and that combination exists on macOS alone:

| platform | scoped egress | reason |
| --- | --- | --- |
| macOS (Seatbelt) | enforced | remote egress denied, loopback kept, so the broker is the only route out |
| Linux (Landlock) | unavailable | Landlock filters TCP by port and never by address; allowing the broker's port allows every remote host on that port, and the adversary this targets chooses its own port |
| Windows (AppContainer) | unavailable | AppContainer blocks loopback to unpackaged local services, so a sandboxed command cannot reach the broker at all |

On Linux and Windows the setting is refused under `sandbox: "require"` and
degrades visibly under `auto`, leaving `sandbox_allow_network` in charge.
With `sandbox: "off"` no broker starts anywhere: without OS-level denial a
proxy is a convention any program can ignore, and Collomia does not present a
cooperative control as a boundary. No preset sets `sandbox_egress`.

**Linux: Landlock** (kernel 5.13+, via a hidden `collo __landlock`
re-exec shim — Landlock restricts the calling process, so the command is
re-executed through the shim, which applies the ruleset to itself and then
execs the real command):

- File write rules are available on ABI v1, but ABI v1–v2 cannot deny a
  standalone `truncate(2)` operation; ABI v3 (Linux 6.2) is the recommended
  minimum for robust write confinement. Other writes are confined to the
  granted roots (the workspace and temp directories). With
  `sandbox_allow_read_outside_workspace: false`, Landlock
  also handles read/execute access and grants it only to the workspace,
  temporary/writable roots, conventional system runtime/configuration roots,
  executable directories from `PATH`, and explicit
  `sandbox_readable_roots`. System roots such as `/usr`, `/lib`, and `/etc`
  stay readable so normal dynamically linked tools, TLS, identity lookup, and
  package clients continue to work; ungranted user data does not.
- Landlock ABI v4+ denies TCP connect/bind unless
  `permissions.sandbox_allow_network` is true. ABI v10+ additionally handles
  UDP bind/connect/send, including DNS. Below ABI v4, only the filesystem is
  confined; on ABI v4–v9, UDP remains reachable and `collo doctor` reports
  TCP-only isolation.
- `require` checks capabilities rather than merely checking that Landlock
  exists. With `sandbox_allow_network: false`, ABI v4–v9 fails closed because
  UDP cannot be denied completely; ABI v10+ satisfies the full network-denial
  request. `auto` still applies filesystem confinement and the available
  network controls while reporting any limitation in `collo doctor`, `/status`,
  and command output.

The Linux kernel's [Landlock userspace API documentation](https://docs.kernel.org/userspace-api/landlock.html)
defines the read/execute rights, TCP and ABI v10 UDP rights, ruleset layering,
and special-filesystem limitations used for this capability reporting. The
project's [Linux sandbox setup and Landlock compatibility guide](LINUX_SANDBOX.md)
provides the kernel/ABI matrix, Ubuntu 26.04 behavior, configuration recipes,
host verification, custom-kernel requirements, and container/WSL
troubleshooting.

**Windows 11: AppContainer + Job Object**:

- A workspace-specific AppContainer SID provides low-integrity filesystem,
  registry, credential, device, network, and cross-process isolation.
- Collomia grants that SID access to the workspace, the user temp directory,
  explicit `permissions.sandbox_readable_roots` (read/execute), and explicit
  `permissions.sandbox_writable_roots` (read/write). User-local executable
  directories on `PATH` receive read/execute access, not write access. The
  normal user's existing access checks still apply as well. AppContainer
  always restricts user-data reads even though the compatibility read switch
  defaults to broad reads on macOS/Linux; granting the whole user profile is
  deliberately not used to weaken the Windows boundary.
- With `sandbox_allow_network: false`, no Internet or private-network
  capabilities are placed in the process token. With it set to `true`,
  Collomia grants the `internetClient` and `privateNetworkClientServer`
  capabilities. Windows still blocks AppContainer loopback to ordinary local
  processes by default; Collomia does not request an administrator-only
  loopback exemption. Use `sandbox: off` for a command that must connect to an
  unpackaged localhost development server.
- The initial process is created suspended, assigned to a Job Object with
  `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, and then resumed. Descendants inherit
  the job so cancellation, timeout, or shim termination closes the job and
  kills the tree. A lifecycle-only launch broker pauses each new descendant
  before user-mode execution and gives it the same private `NUL` device map.
  This keeps unmodified Go, Python, Rust, and other developer tools compatible
  without opening the host `NUL` device to AppContainers or changing a global
  device ACL; the broker does not inspect memory, set breakpoints, or suppress
  application exceptions. Windows exposes this pre-execution lifecycle through
  its debug-event API, so a sandboxed process can observe that a debugger is
  attached. A workflow that must own the Windows debugging relationship itself
  may need an explicit `sandbox: off` compatibility exception.
- Windows stores a small per-workspace AppContainer profile and an inheritable
  ACE naming that container SID on granted roots. The ACE gives no access to
  ordinary users or unrelated AppContainers and is reused on later commands.

Microsoft's [AppContainer launch documentation](https://learn.microsoft.com/en-us/windows/win32/secauthz/implementing-an-appcontainer)
describes the inbox profile, SID/DACL, capability, low-integrity, and process
attribute APIs used here. Its [loopback guidance](https://learn.microsoft.com/en-us/windows/apps/develop/communication/interprocess-communication)
documents why access to an unpackaged localhost process needs an administrator
exemption that Collomia deliberately does not create.

Shared limitations, stated plainly:

- Read confinement is opt-in on macOS/Linux and always present for Windows
  AppContainer. It protects ordinary user data, not public operating-system
  files, executable PATH entries, temp paths, or explicit grants. On macOS,
  metadata remains visible and the content boundary targets user homes and
  mounted volumes because a global Seatbelt content denial prevents stable
  process startup on current macOS. Linux Landlock provides the stricter
  allowlisted filesystem view, but pseudo-filesystems have kernel-specific
  mediation limits. Do not describe either mode as a secret vault.
- `auto` never silently equates partial enforcement with a complete sandbox:
  it emits an actionable degradation warning. `require` refuses a command if
  any requested write, read, or network protection is unavailable.
- Network policy remains all-or-nothing for a command. Domain-scoped egress is
  roadmap work; environment-only proxy settings would not be a security
  boundary because a hostile command could bypass them.
- Package managers and build tools may need dependencies or caches outside the
  workspace. Prefer a narrow `sandbox_readable_roots` entry for immutable
  inputs and `sandbox_writable_roots` only for paths that must change. Writable
  roots are implicitly readable. `command_env: "minimal"` can also hide proxy
  variables or registry credentials; switch to `full` only when that tradeoff
  is intended.

## Repository trust

A repository can ship `.collomia.json` (permissions, MCP servers) plus
skills and instruction files. All of it is quarantined until you run `collo
trust` after review. Trust is bound to the file's SHA-256; any change
re-quarantines. The trust database lives at `~/.collomia/trust.json` on
macOS/Linux or `%USERPROFILE%\.collomia\trust.json` on Windows, never in the
workspace, so a repository cannot approve itself.

The trust record is anchored to `.collomia.json`. A workspace without that
file has no project configuration to approve, and the runtime treats its
project-trust state as active. Review repository-provided instructions and
skills before use; add a project configuration when you want their activation
to be bound to an explicit `collo trust` decision.

## Process control

Commands run in their own process group with a hard timeout; cancellation
and timeout kill the whole group (Unix `SIGKILL` to the group, Windows
`taskkill /T`). Sandboxed Windows commands additionally live in a kill-on-close
Job Object. Ordinary descendants are terminated on timeout — this is tested.
Detached Unix daemons that re-parent before the process-group kill are the
known residual gap.

**Background processes** (`start_process`) are a deliberate exception to
the timeout: their lifetime is the session, not the tool call, so a
dev server started this way keeps running while the agent does other work.
They still run in their own process group and are killed — group-wide —
by `stop_process`, `/ps stop`, or automatically when the session ends
(including background processes started by a delegated write-agent inside
its own worktree, which are stopped when that sub-agent's task finishes).
Nothing started this way is expected to outlive the `collo` process.
Runtime shutdown waits for tracked background-command completion after
requesting group/tree termination, with a finite safety bound so a broken
platform primitive cannot hang terminal restoration indefinitely.

## Durable fail-stop boundary

The durable session log is append-only. A short write or operating-system
storage error latches the first failure; Collomia never appends a later record
behind a torn tail. The agent checks this latch before each subsequent provider
request and again before a tool crosses its permission/start boundary. The
same check is inherited by delegated agents. This prevents a failed session
record from being followed silently by another external or mutating action.

A tool already executing when storage fails cannot be undone. If its result
was not accepted, resume labels that call `interrupted`, says that it may or
may not have taken effect, and never replays it automatically. The user must
inspect the workspace or external system before retrying.

Immutable session attachments and retained oversized results are accepted only
after write, sync, and close complete; catchable failures remove the partial
file. Rooted source mutations write a private same-directory temporary and
publish with atomic rename. A forced process death therefore leaves the
destination entirely old or entirely new, never partially replaced. A kill
before rename can leave an owner-only `.collomia-*.tmp` file. That orphan is
not model-visible or executable and may be removed after confirming no active
Collomia operation owns it.

This fail-stop guard covers the durable conversation/session store. The debug
log remains a best-effort diagnostic: its availability is checked by
`collo doctor`, but a later I/O failure in it does not stop a tool.

The permission audit ledger is deliberately neither of those. A ledger write
failure does not stop a tool — refusing work the user already authorized
because a record could not be filed would be the wrong trade — but it is never
silent either. Each failure is counted, the first is reported to the session as
a warning, and the next entry that does reach disk is preceded by a `gap`
record naming how many entries were lost, when the loss began, and why. The
count is latched for the life of the session and shown in the Session tab's
Security block, so "is this record complete?" is answerable while the session
is still running rather than only afterwards.

That property is what lets a reader trust a ledger with no gap records in it.
`collo audit` leads with an integrity line stating any declared gap, any line
that would not parse, and any older generation discarded at rotation, before it
prints a single entry — a damaged record must not be read as an intact one.
`collo doctor` reports the same thing as a warning check.

Every entry names the session that produced it, the actor (`primary`, or
`agent:<profile>` for a delegated agent), and the delegated task id. One
workspace file receives writes from the primary agent, from every concurrently
scheduled delegated agent, and from any other Collomia process open on that
directory; without that identity those streams could not be separated again.

The ledger is bounded: one generation rotates at 64 MiB and exactly one
previous generation is retained, so a workspace's history occupies at most
128 MiB. A rotation that had to discard an older generation records that fact
in the new file rather than leaving it to be inferred from a missing one.

## Delegated-agent boundary

Delegated agents use the same security boundary as the parent and can only be
made more restrictive. An `agents.<name>` profile may choose a smaller tool or
skill allowlist, add denied tools or command regexes, add `prompt`/`deny`
permission rules, and lower the autonomy mode. Configuration validation rejects
an agent-level `allow` rule. Parent and built-in denials remain additive, and a
child cannot enable outside-workspace access, network access, user-data reads,
or a weaker sandbox policy that the parent did not have. Disabled tools are
checked again at execution time, not merely hidden from the model's tool list.

Every child gets a distinct permission manager and its own ledger handle,
writing to the workspace ledger under its own actor and task identity — so
`collo audit --actor agent:<name>` reconstructs exactly what one delegated
agent was allowed to do. A child whose ledger cannot be opened is reported as a
session warning naming that actor, because a delegated agent acting with no
record is precisely the case that must not pass unmentioned. A child
approval is shown through the normal themed approval dialog with the delegated
task name and ID, and approval affects only that proposed action. Write-capable
children get independent Git worktrees; Collomia records their changed files
and common-base hunk ranges but never commits, Git-merges, or chooses between
sibling changes automatically. Retained worktrees may contain user-reviewable
changes after a task ends. Optional `plan_step` metadata is validated against
the current structured plan and its completed dependencies; it grants no new
capability.

The session-wide FIFO scheduler bounds total and per-provider concurrency.
Each task also has a queue-inclusive timeout, maximum iteration count, and
optional token and estimated-cost budgets. Primary profiles can apply the same
budgets to the durable session. Cost enforcement requires positive
user-configured provider pricing; Collomia deliberately has no built-in price
catalog. Token/cost enforcement estimates the next input and caps requested
output before sending, then uses provider-reported usage. Because providers may
report usage only after a response, a final response can exceed the target; no
tool or later provider call proceeds after exhaustion. Missing usage fails
closed for an enabled cost budget. Accounting survives resume and cannot be
reset with profile switching or `/clear`; `/new` creates a fresh session.
Timeouts and iteration limits remain the hard fallback. These controls limit
agent work, not operating-system CPU or memory consumption, and cost estimates
are not invoices.

Reasoning effort is opt-in. Omitting it leaves provider request shapes
unchanged. Adapters translate it only to documented protocol fields and retry
without it after an explicit unsupported-field response where safe; native
Bedrock refuses to apply Claude-specific reasoning fields to an unrecognized
model family. Reasoning settings never grant tools or alter permissions.

`/agents steer <id> <guidance...>` queues bounded guidance only for the child's
next provider boundary. It cannot change an executing tool/provider call,
answer a pending approval, or widen permissions; the model-visible wrapper
states that steering grants no permission. `alt+a` exposes a deliberate
inspect/steer/stop action menu, and `/agents stop <id-or-name>` cancels one
queued or active child.

`/agents apply <id>` is an explicit copy-and-review operation, not a branch
merge. Collomia treats the durable worktree path as untrusted: it verifies Git
still registers that exact directory and branch for the parent repository,
requires branch `HEAD` to equal the recorded base commit, rejects path
traversal, symlinks, non-regular/binary/non-UTF-8/oversized files, and blocks a
child that changed paths outside its declared write scope.

Writers declare repository-relative file or directory scopes. The scheduler
case-folds comparisons conservatively and serializes overlapping,
workspace-wide, or unspecified writers while allowing disjoint writers to
run concurrently. This is not a filesystem authorization boundary: inherited
permissions and the OS sandbox remain authoritative. Actual Git changes are
checked against the declaration after execution; a violation makes the child
result an error, stays isolated in the retained worktree, and is unavailable
to guarded integration.

For supported existing text files, integration compares the recorded base,
current parent, and retained child. An unchanged parent gets the ordinary
child diff. Clean non-overlapping parent/child edits produce a composed,
selectable preview that preserves both sides. Overlapping edits produce a
bounded diff3 conflict preview and remain non-selectable; incompatible
add/delete or mode changes also fail closed. This computation does not grant
permission or write content.

The review token binds worktree/branch/base identity, exact parent and child
bytes and modes, the composed result, and conflict state. The user selects
text hunks in a floating review; normal `integrate_delegate` permission rules
still apply. The entire comparison is recomputed after interactive approval
to close the approval-time race. Rooted atomic replacement/removal, multi-file
rollback, and the ordinary change tracker then publish only selected clean
content. The child worktree and branch are retained. Collomia never creates a
merge commit, commits, pushes, chooses an overlapping resolution, or deletes
those recovery artifacts.

`options.agent_integration: "reviewed"` permits the primary model—but never a
delegated child—to inspect, verify, compare, and selectively copy retained
work. It does not add a new mutation primitive. `inspect_delegate_changes`
returns bounded evidence and numbered ordinary or clean-three-way hunks plus
two opaque SHA-256 tokens: the publication review token covers the registered
worktree/base, parent bytes, child bytes, relevant modes, composed result, and
conflict state; the verification token covers only the registered child state
so unrelated parent drift cannot falsify child test evidence.

`verify_delegate_changes` accepts exactly one repository-detected command and
requires the current child token. Its permission and hook identity remains
`run_command`; command analysis, catastrophic and configured denials,
executable rules, minimal environment, sandbox/network policy, output caps,
timeouts, descendant cancellation, audit recording, and lifecycle hooks all
remain effective. Results are bounded/redacted machine observations. All
detected commands must pass against one token for aggregate `passed` state,
and a changed child becomes `stale`. Verification never copies files or grants
permission. `/agents verify` applies the same contract one command at a time.

`compare_delegate_changes` is read-only and exposes bounded conflicts,
selectable hunks, verification, evidence, and usage for two to six candidates.
It does not choose a winner. `apply_delegate_changes` remains unavailable
without the publication token and refuses it if reviewed or three-way state
changed before authorization. The agent loop applies the ordinary write
policy under `integrate_delegate`, then calls the shared post-authorization
publication path. Rooted writes, rollback, change tracking, retained
worktrees, and the prohibition on commit/push/merge commits are identical to
`/agents apply`.

Reviewed mode is opt-in and defaults to `manual`. It lets the primary model
make a better-supported quality decision; it does not prove that decision
correct. Child-authored evidence and repository text remain data rather than
instructions. A machine-observed child pass covers only that exact retained
worktree state—not parent-only edits or interactions among integrated
candidates—so the combined parent workspace must still be verified after
publication. Clean parent drift is reviewable through the three-way preview;
overlapping drift, sibling overlap, unsupported entries, scope violations,
stale reviews or verification, and moved branches fail closed for explicit
resolution.

Closing Collomia requests cancellation for every child and stops background
processes owned by write agents. Durable sessions keep bounded status, summary,
evidence, usage, and change manifests—not raw child transcripts. On resume,
recorded completed outcomes remain visible and any nonterminal task becomes
`interrupted`; it is never automatically scheduled again. This avoids duplicate
mutations but means the user or parent must explicitly start replacement work.
Cancellation is honored while a child is queued, calling a provider, or waiting
for approval. A cancelled approval returns no permission and cannot publish the
proposed mutation; late child updates cannot revive a cancelling task.

**PTY commands** (`run_command` with `pty: true`) reach every descendant on
cancellation, by a different mechanism on each platform.

On Unix the child runs in its own session (`setsid`) rather than merely a
process group, because a pseudo-terminal's child processes attach to the
session leader; killing the session on timeout or cancellation reaches the
whole tree.

On Windows the child is attached to a pseudoconsole, created suspended, and
assigned to a job object before it is resumed, so there is no instant in which
it could spawn a descendant outside the job; cancellation terminates the job
and then waits for the kernel to release each process. Creating the child
suspended is what makes this stronger than the non-PTY path's `taskkill /T`,
which has that window. The pseudoconsole API requires Windows 10 1809 or
later; an older release reports that rather than running without terminal
semantics. There is no SIGTERM to send first — `GenerateConsoleCtrlEvent`
requires the sender to share the target's console, which a pseudoconsole host
does not — so a one-shot command is terminated directly, and only the
interactive browser-terminal session has a graceful step (closing the child's
console input, with a short deadline before the job is terminated).

## Browser-terminal boundary

`collo --web` is a terminal transport around the same TUI, not a separate
agent service. It starts a child `collo tui` in a real PTY — a pseudoconsole
on Windows — so the child has the same workspace, environment, provider
credentials,
configuration, tools, and permission policy as a normal terminal session.
The browser receives and sends terminal bytes; it cannot choose a different
executable, working directory, or environment through HTTP.

The initial implementation is deliberately local-only:

- The listener always binds to `127.0.0.1`; there is no remote-host option.
- The port is randomly assigned by default. `--web-port` selects only the
  loopback port and does not change the bind address.
- Every invocation generates a 256-bit random bearer token. The launcher puts
  it in the browser URL fragment (which HTTP requests do not transmit), the
  page removes the fragment from browser history, and JavaScript sends the
  token in the first WebSocket message before the PTY starts.
- The server requires the exact origin it served, applies restrictive browser
  security headers, and accepts only one authenticated controlling connection.
- Closing the controlling browser connection terminates the PTY session and
  its process group. Page refresh/reconnection and observer sessions are not
  supported yet.

Anyone who obtains the printed URL before it is used has the same interactive
control as the user at the terminal, including the ability to answer approval
prompts. Do not share it. Do not put this server behind a reverse proxy, port
forward, tunnel, or non-loopback listener: there is no TLS, account identity,
remote-access policy, or idle-session authentication. The server shuts down
with the TUI. Windows web-terminal mode is rejected until a real ConPTY backend
can preserve equivalent terminal and process-lifecycle behavior.

## Provider streams

Provider requests can be retried only before a response begins, using a
replayable request body and the bounded retry policy. Once any text,
reasoning, or tool-call fragment may have reached the runtime, an in-stream
exception is returned without replaying the request. This avoids duplicate
visible output and repeat billing. Streamed tool arguments are never trusted
as executable input: the adapter must receive, assemble, and validate the
complete JSON document before the normal permission pipeline can see the tool
call. Truncated Responses and Bedrock streams fail closed instead of accepting
their partial content as a completed model response.

Recorded provider contracts run in the ordinary credential-free CI suite.
Real-endpoint qualification is separately double-gated by
`COLLO_LIVE_PROVIDER_TESTS=1` and a manifest path. The live manifest rejects
literal API keys and embedded URL credentials, resolves keys and sensitive
headers from named environment variables, and redacts resolved values from
reported failures. The synthetic tool returned by a model is inspected but
never executed. See [Live provider contract tests](LIVE_PROVIDER_CONTRACTS.md).

## Secrets

Configured provider keys, MCP headers/env values, and common credential
shapes are redacted from debug logs, JSONL events, and the audit ledger:
OpenAI, Anthropic, AWS, GitHub, GitLab, Google, npm, Stripe, and Slack keys;
JWTs; bearer tokens; `key=value` credential assignments; and PEM private key
blocks (`RSA`, `EC`, `OPENSSH`, `ENCRYPTED`, PKCS#8, and PGP), which are
removed whole. Public keys and certificates are deliberately left alone.

Redaction is best-effort defense in depth, and two limits are worth stating
because they decide what it can be relied on for. It does not sit between a
tool result and the provider — an agent has to see the files it was asked to
work on, so a secret a command legitimately reads still reaches the model, and
keeping it out is [the permission layer's
job](#credential-stores-sit-alongside-tier-2). And it is applied to bounded
chunks rather than an unbounded stream, so a credential split across two chunks
can be matched in the one carrying its recognizable prefix and missed in the
next. Neither limit defeats deliberate exfiltration, which redaction was never
positioned to stop.

For native Amazon Bedrock, `auth: "sigv4"` delegates credential discovery to
the AWS SDK chain (environment access/secret/session values, shared profiles,
IAM Identity Center, assumed/web-identity roles, and workload identity) and
signs each request without placing those credentials in Collomia's
configuration. `auth: "bearer"` sends a configured short- or long-term Bedrock
API key only in the HTTPS Authorization header. `auth: "auto"` prefers an
explicit `api_key`/`api_key_env` or `AWS_BEARER_TOKEN_BEDROCK`, then falls back
to SigV4. Set an explicit mode when both families exist and credential choice
must not vary with the environment.

The standard `AWS_BEARER_TOKEN_BEDROCK` value is registered with the redactor
when Bedrock is configured. Prefer `api_key_env` over literal `api_key`.
Collomia accepts already-generated short- and long-term Bedrock keys but does
not mint or refresh short-term bearer keys; replace an expiring token and
restart the process. AWS SDK-managed temporary SigV4 credentials retain the
SDK's normal refresh behavior.

### Optional OS credential storage

`collo auth` stores a provider API key in the macOS Keychain or the Windows
Credential Manager. It is optional and additive: the store is consulted only
after `api_key`, `api_key_env`, and a provider family's own variable, so an
exported environment variable always wins and no existing configuration changes
meaning. A machine that has never run `collo auth set` performs no credential
manager call at all — a local name index is checked first, and its absence ends
the lookup.

What the store is not: it is not a Collomia account, not a network service, and
not a file-backed secret store. The only file it keeps,
`~/.collomia/credentials.json` (mode 0600), records provider names so entries
can be listed and lookups skipped. It holds no credential material and is not
consulted as a fallback when the operating system has no entry. Linux has no
backend, and no encrypted-file substitute is offered: the passphrase would have
to be stored somewhere, and an unencrypted file would be weaker than the
environment variable it replaced.

Nothing reads a stored value back to a user, a log, or a tool result. Values
are read only to authenticate a provider request, and are registered with the
redactor exactly like a configured key.

Two platform properties are worth stating plainly:

- **macOS** entries are written through `/usr/bin/security`, which accepts the
  secret only as a command-line argument; the tool has no option to read one
  from standard input. macOS restricts reading another process's arguments to
  its owner and root, so the exposure is to root and to the user's own session,
  which already holds the unlocked keychain — for the lifetime of one
  short-lived process. Apple's signed tool is used rather than linking
  Security.framework so the binary stays cgo-free and keychain authorization is
  not re-requested for every unsigned build. In a session with no graphical
  login the keychain cannot prompt, and the command fails saying so rather than
  falling back to anything weaker.
- **Windows** entries are generic credentials named `collomia:<provider>`,
  protected by DPAPI under the current user account.

Collomia's own credential locations, including this index, remain subject to
[credential-store protection](#credential-stores-sit-alongside-tier-2): an
agent action that reaches them is its own permission decision.

### Microsoft Entra credentials

Azure OpenAI and Microsoft Foundry providers use Microsoft Entra only when the
configuration explicitly selects `auth: "entra"`. Collomia never treats an
ambient Azure CLI session, managed identity, or service-principal environment
as permission to replace an API key implicitly.

Entra mode constructs the official Azure Identity SDK's
`DefaultAzureCredential`. The resulting access token is kept in process memory,
never written to configuration, sessions, debug logs, or the audit ledger, and
is refreshed before the SDK's `RefreshOn` time or expiry. Concurrent requests
share one refresh. A token-acquisition failure is classified as authentication
and stops before provider HTTP; a partially obtained or invalid token is never
sent. Standard `AZURE_CLIENT_SECRET` and
`AZURE_CLIENT_CERTIFICATE_PASSWORD` values are registered with the redactor as
defense in depth.

The mode is intentionally deterministic at the configuration boundary:

- `auth: "api_key"` (or omitted) uses the `api-key` header.
- `auth: "bearer"` uses a caller-supplied static token and cannot refresh it.
- `auth: "entra"` rejects `api_key`, `api_key_env`, and custom authentication
  headers, then writes the current SDK token after all other custom headers.

Traditional Azure OpenAI and current Foundry endpoints use different default
audiences. `entra_scope` can override that choice, and
`entra_authority_host` can select a sovereign/private Entra authority. Both are
validated as HTTPS values; authority URLs cannot contain credentials, paths,
queries, or fragments, and scopes must end in `/.default`. Collomia does not
disable Entra instance discovery or infer sovereign audiences. Use
`AZURE_TOKEN_CREDENTIALS` to restrict `DefaultAzureCredential` to the intended
development or production credential set.

By default, agent commands (including background processes and PTY runs)
inherit your full environment, which may include unrelated secrets from
your shell. `permissions.command_env: "minimal"` strips commands down to an
allowlist — this is the default automatically whenever the sandbox is enabled
(`sandbox: "auto"` or `"require"`), and can be set explicitly without the
sandbox too.

The allowlist is exactly `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`,
`TMPDIR`, `TEMP`, `TMP`, `TERM`, `LANG`, `LC_ALL`, `LC_CTYPE`, `COLUMNS`,
`LINES`, `SYSTEMROOT`, `COMSPEC`, `PATHEXT`, `USERPROFILE`, `LOCALAPPDATA`,
and `GOCACHE`, each passed only when it is set in the parent environment. No
other variable reaches an agent command, so shell-resident credentials such
as `GITHUB_TOKEN`, `NPM_TOKEN`, `AWS_*`, and provider API keys are not
exposed to commands. There is no per-variable passthrough; a command that
needs one value should set it inline in the command string. See the user
guide's [command environment](USER_GUIDE.md) section for what this breaks and
how to work around it.

### Durable conversation and retained tool output

Session transcripts are operational history, not a secret vault. They can
contain source text, prompts, tool arguments/results, command errors, and data
returned by external services. When a returned string exceeds
`options.max_tool_output_bytes`, a durable session may additionally retain up
to 4 MiB of that result under an opaque ID; total retained-result storage is
capped at 32 MiB per session. `read_tool_result` reads bounded ranges from this
local copy and never reruns the originating action.

Session JSONL and result-artifact files live under the workspace-specific
directory in `~/.collomia/sessions/` (or `%USERPROFILE%\.collomia\sessions\`
on Windows), outside the repository, with owner-only modes where the platform
supports them. Artifacts remain outside model context until explicitly read,
are framed as untrusted content, follow forks, and follow rewind branches only
when referenced by the retained conversation prefix. They are removed with
their session and excluded
from support bundles. None of these properties redact arbitrary stored tool
output or encrypt it at rest; protect the user account and delete sensitive
sessions when they are no longer needed.

`/activity` is a read-only projection of these already-recorded runtime
events. Opening, filtering, searching, or copying from it never contacts a
provider, starts a process, grants permission, or replays a tool. Its UI keeps
only the newest 500 projected entries and redacts displayed text with the
active runtime redactor; the durable session log may contain older and more
detailed records. Activity text can still contain paths, tool summaries, and
other session content, so terminal clipboard copies should be handled like
transcript copies. Opaque `err-…` IDs contain no embedded prompt, path,
provider, session, or credential information.

## Optional external reviewer

`permissions.reviewer_command`, when set, runs before any non-read action
that the policy pipeline would otherwise auto-approve. It receives the
request (tool, summary, risk, normalized resources) as JSON on stdin; a
non-zero exit or a `{"decision":"deny"}` reply escalates the action to an
interactive prompt instead of silently allowing it. The reviewer can only
*tighten* decisions — it is never consulted for actions that would already
prompt, and a failing or misconfigured reviewer command fails closed
(escalates to a prompt), never open.

## Audit

Every privileged-action decision (tool, summary, normalized resources,
decision, source, matched rule) and every execution outcome is appended to
a per-workspace JSONL ledger under the user configuration directory —
outside the workspace, so agent-writable files cannot rewrite history.

## Support-bundle privacy boundary

`collo support bundle` performs local, read-only inspection without creating
the application runtime, contacting providers or MCP servers, opening
sessions, executing tools, or making network requests. Its default manifest
uses aggregate counts and anonymous configuration keys. It excludes
configuration values, environment variable names and values, credential
references, provider and MCP names/definitions, endpoint/model/deployment
details, workspace paths and files, prompts, transcripts, sessions, audit
records, and logs. Configuration validation failures are represented by a
generic status because detailed validator errors can echo user-defined names,
paths, patterns, or values. Default collection also suppresses environment
expansion, so `api_key_env` and MCP environment references are not fetched.
The manifest may contain up to eight recent opaque failure IDs collected from
bounded debug-log tails. Each ID is random and contains no error text, path,
session, provider, prompt, or secret. Only the identifier is copied; log
messages and attributes remain excluded unless the user requests logs.

Logs require the explicit `--include-logs` flag and are bounded to five files,
1 MiB per file, and 3 MiB total. Configured/common credential values are
redacted, exact home/workspace paths are normalized, and terminal controls are
removed. This explicit mode resolves configured secret references locally only
to register their values with the redactor; it never writes those values to the
manifest. Those transforms are defense in depth: arbitrary source/tool output
can contain sensitive material no pattern matcher recognizes. Inspect every
archive before sharing it. Bundles are created with owner-only permissions
where the operating system supports them, use same-directory atomic publish,
and refuse to overwrite an existing path even if it appears during creation.

## Prompt injection

Tool output, repository text, skills, web pages, and MCP responses are external
data. A sufficiently capable injection can still steer the model, so prose is
not the security boundary.

Every model-visible MCP tool result, resource, resource catalog, and expanded
prompt template is wrapped in an `EXTERNAL_MCP_DATA` content-derived boundary
that identifies its server, content type, subject, and byte count. Its handling
guidance explicitly permits using relevant factual and structured data while
refusing instructions, claimed authority, or claimed permissions embedded in
the payload. Terminal control characters are removed. Server-supplied
tool-schema descriptions/titles are labeled external and descriptive and are
bounded; schema comments and examples are discarded. Catalog and elicitation
metadata is likewise control-safe and bounded. A server's `trusted` setting
authorizes Collomia to connect to and run that server; it does not give the
server's returned text instructional authority.

These frames make provenance clear to both users and models, but they are not
an instruction-following guarantee. The controls that hold regardless of what
the model was told are the permission pipeline (MCP calls remain external
risk), denied commands, uninspectable-command prompts, repository/server trust
gates, rooted structured-file mutations, and—when enabled—the OS sandbox. A
credential-free adversarial evaluation verifies that an allowed MCP-like read
containing a forged permission grant still cannot authorize a workspace write.

Web search results and fetched pages get the same treatment through the same
implementation, as `EXTERNAL_WEB_DATA` frames carrying the source URL or search
engine, the content type, and the byte count. This matters more for the web
than for MCP: an MCP server is one the user chose to trust, while a fetched
page is written by whoever the search ranked. The boundary is derived from a
digest of the label, the provenance fields, and the content, so a page cannot
close a boundary it has no way to predict, and the same normalization strips
terminal control sequences before anything reaches the transcript. Titles,
snippets, and provenance values are bounded and reduced to a single line, so an
attacker-controlled page title cannot forge extra header fields or become a
second set of instructions beside Collomia's own.

The frames are not an instruction-following guarantee here either. What holds
regardless is the same list: the web tools remain external risk and are never
silently approved by autopilot; the address guard cannot be configured away, so
a page that persuades the model to fetch `http://169.254.169.254/` still gets a
refusal; a cross-site redirect cannot smuggle in a host the user never
approved; and any action the model takes *because* of what it read still passes
through the ordinary permission pipeline, denied commands, and the sandbox.

## Image attachment storage

User-selected and supported MCP-returned images are copied into the active
session's per-user storage, never into the repository. Durable session JSONL
contains only a random attachment ID, display name, MIME type, size, and
SHA-256 digest; provider requests resolve the owner-only raw blob and verify its
regular-file status, size, detected type, and digest immediately before send.
Limits are 5 MiB per image, four images per turn/tool batch, and 24 MiB per
session. Only PNG, JPEG, GIF, and WebP are accepted; active SVG and arbitrary
binary files are refused. Fork copies attachments, rewind copies only IDs
reachable from retained messages, and delete removes the attachment directory.

Attaching an image is an explicit data disclosure to the selected provider.
The submit-time read is anchored to the workspace directory, so changing a
path component or symbolic link after selection cannot redirect it elsewhere
in the user account. It does not redact pixels, strip EXIF or other embedded
metadata, or determine whether a screenshot contains credentials. Inspect and
sanitize images before sending them to a hosted model. Unsent selections are
kept only in the running TUI; user images are copied only after the
`user_prompt` hook accepts the submission, so a blocked turn leaves no blob.
MCP images retain external-data provenance and never authorize a later action.

## Reporting

Follow the repository [security policy](../SECURITY.md). Use GitHub private
vulnerability reporting rather than a public issue when exploit details,
credentials, private source, or user data may be involved.

## Release supply-chain boundary

Tagged beta releases are built only after the exact commit passes the
Linux/macOS/Windows build, uncached test, race, vet, installer, deterministic
evaluation, bounded fuzz, module-integrity, and reachable-vulnerability gates.
The build date comes from the tagged commit rather than the runner clock.
Generated binaries execute on native Linux, macOS, and Windows runners before
the workflow creates a draft release.

`checksums.txt` covers every binary and the CycloneDX SBOM. A checksum from the
same release detects corruption and mismatched files but cannot defend against
replacement of both the file and manifest. The release workflow therefore
also creates GitHub/Sigstore SLSA provenance attestations and attaches the SBOM
as an attested predicate for the binaries. Verification can bind the exact
file to this repository and `.github/workflows/release.yml`; it does not prove
that the source or dependencies are vulnerability-free.

The beta binaries are not yet Authenticode-signed, Apple-signed, or notarized.
GitHub/Sigstore provenance authenticates workflow origin but does not satisfy
operating-system platform-signing policy. See [Installing
Collomia](INSTALLING.md), [beta limitations](BETA.md), and [the maintainer
release process](RELEASING.md).
