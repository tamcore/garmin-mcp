# Implementation status

This file is the resume point. A cold agent run resumes from `AGENTS.md` plus
this file alone. If those two files are not sufficient, fix that before any
further feature work.

Every stopping point updates this file in the same commit as the work it
describes. Never mark an item done on the strength of a placeholder or
`not implemented` handler.

Last updated: 2026-08-15.

## Phase status

| Phase | State |
|-------|-------|
| 0 — native login feasibility gate | **CLOSED — GO** (see `docs/adr/0001-garmin-login-feasibility.md`) |
| 1 — inventory, docs skeleton, and CI | **CLOSED** with the recorded gaps below |
| 2 — core auth and storage (M1) | **CLOSED** |
| 3 — MCP foundation (M1) | **CLOSED** |
| 4 — remote multi-user (M2) | **CLOSED.** The MCP conformance requirement is **blocked upstream**, with evidence below, not outstanding work |
| 5 — compatibility breadth (M3) | **IN PROGRESS** — 45 of the 138 upstream tools are implemented, plus 5 tools the pinned manifest does not carry. No resource is implemented |
| 6 — hardening and release | not started |

Phase definitions are in `docs/phases.md`.

## Measured coverage

Statement coverage from `go test -count=1 -cover ./...`, measured on 2026-08-15.
The tagged column is the same command with `-tags=fakegarmin`; a blank cell means
the tag changes nothing.

| Package | Untagged | With `fakegarmin` |
|---------|----------|-------------------|
| `internal/cmd` | 74.3% | 77.4% |
| `internal/config` | 90.8% | |
| `internal/cryptostore` | 89.9% | |
| `internal/garmin/api` | 86.9% | |
| `internal/garmin/auth` | 67.3% | 88.2% |
| `internal/garmin/client` | 93.0% | 93.3% |
| `internal/garmin/protocol` | 96.7% | |
| `internal/identity` | 97.7% | |
| `internal/loginweb` | 82.6% | |
| `internal/mcplog` | 98.5% | |
| `internal/mcpserver` | 89.0% | |
| `internal/oauthserver` | 92.4% | |
| `internal/oauthstore` | 84.6% | |
| `internal/policy` | 91.7% | |
| `internal/ratelimit` | 95.7% | |
| `internal/securefile` | 84.5% | |
| `internal/store` | 83.8% | |
| `internal/testkit` | 96.8% | |
| `internal/tokenlink` | 80.0% | |
| `internal/tools` | 77.0% | 77.1% |
| `migrations` | 100.0% | |

Two packages sit under the 80% review rule: `internal/tools` at 77.0% and
`internal/cmd` at 74.3%. No CI job enforces a threshold, so this is a review
duty. Raise both with the next slice that touches them.

### Phase 1 detail

- [x] Documentation skeleton: `AGENTS.md` with the committed `CLAUDE.md`
      symlink, `docs/phases.md`, this file, `docs/upstream-pins.md`,
      `docs/threat-model.md`, and the ADRs.
- [x] Upstream tool inventory by static source extraction at the pinned Taxuspt
      commit; `compat/tools.json`, `compat/resources.json`, `docs/parity.md`.
      Measured 138 tools and 5 resources by two independent methods.
- [x] Repository skeleton: `go.mod` with the pinned Go directive, golangci-lint
      v2 config, `.pre-commit-config.yaml`, `.goreleaser.yaml`, `Dockerfile`.
- [x] Hardened GitHub Actions workflows with SHA-pinned actions, the SHA-pinned
      dependency review gate on pull requests, name-based tagged-suite guards on
      `test-fakegarmin` and `e2e`, `-race` on every Go test invocation, and
      distinct literal concurrency prefixes in `ci.yaml` and `release.yaml`.
- [x] Scripted fake Garmin service in `internal/testkit`, plus
      `internal/garmin/protocol` with the endpoint/identity constants and the
      login failure classifier.
- [x] Upstream `python-garminconnect` baseline re-pinned from 0.3.8 to 0.3.10.
      The reconciliation window is 0.3.2 to 0.3.10. See `docs/upstream-pins.md`.
- [x] Cobra command tree in `internal/cmd`. `serve`, `auth` and `doctor` now do
      real work; `migrate` and `tools list` are still declared gaps. See the gap
      list below.
- [x] Official Go SDK and MCP spec version pinned; ADR 0002 decided. The module
      requirement is in `go.mod` now.

