# AGENTS.md

Guidelines for humans and AI agents that contribute to garmin-mcp.

**How to read this file.** Sections marked **[NOW]** describe code and
configuration that exist in the repository today. Sections marked **[TARGET]**
describe the architecture being built. A [TARGET] statement is a binding design
decision, never a claim that the code exists. Do not cite a [TARGET] section as
evidence that something works.

## Project Overview [TARGET]

garmin-mcp is being built as a Model Context Protocol (MCP) server that exposes
Garmin Connect data and operations as MCP tools. It is a native Go rewrite: no
Python, no Garth, no Python subprocess.

- Go module: `github.com/tamcore/garmin-mcp`.
- MCP layer: the official `modelcontextprotocol/go-sdk` (not `mark3labs/mcp-go`).
  Not yet added to `go.mod`; the version is pinned by ADR 0002.
- Transports: stdio for local single-user use, Streamable HTTP for remote
  multi-user use.
- Garmin Connect is an unofficial, undocumented private API. Endpoints, schemas,
  and WAF behavior can drift. Never add CAPTCHA bypasses, browser automation, or
  credential harvesting. This rule applies now, not later.

## Current state [NOW]

As of 2026-08-14 the repository contains the following Go code. Coverage is the
statement coverage reported by `go test -cover`.

| Path | What is there | Coverage |
|------|---------------|----------|
| `cmd/garmin-mcp/main.go` | Thin `main`: passes the ldflags-injected `version` and `commit` into `cmd.Execute` and calls `os.Exit` with the returned code | n/a |
| `internal/cmd` | Cobra tree — root, `serve`, `auth`, `doctor`, `tools` with a `list` subcommand, `migrate`, `version`. Only `version` (and root `--version`) does real work. Bare root and bare `tools` print help; the rest validate configuration and then fail; see below | 96.4% |
| `internal/config` | `Config`, deterministic four-layer precedence, `_FILE` secret variants, full lexical validation, redacted output | 95.3% |
| `internal/garmin/auth` | Login state machine, strategy fallback, bounded MFA transaction registry with a single completion lease, DI ticket exchange, session validation, refresh with per-principal collapsing and CAS, a per-principal `TokenGate` that serializes login against refresh, unverified-JWT `exp` parsing | 87.5% with `-tags=fakegarmin`, 65.1% untagged |
| `internal/garmin/protocol` | Garmin host/path/endpoint-label constants, client identities, DI client-ID candidates, and the login response classifier (JSON and widget HTML). No I/O | 96.7% |
| `internal/store` | `FileStore`: encrypted, atomically written, owner-only per-principal token records whose wrapper schema and CAS version are authenticated by the AEAD, plus legacy `garmin_tokens.json` import and export | 91.7% |
| `internal/cryptostore` | AES-256-GCM envelope encryption with versioned key IDs and principal/record-type AAD, and an owner-only key file | 89.9% |
| `internal/securefile` | The shared filesystem hardening both stores use: `os.Root` component-by-component path resolution, post-open identity verification, link-based exclusive install, non-blocking regular-file reads, owner-only modes, and Windows ACL evaluation | 84.2% |
| `internal/tokenlink` | `Store`, the adapter that makes a `*store.FileStore` satisfy `auth.TokenStore` by converting between the two packages' `TokenSet` types | 80.0% |
| `internal/testkit` | Scripted fake Garmin service, fake clock, fixtures, transport guard | 96.8% |
| `e2e` | Build tag `e2e`. `cli_test.go` builds the binary and drives it as a subprocess: version output, clean stdout on the stdio path, unknown command | n/a |

Everything else in the repository is documentation, contract manifests
(`compat/`), and CI, lint, pre-commit, GoReleaser, and container configuration.

`go.mod` now has real requirements: `spf13/cobra`, `spf13/viper`, `spf13/pflag`,
and `golang.org/x/sys`, plus the transitive indirect set. `golang.org/x/sys` is a
direct requirement because `internal/securefile` reads Windows security
descriptors through `golang.org/x/sys/windows`. See `docs/dependencies.md`.

