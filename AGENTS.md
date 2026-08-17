# AGENTS.md

Guidelines for humans and AI agents that contribute to garmin-mcp.

**How to read this file.** Sections marked **[NOW]** describe code and
configuration that exist in the repository today. Sections marked **[TARGET]**
describe the architecture being built. A [TARGET] statement is a binding design
decision, never a claim that the code exists. Do not cite a [TARGET] section as
evidence that something works.

## Project Overview [NOW]

garmin-mcp is a Model Context Protocol (MCP) server that exposes Garmin Connect
data and operations as MCP tools. It is a native Go rewrite: no Python, no
Garth, no Python subprocess.

- Go module: `github.com/tamcore/garmin-mcp`.
- MCP layer: the official `modelcontextprotocol/go-sdk` (not `mark3labs/mcp-go`),
  `v1.7.0`, pinned by ADR 0002 and required in `go.mod`.
- Transports: stdio for local single-user use, Streamable HTTP for remote
  multi-user use. Both exist.
- Garmin Connect is an unofficial, undocumented private API. Endpoints, schemas,
  and WAF behavior can drift. Never add CAPTCHA bypasses, browser automation, or
  credential harvesting. This rule applies now, not later.

## Current state [NOW]

As of 2026-08-16 the repository contains the following Go code. Coverage is the
statement coverage reported by `go test -count=1 -cover ./...`, measured on that
date.

| Path | What is there | Coverage |
|------|---------------|----------|
| `cmd/garmin-mcp/main.go` | Thin `main`: passes the ldflags-injected `version` and `commit` into `cmd.Execute` and calls `os.Exit` with the returned code | n/a |
| `cmd/notices/main.go` | Thin `main` for the notices generator: flags, then `notices.Generate`. A maintenance tool, never linked into `garmin-mcp` | n/a |
| `internal/cmd` | Cobra tree and the composition root. `serve` (stdio and streamable-http), `auth`, `doctor`, `version`, `tools list` and `migrate` all do real work; no command returns a not-implemented sentinel | 80.8% |
| `internal/config` | `Config`, deterministic four-layer precedence, `_FILE` secret variants, full lexical validation, redacted output, and the operator OAuth client registry | 90.8% |
| `internal/garmin/protocol` | Garmin host/path/endpoint-label constants, client identities, DI client-ID candidates, the login response classifier (JSON and widget HTML), the rejected-OTP outcome, and the widget MFA variable parse. No I/O | 96.6% |
| `internal/garmin/auth` | Login state machine, strategy fallback, bounded MFA transaction registry with a single completion lease, DI ticket exchange, session validation, explicit widget MFA code delivery, refresh with per-principal collapsing and CAS, the shared `TokenGate`, the request-time host guard, unverified-JWT `exp` parsing | 66.1% untagged, 88.3% with `-tags=fakegarmin` |
| `internal/garmin/client` | The authenticated request layer: bounded wire and decompressed sizes, page and page-start caps, one bounded post-`401` retry that never replays a `POST` or `PATCH`, typed errors, and the exact-integer accessor an identifier is compared through | 94.3% |
| `internal/garmin/api` | Domain clients — activities, analysis, splits, profile, workouts, gear, strength writes, downloads, the published exercise catalog with its compiled-in fallback, FIT activity decoding through `github.com/muktihari/fit`, the training scores, thresholds and trends, nutrition, challenges and badges, and the device inventory | 90.7% |
| `internal/mcpserver` | Server, registry, stdio and Streamable HTTP transports, bearer middleware, session binding, origin and forwarded-header guards, elicitation confirmation, `server_info` | 89.3% |
| `internal/resources` | The five constant MCP documents — four workout templates and the structure reference — with the manifest contract, the render, and the check that this server's own upload path accepts every template | 95.7% |
| `internal/tools` | 142 registered tools — 98 read-only, 35 write, 9 destructive — the whole pinned manifest bar one documented refusal, with contracts snapshot-tested against `compat/tools.json` | 85.8% |
| `internal/policy` | Three tiers, explicit name lists validated against the registered set at start-up, the enablement-and-scope intersection, confirmation requirement | 91.7% |
| `internal/identity` | Principal type, request context, and the bearer resolver that takes the principal only from a verified token | 97.7% |
| `internal/oauthserver` | The authorization server: PKCE S256 only, exact issuer and redirect matching, single-use bound codes, hashed opaque tokens, rotating refresh with family revocation, consent | 92.4% |
| `internal/oauthstore` | The adapter from the authorization server's `Store` interface onto the SQLite store, with a compile-time assertion and five contention tests | 84.6% |
| `internal/store` | `FileStore` for stdio, plus the migration-backed SQLite backend for remote: principals, encrypted DI token sets with CAS, clients, consents, hashed transactions and codes, token families, audit events | 83.6% |
| `migrations` | The embedded, checksummed, monotonic SQL migrations `0001_initial.sql` and `0002_oauth_contract.sql` | 100.0% |
| `internal/cryptostore` | AES-256-GCM envelope encryption with versioned key IDs and principal/record-type AAD, and an owner-only key file | 89.9% |
| `internal/securefile` | The shared filesystem hardening every store uses: `os.Root` component-by-component path resolution, post-open identity verification, link-based exclusive install, non-blocking regular-file reads, owner-only modes, and Windows ACL evaluation | 84.5% |
| `internal/tokenlink` | `Store`, the adapter that makes a `*store.FileStore` satisfy `auth.TokenStore` by converting between the two packages' `TokenSet` types | 80.0% |
| `internal/loginweb` | The browser login flow in two profiles: the one-shot loopback profile and the remote profile with the `__Host-` cookie, HSTS, disclosure page, independent CSRF token, and server-held MFA continuation | 82.6% |
| `internal/mcplog` | Structured `slog` logging with the allowlisted field set, level mapping, and the stderr sink that refuses stdout | 98.5% |
| `internal/notices` | The `THIRD_PARTY_NOTICES.md` generator: the linked module set unioned over the six released targets, the curated SPDX and licence-file registry, verbatim licence copying, and the freshness test that fails on a stale notices file | 89.3% |
| `internal/ratelimit` | The per-principal limiter and its handler middleware | 94.8% |
| `internal/testkit` | Scripted fake Garmin service, fake clock, fixtures, synthetic FIT builder, transport guard | 91.5% |
| `e2e` | Build tag `e2e`. `cli_test.go` builds the binary and drives it as a subprocess: version output, clean stdout on the stdio path, unknown command | n/a |
| `live` | Build tag `garminlive`. The opt-in suite against the real Garmin service: one shared login; a read half behind three gates whose caller admits only reads and the GraphQL query documents the request layer itself renders; and a write half behind a fourth gate whose caller mutates only objects a verifying ownership ledger holds. Carries the FIT-against-summary agreement, tool-against-domain-client agreement, the read-only surface sweep in three halves (account, health, training), and the write and destructive surface end to end. Never in CI | n/a |

