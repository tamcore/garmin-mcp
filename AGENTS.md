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

As of 2026-08-15 the repository contains the following Go code. Coverage is the
statement coverage reported by `go test -count=1 -cover ./...`, measured on that
date.

| Path | What is there | Coverage |
|------|---------------|----------|
| `cmd/garmin-mcp/main.go` | Thin `main`: passes the ldflags-injected `version` and `commit` into `cmd.Execute` and calls `os.Exit` with the returned code | n/a |
| `cmd/notices/main.go` | Thin `main` for the notices generator: flags, then `notices.Generate`. A maintenance tool, never linked into `garmin-mcp` | n/a |
| `internal/cmd` | Cobra tree and the composition root. `serve` (stdio and streamable-http), `auth`, `doctor` and `version` do real work; `tools list` and `migrate` are still declared gaps | 74.3% |
| `internal/config` | `Config`, deterministic four-layer precedence, `_FILE` secret variants, full lexical validation, redacted output, and the operator OAuth client registry | 90.8% |
| `internal/garmin/protocol` | Garmin host/path/endpoint-label constants, client identities, DI client-ID candidates, and the login response classifier (JSON and widget HTML). No I/O | 96.7% |
| `internal/garmin/auth` | Login state machine, strategy fallback, bounded MFA transaction registry with a single completion lease, DI ticket exchange, session validation, refresh with per-principal collapsing and CAS, the shared `TokenGate`, the request-time host guard, unverified-JWT `exp` parsing | 67.3% untagged, 88.2% with `-tags=fakegarmin` |
| `internal/garmin/client` | The authenticated request layer: bounded wire and decompressed sizes, page and page-start caps, one bounded post-`401` retry that never replays a `POST` or `PATCH`, typed errors | 93.0% |
| `internal/garmin/api` | Domain clients — activities, analysis, splits, profile, workouts, gear, strength writes, downloads, the compiled-in exercise catalog | 86.9% |
| `internal/mcpserver` | Server, registry, stdio and Streamable HTTP transports, bearer middleware, session binding, origin and forwarded-header guards, elicitation confirmation, `server_info` | 89.0% |
| `internal/tools` | 47 registered tools — 21 read-only, 21 write, 5 destructive — with contracts snapshot-tested against `compat/tools.json` | 77.0% |
| `internal/policy` | Three tiers, explicit name lists validated against the registered set at start-up, the enablement-and-scope intersection, confirmation requirement | 91.7% |
| `internal/identity` | Principal type, request context, and the bearer resolver that takes the principal only from a verified token | 97.7% |
| `internal/oauthserver` | The authorization server: PKCE S256 only, exact issuer and redirect matching, single-use bound codes, hashed opaque tokens, rotating refresh with family revocation, consent | 92.4% |
| `internal/oauthstore` | The adapter from the authorization server's `Store` interface onto the SQLite store, with a compile-time assertion and five contention tests | 84.6% |
| `internal/store` | `FileStore` for stdio, plus the migration-backed SQLite backend for remote: principals, encrypted DI token sets with CAS, clients, consents, hashed transactions and codes, token families, audit events | 83.8% |
| `migrations` | The embedded, checksummed, monotonic SQL migrations `0001_initial.sql` and `0002_oauth_contract.sql` | 100.0% |
| `internal/cryptostore` | AES-256-GCM envelope encryption with versioned key IDs and principal/record-type AAD, and an owner-only key file | 89.9% |
| `internal/securefile` | The shared filesystem hardening every store uses: `os.Root` component-by-component path resolution, post-open identity verification, link-based exclusive install, non-blocking regular-file reads, owner-only modes, and Windows ACL evaluation | 84.5% |
| `internal/tokenlink` | `Store`, the adapter that makes a `*store.FileStore` satisfy `auth.TokenStore` by converting between the two packages' `TokenSet` types | 80.0% |
| `internal/loginweb` | The browser login flow in two profiles: the one-shot loopback profile and the remote profile with the `__Host-` cookie, HSTS, disclosure page, independent CSRF token, and server-held MFA continuation | 82.6% |
| `internal/mcplog` | Structured `slog` logging with the allowlisted field set, level mapping, and the stderr sink that refuses stdout | 98.5% |
| `internal/notices` | The `THIRD_PARTY_NOTICES.md` generator: the linked module set unioned over the six released targets, the curated SPDX and licence-file registry, verbatim licence copying, and the freshness test that fails on a stale notices file | 89.3% |
| `internal/ratelimit` | The per-principal limiter and its handler middleware | 95.7% |
| `internal/testkit` | Scripted fake Garmin service, fake clock, fixtures, transport guard | 96.8% |
| `e2e` | Build tag `e2e`. `cli_test.go` builds the binary and drives it as a subprocess: version output, clean stdout on the stdio path, unknown command | n/a |

Everything else in the repository is documentation, contract manifests
(`compat/`), and CI, lint, pre-commit, GoReleaser, and container configuration.

`internal/tools` at 77.0% and `internal/cmd` at 74.3% are **below** the 80% rule
below. No CI job enforces a threshold, so raise them with the next slice that
touches them.

`go.mod` direct requirements: `modelcontextprotocol/go-sdk`, `spf13/cobra`,
`spf13/viper`, `spf13/pflag`, `golang.org/x/sys` and `modernc.org/sqlite`, plus
the transitive indirect set. `golang.org/x/sys` is direct because
`internal/securefile` reads Windows security descriptors through
`golang.org/x/sys/windows`. `modernc.org/libc` moves only together with
`modernc.org/sqlite`. See `docs/dependencies.md`.

