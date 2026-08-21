# ADR 0008 — stdio operator authority

## Status

Accepted, 2026-08-21.

## Context

The write and destructive enablement flags applied to both transports, but stdio
could never use them. Policy also required an OAuth scope, and a process-local
stdio client has no bearer token or OAuth caller from which such a scope could
come. `tools list` reported the tiers enabled while `tools/list` removed every
tool in them.

Treating local enablement as a synthetic OAuth grant would make
`server_info.grantedScopes` report a credential that does not exist. Bypassing
scope checks on `ModeLocal` alone would make one wrong mode value sufficient to
open a remote listener.

## Decision

Local stdio operator enablement authorizes its tier. Streamable HTTP keeps the
intersection of operator enablement and the verified caller's matching OAuth
scope.

The local composition root supplies a separate internal operator-authority bit.
Policy construction accepts it only in local mode and only without a scope
source. Name allowlists and denylists run before this authority. Destructive
tools still require MCP elicitation confirmation and stay hidden when the client
does not declare that capability.

No OAuth scope is synthesized. Local `server_info.grantedScopes` remains empty;
its effective tiers and visible tool count report what the local client can use.

## Consequences

- `serve --enable-write-tools` can change a Garmin account over stdio without a
  second confirmation step. Operators can narrow that exposure with the tool
  allowlist, denylist, and safety delay.
- `serve --enable-write-tools --enable-destructive-tools` exposes destructive
  tools only to an elicitation-capable client, and each call still fails closed
  without explicit confirmation.
- Remote authorization and tenant isolation are unchanged.
- The opt-in behavior change belongs in the next minor release, `v0.1.0`.
