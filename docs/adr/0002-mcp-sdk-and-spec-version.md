# ADR 0002 — MCP SDK choice and specification version

## Status

Accepted — 2026-08-14.

Supersedes the open version-selection rule that this ADR carried before. The SDK
choice was already decided; this revision closes the version pin.

## Context

The house MCP servers use `github.com/mark3labs/mcp-go` (kubectl-mcp is on
v0.57.0). This project needs three things that point away from that SDK: OAuth
resource-server integration, Streamable HTTP behavior, and the official MCP
conformance suite. The official `github.com/modelcontextprotocol/go-sdk`
provides all three.

The selected dated MCP specification is normative for wire behavior and
conformance. Newer MCP security guidance may be adopted as defense in depth only
where it does not conflict with the selected specification.

The brief was written on 2026-08-05. At that date the stable SDK line was
v1.4.0 to v1.6.1 on specification 2025-11-25, and v1.7.0 existed only as
prereleases (`v1.7.0-pre.1` to `v1.7.0-pre.3`). The brief therefore said to
adopt v1.7.0 with specification 2026-07-28 only if v1.7.0 was stable when
implementation started, or after this ADR justified a prerelease.

That condition is now satisfied. Verified on 2026-08-14 against the GitHub
releases API and the repository tree at the `v1.7.0` tag:

- `v1.7.0` was published on 2026-07-28 with `prerelease=false` and
  `draft=false`. It is a stable release. No prerelease exception is needed.
- The README compatibility table at the tag states that v1.7.0 and later support
  specification versions 2026-07-28, 2025-11-25, 2025-06-18, 2025-03-26, and
  2024-11-05. The 2025-11-25 entry carries a footnote: client-side OAuth support
  is experimental at that version.
- The README states that roots, sampling, and logging are deprecated as of
  protocol version 2026-07-28 by SEP-2577. The SDK keeps them for a deprecation
  window of at least twelve months.

Verified package surface at the tag, which decides how much of the OAuth work
this project must supply itself:

- `auth/` contains `auth.go`, `authorization_code.go`, `client.go`, and
  `shared.go`, plus an `extauth` subpackage (`client_credentials.go`,
  `enterprise_handler.go`, `oidc_login.go`).
- `oauthex/` contains `audience.go`, `auth_meta.go`, `client.go`, `dcr.go`,
  `oauth2.go`, `oauthex.go`, `resource_meta.go`, and `token_exchange.go`.

So the SDK covers the OAuth **resource-server** side plus metadata types,
audience validation, dynamic-registration types, and token-exchange types. It
does **not** provide an authorization-server implementation. Its only
authorization server is `internal/oauthtest/fake_authorization_server.go`, which
is an internal test fixture and is not importable. Selecting the
authorization-server component is ADR 0003's decision, and this ADR does not
pre-empt it.

## Decision

Use the official `github.com/modelcontextprotocol/go-sdk`.

| Item | Pin |
|------|-----|
| SDK module version | `v1.7.0` |
| SDK release date | 2026-07-28 |
| SDK tag object | `25cb00203c6b693780f602ab4041c06f7f4b9570` |
| SDK tagged commit | `bc72835f62eb94d0fb484439f886b6885b075f36` |
| MCP specification | `2026-07-28` |

Port the house *patterns* — registration structure, tier gating, annotations,
middleware, policy, and test layering — never the `mcp-go` call shapes.

The dependency is a direct requirement in `go.mod` now. It landed with the MCP
foundation slice (phase 3), in the same commit as the first code that imports
`mcp`, which is what a clean `go mod tidy` diff requires.

Per-feature obligations for this pair are in `docs/mcp-version-matrix.md`, which
marks each relevant feature required, optional, or deferred and names the SDK
package or type that provides it.

Roots, sampling, and logging are deprecated as of protocol 2026-07-28. The SDK
still supports them, but this project must not build any behavior on them. The
`logging` protocol capability in particular must not be used. Structured logging
lives in `internal/mcplog`, a local package that writes to stderr and refuses
stdout, which keeps stdout reserved for MCP frames under stdio. It reads
`Config.LogLevel` and `Config.LogFormat`.

### The conformance suite cannot score this server