### Phase 2 detail — core auth and storage

- [x] `internal/config`. Deterministic precedence (a flag the operator changed,
      then `GARMIN_MCP_*` environment, then the configuration file, then the
      default), `_FILE` variants for the secret settings with both-set
      rejection, full lexical validation before anything binds or opens, region
      validated through `protocol.ParseDomain`, and
      `String`/`GoString`/`MarshalJSON`/`slog.LogValuer` redaction. There is no
      password, MFA, email, or account-selector field, and two reflective guard
      tests keep it that way. It now also carries the OAuth client registry
      (`internal/config/oauthclient.go`): a bounded client count, exact redirect
      URIs, exact resource indicators, a scope list per client, and a secret
      **digest** supplied through a file rather than the secret itself.
- [x] `internal/garmin/auth` login state machine. Seven states and seven
      transitions, all 49 pairs asserted by `TestMachineTransitionMatrix`.
- [x] Strategy fallback `mobile_ios` -> `sso_widget` -> `portal`. A success whose
      DI exchange or session validation fails also falls through and is not
      stored.
- [x] Bounded MFA transaction registry. A 256-bit capability from `crypto/rand`
      stored only as its SHA-256, a 5-minute absolute TTL that is never
      extended, a 5-attempt budget charged before the principal check,
      single-use terminal completion, and a 1024-entry cap. Proven by 16-way
      concurrent isolation and single-winner completion tests under `-race`.
- [x] DI ticket exchange over the pinned candidate client IDs, with per-candidate
      HTTP Basic identity and an early stop on rate limiting. Session validation
      runs before a token set is accepted or saved.
- [x] Refresh with a 15-minute default safety window, per-principal collapsing of
      concurrent refreshes, and CAS save that yields to a newer stored token set.
- [x] `auth.TokenGate`, and **it is wired now**. `internal/cmd/wiring.go` passes
      one `*auth.TokenGate` to both `auth.Config` and `auth.RefreshConfig`, and
      `TestServeSharesOneTokenGateBetweenLoginAndRefresh` asserts the two configs
      hold the same pointer, so a refactor cannot split them silently.
- [x] Request-time host guard. `internal/garmin/auth/hostguard.go` refuses a
      caller-supplied request whose host is not a `protocol.ValidatedDomain`
      host, with `ErrForeignHost`, on the first attempt and on the replay after a
      `401`.
- [x] Unverified-JWT `exp` parsing that rejects `alg:none` (case-folded), missing
      or empty signatures, non-numeric, non-finite and overflowing values, and
      oversized tokens and segments. Used for expiry only, never authorization.
- [x] `internal/store` `FileStore`: encrypted records, atomic write through a
      random-suffixed temp sibling with `fsync` and directory sync, `0600` files
      in a `0700` directory, symlink refusal across the full path ancestry,
      `~user` refusal, legacy `garmin_tokens.json` detection with import and
      export, and per-principal CAS versioning. The wrapper's schema and CAS
      version are bound into the AEAD by `recordAAD`. Hostile umask is proven in
      a re-exec subprocess with mask `0o277`, which strips the owner write bit,
      so the assertions can only hold if the explicit `chmod` ran.
- [x] `internal/cryptostore`: five exported functions pinned by an AST-based
      surface test, AES-256-GCM with `crypto/rand` nonces, a versioned key ID in
      the envelope header and inside the AAD, length-prefixed AAD binding the
      principal and the record type, an owner-only key file, and staged rotation
      proven end to end.
- [x] `internal/securefile`: the shared filesystem hardening. `os.Root`
      component-by-component resolution from the volume root, post-open identity
      verification with `os.SameFile`, link-based exclusive install, `O_NONBLOCK`
      regular-file reads proven against a planted FIFO under a watchdog, and
      owner-only modes enforced by an explicit `chmod` on the open descriptor.
- [x] `internal/tokenlink` `Store`, the adapter that makes a `*store.FileStore`
      satisfy `auth.TokenStore`, asserted against the real consumer interface.

### Phase 3 detail — MCP foundation

- [x] `internal/mcpserver` stdio transport on the pinned SDK. Stdout carries MCP
      frames only; `internal/mcplog` puts the `slog` logger on stderr and refuses
      stdout when the process can tell its streams apart.
- [x] `internal/identity`: the principal and request-context types. On stdio one
      principal comes from process-local configuration; no tool argument can name
      a user, an email, a token path, or an account.