Everything else in the repository is documentation, contract manifests
(`compat/`), and CI, lint, pre-commit, GoReleaser, and container configuration.

Every package in the untagged profile is at or above the 80% rule below.
`internal/garmin/auth` is the one exception CI carries on merit: its login, MFA
and refresh paths are tagged `fakegarmin`, so the untagged profile sees 66.1% and
the tagged job reports 88.3%. `cmd/garmin-mcp` is the other, because it is the
process entry point and the `e2e` job runs the built command instead. CI enforces
the floor per package against that explicit list in both directions, so a package
that drops under it fails the build and a listed package that reaches it must
leave the list.

`go.mod` direct requirements: `modelcontextprotocol/go-sdk`, `spf13/cobra`,
`spf13/viper`, `spf13/pflag`, `golang.org/x/sys`, `modernc.org/sqlite` and
`github.com/muktihari/fit`, plus the transitive indirect set. `muktihari/fit`
decodes Garmin activity files and links no third-party package of its own; ADR
0007 records why the format is not hand-decoded. `golang.org/x/sys` is direct because
`internal/securefile` reads Windows security descriptors through
`golang.org/x/sys/windows`. `modernc.org/libc` moves only together with
`modernc.org/sqlite`. See `docs/dependencies.md`.

What `tools list` and `migrate` actually do: `tools list` reads the declared
tool contracts and prints each name with its tier and effect, without needing a
Garmin client, a token or a database. `migrate` applies the embedded migrations
to the configured database, and refuses with a configuration error when no
database path is set rather than guessing a location. The
`cmd.ErrNotImplemented` sentinel has been removed; no command returns it.

There is still no `LoginTransport` type (the
auth package uses a one-method `Doer` transport interface instead), no
`garminlive` command — the tag carries a test suite in `live/`, not a
subcommand — no fuzz target, no MCP conformance job, and no
`docs/operations.md`.

`internal/cmd` is the composition root and assembles the packages. It builds the
key, the store, the authenticator, the refresher, the policy, the tool
registrar, and — in remote mode — the authorization server, the SQLite store and
the HTTP transport. `TestRemoteAndStdioShareNoState` proves the two modes share
no state inside one process.

`docs/implementation-status.md` is the authoritative task and gap list. Read it
with this file before any work. Where this file and the repository disagree, the
repository wins, and fixing the file is part of the next commit.

The upstream baseline is `python-garminconnect` 0.3.10. The security behaviors
that release adds are required work for the auth and session slice, and they are
listed in `docs/upstream-pins.md`.

## Authorization model [NOW]

These boundaries stay separate at all times, and all three are enforced by code:

