# AGENTS.md

Guidelines for humans and AI agents that contribute to garmin-mcp.

## Project Overview

garmin-mcp is a Model Context Protocol (MCP) server that exposes Garmin Connect
data and operations as MCP tools. It is a native Go rewrite: no Python, no Garth,
no Python subprocess.

- Go module: `github.com/tamcore/garmin-mcp`.
- MCP layer: the official `modelcontextprotocol/go-sdk` (not `mark3labs/mcp-go`).
- Transports: stdio for local single-user use, Streamable HTTP for remote
  multi-user use.
- Garmin Connect is an unofficial, undocumented private API. Endpoints, schemas,
  and WAF behavior can drift. Never add CAPTCHA bypasses, browser automation, or
  credential harvesting.

Two authorization boundaries stay separate at all times:

| Boundary | Credential | Rule |
|----------|-----------|------|
| MCP client to this server | This server's OAuth access token | Never forwarded to Garmin |
| This server to Garmin | Per-principal Garmin DI token set | Never returned to the MCP client |
| Browser to login transaction | One-time cookie plus server-side transaction state | Credentials never become MCP tool arguments |

### Safety model

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

## Development Workflow

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

## Package Layout

```
cmd/garmin-mcp/          main package only
internal/
  cmd/                   Cobra commands: serve, auth, doctor, tools, migrate, version
  config/                parsing, precedence, validation, redacted output
  garmin/protocol/       endpoints, client identities, pacing, failure classifier
  garmin/auth/           native login/MFA strategies and DI exchange
  garmin/client/         authenticated HTTP, refresh, retry, typed errors
  garmin/api/            domain clients: activity, health, workout, device, ...
  mcpserver/             server, transports, middleware wiring
  tools/                 one file per MCP tool + register.go RegisterAll
  resources/             MCP resource templates and handlers
  mcplog/                structured MCP logging, level mapping, transport sink
  ratelimit/             limiter + handler middleware, keyed per principal
  identity/              principal and request-context resolution
  oauthserver/           MCP-facing OAuth AS/RS integration and consent
  loginweb/              embedded templates and one-time login transactions
  store/                 storage interfaces, SQLite implementation, migrations
  cryptostore/           versioned envelope encryption and key rotation
  policy/                scopes, write/destructive gates, limits
  observability/         redacted logging, metrics, tracing hooks
  testkit/               fake Garmin, fake clock, fixtures, test keys
e2e/                     end-to-end tests (build tag: e2e)
web/                     embedded HTML/CSS; no remote JS dependency
migrations/              embedded, monotonic database migrations
compat/                  pinned tool and resource contract manifests
docs/adr/                consequential design records
```

File discipline:

- All real code lives under `internal/`. `cmd/garmin-mcp/` holds only `main`.
- Every non-trivial `.go` file has a sibling `_test.go` in the same package.
- Files under 400 lines, functions under 50 lines, nesting depth under 5.
  Extract helpers before you cross those limits.
- No package-level mutable state. Pass `*config.Config`, stores, clocks, and
  loggers explicitly.
- Interfaces live with the consumer, not in a global interfaces package.

## Adding a New MCP Tool

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

Four layers. The first three run in CI, each with its own job.

| Layer | Command | Build tag | What it tests |
|-------|---------|-----------|---------------|
| Unit | `go test -race -count=1 ./...` | *(none)* | Logic, handlers, policy, crypto, state machines with fakes |
| Fake-service integration | `go test -race -count=1 -tags=fakegarmin ./...` | `fakegarmin` | Login strategies, MFA, DI refresh, retries, API decoding against the scripted fake Garmin |
| E2E | `go test -tags=e2e -timeout=10m ./e2e/...` | `e2e` | stdio and Streamable HTTP MCP, OAuth flow, browser login form, tenant isolation |
| Live (opt-in) | `go test -tags=garminlive -count=1 ./...` | `garminlive` | Real Garmin login drift detection. Never in CI |

Rules:

- Always run Go tests with `-race`.
- 80%+ coverage on new code.
- No test may reach the public Garmin service by default.
- Live tests need the `garminlive` tag, an explicit environment
  acknowledgement, and a dedicated non-primary account. They never mutate
  unless separately enabled and never record raw traffic.
- Fixtures are synthetic and hand-sanitized. Never commit recordings that could
  contain credentials, authorization headers, health data, or precise locations.
- Missing live credentials never block unit, fake-service, contract, auth, or
  MCP work. Record live status as `not run — credentials unavailable`.

## CI Pipeline

All workflows run on push to the default branch and on pull requests, with
top-level `permissions: contents: read` and a cancel-in-progress concurrency
group keyed on workflow and ref.

| Workflow | Jobs |
|----------|------|
| CI | `gofmt`/`goimports` verify, `go mod tidy` verify, `go vet`, golangci-lint (pinned version), unit tests with coverage, `govulncheck`, cross-platform build |
| Integration | fake-service integration, MCP conformance suite, fake-Garmin E2E, bounded fuzz smoke |
| Release checks | `goreleaser check`, snapshot release, container build, non-root/read-only container smoke test |
| Release | `v*` tags only, narrowest write permission needed |

Every third-party action is pinned to a full commit SHA with the intended
version in a trailing comment. `golangci-lint` and GoReleaser use explicit
pinned versions, never `latest`. Secrets are never exposed to forked pull
requests.

## Quality Gates

These must pass before any tag:

```sh
go build ./...
go vet ./...
golangci-lint run
go test -race -count=1 ./...
govulncheck ./...
goreleaser check
goreleaser release --snapshot --clean
```

Plus:

- The container image builds and passes the non-root/read-only smoke test.
- `docs/implementation-status.md` matches reality and `git status --short` is
  clean.
- No placeholder or `not implemented` handler is counted as working behavior.
- Pre-commit hooks may mutate the worktree; CI only verifies and fails on a
  dirty diff.

## Code Conventions

- Immutability: return new values, do not mutate shared state in place.
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
- Structured `slog` logging only. Log request ID, pseudonymous principal ID,
  client ID, coarse category, outcome, latency, and coarse status. Never log
  request or response bodies by default.
- Stdout is reserved exclusively for MCP frames in stdio mode. Logs go to
  stderr.
- Prefer the standard library. Every nontrivial dependency needs a rationale,
  license, and maintenance note in an ADR or `docs/dependencies.md`.
