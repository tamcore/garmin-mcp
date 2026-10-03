# AGENTS.md

Rules for humans and AI agents that contribute to garmin-mcp. These rules are
binding. Where this file and the code disagree, the code is correct, and you must
fix this file in the same change.

## Project overview

garmin-mcp is a Model Context Protocol (MCP) server for Garmin Connect, written
in Go. It does not use Python, Garth, or a Python subprocess.

- Go module: `github.com/tamcore/garmin-mcp`.
- MCP layer: the official `modelcontextprotocol/go-sdk`, at the version in
  `go.mod`. Do not use `mark3labs/mcp-go`.
- Transports: stdio for one local account, Streamable HTTP for many remote
  accounts.
- Garmin Connect is an unofficial, undocumented private API. Endpoints, schemas,
  and WAF behavior can change. Do not add CAPTCHA bypasses, browser automation,
  or credential harvesting.
- The tool contracts come from the Taxuspt/garmin_mcp project at a pinned commit.
  `compat/tools.json` and `compat/resources.json` record it.

### Supported platforms

Linux and macOS, on amd64 and arm64. **Windows is not supported. Do not add it**
— no Windows-tagged file, job, release archive, or `GOOS=windows` build.
`internal/securefile` refuses a secret under permissions it cannot verify, so it
compiles only on unix. A Windows binary would silently lose owner-only
enforcement.

## Authorization model

These boundaries stay separate. Code enforces all three.

| Boundary | Credential | Rule |
|----------|-----------|------|
| MCP client to this server | This server's OAuth access token | Never forwarded to Garmin |
| This server to Garmin | Per-principal Garmin DI token set | Never returned to the MCP client |
| Browser to login transaction | One-time cookie plus server-side transaction state | Credentials never become MCP tool arguments |

Redirect URI matching is byte-exact except for loopback ports and the opt-in
wildcard; see `docs/configuration.md` for that and `login-allowed-emails`.

### Safety model

- Read-only tools are always registered. Write and destructive enablement both
  default to false, and destructive requires write.
- On stdio, explicit operator enablement authorizes the higher tiers: the
  composition root marks local operator authority, and policy construction
  rejects that authority in remote mode or next to a scope source.
- On streamable-http, the higher tiers need the **intersection** of operator
  enablement and the OAuth scope of the verified bearer token.
- Destructive tools ask for confirmation through MCP elicitation and **fail
  closed**. If confirmation is unsupported, declined, or timed out, the call is
  refused and the refusal names the reason.
- Each principal has its own Garmin client, token set, cookie jar, and tool
  results. There is no global cross-user client.

## Development workflow

1. Plan before you write code for a non-trivial change.
2. TDD for each behavior: failing test, smallest implementation, refactor.
3. Run `golangci-lint run` and `go vet ./...` before you commit.
4. Conventional commits (`feat:`, `fix:`, `refactor:`, `test:`, `ci:`, `docs:`,
   `chore:`), one behavior for each commit.
5. After each push, wait for CI to pass before you add the next commit.

`Co-authored-by` trailers name only who changed the code in that commit:

- Claude: `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`
- Codex: `Co-authored-by: Codex <noreply@openai.com>`

## Package layout

```
cmd/                     main packages only: garmin-mcp, notices generator
internal/
  cmd/                   Cobra commands and the composition root
  config/                parsing, precedence, validation, redacted output
  garmin/protocol/       endpoints, client identities, login response classifier
  garmin/auth/           login and MFA strategies, DI exchange, refresh, TokenGate
  garmin/client/         authenticated HTTP, bounds, one post-401 retry, typed errors
  garmin/api/            domain clients and FIT decoding
  mcpserver/             server, transports, middleware
  tools/                 one file or domain file per MCP tool, plus register.go
  resources/             the five constant MCP documents
  mcplog/, metrics/      structured logging (never stdout) and Prometheus metrics
  ratelimit/, policy/    per-principal limiter; tiers, scopes, confirmation
  identity/              principal and request-context resolution
  oauthserver/           OAuth authorization server and consent
  oauthstore/            oauthserver.Store over the SQLite store
  loginweb/              browser login pages and transactions
  store/, tokenlink/     FileStore (stdio), SQLite store (remote), auth adapter
  cryptostore/           versioned envelope encryption and key files
  securefile/            filesystem hardening for every secret file
  notices/               notices generator and freshness test
  testkit/               fake Garmin, fake clock, fixtures, FIT builder
e2e/                     end-to-end tests over the built binary (tag: e2e)
live/                    opt-in tests against real Garmin (tag: garminlive)
migrations/              embedded, checksummed, monotonic SQL migrations
compat/                  pinned tool and resource contract manifests
charts/garmin-mcp/       Helm chart
```