| Boundary | Credential | Rule |
|----------|-----------|------|
| MCP client to this server | This server's OAuth access token | Never forwarded to Garmin |
| This server to Garmin | Per-principal Garmin DI token set | Never returned to the MCP client |
| Browser to login transaction | One-time cookie plus server-side transaction state | Credentials never become MCP tool arguments |

### Safety model [NOW]

- Read-only tools are always registered. Write and destructive tools need the
  **intersection** of operator enablement and granted OAuth scope. Operator
  enablement alone is never sufficient.
- On stdio the scope source is empty by construction, so every write and
  destructive tool is refused there whatever the operator enables. On
  streamable-http the scopes come from the verified bearer token, so an operator
  who both enables the tier and registers a client carrying `garmin:write` gets
  working writes.
- Remote deployments default to read-only: both enablement flags default to
  false, and destructive enablement additionally requires write enablement.
- Destructive tools request confirmation through MCP elicitation and **fail
  closed**: if confirmation cannot be obtained (elicitation unsupported,
  declined, or timed out), the operation is refused and the refusal names the
  reason.
- Every principal owns its own Garmin client, token set, cookie jar, cache
  entry, and tool results. No global cross-user client exists.

## Development Workflow [NOW]

1. **Plan** before writing code for non-trivial changes.
2. **TDD** per behavior — write the failing test (RED), implement the smallest
   coherent behavior (GREEN), then refactor while green (REFACTOR).
3. **Lint and vet** before committing: `golangci-lint run` and `go vet ./...`.
4. **Semantic commits** in small reviewable chunks — `feat:`, `fix:`,
   `refactor:`, `test:`, `ci:`, `docs:`, `chore:`. One behavior per commit.
5. **Status file** — every stopping point updates
   `docs/implementation-status.md` in the same commit as the work it describes.
6. **Per-commit CI rule** — after every push, wait for CI to go green before you
   stack the next commit.

### Commit attribution

`Co-authored-by` trailers name only the agent or person that actually touched
the code in that commit. Do not add a trailer for an agent that did not write
the change.

- Claude: `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`
- Codex: `Co-authored-by: Codex <noreply@openai.com>`

### Resume point

A cold agent run resumes from `AGENTS.md` plus `docs/implementation-status.md`
alone. If those two files are not sufficient to resume correctly, fix that
before any further feature work.

## Package Layout [NOW]

Most of the target layout exists. `exists` marks a path that is present today;
a path marked `planned` must still be created, and referencing it will not
compile.

```
cmd/garmin-mcp/          main package only                                    exists
cmd/notices/             main package only: the notices generator             exists
internal/
  cmd/                   Cobra commands and the composition root              exists
  config/                parsing, precedence, validation, redacted output      exists
  garmin/protocol/       endpoints, client identities, pacing, failure classifier  exists
  garmin/auth/           native login/MFA strategies, DI exchange, refresh, token gate  exists
  garmin/client/         authenticated HTTP, retry, bounds, typed errors      exists
  garmin/api/            domain clients: activity, analysis, profile, workout, gear, downloads  exists (health, nutrition, training, challenges, devices beyond get_devices still to come)
  mcpserver/             server, transports, middleware wiring                 exists
  tools/                 one file or domain file per MCP tool + register.go    exists (47 tools)
  resources/             the five constant MCP documents and their registrar   exists
  mcplog/                structured MCP logging, level mapping, transport sink  exists
  ratelimit/             limiter + handler middleware, keyed per principal     exists
  identity/              principal and request-context resolution              exists
  oauthserver/           MCP-facing OAuth AS/RS integration and consent        exists
  oauthstore/            oauthserver.Store implemented over the SQLite store   exists
  loginweb/              embedded templates and one-time login transactions    exists (loopback and remote profiles)
  store/                 storage interfaces, FileStore, SQLite implementation  exists
  cryptostore/           versioned envelope encryption and key rotation        exists
  securefile/            shared filesystem hardening for every secret file     exists
  tokenlink/             store-to-auth TokenSet adapter                        exists
  notices/               THIRD_PARTY_NOTICES.md generator and freshness test  exists
  policy/                scopes, write/destructive gates, limits               exists
  observability/         redacted logging, metrics, tracing hooks              planned (logging lives in mcplog; no metrics, no tracing)
  testkit/               fake Garmin, fake clock, fixtures, test keys          exists
e2e/                     end-to-end tests (build tag: e2e)                     exists (CLI-level only)
live/                    opt-in live tests (build tag: garminlive)            exists (read-only, three gates)
web/                     embedded HTML/CSS; no remote JS dependency            planned (pages are embedded under internal/loginweb/pages)
migrations/              embedded, monotonic database migrations               exists (0001, 0002)
compat/                  pinned tool and resource contract manifests           exists
docs/adr/                consequential design records                          exists
```

