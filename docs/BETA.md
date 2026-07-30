# Collomia beta status and known limitations

Collomia is suitable for a public **technical beta** aimed at developers who
want an interactive, inspectable terminal coding agent. Beta means the core
permission, session, provider, editing, MCP, and multi-agent paths are usable
and heavily tested; it does not mean unattended execution is risk-free or that
every roadmap feature is complete.

## Appropriate beta use

- Interactive repository work with `permissions.mode: "ask"`.
- Reviewable workspace edits backed by Git and Collomia's diff/undo tools.
- Provider, MCP, skills, hooks, LSP, web lookup, and headless evaluation in
  non-production environments.
- Default `auto` sandboxing after reviewing the platform-specific behavior and
  granting only the dependency/cache roots a toolchain actually needs.

Keep valuable repositories backed up, review permission dialogs and diffs, and
use ordinary Git branches for recoverability. Start with non-sensitive projects
when evaluating new providers, MCP servers, hooks, skills, or agent profiles.

## Important limitations

- Sandboxing defaults to capability-aware `auto`, while command network access
  and broad command reads remain available for compatibility. External caches
  may need narrow writable grants, sandboxed commands receive the minimal
  environment by default, and Windows AppContainer always confines user-data
  reads and blocks ordinary unpackaged localhost services. `auto` warns and
  continues when a backend is unavailable; use `require` for fail-closed
  operation.
- Endpoint-scoped policy for commands — host rules, the `network: "scoped"`
  posture, and per-capability session grants — describes only the endpoints a
  command's own text names. A program that opens a socket without naming it on
  a command line is invisible to that layer. OS-enforced per-host egress does
  exist as opt-in `sandbox_egress: "scoped"`, but it is experimental and macOS
  only: Landlock filters TCP by port rather than address, and Windows
  AppContainer cannot reach a loopback broker at all, so neither platform gets
  an enforcement claim it could not keep. The built-in `web_search` and
  `web_fetch` tools are the exception to all of this — Collomia opens those
  connections itself and enforces a public-internet-only address boundary that
  no configuration can disable.
- Credential files are protected by conventional location, not by detecting
  secret material. Reaching one prompts by default and is not covered by a
  broad approval, but a key stored somewhere unconventional is not recognized,
  and the check describes what a command's text names rather than what a
  running process opens. Redaction does not sit between a tool result and the
  provider, so a secret an agent legitimately reads still reaches the model.
- `autopilot` is not a promise that arbitrary commands are safe. Built-in
  catastrophic denials, policy, and OS sandboxing reduce risk but do not replace
  review, backups, source control, or host isolation. It also does not approve
  everything: actions classified as external risk, which includes MCP tool
  calls and both web tools, still require a rule, a session grant, or a person.
- macOS and Windows release binaries are not yet platform code-signed or Apple
  notarized. Release provenance is signed through GitHub/Sigstore instead.
- `run_command` with `pty: true` and `collo --web` now work on every supported
  platform. Windows uses a pseudoconsole, which requires Windows 10 1809 or
  later; on anything older the console cannot be created and the command
  reports that rather than running without terminal semantics. Windows has no
  signal equivalent to SIGTERM, so cancelling a pseudoconsole session closes
  the child's console input and terminates the job after a short grace period
  rather than asking politely first.
- MCP OAuth, experimental tasks, resource subscriptions, audio passthrough,
  annotations, and argument-level permission matching remain incomplete.
- LSP code actions are not implemented. Diagnostics, `find_definition`,
  `find_references`, `format_file`, and the lexical symbol index are available
  now, and each language server must be installed for its language.
- The built-in web tools read the public web and nothing else. They cannot
  reach a local development server, an intranet host, or a cloud metadata
  endpoint, and that boundary has no configuration escape — use `run_command`
  with `curl` for anything inside your own network. They do not run
  JavaScript, so a page that renders its content client-side comes back as a
  near-empty shell with a note saying so rather than as the article. Search
  parses DuckDuckGo's HTML endpoints, which can change without notice; a
  layout change is reported as an engine failure rather than as "no results",
  and bursts of searches are rate limited per address. Page content is
  untrusted external data: it arrives inside a provenance frame, but framing
  guides a model rather than constraining it, and the controls that actually
  hold are the permission pipeline and the address boundary.
- Multi-agent work is isolated and selectively integrated, but Collomia does
  not automatically reconcile conflicts, resume pending child work, or execute
  a complete plan graph autonomously.
- Prompt caching is requested on the Anthropic Messages routes only, with the
  provider's default five-minute lifetime, so a session resumed after a longer
  pause pays a full uncached prompt again. OpenAI-family endpoints cache
  implicitly and need nothing from Collomia. Bedrock is declared without cache
  support on purpose: its cache points vary by model and region and fail the
  whole request rather than being ignored, so support waits until it can be
  qualified against a real deployment. An endpoint that rejects a cache
  breakpoint disables caching for the life of the process after one wasted
  request, which is correct but costs that request.
- Provider behavior still depends on the selected model, account, deployment,
  regional availability, and upstream API changes. Use the capability display,
  `collo doctor`, and live provider qualification before relying on a hosted
  endpoint.
- The project has extensive automated security tests but has not completed the
  independent security review required for 1.0.
- Configuration, event, and session formats have explicit version checks and a
  documented [compatibility policy](COMPATIBILITY.md), but downgrading a global
  state directory after a newer release has written to it is not guaranteed.
- Durable-session write failures fail visibly and block later provider/tool
  boundaries. An action already executing at the instant of a disk failure or
  process death may still have taken effect; resume marks it interrupted and
  requires inspection instead of replaying it.
- The audit ledger records what Collomia's permission layer decided and what
  the resulting execution returned; it is not a system-call audit, so a program
  that was approved and then opened a socket or read a file on its own is
  outside its view. A ledger write failure does not stop the agent loop, but it
  is reported and declared in the file as a gap, so an incomplete record never
  reads as a complete one — check with `collo audit` or `collo doctor`.
  Redaction is best-effort pattern matching, so review a ledger before sharing
  it.

Do not advertise the beta as safe for unattended production changes,
deployments, credential-bearing automation, or security-critical environments.

## Reporting problems

Run `collo doctor` first. For reproducible runtime failures, note the opaque
`err-…` identifier and create a privacy-conscious bundle with:

```sh
collo support bundle
```

The default bundle excludes configuration values, prompts, source files,
session transcripts, audit content, and logs. Review any bundle before sharing
it. Use the repository's [security policy](../SECURITY.md) for vulnerabilities;
use an ordinary issue for non-sensitive defects and usability feedback.