- [x] `internal/policy`: three tiers, explicit `ReadOnlyTools`, `WriteTools` and
      `DestructiveTools` name lists validated against the registered set in both
      directions at start-up, allowlist and denylist intersected with the tiers
      rather than bypassing them, and the write/destructive gate as the
      **intersection** of operator enablement and granted scope.
- [x] `internal/ratelimit`: the per-principal limiter and its handler middleware.
- [x] `internal/garmin/client`: the authenticated request layer, with bounded
      wire and decompressed response sizes, bounded page size and page start, a
      single bounded retry after a `401` that never replays a `POST` or `PATCH`,
      and typed errors.
- [x] `internal/garmin/api` read clients and the first 10 read-only tools, one
      per major Garmin payload style, each with all four annotation hints, a
      strict schema, bounded results, sanitized errors, and name and schema
      snapshot tests against `compat/tools.json`.
- [x] `garmin-mcp auth` completes the loopback browser login and MFA flow
      (`internal/loginweb` loopback profile), with the explicit TTY fallback in
      `internal/cmd`.
- [x] `garmin-mcp doctor` reports the effective configuration and branches on the
      `internal/cryptostore` key sentinels, so bad key material is now observed
      at start-up rather than only validated lexically.

### Phase 4 detail — remote multi-user

- [x] `internal/oauthserver`, the authorization server, on the standard library
      and the pinned SDK's types. ADR 0003 records why it is local rather than
      `ory/fosite`.
      - PKCE **S256 only**. `plain` exists in the source solely to be refused,
        and `migrations/0001_initial.sql` repeats the constraint as
        `CHECK (code_challenge_method = 'S256')`. A zero challenge also fails, so
        "no PKCE" is unrepresentable.
      - Exact issuer validation (`validateIssuer`: HTTPS, bare origin, no query,
        no fragment) and byte-exact redirect matching (`RedirectURI.Equal`
        compares the raw string; host case, default port and trailing slash are
        not folded), revalidated at redemption by `verifyCodeBindings`.
      - The client's `state` is echoed byte for byte and is never reused as
        server state: the transaction capability, the browser cookie and the form
        CSRF token are three independent server-generated values.
      - Single-use authorization codes bound to client, exact redirect,
        challenge, resource, scopes and principal. **The TTL is 60 seconds by
        default with a 5-minute configurable ceiling** (`DefaultCodeTTL`,
        `MaxCodeTTL`), not a flat five minutes. Redemption consumes the code
        atomically first; `TestConsumeCodeElectsExactlyOneRedeemer` proves one
        winner.
      - Opaque credentials with 256 bits of entropy (`SecretBytes = 32`) from
        `crypto/rand`, persisted and compared only as a SHA-256 `Lookup`.
      - Refresh rotation on every use with reuse detection that revokes the whole
        family transactionally, proven by
        `TestRotateRefreshTokenElectsOneWinnerAndKillsTheFamily`.
      - Consent is keyed on `(principal, client, exact redirect, resource)`.
        **Scopes are the value, not part of the key**: a request is admitted only
        when `Consent.Covers` shows the requested set is a subset of the consented
        set, so widening scope needs fresh consent while narrowing does not.
- [x] `internal/store` SQLite backend beside the file store, with `migrations/`
      `0001_initial.sql` and `0002_oauth_contract.sql` applied by a checksummed
      migrator. Down migrations are deliberately unsupported.
      - Principals keyed by an internal random UUID (v4, `crypto/rand`), never
        derived from an email or a Garmin id, with the Garmin account linkage as
        a unique keyed hash plus a sealed identity blob.
      - Versioned encrypted DI token sets with compare-and-set
        (`UPDATE ... WHERE principal_id = ? AND version = ?`,
        `ErrVersionConflict`).
      - Clients and their exact redirect URIs, consents on the full tuple, hashed
        transactions and codes, token families with a generation column, and
        audit events.
      - WAL, foreign keys and busy timeout are asserted by **querying the
        pragmas** on every pooled connection, not by inspecting the DSN string.
      - `ReconcileClient` is the privileged operator path that turns a configured
        client into a row; `RegisterClient` mints its own identifier, so a
        registration cannot squat another client's.
- [x] `internal/oauthstore`, the adapter, with
      `var _ oauthserver.Store = (*Adapter)(nil)` in both the production and the
      test file, and **five** contract-obligation tests against the real SQLite
      store with real goroutines, run under `-race`: code consumption,
      transaction consumption, refresh rotation, consent revocation and principal
      revocation.