File discipline, in force now:

- All real code lives under `internal/`. `cmd/garmin-mcp/` and `cmd/notices/`
  hold only `main`.
- Every non-trivial `.go` file has a sibling `_test.go` in the same package.
- Files under 400 lines, functions under 50 lines, nesting depth under 5.
  Extract helpers before you cross those limits.
- No package-level mutable state. Pass `*config.Config`, stores, clocks, and
  loggers explicitly. A constant that cannot be a `const` — `time.Date` above all
  — is a function returning the value, never a `var`.

  There are exactly **two** exceptions, both in `live/` and both forced by
  `go test`: a suite gets one entry point that is not a test, `TestMain`, and a
  test can be handed nothing but its own `*testing.T`, so state that must
  outlive an individual test has nowhere else to live. They are
  `live/live_test.go`'s `stateDir`, `closers` and `shared`, and
  `live/writeenv_test.go`'s `theWriteSuite`. Each is written once during
  start-up and read afterwards, and everything a run accumulates lives inside
  the environment the handle points at, so the state is per run and explicit.
  Any further package-level `var` anywhere in the repository is a defect, and
  new state in `live/` goes inside `writeEnv` rather than beside it.
- Interfaces live with the consumer, not in a global interfaces package.

## Adding a New MCP Tool [NOW]

Every package this procedure names exists, and 47 tools already follow it. Copy
the closest existing tool in `internal/tools` rather than inventing a shape.

1. Take the contract (name, description, input schema, sensitivity, effect,
   scope) from `compat/tools.json`. That file is a **generated** snapshot of the
   pinned upstream commit and is not hand-edited: a tool that has no manifest
   record is an addition beyond the pin, and it is recorded in `docs/parity.md`
   and in the ADR 0006 register instead.
2. Add the failing contract test: registered name plus normalized schema
   snapshot against the manifest, or the documented-exclusion entry for a tool
   the manifest does not carry.
3. Create `internal/tools/<name>.go` with a `register<Name>(...)` function.
   - Use the upstream compatibility tool name unless there is a documented
     security reason not to.
   - Declare **all four** annotation hints explicitly: read-only, destructive,
     idempotent, and open-world. Garmin is an open-world API, so the open-world
     hint is always true.
   - Give a strict JSON schema with ranges, formats, and defaults.
   - Resolve the principal from the authenticated request context. Never accept
     `user_id`, email, token path, or any account selector as an argument.
   - Enforce OAuth scope and operator policy before you touch Garmin.
   - Bound result size, page size, and date windows.
   - Return sanitized errors: no tokens, cookies, raw bodies, health payloads,
     coordinates, or stack traces.
4. Wire it into `RegisterAll()` in `internal/tools/register.go`, in explicit
   tier order.
   - Read-only tools are always registered.
   - Write tools go in the `writeTools` name list and need write enablement plus
     a granted write scope.
   - Destructive tools go in the `destructiveTools` name list, need destructive
     enablement plus a granted destructive scope, and must call elicitation
     confirmation that fails closed.
   - Both name lists are validated at startup against the actually registered
     set, so a typo fails fast.
5. Nothing. The configurable safety delay is **not** a per-tool concern: it lives
   in the policy middleware, applies to the whole write and destructive tiers, and
   a new tool inherits it by being registered in the right tier. Never add a
   private sleep to a handler. The setting is `safety-delay`, it defaults to `0`,
   and `docs/configuration.md` states what it does and does not do. The "skip it
   for dry-run calls" clause this step used to carry is gone with it: this server
   has no dry-run mode to skip.
6. Write unit tests in `internal/tools/<name>_test.go` against the fake Garmin
   service from `internal/testkit`.
7. Add fake-service integration coverage (tag `fakegarmin`) and E2E coverage
   (tag `e2e`) where the tool crosses a transport or policy boundary.
8. Update `docs/parity.md` and `docs/implementation-status.md`.

## Testing

Four layers. The first three have a CI job each.