[`modelcontextprotocol/conformance`](https://github.com/modelcontextprotocol/conformance)
is a TypeScript CLI published to npm as `@modelcontextprotocol/conformance`, plus
a composite GitHub Action. It is not a Go package and not a container. In server
mode it connects to a running server as an MCP client over Streamable HTTP.

It was run for real against a live deployment of this server — generated TLS
certificate, master key, empty database, one preregistered public client, serving
at `https://127.0.0.1:8443/mcp` on protocol version `2026-07-28` with 48 tools.
**45 passed, 106 failed.** Every one of the 36 scored server scenarios failed
except three, and two of those three passed vacuously with zero checks.

Two independent blockers, both read out of the suite's own source:

1. The only stable release, `v0.1.16` (tag commit
   `21a9a2febd7100d7c17ac1021ee7f2ed9f66a1e0`), knows specification versions only
   up to `2026-02-12`. `2026-07-28` exists solely on the `0.2.0-alpha` line, so
   testing the pinned wire version means pinning a prerelease.
2. The suite's server leg cannot present a credential — `ServerOptionsSchema`
   accepts only a `url` and a `scenario`, with no header, token or client
   credentials — while this server authenticates every `POST`, `GET` and `DELETE`
   from the `Authorization` header. Even with a token, the scored scenarios call
   the SDK reference fixture's tools by literal name (`test_simple_text`,
   `test_image_content`, `test_audio_content`, `test_tool_with_progress`, plus
   fixture prompts, resources and completion flows), and a missing tool is
   recorded as a failure rather than a skip. A domain server fails by
   construction.

**No baseline was written, deliberately.** A baseline covering roughly 35
scenarios would encode "this is not the SDK reference fixture", which is not a
verified SDK limitation and could never legitimately clear. This ADR permits a
baseline entry only for a verified limitation.

Three things would unblock it, and none was attempted: a header or bearer input
on the suite's server leg, which is an upstream change; a conformance fixture
profile inside this server that exposes the suite's expected tool, prompt and
resource surface, which would test the SDK rather than this product and is
therefore refused; or an upstream requirement set for domain servers.

The requirement stays recorded rather than dropped. Re-check it when the suite
ships a stable release that knows `2026-07-28` **and** accepts a credential on
the server leg. The suite validates MCP wire, transport, tool, and resource
behavior only in any case. It is not certification of the embedded OAuth
authorization server, which keeps its own negative test matrix, and the
transport-level authorization behaviors it cannot reach are covered by
`e2e/remote_test.go` against the real binary instead.

## Alternatives considered

**The v1.4.0 to v1.6.1 line on specification 2025-11-25.** Rejected. This was
the correct choice under the brief's rule on 2026-08-05, and only because v1.7.0
was not yet stable. Choosing it now would pin a superseded specification on the
first day of implementation and would then need a second migration during M2,
when the OAuth surface is the largest. Its client-side OAuth support is also
marked experimental at 2025-11-25, whereas 2026-07-28 makes it ordinary. The
2025-11-25 specification stays reachable through the SDK's supported-version
list, so older clients keep working without pinning to it.

**`github.com/mark3labs/mcp-go`.** Rejected, and this is a deliberate deviation
from house convention. It is the maintainer's house SDK in other repositories,
including `tamcore/kubectl-mcp` on v0.57.0. It is not used here for the three
reasons the brief's SDK-deviation section gives: this project needs OAuth
resource-server integration, it needs Streamable HTTP behavior, and it was
expected to run the official conformance suite. The official SDK ships the first
two, which is what still justifies the choice; the conformance argument turned
out to be worth nothing, because the suite cannot score a domain server at all.

House patterns with no direct official-SDK equivalent, and how each is
reimplemented, are recorded in `docs/mcp-version-matrix.md` next to the feature
they belong to. The per-domain registration layout, the explicit
`writeTools`/`destructiveTools` name lists validated at startup, and the
rate-limit handler middleware are local code in `internal/tools`,
`internal/policy` and `internal/ratelimit`, because the official SDK supplies
only `mcp.AddTool` and the `Middleware` hooks they build on.

## Consequences

- Every MCP call shape in this repository follows the official SDK, so house
  reference code cannot be copied literally.
- This project supplies the authorization-server role itself. The SDK's `auth`
  and `oauthex` packages cover only the resource-server side and the metadata,
  audience, registration, and token-exchange types.
- Roots, sampling, and the `logging` capability are off limits as a design
  foundation, even though the SDK still accepts them.
- **There is no conformance signal for this server, and none can be
  manufactured.** No CI job runs the suite and no baseline file exists. If the
  suite ever becomes usable, results are tied to the pinned pair, a baseline
  entry may record a verified SDK limitation temporarily, and CI must fail on a
  new failure and on a stale expected-failure entry.
- Changing the pin later needs an amendment here. It cannot be validated by a
  conformance re-run, so the SDK upgrade must be covered by this repository's own
  tests, including `e2e/remote_test.go`.
- The transport authorization behaviors the suite would have covered are proven
  instead by `e2e/remote_test.go` against the real binary.
