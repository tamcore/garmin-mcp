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
| 5 — compatibility breadth (M3) | **IN PROGRESS** — 80 of the 138 upstream tools are implemented, plus 5 tools the pinned manifest does not carry. No resource is implemented |
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
| `internal/garmin/api` | 90.7% | 90.7% |
| `internal/garmin/auth` | 65.9% | 88.3% |
| `internal/garmin/client` | 94.3% | 94.6% |
| `internal/garmin/protocol` | 96.7% | |
| `internal/identity` | 97.7% | |
| `internal/loginweb` | 82.6% | |
| `internal/mcplog` | 98.5% | |
| `internal/mcpserver` | 89.0% | |
| `internal/notices` | 89.3% | |
| `internal/oauthserver` | 92.4% | |
| `internal/oauthstore` | 84.6% | |
| `internal/policy` | 91.7% | |
| `internal/ratelimit` | 95.7% | |
| `internal/securefile` | 83.0% | |
| `internal/store` | 83.8% | |
| `internal/testkit` | 91.5% | |
| `internal/tokenlink` | 80.0% | |
| `internal/tools` | 85.8% | 85.8% |
| `migrations` | 100.0% | |

One package sits under the 80% review rule: `internal/cmd` at 74.3%.
`internal/tools` left the list earlier and stands at 85.8%.
CI enforces a per-package floor with an explicit exception list, so a package
that drops under the floor fails the build unless it is named there.

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
- [x] Cobra command tree in `internal/cmd`. `serve`, `auth`, `doctor`,
      `version`, `migrate` and `tools list` all do real work. No command returns
      a not-implemented sentinel any more.
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
- [x] `e2e/oauthflow_test.go`, `e2e/loginform_test.go` and
      `e2e/tenantisolation_test.go` close three of the four rows AGENTS.md's
      testing table used to mark **[TARGET]** at the e2e layer, over the real
      binary:
      - The authorization-code grant against the real `/token` endpoint: PKCE
        S256 is required (a missing or wrong `code_verifier` is refused as
        `invalid_grant`, **and** a positive control with the correct verifier
        and a freshly seeded code — through the production digest helper,
        `oauthserver.SecretFromString(code).Lookup().Hex()`, not a hand-rolled
        one — succeeds, so the negative case cannot pass merely because
        `/token` always answers `invalid_grant`); the redirect URI must match
        the authorization exactly, with the same positive-control shape; a
        code redeemed by N concurrent requests mints exactly one token (fired
        as true concurrent goroutines, which a serial replay cannot
        distinguish from "marked used after minting" rather than atomically);
        a `plain` PKCE method is refused at `/authorize` before any login; and
        a token this endpoint issues authenticates a real MCP `initialize`
        call.
      - The remote browser login profile driven with an `http.Client`: the
        disclosure page and the full `__Host-` cookie attribute set (`Secure`,
        `HttpOnly`, `Path=/`, no `Domain`, `SameSite=Lax`); the rotating CSRF
        token — refused when wrong, accepted when right, refused again on
        replay of the now-rotated value, and refused when one session's token
        is presented under a different session's cookie; and a credential
        submission that fails safely. That last case is now a proof rather
        than an assumption: the deployment is pointed at a recording,
        always-refusing CONNECT proxy (the same one
        `e2e/exercisecatalog_test.go` uses for the start-up catalog read), and
        the test asserts the login attempt reached exactly `sso.garmin.com`
        and carried no `Authorization` or `Cookie` header — a build that
        deleted the `authenticator.Login` call outright, which the earlier
        blackhole-only version could not tell apart from a real attempt, fails
        this one.
      - Tenant isolation: two principals each mint their own bearer token
        through the real `/token` endpoint; revoking one principal's token via
        the real `/revoke` endpoint is proven to leave the other principal's
        still-valid token untouched; and a further assertion resolves each
        token through the store to its principal ID and requires the two to
        differ, which is the property "tenant isolation" actually names (the
        revocation check alone cannot tell two distinct principals from one
        principal a build mis-bound both tokens to, since `codeGrant` mints a
        fresh token family per exchange regardless). That last assertion is
        implemented exactly as reviewed; applying the described mutant
        (binding every issued token to the first principal a process ever saw)
        makes it fail as intended, and it passes against unmodified code.

        Reaching that state meant fixing the harness, not the server, and the
        misdiagnosis is worth recording because it looked exactly like a
        product defect. Seeding used to open the database from the test process
        **while the server subprocess held it** — two writers on one SQLite
        file, which violates this project's own single-writer requirement
        (`docs/operations.md`). The server's own error named the real cause,
        `disk I/O error (522)`, which is `SQLITE_IOERR_SHORT_READ`. Two
        symptoms followed from that one cause: rows the server had written were
        unreadable from the test process, and a token issued while two or more
        redemptions of one code raced failed its own verification, so `/token`
        answered 200 with a token the server then refused. Neither was a defect
        in the store: a single-process reproducer over the real store, real WAL,
        the production pool size and 8-way concurrency under `-race` stayed
        clean, which is what cleared the product and pointed at the harness.
        Every seed, the client row included, now runs before the subprocess
        starts, and nothing writes while it runs.
      - `e2e/cli_test.go` now strips every inherited `GARMIN_MCP_*`
        environment variable before launching a subprocess, rather than only
        adding the offline proxy on top of the ambient environment: those
        variables outrank a deployment's config file
        (`docs/configuration.md`), so an operator's own exported
        `GARMIN_MCP_DATABASE_PATH` previously redirected every subprocess this
        suite starts at the operator's real database.

      **What is still not covered, and why.** Completing the browser login —
      including an MFA continuation — needs a real answer from Garmin. This is
      not because no seam exists to redirect the compiled binary's own Garmin
      traffic: `internal/cmd/components.go` builds the login transport with
      `http.ProxyFromEnvironment` and no custom `RootCAs`, and on Linux
      `crypto/x509` honours `SSL_CERT_FILE`, so an `HTTPS_PROXY` pointed at a
      TLS-terminating, per-host-certificate CONNECT proxy in front of a fake
      Garmin backend, plus `SSL_CERT_FILE` naming that proxy's CA, is a
      working seam there. It is not used for a full login because it is
      expensive to build (a CONNECT proxy that mints a certificate per host on
      the fly) and silently unavailable on macOS, where the root verifier does
      not consult `SSL_CERT_FILE` — a cost and platform asymmetry, not an
      impossibility. `e2e/loginform_test.go`'s credential-submission test now
      uses exactly this seam, pointed at a proxy that only records and refuses
      rather than terminates TLS, which is enough to prove the attempt reached
      Garmin's SSO host and nothing else; a full login still needs the
      TLS-terminating version, which remains future work. The OAuth-flow and
      tenant-isolation tests route around the same gap the same way: the
      authorization code a granted consent would have issued is seeded
      directly into the SQLite database with the exact bindings
      `BeginAuthorization` and `GrantConsent` produce, and only that one step
      is not driven over HTTP. Every other step — discovery, `/authorize`'s
      own validation, PKCE, the token exchange, single-use, revocation and the
      MCP call — is.

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
- [x] `internal/tools` registers **32 read-only, 22 write and 5 destructive
      tools, including the server's own `server_info`**, which is 59 tools on the
      wire. The calendar tools arrived with the GraphQL request shape.
