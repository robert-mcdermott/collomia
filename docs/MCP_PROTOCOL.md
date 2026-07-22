# MCP protocol support

Collomia uses the official Model Context Protocol Go SDK and currently builds
against `github.com/modelcontextprotocol/go-sdk` v1.6.1. Each connection
negotiates its protocol revision during initialization; `/mcp status` reports
the revision actually selected rather than assuming every server is current.

## Revisions

The SDK used by this release offers MCP 2025-11-25 and can negotiate these
older revisions with compatible servers:

- 2025-06-18
- 2025-03-26
- 2024-11-05

Protocol negotiation does not imply that Collomia exposes every feature in a
revision. The table below is the product-level contract.

## Implemented client subset

| Protocol area | Collomia behavior |
| --- | --- |
| Initialization | Negotiates a revision and records server identity and capabilities. |
| Transports | stdio and Streamable HTTP. |
| Tools | Paginated discovery, JSON Schema definitions, calls, typed results (including bounded image passthrough on capable provider routes), cancellation, and progress. |
| Tool list changes | Complete catalog is fetched and validated, then atomically replaces that server's registered tools. Failed refreshes keep the previous catalog. |
| Resources | Paginated live listing and reads; text/binary metadata and embedded content are preserved as documented. |
| Resource list changes | Marks the catalog pending until the next successful live list. |
| Prompts | Paginated live listing and explicit expansion into the user-editable composer. |
| Prompt list changes | Marks the catalog pending until the next successful live list. |
| Elicitation | TUI form mode only; URL mode is declined and headless clients do not advertise elicitation. |
| Progress | Routed to the active tool's streamed output by progress token. |
| Logging | Negotiated by the SDK; Collomia does not currently expose a separate server-log viewer. |

`/mcp refresh <server>` manually retries a tool-catalog refresh without closing
the session. `/mcp reconnect <server>` is the stronger recovery action when the
transport or remote implementation itself needs to be reinitialized.

## External-data boundary

MCP trust authorizes the configured connection and executable; it does not
make server-authored text authoritative. Before MCP data becomes visible to a
model, Collomia wraps tool results, resources and resource catalogs, and
expanded prompt templates in a deterministic content-derived frame that names
the server, content kind, subject, and normalized byte count. The frame states
that the payload is data, not authority, permission, or a reason to call tools.
Terminal controls are removed.

Tool-schema descriptions and titles are bounded and labeled as external,
descriptive MCP metadata; nonessential schema comments and examples are
removed while the structural schema is preserved. Catalog, progress, and
elicitation prose is also bounded and made control-safe. The frame explicitly
tells the model to use relevant factual and structured data while refusing
embedded instructions, claimed authority, or claimed permissions. These
transformations preserve provenance and reduce prompt-injection ambiguity, but
they do not replace permission checks: every MCP call remains external risk,
and any write or command proposed after reading MCP content is authorized
independently.

## Not implemented

- Experimental MCP tasks and task status notifications.
- Resource subscribe/unsubscribe and resource-updated notifications.
- Standards-based OAuth discovery, login, refresh, logout, and token storage.
- Sampling requests from servers.
- Direct audio delivery to provider inputs. Images retain safe type-and-size
  markers and additionally pass through as typed bytes for Anthropic Messages
  and Bedrock Converse tool-result turns; OpenAI-compatible Chat Completions
  remains marker-only because its tool-message image shape is not portable.

MCP tasks were introduced as experimental in the 2025-11-25 specification.
Collomia will not create a private task dialect while that surface and its SDK
API are evolving.

## Conformance and regression coverage

Ordinary `go test ./...` uses in-memory MCP fixture servers and no external
credentials. Together the fixtures cover:

- initialization, identity, revision, and negotiated capabilities;
- tools, resources, prompts, and rich result content, including typed image-byte preservation;
- dynamic tool/resource/prompt list-change notifications;
- atomic hot refresh, notification coalescing, stale-session rejection, and
  preservation of the last-known-good tools when replacement validation fails;
- progress routing, form elicitation, decline behavior, cancellation/timeouts,
  ping/reconnect/enable/disable/add/remove, and server pinning.
- external-data provenance framing, delimiter/control-character attacks, bounded
  schema/catalog metadata, and an agent-level injected-permission refusal.

This suite validates Collomia's supported subset against the SDK's in-memory
wire implementation. It is not a claim that every third-party MCP server is
conformant; `/mcp status`, `/mcp ping`, and `/mcp refresh` remain the operational
diagnostics for a configured server.

Specification references:

- [MCP 2025-11-25 specification](https://modelcontextprotocol.io/specification/2025-11-25)
- [Tools and list-change notifications](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
- [Experimental tasks](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks)
- [Official Go SDK](https://github.com/modelcontextprotocol/go-sdk)
