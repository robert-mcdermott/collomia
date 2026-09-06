# Documentation guide

These documents describe the current development checkout. A release binary
may predate unreleased changes; consult its version and the corresponding tag.
The [capability matrix](CAPABILITIES.md) is generated from the CLI. The
[improvement plan](IMPROVEMENT_PLAN.md) owns current implementation and user
acceptance; historical test builds are not installation instructions.

## Everyday use

| Document | Purpose |
| --- | --- |
| [Installing](INSTALLING.md) | Installation, upgrades, checksums, and local builds. |
| [User guide](USER_GUIDE.md) | CLI, TUI, configuration, providers, tools, and workflows. |
| [Work mode](WORK_MODE.md) | General-purpose tasks in ordinary folders; no template required. |
| [Completion](COMPLETION.md) | Choosing meaningful checks, current evidence, and automatic recovery. |
| [Task context](TASK_CONTEXT.md) | Retained notes and retrieval across compaction and restart. |
| [Recovery](RECOVERY.md) | Continue sessions, inspect uncertainty, and restore checkpoints. |
| [Features](FEATURES.md) | High-level feature and security overview. |
| [Capabilities](CAPABILITIES.md) | Generated implemented, experimental, and unsupported status. |
| [Beta status](BETA.md) | Appropriate use and remaining limitations. |

## Integration and operations

| Document | Purpose |
| --- | --- |
| [Automation](AUTOMATION.md) | Headless commands, JSONL evidence, and process outcomes. |
| [Compatibility](COMPATIBILITY.md) | Configuration, event, session, and migration contracts. |
| [Security](SECURITY.md) | Permissions, containment, trust boundaries, and their limits. |
| [Linux sandbox](LINUX_SANDBOX.md) | Native Linux qualification and troubleshooting. |
| [MCP protocol](MCP_PROTOCOL.md) | Supported protocol surface and interoperability limits. |
| [Live provider contracts](LIVE_PROVIDER_CONTRACTS.md) | Opt-in live adapter qualification. |
| [Quality evaluations](QUALITY_EVALUATIONS.md) | `collo eval`, task grading, reports, and interpretation. |
| [Testing](TESTING.md) | Offline regressions and platform/release qualification. |
| [Releasing](RELEASING.md) | Maintainer release process and supply-chain checks. |

## Product decisions and historical evidence

| Document | Status and purpose |
| --- | --- |
| [Improvement plan](IMPROVEMENT_PLAN.md) | Current September work, pending manual gate, and later priorities; completed sections retain dated evidence. |
| [Orchestration strategy](ORCHESTRATION_STRATEGY.md) | Authoritative current graph contract and milestone status, followed by dated implementation decisions. |
| [Roadmap history](ROADMAP_HISTORY.md) | Historical decisions and validation at the time they shipped; later entries may supersede earlier behavior. |
| [September 4 review](PROJECT_REVIEW_2026-09-04.md) | Historical assessment of `f523696`; findings and proposals are not current capability claims. |

The [main roadmap](../ROADMAP.md) owns product priorities. Historical wave names,
old binaries, retired workflow descriptions, and earlier limits are retained
only where they explain a dated decision or test. Current use follows the guides
above. `collo-screenshot.png` remains the illustrative TUI image used by the root
README; it is not a specification of current keyboard bindings or capabilities.

## September 6 documentation audit

Reviewed the documentation inventory for completion contracts, retired Work
workflow instructions, old version labels, cross-links, and current versus
historical status. Updated the affected guides, generated capabilities, event
contract, beta limits, and roadmap handoffs together. Installation, release,
Linux, provider, and MCP guides retain their distinct operational purpose;
their platform/live qualification instructions remain necessary and are not
claims that those external checks ran during this change.

The automated documentation checks cover links, documented commands/tools,
configuration fields, event kinds, and generated capability drift. The current
[manual acceptance checklist](IMPROVEMENT_PLAN.md#completion-simplification-checks)
is separate from those automated checks.
