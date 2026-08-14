# Upstream pins

These pins are the immutable baseline for this repository. Do not silently
change a baseline once implementation begins. A baseline change needs an ADR and
a parity-matrix update.

Use immutable commit links in every design note. Never cite a moving branch.

## Application references

| Upstream | Tag | Commit | Governs |
|----------|-----|--------|---------|
| `cyberjunky/python-garminconnect` | `0.3.10` | [`414b54023a31259232744bb67f00a2aa71065e09`](https://github.com/cyberjunky/python-garminconnect/commit/414b54023a31259232744bb67f00a2aa71065e09) | Garmin login, DI token refresh and persistence, HTTP behavior, endpoints, payload tolerance, retry semantics |
| `Taxuspt/garmin_mcp` | *(main at pin time)* | [`3610be6feed93088d85b0f35aba9d7d07c2505a7`](https://github.com/Taxuspt/garmin_mcp/commit/3610be6feed93088d85b0f35aba9d7d07c2505a7) | MCP tool contracts and user-visible compatibility |

The `python-garminconnect` baseline was re-pinned from
[`0.3.8`](https://github.com/cyberjunky/python-garminconnect/commit/e4e9748cf3fa62f997e77171addee3acc333232c)
to `0.3.10` on 2026-08-14. Release 0.3.10 is dated 2026-08-11. The re-pin
happened before any auth or session code existed, so it invalidates no
implemented behavior, and it is recorded here and in
`docs/implementation-status.md`. Earlier design notes that cite 0.3.8 describe
the previous baseline. `docs/parity.md` must record the widened reconciliation
window. Any further baseline change, now that reimplementation work has started,
needs its own ADR.

Notes on these pins:

- `python-garminconnect` 0.3.0 removed Garth and introduced native
  authentication with `garmin_tokens.json` persistence. Garth is historical
  context only. Do not build login on top of it.
- The pinned Taxuspt `pyproject.toml` still targets `garminconnect==0.3.2`, so
  the reconciliation window is now **0.3.2 to 0.3.10**. Account deliberately for
  every behavior change in that window.
- The pinned Taxuspt README claims "110+" tools. A strict source inventory finds
  138 `@app.tool()` registrations plus five workout-template resources. Verify
  the totals by static source extraction, then snapshot them in
  `docs/parity.md`.
- Both upstream repositories are MIT-licensed. Verify the licenses at the pinned
  commits and preserve the required notices.

### Why the baseline moved to 0.3.10

0.3.10 is largely a security-hardening release, and it hardens exactly the
surface this project reimplements: login, MFA continuation, token persistence,
token refresh, error rendering, and paged reads. Staying on 0.3.8 would mean
reimplementing known-weak behavior on purpose.

The items below are **requirements for this repository**. Each one is a behavior
we must implement and cover with tests, not upstream trivia. Each is
release-blocking for the auth/session slice.

| # | Required behavior | Where it lands |
|---|-------------------|----------------|
| 1 | Host allowlist: only `garmin.com` and `garmin.cn` derived hosts are reachable. Reject any other host before a request is built. | `internal/garmin/protocol`, HTTP client |
| 2 | Sanitized exception messages: no credential, token, cookie, header, or raw body text in any error string. | error types, client, tools |
| 3 | URL query redaction in login errors: an error that names an endpoint carries no query string. | login/MFA error rendering |
| 4 | Symlink-rejecting token paths: refuse a symlink at the token path **and** at every ancestor directory, checking the full ancestry. | token store |
| 5 | Serialized token refresh with atomic writes: one refresh at a time per principal, write to a temporary file and rename, never a partial file. | session/refresh, token store |
| 6 | JWT `exp` validation, and rejection of an unsigned payload. Never trust an unverified or `alg=none` token. | token parsing |
| 7 | Server-driven pagination caps: honor the server page cap instead of a client-chosen page size, and bound total results. | API clients, tools |
| 8 | Interleaved MFA logins must not overwrite each other's pending state. Pending MFA state is keyed per login transaction. | login transaction state |
| 9 | Explicit widget MFA code delivery (upstream GH-386): request the code explicitly instead of assuming that the page sends it. | widget login strategy |
| 10 | Segment-aware path-traversal guards: validate each path segment, not the joined string. | download/file-taking paths, token store |

Items 4 to 10 have no equivalent in this repository yet. Items 1,
2, and 3 exist only in part. `internal/garmin/protocol` derives every host from
the two allowed domains, sanitizes page titles and scraped tokens, and its
`Error` type carries a fixed endpoint label instead of a URL. No HTTP client
enforces the allowlist at request time, because no client exists yet.

## Protocol and SDK pins

Both entries are pinned. The decision and its evidence are recorded in
`docs/adr/0002-mcp-sdk-and-spec-version.md`, which is **Accepted** as of
2026-08-14.

| Item | Repository | Pin | Governs |
|------|-----------|-----|---------|
| Official MCP Go SDK | [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) | `v1.7.0`, tagged commit [`bc72835f62eb94d0fb484439f886b6885b075f36`](https://github.com/modelcontextprotocol/go-sdk/commit/bc72835f62eb94d0fb484439f886b6885b075f36) (tag object `25cb00203c6b693780f602ab4041c06f7f4b9570`) | MCP types, tool registration, stdio, Streamable HTTP |
| MCP specification | [`modelcontextprotocol/modelcontextprotocol`](https://github.com/modelcontextprotocol/modelcontextprotocol) | `2026-07-28` | Transport, authorization discovery, security, tool semantics |

Why this pair, verified on 2026-08-14 against the GitHub releases API and the
repository tree at the `v1.7.0` tag:

- `v1.7.0` was published on 2026-07-28 with `prerelease=false` and
  `draft=false`. It is a **stable** release, so no prerelease exception is
  needed. The brief's selection rule, written on 2026-08-05 when v1.7.0 was
  still a prerelease, is therefore satisfied by adopting it.
- The README compatibility table at the tag states that v1.7.0 and later support
  2026-07-28, 2025-11-25, 2025-06-18, 2025-03-26, and 2024-11-05. The
  2025-11-25 entry is footnoted: client-side OAuth support is experimental at
  that version. 2026-07-28 is the latest dated specification the pinned SDK
  supports, so it is the pinned specification.
- The README states that roots, sampling, and logging are deprecated as of
  protocol 2026-07-28 by SEP-2577, and that the SDK keeps them for at least
  twelve months. This project must not build on them.

The SDK is deliberately **not yet a requirement in `go.mod`**. `go mod tidy`
drops a requirement that no package imports, and CI verifies a clean
`go mod tidy` diff, so an unused requirement would fail the build. The module
line lands with the MCP foundation slice (phase 3), in the same commit as the
first code that imports `mcp`. A reader who finds no SDK line in `go.mod` must
read that as sequencing, not as a forgotten pin.

Per-feature obligations for this pair — required, optional, or deferred, with the
providing SDK package or type and the owning milestone — are in
`docs/mcp-version-matrix.md`.

The MCP conformance suite
([`modelcontextprotocol/conformance`](https://github.com/modelcontextprotocol/conformance))
is pinned to an immutable release or commit SHA in the CI workflow that runs it.

## Requirement precedence

When requirements conflict, resolve in this order:

1. credential and tenant security;
2. the selected dated MCP specification;
3. the pinned Taxuspt public contracts;
4. the pinned `python-garminconnect` behavior;
5. house Go engineering standards;
6. optional convenience.

Record every intentional security-driven compatibility break in
`docs/adr/0006-deliberate-compatibility-breaks.md` and in `docs/parity.md`.