- [x] `get_exercise_types` serves the catalog Garmin publishes rather than the
      compiled-in subset. The document at the compiled-in URL
      `https://connect.garmin.com/web-data/exercises/Exercises.json` is read once
      per process, as the server is assembled in `cmd/garmin-mcp`, and the
      immutable snapshot is shared by every tool call and every strength-write
      validation for the process lifetime — in memory only, refetched on restart.
      The compiled-in subset stays as the **fallback**: a timeout, a refused
      connection, a TLS failure, a redirect, a non-200, a truncated or oversized
      body, malformed JSON, a document with no categories, and a document smaller
      than the subset itself each land on it, and none of them can fail a
      start-up. The result reports which catalog answered and the muscle groups
      the published document carries. The published document is preferred to the
      vendored FIT profile because the two sets differ in both directions: it
      holds values Garmin's own client writes that the enum cannot express, and
      it holds muscle data the enum has no field for.

      The read is anonymous by construction — its own dedicated transport, never
      the process-wide default, no jar, no token, no cookie, and exactly two
      compiled-in headers, `Accept` and `User-Agent`. A test pins that whole
      header set rather than only the absence of a credential, and an end-to-end
      test records what the binary actually asked to reach.

      It is bounded twice: 4 MiB on the wire, and — because a byte cap does not
      bound what a document expands into — 256 categories, 1024 exercises per
      category, 8192 exercises, 64 muscles per list and 2 MiB rendered. The
      document is walked as a stream, categories, exercises and muscle arrays
      alike, so each bound is applied at the key or element that crosses it and a
      hostile body is refused before it is materialized rather than after. One
      rule everywhere: over a bound a document is refused, never truncated.

      Every bound is measured against the published document (2026-08-16), and the
      one that was not cost a regression: `MaxCatalogMuscles` was set to 8 without
      being measured, Garmin publishes 10, and refuse-never-trim then made a
      running server serve the 98-exercise fallback until the live drift detector
      caught it. Observed against each bound: 198,082 wire bytes (21.2x), 47
      categories (5.4x), 131 exercises in the largest category (7.8x), 1510
      exercises (5.4x), 10 muscles in the longest list out of a vocabulary of 18
      (6.4x), 225,666 rendered bytes (9.3x) — the rendered cap being the tightest.
      The recognition floor of 50% sits against a measured 64.3%, which tolerates
      14 of the 63 recognized names disappearing. `docs/parity.md` carries the
      table. The offline guard the suite lacked is now
      `TestAMuscleListAtGarminsObservedMaximumLoads`: it carries Garmin's observed
      muscle shape without the network, so a bound below reality fails in CI rather
      than in production, which the credentialed drift detector cannot do.

      Ambiguity is refused for the same reason: two keys that normalize to one
      (including empty and whitespace-only keys, which are recorded before they
      are judged), a structural member carried twice, and data after the top-level
      document each would let the order a document happens to carry things in
      decide what gets served.

      A count-only plausibility gate would still admit a fabricated catalog, whose
      categories would then become the closed set strength writes validate
      against, so a fetched document must also be recognizable as Garmin's
      taxonomy: every compiled-in category except the FIT `UNKNOWN` sentinel, and
      at least half of the compiled-in exercise names under their own parent —
      measured at 33 of 33 and 63 of 98 (64%). That is recognition, not
      authentication; the trust anchor is TLS to connect.garmin.com.
- [x] Activity file decoding moved to `github.com/muktihari/fit` (ADR 0007). The
      hand-rolled container decoder shipped two defects that only real files
      exposed: session segments collapsed to a single sample, because these
      devices write the same instant into `session.timestamp` and
      `session.start_time`, and whole-activity ascent came out near double
      Garmin's own figure by summing barometric jitter. Session, lap and overall
      figures now come from the profile the SDK carries, and three real
      activities reproduce Garmin's own distance, elapsed time, heart rate,
      ascent and calories exactly. The synthetic fixtures caught neither defect,
      because a fixture built from a test's declared values agrees with any
      derivation of them. One behaviour is lost: the old reader tolerated a
      truncated announced `dataSize` in the header, and the SDK rejects it.

      Four defects an adversarial review of that slice found are fixed with it.
      The **coordinate claim was wrong**: the SDK decodes every field of every
      message, position included, and has no field filter, so "coordinates are
      never decoded" was untrue everywhere it was written. The guarantee is now
      stated as what the code enforces — never read into this server's model,
      scrubbed out of the reused decode buffer after each sample, never returned
      and never logged — and the test is named for it. **Session and lap spans
      are now bounded during collection**: every span is summarized against the
      whole record stream, so a file carrying overlapping spans over a full
      sample stream was quadratic work performed before any result bound could
      apply. **The caller's context reaches the decode**, so an MCP deadline can
      stop it, and a cancelled decode is reported as cancellation rather than as
      a malformed file. **`FITData.LogValue` no longer logs the ride's shift
      total**, which is activity telemetry.

      A **second** adversarial review of those fixes found five more defects in
      this package and its tool, and all five are fixed:

      - The **coordinate claim was still wrong**, for the third time. Scrubbing
        `PositionLat` and `PositionLong` leaves `mesgdef.Record.UnknownFields` —
        every field number the profile does not define — and `DeveloperFields`,
        which an application names itself, both of which can carry a latitude and
        neither of which any method suppresses. The collector now clears all four
        after every sample, and `TestCollectorScrubsEveryFieldAPositionCouldHideIn`
        inspects the retained struct rather than the returned model, which is the
        only place the difference is visible.
      - The **span bound was not a bound**. Sessions and laps each got 1000, so
        2000 spans over 60 000 retained records is 120 million record visits per
        analysis pass, several passes deep, with a power series allocated per
        span. The two classes now carry their own bound at the count the result
        renders — `DefaultMaxFITSessions` 20 and `DefaultMaxFITLaps` 200 — and
        `get_activity_fit_data` sets them from its own render bounds.
        `TestTheSpanBoundsKeepTheAnalysisAffordable` asserts the arithmetic
        rather than the mechanism, so a later widening fails there.
      - The **context reached the decoder but not the archive**: an
        already-cancelled caller with a malformed zip was told its file was
        malformed, and expansion ran to the byte bound regardless. It is now
        checked before extraction and between expansion chunks.
      - **`FITData.LogValue` still logged the exact shift count** as a list
        length, under a comment saying it did not. It logs presence.
      - **`update_workout` believed any identifier it was answered with**, and
        the 204 read-back copied whatever the GET returned. Both now require the
        identifier to be the one the request addressed and fail loudly otherwise:
        the result of an update is what a caller schedules or deletes next.
- [x] Health and wellness, all 27 remaining tools of `health_wellness.py`, all
      read-only and all `health` sensitivity. The request constants landed first
      and centrally (ADR-free, `internal/garmin/client/endpoints_health.go`)
      because `Request.Validate` refuses anything outside its allowlists, so a
      missing entry makes a tool impossible to call.

      Shapes were established from the pinned upstream source where it names a
      field, and from **sampled responses** where it does not: `Taxuspt`'s
      `health_wellness.py` is the curating layer and therefore names Garmin's
      wire fields directly, which sourced most of them. Six live samples were
      taken against a real account under a one-call-per-ten-minutes budget, with
      every call logged. They are not in this repository — they carry health
      data — but they changed the code in ways no source reading did:

        * `get_respiration_data` encodes a missing reading as the **sentinel**
          `-1.0` or `-2.0`, not as null. Decoded at face value that is a
          respiration rate of minus one breath per minute, and every minimum or
          mean computed over the series is silently wrong. The two sentinels
          differ, so both are kept distinguishable, and the handling was applied
          to every intraday series: a negative heart rate or SpO2 is not a
          measurement either.
        * The same document carries a **second** series whose descriptor list
          uses different key names — `respirationAveragesValueDescriptionKey`,
          "Description" where the first list says "Descriptor". A reader written
          for the first silently falls back to positional order on the second.
        * `get_heart_rates` returns a raw Garmin document where a reading may be
          `null` and the series is neither contiguous nor evenly spaced, while
          `get_stats` returns a curated one. The two shapes are not symmetric.
        * `get_body_composition` returns `totalAverage` **present with every
          metric null** when an account records no weight, so absence is per
          field and not per object.

      Where a shape could not be established, the tool returns Garmin's document
      under Garmin's own key names and under a bound, rather than inventing
      curated ones. That is recorded per tool in `docs/parity.md`.

      Keeping Garmin's names is not the same as forwarding Garmin's document.
      Every untyped passthrough goes through one shared sanitiser,
      `internal/tools/sanitize.go`, which recursively removes the keys that
      identify a person or a place — `userProfilePK` and every other `*ProfilePK`,
      `userId`, `userProfileId`, `ownerId`, `ownerDisplayName`, `displayName`, and
      the latitude, longitude and `position*` keys — and reports how many it
      removed as `dropped_fields`, a count and never a list of names. The walk is
      bounded in depth and node count, so a drifted or hostile document cannot
      exhaust the stack or the heap. Five tools use it: `get_lifestyle_logging_data`,
      `get_body_composition`, `get_stats_and_body`, `get_body_battery_events` and
      `get_all_day_events`. No allowlist was preferred over it for the two event
      tools, because upstream curates no field of either document and no observed
      sample establishes one — an allowlist there would be invented, not sourced.
