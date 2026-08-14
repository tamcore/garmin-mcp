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
- [ ] Cobra command tree in `internal/cmd` (`serve`, `auth`, `doctor`, `tools`,
      `migrate`, `version`); `cmd/garmin-mcp` is still a bare main.
- [ ] Official Go SDK and MCP spec version pinned; ADR 0002 decided.

### Known gaps carried out of phase 1

These are deliberate and tracked, not silently dropped:

- CI has no fuzz smoke job, no MCP conformance job, and no two-clean-build
  reproducibility check. There are no fuzz targets, no MCP server, and no
  reproducibility harness to point them at yet. Wire each one with the
  subsystem that creates it.
- Release signing covers the checksum file only. Container image signing and
  SBOM attestation are configured but unverified, because the release workflow
  cannot run outside a tag.
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
- [ ] Native 0.3.8 login, MFA continuation, DI exchange, refresh with rotation,
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
- [ ] 0.3.2 to 0.3.8 behavior differences affecting those contracts are
      reconciled and recorded; unrelated 0.3.8 additions are in the documented
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

Pin the official `modelcontextprotocol/go-sdk` stable release and its MCP
specification date, decide ADR 0002, and write `docs/mcp-version-matrix.md`.
The first failing test to write: `internal/cmd` asserting that `garmin-mcp
version` prints the ldflags-injected version and commit, which forces the Cobra
command tree into existence.
