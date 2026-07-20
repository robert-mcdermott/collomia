# Live provider contract tests

Collomia's ordinary `go test ./...` suite runs deterministic, recorded protocol
contracts for every built-in adapter family. Those tests are safe for pull
requests and need no network access or credentials. They cover streaming text,
reasoning, tool-call fragments and completed arguments, usage, HTTP and
in-stream failures, retry behavior, truncated streams, and cancellation.

The separate live suite checks the same public client contract against real
provider endpoints. It is intended for maintainer qualification, endpoint
compatibility checks, and release preparation. It is never enabled by the
normal test command.

## What a live run does

For each configured endpoint, the test makes two billable model requests:

1. It asks the model to call a synthetic `echo_contract` tool with a fixed JSON
   argument and verifies normalized streaming tool deltas and token usage.
2. It supplies the synthetic tool result itself and verifies a streamed text
   response and token usage.

The harness does **not** execute a command, touch the workspace, or invoke any
returned tool. A strict run can require coverage of all four protocol families:
OpenAI Chat Completions, Anthropic Messages, Responses/Mantle, and native
Bedrock ConverseStream.

Live responses can vary and providers can charge for them. Run this suite
manually with test accounts or appropriately budgeted development credentials;
do not enable it for untrusted pull-request workflows.

## Manifest

Create a JSON manifest outside the repository, for example
`~/.collomia/live-provider-contracts.json`. It uses the same provider fields as
Collomia configuration, with two safety restrictions:

- `api_key` is rejected; use `api_key_env` so the manifest contains only an
  environment-variable name.
- Credential-bearing custom headers must expand an environment variable, such
  as `"Authorization": "Bearer ${MY_TOKEN}"`.

This complete qualification example shows one endpoint from each family.
Replace model IDs, regions, and endpoints with ones available to your accounts:

```json
{
  "required_families": ["openai", "anthropic", "responses", "bedrock"],
  "timeout_seconds": 120,
  "providers": {
    "openai-live": {
      "type": "openai",
      "base_url": "https://api.openai.com/v1",
      "api_key_env": "OPENAI_API_KEY",
      "model": "your-openai-model"
    },
    "anthropic-live": {
      "type": "anthropic",
      "base_url": "https://api.anthropic.com",
      "api_key_env": "ANTHROPIC_API_KEY",
      "model": "your-anthropic-model"
    },
    "mantle-live": {
      "type": "bedrock-mantle",
      "base_url": "https://bedrock-mantle.us-west-2.api.aws/v1",
      "api_key_env": "AWS_BEARER_TOKEN_BEDROCK",
      "model": "your-mantle-model"
    },
    "bedrock-live": {
      "type": "bedrock",
      "auth": "sigv4",
      "region": "us-west-2",
      "profile": "development",
      "model": "your-bedrock-model"
    }
  }
}
```

Native Bedrock uses the same authentication behavior as the application:
`sigv4` uses the AWS SDK credential chain, `bearer` uses `api_key_env` (or the
standard `AWS_BEARER_TOKEN_BEDROCK` variable), and `auto` selects between them.
Temporary access/secret/session credentials and SDK-managed profile or SSO
refresh therefore work without being copied into the manifest.

For a smaller development run, configure only the endpoints you have and omit
`required_families`. All provider types are accepted and mapped to their
underlying family:

- OpenAI: `openai`, `openai-compatible`, `azure-openai`, `azure-foundry`
- Anthropic: `anthropic`, `anthropic-compatible`,
  `azure-foundry-anthropic`
- Responses: `bedrock-mantle`
- Bedrock: `bedrock`

## Run it

Export the credential variables named by the manifest, then use both opt-ins:

```sh
COLLO_LIVE_PROVIDER_TESTS=1 \
COLLO_LIVE_PROVIDER_CONFIG="$HOME/.collomia/live-provider-contracts.json" \
go test -v -count=1 -run '^TestLiveProviderContracts$' ./internal/provider
```

The file is strict JSON (not JSONC): unknown fields, multiple JSON values,
missing models, missing referenced environment variables, embedded URL
credentials, and incomplete `required_families` coverage fail before the first
network request. Provider names are sorted so repeated runs are predictable.

Known environment credential values are removed from reported test errors as a
final safeguard. This does not make logs a safe place for secrets: keep the
manifest secret-free, avoid shell tracing (`set -x`), and review CI log access
before running against a hosted service.