### File discipline

- All real code is under `internal/`.
- Each non-trivial `.go` file has a sibling `_test.go` in the same package.
- Files under 400 lines, functions under 50 lines, nesting depth under 5.
- No package-level mutable state. Pass `*config.Config`, stores, clocks, and
  loggers explicitly. A value that cannot be a `const` (for example `time.Date`)
  is a function that returns it, never a `var`.

  A package-level `var` is allowed only for sentinel errors, compile-time
  interface assertions, ldflags-injected build values, and lookup tables that
  nothing writes after initialization. The only mutable state is in `live/`,
  forced by `go test`: `live/live_test.go`'s `stateDir`, `closers` and `shared`,
  and `live/writeenv_test.go`'s `theWriteSuite`. Each is written once at
  start-up and only read after. New state in `live/` goes inside `writeEnv`.
- Interfaces live with the consumer, not in a shared interfaces package.

## Adding a new MCP tool

Copy the nearest existing tool in `internal/tools`. Do not invent a new shape.

1. Take the contract (name, description, input schema, sensitivity, effect,
   scope) from `compat/tools.json`. That file is a generated snapshot. Do not
   edit it by hand.
2. Write the failing contract test: registered name plus normalized schema
   snapshot against the manifest. A tool the manifest does not carry is an
   addition beyond the pin: add a documented-exclusion entry to
   `additionsBeyondTheManifest()` in `internal/tools/contract_test.go`, and add
   it to `wantReadOnlyToolNames`, `wantWriteToolNames` or
   `wantDestructiveToolNames` in
   `internal/tools/register_test.go`.
3. Create `internal/tools/<name>.go` with a `register<Name>(...)` function.
   - Use the upstream tool name, unless a documented security reason prevents it.
   - Declare **all four** annotation hints: read-only, destructive, idempotent,
     open-world. Open-world is always true.
   - Give a strict JSON schema with ranges, formats, and defaults.
   - Get the principal from the authenticated request context. Never accept
     `user_id`, email, token path, or an account selector as an argument.
   - Enforce OAuth scope and operator policy before you call Garmin.
   - Bound result size, page size, and date windows.
   - Return sanitized errors: no tokens, cookies, raw bodies, health payloads,
     coordinates, or stack traces.
4. Add it to `readOnlyRegistrations()`, `writeRegistrations()` or
   `destructiveRegistrations()` in `internal/tools/register.go`. A destructive
   tool must call elicitation confirmation that fails closed.
   `validateTierLists` checks the tiers against the registered set at start-up.
5. Do not add a delay to a handler. `safety-delay` lives in the policy middleware
   and applies to the whole write and destructive tiers.
6. Write unit tests against the fake Garmin service in `internal/testkit`, and
   add `fakegarmin` and `e2e` coverage where the tool crosses a transport or
   policy boundary.
7. Account for it in `live/`: a read-only tool goes into `accountCalls` plus a
   `resultShapes` entry, or into `coveredElsewhere` with a reason; a write tool
   goes into `exercisedWrites()` or `writesCoveredElsewhere`.

Maintainers: see `AGENTS.md.local`.

## Testing

| Layer | Command | Tag | Scope |
|-------|---------|-----|-------|
| Unit | `go test -race -count=1 ./...` | none | Logic, handlers, tools, policy, OAuth, store, crypto, with fakes |
| Fake service | `go test -race -count=1 -tags=fakegarmin ./...` | `fakegarmin` | Login, MFA, DI refresh, host guard, retries, API decoding against the scripted fake Garmin |
| E2E | `go test -race -count=1 -tags=e2e -timeout=10m ./e2e/...` | `e2e` | The built binary: stdio and Streamable HTTP, OAuth flow, browser login form, tenant isolation |
| Live | `go test -race -count=1 -tags=garminlive ./live/...` | `garminlive` | The real Garmin service. Never in CI |