What `tools list` and `migrate` actually do: they load and validate the
configuration, then return a `*cmd.NotImplementedError` that wraps the exported
`cmd.ErrNotImplemented` sentinel. `cmd.Execute` prints it to stderr and returns
exit code 1, which `main` passes to `os.Exit`. This is a declared gap, not
working behavior — both subsystems exist and only the command wiring is missing.
Stdout stays byte-empty on those paths, and `internal/cmd` tests assert
byte-emptiness.

There is still **no** MCP resource of any kind (the five upstream
workout-template resources are unimplemented), no `LoginTransport` type (the
auth package uses a one-method `Doer` transport interface instead), no
`garminlive` command, no fuzz target, no MCP conformance job, and no
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
  cmd/                   Cobra commands and the composition root              exists (tools list and migrate still fail)
  config/                parsing, precedence, validation, redacted output      exists
  garmin/protocol/       endpoints, client identities, pacing, failure classifier  exists
  garmin/auth/           native login/MFA strategies, DI exchange, refresh, token gate  exists
  garmin/client/         authenticated HTTP, retry, bounds, typed errors      exists
  garmin/api/            domain clients: activity, analysis, profile, workout, gear, downloads  exists (health, nutrition, training, challenges, devices beyond get_devices still to come)
  mcpserver/             server, transports, middleware wiring                 exists
  tools/                 one file or domain file per MCP tool + register.go    exists (47 tools)
  resources/             MCP resource templates and handlers                   planned (all 5 upstream resources unimplemented)
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
  loggers explicitly.
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
5. Apply the configurable safety delay before write and destructive execution.
   Skip it for dry-run style calls.
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
| E2E | `go test -race -count=1 -tags=e2e -timeout=10m ./e2e/...` | `e2e` | stdio and Streamable HTTP MCP, OAuth flow, browser login form, tenant isolation | **[NOW]** seven tests over the real binary. `cli_test.go` covers version output, a clean stdout on the stdio path, and an unknown command. `remote_test.go` stands up a synthetic TLS deployment and covers protected resource metadata read unauthenticated with `bearer_methods_supported` exactly `["header"]`, an untokened MCP request refused with a challenge carrying `resource_metadata` and no error code, a token in a query parameter that never authenticates, and a bad header token reported as `invalid_token`. The OAuth-flow, browser-login and isolation rows are still **[TARGET]** at this layer: they are covered by package tests |
| Live (opt-in) | `go test -tags=garminlive -count=1 ./...` | `garminlive` | Real Garmin login drift detection. Never in CI | **[TARGET]** nothing carries the tag |
| Conformance | *(no command)* | — | The official MCP server conformance suite | **BLOCKED**, not merely unwired. The suite was run for real against a live deployment and cannot pass a domain server; see `docs/implementation-status.md` and ADR 0002 |

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
- 80%+ coverage on new code. CI prints a coverage summary but does not yet
  enforce a threshold, so this is a review duty until it does. Two packages are
  under the rule today — `internal/tools` at 77.0% and `internal/cmd` at 74.3%.
- No test may reach the public Garmin service by default.
- Live tests need the `garminlive` tag, an explicit environment
  acknowledgement, and a dedicated non-primary account. They never mutate
  unless separately enabled and never record raw traffic.
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
| CI (`ci.yaml`) | `verify` (gofmt, `go mod tidy`, `go vet`, then `go vet` again for `GOOS=linux`, `darwin` and `windows` so platform-specific files and their tests are type-checked), `dependency-review` (pull requests only, SHA-pinned, `fail-on-severity: low` and an explicit license allowlist), `lint` (golangci-lint plus `golangci-lint fmt --diff`), `test` (race, coverage profile, coverage summary), `test-fakegarmin` (race, coverage, and the declared-test assertion), `e2e` (race and the declared-test assertion), `vulncheck`, `build` (3 OS x 2 arch), `goreleaser` (`check` plus snapshot with `--skip=sign,sbom,docker`), `container` (build the image from a prepared context, then hardening smoke test, which proves a nonroot read-only shell-free image runs the binary and does not yet prove server start-up or a writable `/data`) |
| Release (`release.yaml`) | `v*` tags only. `gates` re-runs the whole CI workflow against the tagged commit, then `release` runs GoReleaser with the narrowest write permissions plus `id-token: write` for keyless cosign |

Every third-party action is pinned to a full commit SHA with the intended
version in a trailing comment. `golangci-lint`, GoReleaser, `govulncheck`,
cosign, and syft use explicit pinned versions, never `latest`. Secrets are never
exposed to forked pull requests.

Jobs the target pipeline still needs: a bounded fuzz smoke job, a coverage
threshold gate, and a two-clean-build reproducibility check. None exists in
`ci.yaml` today. An MCP conformance job is **not** on that list any more: the
suite cannot score a domain server, and the evidence is in
`docs/implementation-status.md` and ADR 0002. See the same status file for the
current supply-chain coverage, which is checksum signing and archive SBOMs only.

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
- Structured `slog` logging only. Log request ID, pseudonymous principal ID,
  client ID, coarse category, outcome, latency, and coarse status. Never log
  request or response bodies by default.
- Stdout is reserved exclusively for MCP frames in stdio mode. Logs go to
  stderr.
- Prefer the standard library. Every nontrivial dependency needs a rationale,
  license, and maintenance note in an ADR or `docs/dependencies.md`. That file
  exists and carries the current direct requirements; add the entry in the same
  commit as the requirement.
