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
| 1 — inventory, docs skeleton, and CI | **CLOSED** with the recorded gaps below |
| 2 — core auth and storage (M1) | **IN PROGRESS** — `internal/config`, `internal/cmd`, `internal/garmin/auth`, `internal/store`, `internal/cryptostore` landed |
| 3 — MCP foundation (M1) | not started — this is the next slice |
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
- [x] Cobra command tree in `internal/cmd` (`serve`, `auth`, `doctor`,
      `tools list`, `migrate`, `version`). `cmd/garmin-mcp/main.go` is now a thin
      main that calls `cmd.Execute` and exits with its code. Only `version` and
      root `--version` do real work; the other commands validate configuration
      and then fail with `*cmd.NotImplementedError` wrapping
      `cmd.ErrNotImplemented`, exit code 1. That is a declared gap, not working
      behavior. Coverage 96.4%.
- [x] First real module requirements: `spf13/cobra` v1.10.2,
      `spf13/viper` v1.21.0, `spf13/pflag` v1.0.10. Rationale, licenses,
      maintenance status, and the indirect set are recorded in
      `docs/dependencies.md`.
- [x] Official Go SDK and MCP spec version pinned; ADR 0002 decided. Pinned
      `modelcontextprotocol/go-sdk` `v1.7.0` (stable, released 2026-07-28,
      commit `bc72835f62eb94d0fb484439f886b6885b075f36`) with MCP specification
      `2026-07-28`. ADR 0002 is Accepted, `docs/mcp-version-matrix.md` records
      the per-feature obligations, and `docs/upstream-pins.md` carries the pins.
      The module line itself lands with the MCP foundation slice, because
      `go mod tidy` would drop an unimported requirement.

### Phase 2 detail

Coverage figures are statement coverage from `go test -cover`.

- [x] `internal/config`. `Config` with deterministic precedence (a flag the
      operator changed, then `GARMIN_MCP_*` environment, then the configuration
      file, then the default), `_FILE` variants for the two secret settings
      (`master-key` and `garmin-tokens`) with both-set rejection, full lexical
      validation before anything binds or opens, region validated through
      `protocol.ParseDomain` rather than a reimplemented allowlist, and
      `String`/`GoString`/`MarshalJSON`/`slog.LogValuer` redaction on both
      `Secret` and `Config`. There is no password, MFA, email, or
      account-selector field, and two reflective guard tests keep it that way.
      Secret settings have no flag at all, so they cannot appear in a process
      listing. Coverage 94.4%.
- [x] `internal/garmin/auth` login state machine. Seven explicit states
      (`created`, `credentials_submitted`, `mfa_pending`, `authenticated`,
      `failed`, `expired`, `cancelled`) and seven transitions, with all 49
      state/transition pairs asserted by `TestMachineTransitionMatrix` against an
      independent permission table. The machine is an immutable value type and
      rejections carry a `*TransitionError` matching `ErrInvalidTransition`.
- [x] Strategy fallback `mobile_ios` -> `sso_widget` -> `portal`, driven by
      `protocol.Outcome.StopsFallback()`. A success whose DI exchange or session
      validation fails also falls through, and is not stored. Anti-WAF pacing is
      injected, and the default jitter source fails slow.
- [x] Bounded MFA transaction registry. A 256-bit capability from `crypto/rand`,
      stored only as its SHA-256 (no field holds the raw value), a 5-minute
      absolute TTL that is never extended, a 5-attempt budget charged before the
      principal check, single-use terminal completion under one mutex, and a
      1024-entry cap. Interleaving is impossible by construction: there is no
      per-client "current login" field, each `Attempt` returns an immutable
      deep-copied `Pending` value, and each login and continuation builds its own
      session and cookie jar. Proven by 16-way concurrent isolation and
      single-winner completion tests under `-race`.
- [x] DI ticket exchange over the pinned candidate client IDs from
      `protocol.DIClientIDs()`, in order, with per-candidate HTTP Basic identity
      and an early stop on rate limiting. Session validation runs before a token
      set is accepted or saved.