| Layer | Command | Build tag | What it tests | State |
|-------|---------|-----------|---------------|-------|
| Unit | `go test -race -count=1 ./...` | *(none)* | Logic, handlers, tools, policy, OAuth, store, crypto, state machines with fakes | **[NOW]** real tests in every `internal/` package and in `migrations` |
| Fake-service integration | `go test -race -count=1 -tags=fakegarmin ./...` | `fakegarmin` | Login strategies, MFA, DI refresh, the host guard, retries, API decoding, and the remote login command against the scripted fake Garmin | **[NOW]** real tests in `internal/garmin/auth`, `internal/garmin/api`, `internal/garmin/client`, `internal/tools` and `internal/cmd`. The job no longer passes vacuously |
| E2E | `go test -race -count=1 -tags=e2e -timeout=10m ./e2e/...` | `e2e` | stdio and Streamable HTTP MCP, OAuth flow, browser login form, tenant isolation | **[NOW]** ten tests over the real binary. `cli_test.go` covers version output, a clean stdout on the stdio path, and an unknown command. `exercisecatalog_test.go` points the process at a proxy that refuses every tunnel and proves the start-up catalog read is attempted, is anonymous, reaches only `connect.garmin.com:443`, and cannot fail a start-up; every other binary this suite starts is pointed at a blackhole proxy, so no e2e run reaches the public Garmin service. `remote_test.go` stands up a synthetic TLS deployment and covers protected resource metadata read unauthenticated with `bearer_methods_supported` exactly `["header"]`, an untokened MCP request refused with a challenge carrying `resource_metadata` and no error code, a token in a query parameter that never authenticates, and a bad header token reported as `invalid_token`. The OAuth-flow, browser-login and isolation rows are still **[TARGET]** at this layer: they are covered by package tests |
| Live (opt-in) | `go test -race -count=1 -tags=garminlive ./live/...` | `garminlive` | The real service: login strategy fallback, DI exchange and session validation; the decoded device file against Garmin's own activity summary; tool results against the domain clients; the read-only tool surface for shape, bounds, truncation flags and leaks; and, behind a fourth gate, the write and destructive surface driven end to end against objects the suite creates itself. Never in CI | **[NOW]** real tests in `live/`, and the count is deliberately not repeated here: `TestEveryReadOnlyToolIsAccountedFor` and `TestEveryWriteAndDestructiveToolIsAccountedFor` fail when a registered tool is neither driven nor excused, which is the property a number cannot state. The read half is read-only by construction and gated three ways; the write half is gated four ways, mutates only what it created, and removes it again. See **Running the live suite** below |
| Conformance | *(no command)* | — | The official MCP server conformance suite | **BLOCKED**, not merely unwired. The suite was run for real against a live deployment and cannot pass a domain server; see `docs/implementation-status.md` and ADR 0002 |

### Running the live suite

The live layer contacts the real Garmin Connect service, so three gates must all
be open and a missing one is a **skip**, never a failure:

```sh
export GARMIN_USERNAME=...        # a dedicated non-primary account
export GARMIN_PASSWORD=...
export GARMIN_LIVE_ACK=i-accept-live-garmin-traffic
go test -race -count=1 -tags=garminlive ./live/...
```

A **fourth** gate, separate and default off, additionally enables the write
checks. With it unset the read-only suite behaves exactly as it did before and
every write check skips with a reason:

```sh
export GARMIN_LIVE_WRITE_ACK=i-accept-live-garmin-writes
```

Acknowledging live *traffic* never acknowledges live *mutation*: the two values
are separate on purpose, so a credential set that later points at a different
account cannot start mutating it by inheriting one export.

**Three further gates guard the writes that cannot be fully undone.** Each is
separate, default off, and skips with a reason naming exactly what is at stake,
because the write half's owned-objects-only rule cannot reach any of them:

```sh
# a per-day nutrition-goal override Garmin exposes no way to remove
export GARMIN_LIVE_NUTRITION_SETTINGS_ACK=i-accept-live-nutrition-settings-override
# delete_weigh_ins takes a date, not an identifier, and defaults to deleting all
export GARMIN_LIVE_WEIGHIN_DELETE_ACK=i-accept-live-weighin-delete
# add_body_composition and set_blood_pressure append records with no delete tool
export GARMIN_LIVE_HEALTH_WRITE_ACK=i-accept-live-irreversible-health-writes
```

`add_hydration_data` needs none of them: hydration is a daily total and the
tool's own value is signed, so the test undoes itself with a compensating write
and verifies the total returned to what it was. A write is gated only when
nothing can put the account back.

The acknowledgement values are exact: a truthy `1` does not open either gate.
`GARMIN_LIVE_MFA_CODE` is optional and only needed for an account that
challenges the login; without it an MFA challenge skips the suite rather than
hanging on a prompt no test can answer.

What it asserts, in priority order:

1. The decoded device file of one recent activity against Garmin's own summary
   of that same activity — distance to 0.5%, elapsed time to 2 s, ascent to 2 m,
   calories to 1 kcal, average and maximum heart rate exactly — plus the
   invariant that a single-session file's session window covers at least 90% of
   its record stream. This is the check a fixture structurally cannot make, and
   it is the one that would have caught both defects ADR 0007 records.
2. Tool results against an independent read through the domain client that backs
   them, so a dropped or transposed field on the way out is visible.
3. The whole registered read-only surface: every tool answers, its result obeys
   the declared bounds and truncation flags, and it carries no coordinate,
   credential or raw payload. A read-only tool that is neither exercised nor
   listed with a reason fails the suite, so the sweep cannot decay.
