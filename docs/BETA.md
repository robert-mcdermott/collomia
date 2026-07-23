# Collomia beta status and known limitations

Collomia is suitable for a public **technical beta** aimed at developers who
want an interactive, inspectable terminal coding agent. Beta means the core
permission, session, provider, editing, MCP, and multi-agent paths are usable
and heavily tested; it does not mean unattended execution is risk-free or that
every roadmap feature is complete.

## Appropriate beta use

- Interactive repository work with `permissions.mode: "ask"`.
- Reviewable workspace edits backed by Git and Collomia's diff/undo tools.
- Provider, MCP, skills, hooks, LSP, and headless evaluation in non-production
  environments.
- Opt-in sandboxing after reviewing the platform-specific behavior.

Keep valuable repositories backed up, review permission dialogs and diffs, and
use ordinary Git branches for recoverability. Start with non-sensitive projects
when evaluating new providers, MCP servers, hooks, skills, or agent profiles.

## Important limitations

- Sandboxing is opt-in. The compatibility-first defaults keep command network
  access and broad command reads available so package managers and developer
  tools continue to work. Domain-scoped egress grants are not implemented.
- `autopilot` is not a promise that arbitrary commands are safe. Built-in
  catastrophic denials, policy, and OS sandboxing reduce risk but do not replace
  review, backups, source control, or host isolation.
- macOS and Windows release binaries are not yet platform code-signed or Apple
  notarized. Release provenance is signed through GitHub/Sigstore instead.
- Windows PTY/ConPTY support and the browser-terminal backend remain pending.
- MCP OAuth, experimental tasks, resource subscriptions, audio passthrough,
  annotations, and argument-level permission matching remain incomplete.
- LSP definitions, references, formatting, and code actions remain incomplete;
  diagnostics and the lexical symbol index are available now.
- Multi-agent work is isolated and selectively integrated, but Collomia does
  not automatically reconcile conflicts, resume pending child work, or execute
  a complete plan graph autonomously.
- Provider behavior still depends on the selected model, account, deployment,
  regional availability, and upstream API changes. Use the capability display,
  `collo doctor`, and live provider qualification before relying on a hosted
  endpoint.
- The project has extensive automated security tests but has not completed the
  independent security review required for 1.0.

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