- [x] Refresh with a 15-minute default safety window, per-principal collapsing of
      concurrent refreshes, and CAS save that yields to a newer stored token set
      on conflict. Different principals do not serialize. All asserted under
      `-race`.
- [x] Unverified-JWT `exp` parsing that rejects `alg:none` (case-folded),
      missing or empty signatures, boolean, string, object and null `exp`,
      non-finite and overflowing values, and oversized tokens and segments. It is
      used for expiry only and never for authorization.
- [x] `internal/store` `FileStore`: encrypted records, atomic write through a
      random-suffixed temp sibling with `fsync` and directory sync, `0600` files
      in a `0700` directory enforced by explicit `chmod` and re-checked on read,
      symlink rejection across the full path ancestry with `O_NOFOLLOW` on unix,
      `~user` refusal, structure-based legacy `garmin_tokens.json` detection with
      import and export, inline token JSON refused unless explicitly enabled, and
      per-principal CAS versioning. `TestRecordOnDiskHoldsNoPlaintextToken`
      proves the principal is absent from the file bytes as well as the tokens.
      Hostile umask is proven in a re-exec subprocess by
      `TestHostileUmaskIsIgnored`, with the parent independently re-verifying the
      artifacts so a skipped child cannot pass. Coverage 87.1%.
- [x] `internal/cryptostore`: five exported functions (`GenerateKey`, `LoadKey`,
      `LoadOrCreateKey`, `Encrypt`, `Decrypt`) pinned by an AST-based surface
      test, AES-256-GCM with `crypto/rand` nonces, a versioned key ID in the
      envelope header that is also inside the AAD, AAD binding the principal and
      the record type with length prefixes so adjacent fields cannot be confused,
      an owner-only key file, and staged rotation proven end to end by
      `TestStagedRotationReencryptsRecords`. Tampering, wrong key of the same
      version, wrong principal, and wrong record type all collapse to
      `ErrAuthentication` on purpose. Coverage 83.8%.

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
- The `test-fakegarmin` job is **no longer vacuous**. `internal/garmin/auth`
  carries 23 tagged test functions across `login_fakegarmin_test.go` and
  `mfa_fakegarmin_test.go`, plus a tagged harness file, which is 29 of the 113
  test runs the package reports under the tag. Package coverage is 85.1% with
  `-tags=fakegarmin` against 55.2% untagged. The `e2e` job is **no longer
  vacuous** either: `e2e/cli_test.go` carries three passing tests
  (`TestVersionReportsTheInjectedBuildInfo`,
  `TestStdioTransportKeepsStdoutClean`,
  `TestUnknownCommandFailsWithoutTouchingStdout`) that build the binary and drive
  it as a subprocess. What is **still missing** is the guard: both jobs would
  pass again if every tagged file were deleted. Each must be made to **fail when
  the expected suite is absent**, so an empty suite can never read as a pass.
  Until that guard exists, the green tick on these jobs proves the tests that ran,
  not that any tests ran.
- The widget MFA path is still incomplete. `ClassifyWidgetLogin` decides from the
  HTTP status and the page title only. The inline-JS variables embedded in the
  widget page (`customerGuid`, `mfaMethod`, `locale`, `clientId`, `codeSentTo`)
  are **not parsed**, so the delivery method stays the hardcoded default. Because
  of that, the explicit email/SMS code-delivery request is **not sent**: the wire
  constants exist (`PathWidgetRequestMFACode`, `EndpointWidgetRequestMFACode`,
  `Hosts.WidgetRequestMFACodeURL`) and nothing calls them, so requirement 9 in
  `docs/upstream-pins.md` still has a constant and no behavior. What did land is
  honest signalling of the uncertainty: `Pending.MFADeliveryUncertain()` and
  `Result.MFADeliveryUncertain()` surface it to the caller. There is still no
  outcome distinct from `OutcomeInvalidCredentials` for a rejected OTP, so a
  wrong code and a wrong password cannot be told apart.