4. The login itself: which strategy of the fallback chain succeeded, that the DI
   exchange produced a reusable token set, and that the API tier accepted it.

With the fourth gate open it additionally drives **26 of the 27 write and
destructive tools** end to end, each against an object it created itself:

5. One workout from creation to removal — a builder uploads it, the library
   reads it back, the calendar takes it, an in-place `update_workout` replaces
   its content, the calendar entry survives that update, and the entry and the
   template are removed again. The surviving entry is the live check of the
   ported proposal's stated purpose, and it is only testable where a real
   calendar exists.
6. One manual activity from creation to removal, with all six per-activity
   metadata writes — name, type, event type, description, feel and perceived
   effort — written and then read back from the activity record. The feel is
   compared as written and the effort against the ten-fold scale Garmin stores.
7. One completed strength session: created with its sets attached, its whole set
   list replaced, and the result re-read through the read-only tool and compared
   position by position.
8. The four batch tools and the week schedule, over workouts it created, so the
   per-item reporting and `schedule_week`'s calendar pre-check are exercised
   against a real calendar rather than a fixture.
9. `download_activity_file` in three formats. It is write-tier because it moves
   a whole device file, but it mutates nothing, so it runs against the same
   activity the read half analyses.

Rules the suite enforces on itself:

- **Read-only by construction on the read half.** Every domain client and every
  tool of the read half reaches Garmin through a caller that refuses anything
  but a `GET`, a `HEAD`, or the one `POST` the GraphQL calendar gateway needs.
  There is no mutation path in that half, and the guard has its own test.
