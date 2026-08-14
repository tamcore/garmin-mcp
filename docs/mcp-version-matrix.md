# MCP version matrix

Scope: the pinned pair from `docs/adr/0002-mcp-sdk-and-spec-version.md`.

| Item | Pin |
|------|-----|
| SDK | `github.com/modelcontextprotocol/go-sdk` `v1.7.0` (released 2026-07-28) |
| SDK tagged commit | `bc72835f62eb94d0fb484439f886b6885b075f36` |
| MCP specification | `2026-07-28` |

Verified against the repository tree and the README at the `v1.7.0` tag on
2026-08-14. Every SDK symbol named below was read at that tag. Where a column
says "none", the SDK provides no importable equivalent and the behavior is local
code.

Status meanings:

- **required** — this project must implement it, and the named milestone gate
  covers it.
- **optional** — the specification and the SDK allow it; this project may use it,
  and doing so is not release-blocking.
- **deferred** — deliberately not built in v1. A deferred row is a decision, not
  an omission.

Milestones: **M1** is the local single-user stdio server, **M2** the remote
multi-user server, **M3** full Taxuspt parity. Definitions are in
`docs/phases.md`, checklists in `docs/implementation-status.md`.

**Every row in this file is target work.** Nothing MCP-facing exists in the
repository: there is no SDK requirement in `go.mod`, no transport, no tool, no
resource, no policy layer, no rate limiter, and no logger. Where a row names a
local package, that names the intended owner of the behavior, not a package that
is present today.

## Transports

| Feature | Status | Milestone | SDK package or type | Notes |
|---------|--------|-----------|---------------------|-------|
| stdio transport | required | M1 | `mcp.StdioTransport` | Stdout carries MCP frames only. Logs and status must go to stderr through the local structured logger, which does not exist yet and lands with this transport. |
| Streamable HTTP lifecycle | required | M2 | `mcp.StreamableHTTPHandler`, `mcp.NewStreamableHTTPHandler`, `mcp.StreamableHTTPOptions`, `mcp.StreamableServerTransport` | Authenticate every applicable `POST`, `GET`, and `DELETE`. The handler serves a stateless and a stateful path; see session behavior below. |
| Streamable HTTP request-body cap | required | M2 | `mcp.StreamableHTTPOptions.MaxRequestBodyBytes` | Set explicitly. Never rely on the default. |
| Origin and localhost protection | required | M2 | `mcp.StreamableHTTPOptions.CrossOriginProtection`, `.DisableLocalhostProtection` | Default CORS to deny. A Streamable HTTP request that carries `Origin` must match the configured allowlist; standards-compliant non-browser token requests may omit it. |
| Request cancellation propagation | required | M2 | `mcp.StreamableHTTPOptions.PropagateRequestCancellation` | Cancellation must reach the Garmin call, so a dropped client stops upstream work. |
| SSE-only legacy transport | deferred | — | `mcp.SSEHandler` | The 2026-07-28 transport is Streamable HTTP. No legacy SSE endpoint is exposed. |

## Authorization