- The 0.3.8 to 0.3.10 security behaviors are now **mostly implemented**, not
  mostly missing. Landed: host allowlist enforcement through
  `protocol.ValidatedDomain`, sanitized exception messages, login-error query
  redaction, symlink-rejecting token paths with full ancestry checks, atomic
  writes with owner-only modes, refresh serialized per principal with CAS, JWT
  `exp` validation with unsigned-payload and `alg:none` rejection, and
  per-transaction pending MFA state in a bounded registry. Still missing:
  explicit widget MFA code delivery (above), server-driven pagination caps, and
  segment-aware path-traversal guards. See `docs/upstream-pins.md` for the
  numbered list.
- The `JWT_WEB` cookie fallback after a failed DI exchange is not implemented.
  Upstream consumes the CAS ticket through the web front end when the DI
  exchange fails; this project requires the DI token set and reports
  `ErrTokenExchangeFailed` instead. That is a deliberate narrowing, and it means
  a Garmin-side DI change becomes a hard login failure rather than a degraded
  one.
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
      Done: login, MFA continuation, DI exchange over the candidate client IDs,
      session validation, refresh with rotation and CAS, host selection through
      `protocol.ParseDomain`, and the fallback classification, all against the
      fake service under `-tags=fakegarmin`. Not done: explicit widget MFA code
      delivery, a distinct rejected-OTP outcome, and the `JWT_WEB` fallback. The
      item stays unchecked until those close.
- [ ] `garmin-mcp serve --transport=stdio` binds exactly one principal from
      process-local configuration, rejects ambiguous multi-account
      configuration, and keeps stdout reserved for MCP frames.
      Done: `serve` loads and validates configuration first, and stdout is
      asserted byte-empty on every failure path including the flag-parse path.
      `Config` has no account selector, so ambiguous multi-account configuration
      is unrepresentable. Not done: everything after validation — there is no
      server, no transport, and no principal binding.
- [ ] Tokens are stored owner-only and encrypted; hostile-umask, symlink,
      atomic-write, and platform-ACL tests pass.
      Done: encryption at rest, `0600` in `0700`, atomic writes, full-ancestry
      symlink rejection, and the subprocess hostile-umask test. Not done: the
      platform-ACL test never runs anywhere, so the Windows behavior is
      unverified. See the gap below.
- [ ] `garmin-mcp auth` completes the one-shot loopback browser login and MFA
      flow, plus the explicit TTY fallback.
      `auth` is a declared gap that validates configuration and fails. The
      underlying MFA transaction registry it will drive does exist.
- [ ] At least one representative read-only tool per major Garmin payload style
      is registered with accurate annotations, strict schemas, bounded results,
      and sanitized errors, and each has name/schema snapshot tests.