- [x] The remaining upstream breadth. **137 of the 138 manifest tools are
      implemented and all 5 resources are served.** Health and wellness,
      nutrition, weight management, training, challenges, courses, women's health,
      data management, the device surface and the gear reads are all ported. The
      single unimplemented row is `set_fit_download_dir`, a documented refusal
      rather than remaining work. See `docs/parity.md` for the per-tool status.
      This entry read "80 of the 138 … and no resource is" for a long time after
      both halves were finished.

Live Garmin validation: the opt-in `garminlive` layer exists and was run for
real. See [The live layer](#the-live-layer) for what passed, what could not run,
and why.

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
- [x] Native 0.3.10 login, MFA continuation, DI exchange, refresh with rotation,
      `.com`/`.cn` host selection, and the full failure classification pass
      against the fake Garmin service.
      Done: login, MFA continuation, DI exchange over the candidate client IDs,
      session validation, refresh with rotation and CAS, host selection, the
      request-time host guard, the fallback classification, a distinct
      rejected-OTP outcome, and explicit widget MFA code delivery, all under
      `-tags=fakegarmin`. **Not done: the `JWT_WEB` cookie fallback**, which was
      implemented and then deliberately removed — a credential this architecture
      can never carry to a second call. That is a recorded decision rather than
      outstanding work, which is why this item is checked; the deviation is in
      `docs/parity.md` and the ADR 0006 register.
- [x] `garmin-mcp serve --transport=stdio` binds exactly one principal from
      process-local configuration, rejects ambiguous multi-account configuration,
      and keeps stdout reserved for MCP frames.
- [x] Tokens are stored owner-only and encrypted; hostile-umask, symlink and
      atomic-write tests pass. The platform-ACL half of this item is **gone with
      the platform**: Windows is no longer supported, so there is no ACL to test.
      `internal/securefile` compiles on unix only, and the hostile-umask, symlink,
      ancestry and atomic-write tests all execute on the platforms that ship.

      This item was unchecked for a long time on the strength of the ACL half, and
      the sequence is worth recording. A `windows-acl` CI job was added to make the
      security-descriptor syscalls execute for the first time, and its first run
      immediately failed — usefully. `internal/securefile` passed, so the syscall
      layer worked, but three `internal/store` legacy-import tests failed because
      files the test process had just created were owned by `S-1-5-32-544`,
      `BUILTIN\Administrators`, and the owner check refused them as not owned by
      the current user. That is standard Windows behaviour: a process holding an
      elevated token stamps *Administrators* as owner of what it creates, and
      `currentUserSID` compared against `GetTokenUser` instead. An operator running
      elevated would have had their own token file refused. Rather than fix a
      platform nobody here runs, Windows was dropped.
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
- [x] Documentation covers remote deployment, reverse proxy/TLS, security
      assumptions, backup/restore, migrations, and key rotation.
      `docs/operations.md` covers all six across its eight sections. This entry
      claimed the document did not exist long after it was added in `1307f39`;
      the claim, not the work, was the gap.

## M3 — full Taxuspt parity

- [x] The generated parity matrix accounts for every tool and resource at the
      pinned Taxuspt commit. `docs/parity.md` carries per-tool status. **All 5
      resources are served**, and **137 of the 138 tools are implemented**. The
      single remaining row is `set_fit_download_dir`, a documented refusal rather
      than remaining work, so no tool is outstanding. What is still open is the
      **regenerator**, not the matrix: the extractor scripts are not committed, so
      drift against a new upstream pin cannot be diffed in CI.
- [x] Every required contract has passing name/schema/behavior tests, or a
      documented exclusion with evidence. All 137 implemented tools do. The
      documented exclusions are in `docs/parity.md` and in the ADR 0006 register.
- [x] 0.3.2 to 0.3.10 behavior differences affecting those contracts are
      reconciled and recorded. See `docs/upstream-pins.md`: **all 10** numbered
      requirements are landed. Explicit widget MFA code delivery was the last and
      closed on 2026-08-16.

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

All three tagged suites hold real tests, so report their results, not just their
exit status. There is no conformance command to add; see the next section.

The live layer is **not** a milestone command: it contacts the real Garmin
service and it is opt-in. Run it deliberately, and record its outcome:

```sh
GARMIN_USERNAME=... GARMIN_PASSWORD=... \
GARMIN_LIVE_ACK=i-accept-live-garmin-traffic \
GARMIN_LIVE_WRITE_ACK=i-accept-live-garmin-writes \
go test -race -count=1 -tags=garminlive ./live/...
```

## The live layer

`live/` carries the `garminlive` tag, in two halves. The test count is not
restated here, because it rots on every added test and states nothing: what keeps
the layer honest is `TestEveryReadOnlyToolIsAccountedFor` and
`TestEveryWriteAndDestructiveToolIsAccountedFor`, which fail when a registered
tool is neither driven by the suite nor listed with a reason.

The **read half** is read-only by construction — every domain client and every
tool of that half reaches Garmin through a caller that refuses anything but a
`GET`, a `HEAD`, or a `POST` whose body is one of the GraphQL query documents
the request layer itself renders, so no mutation can reach the gateway — and
it is gated three ways: the build tag, `GARMIN_USERNAME`/`GARMIN_PASSWORD`, and
`GARMIN_LIVE_ACK` set to the exact value `i-accept-live-garmin-traffic`.

The **write half** needs a fourth gate on top of those three:
`GARMIN_LIVE_WRITE_ACK` set to the exact value `i-accept-live-garmin-writes`.
It is default off, so acknowledging live traffic never acknowledges live
mutation. With it shut every write check skips and the read half behaves exactly
as it did before.

A missing gate is a skip, never a failure. No workflow builds the tag and none
may. `AGENTS.md` holds the full how-to.

It asserts cross-source consistency and never a golden value. Nothing in the
package is pinned to the account under test, and a failure names the field and
the relative delta rather than the reading, so a failing run cannot print health
data into a terminal.

**Run on 2026-08-15 against the dedicated test account.** What passed:

- Login through the `mobile_ios` strategy, the DI exchange, session validation
  against the API tier, and a second read on the same stored token set.
- The read-only caller refusing `POST`, `PUT`, `PATCH` and `DELETE` on a write
  path while still passing the GraphQL calendar read.
- All nineteen account-scoped read-only tools: every one answered, obeyed its
  declared bounds and truncation flags, and carried no coordinate, credential or
  raw payload.
- `get_full_name` and `get_devices` agreeing with the profile and device domain
  clients.
- The accounting test that fails when a registered read-only tool is neither
  exercised nor excused with a reason.

**The accounting test caught the health slice.** The 27 health-and-wellness tools
were registered without a live sweep entry, and because no workflow builds the
`garminlive` tag that landed on master with `TestEveryReadOnlyToolIsAccountedFor`
failing — the exact decay the test exists to stop, proven by the test rather than
by a reviewer. `live/healthsweep_test.go` now drives all 27 through the same paced
read-only caller, the same bound and truncation-flag checks and the same leak scan
as the rest of the sweep, with the argument shape each contract declares: a
calendar day, an inclusive range (`get_body_composition`, `get_daily_steps`,
`get_body_battery`, `get_blood_pressure`) or an end date plus a week count
(`get_weekly_steps`, `get_weekly_intensity_minutes`, `get_weekly_stress`). It is a
separate file only because `surface_test.go` would otherwise cross the 400-line
limit.

**Run on 2026-08-16 against the dedicated test account: all 27 passed, and none is
excused.** The account records almost no wellness data, so most answered with an
empty day. That is a pass and not a skip, and what the run proves is now stated
exactly rather than generously.

**What a sweep entry proves, and what it does not.** The first version of this
sweep asserted only that a result was non-empty and leaked nothing, which a handler
returning a well-formed object without ever contacting Garmin would satisfy. Two
assertions closed that, and both were run against the real service in a failing
state before being trusted:

- **Transport evidence.** `readOnlyCaller` counts what it dispatches, and
  `assertToolAnswers` requires the count to rise across every call. Removing the
  one declared exception made `get_exercise_types` fail with `answered without
  dispatching a request to Garmin`, which is the assertion working.
- **Result shape.** `live/shapes_test.go` declares, per swept tool, the keys that
  tool's own answer always carries; a key that is also an argument must repeat the
  value that was sent. Adding a key no answer has made `get_stats` fail with
  `returned no "a_key_no_answer_has"`.
  `TestEverySweptToolDeclaresItsShape` pins the table to the sweep in both
  directions and needs no credentials, so a tool added to one and not the other
  fails offline.

So the claim the sweep supports is: **each tool dispatched a request and its answer
carried its own shape, its declared bounds, boolean truncation flags, and no
coordinate, credential or raw payload.** It does not claim the readings are
correct — that is what the FIT cross-check and the domain-client agreement checks
are for, and neither covers the wellness surface.

`get_exercise_types` is the one read-only tool that legitimately dispatches
nothing through `readOnlyCaller`: it answers from the strength catalog the process
loaded before the sweep started. That load is the published-catalog read, and it
runs on its own anonymous client rather than through the suite's caller, so the
`answersLocally` assertion still holds after the fetch landed — it was verified,
not assumed. Its reason in `live/shapes_test.go` was rewritten to say that, and
`live/exercisecatalog_test.go` is the drift detector for the URL itself: it fails
when the published document stops answering, stops carrying muscle groups, or
shrinks past a floor.

That drift test is gated like every other request this suite makes, and so is the
start-up read the two environments perform: `gatedExerciseCatalog` checks the
acknowledgement itself rather than trusting its callers, because it is the one
place in the suite that contacts Garmin outside the authenticated session. A
build tag alone therefore dispatches nothing — verified by running
`go test -tags=garminlive ./live/...` with no acknowledgement and no credentials
through a recording proxy, which observed no connection at all, and by a
gate-free test that counts fetch **attempts** rather than inspecting the returned
catalog, since an unreachable network would otherwise make a leak look like a
pass. `TestEverySweptToolDeclaresItsShape` stays gate-free and network-free.

On the first run the account held **zero** activities and an empty workout
library, so everything needing one skipped — including the FIT cross-check, the
whole reason this layer exists. The skip stated the account's own activity
count, so an empty account was never mistaken for a listing this server can no
longer read.

**Second run, after one activity with a device file and one workout were added
to that account: the whole suite passes.** Every previously skipped check ran:
the FIT-against-summary cross-check, the session-coverage invariant, the nine
activity-scoped tools, `get_activity` and `get_activity_fit_data` agreement, and
`get_workout_by_id`/`download_workout`. The decoded device file reproduced
Garmin's own distance, elapsed time, heart rate, ascent and calories inside the
stated tolerances, so ADR 0007's replacement is now confirmed against the real
service and not only against three files decoded by hand.

**The layer earned its keep on that run.** `get_personal_record` — registered,
shipped and green in every fixture test — failed against the live account with
`malformed_payload`. Garmin sends `prStartTimeGmt` as a number, an epoch in
milliseconds, and the model demanded a string, so the tool was broken for every
real account. The fixture had declared `prStartTimeLocal` as a string and
omitted `prStartTimeGmt` entirely: it was written to the same wrong assumption
as the model, which is exactly the blind spot this layer exists to remove. The
fields are `client.Text` now, and a regression test pins both the numeric and
the string form.

### The write half

The write half drives **every registered write and destructive tool but one**
against the real service — 38 of the 39 as of 2026-08-17, and the count is
deliberately not restated per slice, because an accounting test is what keeps it
true and a number in prose is what goes stale. `upload_workout` is the one
exclusion, and it is recorded rather than silent: `upload_workouts` sends the same document to the same endpoint
through the same api-layer method and additionally proves the per-item reporting
the single form has none of. An accounting test fails when a registered write or
destructive tool is neither exercised nor excused with a reason, so this list
cannot decay.

What makes it safe is structural, not conventional:

- A **write caller** refuses any mutating request whose target is not an object
  this suite created, before the request leaves the process. The recognised
  endpoint set is an allowlist, so a mutating endpoint a later slice adds is
  refused until the guard is taught how to own its objects.
- The **ownership ledger has no way to declare ownership**. It exposes three
  entry points and every one of them verifies: `ownCreated` reads the assigned
  identifier out of Garmin's own create response, so a tool that creates and then
  immediately mutates its own creation inside one call still passes; `ownSwept`
  parses the object's name and requires an earlier run's stamp; `ownScheduled`
  takes a calendar entry that was read back and names an already-owned workout,
  so the entry and the workout it belongs to come from the same answer rather
  than from a caller. Go has no file-level visibility, so this is a boundary
  every path respects rather than one the compiler draws — what it does remove is
  any way to *assert* ownership without evidence.
- Both halves of that guard have tests, and one of them drives `delete_activity`
  through the whole real stack — registry, policy with both tiers enabled and
  both scopes held, confirmation middleware with a consenting client — against
  a non-owned identifier, and it is still refused.
- Every created object is removed: by `t.Cleanup` so a failing assertion still
  cleans up, and by an end-of-suite pass over anything the ledger still holds. A
  removal that fails is reported and never swallowed.
- Every created object carries a generated name — the reserved
  `garmin-mcp-live-` prefix, a label, the run stamp and a counter. The sweeper at
  suite start removes a leftover **only** when that whole shape parses and the
  run stamp lies between a compiled-in floor and the instant the current run
  began, so a prefix alone is never taken for ownership and nothing the current
  run created can be swept. The residual — a hand-written name that reproduces
  the exact generated shape with a past stamp — is stated in the code rather than
  hidden. The read half skips anything merely carrying the prefix, which is the
  safe direction for a reader.

A second adversarial review found six defects in the suite's own safety
machinery, and all six are fixed:

- **A create response's identifier was taken as ownership.** It is a number the
  service chose: deduplication, a cache or drift could name an object the suite
  never created, after which the guard would permit mutating and deleting it —
  inside one call, for the three tools that create and then write to their own
  creation. The guard now reads the created object back and admits it only when
  it carries the name the create sent, and `ownCreated` takes that binding rather
  than a response body.
- **The read-only guard judged `GetBody` and dispatched `Body`.** Those are
  independent fields, so a benign query in the replay copy admitted a mutation in
  the body. It now reads the bytes that will be sent, judges those, and puts
  exactly them back.
- **The sweeper's licence was too wide.** Its floor predated the suite's own
  first run, and the name parser accepted empty labels, unknown labels and
  non-positive counters — none of which a generated name has. The floor is now
  the month the write half was written, labels are a declared closed set, and the
  counter must be positive. Name matching still cannot prove creation ownership,
  and the code says so.
- **Deletes released the ledger entry on the tool's own report.** A stale success
  or a no-op removal left a real object untracked, invisible to the leak report
  and beyond any retry. An entry is now released only after the object is proven
  absent, and an object that cannot be proven absent stays in the ledger so the
  cleanup retries it.
- **The absence proofs failed open.** The calendar accepted the first omitted
  result from a gateway the code itself documents as lagging in both directions,
  and the workout path read *any* tool error — a rate limit, an expired session,
  a decode failure — as proof of deletion. Absence now needs two consecutive
  agreeing reads, and for a record it needs the tool layer's own not-found
  advice, never "an error occurred, therefore it is gone".
- **"Exactly one outcome per requested item" was not tested.** Aggregate counts
  and slice lengths pass on a batch that reported the first item twice and
  omitted the rest. Every outcome is now matched against the identifier, date and
  status sent at that position, and the identifiers must be distinct.

Two of the suite's package-level mutables are gone with it: the name counter and
the run stamp are per-run state on the environment, fed by one injected clock, so
the stamp every name carries and the cut-off the sweeper compares against are the
same instant. One package-level handle remains, because `go test` gives a suite
exactly one non-test entry point and hands a test nothing but its own `*testing.T`;
everything a run accumulates lives inside the environment it holds.

**Re-run on 2026-08-15 against the dedicated test account after both rounds of
adversarial review fixes, all four gates open: the whole suite passes**, with one
skip. It was run twice: the second run's sweeper reported nothing, which is what
proves the first left nothing behind.

- `TestLiveWorkoutLifecycle` — create through `create_run_workout`, read back,
  schedule, `update_workout` in place, the calendar entry still points at the
  same workout afterwards, unschedule, delete, gone.
- `TestLiveManualActivityLifecycle` — create, read back, all six metadata writes
  and their read-back, delete, gone.
- `TestLiveStrengthActivityLifecycle` — create with sets, replace the whole set
  list, re-read and compare position by position, delete, gone.
- `TestLiveWorkoutBatchToolsApplyEachItemSeparately` — `upload_workouts`,
  `schedule_workouts`, `schedule_week`, `unschedule_workouts`,
  `delete_workouts`, each item applied and reported on its own.
- `TestLiveRemainingWorkoutBuildersUpload` — the three other builders.
- `TestLiveDownloadActivityFileAnswersForEveryFormat` — `fit`, `tcx`, `gpx`.
- The four guard tests.
- `TestLiveGearLinkAndUnlinkOnACreatedActivity` **skipped**: the account links no
  gear to the activity the read half analyses, so no gear identifier can be
  derived. The skip states the reason. Link a piece of gear to that activity and
  the check runs.

Nothing leaked: the run ended with no outstanding-object report, and the
following run's sweeper removed nothing, which is what proves it. The account
still holds exactly the pre-existing activity and workout it started with — the
FIT cross-check, the session-coverage invariant, the nine activity-scoped tools
and the derived-argument tests all selected them in the same run and passed.

### A third adversarial review found six more, and all six are fixed

The review cleared three areas outright — the read guard, the coordinate scrub and
the two file splits — and they are untouched. What it found:

- **The ownership read-back checked the name and not the identifier.** A generated
  name carries a one-second run stamp and a per-run counter, so two runs starting
  inside the same second render byte-identical names: a stale or drifted create
  identifier naming the *other* run's object satisfied a name-only comparison. The
  fixture that let this pass omitted the identifier from the read-back entirely, so
  no test could tell the two apart. The read-back now returns the identifier as
  well as the name, `ownCreated` requires the fetched object to report the
  identifier being adopted, and the fixture carries one — with a case whose
  read-back names a *different* object under the right name, which fails without
  the check.
- **The sweeper could delete a concurrent run's objects.** Names carry whole
  seconds and the cut-off carried nanoseconds, so a run starting later in the same
  second read a live run's stamp as strictly earlier and swept it. The cut-off is
  now truncated to the resolution the name carries, so two runs in one second
  compare equal and equal is not earlier. Two further holes in the same parser: it
  accepted integer spellings `strconv.FormatInt` cannot produce (`+1`, zero
  padding), which are now refused by round-tripping every numeric field; and the
  stamp floor sat at the midnight *before* the write half existed, admitting almost
  fifteen hours of seconds no run of it ever stamped. The floor is now the author
  instant of the commit that introduced the write half, and the test asserts
  equality rather than a range, so loosening it fails.
- **The FIT affordability assertion priced one pass and the analysis makes five.**
  `deriveSegment` walks a span's records through `distanceOf`, `ascentOf`, the
  accumulator loop, `powerSeries` and `dynamicsOf`. The honest worst case is
  therefore **66,300,000 record visits** — 221 spans (20 sessions, 200 laps, the
  whole-activity segment) walked 5 times over 60,000 records — plus **19,094,400**
  per-second series elements in normalized power, each costing a fourth power. It
  was measured end to end at **0.85 s of CPU** on the development machine, roughly
  a third of it the series. Both figures are now asserted at exactly the current
  product, so any widening of any bound fails; the reviewed-not-enforced status of
  the walk count is written into the test rather than implied. The bounds were
  **not** lowered: 0.85 s is proportionate to a call that first streams a 12 MB
  device file, and it is now interruptible — `AnalyzeFIT` takes a
  `context.Context`, checks it between every whole-activity stage and before every
  span, and reports a cancelled caller as itself. The reviewer's "roughly 152 MB of
  power-series allocation" is right as a *cumulative* figure and is not a memory
  figure: the series are built and dropped one span at a time, so peak residency is
  one 86,400-element buffer, about 691 KB.
- **The workout identifier comparison was not exact.** `client.Number` parses
  through `ParseFloat`, so `Int64()` truncated: an answer naming `123.9` compared
  equal to a requested `123`, and at 2^53 two identifiers one apart compared equal
  in either direction. `Number` now keeps the payload's own spelling and
  `Int64Exact` answers from it, refusing a fractional literal, an exponent form and
  anything outside the int64 range rather than rounding into a neighbouring object.
  `Update`, its 204 read-back and the live sweeper's delete target all use it.
- **Two calendar absences do not prove a deletion, and now say so.** The record
  paths were already correct — they require the tool layer's own not-found advice,
  and a rate limit or a 401 does not count. The calendar has **no authoritative
  not-found**: the GraphQL gateway answers a day with the entries it holds, and an
  entry that never replicated is indistinguishable from one that was deleted.
  Repetition raises the number of replicas that must all have missed it and cannot
  rule out one lagging replica answering every read. The proof is now a value
  carrying its own strength — three agreeing reads for the calendar against two for
  a record — and the code states the residual plainly instead of implying
  certainty. What actually guarantees the calendar is clean is the removal of the
  workout template the entry points at, which *is* proven authoritatively and which
  every scheduling test performs.
- **A raw transport error was printed with `fmt.Fprintf`.** A `*url.Error` carries
  the request URL, and for this suite that URL is a Garmin object path. Every
  diagnostic in `live/` now goes through one structured `slog` logger to stderr,
  and every error through `safeError`, which renders a `*client.APIError` with the
  request layer's own redacting renderer, names a cancelled context as itself, and
  reduces everything else to the Go type of the deepest error in the chain.
- **Four package-level mutables.** Three were `time.Date` values that cannot be
  constants; each is now a function. The fourth, `theWriteSuite`, cannot move —
  `go test` gives a suite one non-test entry point and hands a test nothing but its
  own `*testing.T` — so it and `live_test.go`'s three start-up handles are now
  recorded as the two named exceptions in AGENTS.md's own rule rather than left
  silently violating it.

### The write half earned its keep too: two shipped tools were broken

Both were green in every fixture test and both failed on first contact with the
real service. Neither could have been caught by a fixture, because in both cases
the fixture was written to the same wrong assumption as the code.

**`update_workout` failed on every real update.** Garmin answers an in-place
workout `PUT` with **204 and an empty body** — it names neither the workout nor
the name it stored. `SavedWorkout.ID()` then reported `malformed_payload`, so
the tool returned an error for an update that had already succeeded. The fixture
had scripted a `200` with a full body, which no deployment sends. `Update` now
reads the workout back when the answer carries no identifier, which keeps the
rule the type documents — the identifier and the name are the server's, not the
caller's — instead of echoing what was sent.

**`set_activity_strength_exercise_sets` and `create_strength_training_activity`
failed on every real write.** Garmin refused the set list with HTTP 400 and
`{"message":"Activity ID should not be Null in the Exercises Object"}`. The
replace-all envelope carried only `exerciseSets`; Garmin also requires
`activityId` at the **envelope root**, and it wants it there specifically —
repeating the identifier inside a set or inside an exercise object leaves the
same refusal. `renderSets` now names the activity in the envelope, and the unit
test asserts it with the reason written down. Both tools work live now.

**One catalog entry does not survive a real write.** With the envelope fixed,
Garmin rejected the `SQUAT` / `BACK_SQUAT` pair with
`{"message":"Invalid Sub-Category Passed in the request"}`, while
`BENCH_PRESS` / `BARBELL_BENCH_PRESS` and any known category with a null
exercise name were accepted. That is a state
`internal/garmin/api/exercisecatalog.go` already documents — it is "a documented
subset, not a mirror", and "a name it lists is still rejected if Garmin's enum
disagrees" — so it is recorded rather than patched: verifying the whole
compiled-in catalog against the live service is its own slice, and guessing at
one entry would not make the rest trustworthy. The live suite uses a pair
Garmin accepts.

**One behaviour of the service is worth recording for anyone writing calendar
code.** Garmin serves the workout calendar from a GraphQL gateway that does not
always answer with an entry the REST tier accepted a moment earlier, and the lag
runs in both directions — a removed entry can still be listed. The live suite
re-reads a bounded number of times rather than asserting on the first answer; it
is a wait, not a weaker assertion, because the bound still fails.

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
- **`get_exercise_types` reads Garmin's published catalog once at start-up**,
  from the compiled-in URL
  `https://connect.garmin.com/web-data/exercises/Exercises.json`, and serves that
  immutable snapshot for the process lifetime. The compiled-in subset of the FIT
  `exercise_category` enum remains **only as the fallback** for a read that
  failed, and the result names which catalog answered in a `source` field. The
  published document is preferred over the vendored FIT profile because the two
  sets differ in both directions: the web catalog carries values Garmin's own
  client writes that the enum cannot express — bare category-name entries, and
  names with a leading digit such as `_3_WAY_CALF_RAISE` — and it carries muscle
  groups, which the enum has no equivalent for. The read is anonymous, bounded,
  refuses a redirect, refuses a document smaller than the compiled-in subset, and
  cannot fail a start-up. Categories are validated against a closed set — the
  fetched catalog merged over the compiled-in one, so the fetch only widens it —
  and an exercise name gets a lexical check only, with Garmin authoritative.
- **`get_workout_by_id` serves the numeric identifier only.** The UUID form that
  adaptive Garmin Coach plans use is not served.
- **`decoupling_percent` carries the opposite sign to upstream's
  `hr_drift_pct`.** This server reports `(first - second) / first * 100` over the
  per-half power-to-heart-rate ratios, which is the standard convention: positive
  means the ratio fell, negative means it rose. Upstream computes the inverse and
  still calls it drift, so its label contradicts its own sign. The arithmetic here
  is not changed to match; the convention is stated in the `api.FITDrift` doc
  comment and in the schema description, and the reasoning is in
  `docs/parity.md`. **No interpretation label is served** — upstream's
  `well_coupled` needs a threshold nobody published, so a label would be an
  invented cut-off served as a finding.
- **`get_activities` returns three keys the manifest does not pin.** `steps`,
  `elevation_gain_meters` and `elevation_loss_meters` are on each list entry, as
  upstream returns them. The manifest record pins the input schema only, so the
  naming follows this server's own list result. All three are omitted when the
  activity does not carry them.
- **`get_activity_fit_data` reports `descent_meters` and `max_cadence`** beside
  ascent and average cadence on every session, lap and whole-activity segment,
  from the FIT profile's `total_descent` and `max_cadence` by the same route
  ascent and average cadence take, with the record-derived walk as the fallback in
  both directions. Ascent and descent are absent, not zero, when the file carries
  no altitude series; a stream that carried altitude and did not move reports a
  measured zero.
- **The FIT cadence keys name no unit**, where upstream's say `_rpm`. Only the
  session and lap fields are sport-dynamic — on a running session they are
  `avg_running_cadence` and `max_running_cadence` in strides per minute — so the
  suffix is wrong for every run there. `Record.Cadence` has no dynamic form and is
  always rpm, so the descriptions split by surface: segments say rpm or
  strides/min, and everything derived from the record stream says rpm.
  `average_cadence` was corrected together with the newer `max_cadence`.
- **The whole-activity FIT summary refuses a fold over a subset of sessions.**
  When a multisport file's sessions disagree in provenance, the folded total, peak
  or average is absent and the complete record-derived figure stands. A total over
  a subset under-reports, a peak over a subset is a lower bound printed as a
  maximum, and neither says a session is missing from it. Every folded field was
  audited against its fallback; `total_calories` is the one with none, so there
  absence is terminal and the per-session figures carry what is known — unless
  `sessions_truncated` is set, when even those are a subset.
- **A truncated FIT decode reports absence rather than a prefix.** Past a decode
  bound the retained stream is a part, so figures derived from it are left absent
  and the whole-stream aggregates — curve, grade bands, temperature split,
  decoupling — are not computed. Device figures are untouched, and lists of
  detected events are kept with their own truncation flag. `samples_truncated`,
  `sessions_truncated` and `laps_truncated` are reported separately, because each
  voids only what it touched: lap truncation voids nothing but the lap list, and
  in particular does not disable the session fold. A suppressed segment also
  withholds `end_time` and `duration_seconds` unless the file declared the window,
  since the last retained sample is where the bound fell rather than where the
  segment ended. `get_power_duration_curve` skips a truncated file rather than
  folding a lower bound into a season best.
- **One tool is left unregistered rather than stubbed**: `set_fit_download_dir`
  (it would persist a caller-supplied server filesystem path, and is refused by
  design). `get_activity_fit_data` was on this list before FIT decoding landed;
  it is registered now. The three calendar tools that were also unregistered —
  `get_scheduled_workouts`, `get_training_plan_workouts` and `schedule_week` —
  are registered now that the client layer builds the GraphQL request they need.

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

The **coverage threshold** gate already existed in `ci.yaml` (the `test` job's
"Enforce the per-package coverage floor" step), contrary to what this file
previously said; it computes each package's percentage from `cover.out` and
fails in both directions against the explicit exception list
(`cmd/garmin-mcp`, `internal/garmin/auth`). A bounded **fuzz smoke** job
(`fuzz-smoke`) and a **two-clean-build reproducibility** check
(`reproducible-build`) are now added too. **Container image signing,
container image and per-binary SBOMs, and build provenance attestation** are
now configured; each is described below, and each carries an explicit note on
what is proven locally versus what only a real tag push can prove. The MCP
conformance job is not on this list: it is blocked upstream, not unstarted.

- Supply-chain attestation beyond checksum signing is **out of scope by
  decision**, not pending. `.goreleaser.yaml` signs the checksum file
  (`signs: artifacts: checksum`, keyless cosign `sign-blob`) and that stays: it is
  the minimal integrity guarantee, it needs no extra machinery, and a downloader
  can verify an archive against it.

  Container image signing, SBOMs of any kind, and build provenance attestation
  were configured and then removed. They are not wanted here. The removal also
  deletes their failure modes: every one of them could only ever run inside a
  tagged release job with a live OIDC token, so none was verifiable before the
  first real tag, and each was a step that could fail a publish part-way for
  reasons unrelated to the artifact being correct. `--skip=sbom` is gone from the
  CI snapshot invocation with them, and a local snapshot now produces exactly four
  archives and no SBOM documents.

- The CI unit job writes `cover.out` with `-covermode=atomic`, and the `test`
  job's coverage-floor step enforces the documented 80% rule per package,
  checked in both directions against the exception list. See
  [Measured coverage](#measured-coverage) for the current numbers.
- Fuzz targets exist for the parsers most exposed to untrusted or drifting
  input: `internal/garmin/protocol` (`FuzzClassifyJSONLogin`,
  `FuzzClassifyWidgetPages`, `FuzzParseWidgetMFAVars`), `internal/garmin/client`
  (`FuzzNumberUnmarshalJSON`, `FuzzTextUnmarshalJSON`, `FuzzParseDate`),
  `internal/tools` (`FuzzSanitizeUntyped`) and `internal/garmin/api`
  (`FuzzParseFITActivity`). The `fuzz-smoke` CI job discovers every declared
  `FuzzX` function and runs each for a bounded 10s; none performs I/O or reaches
  the network. The race detector is deliberately off in that job: it slows
  fuzzing by about an order of magnitude, and on a GitHub runner that made the
  engine miss its own shutdown deadline and fail the build on timing rather than
  on a finding. Those parsers still run under `-race` in the unit, fakegarmin
  and e2e jobs.
- The `reproducible-build` job builds `cmd/garmin-mcp` twice in one job, each
  from its own `GOCACHE` so the second build is a real recompile, with
  `-trimpath` and fixed `-ldflags` version/commit literals, and fails if the
  two binaries' SHA-256 hashes differ.
- GitHub-native **secret scanning** is a repository setting, not a workflow file,
  and still needs enabling. Dependency and license review **is** an enforced CI
  gate: `ci.yaml` runs a SHA-pinned `actions/dependency-review-action` on
  `pull_request` with `fail-on-severity: low` and an explicit `allow-licenses`
  list that matches `docs/dependencies.md`.

### Commands: no declared gaps remain

`garmin-mcp migrate` and `garmin-mcp tools list` were the last two commands that
validated configuration and then returned a `*cmd.NotImplementedError`. Both are
wired now: `tools list` prints the registered surface with its tier and effect
and exits 0, and `migrate` applies the embedded migrations, refusing with a
configuration error when no database path is set rather than guessing a
location. The `cmd.ErrNotImplemented` sentinel no longer appears anywhere in the
source.

### Platform and environment limits

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
- `FileStore` now serializes one principal's record across processes with an
  `flock(2)` advisory lock held for the whole read-modify-write, alongside the
  per-principal in-process mutex. Key rotation forced this: `rotate-key` is a
  separate process from `serve`, and the prescribed content-equality re-check
  narrows the window without closing it, because a Go-level read-then-write is
  two operations rather than one atomic engine statement. The lock is advisory
  and host-local, so the store must not sit on a network filesystem. Both
  deployments stay single-active-instance by design; the change is that a second
  process can no longer silently overwrite a newer record with an older one.
- `modernc.org/sqlite` is a direct dependency, and `modernc.org/libc` must move
  only with it. Both are on the manual-review list in `.github/renovate.json` and
  neither may be automerged.

### Storage and key-management gaps

- Key rotation is **landed**, not open: `garmin-mcp rotate-key` re-seals every
  sealed record in both backends, and `docs/operations.md` §4 carries the
  procedure. The residual limits are that it is offline, that the retiring key is
  never deleted automatically, and that a FileStore run can only speak for the
  principal the configuration binds. This entry described it as unavailable for
  several commits after it shipped.
- Backup and restore are **out of scope by decision**: the database sits on an
  operator-controlled volume and backing it up is the operator's job.
  `docs/operations.md` carries the procedure, including that the database and the
  master key are two halves of one backup and that a restore rolls consents back
  to the backup's moment.
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
- The widget MFA path now parses the page's inline JS variables and requests code
  delivery. `ClassifyWidgetLogin` reads `customerGuid`, `mfaMethod`, `locale`,
  `clientId` and `codeSentTo`, the parsed method outranks the title guess, and
  `Authenticator.requestWidgetMFACode` POSTs `PathWidgetRequestMFACode` when the
  method is email or SMS and no code has been sent yet. A confirmed delivery
  clears `MFADeliveryUncertain`, which now means the code may not have been sent
  rather than that this server never asked. A failed request does not fail the
  login, deliberately, and is never retried. A rejected OTP now has its own
  outcome: `protocol.OutcomeMFARejected`, produced only by
  `ClassifyMFAVerifyJSON` and `ClassifyMFAVerifyWidget`, which classify the
  mobile/portal/widget verify responses specifically and reinterpret a
  credential-shaped rejection there — never a bare status, never the initial
  credential POST — as a wrong code rather than a wrong password. This is not an
  upstream distinction: 0.3.10 folds both into one `GarminConnectAuthenticationError`.
  The registry's existing lease and attempt-budget behavior already kept the
  transaction retryable on any verify failure, so the addition is classification
  and error-matching (`errors.Is(err, protocol.ErrMFARejected)`) only.
- The `JWT_WEB` cookie fallback is **deliberately not ported**. It was built,
  reviewed and then removed, and the reason is architectural rather than a
  matter of effort. Upstream recovers with it because one long-lived Python
  process holds the fallback session and the next API call in the same
  in-memory object. This server has no such continuity: every tool call
  authenticates through `Refresher.Do`, which reads the **persisted** per-principal
  DI token set, and upstream itself never persists `jwt_web` (`Client.dumps`
  serializes only `di_token`, `di_refresh_token` and `di_client_id`, and its
  own JWT_WEB refresh depends on the CAS ticket-granting cookie inside the same
  in-memory session). On stdio the `auth` command exits before `serve` starts,
  so process memory cannot bridge the two. A credential that no later call can
  read is not a fallback.

  The removal followed a working implementation, so what it cost is recorded
  honestly: the port passed both tagged suites, sealed the cookie two pointers
  deep with alias-stripping leak tests, and narrowed the trigger well below
  upstream's bare `except Exception`. What it could not do was reach a second
  call. Review also found that shipping it would have made a previously
  unreachable state reachable in `internal/cmd`: `remoteLogin.bind` resolves and
  **links** a Garmin account durably before `commit` stores the token set, so a
  result carrying no token set would have linked an account and then failed, while
  `internal/cmd/tty.go` printed "the tokens are stored encrypted". Reintroducing
  `JWT_WEB` requires a deliberate durable credential lifecycle first — a
  process-local map is not one — and is not currently in scope.

  Related latent defect, independent of `JWT_WEB` and **not fixed**: that same
  resolve-before-commit ordering means any `commit` failure — expired staged
  tokens, a cancelled token gate, a store read or save error — leaves a
  principal created and a Garmin account linked with no token set. It is
  self-healing on retry, because the next successful login resolves the
  already-linked principal and commits onto it, which is why it is recorded here
  rather than treated as a release blocker. The durable fix is one transaction
  spanning principal creation, account linkage and the token write; reversing the
  call order alone cannot work, because the token row requires the principal.
- The login state machine is still not on the top-level login path.
  `auth.Machine` is exhaustively tested, but `Authenticator.Login` and
  `CompleteMFA` report progress through `Result` states, and the machine is
  stepped only by the MFA transaction registry. Either route the top-level flow
  through it or state plainly that it governs transactions only.
- The container job now proves start-up, not only that the binary executes. Beyond
  the original **nonroot, read-only, shell-free** smoke test it runs the image with
  a volume at `/data`, polls `/readyz` until the server reports ready rather than
  merely alive, and checks that the database and the encryption key appear under
  that volume owner-only. A read-only `/data` must make start-up fail promptly:
  every reserved docker exit status is rejected and the log must name a read-only
  filesystem, so an image whose entrypoint cannot execute at all — which would
  otherwise satisfy "it failed, as expected" — fails the job instead. Verified
  locally against a real engine: ready on the first poll, both files `600` owned
  `65532:65532`, and the read-only case exiting `1` with
  `mkdirat keys: read-only file system`.

  One operational fact this surfaced, and it matters for any test deployment: the
  authorization server refuses to name a cleartext issuer at **any** origin,
  loopback included, and `allow-insecure-http` does not cover that refusal
  (`internal/cmd/remoteendpoints.go`). A remote deployment therefore cannot be
  smoke-tested over plain HTTP even on `127.0.0.1`; the job generates a
  throwaway self-signed certificate.
- The parity extractor scripts are not committed; `docs/parity.md` documents the
  algorithm instead. The obligation is **narrowed rather than open**: a full Go
  re-implementation is rejected (AST extraction over Python source needs either a
  third-party Go Python parser, which this repository requires an ADR and a
  notices entry for, or pattern matching that emits wrong contracts the first time
  upstream reformats), and committing the Python scripts has an unmet
  prerequisite — a committed generator becomes CI's authority, so it must first
  reproduce the **reviewed** artifacts byte for byte, and these manifests have
  been hand-corrected since generation.

  What is enforced instead, offline and in the ordinary `test` job, is the
  coupling: `TestUpstreamPinsAgreeBetweenTheDocAndBothManifests` fails when
  `docs/upstream-pins.md` and the commit embedded in either manifest disagree.
  That is the failure a pin bump actually has, and it was silent before. Content
  regeneration stays the documented manual procedure in `docs/parity.md`.

## The write safety delay exists

`AGENTS.md` instructed for a long time that a configurable safety delay be applied
before write and destructive execution, and nothing implemented it. All 23 write and
5 destructive tools were registered under that instruction without one. It is now
built, and the instruction that described it as a per-tool step is gone: a tool
inherits the delay from its tier and must never carry a sleep of its own.

Where it lives: `Server.awaitSafetyDelay` in `internal/mcpserver/middleware.go`,
inside the policy middleware, after the tier and scope gate and after destructive
confirmation. The setting is `config.Config.SafetyDelay`, flag `--safety-delay`,
default `0`, ceiling `MaxSafetyDelay` of 5 minutes.

Four properties are pinned by tests in `internal/mcpserver/safetydelay_test.go`, and
each was checked against a mutant that breaks it:

1. Writes and destructive calls wait; reads never do.
2. Zero disables the pause, which is what every existing deployment gets.
3. A cancellation during the wait stops the call: the handler never runs.
4. A refused call never waits, so a refusal costs neither the server the wait nor
   the prober the timing signal.

`internal/cmd`'s `TestServeCarriesTheSafetyDelayIntoTheServer` pins the wiring, so a
setting that parses and validates but never reaches the middleware fails the build.

What it deliberately is not: a second confirmation. Destructive tools already require
elicitation that fails closed. The delay's value is on the write tier, which has no
interactive gate, and there the cancellation window is the only one a caller gets.

## The five MCP resources are served

`internal/resources` publishes all five documents the pinned upstream declares:
four workout templates and the structure reference. They are compiled in, reach no
Garmin endpoint, and carry nothing of the caller's, so they are registered through
`mcpserver.AddResource` rather than as tools and hold no tier.

Why they are not gated like tools, deliberately rather than by omission: the rate
limiter and the logging middleware both scope themselves to `tools/call`, and
`internal/ratelimit/middleware.go` states the reason — reading a resource costs the
Garmin account nothing, so charging a budget for it would only make discovery
unreliable. That reasoning holds exactly for a constant document. The gate that does
apply on remote is the HTTP layer's, which authenticates every `POST`, `GET` and
`DELETE` before dispatch, so a resource read still needs a verified bearer token.
Principal resolution already ran for every method, not only tool calls.

What is pinned by tests: the manifest set in both directions, each resource's name,
description and media type against the manifest, that every document renders and
parses, that every template is accepted by this server's own upload path, that no
`stepOrder` repeats inside a document, and that the structure reference lists every
value the templates use. At the server layer: listing and reading over a session, an
unknown URI refused, a duplicate URI refused before it reaches the SDK — whose own
`AddResource` replaces on conflict — a scheme-less URI refused before the SDK panics
on it, and a nil registrar refused at construction.

The one thing not claimed: byte-identical template contents. The contract fields and
the vocabulary are upstream's; the step counts, durations and descriptions inside
each template were written here. `docs/parity.md` says so rather than implying
equivalence that was never verified.

## Next task

The tool surface is finished: 137 of 138 manifest tools and all 5 resources, with
the one refusal documented. Phase 5 is closed. What follows is not breadth.

In order:

1. ~~A store-level key-rotation driver.~~ **Landed.** `garmin-mcp rotate-key`
   re-seals every sealed record in both backends, resumably and without
   double-sealing, with the CAS interaction inside `internal/store`. ADR 0005's two
   open items are closed; see `docs/operations.md` §4.
2. ~~A Go parity regenerator plus a CI drift check.~~ **Narrowed and closed.** A
   full Go re-implementation is rejected on dependency and correctness grounds,
   and committing the Python extractor requires a golden reproduction of
   hand-corrected manifests that does not exist. The pin/manifest coupling is
   enforced by a test instead, which catches the failure a pin bump actually has.
   See `docs/parity.md`.
3. **The `remoteLogin.bind` half-write.** `resolve` creates a principal and links
   a Garmin account durably before `commit` stores the token set, so any commit
   failure leaves a linked principal with no tokens. It self-heals on retry, which
   is why it is recorded rather than treated as a blocker. The fix is one
   transaction spanning principal creation, account linkage and the token write;
   reversing the call order cannot work, because the token row requires the
   principal.
4. **The final security review**, last, once the above are in.

Explicitly **not** work, so that a cold agent does not go looking for it:

- MCP conformance is blocked upstream, not unstarted. ADR 0002 and the section
  above carry the evidence.
- The `JWT_WEB` cookie fallback is deliberately not ported.
- Windows is deliberately unsupported.
- Backup and restore are the operator's responsibility, documented in
  `docs/operations.md` rather than tested here.
- SBOMs, container image signing and build provenance were configured and then
  removed by decision. Checksum signing stays.
- `docs/operations.md` **exists** — eight sections, including remote deployment,
  TLS, the database, key management, revocation, lifetimes, upgrades and the
  limits. Several places in this file used to claim it did not.

A note on reading this file at all: it has repeatedly said work was outstanding
after that work was finished, and the checkbox state was wrong in three places at
once while every one of those entries' own prose said "done". A cold agent resumes
from `AGENTS.md` plus this file, so verify a claim against the repository before
acting on it, and fix the line in the same commit as the work.
