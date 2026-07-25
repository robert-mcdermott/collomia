# Compatibility and migration policy

Collomia stores configuration, sessions, automation events, attachments, and
support metadata on the user's machine. This document defines which formats
are stable, how version changes are handled, and what to do before upgrading
or downgrading.

Collomia is currently a technical beta. The project still treats persisted
user data and the documented automation interface as compatibility contracts:
an upgrade must either read an existing supported format or reject it clearly
without rewriting it.

## Versioned formats

| Format | Current version | Compatibility behavior |
| --- | --- | --- |
| User/project configuration | `schema_version: 1` | A missing version is legacy version 1. Normal loading tolerates unknown fields; strict validation rejects them. A newer version is rejected before activation. |
| Headless runtime events | `schema: 1` | Optional additive fields are allowed. Unknown event kinds, incompatible required fields, and other schema versions are rejected by `collo replay`. |
| Durable session records | `schema_version: 1` | New records carry the version on every JSONL line. Legacy records without it are version 1. Unknown optional fields are ignored; a newer version is rejected without appending to the session. |
| Referenced tool-result artifacts | `schema_version: 1` | The stored object must match the supported version, ID, size, and quota checks before it is returned. |
| Support-bundle manifest | Versioned in the manifest | Intended for diagnostics, not restoration. Readers should tolerate additive fields and reject unsupported incompatible versions. |

Image attachment blobs do not contain an executable schema. Their session
references carry type, size, and SHA-256 metadata, all of which are rechecked
before provider delivery.

Trust records, MCP pins, audit ledgers, debug logs, and generated configuration
references are internal operational data. They are not public extension APIs.
When an internal cache or pin can be safely reconstructed, a future release
may require the user to review or recreate it instead of attempting an unsafe
migration.

## Additive and breaking changes

These changes normally remain within the current version:

- adding an optional JSON field;
- adding an optional configuration setting with a safe default;
- adding an enum value that old consumers are explicitly documented to ignore;
- adding human-readable diagnostic detail without changing the stable status
  or exit code.

These changes require a schema-version decision and normally a version bump:

- removing or renaming a field;
- changing a field's type, unit, required status, or meaning;
- changing the ordering or lifecycle guarantees of automation events;
- adding an event or record kind that older readers cannot safely ignore;
- changing how a persisted permission, mutation, or delegated task would be
  interpreted.

The Go structure alone does not decide compatibility. The observable meaning
and safety behavior do.

## Configuration upgrades

Validate before and after upgrading:

```sh
collo config validate --strict
collo config show
```

Collomia does not silently rewrite configuration files. Version 1 currently
needs no migration command. When a future configuration version requires a
change, the release notes and migration documentation must specify:

1. the oldest directly supported source version;
2. whether migration is automatic, previewable, or explicit;
3. the exact backup created before a write;
4. any setting whose default or security behavior changes;
5. how to validate and, where possible, roll back.

### Sandbox default transition

The current runtime default for an omitted `permissions.sandbox` field is
`auto`, replacing the earlier `off` default. This is an intentional security
behavior change within schema version 1:

- an existing *global* file that explicitly says `"sandbox": "off"` remains off;
- a *project* file that says `"sandbox": "off"` is refused and reported (see
  the containment-precedence change below), keeping the inherited mode;
- an existing file that omitted the field begins using `auto` after upgrade;
- new global starter files write `"sandbox": "auto"`;
- project starter files continue to omit the field and inherit the user or
  built-in setting;
- Collomia does not edit an existing file during the transition.

The compatibility-first companion defaults remain
`sandbox_allow_network: true` and
`sandbox_allow_read_outside_workspace: true`. Sandboxed commands use the
minimal environment when `command_env` is omitted. Before or after upgrading,
run `collo config show` and `collo doctor`; add narrow readable/writable roots
for SDKs and caches where needed. Add an explicit `"sandbox": "off"` only when
the prior unsandboxed behavior is deliberately required.

### Containment-precedence change

A repository can now tighten any containment setting but never weaken one.
This is an intentional security behavior change within schema version 1, and
it can change the effective policy of an unmodified project file:

- affected settings are `sandbox`, `sandbox_allow_network`,
  `sandbox_allow_read_outside_workspace`, `command_env`, `network`,
  `commands`, and `allow_outside_workspace`;
- it applies identically to an explicit field and to `permissions.preset`;
- a project file that weakens one of these is refused, not applied, and the
  refusal is printed by `collo config show` and `collo config validate`;
- your global configuration is unaffected — it may still select
  `"sandbox": "off"` or `"preset": "frictionless"`;
- no file is rewritten.

A project that relied on `.collomia.json` setting `"sandbox": "off"` (or
`"command_env": "full"`) must move that setting to the global configuration,
or select `"preset": "frictionless"` there. Run `collo config show` after
upgrading to see whether anything was refused.

A newer configuration is rejected with an instruction to upgrade the binary.
Normal loading's unknown-field tolerance supports forward-compatible optional
settings; use strict validation in CI and after manual edits to catch typos.

## Sessions and upgrades

Durable session JSONL is append-only. Current releases:

- load legacy version-1 records that predate the explicit `schema_version`;
- tolerate unknown optional fields;
- discard only a malformed final line, treating it as a possible crash-torn
  write;
- reject an unsupported record version before opening the session for append;
- never execute a stored tool call during load, replay, fork, or rewind.

Before a major upgrade, back up `~/.collomia/` on macOS/Linux or
`%USERPROFILE%\.collomia\` on Windows if the sessions are important. Do not
edit live session JSONL by hand.

Downgrading is not guaranteed. An older binary may not understand data created
by a newer release even when the newer release can still read the older data.
Use the previous binary with a backup made before the upgrade rather than
pointing it at state already updated by a newer version.

## Automation consumers

Use `collo schema events` to retrieve the JSON Schema embedded in the exact
binary being run. Consumers should:

- select behavior using `schema`, `kind`, and `run.result.status`;
- tolerate unknown optional fields;
- avoid inferring success from streamed text or an intermediate error;
- require the final `run.result` for a complete run;
- fail clearly on an unsupported schema or event kind.

Adding a new event kind is not treated like adding an optional field: existing
strict replay clients reject unknown kinds, so the change requires an explicit
compatibility decision.

## Release and developer checklist

Any change to a persisted or machine-readable structure should include:

1. a compatibility classification: additive, breaking, or internal-only;
2. fixtures proving the oldest supported input still loads;
3. a rejection test for unsupported future versions;
4. an updated embedded schema or format constant when applicable;
5. migration and rollback documentation before release;
6. roadmap, capability, automation, and user-guide updates where behavior is
   user-visible.

Compatibility tests are credential-free and run on macOS, Linux, and Windows.
The release workflow repeats them against the exact tagged source.