- [ ] Refresh singleflight, rotating-token CAS, and cache-invalidation tests
      pass under the race detector.
      Done: per-principal collapsing of concurrent refreshes and rotating-token
      CAS both pass under `-race`. The collapsing is hand-rolled from
      `sync.Mutex` plus a per-principal in-flight map with a done channel; there
      is no `singleflight` package involved, and `golang.org/x/sync` is not a
      dependency. Not done: cache invalidation, because no cache exists.
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
go test -race -count=1 -tags=fakegarmin ./...
go test -tags=e2e -timeout=10m ./e2e/...
go vet ./...
golangci-lint run
govulncheck ./...
go build ./...
goreleaser check
goreleaser release --snapshot --clean
```

Both tagged suites now hold real tests, so report their results, not just their
exit status. Plus the pinned MCP conformance command and the container target.

## Known gaps

- The official Go SDK `v1.7.0` and MCP specification `2026-07-28` are pinned in
  `docs/upstream-pins.md` and decided in ADR 0002, but the module requirement is
  not in `go.mod` yet. It lands with the MCP foundation slice, because
  `go mod tidy` drops an unimported requirement and CI fails on a dirty tidy
  diff. No MCP server code exists yet.
- Live `MFA_REQUIRED` continuation is unproven against the real service. It must
  be covered by fake-service tests. See ADR 0001.
- Phase-0 evidence comes from one residential source IP. Datacenter and CI
  egress may be scored differently by Cloudflare. See ADR 0001.

### Gaps from the auth and storage slice

- `*store.FileStore` does **not** satisfy `auth.TokenStore`. The two interfaces
  are method-for-method identical but use `store.TokenSet` and `auth.TokenSet`
  respectively, so the types do not unify. The compile-time assertion in
  `internal/store/interface_test.go` is against a **local copy** of the consumer
  interface declared in that test file, not against `auth.TokenStore`. The
  adapter that will bridge them exists only as a comment in
  `internal/store/tokens.go`. Nothing wires the store to the authenticator yet,
  so this has not broken anything, and it must be closed before it can.
- Windows ACL enforcement in `internal/store` and `internal/cryptostore` uses
  `icacls` and is **unverified at runtime**. Windows-only tests exist and would
  assert that every ACE names the current user, but CI runs tests on Linux only
  and the Windows matrix entry runs `go build ./...`, which does not compile
  `_test.go` files. `go vet` runs with the host `GOOS`, so the `_windows.go`
  files are never type-checked in CI either. The claim that can be made today is
  "compiles under `GOOS=windows`", nothing stronger.
- The OS keyring backends in `internal/cryptostore` (`keyring_darwin.go`,
  `keyring_linux.go`, `keyring_other.go`) are cgo-free **no-ops** — deliberate
  placeholders, not working backends. All three return an unsupported error and
  report unavailable, which keeps `CGO_ENABLED=0` cross-compilation working per
  ADR 0005. The owner-only key file is the only real backend.
- Roughly 130 to 150 lines of filesystem-hardening helpers are **duplicated**
  between `internal/store` and `internal/cryptostore`:
  `checkNoSymlinkAncestry`, `readBounded`, `writeFileAtomically`,
  `writeTempFile`, `syncDir`, `openNoFollow`, `createExclusiveNoFollow`, the
  owner-restriction and `checkOwnerOnly` helpers including the verbatim `icacls`
  invocation, and the `presence` redaction helper. The duplication is not marked
  intentional anywhere, and it has already drifted: only the `store` copy of
  `writeFileAtomically` re-checks symlink ancestry internally. Extract these into
  a shared `internal/` package.
- Symlink checking is strict enough to refuse a store or key directory reached
  **through** a symlinked path, with no `EvalSymlinks` normalization and no
  same-tree exemption. On macOS that refuses anything under `/var`, `/tmp`, or
  `/etc`, because `/var` is a symlink to `/private/var`. Both test suites work
  around it by calling `filepath.EvalSymlinks(t.TempDir())`. Callers must pass
  already-resolved paths, and `ResolveStoreDir` offers no opt-out. Decide whether
  that is the final contract and document it for operators, or normalize before
  checking.
- Cross-process CAS is **not** provided. `FileStore.Save` compares the version
  under a per-principal in-process mutex, with no file locking anywhere, so two
  processes sharing a directory could both pass their version check before
  either renames. The limitation is stated in the source. The race tests are
  goroutine-level only.
- Key rotation has no store-level driver. `internal/cryptostore` proves staged
  rotation end to end, but `FileStore` holds exactly one key and there is no
  loop that re-seals existing records, so rotation is a library capability, not
  an operator procedure yet.
- Inline token JSON has no caller. `ParseInlineTokenJSON` takes the
  allow-insecure boolean as a parameter and `FileStore.AllowsInlineTokens()`
  exposes the configured value, but nothing connects them.
- "Refuse to start in remote mode on bad key material" is **not wired**.
  `internal/cryptostore` returns distinguishable sentinels
  (`ErrKeyNotFound`, `ErrMalformedKey`, `ErrInsecureKeyPermissions`,
  `ErrInsecureKeyPath`, `ErrInvalidKeyVersion`) and `NewFileStore` rejects an
  unusable key, but the startup decision belongs to `internal/config` or
  `internal/cmd` and neither implements it. ADR 0005 requires it.
- No effective-configuration printer is wired to a command, because `doctor` is
  a declared gap. `Config` already redacts itself through `String`, `GoString`,
  `MarshalJSON`, and `LogValue`, so the printer has nothing left to invent.
- The login state machine is not on the login path. `auth.Machine` is fully
  specified and exhaustively tested, but `Authenticator.Login` and `CompleteMFA`
  report progress through `Result` states; the machine is stepped only by the MFA
  transaction registry. Either route the top-level flow through it or state
  plainly that it governs transactions only.
- OAuth client, redirect URI, registration policy, and scope settings are
  deliberately **absent** from `Config` until the M2 subsystem exists, so a
  configuration file cannot claim a protection that nothing enforces. Add them
  with the subsystem, not before.
- Two `govulncheck` advisories stand against versions Viper pins:
  `GO-2026-5970` in `golang.org/x/text` v0.28.0 and `GO-2026-5024` in
  `golang.org/x/sys` v0.29.0. Neither is on a called path, so the gate is green.
  Both are fixable by an explicit bump in `go.mod`. See `docs/dependencies.md`.

## Next task

The MCP foundation slice. Configuration, the command tree, auth, the token
store, and encryption all exist; nothing MCP-facing does. This slice adds the
pinned `modelcontextprotocol/go-sdk` `v1.7.0` to `go.mod` in the same commit as
the first code that imports it, stands up the stdio server behind
`garmin-mcp serve --transport=stdio` so the command stops returning
`ErrNotImplemented`, resolves the request context to a principal, enforces the
tool policy before any Garmin call, and registers one representative read-only
tool per major Garmin payload style. The SDK and specification pins are settled,
so no version research remains.

Scope, in order:

1. Add the SDK requirement and the stdio server. Stdout carries MCP frames only;
   stderr carries the `slog` logger, which does not exist yet and must be
   created here — `Config.LogLevel` and `Config.LogFormat` are already parsed and
   validated and nothing reads them.
2. Request-to-principal context. On stdio there is exactly one principal, from
   process-local configuration. No `user_id`, email, token path, or account
   selector is ever a tool argument, and `Config` already makes that
   unrepresentable.
3. Close the store-to-auth seam: the `store.TokenSet` to `auth.TokenSet` adapter,
   so `serve` can hand the authenticator a real `auth.TokenStore`.
4. Tool policy. Read-only tools always register. Write and destructive tools need
   the intersection of operator enablement and granted scope, and both name lists
   are validated at startup against the registered set.
5. One representative read-only tool per major payload style, each with all four
   annotation hints declared, a strict schema, bounded results, sanitized errors,
   and name and schema snapshot tests against `compat/tools.json`.

First failing test to write:

`internal/mcpserver`, `TestServeStdioAnswersInitializeWithoutWritingLogsToStdout`
— drive `garmin-mcp serve --transport=stdio` over an in-memory transport pair,
send one `initialize` request, and assert three things: the response is a
well-formed MCP `initialize` result naming the pinned protocol version, stdout
holds nothing but that frame, and the log output captured from stderr is
non-empty. It fails today because `internal/mcpserver` does not exist and
`serve` returns `ErrNotImplemented`, and it forces the SDK requirement, the
stdio wiring, and the stdout/stderr split into existence together.

Then, in order: `TestToolPolicyRegistersReadOnlyToolsAndWithholdsWriteTools`,
`TestStoreAdapterSatisfiesAuthTokenStore` (a compile-time assertion against the
real `auth.TokenStore`, not a local copy), and the first tool's name and schema
snapshot test.
