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

Most rows have landed. The SDK is a direct requirement in `go.mod`, both
transports exist, 143 tools and all five resources are registered, and the policy
layer, the rate limiter and the logger all exist.
`docs/implementation-status.md` is the authoritative gap list.

**Conformance is not a review gate for this project.** The official suite was run
against a live deployment and cannot score a domain server: its stable release
does not know the pinned specification version, and its server leg can present no
credential while every scored scenario calls the SDK reference fixture's tools by
literal name. No baseline was written, because a baseline of that size would
encode "this is not the SDK fixture", which is not a verified SDK limitation. The
measurement and the two blockers are recorded in ADR 0002 and in
`docs/implementation-status.md`. Read every row below that mentions a conformance
run against that finding.

## Transports

| Feature | Status | Milestone | SDK package or type | Notes |
|---------|--------|-----------|---------------------|-------|
| stdio transport | required | M1 | `mcp.StdioTransport` | Stdout carries MCP frames only. Logs and status go to stderr through `internal/mcplog`, which refuses stdout. |
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
| Client registration — RFC 7591 DCR | deferred | — | `oauthex.ClientRegistrationMetadata`, `oauthex.ClientRegistrationResponse`, `oauthex.RegisterClient`, `auth.DynamicClientRegistrationConfig` | The SDK support is client-side: it registers *with* a server. Serving a registration endpoint is local work. Unrestricted anonymous production DCR is prohibited. The "constrained profile for the conformance run" this row previously allowed is withdrawn: there is no conformance run, so there is no reason to build it. |
| Client registration — Client ID Metadata Documents | deferred | — | `auth.ClientIDMetadataDocumentConfig` | Needs an explicit trust policy and an SSRF-safe fetcher. Not built in v1. Revisit only if the client interoperability matrix requires it. |
| Scope definitions and the tool-to-scope map | required | M1 | none | Read scopes separate profile, activities and location, health, devices, nutrition, and women's health. Write and destructive scopes stay separate. The map is generated and documented in M1, even though remote enforcement is M2. |
| Scope enforcement from the verified token | required | M2 | `auth.RequireBearerTokenOptions.Scopes`, `auth.TokenInfo.Scopes` | For remote callers, the effective gate is the intersection of operator enablement and granted scope. Enablement alone never suffices. Remote deployments default to read-only. |
| Token exchange (RFC 8693) | deferred | — | `oauthex.TokenExchangeRequest`, `oauthex.ExchangeToken` | No upstream identity is delegated. The MCP token is never forwarded to Garmin. |
| External IdP integration | deferred | — | `auth/extauth` (`client_credentials.go`, `enterprise_handler.go`, `oidc_login.go`) | v1 resolves the principal from the Garmin login transaction. No enterprise IdP, client-credentials grant, or OIDC login is offered. |

## Sessions

| Feature | Status | Milestone | SDK package or type | Notes |
|---------|--------|-----------|---------------------|-------|
| Stateful sessions and `Mcp-Session-Id` | required | M2 | `mcp.StreamableHTTPHandler` stateful path; `mcp.StreamableHTTPOptions.SessionTimeout`; `Connection.SessionID` | `Mcp-Session-Id` is never authentication. Bind each session to the verified principal, OAuth client, resource, and scopes. Reject cross-principal resume and delete. Terminate active streams when the token family, consent, client, or Garmin account is revoked. |
| Stateless mode | optional | M2 | `mcp.StreamableHTTPOptions.Stateless` | Preferred where it is suitable, because it removes the session-binding attack surface. Not mandated: elicitation for destructive confirmation needs a server-to-client request path. |
| `Last-Event-ID` event resumption | optional | M2 | `mcp.StreamableHTTPOptions.EventStore` | The in-memory default is acceptable for the single-active-instance SQLite design. `Last-Event-ID` is never authentication, and the event buffer is bound to the principal. A persistent store is not built in v1. |
| `Mcp-Protocol-Version` negotiation | required | M2 | `mcp.ProtocolVersionSupporter`, `StreamableServerTransport.SupportsProtocolVersion` | The SDK negotiates across 2026-07-28, 2025-11-25, 2025-06-18, 2025-03-26, and 2024-11-05. This server advertises 2026-07-28. |