- [x] `internal/mcpserver` Streamable HTTP.
      - The bearer is read from the `Authorization` header and nowhere else: not
        a query parameter, not a cookie, not a body field.
      - Sessions are bound to principal, client, resource and scopes, and the
        session id is a routing label stored only as a hash.
      - Revocation closes open sessions.
      - Protected Resource Metadata at
        `/.well-known/oauth-protected-resource`, plus the RFC 6750
        `WWW-Authenticate` challenge with `realm`, `resource_metadata`, and
        `invalid_token` / `insufficient_scope`.
      - Origin allowlist with CORS defaulting to deny, and forwarded headers
        trusted only from configured proxy CIDRs.
      - A cleartext public bind is refused unless `AllowInsecureCleartext` is set.
- [x] `internal/identity` bearer resolver. The principal comes only from a
      verified token; a principal already on the context is deliberately not
      consulted. `TestRemotePrincipalComesOnlyFromAVerifiedToken` proves it.
- [x] `internal/loginweb` remote profile beside the loopback one: the
      `__Host-garmin_mcp_auth` cookie, HSTS, a capability that never appears in a
      path, a query, a page or a log line, the disclosure page before credential
      entry, an independent CSRF token that is constant-time compared and
      rotated, MFA continuation held server-side, and `SameSite=Lax` because
      `Strict` is not sent on the cross-site navigation that starts the flow.
- [x] `internal/cmd` `serve --transport=streamable-http` is real. Mode isolation
      is proven inside one process by `TestRemoteAndStdioShareNoState`, which
      compares the token gate, the Garmin token store, the policy, the limiter,
      the principal resolver and the file-store presence across both shapes.
- [x] `e2e/remote_test.go` drives the real binary against a synthetic TLS
      deployment, under the `e2e` tag, with four tests: protected resource
      metadata readable unauthenticated with `bearer_methods_supported` exactly
      `["header"]`, an MCP request without a token refused with a challenge that
      carries `resource_metadata` and no error code, a token in a query parameter
      that never authenticates, and a bad header token reported as
      `invalid_token`.

### Phase 5 detail — compatibility breadth, in progress

- [x] `internal/garmin/api` gained the write and download surface for activity
      management, workouts, user profile, activity analysis and file downloads.
- [x] The two upstream pull requests are ported: in-place workout update that
      keeps the identifier (`Workouts.Update` forces the body `workoutId` to the
      path id so existing schedules stay valid), the strength exercise catalog,
      set replacement, and creating a completed strength activity.
      **Two API writes verify the saved result** — `StrengthWrites.ReplaceSets`
      re-reads the sets and compares them position by position, and
      `StrengthWrites.Create` additionally re-reads the summary and checks the
      stored activity identifier. Every other write returns what Garmin returned
      and does not read back.
- [x] `internal/tools` registers **23 read-only, 22 write and 5 destructive
      tools, plus the server's own `server_info`**, which is 51 tools on the
      wire. The calendar tools arrived with the GraphQL request shape.
- [ ] The remaining upstream breadth. 45 of the 138 manifest tools are
      implemented and **no resource is**. Health and wellness, nutrition, weight
      management, training, challenges, courses, women's health, data management,
      the device surface beyond `get_devices`, and the gear reads are all still
      unported. See `docs/parity.md` for the per-tool status.

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
- This file matches reality, and `git status --short` is clean.
- No placeholder or `not implemented` handler is counted as working behavior.

## M1 — local single-user stdio server

**Complete**, except the named gaps below, which are carried forward rather than
closed silently.

- [x] The phase-0 login gate is closed with a recorded outcome. — GO, ADR 0001.
- [ ] Native 0.3.10 login, MFA continuation, DI exchange, refresh with rotation,
      `.com`/`.cn` host selection, and the full failure classification pass
      against the fake Garmin service.
      Done: login, MFA continuation, DI exchange over the candidate client IDs,
      session validation, refresh with rotation and CAS, host selection, the
      request-time host guard, and the fallback classification, all under
      `-tags=fakegarmin`. **Not done: explicit widget MFA code delivery, a
      distinct rejected-OTP outcome, and the `JWT_WEB` fallback.** The item stays
      unchecked until those close.