| Feature | Status | Milestone | SDK package or type | Notes |
|---------|--------|-----------|---------------------|-------|
| Bearer-token resource server | required | M2 | `auth.RequireBearerToken`, `auth.TokenVerifier`, `auth.TokenInfo`, `auth.TokenInfoFromContext` | The principal comes only from the verified token context. Accept bearer tokens in the `Authorization` header only, never a query parameter or a cookie. |
| Protected Resource Metadata (RFC 9728) | required | M2 | `oauthex.ProtectedResourceMetadata`, `auth.ProtectedResourceMetadataHandler` | Served from the explicit canonical public URL, never derived from `Host` or `X-Forwarded-*`. |
| RFC 6750 `WWW-Authenticate` challenge | required | M2 | `auth.RequireBearerTokenOptions.ResourceMetadataURL` and `.Scopes`; `oauthex.Challenge`, `oauthex.ParseWWWAuthenticate` | Must distinguish a missing token from an invalid one. The SDK emits the challenge; the negative matrix asserts the distinction. |
| Authorization-server metadata (RFC 8414) | required | M2 | `oauthex.AuthServerMeta`, `oauthex.GetAuthServerMeta`, `auth.GetAuthServerMetadata` | The SDK types and fetchers are client-side. Serving the document in self-contained mode is local code over the ADR 0003 component. |
| Authorization-server role | required | M2 | none | The SDK ships no importable authorization server. Its only one is the internal test fixture `internal/oauthtest/fake_authorization_server.go`. The component choice belongs to ADR 0003. |
| Resource indicators (RFC 8707) | required | M2 | `oauthex.MatchesResource` | Exact audience validation on every token. Refresh must never change the resource. |
| Client registration — preregistration | required | M2 | none | Operator-configured clients with exact redirect URIs. This is the default, and the only mode enabled in production by default. No vendor client ID is hardcoded. |
| Client registration — RFC 7591 DCR | deferred | — | `oauthex.ClientRegistrationMetadata`, `oauthex.ClientRegistrationResponse`, `oauthex.RegisterClient`, `auth.DynamicClientRegistrationConfig` | The SDK support is client-side: it registers *with* a server. Serving a registration endpoint is local work. Unrestricted anonymous production DCR is prohibited. A constrained profile may be enabled for the conformance run only, with quotas, strict redirect schemes and hosts, metadata limits, rate limits, and audit events. |
| Client registration — Client ID Metadata Documents | deferred | — | `auth.ClientIDMetadataDocumentConfig` | Needs an explicit trust policy and an SSRF-safe fetcher. Not built in v1. Revisit only if the client interoperability matrix requires it. |
| Scope definitions and the tool-to-scope map | required | M1 | none | Read scopes separate profile, activities and location, health, devices, nutrition, and women's health. Write and destructive scopes stay separate. The map is generated and documented in M1, even though remote enforcement is M2. |
| Scope enforcement from the verified token | required | M2 | `auth.RequireBearerTokenOptions.Scopes`, `auth.TokenInfo.Scopes` | The effective gate is the intersection of operator enablement and granted scope. Enablement alone never suffices. Remote deployments default to read-only. |
| Token exchange (RFC 8693) | deferred | — | `oauthex.TokenExchangeRequest`, `oauthex.ExchangeToken` | No upstream identity is delegated. The MCP token is never forwarded to Garmin. |
| External IdP integration | deferred | — | `auth/extauth` (`client_credentials.go`, `enterprise_handler.go`, `oidc_login.go`) | v1 resolves the principal from the Garmin login transaction. No enterprise IdP, client-credentials grant, or OIDC login is offered. |

## Sessions

| Feature | Status | Milestone | SDK package or type | Notes |
|---------|--------|-----------|---------------------|-------|
| Stateful sessions and `Mcp-Session-Id` | required | M2 | `mcp.StreamableHTTPHandler` stateful path; `mcp.StreamableHTTPOptions.SessionTimeout`; `Connection.SessionID` | `Mcp-Session-Id` is never authentication. Bind each session to the verified principal, OAuth client, resource, and scopes. Reject cross-principal resume and delete. Terminate active streams when the token family, consent, client, or Garmin account is revoked. |
| Stateless mode | optional | M2 | `mcp.StreamableHTTPOptions.Stateless` | Preferred where it is suitable, because it removes the session-binding attack surface. Not mandated: elicitation for destructive confirmation needs a server-to-client request path. |
| `Last-Event-ID` event resumption | optional | M2 | `mcp.StreamableHTTPOptions.EventStore` | The in-memory default is acceptable for the single-active-instance SQLite design. `Last-Event-ID` is never authentication, and the event buffer is bound to the principal. A persistent store is not built in v1. |
| `Mcp-Protocol-Version` negotiation | required | M2 | `mcp.ProtocolVersionSupporter`, `StreamableServerTransport.SupportsProtocolVersion` | The SDK negotiates across 2026-07-28, 2025-11-25, 2025-06-18, 2025-03-26, and 2024-11-05. Conformance runs against 2026-07-28. |

