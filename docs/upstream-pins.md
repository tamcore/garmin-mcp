# Upstream pins

These pins are the immutable baseline for this repository. Do not silently
change a baseline once implementation begins. A baseline change needs an ADR and
a parity-matrix update.

Use immutable commit links in every design note. Never cite a moving branch.

## Application references

| Upstream | Tag | Commit | Governs |
|----------|-----|--------|---------|
| `cyberjunky/python-garminconnect` | `0.3.8` | [`e4e9748cf3fa62f997e77171addee3acc333232c`](https://github.com/cyberjunky/python-garminconnect/commit/e4e9748cf3fa62f997e77171addee3acc333232c) | Garmin login, DI token refresh and persistence, HTTP behavior, endpoints, payload tolerance, retry semantics |
| `Taxuspt/garmin_mcp` | *(main at pin time)* | [`3610be6feed93088d85b0f35aba9d7d07c2505a7`](https://github.com/Taxuspt/garmin_mcp/commit/3610be6feed93088d85b0f35aba9d7d07c2505a7) | MCP tool contracts and user-visible compatibility |

Notes on these pins:

- `python-garminconnect` 0.3.0 removed Garth and introduced native
  authentication with `garmin_tokens.json` persistence. Garth is historical
  context only. Do not build login on top of it.
- The pinned Taxuspt `pyproject.toml` still targets `garminconnect==0.3.2`.
  Account deliberately for the behavior changes through 0.3.8.
- The pinned Taxuspt README claims "110+" tools. A strict source inventory finds
  138 `@app.tool()` registrations plus five workout-template resources. Verify
  the totals by static source extraction, then snapshot them in
  `docs/parity.md`.
- Both upstream repositories are MIT-licensed. Verify the licenses at the pinned
  commits and preserve the required notices.

## Protocol and SDK pins

These two entries are not pinned yet. They are pinned when the official Go SDK
is added to `go.mod`, and the decision is recorded in
`docs/adr/0002-mcp-sdk-and-spec-version.md`.

| Item | Repository | Pin | Governs |
|------|-----------|-----|---------|
| Official MCP Go SDK | [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) | TODO: pin the latest stable release and its commit SHA when the SDK is added | MCP types, tool registration, stdio, Streamable HTTP |
| MCP specification | [`modelcontextprotocol/modelcontextprotocol`](https://github.com/modelcontextprotocol/modelcontextprotocol) | TODO: pin the latest dated specification fully supported by that stable SDK | Transport, authorization discovery, security, tool semantics |

Selection rule, as researched on 2026-08-05: the Go SDK compatibility table says
the stable v1.4.0 to v1.6.1 line supports MCP 2025-11-25, while v1.7.0 support
for MCP 2026-07-28 was still represented by prereleases. Default to the latest
stable SDK and specification pairing. Adopt v1.7.0 with 2026-07-28 only if
v1.7.0 is stable when implementation starts, or after ADR 0002 documents why a
prerelease is necessary and the conformance suite passes.

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