- [x] `garmin-mcp serve --transport=stdio` binds exactly one principal from
      process-local configuration, rejects ambiguous multi-account configuration,
      and keeps stdout reserved for MCP frames.
- [ ] Tokens are stored owner-only and encrypted; hostile-umask, symlink,
      atomic-write, and platform-ACL tests pass.
      Done: everything except the platform half. The ACL **rule** is a pure
      function whose 18 cases execute on Linux, and the platform-specific sources
      and their `_test.go` files type-check for `GOOS=linux`, `darwin` and
      `windows` because `verify` runs `go vet` for each. **The Windows syscall
      layer has never executed.** The item stays unchecked for that reason alone.
- [x] `garmin-mcp auth` completes the one-shot loopback browser login and MFA
      flow, plus the explicit TTY fallback.
- [x] At least one representative read-only tool per major Garmin payload style
      is registered with accurate annotations, strict schemas, bounded results,
      and sanitized errors, and each has name/schema snapshot tests.
- [x] Refresh singleflight, rotating-token CAS, and cache-invalidation tests pass
      under the race detector. The collapsing is hand-rolled from `sync.Mutex`
      plus a per-principal in-flight map with a done channel; there is no
      `singleflight` package involved. One shared `auth.TokenGate` is wired and
      asserted. No cache exists, so cache invalidation has nothing to test.
- [x] CI, cross-platform builds, and the release pipeline are green.

## M2 — remote multi-user server

**Complete.** Every checked item is covered by tests in this repository. The two
unchecked items are the conformance requirement, which is blocked upstream with
evidence, and the operations documentation, which is real remaining work.

- [x] Streamable HTTP resolves the principal only from a verified bearer token
      on every applicable `POST`, `GET`, and `DELETE`; no `user_id`, email,
      token path, or account selector is ever a tool argument.
- [x] Protected Resource Metadata, the RFC 6750 challenge, authorization-server
      metadata, PKCE S256, resource indicators, exact issuer/redirect matching,
      and per-client consent behave as specified.
- [x] Transaction-gated browser login and MFA work end to end against the fake
      Garmin service; no credential-entry MCP tool exists.
- [x] Encrypted per-principal Garmin tokens, per-client consent records, hashed
      opaque MCP token material, and transactional revocation/unlink all persist
      and cascade correctly, failing closed on partial deletion.
- [x] Cross-user isolation and concurrent refresh pass under
      `go test -race -count=1`; session and event identifiers cannot
      authenticate, resume, read, or delete another principal's or client's data.
- [x] The OAuth negative matrix, rate limits, security headers, cookie
      attributes, request-size limits, redaction, and encrypted-store tamper
      tests pass.
- [x] Write and destructive tools are off by default remotely and require both a
      granted scope and operator enablement; destructive actions fail closed when
      confirmation cannot be obtained.
