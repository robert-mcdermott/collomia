# Automation and JSONL contract

Collomia's headless interface is designed for CI jobs, editor integrations,
shell scripts, and other programs that need a stable result rather than a
terminal UI:

```sh
collo run --jsonl --plan "Inspect the repository and summarize its architecture"
```

This document describes schema version 1. Print the exact schema embedded in
the installed binary at any time:

```sh
collo schema events
collo schema events > collomia-events-v1.schema.json
```

The same schema is published in the repository at
`internal/event/schema/events-v1.schema.json`. The binary and its schema
therefore cannot drift when Collomia is distributed as a single executable.

Headless runs use the same command policy as the TUI. The OS command sandbox
defaults to capability-aware `auto`, command networking and broad macOS/Linux
reads remain enabled, and sandboxed commands receive the minimal environment
unless configuration explicitly selects `command_env: "full"`. CI builds may
need narrow `sandbox_readable_roots` or `sandbox_writable_roots` entries for
dependency stores and caches. Use `sandbox: "require"` when an unavailable
backend must fail the job rather than emit a degradation warning, or an
explicit `off` only when the job deliberately requires unsandboxed behavior.

## Stream guarantees

With `--jsonl`:

- stdout contains JSONL events only: one complete JSON object per line;
- progress and human-readable diagnostics go to stderr;
- every line has `schema`, UTC `time`, and `kind` fields;
- secrets known from configuration and common credential shapes are redacted
  before a line is written;
- for an actual run whose arguments parsed successfully, exactly one
  `run.result` is written last for success, startup/configuration failure,
  provider failure, timeout, permission failure, or cancellation.

`--help` and `--version` are informational requests rather than runs, so they
print their normal text and do not emit a terminal verdict.

An uncatchable process termination (`SIGKILL`, power loss, runtime crash, or an
unwritable/broken stdout) cannot produce a final record. Invalid command-line
syntax can fail before JSONL mode itself is established; use the process exit
code and stderr for that case.

The event `schema` number changes only for a breaking wire-format change.
Collomia may add optional fields to schema v1. Consumers should validate known
fields and tolerate new optional fields as described by their chosen JSON
decoder. For that reason, the published schema permits additional properties
while still validating all known fields and kind-specific required payloads.
New event kinds require an explicit compatibility decision because existing
strict replay consumers reject unknown kinds. The repository's
[compatibility and migration policy](COMPATIBILITY.md) defines the complete
versioning contract.

## Event kinds

Current one-shot runs emit these events when applicable:

| Kind | Payload | Meaning |
|---|---|---|
| `turn.start`, `turn.end` | — | Agent-turn boundaries. |
| `text.delta` | `text` | Incremental visible answer text. |
| `reasoning.delta` | `text` | Provider-exposed reasoning text, when supported. |
| `tool.call.delta` | `tool_call` | Incomplete provider tool-call fragments; never execute these directly. |
| `tool.start` | `tool` | A validated, permitted tool started. |
| `tool.output` | `tool` | Live bounded tool output. |
| `tool.result` | `tool` | Completed tool result and error flag. |
| `permission.decision` | `permission` | Allow/deny decision, source, matched rule, and resources. |
| `usage` | `usage` | Provider-reported input/output/cached/cache-write/reasoning tokens plus optional user-priced `cost_usd`, `cost_available`, and `cost_estimated`. `input_tokens` counts the whole prompt including cached tokens; see [final result](#final-result). |
| `context.compaction` | `text` | Context was compacted. |
| `warning` | `text` | Non-fatal runtime/provider warning. |
| `error` | `error`, optional `provider`, optional `failure_id` | A failure observed during the run. |
| `run.result` | `result`, optional `usage`, optional `failure_id` | Exactly one terminal verdict. |

Schema v1 also reserves `session.start`, `permission.request`, `file.change`,
`plan.update`, and `delegate.update` for consumers sharing the runtime event
model. `delegate.update` carries the latest bounded child status used by
durable interactive sessions; one-shot JSONL runs do not currently promise it.
Do not assume every reserved kind appears in every current CLI stream.

When present, its `delegate` object includes stable identity/profile/provider,
`plan_step`, lifecycle state/current action, bounded `recent_output`, steering
history plus `pending_guidance`, declared `write_scopes`, any
`scope_violations`, evidence and changed/integrated file lists, optional
`integration_status`/`integration_error`, usage plus token/cost budgets, and retained
worktree/branch/base metadata. A writer without explicit paths records `["*"]`.
Scope violations make the task an error and block guarded integration; the
worktree remains available. Integration status describes a user/primary
reviewed-copy disposition (`reviewed`, `reviewed_with_conflicts`, `integrated`,
`partial`, `blocked`, or `rejected`), not a Git branch merge. A reviewed file
may be a freshness-bound clean three-way composition; overlapping diff3
content is observational and never published automatically.

Delegated write records may also include `verification_status`,
`verification_error`, `verification_token`, `verification_required`, and
bounded `verification_results`. Each result carries the detected purpose,
exact command, observed status, bounded output/error, state token, and
timestamps. Aggregate status can be `running`, `partial`, `passed`, `failed`,
`cancelled`, `timed_out`, `blocked`, `rejected`, `stale`, or `unavailable`.
This is child-worktree evidence only; it grants no permission and says nothing
about the combined parent workspace.

These fields are observations only: replay never restarts a child, runs a
stored verification command, delivers stored guidance, or integrates stored
changes. Consumers must tolerate additive fields.

Failed, cancelled, timed-out, budget-exhausted, or recovery-interrupted child
records may also carry `delegate.failure_id`. It is the same opaque ID shown in
that child's interactive diagnostic; it is not a task ID and grants no access.

Costs are local estimates, not provider invoices. They appear only when the
selected provider has explicit `pricing`; Collomia ships no price catalog.
Headless runs using a named primary profile can enforce its durable
`cost_budget_usd` with `--agent <name>`. A missing/invalid price configuration
is a configuration failure; exhaustion is reported as failure kind `usage`.

`tool.call.delta.arguments_delta` can be incomplete JSON until `done` is true.
It is an observation stream, not an execution request. Collomia itself waits
for the provider's complete call, validates it, applies policy, and only then
emits tool lifecycle events.

## Final result

A successful terminal record looks like this (timestamps omitted here only for
readability):

```json
{
  "schema": 1,
  "kind": "run.result",
  "result": {
    "status": "ok",
    "answer": "...",
    "session_id": "20260721-120000-a1b2c3",
    "changed_files": ["main.go"],
    "duration_ms": 8412
  },
  "usage": {
    "input_tokens": 5210,
    "cached_tokens": 3410,
    "cache_write_tokens": 0,
    "output_tokens": 644,
    "cost_usd": 0.0121,
    "cost_estimated": true
  }
}
```

`result` fields:

| Field | Meaning |
|---|---|
| `status` | Stable schema-v1 status: `ok`, `error`, or `cancelled`. |
| `answer` | Final answer, or provider-streamed partial text when a failed stream produced some. |
| `error` | Human-readable failure message. Do not parse it for control flow. |
| `failure` | Stable structured failure classification for non-`ok` results. |
| `partial` | The failed/cancelled run emitted meaningful text/tool activity or changed files before stopping. |
| `refused` | At least one requested action was denied. This may coexist with `status: ok` if the agent recovered and explained the refusal. |
| `ephemeral` | The run deliberately skipped durable conversation/session persistence. |
| `session_id` | Durable session ID. Omitted for ephemeral or pre-session startup failures. |
| `changed_files` | Workspace files changed through Collomia's tracked write tools. |
| `duration_ms` | Wall-clock duration measured by the headless runner. |

`usage` fields:

| Field | Meaning |
|---|---|
| `input_tokens` | The whole prompt: uncached tokens plus cache reads plus cache writes. |
| `cached_tokens` | Portion of `input_tokens` served from the provider's prompt cache. |
| `cache_write_tokens` | Portion of `input_tokens` written to the cache by this request. |
| `output_tokens` | Tokens generated. |
| `reasoning_tokens` | Reasoning tokens, where the provider reports them separately. |
| `cost_usd` | User-priced estimate covering all four rates. Present only when `cost_available` is true. |
| `cost_available` | Configured pricing was sufficient to produce an estimate. |
| `cost_estimated` | The figure is Collomia's own arithmetic over configured prices, never a provider bill. |

**`input_tokens` changed meaning in v0.2.0 on the Anthropic routes.** It now
includes the cached portion, where it previously excluded it, because the
Anthropic Messages API reports that field net of both cache counters and
Collomia now requests caching. A consumer comparing the field across the
upgrade will see an apparent increase that is a correction rather than new
consumption. Cache reads bill below ordinary input and writes above it, so
recompute spend from `cost_usd` rather than from a token count and a single
rate. `cache_write_tokens` is new and optional; readers tolerating unknown
fields need no change. See the [compatibility
note](COMPATIBILITY.md#reported-prompt-token-counts).

For a failed or cancelled run, the event-level `failure_id` and
`result.failure.id` contain the same value, for example
`err-0123456789abcdef`. The value is random per failure occurrence and contains
no error text, path, session, provider, prompt, or credential material. Treat
its format as stable but its value as opaque: use it only to correlate the
terminal verdict with a preceding `error` event, debug log, TUI report, or
reviewed support bundle. A repeated root cause receives a new ID.

Failure kinds are stable and provider-neutral:

| `failure.kind` | Meaning |
|---|---|
| `usage` | Missing prompt or invalid run invocation. |
| `configuration` | Validated configuration is unusable. |
| `permission` | A permission failure terminated the run. Non-fatal denied tool requests use `refused`. |
| `provider` | Provider failure; `failure.provider` contains its normalized kind/status/retry metadata. |
| `timeout` | Deadline, request timeout, or provider timeout. |
| `cancelled` | User/signal cancellation. The top-level status is also `cancelled`. |
| `runtime` | Other local runtime failure. |

Provider detail can classify `authentication`, `permission`, `rate_limit`,
`invalid_request`, `not_found`, `timeout`, `unavailable`, `protocol`,
`cancelled`, or `unknown`, and can include HTTP status, retryability,
`retry_after_ms`, operation, and request ID.

## Exit codes

| Code | Meaning |
|---:|---|
| `0` | Run completed. Inspect `refused` if denied actions matter to the caller. |
| `1` | Runtime, provider, permission, timeout, or other execution failure. |
| `2` | Command usage or validated configuration failure. |
| `130` | Cancelled, normally by Ctrl+C/SIGINT. |

The JSONL verdict is authoritative for run details; the exit code is the compact
shell-level outcome. With a pipeline, enable `pipefail` or the shell normally
reports only the last program's status:

```sh
set -o pipefail
collo run --jsonl --autopilot "Run tests" |
  tee run.jsonl |
  jq -c 'select(.kind == "run.result")'
```

In PowerShell, preserve `$LASTEXITCODE` immediately after Collomia if another
command will run before it is checked:

```powershell
collo run --jsonl --plan "Summarize this repository" | Tee-Object run.jsonl
$ColloExit = $LASTEXITCODE
$Result = Get-Content run.jsonl | Select-Object -Last 1 | ConvertFrom-Json
if ($ColloExit -ne 0) { exit $ColloExit }
```

## Ephemeral runs

Use `--ephemeral` when a CI or one-shot task should not appear in session
history:

```sh
collo run --jsonl --ephemeral --plan "Check whether this change needs docs"
```

Ephemeral means only that Collomia does not create, resume, or append a durable
conversation session under `~/.collomia/sessions` (or
`%USERPROFILE%\.collomia\sessions` on Windows). Consequently the final result
has `ephemeral: true` and no `session_id`.

It does **not** make the run read-only or untracked:

- workspace mutations still happen when policy allows them;
- permission decisions and execution outcomes still go to the audit ledger;
- `--debug` still writes its explicitly requested redacted diagnostic log;
- provider, MCP, hook, and command behavior is otherwise unchanged.

`--ephemeral` is available only on `collo run` and cannot be combined with
`--resume` or `--continue`. Use `--plan` as well when you want a read-only tool
surface.

## Validating and replaying saved traces

Use the offline replay command to verify that a retained JSONL stream is a
complete, internally consistent Collomia run:

```sh
collo replay --check run.jsonl
```

A valid check prints a compact summary and exits 0. A malformed line,
unsupported schema or event kind, missing required payload, impossible
turn/tool ordering, inconsistent final result, event after `run.result`, or
missing final result exits 1 with the source line number. Missing or extra
command arguments exit 2.

Without `--check`, the same validation runs first and then Collomia renders a
readable transcript:

```sh
collo replay run.jsonl
cat run.jsonl | collo replay -
```

Replay is intentionally side-effect-free. It does not load user or project
configuration, establish repository trust, create or resume a session, contact
a provider or MCP server, or execute a recorded tool. The human renderer strips
terminal control characters, forces identifiers onto one line, visibly frames
untrusted payload text, normalizes CRLF output, limits an individual rendered
payload to 64 Ki characters, and applies the common credential-shape redactor
again. The source file is never rewritten.

JSONL produced by `collo run --jsonl` was already scrubbed with both configured
secrets and common credential patterns before being written. Because offline
replay deliberately does not load configuration, its second pass cannot know
arbitrary custom secrets. Review traces before sharing them and treat redaction
as defense in depth rather than a proof that a file contains no sensitive
data.

Replay accepts additive fields within schema v1, matching the consumer
compatibility rule. A binary rejects newer schema numbers or unknown event
kinds it cannot interpret; use a matching or newer Collomia binary for those
traces. Failed and cancelled traces may end with an interrupted turn or tool,
while a successful result requires clean `turn.end` and tool completion.

`collo replay` consumes completed headless run streams. Durable session JSONL
and permission audit ledgers have different record envelopes and are not valid
replay inputs. Replay verifies recorded structure; it does not prove that an
upstream provider response was factually correct or that an external action
actually had the claimed effect.

## Complete automation examples

The examples below use `jq` to inspect the terminal `run.result`. They are
read-only by default: `--plan` removes mutating tools, while `--ephemeral`
keeps the run out of durable conversation history. Each example either creates
or explicitly identifies the required user-level provider configuration.

### GitHub Actions: read-only pull-request review

Add `scripts/collomia-ci.sh` to the repository:

```bash
#!/usr/bin/env bash
set -u

workspace="${GITHUB_WORKSPACE:-$(pwd)}"
base_ref="origin/${GITHUB_BASE_REF:-main}"
run_log="${RUNNER_TEMP:-/tmp}/collomia-run.jsonl"
error_log="${RUNNER_TEMP:-/tmp}/collomia-run.stderr"

if collo run \
    --cwd "$workspace" \
    --provider openrouter \
    --jsonl \
    --ephemeral \
    --plan \
    "Review the changes relative to $base_ref for correctness, security problems, missing tests, and stale documentation. Return a concise CI verdict." \
    >"$run_log" 2>"$error_log"
then
    collo_exit=0
else
    collo_exit=$?
fi

result="$(tail -n 1 "$run_log" 2>/dev/null || true)"
if ! jq -e '.kind == "run.result"' >/dev/null 2>&1 <<<"$result"; then
    echo "Collomia did not produce a final result" >&2
    cat "$error_log" >&2
    exit 1
fi

echo "### Collomia review"
jq -r '.result.answer // "(no answer)"' <<<"$result"

status="$(jq -r '.result.status' <<<"$result")"
refused="$(jq -r '.result.refused // false' <<<"$result")"
partial="$(jq -r '.result.partial // false' <<<"$result")"

if (( collo_exit != 0 )); then
    echo "Collomia failed:" >&2
    jq '.result | {status, failure, error, partial, refused}' <<<"$result" >&2
    cat "$error_log" >&2
    exit "$collo_exit"
fi

if [[ "$status" != "ok" || "$refused" == "true" || "$partial" == "true" ]]; then
    echo "Collomia did not complete the entire requested review" >&2
    jq '.result | {status, failure, error, partial, refused}' <<<"$result" >&2
    exit 1
fi
```

Make it executable, then add `.github/workflows/collomia-review.yml`:

```yaml
name: Collomia review

on:
  pull_request:

jobs:
  review:
    runs-on: ubuntu-latest
    # Fork pull requests do not receive repository secrets. Skip them instead
    # of turning a missing provider credential into a confusing CI failure.
    if: github.event.pull_request.head.repo.full_name == github.repository
    permissions:
      contents: read

    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Install Collomia
        run: |
          curl --proto '=https' --tlsv1.2 -fsSL \
            https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"

      - name: Configure the OpenRouter provider
        run: |
          mkdir -p "$HOME/.collomia"
          cat > "$HOME/.collomia/config.json" <<'JSON'
          {
            "schema_version": 1,
            "default_provider": "openrouter",
            "providers": {
              "openrouter": {
                "type": "openai",
                "base_url": "https://openrouter.ai/api/v1",
                "api_key_env": "OR_API_KEY",
                "model": "z-ai/glm-5.2",
                "max_tokens": 128000,
                "context_window": 500000
              }
            }
          }
          JSON

      - name: Run Collomia review
        env:
          OR_API_KEY: ${{ secrets.OR_API_KEY }}
        run: bash scripts/collomia-ci.sh
```

This example uses a provider named `openrouter` and `OR_API_KEY`; the workflow
creates that non-secret provider definition on the ephemeral runner. Change the
definition and secret name to match the selected provider. GitHub-hosted Ubuntu
runners include `jq`; a self-hosted runner must provide it separately. The
workflow grants only read access to repository contents and does not post a
review or comment back to GitHub.

Never expose a long-lived provider credential to untrusted pull-request code;
use an approved trusted environment or a short-lived, narrowly scoped gateway
credential if third-party changes must be analyzed.

To let an automated job edit its isolated checkout, replace `--plan` with an
explicitly chosen writable autonomy mode such as `--autopilot`. Do that only
with narrow permission rules, a disposable checkout, retained JSONL/audit
artifacts, and a later human review of `git diff`. `--ephemeral` does not make
workspace mutations temporary.

### Cron: scheduled repository maintenance report

On a Linux or macOS host, create a private script directory and then create
`$HOME/bin/collomia-weekly-report.sh`:

```sh
mkdir -p "$HOME/bin"
chmod 700 "$HOME/bin"
```

The scheduled account must already have a working user-level provider in
`$HOME/.collomia/config.json`; configure and test it interactively before
installing the cron entry.

```bash
#!/usr/bin/env bash
set -u

# Cron starts with a minimal environment. Use explicit, real paths for this
# account instead of relying on an interactive shell profile.
export HOME="/home/alice"
export PATH="$HOME/.local/bin:/usr/local/bin:/usr/bin:/bin"

workspace="/srv/my-project"
report_dir="$HOME/.collomia/reports"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
jsonl="$report_dir/weekly-$timestamp.jsonl"
errors="$report_dir/weekly-$timestamp.stderr"
report="$report_dir/weekly-$timestamp.md"

mkdir -p "$report_dir"
cd "$workspace" || exit 1

# This file must be readable only by this account.
if [[ ! -r "$HOME/.collomia/cron.env" ]]; then
    echo "Missing $HOME/.collomia/cron.env" >&2
    exit 1
fi
source "$HOME/.collomia/cron.env"

if collo run \
    --cwd "$workspace" \
    --provider openrouter \
    --jsonl \
    --ephemeral \
    --plan \
    "Inspect the repository. Summarize recent Git changes, failing or missing tests, TODOs, dependency concerns, and the three highest-priority maintenance tasks. Do not modify files." \
    >"$jsonl" 2>"$errors"
then
    collo_exit=0
else
    collo_exit=$?
fi

result="$(tail -n 1 "$jsonl" 2>/dev/null || true)"
if ! jq -e '.kind == "run.result"' >/dev/null 2>&1 <<<"$result"; then
    {
        echo "# Weekly Collomia report"
        echo
        echo "Collomia produced no final result."
        echo
        cat "$errors"
    } >"$report"
    exit 1
fi

{
    echo "# Weekly Collomia report"
    echo
    echo "Generated: $(date -u)"
    echo
    jq -r '.result.answer // "(no answer produced)"' <<<"$result"
    echo
    echo "## Execution metadata"
    echo
    jq '.result | {
        status,
        refused: (.refused // false),
        partial: (.partial // false),
        failure
    }' <<<"$result"
} >"$report"

exit "$collo_exit"
```

Replace `/home/alice` and `/srv/my-project` with absolute paths for the account
running the job. On macOS, a typical home is `/Users/alice`. Create the
credential environment file without checking it into a repository:

```sh
mkdir -p "$HOME/.collomia"
touch "$HOME/.collomia/cron.env"
chmod 600 "$HOME/.collomia/cron.env"
${EDITOR:-vi} "$HOME/.collomia/cron.env"
```

For the OpenRouter example, its content is:

```sh
export OR_API_KEY='replace-with-the-real-key'
```

Make the report script executable and schedule it for 07:00 every Monday:

```sh
chmod 700 "$HOME/bin/collomia-weekly-report.sh"
crontab -e
```

```cron
0 7 * * 1 /home/alice/bin/collomia-weekly-report.sh
```

Cron uses the host's local timezone. The script preserves the complete JSONL
stream and stderr diagnostics beside the Markdown report, making failures
inspectable. Ensure overlapping runs are impossible for the expected task
duration, or add the host's normal job-locking mechanism.

## Resume and retention

Without `--ephemeral`, every run creates a durable session. Continue it later:

```sh
collo run --jsonl "Inspect the failing tests" > first.jsonl
SESSION_ID=$(tail -n 1 first.jsonl | jq -r '.result.session_id')
collo run --jsonl --resume "$SESSION_ID" "Implement the fix"
```

`--continue` selects the most recently updated non-archived session. Use an
explicit ID in unattended automation to avoid ambiguity.

## Recommended consumer pattern

1. Start `collo run --jsonl` and parse stdout line by line; do not wait for the
   whole stream if live progress matters.
2. Select behavior by `kind`, not by which optional payload happens to exist.
3. Treat tool-call deltas as display data only.
4. Retain the last `run.result` and the process exit code.
5. Fail the surrounding job on non-`ok`, on `partial: true` if partial work is
   unacceptable, or on `refused: true` if every requested action is mandatory.
6. Show stderr and provider request IDs to operators, but never depend on
   human-readable error wording.
7. Pin a Collomia release and validate its embedded schema during integration
   testing.
