# ADR 0006 — Deliberate compatibility breaks

## Status

Open, and permanently open. This ADR is a running register. Every intentional
break from the pinned Taxuspt contract gets an entry here at the moment it is
made.

## Context

The pinned Taxuspt commit defines the public tool surface: names, descriptions,
input schemas, output shapes, and filtering behavior. Requirement precedence puts
credential and tenant security above that contract, so some breaks are expected.

Known break classes, from the brief:

- no `login_to_garmin`, `submit_credentials`, or MFA MCP tool exists, because MCP
  arguments reach model context, transcripts, telemetry, and tool logs;
- no tool accepts `user_id`, email, token path, or an account selector, so the
  single-global-client architecture cannot be reproduced;
- write and destructive tools are off by default remotely and need the
  intersection of operator enablement and granted scope;
- destructive tools fail closed when elicitation confirmation cannot be obtained,
  where the house behavior degrades gracefully;
- remote tools cannot write an arbitrary server filesystem path, so path-taking
  download tools return a bounded resource, blob, or short-lived principal-bound
  handle instead.

## Decision

Record each break as a row in the register below, and mirror it in
`docs/parity.md`. Silent breaks are prohibited. Compatibility aliases may be kept
for upstream misspellings or legacy names, but each alias is documented.

Every entry states the upstream contract, what changed, the security reason, the
precedence rule that justifies it, and the client-visible effect.

### Register

No entry yet. Entries are added by the phase that makes the break.

| Upstream contract | Change | Reason | Precedence rule | Client-visible effect |
|-------------------|--------|--------|-----------------|-----------------------|
| *(none recorded)* | | | | |

## Consequences

- `docs/parity.md` and this register must agree. A mismatch is a release blocker.
- An exclusion that is not a security-driven break stays release-blocking until a
  maintainer approves it, with evidence.
- Placeholder and `not implemented` handlers are never counted as parity, and are
  not compatibility breaks either.
