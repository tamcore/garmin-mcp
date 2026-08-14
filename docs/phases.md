# Implementation phases

This file defines the order of work. `docs/implementation-status.md` records
where the work currently stands.

Rules that apply to every phase:

- Keep every phase green and reviewable. Stop only at a green checkpoint.
- Update `docs/implementation-status.md` in the same commit as the work it
  describes.
- Never inflate progress with stub handlers. A placeholder handler is not
  progress.
- Tenant isolation, token encryption, and authorization are architectural
  foundations. They are never deferred behind broad tool implementation.
- Missing live Garmin credentials never block fake-service, contract, auth, or
  MCP work. Record live status as `not run — credentials unavailable`.

## Phase 0 — native login feasibility gate (blocking)

The project depends on one assumption: a native Go HTTP client can complete a
Garmin login. Phase 0 settles it before phases 2, 4, and 5 have any value.

Scope: the `LoginTransport` interface, a plain `net/http` implementation, the
mobile-iOS/widget/portal request shapes from the pinned source, the failure
classifier, and an opt-in live command/test behind the `garminlive` tag with an
explicit environment acknowledgement and a dedicated non-primary account.
Nothing else.

Outcome: **closed, GO**, recorded in
`docs/adr/0001-garmin-login-feasibility.md`. The gate **must become**
re-runnable as an opt-in `garminlive` check for drift detection. No `garminlive`
command and no tagged test exists yet, so the gate is a one-time result and not a
repeatable check; ADR 0001 records that as required work. When the check exists
and later fails, re-enter the decision in ADR 0001.

## Phase 1 — inventory, docs skeleton, and CI

- Pin the upstream sources in `docs/upstream-pins.md`.
- Generate the parity manifest: `compat/tools.json`, `compat/resources.json`,
  and `docs/parity.md`, by static source extraction at the pinned Taxuspt
  commit. Do not trust the upstream README tool count.
- Establish the repository skeleton, golangci-lint v2 config, pre-commit hooks,
  and the hardened GitHub Actions workflows.
- Build the scripted fake Garmin service in `internal/testkit`.
- Write `docs/threat-model.md`.
- Materialize the brief into repository documentation so a cold run can resume:
  `AGENTS.md` plus the committed `CLAUDE.md` symlink,
  `docs/implementation-status.md` carrying the M1/M2/M3 checklists and the exact
  next task, the ADR stubs, and this file.

A cold agent run must be able to resume from `AGENTS.md` plus
`docs/implementation-status.md` alone. If it cannot, that is a bug to fix before
any further feature work.

## Phase 2 — core auth and storage (M1)

Native login state machine, MFA continuation, DI token exchange and refresh with
rotation, encrypted token store, local `garmin-mcp auth`, redaction, and
concurrency tests.

## Phase 3 — MCP foundation (M1)

The official Go SDK, stdio transport, request-to-principal context, tool policy
and tiering, and one representative read-only tool for each major Garmin payload
style.

## Phase 4 — remote multi-user (M2)

Protected Resource Metadata, the OAuth authorization server and resource server,
per-client consent, on-demand browser login pages, Streamable HTTP, revocation
and unlink cascades, tenant isolation, and the conformance plus security E2E
suites.

## Phase 5 — compatibility breadth (M3)

Port the remaining Taxuspt tools and resources domain by domain. Contract tests
come before handlers. Reconcile the 0.3.2 to 0.3.10 behavior changes that affect
those contracts and backlog the unrelated additions.

## Phase 6 — hardening and release

Fuzzing, layered limits, operational endpoints, documentation, container image,
GoReleaser, signing, SBOM, provenance, upgrade tests, and the final security
review. Hardening named by a milestone gate belongs to that milestone, not here.

## Milestone mapping

| Milestone | Phases | Deliverable |
|-----------|--------|-------------|
| M1 | 0, 1, 2, 3 | Local single-user stdio server |
| M2 | 4 | Remote multi-user server |
| M3 | 5, 6 | Full Taxuspt parity, hardened release |

Each milestone is independently releasable and tagged. M1 and M2 are not gated
on tool parity. No M1 or M2 security property may be deferred into a later
milestone: scope moves forward, security does not.