What `serve`, `auth`, `doctor`, `tools list`, and `migrate` actually do: they
load and validate the configuration, then return a `*cmd.NotImplementedError`
that wraps the exported `cmd.ErrNotImplemented` sentinel. `cmd.Execute` prints
it to stderr and returns exit code 1, which `main` passes to `os.Exit`. This is
a declared gap, not working behavior. Stdout stays byte-empty on those paths,
and `internal/cmd` tests assert byte-emptiness.

There is still **no** MCP server, no MCP SDK requirement in `go.mod`, no stdio
or HTTP transport, no OAuth authorization server, no registered tool or
resource, no `slog` logger anywhere in the binary path, no SQLite store, no
`LoginTransport` type (the auth package uses a one-method `Doer` transport
interface instead), and no `garminlive` command.

Nothing assembles the packages either. `internal/cmd` imports only
`internal/config`, so no command builds a key, a store, an authenticator, or a
refresher. Every package above is exercised by its own tests alone.

`docs/implementation-status.md` is the authoritative task and gap list. Read it
with this file before any work. Where this file and the repository disagree, the
repository wins, and fixing the file is part of the next commit.

The upstream baseline is `python-garminconnect` 0.3.10. The security behaviors
that release adds are required work for the auth and session slice, and they are
listed in `docs/upstream-pins.md`.

## Authorization model [TARGET]

These boundaries stay separate at all times:

| Boundary | Credential | Rule |
|----------|-----------|------|
| MCP client to this server | This server's OAuth access token | Never forwarded to Garmin |
| This server to Garmin | Per-principal Garmin DI token set | Never returned to the MCP client |
| Browser to login transaction | One-time cookie plus server-side transaction state | Credentials never become MCP tool arguments |

### Safety model [TARGET]

- Read-only tools are always registered. Write and destructive tools need the
  **intersection** of operator enablement and granted OAuth scope. Operator
  enablement alone is never sufficient.
- Remote deployments default to read-only.
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

## Target Package Layout [TARGET]

This is the layout to build toward, not the current tree. `exists` marks a path
that is present today; every other path must still be created. Do not import or
reference a path marked `planned` — it will not compile.

```
cmd/garmin-mcp/          main package only                                    exists
internal/
  cmd/                   Cobra commands: serve, auth, doctor, tools, migrate, version   exists (only version works)
  config/                parsing, precedence, validation, redacted output      exists
  garmin/protocol/       endpoints, client identities, pacing, failure classifier  exists
  garmin/auth/           native login/MFA strategies and DI exchange           exists
  garmin/client/         authenticated HTTP, refresh, retry, typed errors      planned (refresh/retry live in garmin/auth today)
  garmin/api/            domain clients: activity, health, workout, device, ...  planned
  mcpserver/             server, transports, middleware wiring                 planned
  tools/                 one file per MCP tool + register.go RegisterAll       planned
  resources/             MCP resource templates and handlers                   planned
  mcplog/                structured MCP logging, level mapping, transport sink  planned
  ratelimit/             limiter + handler middleware, keyed per principal     planned
  identity/              principal and request-context resolution              planned
  oauthserver/           MCP-facing OAuth AS/RS integration and consent        planned
  loginweb/              embedded templates and one-time login transactions    planned
  store/                 storage interfaces, SQLite implementation, migrations  exists (FileStore only; SQLite per ADR 0004 still planned)
  cryptostore/           versioned envelope encryption and key rotation        exists
  securefile/            shared filesystem hardening for every secret file    exists
  tokenlink/             store-to-auth TokenSet adapter                       exists
  policy/                scopes, write/destructive gates, limits               planned
  observability/         redacted logging, metrics, tracing hooks              planned
  testkit/               fake Garmin, fake clock, fixtures, test keys          exists
e2e/                     end-to-end tests (build tag: e2e)                     exists (CLI-level only)
web/                     embedded HTML/CSS; no remote JS dependency            planned
migrations/              embedded, monotonic database migrations                planned
compat/                  pinned tool and resource contract manifests           exists
docs/adr/                consequential design records                          exists
```

File discipline, in force now:

- All real code lives under `internal/`. `cmd/garmin-mcp/` holds only `main`.
- Every non-trivial `.go` file has a sibling `_test.go` in the same package.
- Files under 400 lines, functions under 50 lines, nesting depth under 5.
  Extract helpers before you cross those limits.
- No package-level mutable state. Pass `*config.Config`, stores, clocks, and
  loggers explicitly.
