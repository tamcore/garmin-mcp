# Implementation status

This file is the resume point. A cold agent run resumes from `AGENTS.md` plus
this file alone. If those two files are not sufficient, fix that before any
further feature work.

Every stopping point updates this file in the same commit as the work it
describes. Never mark an item done on the strength of a placeholder or
`not implemented` handler.

Last updated: 2026-08-14.

## Phase status

| Phase | State |
|-------|-------|
| 0 — native login feasibility gate | **CLOSED — GO** (see `docs/adr/0001-garmin-login-feasibility.md`) |
| 1 — inventory, docs skeleton, and CI | **IN PROGRESS** |
| 2 — core auth and storage (M1) | not started |
| 3 — MCP foundation (M1) | not started |
| 4 — remote multi-user (M2) | not started |
| 5 — compatibility breadth (M3) | not started |
| 6 — hardening and release | not started |

Phase definitions are in `docs/phases.md`.

### Phase 1 detail

- [x] Documentation skeleton: `AGENTS.md` with the committed `CLAUDE.md`
      symlink, `docs/phases.md`, this file, `docs/upstream-pins.md`,
      `docs/threat-model.md`, and the ADR stubs.
- [x] Upstream tool inventory by static source extraction at the pinned Taxuspt
      commit; `compat/tools.json`, `compat/resources.json`, `docs/parity.md`.
      Measured 138 tools and 5 resources by two independent methods.
- [x] Repository skeleton: `go.mod` with the pinned Go directive, golangci-lint
      v2 config, `.pre-commit-config.yaml`, `.goreleaser.yaml`, `Dockerfile`.
- [x] Hardened GitHub Actions workflows with SHA-pinned actions.
- [x] Scripted fake Garmin service in `internal/testkit`, plus
      `internal/garmin/protocol` with the endpoint/identity constants and the
      login failure classifier.
- [x] Upstream `python-garminconnect` baseline re-pinned from 0.3.8 to 0.3.10 on
      2026-08-14. The reconciliation window is now 0.3.2 to 0.3.10, and the
      security behaviors that 0.3.10 adds are now required work. See
      `docs/upstream-pins.md`.
- [ ] Cobra command tree in `internal/cmd` (`serve`, `auth`, `doctor`, `tools`,
      `migrate`, `version`); `cmd/garmin-mcp` is still a bare main.
- [ ] Official Go SDK and MCP spec version pinned; ADR 0002 decided.

### Known gaps carried out of phase 1

These are deliberate and tracked, not silently dropped:

- CI has no fuzz smoke job and no MCP conformance job. There are no fuzz targets
  and no MCP server to point them at yet. Wire each one with the subsystem that
  creates it.
- Release supply-chain coverage is narrower than the brief asks for.
  `.goreleaser.yaml` signs the **checksum file only** (`artifacts: checksum`,
  keyless cosign `sign-blob`) and emits **archive SBOMs only**
  (`sboms: artifacts: archive`). There is no image signing block, no container
  image SBOM, no per-binary SBOM, and no build provenance attestation. There is
  also no two-clean-build reproducibility check anywhere in CI. None of this is
  "configured but unverified": it is not configured.
- Coverage thresholds are not enforced. The CI unit job writes
  `cover.out` with `-covermode=atomic`, but no job asserts a minimum
  percentage, so the documented 80% rule is unenforced.
- The `test-fakegarmin` and `e2e` CI jobs pass vacuously. No file carries the
  `fakegarmin` tag, and the only `e2e`-tagged file is `e2e/doc.go`, which holds
  no tests. Both jobs must be changed to **fail when the expected suite is
  absent**, so an empty suite can never read as a pass.
- The widget MFA classifier is incomplete. `ClassifyWidgetLogin` decides from the
  HTTP status and the page title only. It does not parse the MFA variables
  embedded in the widget page, and it reports the delivery method as the
  hardcoded `MFAMethodEmail` default. There is no outcome distinct from
  `OutcomeInvalidCredentials` for a rejected OTP, so a wrong code and a wrong
  password cannot be told apart. No path or endpoint label models the explicit
  MFA code request, so requirement 9 in `docs/upstream-pins.md` has nothing to
  build on.
- The 0.3.8 to 0.3.10 security behaviors are not implemented. See the required
  list in `docs/upstream-pins.md`: host allowlist enforcement, sanitized
  exception messages, login-error query redaction, symlink-rejecting token paths
  with full ancestry checks, serialized refresh with atomic writes, JWT `exp`
  validation and unsigned-payload rejection, server-driven pagination caps,
  per-transaction pending MFA state, explicit widget MFA code delivery, and
  segment-aware path-traversal guards.
- GitHub-native secret scanning and dependency/license review are repository
  settings, not workflow files. They still need enabling.
- The parity extractor scripts are not committed; `docs/parity.md` documents the
  algorithm instead. A Go regenerator that fails CI on manifest drift is
  deferred.

Live Garmin validation: `not run — credentials unavailable` for anything beyond
the phase-0 gate already recorded in ADR 0001.

## Invariants (true at every tag, no exceptions)

- The repository builds and runs with no Python/Garth runtime or subprocess.
- Garmin tokens and sensitive identity fields are encrypted at rest; secrets
  never appear in logs, metrics, traces, errors, tool results, or the handoff.
