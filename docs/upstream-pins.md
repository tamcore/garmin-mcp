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

The status column was re-verified against the code on 2026-08-15, row by row.

| # | Required behavior | Where it lands | Status |
|---|-------------------|----------------|--------|
| 1 | Host allowlist: only `garmin.com` and `garmin.cn` derived hosts are reachable. Reject any other host before a request is built. | `internal/garmin/protocol`, HTTP client | **landed.** Only those two domains parse into a `protocol.ValidatedDomain`, and every URL the auth and API layers build comes from a `protocol.Hosts` derived from one. `internal/garmin/auth/hostguard.go` adds the request-time check: `Refresher.Do` refuses a caller-supplied request whose host is not a validated Garmin host, with `ErrForeignHost`, on the first attempt and on the post-`401` replay. |
| 2 | Sanitized exception messages: no credential, token, cookie, header, or raw body text in any error string. | error types, client, tools | **landed.** `internal/garmin/protocol` and `internal/garmin/auth` sanitize page titles and scraped tokens, the alias-leak tests prove the sealed values stay unreadable under every `fmt` verb, and `internal/tools` carries its own redaction tests over the tool result and error paths. |
| 3 | URL query redaction in login errors: an error that names an endpoint carries no query string. | login/MFA error rendering | **landed.** `protocol.Error` carries a fixed endpoint label instead of a URL. |
| 4 | Symlink-rejecting token paths: refuse a symlink at the token path **and** at every ancestor directory, checking the full ancestry. | token store | **landed.** `store.ResolveTokenFilePath` refuses `~user` and checks the full ancestry, and `internal/securefile` re-verifies every component against a directory descriptor with an identity check after the open. |
| 5 | Serialized token refresh with atomic writes: one refresh at a time per principal, write to a temporary file and rename, never a partial file. | session/refresh, token store | **landed.** Concurrent refreshes for one principal collapse into one flight; `securefile.WriteFile` writes a random-suffixed temporary sibling, `fsync`s it, renames it over the target, and syncs the directory. Cross-process CAS is out of scope for the file store. |
| 6 | JWT `exp` validation, and rejection of an unsigned payload. Never trust an unverified or `alg=none` token. | token parsing | **landed.** `auth/jwt_unverified.go` and `store/document.go` reject `alg:none` case-folded, a missing or empty signature segment, non-numeric, non-finite and overflowing `exp`, and oversized tokens and segments. The value is used for expiry only. |
| 7 | Server-driven pagination caps: honor the server page cap instead of a client-chosen page size, and bound total results. | API clients, tools | **landed.** `internal/garmin/client` carries `MaxPageSizeCap`, `MaxPagesCap` and the derived `MaxPageStartCap`, and a page is clamped to the resolved `Limits.MaxPageSize` rather than to the caller's request. `internal/tools/args.go` refuses a `start` or `limit` above the cap and silently narrows a limit above the configured page size, and the date-window argument is bounded by `Limits.MaxDateRangeDays`. Wire and decompressed response bytes are bounded separately. |
| 8 | Interleaved MFA logins must not overwrite each other's pending state. Pending MFA state is keyed per login transaction. | login transaction state | **landed.** The bounded registry keys state per transaction capability, holds no per-client "current login" field, and returns an immutable deep copy per attempt. Proven by 16-way concurrent isolation tests under `-race`. |
| 9 | Explicit widget MFA code delivery (upstream GH-386): request the code explicitly instead of assuming that the page sends it. | widget login strategy | **not started.** `PathWidgetRequestMFACode`, `EndpointWidgetRequestMFACode` and `Hosts.WidgetRequestMFACodeURL` exist and nothing calls them, so the requirement has a constant and no behavior. `MFADeliveryUncertain()` signals the gap to the caller. |
| 10 | Segment-aware path-traversal guards: validate each path segment, not the joined string. | download/file-taking paths, token store | **landed, and partly by removal.** The store and key paths are resolved component by component with `os.Root`, so the joined string is never trusted. The download side takes **no** path at all: `download_activity_file` accepts only an activity id and a format and returns a bounded embedded resource, and `set_fit_download_dir` is not registered. There is therefore no caller-supplied filename anywhere in the server to validate. Re-open this row if a file-taking path is ever added. |

Items 1 to 8 and 10 are landed. Item 9, explicit widget MFA code delivery, has
not started and is the only one outstanding.
`docs/implementation-status.md` carries the same split in its gap list, and the
two files must be changed together.

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

The SDK is a direct requirement in `go.mod` now. It landed with the MCP
foundation slice (phase 3), in the same commit as the first code that imports
`mcp`, which is what a clean `go mod tidy` diff requires.

Per-feature obligations for this pair — required, optional, or deferred, with the
providing SDK package or type and the owning milestone — are in
`docs/mcp-version-matrix.md`.

The MCP conformance suite
([`modelcontextprotocol/conformance`](https://github.com/modelcontextprotocol/conformance))
**is not pinned, and it will not be.** It was run for real against a live
deployment of this server and cannot score a domain server: 45 passed and 106
failed, and every scored server scenario failed except three, two of which passed
with zero checks. Its only stable release, `v0.1.16` (tag commit
`21a9a2febd7100d7c17ac1021ee7f2ed9f66a1e0`), knows specification versions only up
to `2026-02-12`, and its server leg can present no credential while every scored
scenario calls the SDK reference fixture's tools by literal name. ADR 0002 and
`docs/implementation-status.md` carry the full evidence and the conditions that
would make a pin worthwhile.

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
