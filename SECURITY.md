# Security policy

Collomia executes model-proposed tools and commands, so suspected sandbox
escapes, permission bypasses, secret exposure, unsafe path handling, duplicate
mutations, and release-supply-chain issues should be reported privately.

## Reporting a vulnerability

Use **Report a vulnerability** on the repository's GitHub **Security** tab to
open a private vulnerability report. If private reporting is unavailable,
contact the maintainer through the
[GitHub profile](https://github.com/robert-mcdermott) and request a private
channel. Do not include exploit details, credentials, private source, or user
data in a public issue.

Include, when available:

- the Collomia version and commit from `collo --version`;
- operating system, architecture, and sandbox mode;
- a minimal reproduction and expected security boundary;
- whether untrusted content, an MCP server, hook, skill, or provider was
  involved;
- an opaque `err-…` failure identifier;
- impact and any known workaround.

Please allow a reasonable period for acknowledgement, reproduction, a fix, and
coordinated disclosure. The maintainer will credit reporters who want credit
and will not require public disclosure before affected users have a practical
upgrade path.

## Supported versions

Before 1.0, security fixes target the newest published beta or stable release
and the current `main` branch. Older prereleases may be asked to upgrade before
triage continues. Release notes will identify fixes that require urgent
upgrades.

## Security documentation

The complete trust, permission, command-analysis, sandbox, MCP provenance,
session, logging, and browser-terminal model is in
[docs/SECURITY.md](docs/SECURITY.md). Beta limitations are listed in
[docs/BETA.md](docs/BETA.md). A checksum verifies consistency with a release
manifest; GitHub/Sigstore artifact attestations additionally verify the
repository and workflow that produced a release file.