## Server features

| Feature | Status | Milestone | SDK package or type | Notes |
|---------|--------|-----------|---------------------|-------|
| Tools | required | M1 | `mcp.AddTool[In, Out]`, `mcp.Tool`, `mcp.ToolHandlerFor`, `mcp.CallToolResult` | One tool per file with a `register<Name>` function; a single `register.go` wires everything in tier order. The tier name lists and their startup validation are local code. |
| Tool annotations | required | M1 | `mcp.ToolAnnotations` (`ReadOnlyHint`, `DestructiveHint`, `IdempotentHint`, `OpenWorldHint`) | Every tool declares all four hints explicitly. Garmin is an open-world API. At v1.7.0 `ReadOnlyHint` and `IdempotentHint` are plain `bool` and serialize even when false, while `DestructiveHint` and `OpenWorldHint` are `*bool`. The schema snapshot tests must expect that shape. |
| Structured tool output schemas | required | M1 | `mcp.AddTool[In, Out]` generic input and output binding | Strict typed request models with ranges, formats, and defaults. Tolerant decoding stays on the Garmin side, not at the tool boundary. |
| Elicitation | required | M1 | `mcp.ServerSession.Elicit`, `mcp.ElicitParams`, `mcp.ElicitResult`, `mcp.ElicitationCapabilities` | Destructive operations request confirmation with a bounded timeout. **Deviation from the house pattern:** the house server proceeds when elicitation is unsupported or times out; this project fails closed, and the refusal names the reason. |
| Middleware | required | M1 | `Server.AddReceivingMiddleware`, `Server.AddSendingMiddleware`, `mcp.Middleware` | Rate limiting is handler middleware keyed per principal, returning a caller-actionable error result rather than a transport error. A nil limiter passes through. Per-principal keying has no house equivalent. |
| Progress notifications | optional | M1 | `mcp.ServerSession` notification methods | Used by the configurable safety delay before write and destructive execution, emitting one notification per second and honoring context cancellation. |
| Resources and resource templates | required | M3 | `mcp.Resource`, `mcp.ResourceTemplate`, `Server.AddResource`, `Server.AddResourceTemplate` | Covers the five workout-template resources at the pinned Taxuspt commit. Also the return path for bounded activity downloads, instead of writing a server filesystem path. |
| Prompts | deferred | — | `mcp.Prompt`, `Server.AddPrompt` | The pinned Taxuspt commit registers no prompts, so there is no parity obligation. Adding one would create a new public contract for no compatibility gain. |
| Completion | optional | M3 | the SDK completion handler path | Argument completion for resource templates. No milestone gate requires it. |

## Deprecated as of protocol 2026-07-28

SEP-2577 deprecates the three features below. The SDK keeps them for a
deprecation window of at least twelve months. This project must not build on
them.

| Feature | Status | Milestone | SDK package or type | Notes |
|---------|--------|-----------|---------------------|-------|
| Roots | deferred | — | `mcp.Root`, `ServerSession.ListRoots` | Deprecated. A remote tool must never write an arbitrary server filesystem path, so a client-declared root has no role here. |
| Sampling | deferred | — | `ServerSession.CreateMessage`, `ServerSession.CreateMessageWithTools` | Deprecated. No handler asks the client's model for a completion. |
| MCP `logging` capability | deferred | — | `mcp.LoggingCapabilities`, `ServerSession.Log` | Deprecated. Structured logging must instead be a local package that writes to stderr under the redaction rules in `docs/threat-model.md`, which also keeps stdout reserved for MCP frames under stdio. No logger exists yet. |

## Review triggers

Re-open this matrix when any of the following happens:

- the SDK or specification pin changes, which needs an ADR 0002 amendment and a
  conformance re-run;
- the client interoperability matrix forces DCR or Client ID Metadata Documents
  out of the deferred rows;
- the deprecation window on roots, sampling, or logging closes, which may remove
  SDK symbols this repository still compiles against indirectly;
- a conformance baseline entry appears, which must name the verified SDK
  limitation and the row it affects.