- [ ] **The selected MCP server conformance suite passes with no unexplained
      baseline entry. BLOCKED upstream**, with measured evidence. See
      [MCP conformance is blocked](#mcp-conformance-is-blocked). This item cannot
      close without an upstream change, and it is not a to-do this repository can
      pick up.
- [ ] Documentation covers remote deployment, reverse proxy/TLS, security
      assumptions, backup/restore, migrations, and key rotation. `docs/` has no
      operations document.

## M3 — full Taxuspt parity

- [ ] The generated parity matrix accounts for every tool and resource at the
      pinned Taxuspt commit. `docs/parity.md` carries per-tool status, and 93 of
      the 138 tools and all 5 resources are still `not-implemented`.
- [ ] Every required contract has passing name/schema/behavior tests, or a
      documented exclusion with evidence. The implemented 45 do; the rest have no
      handler yet. The documented exclusions are in `docs/parity.md` and in the
      ADR 0006 register.
- [ ] 0.3.2 to 0.3.10 behavior differences affecting those contracts are
      reconciled and recorded. See `docs/upstream-pins.md`: 9 of the 10 numbered
      requirements are landed. Only explicit widget MFA code delivery is
      outstanding.

## Commands to run and report at every milestone

```sh
go test -race -count=1 ./...
go test -race -count=1 -tags=fakegarmin ./...
go test -race -count=1 -tags=e2e -timeout=10m ./e2e/...
go vet ./...
golangci-lint run
govulncheck ./...
go build ./...
goreleaser check
goreleaser release --snapshot --clean
```

Both tagged suites hold real tests, so report their results, not just their exit
status. There is no conformance command to add; see the next section.

## MCP conformance is blocked

This is a measured result, not an unstarted task. The suite was run for real and
cannot score a domain server.

**What the suite is.** `modelcontextprotocol/conformance` is a TypeScript CLI
published to npm as `@modelcontextprotocol/conformance`, plus a composite GitHub
Action. It is not a Go package and not a container. In server mode it connects to
a running server as an MCP client over Streamable HTTP.

**What was run.** A live deployment of this server: a generated TLS certificate,
a master key, an empty database and one preregistered public client, serving at
`https://127.0.0.1:8443/mcp` with protocol version `2026-07-28` and 48 tools,
which was the registered count on the day of that run.
Result: **45 passed, 106 failed.** Every one of the 36 scored server scenarios
failed except three, and two of those three passed vacuously with zero checks.

**Two independent blockers**, both verified in the suite's own source rather than
inferred:

1. **Version gap.** The only stable release, `v0.1.16` (tag commit
   `21a9a2febd7100d7c17ac1021ee7f2ed9f66a1e0`), knows specification versions only
   up to `2026-02-12`. Support for the pinned `2026-07-28` exists solely on the
   `0.2.0-alpha` line, so running the pinned wire version means pinning a
   prerelease.
2. **The suite's server leg cannot authenticate, and its scored scenarios require
   the SDK's reference fixture server.** Its `ServerOptionsSchema` accepts only a
   `url` and a `scenario` — no header, no token, no client credentials — while
   this server authenticates every `POST`, `GET` and `DELETE` from the
   `Authorization` header. Even with a token, the scored scenarios call fixture
   tools by literal name (`test_simple_text`, `test_image_content`,
   `test_audio_content`, `test_tool_with_progress`, and fixture prompts,
   resources and completion flows). A missing tool is recorded as a **failure**
   rather than skipped, so a domain server fails by construction.

**No baseline was written, deliberately.** A baseline covering roughly 35
scenarios would encode "this is not the SDK reference fixture", which is not a
verified SDK limitation and could never legitimately clear. The brief permits a
baseline only for a verified limitation.

**What would unblock it**, none of which was attempted:

- a header or bearer input on the suite's server leg, which is an upstream
  change; or
- a conformance fixture profile in this server exposing the suite's expected
  tool, prompt and resource surface — which would test the SDK rather than this
  product, so it is refused; or
- an upstream requirement set for domain servers.

Do not re-open this as "wire the conformance job". It is wire-able only against a
fixture this project must not become. Re-check it when the suite ships a stable
release that knows `2026-07-28` **and** accepts a credential on the server leg.

## Known gaps

These are deliberate and tracked, not silently dropped.

### Deliberate deviations from the upstream contract

Each of these is also recorded in `docs/parity.md` and in the ADR 0006 register.
A reader who assumes parity from the tool name alone would be wrong about all of
them.

- **`download_activity_file` writes nothing to a server path.** The manifest's
  `output_dir` argument, the `GARMIN_FIT_DOWNLOAD_DIR` environment variable and
  the persisted download directory are all absent. No path is accepted from a
  caller and no file is opened; the bytes come back as a bounded embedded
  resource under `garmin://activity/{id}.{format}`, and a payload over the bound
  is refused rather than truncated. The manifest classifies the tool
  `external-side-effect`; **this server puts it in the write tier**, so it is
  gated like any other write.
- **The scheduling tools have no duplicate avoidance.** Upstream's
  `_is_already_scheduled` pre-check is a GraphQL calendar read that this server
  does not make, so `schedule_workout` and `schedule_workouts` are honestly
  non-idempotent. Their annotations say so, and upstream's `Idempotent:` opening
  sentence is deliberately absent from every description in this server — a
  registration test asserts that no description contains it.
- **`set_activity_description` cannot clear a description with an empty string.**
  `api.requireText` refuses an empty write field with `client.ErrValidation`, and
  the tool layer rejects it before that.
- **`get_exercise_types` serves a compiled-in subset** of the FIT
  `exercise_category` enum rather than fetching Garmin's web-tier catalog,
  because that host is outside the client's allowlist. Categories are validated
  against a closed set; an exercise name gets a lexical check only, with Garmin
  authoritative.
- **`get_workout_by_id` serves the numeric identifier only.** The UUID form that
  adaptive Garmin Coach plans use is not served.
- **Two tools are left unregistered rather than stubbed**:
  `get_activity_fit_data` (no FIT parsing), and `set_fit_download_dir` (it would
  persist a caller-supplied server filesystem path, and is refused by design).
  The three calendar tools that were also unregistered — `get_scheduled_workouts`,
  `get_training_plan_workouts` and `schedule_week` — are registered now that the
  client layer builds the GraphQL request they need.

Five registered tools are **not** in the pinned manifest at all, because they
come from open upstream pull requests rather than the pinned commit:
`get_exercise_types`, `set_activity_strength_exercise_sets`,
`create_strength_training_activity`, `update_workout` and `delete_activity`.

### What actually gates a write tool today

The write and destructive tiers need the **intersection** of operator enablement
and a granted scope, and the two halves behave differently per transport:

- **stdio**: `internal/cmd/wiring.go` leaves the scope source nil, which becomes
  `policy.NoScopes`, so every write and destructive tool is refused however the
  operator sets `enable-write-tools`.
- **streamable-http**: `internal/cmd/remotescopes.go` reads the scopes from the
  verified bearer token and nothing else. An operator who registers an OAuth
  client carrying `garmin:write` **and** enables the write tier gets working
  writes. Nothing in the repository blocks that combination, and it is the
  intended M2 behavior. Both halves default off, so the default deployment is
  read-only.

The package comment in `internal/policy/tier.go` still says that no scope is
issued anywhere in this repository. That was true before the remote path landed
and is now true only of the default configuration. Correct it in the next commit
that touches the package.

### Fail-safe limits that are known and accepted

- `mcpserver.Revocation` carries principal, client and family, and **no resource
  selector**, while a session binding and a consent key both carry the resource.
  Revoking one consent therefore closes slightly more sessions than that grant
  covered. The direction is fail-safe, so it is accepted rather than fixed.
- A revocation event dropped under buffer pressure (a 256-entry channel with a
  counted non-blocking send) costs the affected session its early termination
  only. The database stays the authority and the token check refuses the next
  request on that session.

### Gates the pipeline still needs

None of these exists: a bounded **fuzz smoke** job, a **coverage threshold**
gate, a **two-clean-build reproducibility** check, **container image signing**,
**container image and per-binary SBOMs**, and **build provenance attestation**.
The MCP conformance job is not on this list: it is blocked upstream, not
unstarted.

- `.goreleaser.yaml` signs the **checksum file only** (`artifacts: checksum`,
  keyless cosign `sign-blob`) and emits **archive SBOMs only**
  (`sboms: artifacts: archive`). There is no image signing block, no container
  image SBOM, no per-binary SBOM, and no build provenance attestation. None of
  this is "configured but unverified": it is not configured.
- The CI unit job writes `cover.out` with `-covermode=atomic`, and no job asserts
  a minimum percentage, so the documented 80% rule is unenforced. Two packages
  are under it today; see [Measured coverage](#measured-coverage).
- No fuzz target exists anywhere in the repository.
- GitHub-native **secret scanning** is a repository setting, not a workflow file,
  and still needs enabling. Dependency and license review **is** an enforced CI
  gate: `ci.yaml` runs a SHA-pinned `actions/dependency-review-action` on
  `pull_request` with `fail-on-severity: low` and an explicit `allow-licenses`
  list that matches `docs/dependencies.md`.

### Commands that are still declared gaps

`garmin-mcp migrate` and `garmin-mcp tools list` still validate configuration and
then return a `*cmd.NotImplementedError`. Both subsystems now exist — the
migrator is in `internal/store` with the embedded `migrations` package, and the
registry is `internal/tools` — so these two commands are wiring that was not
done, not missing capability. They must not be counted as working behavior.

### Platform and environment limits

- **Windows ACL enforcement has never executed.** `internal/securefile/acl.go`
  carries the decision as a pure function whose 18 cases across 7 test functions
  execute on Linux, the only platform any CI job runs tests on. The
  platform-specific sources and their test files type-check for every supported
  `GOOS` because `verify` runs `go vet` for each, and type-checking executes
  nothing. `acl_windows.go` and `perm_windows.go`, about 200 lines, run on no CI
  runner. The old `icacls` subprocess is gone.
- The OS keyring backends in `internal/cryptostore` are cgo-free **no-ops** that
  report unavailable, which keeps `CGO_ENABLED=0` cross-compilation working per
  ADR 0005. The owner-only key file is the only real backend.
- `internal/securefile` requires hard links on the filesystem holding the key and
  store directories, because exclusive install links a completed temporary into
  place instead of renaming it. There is no rename fallback, on purpose. This is
  still undocumented for operators.
- Symlink checking refuses a store or key directory reached **through** a
  symlinked path. On macOS that refuses anything under `/var`, `/tmp` or `/etc`,
  because `/var` is a symlink to `/private/var`. Four test suites work around it
  with `filepath.EvalSymlinks(t.TempDir())`. Decide whether that is the final
  contract and document it, or normalize before checking.
- `FileStore` provides no cross-process compare-and-set: it compares the version
  under a per-principal in-process mutex with no file locking, so it is safe for
  a **single active instance** only. The SQLite backend is the answer for
  anything else, and the v1 SQLite deployment is single-active-instance too.
- `modernc.org/sqlite` is a direct dependency, and `modernc.org/libc` must move
  only with it. Both are on the manual-review list in `.github/renovate.json` and
  neither may be automerged.

### Storage and key-management gaps

- Key rotation has no store-level driver and no operator procedure.
  `internal/cryptostore` proves staged rotation end to end, but no store re-seals
  existing records and no command drives it. There is no `docs/operations.md`.
- There is no backup or restore test for the SQLite database.
- The record schema version is 2. A schema-1 record reports corruption rather
  than decoding, because its additional data does not match. No migration exists
  and none is needed: nothing has shipped. The next bump after a release carries
  schema 2 does need one.
- Down migrations are deliberately unsupported, because a down migration that
  drops a column silently destroys token material.

### Carried-over uncertainties

- Live `MFA_REQUIRED` continuation is unproven against the real service. See
  ADR 0001.
- Phase-0 evidence comes from one residential source IP. Datacenter and CI egress
  may be scored differently by Cloudflare. See ADR 0001.
- The widget MFA path is incomplete. `ClassifyWidgetLogin` decides from the HTTP
  status and the page title only; the inline-JS variables in the widget page are
  not parsed, so the delivery method stays the hardcoded default and the explicit
  code-delivery request is never sent. `PathWidgetRequestMFACode`,
  `EndpointWidgetRequestMFACode` and `Hosts.WidgetRequestMFACodeURL` exist and
  nothing calls them. `Pending.MFADeliveryUncertain()` and
  `Result.MFADeliveryUncertain()` signal the uncertainty honestly. There is still
  no outcome distinct from `OutcomeInvalidCredentials` for a rejected OTP.
- The `JWT_WEB` cookie fallback after a failed DI exchange is not implemented.
  Upstream consumes the CAS ticket through the web front end when the DI exchange
  fails; this project requires the DI token set and reports
  `ErrTokenExchangeFailed`. A Garmin-side DI change therefore becomes a hard
  login failure rather than a degraded one.
- The login state machine is still not on the top-level login path.
  `auth.Machine` is exhaustively tested, but `Authenticator.Login` and
  `CompleteMFA` report progress through `Result` states, and the machine is
  stepped only by the MFA transaction registry. Either route the top-level flow
  through it or state plainly that it governs transactions only.
- The container smoke test proves that a **nonroot, read-only, shell-free** image
  can execute the binary. It does not prove server start-up and does not prove a
  writable `/data` volume: the container run exercises the default command and
  exits.
- The parity extractor scripts are not committed; `docs/parity.md` documents the
  algorithm instead. A Go regenerator that fails CI on manifest drift is still
  deferred, so manifest drift against a new upstream pin cannot be diffed in CI.

## Next task

Continue phase 5: port the next upstream domain, contract test first, starting
with health and wellness, the largest unported module at 29 tools.

Scope, in order:

1. Add the `internal/garmin/api` read client for the health and wellness
   endpoints, with tolerant decoding, bounded responses and the existing page
   caps, and cover it against the fake Garmin service.
2. Register the read-only tools for that domain, each with all four annotation
   hints, a strict schema drawn from `compat/tools.json`, bounded results and
   sanitized errors, and a name and schema snapshot test.
3. Update `docs/parity.md` in the same commit, and record any deviation in the
   ADR 0006 register rather than in a commit message.

One open item sits outside that slice and must not be forgotten: the operations
documentation that M2 requires — remote deployment, reverse proxy and TLS,
security assumptions, backup and restore, migrations, and key rotation. The MCP
conformance requirement needs no work here; it is blocked upstream and the
evidence is recorded above.