- Always run Go tests with `-race`.
- 80% statement coverage or more for each package. CI enforces this against an
  explicit exception list in both directions (`internal/garmin/auth`: login
  paths are tagged `fakegarmin`; `cmd/garmin-mcp`: the `e2e` job runs it).
- No test reaches the public Garmin service by default. E2E subprocesses get a
  stripped `GARMIN_MCP_*` environment and a blackhole proxy. E2E seeds SQLite
  before the server starts and never writes while it runs: one writer only.
- The `fakegarmin` and `e2e` jobs fail when a declared tagged test does not pass
  in the `go test -json` stream. A vacuous pass is a defect.
- Fixtures are synthetic and sanitized. Never commit recordings that can hold
  credentials, authorization headers, health data, or precise locations.
- Missing live credentials never block other work. Record live status as
  `not run — credentials unavailable`.
- There is no MCP conformance job: the official suite cannot score a domain
  server.

### Live suite

The live suite contacts the real Garmin service. Use a dedicated non-primary
account. Each gate value is exact (a truthy `1` does not open it), and a closed
gate is a skip, never a failure.

```sh
export GARMIN_USERNAME=...
export GARMIN_PASSWORD=...
export GARMIN_LIVE_ACK=i-accept-live-garmin-traffic
export GARMIN_LIVE_MFA_CODE=...    # optional, for an account with MFA
export GARMIN_LIVE_WRITE_ACK=i-accept-live-garmin-writes
# each of these also opens a write that cannot be fully undone
export GARMIN_LIVE_NUTRITION_SETTINGS_ACK=i-accept-live-nutrition-settings-override
export GARMIN_LIVE_WEIGHIN_DELETE_ACK=i-accept-live-weighin-delete
export GARMIN_LIVE_HEALTH_WRITE_ACK=i-accept-live-irreversible-health-writes
```

`TestEveryReadOnlyToolIsAccountedFor` and
`TestEveryWriteAndDestructiveToolIsAccountedFor` fail when a registered tool is
neither driven nor excused with a reason. The suite enforces these rules:

- **The read half is read-only by construction.** Its caller refuses all but
  `GET`, `HEAD`, and the one `POST` the GraphQL calendar gateway needs. It skips
  objects with the suite's name prefix.
- **The write half changes only owned objects.** Its caller refuses a mutating
  request whose target the suite did not create. Ownership comes from Garmin's
  create response, read back, and must match both the identifier and the
  generated name. The one exception is `set_nutrition_daily_settings`: the guard
  admits that endpoint only for the exact date the test declared, and the test
  reads, writes a bounded delta, verifies, and restores. A killed run can leave
  the nutrition goal value changed. Do not add a second exception without the
  same shape and a line here.
- Custom foods have no per-item GET, so their ownership is bound by a name
  search after the create.
- `add_hydration_data` needs no extra gate: the test undoes it with a
  compensating write and verifies the total.
- **Every created object is removed.** Each create registers a `t.Cleanup`, and
  anything the ledger still holds is removed at suite end; a failed removal is
  reported loudly. Created objects carry the `garmin-mcp-live-` prefix. The
  start-of-suite sweeper removes only prefixed names stamped before this run's
  start second, so it spares a concurrent run that started in the same second.
- No golden values, no readings in failure messages, no recorded raw traffic, no
  state outside a temporary directory, and never in CI.

## CI

Three workflows: `ci.yaml`, `release.yaml`, `chart.yaml`. Top-level permissions
are `contents: read`.

- `ci.yaml` runs on push to `master`, on pull requests, and from the release.
  Jobs: `verify`, `dependency-review`, `lint`, `test` (with the coverage floor),
  `test-fakegarmin`, `e2e`, `fuzz-smoke` (every `Fuzz*` target, fails when it
  finds none), `reproducible-build`, `vulncheck`, `build`, `goreleaser`, and
  `container` (non-root, read-only root filesystem, `/readyz` with a mounted
  `/data`, and a prompt failure on a read-only `/data`).