- Interfaces live with the consumer, not in a global interfaces package.

## Adding a New MCP Tool [TARGET]

No tool exists yet, and steps 3 to 7 name packages that must still be created.
This is the procedure to follow once the MCP layer exists, and it is also the
specification that layer must satisfy. Step 1 is already possible today, because
`compat/tools.json` exists.

1. Add the contract to `compat/tools.json` (name, description, input schema,
   sensitivity, effect, scope) before you write the handler.
2. Add the failing contract test: registered name plus normalized schema
   snapshot against the manifest.
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
| Unit | `go test -race -count=1 ./...` | *(none)* | Logic, handlers, policy, crypto, state machines with fakes | **[NOW]** real tests in every `internal/` package |
| Fake-service integration | `go test -race -count=1 -tags=fakegarmin ./...` | `fakegarmin` | Login strategies, MFA, DI refresh, retries, API decoding against the scripted fake Garmin | **[NOW]** real tests. `internal/garmin/auth` carries 29 tagged test functions across four tagged test files, plus a tagged harness, which is 29 of the 100 top-level test runs the package reports under the tag. The job no longer passes vacuously |
| E2E | `go test -tags=e2e -timeout=10m ./e2e/...` | `e2e` | stdio and Streamable HTTP MCP, OAuth flow, browser login form, tenant isolation | **[NOW]** `e2e/cli_test.go` builds the binary and drives it as a subprocess: version output, a clean stdout on the stdio path, and an unknown command. The MCP, OAuth, and isolation rows are still **[TARGET]** |
| Live (opt-in) | `go test -tags=garminlive -count=1 ./...` | `garminlive` | Real Garmin login drift detection. Never in CI | **[TARGET]** nothing carries the tag |

A vacuous pass is a defect, not a green light. Both tagged jobs now run real
tests, and each one now **fails before the suite runs when the suite is absent**:
`test-fakegarmin` counts files carrying `//go:build fakegarmin` and `e2e` counts
test files under `./e2e/`, and a count of zero is a hard error. The guard is
presence-based, so it catches a deleted suite, not a suite that decays to one
trivial test. That remains a review duty.

Rules:

- Always run Go tests with `-race`.
- 80%+ coverage on new code. CI prints a coverage summary but does not yet
  enforce a threshold, so this is a review duty until it does.
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
top-level `permissions: contents: read` and a cancel-in-progress concurrency
group keyed on workflow and ref.

| Workflow | Jobs |
|----------|------|
| CI (`ci.yaml`) | `verify` (gofmt, `go mod tidy`, `go vet`, then `go vet` again for `GOOS=linux`, `darwin` and `windows` so platform-specific files and their tests are type-checked), `lint` (golangci-lint plus `golangci-lint fmt --diff`), `test` (race, coverage profile, coverage summary), `test-fakegarmin`, `e2e`, `vulncheck`, `build` (3 OS x 2 arch), `goreleaser` (`check` plus snapshot with `--skip=sign,sbom,docker`), `container` (build the image from a prepared context, then hardening smoke test) |
| Release (`release.yaml`) | `v*` tags only. `gates` re-runs the whole CI workflow against the tagged commit, then `release` runs GoReleaser with the narrowest write permissions plus `id-token: write` for keyless cosign |

Every third-party action is pinned to a full commit SHA with the intended
version in a trailing comment. `golangci-lint`, GoReleaser, `govulncheck`,
cosign, and syft use explicit pinned versions, never `latest`. Secrets are never
exposed to forked pull requests.

Jobs the target pipeline still needs: a bounded fuzz smoke job, the pinned MCP
conformance suite, a coverage threshold gate, and a two-clean-build
reproducibility check. None exists. Add each one with the subsystem that makes
it meaningful, and see `docs/implementation-status.md` for the current
supply-chain coverage.

## Quality Gates [NOW]

These must pass before any tag, and every one of them is runnable today:

```sh
go build ./...
go vet ./...
golangci-lint run
go test -race -count=1 ./...
go test -race -count=1 -tags=fakegarmin ./...
go test -tags=e2e -timeout=10m ./e2e/...
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
  rotated-token overwrite the gate exists to prevent. Nothing wires this yet;
  see `docs/implementation-status.md`.
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