- **Owned objects only on the write half.** The write half has its own caller,
  which refuses any mutating request whose target is not an object this suite
  created — before the request leaves the process. Ownership is learned from
  Garmin's own create responses rather than declared by a test: the created
  object is read back and admitted only when it reports **both** the identifier
  being adopted and the name the create sent, because a generated name carries a
  one-second run stamp and two runs starting in the same second render the same
  names. The recognised endpoint set is an allowlist, and both halves of the
  guard are pinned by tests. The maintainer's own pre-existing activity and workout are untouchable
  by construction, and the read half additionally skips any object carrying the
  suite's prefix so the two halves cannot interfere.

  **One documented exception, and it is the only one:**
  `set_nutrition_daily_settings` writes an account-wide document that cannot be
  created, so there is no owned object to bind it to. The guard admits that one
  endpoint only for the exact date the running test declared it would write with
  `foodLedger.allowSettingsDate` — every other date on that endpoint is refused
  before the request leaves the process, so a defect in the tool under test that
  computed the wrong date cannot overwrite a day the suite never intended. Beyond
  that narrowing, safety is the caller's rather than the guard's: the test reads
  the current value, skips with a reason when it cannot be read, writes a bounded
  reversible delta, verifies it, and restores the original in a `t.Cleanup`.

  Restoring the original *value* is not the same as restoring the original
  *shape*. Garmin's settings document is normally set once and inherited across
  days (`nutrition.py:108-109`: "settings are typically set once and inherited
  across days, but Garmin accepts per-day overrides"), and neither this codebase
  nor upstream exposes any way to delete or reset a per-day override once one
  exists. A per-day PUT can therefore materialise a day-specific override where
  the account previously inherited a shared default, and writing the same figure
  back leaves that override in place: the account's structure changed even though
  every figure reads the same afterward. Nothing can undo that, so the test does
  not run by default — it additionally requires
  `GARMIN_LIVE_NUTRITION_SETTINGS_ACK=i-accept-live-nutrition-settings-override`,
  a fifth, narrower gate on top of the four above. A killed process still leaves
  the account's goal *value* changed regardless of this gate's restore step,
  which is a smaller blast radius than an unowned create but is not zero — do not
  add a second endpoint to this exception without the same read-verify-restore
  shape, the same per-date narrowing, and a line here saying so.

  Custom foods carry a further limit: Garmin exposes no per-item GET for one, so
  ownership is bound by a name search after the create. The start-of-suite
  sweeper does sweep prefixed custom foods a killed run left behind, the same way
  it sweeps workouts and activities, using that same name search rather than a
  per-item fetch to recognise one.
- **Every created object is removed.** Each create registers a `t.Cleanup`
  removal, so a failing assertion still cleans up; anything the ledger still
  holds when the suite ends is removed there; a removal that fails is reported
  loudly and never swallowed. Every created object carries the
  `garmin-mcp-live-` name prefix, and a sweeper at suite start removes leftovers
  **matching that prefix only**, so a killed run cannot accumulate junk. The
  sweeper parses the whole generated shape — every numeric field must be spelled
  the way `strconv.FormatInt` spells it — and compares the run stamp against a
  cut-off truncated to the second the stamp carries, so a run that starts in the
  same second as another never sweeps that run's live objects.
- **No golden values.** Nothing is pinned to the account under test: no
  distance, heart rate, name, date or identifier appears in the package. Every
  check compares two sources Garmin itself provides, or asserts an invariant.
- **No readings in failures.** A failure names the field and the relative delta.
  A failing live run cannot print the account's health data into a terminal.
- **No state outside a temporary directory.** The key and the token store are
  created under `os.MkdirTemp` and removed when the suite ends, so the
  maintainer's own token store and configuration are never touched. No response
  body, fixture or `.fit` file is written anywhere.
- **Never in CI.** No workflow builds this tag, and none may.

An account with no activity history skips the activity-scoped checks with a
reason that states what the account holds. That is honest reporting, not a pass:
the account-wide checks still run and still fail on a real defect.

A vacuous pass is a defect, not a green light. Both tagged jobs run real tests,
and each one **fails when its suite did not actually run**. Counting files was the
first attempt and was not enough: an empty tagged file, or a suite where every
test skips, satisfies a file count while proving nothing. Each job now takes the
test names declared in the relevant files and requires every one of them to appear
as a pass in the `go test -json` stream, so a deleted, renamed, emptied or
universally skipped tagged test fails the job. A suite that decays into fewer but
still-passing tests is caught, because the declared name disappears with it.

Rules:

- Always run Go tests with `-race`.
- 80%+ coverage on new code. CI enforces this per package against an explicit
  exception list, checked in both directions: a package under the floor fails,
  and a listed package that reaches the floor must leave the list. No package is
  under the rule today.
- No test may reach the public Garmin service by default.
- Live tests need the `garminlive` tag, an explicit environment
  acknowledgement, and a dedicated non-primary account. They never mutate
  unless separately enabled — that is `GARMIN_LIVE_WRITE_ACK`, default off — and
  they never record raw traffic. What they may mutate when it is enabled is
  bounded by construction: an object the suite created itself, and nothing else.
- Fixtures are synthetic and hand-sanitized. Never commit recordings that could
  contain credentials, authorization headers, health data, or precise locations.
- Missing live credentials never block unit, fake-service, contract, auth, or
  MCP work. Record live status as `not run — credentials unavailable`.

## CI Pipeline [NOW]

Two workflows exist: `ci.yaml` and `release.yaml`. CI runs on push to `master`,
on pull requests, and by `workflow_call` from the release workflow, with
top-level `permissions: contents: read`.

Each workflow names its concurrency group with a **literal** prefix, `ci-` and
`release-`, never `github.workflow`. A called workflow inherits the caller's
`github.workflow`, so the shared expression put both in one group and the release
run cancelled itself through its own `gates` job before it could publish. CI
cancels superseded runs; a release does not, because publishing must not be
interrupted part-way.

| Workflow | Jobs |
|----------|------|
| CI (`ci.yaml`) | `verify` (gofmt, `go mod tidy`, `go vet`, then `go vet` again for `GOOS=linux`, `darwin` and `windows` so platform-specific files and their tests are type-checked), `dependency-review` (pull requests only, SHA-pinned, `fail-on-severity: low` and an explicit license allowlist), `lint` (golangci-lint plus `golangci-lint fmt --diff`), `test` (race, coverage profile, and the per-package coverage floor **enforced** against an explicit exception list in both directions), `test-fakegarmin` (race, coverage, and the declared-test assertion), `e2e` (race and the declared-test assertion), `fuzz-smoke` (every `Fuzz*` target for a bounded ten seconds, failing loudly when it discovers none), `reproducible-build` (two builds of `cmd/garmin-mcp`, each with its own `GOCACHE`, compared by hash), `vulncheck`, `build` (3 OS x 2 arch), `goreleaser` (`check` plus snapshot with `--skip=sign,sbom,docker`), `container` (build the image from a prepared context, then hardening smoke test, which proves a nonroot read-only shell-free image runs the binary and does not yet prove server start-up or a writable `/data`) |
| Release (`release.yaml`) | `v*` tags only. `gates` re-runs the whole CI workflow against the tagged commit, then `release` runs GoReleaser with the narrowest write permissions plus `id-token: write` for keyless cosign |

Every third-party action is pinned to a full commit SHA with the intended
version in a trailing comment. `golangci-lint`, GoReleaser, `govulncheck`,
cosign, and syft use explicit pinned versions, never `latest`. Secrets are never
exposed to forked pull requests.

Two of the three jobs that list once named are now in `ci.yaml`, and the third
turned out to have been there all along. `fuzz-smoke` discovers
every `Fuzz*` target and runs each for a bounded ten seconds, failing loudly when
it finds none rather than passing vacuously. `reproducible-build` builds
`cmd/garmin-mcp` twice, each with its own `GOCACHE` so the second is a real
recompile, and fails unless the two binaries hash identically. The per-package
coverage floor was **already** enforced, in the `test` job, against an explicit
exception list checked in both directions — this paragraph claimed for some time
that it did not exist, which was simply wrong.

An MCP conformance job is **not** on that list: the suite cannot score a domain
server, and the evidence is in `docs/implementation-status.md` and ADR 0002. See
the same status file for the current supply-chain coverage, which is checksum
signing and archive SBOMs only.

## Quality Gates [NOW]

These must pass before any tag, and every one of them is runnable today:

```sh
go build ./...
go vet ./...
golangci-lint run
go test -race -count=1 ./...
go test -race -count=1 -tags=fakegarmin ./...
go test -race -count=1 -tags=e2e -timeout=10m ./e2e/...
govulncheck ./...
goreleaser check
goreleaser release --snapshot --clean
```

Plus:

- The container image builds and passes the non-root/read-only smoke test. The
  image needs a binary in the build context; see ADR 0006 for why the
  `Dockerfile` is runtime-only.
- `docs/implementation-status.md` matches reality and `git status --short` is
  clean.
- No placeholder or `not implemented` handler is counted as working behavior.
- Pre-commit hooks may mutate the worktree; CI only verifies and fails on a
  dirty diff.

## Code Conventions [NOW]

These apply to every commit, including the code that already exists.

- Immutability: return new values, do not mutate shared state in place.
- One `auth.TokenGate` per process, shared by every path that writes tokens.
  Whatever builds an `auth.Config` and an `auth.RefreshConfig` must pass the
  **same** `*auth.TokenGate` to both. Each config falls back to a private gate
  when the field is nil, so two gates compile and pass their own tests while
  login and refresh serialize only against themselves — which restores the
  rotated-token overwrite the gate exists to prevent. `internal/cmd/wiring.go`
  does this, and `TestServeSharesOneTokenGateBetweenLoginAndRefresh` asserts the
  two configs hold the same pointer. Keep that test passing.
- `context.Context` end to end. Carry cancellation and deadlines into every
  HTTP request.
- Inject `http.Client`, clock, randomness, Garmin base URLs, stores, and
  loggers where testability needs it.
- Wrap errors with operation context and keep `errors.Is`/`errors.As` working.
- Tolerant decoding for Garmin reads (optional pointers, `json.RawMessage`,
  union decoders). Strict typed models for writes.
- Validate all input at system boundaries and fail closed on unknown or
  insecure combinations.
- No hardcoded values in handlers. Runtime settings come from `config.Config`;
  protocol constants live in the focused protocol package with source comments.
  FIT profile field numbers, scales and offsets come from the SDK's generated
  profile, never from a hand-written table: session and lap number the same
  quantity differently (average heart rate is field 16 on a session and 15 on a
  lap), and a hand-kept table of those is a silent wrong-measurement bug.
- Secrets must not be printable: `String`, `MarshalJSON`, error, and debug paths
  on secret-bearing structs never reveal fields.
- Secret material sits **at least one pointer deeper** than the redacting type.
  A redacting `String` method is not sufficient on its own. `fmt` reaches its
  `badVerb` path for a verb the type does not support, and that path re-prints
  the value at depth 0, where it dereferences a pointer to a struct and prints
  the unexported fields verbatim. It cannot call a method on an unexported
  field, so no method can close the hole. The rule that follows: the field that
  holds the material must be a **pointer** sitting at depth 1 or deeper, where
  `fmt` renders it as an address. Depth 0 is not enough, and a plain field is
  never enough. `fmt` also dereferences a top-level pointer to an array, slice,
  struct or map, so material of those kinds needs one more level than material
  behind a defined string type. `config.Secret` (`*secretMaterial`, a defined
  string type), `store.TokenSet` (`*tokenParts` holding `*secret` fields),
  `cryptostore.Key` (`*keySecret` holding `*keyMaterial`), and
  `protocol.Response` (`*sealedParts` holding `*responseParts`) all follow this
  shape. Each has a leak test that strips the type's methods with an alias and
  asserts the material is still absent from every verb. Every future
  secret-bearing type must follow it too.
- Structured `slog` logging only, in `live/` as well: a diagnostic there goes
  through the suite logger and every error through `safeError`, because a raw
  `*url.Error` carries the request URL and that URL names an account object.
  Log request ID, pseudonymous principal ID,
  client ID, coarse category, outcome, latency, and coarse status. Never log
  request or response bodies by default.
- Stdout is reserved exclusively for MCP frames in stdio mode. Logs go to
  stderr.
- Prefer the standard library. Every nontrivial dependency needs a rationale,
  license, and maintenance note in an ADR or `docs/dependencies.md`. That file
  exists and carries the current direct requirements; add the entry in the same
  commit as the requirement.
