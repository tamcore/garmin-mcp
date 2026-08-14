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

The dependency is deliberately **not** in `go.mod` yet. `go mod tidy` drops a
requirement that no package imports, and CI verifies a clean `go mod tidy` diff,
so an unused requirement would fail the build. The pin therefore lands with the
MCP foundation slice (phase 3), in the same commit as the first code that imports
`mcp`. The pin is not forgotten; it is recorded here and in
`docs/upstream-pins.md` ahead of the code that carries it.

Per-feature obligations for this pair are in `docs/mcp-version-matrix.md`, which
marks each relevant feature required, optional, or deferred and names the SDK
package or type that provides it.

Roots, sampling, and logging are deprecated as of protocol 2026-07-28. The SDK
still supports them, but this project must not build any behavior on them. The
`logging` protocol capability in particular is not used: structured logging lives
in `internal/mcplog` and writes to stderr, which also keeps stdout reserved for
MCP frames under stdio.

The conformance suite
([`modelcontextprotocol/conformance`](https://github.com/modelcontextprotocol/conformance))
is pinned to an immutable release or commit SHA in the CI workflow that runs it,
when that workflow gains a server to test. It validates MCP wire, transport,
tool, and resource behavior only. It is not certification of the embedded OAuth
authorization server, which keeps its own negative test matrix.

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
resource-server integration, it needs Streamable HTTP behavior, and it must run
the official conformance suite. The official SDK ships all three; adopting
`mcp-go` would mean reimplementing the resource-server layer and forgoing the
conformance signal.

House patterns with no direct official-SDK equivalent, and how each is
reimplemented, are recorded in `docs/mcp-version-matrix.md` next to the feature
they belong to. The one-tool-per-file registration layout, the explicit
`writeTools`/`destructiveTools` name lists validated at startup, and the
rate-limit handler middleware are all local code in `internal/tools`,
`internal/policy`, and `internal/ratelimit`; the official SDK supplies only
`mcp.AddTool` and the `Middleware` hooks they build on.

## Consequences

- Every MCP call shape in this repository follows the official SDK, so house
  reference code cannot be copied literally.
- This project supplies the authorization-server role itself. The SDK's `auth`
  and `oauthex` packages cover only the resource-server side and the metadata,
  audience, registration, and token-exchange types.
- Roots, sampling, and the `logging` capability are off limits as a design
  foundation, even though the SDK still accepts them.
- Conformance results are tied to the pinned pair. A baseline entry may record a
  verified SDK limitation temporarily, but CI fails on a new failure and on a
  stale expected-failure entry.
- Changing the pin later needs an amendment here plus a conformance re-run.
- The pin is recorded before the module requirement exists. A reader who sees no
  SDK line in `go.mod` must read that as sequencing, not as an open decision.