## Server features

| Feature | Status | Milestone | SDK package or type | Notes |
|---------|--------|-----------|---------------------|-------|
| Tools | required | M1 | `mcp.AddTool[In, Out]`, `mcp.Tool`, `mcp.ToolHandlerFor`, `mcp.CallToolResult` | One `register<Name>` function per tool, grouped by domain into files; `register.go` wires everything in tier order. The tier name lists and their start-up validation are local code in `internal/tools` and `internal/policy`. `tools/list` is additionally narrowed, per request, to the tools `policy.Decide` would allow the calling session AND, for a destructive tool, to whether the caller declared the elicitation capability the call path would need to confirm it — the registered set stays complete; only this view is filtered, by a receiving middleware that runs the same `Decide` the `tools/call` gate runs plus the same capability test `confirmDestructive` runs. The filtered result also carries `"cacheScope":"private"` rather than the SDK's default `"public"`, since it is caller-specific. Each tool's `_meta` carries its policy tier (`mcp.Tool` embeds `mcp.Meta`, the SDK's supported per-tool metadata slot). |
| Tool annotations | required | M1 | `mcp.ToolAnnotations` (`ReadOnlyHint`, `DestructiveHint`, `IdempotentHint`, `OpenWorldHint`) | Every tool declares all four hints explicitly. Garmin is an open-world API. At v1.7.0 `ReadOnlyHint` and `IdempotentHint` are plain `bool` and serialize even when false, while `DestructiveHint` and `OpenWorldHint` are `*bool`. The schema snapshot tests must expect that shape. |
| Structured tool output schemas | required | M1 | `mcp.AddTool[In, Out]` generic input and output binding | Strict typed request models with ranges, formats, and defaults. Tolerant decoding stays on the Garmin side, not at the tool boundary. |
| Elicitation | required | M1 | `mcp.ServerSession.Elicit`, `mcp.ElicitParams`, `mcp.ElicitResult`, `mcp.ElicitationCapabilities` | Destructive operations request confirmation with a bounded timeout. **Deviation from the house pattern:** the house server proceeds when elicitation is unsupported or times out; this project fails closed, and the refusal names the reason. |
| Middleware | required | M1 | `Server.AddReceivingMiddleware`, `Server.AddSendingMiddleware`, `mcp.Middleware` | Rate limiting is handler middleware keyed per principal, returning a caller-actionable error result rather than a transport error. A nil limiter passes through. Per-principal keying has no house equivalent. |
| Progress notifications | optional | M1 | `mcp.ServerSession` notification methods | Used by the configurable safety delay before write and destructive execution, emitting one notification per second and honoring context cancellation. |
| Resources and resource templates | required | M3 | `mcp.Resource`, `mcp.ResourceTemplate`, `Server.AddResource`, `Server.AddResourceTemplate` | Covers the five workout-template resources at the pinned Taxuspt commit. **All five are implemented** in `internal/resources`, with the manifest contract, the render, and a test that this server's own upload path accepts every template. The embedded-resource return path is in use already: `download_activity_file` returns a bounded `mcp.EmbeddedResource` instead of writing a server filesystem path. |
| Prompts | deferred | — | `mcp.Prompt`, `Server.AddPrompt` | The pinned Taxuspt commit registers no prompts, so there is no parity obligation. Adding one would create a new public contract for no compatibility gain. |
| Completion | optional | M3 | the SDK completion handler path | Argument completion for resource templates. No milestone gate requires it. |

## Destructive confirmation, by negotiated protocol version

A destructive tool never runs unconfirmed, and the shape of the question depends
on the version the session negotiated. A client author needs this, so it is
written here rather than left to be read out of `internal/mcpserver/confirm.go`.

| Negotiated version | Confirmation shape |
|--------------------|--------------------|
| `2026-07-28` and later | The tool call returns a `CallToolResult` carrying `InputRequests` and `RequestState`. Nothing has run and nothing was denied. The client asks its user, then **re-calls the same tool** with the answer in `InputResponses`. SEP-2322 forbids a server-to-client request while a request is in flight, and the SDK enforces it, so this is the only shape available. |
| `2025-11-25`, `2025-06-18`, `2025-03-26`, `2024-11-05` | The server sends `elicitation/create` on the session **while the call is in flight** and waits, under a bounded deadline, for the answer. |

The version is compared as a string, which orders these correctly because MCP
protocol versions are ISO dates. The cut is `>= 2026-07-28`.

What a client must do, in both shapes:

- **Declare the elicitation capability.** That is the only client-side
  prerequisite. A client that declares none is refused before either shape is
  chosen, whatever its protocol version.
- **Answer the single boolean property `confirm`.** The requested schema is an
  object with `confirm` (boolean), and `confirm` is `required`, so accepting the
  prompt and sending nothing is not an answer. Only an accepted result with
  `confirm` true proceeds; declining, cancelling and dismissing all refuse. On the
  in-flight shape, letting the bounded wait elapse also refuses. Two cases are
  *not* refusals: on the multi-round-trip shape an answer that is not an
  elicitation result, or one filed under a different key, counts as no answer at
  all, so the tool asks again rather than being denied — and simply never
  re-calling the tool produces no result of any kind, because nothing is pending
  server-side.
- **On the multi-round-trip shape, echo the key exactly.** The key is
  `confirm:<tool name>` and the retry is looked up under that exact key, so a
  confirmation given for one tool cannot authorize another. `RequestState` carries
  the same key and is a correlation hint, never a capability: the whole policy
  gate — enablement, scope, tier — re-runs from scratch on the retry, so the round
  trip grants nothing by itself.

### What a refusal looks like on the wire

A refused confirmation is **not** a JSON-RPC error. It is a `CallToolResult` with
`isError: true` and one text content, because a transport error is invisible to
the model whereas an error result reaches it as text it can act on. The text is
stable and can be mapped to a message of your own:

| Cause | Text |
|-------|------|
| Client declared no elicitation capability | `This tool was refused because it needs confirmation: policy: destructive tool requires confirmation: policy: confirmation is unsupported by the client.` |
| User declined or dismissed | `This tool was refused because it needs confirmation: policy: destructive tool requires confirmation: policy: confirmation was declined.` |
| Bounded wait elapsed | `This tool was refused because it needs confirmation: policy: destructive tool requires confirmation: policy: confirmation timed out.` |

The underlying transport error is never included: it can carry an `Authorization`
header, a cookie or a response body, so it is classified into one of the reasons
above and discarded.

## Deprecated as of protocol 2026-07-28

SEP-2577 deprecates the three features below. The SDK keeps them for a
deprecation window of at least twelve months. This project must not build on
them.

| Feature | Status | Milestone | SDK package or type | Notes |
|---------|--------|-----------|---------------------|-------|
| Roots | deferred | — | `mcp.Root`, `ServerSession.ListRoots` | Deprecated. A remote tool must never write an arbitrary server filesystem path, so a client-declared root has no role here. |
| Sampling | deferred | — | `ServerSession.CreateMessage`, `ServerSession.CreateMessageWithTools` | Deprecated. No handler asks the client's model for a completion. |
| MCP `logging` capability | deferred | — | `mcp.LoggingCapabilities`, `ServerSession.Log` | Deprecated. Structured logging must instead be a local package that writes to stderr under the redaction rules in `docs/threat-model.md`, which also keeps stdout reserved for MCP frames under stdio. That package is `internal/mcplog`. |

## Review triggers

Re-open this matrix when any of the following happens:

- the SDK or specification pin changes, which needs an ADR 0002 amendment and,
  because no conformance run is available, full coverage by this repository's own
  tests including `e2e/remote_test.go`;
- the client interoperability matrix forces DCR or Client ID Metadata Documents
  out of the deferred rows;
- the deprecation window on roots, sampling, or logging closes, which may remove
  SDK symbols this repository still compiles against indirectly;
- the official conformance suite ships a stable release that knows `2026-07-28`
  **and** accepts a credential on its server leg, which is what would make a
  conformance gate possible at all.
