# ADR 0002 — MCP SDK choice and specification version

## Status

Open. The SDK choice is decided; the version pin is not. This ADR is completed in
phase 1, in the same commit that adds the SDK to `go.mod`.

## Context

The house MCP servers use `github.com/mark3labs/mcp-go`. This project needs
OAuth resource-server integration, Streamable HTTP behavior, and the official
conformance suite, which point at the official `modelcontextprotocol/go-sdk`
instead.

The selected dated MCP specification is normative for wire behavior and
conformance. Newer MCP security guidance may be adopted as defense in depth only
where it does not conflict with the selected specification.

As researched on 2026-08-05, the Go SDK compatibility table says the stable
v1.4.0 to v1.6.1 line supports MCP 2025-11-25, while v1.7.0 support for MCP
2026-07-28 was still represented by prereleases.

## Decision

Use the official `modelcontextprotocol/go-sdk`. Port the house *patterns* —
registration structure, tier gating, annotations, middleware, policy, and test
layering — never the `mcp-go` call shapes.

Version selection rule: pin the latest **stable** SDK release and the latest
dated specification fully supported by it. Adopt v1.7.0 with MCP 2026-07-28 only
if v1.7.0 is stable when implementation starts, or after this ADR documents why a
prerelease is necessary and the conformance suite passes.

Completing this ADR requires:

- the exact SDK version and commit SHA, recorded here and in
  `docs/upstream-pins.md`;
- the selected dated specification;
- the pinned conformance-suite release or SHA;
- `docs/mcp-version-matrix.md`, marking each relevant feature as required,
  optional, or deferred for the selected pair: Streamable HTTP lifecycle,
  Protected Resource Metadata, authorization-server discovery, registration mode,
  resource indicators, scopes, and session behavior;
- the list of house patterns with no direct official-SDK equivalent, and how each
  was reimplemented.

## Consequences

- Every MCP call shape in this repository follows the official SDK, so house
  reference code cannot be copied literally.
- Conformance results are tied to the pinned pair. A baseline entry may record a
  verified SDK limitation temporarily, but CI fails on a new failure and on a
  stale expected-failure entry.
- Changing the pin later needs an amendment here plus a conformance re-run.