- `release.yaml` runs on `v*` tags. It re-runs CI, then GoReleaser with the
  narrowest write permissions and `id-token: write` for keyless cosign. Its
  `chart` job then pushes the Helm chart to `oci://ghcr.io/tamcore/charts`
  (`packages: write`).
- `chart.yaml` lints and templates the Helm chart.
- Concurrency groups use a literal prefix (`ci-`, `release-`, `chart-`), never
  `github.workflow`: a called workflow inherits the caller's value, and the
  release then cancels itself. A release run is never cancelled.
- Pin every action to a full commit SHA with the version in a comment. Pin tool
  versions; never `latest`. Never expose secrets to forked pull requests.
- Supply-chain coverage is keyless cosign `sign-blob` over the checksum file
  only. Do not add image signing, SBOMs, or build provenance unless asked.

## Quality gates

All must pass before a tag:

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

Also: `git status --short` is clean, and no placeholder or `not implemented`
handler counts as working behavior.

## Code conventions

- Immutability: return new values. Do not change shared state in place.
- Pass the **same** `*auth.TokenGate` to `auth.Config` and `auth.RefreshConfig`;
  a nil field silently gets a private gate. Keep
  `TestServeSharesOneTokenGateBetweenLoginAndRefresh` passing.
- `context.Context` end to end, into every HTTP request. Inject `http.Client`,
  clock, randomness, Garmin base URLs, stores, and loggers where tests need it.
- Wrap errors with operation context. Keep `errors.Is` and `errors.As` working.
- Tolerant decoding for Garmin reads (optional pointers, `json.RawMessage`,
  union decoders). Strict typed models for writes. Validate all input at system
  boundaries and fail closed on unknown or insecure combinations.
- No hardcoded values in handlers. Runtime settings come from `config.Config`.
  Protocol constants live in the protocol package with source comments. FIT
  field numbers, scales and offsets come from the SDK's generated profile, never
  from a hand-written table: session and lap number the same quantity
  differently (average heart rate is field 16 on a session and 15 on a lap).
- Secrets must not be printable: `String`, `MarshalJSON`, error, and debug paths
  on secret-bearing types never show the material.
- A redacting `String` method is not enough: `fmt`'s `badVerb` path prints
  unexported fields at depth 0. Secret material must be in a **pointer** field at
  depth 1 or deeper, and at depth 2 or deeper when the material is an array,
  slice, struct or map. Every secret-bearing type needs a leak test that strips
  its methods with an alias and checks every verb (see `config.Secret`,
  `store.TokenSet`, `cryptostore.Key`, `protocol.Response`).
- Structured `slog` logging only. In `live/`, send diagnostics through the suite
  logger and every error through `safeError`: a raw `*url.Error` carries the
  request URL, which names an account object.
- Log request ID, pseudonymous principal ID, client ID, coarse category,
  outcome, latency, coarse status, and argument and result **sizes**; for an
  upstream call, the endpoint label, Garmin status code, and response size.
- **Never log a response body. Never log a write or destructive tool's
  arguments**: they are the payload (a weight, a blood pressure, a food log). A
  read-only tool's arguments are selectors and are logged, bounded at 256 bytes.
  `renderableArguments` (`internal/mcpserver/middleware.go`) holds this rule, and
  `TestEveryToolCallIsLoggedOnceWithCoarseFields` and
  `TestWriteToolArgumentsAreNeverLogged` pin it.
- The exact tool name is logged only behind `log-tool-names` (default off): a
  tool name can disclose a medical domain. An upstream log line names the
  endpoint **label**, never the URL.
- Metric labels use the same closed vocabulary as the log fields.
  `ToolEvent.Arguments` and `ToolEvent.Reason` are never a label.
  `internal/metrics.Recorder` takes the same `mcplog.ToolEvent` as the logger.
- In stdio mode, stdout carries only MCP frames. Logs go to stderr.
- Prefer the standard library. A new direct dependency needs its rationale,
  licence and maintenance note in the commit or pull request, a licence on the
  `dependency-review` allowlist, an entry in `internal/notices`, and a
  regenerated `THIRD_PARTY_NOTICES.md` (`go run ./cmd/notices`).