- Every Garmin client, token set, cookie jar, cache entry, and tool result
  belongs to exactly one principal. No global cross-user client exists.
- `go test -race -count=1 ./...`, `go vet ./...`, `golangci-lint run`,
  `govulncheck ./...`, and `go build ./...` pass.
- `goreleaser check` and a snapshot release succeed; the container image builds
  and passes a non-root/read-only smoke test.
- `docs/implementation-status.md` matches reality, and `git status --short` is
  clean.
- No placeholder or `not implemented` handler is counted as working behavior.

## M1 — local single-user stdio server

- [x] The phase-0 login gate is closed with a recorded outcome (go, or no-go
      with the selected transport implemented and reviewed). — GO, ADR 0001.
- [ ] Native 0.3.10 login, MFA continuation, DI exchange, refresh with rotation,
      `.com`/`.cn` host selection, and the full failure classification pass
      against the fake Garmin service.
- [ ] `garmin-mcp serve --transport=stdio` binds exactly one principal from
      process-local configuration, rejects ambiguous multi-account
      configuration, and keeps stdout reserved for MCP frames.
- [ ] Tokens are stored owner-only and encrypted; hostile-umask, symlink,
      atomic-write, and platform-ACL tests pass.
- [ ] `garmin-mcp auth` completes the one-shot loopback browser login and MFA
      flow, plus the explicit TTY fallback.
- [ ] At least one representative read-only tool per major Garmin payload style
      is registered with accurate annotations, strict schemas, bounded results,
      and sanitized errors, and each has name/schema snapshot tests.
- [ ] Refresh singleflight, rotating-token CAS, and cache-invalidation tests
      pass under the race detector.
- [ ] CI, cross-platform builds, and the release pipeline are green.

## M2 — remote multi-user server

- [ ] Streamable HTTP resolves the principal only from a verified bearer token,
      on every applicable `POST`, `GET`, and `DELETE`; no `user_id`, email,
      token path, or account selector is ever a tool argument.
- [ ] Protected Resource Metadata, the RFC 6750 challenge, authorization-server
      metadata, PKCE S256, resource indicators, exact issuer/redirect matching,
      and per-client consent behave as specified.
- [ ] Transaction-gated browser login and MFA work end to end against the fake
      Garmin service; no credential-entry MCP tool exists.
- [ ] Encrypted per-principal Garmin tokens, per-client consent records, hashed
      opaque MCP token material, and transactional revocation/unlink all persist
      and cascade correctly, failing closed on partial deletion.
- [ ] Cross-user isolation and concurrent refresh pass under
      `go test -race -count=1`; session/event identifiers provably cannot
      authenticate, resume, read, or delete another principal's or client's
      data.
- [ ] The OAuth negative matrix, rate limits, security headers, cookie
      attributes, request-size limits, redaction, and encrypted-store tamper
      tests pass.
- [ ] Write and destructive tools are off by default remotely and require both a
      granted scope and operator enablement; destructive actions fail closed
      when confirmation cannot be obtained.
- [ ] The selected MCP server conformance suite passes with no unexplained
      baseline entry.
- [ ] Documentation covers remote deployment, reverse proxy/TLS, security
      assumptions, backup/restore, migrations, and key rotation.

## M3 — full Taxuspt parity

- [ ] The generated parity matrix accounts for every tool and resource at the
      pinned Taxuspt commit.
- [ ] Every required contract has passing name/schema/behavior tests, or a
      documented exclusion with evidence that remains release-blocking until a
      maintainer approves it.
- [ ] 0.3.2 to 0.3.10 behavior differences affecting those contracts are
      reconciled and recorded; unrelated 0.3.10 additions are in the documented
      backlog.

## Commands to run and report at every milestone

```sh
go test -race -count=1 ./...
go vet ./...
golangci-lint run
govulncheck ./...
go build ./...
goreleaser check
goreleaser release --snapshot --clean
```

Plus the pinned MCP conformance command and the container and E2E targets.

## Known gaps

- The official Go SDK version and the MCP specification date are not pinned yet.
  See `docs/upstream-pins.md` and ADR 0002.
- Live `MFA_REQUIRED` continuation is unproven against the real service. It must
  be covered by fake-service tests. See ADR 0001.
- Phase-0 evidence comes from one residential source IP. Datacenter and CI
  egress may be scored differently by Cloudflare. See ADR 0001.

## Next task

Audit the ten required 0.3.8-to-0.3.10 security behaviors listed in
`docs/upstream-pins.md` against the pinned 0.3.10 source, then implement them in
the auth/session slice, and in parallel pin the official
`modelcontextprotocol/go-sdk` stable release with its MCP specification date and
decide ADR 0002.

First failing tests to write, in this order:

1. `internal/garmin/protocol` asserting that a host outside the `garmin.com` and
   `garmin.cn` allowlist is rejected before a request URL is built.
2. `internal/garmin/protocol` asserting a rejected OTP classifies to its own
   outcome, distinct from invalid credentials.
3. `internal/cmd` asserting that `garmin-mcp version` prints the
   ldflags-injected version and commit, which forces the Cobra command tree into
   existence.
