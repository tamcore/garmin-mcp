# Threat model

**Read this first: almost everything below describes the target system, not the
current one.** There is no MCP server, no OAuth authorization server, no browser
login page, no logger, and no tool in this repository. Sections marked
**[TARGET]** are requirements that must be satisfied before remote
authentication is enabled. They are written with `must` and `will` for that
reason. Only the section
[Mitigations that have landed](#mitigations-that-have-landed-now), marked
**[NOW]**, describes behavior that exists and is covered by tests.

Never cite a **[TARGET]** paragraph as evidence that a control is in place.
`docs/implementation-status.md` is the authoritative gap list.

The model covers assets, trust boundaries, attacker capabilities, and one
mitigation set per threat category. The threat coverage is complete on purpose:
the point of the split below is honesty about status, not a shorter analysis.

The operator of a remote deployment occupies a sensitive trust position: users
type their Garmin credentials into a page that the operator serves. Self-hosting
or a trusted operator is the recommended deployment.

## Mitigations that have landed [NOW]

Each item below is implemented and tested in this repository today. Every other
control in this document is still a requirement.

| Mitigation | Where | Threat category |
|------------|-------|-----------------|
| Secret redaction on every render path (`String`, `GoString`, `MarshalJSON`, `slog.LogValuer`), including the **method-stripping alias case**: a defined type over a secret-bearing type has no methods, so `fmt` reflects over the fields instead, and `alias_leak_test.go` and `strippedalias_test.go` prove nothing readable comes out under `%v`, `%+v`, `%#v`, `%s`, or `%q` | `internal/config/redact.go`, `internal/cryptostore/redact.go`, `internal/store/redact.go`, `internal/garmin/protocol/redact.go`, `internal/garmin/auth/secret.go` | 1 |
| Sanitized failure messages and login-error query redaction: a `protocol.Error` carries a fixed endpoint label, never a URL with a query string, and no credential, token, cookie, header, or raw body text reaches an error string | `internal/garmin/protocol` | 1 |
| Encrypted owner-only token records. AES-256-GCM with `crypto/rand` nonces, a versioned key ID in the envelope header and inside the AAD, and AAD binding the principal, the record type, **and the wrapper's schema and CAS version** with length prefixes, so a record cannot be moved between principals or record types and neither the schema nor the version can be edited on disk without the record failing to open | `internal/cryptostore/envelope.go`, `internal/store/tokens.go` (`recordAAD`) | 10 |
| Symlink and regular-file defenses. Every path is resolved component by component against directory file descriptors with `os.Root`, each component is `Lstat`-checked before it is opened and identity-checked with `os.SameFile` after, reads open `O_NONBLOCK` and require a regular file before and after the open, and owner-only modes are enforced by an explicit `chmod` on the open descriptor. `~user` paths and a symlink anywhere in the ancestry are refused | `internal/securefile`, `internal/store/path.go` | 10 |
| Exclusive key install. A completed temporary file is **hard-linked** into place rather than renamed, so a taken name reports `ErrExists` and two creators agree on one winner instead of clobbering each other | `internal/securefile.InstallNewFile` | 10 |
| Atomic writes with a hostile umask. Content lands in a random-suffixed temporary sibling, is `fsync`ed, and is renamed over the target with a directory sync; the subprocess test uses mask `0o277`, which strips the owner write bit, so the assertions can only hold if the explicit `chmod` ran | `internal/securefile`, `internal/store/filestore.go` | 9, 10 |
| Single completion lease per pending MFA transaction, plus a per-principal token gate that serializes every token-producing operation, so a login cannot overwrite the token set a concurrent refresh rotated. **The gate is not wired yet**; see the gap in `docs/implementation-status.md` | `internal/garmin/auth/attempt.go`, `internal/garmin/auth/gate.go` | 6, 9 |
| Constant-time capability comparison. The 256-bit MFA transaction capability is stored only as its SHA-256 and compared with `crypto/subtle.ConstantTimeCompare` | `internal/garmin/auth/capability.go` | 6 |
| Per-transaction pending MFA state in a bounded registry: a 5-minute absolute TTL that is never extended, a 5-attempt budget charged before the principal check, a 1024-entry cap, and an immutable deep-copied `Pending` per attempt, so interleaved logins cannot overwrite each other | `internal/garmin/auth/registry.go`, `pending.go` | 6 |
| Domain allowlist. Only `garmin.com` and `garmin.cn` parse into a `ValidatedDomain`, and every URL the auth package builds is derived from a `protocol.Hosts` created from one | `internal/garmin/protocol/domain.go`, `hosts.go` | 5 |
| Unverified-JWT hardening. `exp` is read for scheduling only and never for authorization; `alg:none` (case-folded), a missing or empty signature segment, a boolean, string, object or null `exp`, non-finite and overflowing values, and oversized tokens and segments are all rejected | `internal/garmin/auth/jwt_unverified.go`, `internal/store/document.go` | 3 |
| Configuration validation before anything is opened. Every check in `internal/config` is lexical: nothing binds, resolves, or opens a socket or a file, and `serve` validates the effective configuration before it reports its gap. Secret settings have no flag at all, so they cannot appear in a process listing, and `Config` has no password, MFA, email, or account-selector field, kept that way by two reflective guard tests | `internal/config/validate.go`, `validate_network.go`, `internal/cmd/serve.go` | 1, 8 |
| Refresh serialized per principal and rotating-token CAS, both asserted under `-race`. Concurrent refreshes for one principal collapse into one flight; different principals do not serialize; a save yields to a newer stored token set | `internal/garmin/auth/refresh.go`, `internal/store/filestore.go` | 9 |

Two limits on the list above, stated so it cannot be over-read:

- Cross-process compare-and-set does not exist. `FileStore.Save` compares the
  version under a per-process mutex with no file locking, so the file store is
  safe for a **single active instance** only.
- Windows ACL enforcement is real code that no CI runner executes. The decision
  rule is a pure function whose tests run on Linux, the one platform CI executes
  tests on; the platform-specific sources and their test files type-check for
  every `GOOS`, and the Windows syscall layer runs nowhere.

## Assets [TARGET]

The **Where it lives** column is the target placement. Only the encrypted DI
token set, the master key file, and transient login material exist today.

| Asset | Sensitivity | Where it lives |
|-------|-------------|----------------|
| Garmin email, password, MFA code | Highest. Never persisted | Transient request memory during one login attempt |
| Garmin DI token set (`di_token`, `di_refresh_token`, `di_client_id`) | Highest | Encrypted at rest in the store; decrypted only in the per-principal client |
| Master encryption key | Highest | Owner-only key file, or a secret manager / KMS adapter |
| MCP access and refresh tokens | High | Only SHA-256/HMAC lookup values are stored |
| Authorization codes and login transactions | High | Hashed, or in a bounded in-memory registry with a short TTL |
| Consent records and registered clients | Medium | Store |
| Principal identity and Garmin account linkage | High (personal data) | Store, with the Garmin account identifier keyed-HMAC'd or encrypted |
| Garmin health, nutrition, menstrual, location, and device data | Highest (special category) | Passed to the calling principal only; not persisted or shared-cached by default |
| Audit and application logs | Medium | Pseudonymous IDs and coarse categories only |
| Database file and its backups | Highest in aggregate | Operator-controlled volume |

## Trust boundaries [TARGET]

Boundaries 1, 2, and 5 do not exist yet, because no transport and no HTTP
handler exist. Boundary 3 exists on the login and refresh paths. Boundary 4
exists for the file store and the key file; the SQLite half does not.

1. **MCP client to server.** Crossed by Streamable HTTP requests and stdio
   frames. The principal will come only from a verified bearer token in the
   `Authorization` header. Tool arguments are untrusted.
2. **Browser to server.** Crossed by the login, MFA, and consent forms. Requests
   are untrusted and will need the transaction cookie plus the form CSRF token.
3. **Server to Garmin.** Crossed by outbound HTTPS. Garmin responses are
   untrusted input.
4. **Server to store and key material.** Crossed by local file and SQLite
   access. Protected by owner-only modes and envelope encryption.
5. **Reverse proxy to server.** Crossed by forwarded headers. Untrusted unless
   the peer is in a configured proxy CIDR.
6. **Between principals.** Crossed by nothing. Isolation must be enforced by
   per-principal keying of every client, token, cookie jar, cache entry, and
   result.

## Attacker capabilities assumed

- **Remote unauthenticated network attacker**: reaches every public route,
  replays captured requests, forges headers, and enumerates URLs.
- **Malicious or compromised MCP client**: holds a valid token for one
  principal, can register itself where registration is open, and sends arbitrary
  tool arguments.
- **Malicious end user**: drives the browser flow, submits arbitrary form values,
  and tries to link an account that is not theirs.
- **Prompt-injection content author**: controls text inside Garmin data that a
  model will read, and tries to induce destructive tool calls.
- **Offline data thief**: obtains the database file or a backup, but not the
  running host.
- **Log and telemetry reader**: reads logs, metrics, and traces, but not the
  store or key material.
- **Hostile local process**: runs as another local user and probes file modes,
  symlinks, and umask behavior.

The last two attackers are the only ones the current code can be tested
against, because the network-facing surface does not exist.

Out of scope: full compromise of the running host, a malicious operator, and
compromise of Garmin itself. A key colocated with the database protects backups
and file disclosure, not a compromised running host.

## Threat categories and mitigations [TARGET]

Every requirement below is normative for the target system. Where a part of it
has landed, the landed part is in the table above and is named again here.

### 1. Credential and token theft, log leakage

Passwords and MFA codes must exist only for the duration of one Garmin login
attempt, in mutable buffers where practical, with references dropped immediately
after. They must never be persisted, never a tool argument, never a CLI flag, and
never an environment variable. DI tokens must be encrypted with versioned AEAD
before storage and never returned to an MCP client. Only hashed MCP token
material may be stored. Secret-bearing structs must not print their fields
through `String`, `MarshalJSON`, error, or debug paths.

Landed: transient credential handling, the encrypted DI token set, and the
redaction suite including the method-stripping alias case.

Target: **there is no logger.** Logging will be structured `slog` with an
allowlisted field set; authorization and cookie headers, tokens, passwords, MFA
codes, client state, transaction capabilities, email, display names, health and
nutrition and menstrual metrics, GPS coordinates, identity-bearing filenames, and
Garmin payloads must be redacted. Bodies must never be logged by default. Exact
tool names may be logged only under an explicit safe-debugging policy, and
retention must be short. `Config.LogLevel` and `Config.LogFormat` are parsed and
validated, and nothing reads them.

### 2. Authorization-code, state, PKCE, CSRF, redirect, and refresh-token replay

None of this exists: there is no OAuth authorization server.

PKCE S256 must be mandatory; implicit and resource-owner-password grants must not
exist. Authorization codes must carry at least 256 bits of entropy, live at most
five minutes, be single-use, and be bound to client ID, exact redirect URI, PKCE
challenge, resource, scopes, and principal; token exchange must revalidate every
binding. The client's `state` must be preserved byte for byte and never reused as
the server's CSRF or session state; the server must generate an independent
transaction capability, browser cookie, and form CSRF token. Issuer, audience
(RFC 8707 `resource`), and redirect URI must use exact matching; fragments,
userinfo, wildcards, and non-HTTPS redirects must be rejected except
standards-compliant loopback. Duplicate or conflicting security parameters must be
rejected. Refresh tokens must rotate on every use, be bound to principal, client,
resource, and family, never expand scope or change resource, and reuse must
trigger transactional family revocation. Errors may redirect only after the client
and exact redirect URI are validated; otherwise a local sanitized error page must
be rendered. The negative OAuth matrix is a required test class.

### 3. Confused deputy and token passthrough

The MCP access token must never be forwarded to Garmin, and a Garmin DI token
must never be accepted or emitted as an MCP bearer token. The server must never
authorize from a decoded-but-unverified JWT; MCP tokens must be opaque, random,
hashed, and server-stored. Consent must be bound to
`(principal, client_id, exact redirect_uri, exact scopes, resource)`, so a
dynamically registered client cannot inherit another client's sticky consent.
Scope expansion or a redirect change must require fresh consent.

Landed: the unverified-JWT reader is quarantined by naming and documentation to
scheduling and diagnostics, and rejects `alg:none` and unsigned payloads. There
are no MCP tokens and no consent records to bind.

### 4. Cross-tenant object and handle access

No cross-tenant surface exists yet: there is no session, no client cache, no
download handle, and no tool.

The primary principal key must be a random internal UUID. No tool may accept
`user_id`, email, token path, or an account selector. Garmin clients must be
cached by internal principal only, with a bounded size and idle lifetime, and each
entry must own its cookies, token view, and refresh lock. `Mcp-Session-Id` and
`Last-Event-ID` must never be authentication: every session and event buffer must
be bound to the verified principal, client, resource, and scopes, and
cross-principal resume, read, or delete attempts must be rejected. Download
handles must be short-lived and principal-bound. Race-detector tests must prove
concurrent principals cannot share clients, tokens, cookies, results, or errors.

Landed in part: `Config` has no account selector, so ambiguous multi-account
configuration is unrepresentable, and each login and continuation builds its own
session and cookie jar.

### 5. Malicious dynamic registration, client metadata, SSRF, and DNS rebinding

Pre-registered operator clients must be the default, and no vendor client ID may
be hardcoded. Unrestricted anonymous production registration is prohibited. If
RFC 7591 registration is enabled, it must require an initial-access token or an
explicit production policy, plus quotas, strict redirect schemes and hosts,
metadata size limits, rate limits, audit events, and operator revocation.
Conformance testing must use a separate constrained profile. If Client ID
Metadata Documents are selected, retrieval must be SSRF-safe under an explicit
trust policy. The server must never fetch a user-controlled URL for uploads. Any
future fetcher must be a dedicated SSRF-safe component with a scheme allowlist,
DNS and IP controls, redirect revalidation, and egress policy.

Landed: the domain allowlist. Only `garmin.com` and `garmin.cn` parse into a
`ValidatedDomain`, and the auth package builds every URL from a `Hosts` derived
from one. Still target: a request-time host check on the token-attaching call
path. `Refresher.Do` attaches the bearer token to a caller-supplied
`*http.Request` and does not inspect its host.

### 6. Session fixation, login CSRF, clickjacking, brute force, account enumeration

The login route must be transaction-gated: without a valid transaction cookie and
a matching form CSRF value it must return a generic 404 or expired page with no
account disclosure. The transaction capability must carry at least 256 bits, be
stored only as a SHA-256/HMAC lookup value, be delivered as a short-lived
host-only cookie, and never appear in a path or query. It must be bound to the
original authorization request, browser session, client ID, redirect URI,
resource, and PKCE challenge, have a short absolute TTL, a bounded attempt count,
and a single-use terminal transition; cross-user, cross-client, expired, replayed,
and out-of-order transitions must be rejected. Remote cookies must use a
`__Host-` name with `Secure`, `HttpOnly`, `Path=/`, no `Domain`, and an
appropriate `SameSite`; the one-shot loopback profile must use a per-run host-only
`HttpOnly` cookie and be tested separately. Responses must set a restrictive CSP
with `frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, `Cache-Control: no-store`, and HSTS on public
HTTPS. Login attempts must be limited per IP and MFA attempts per transaction.
Error text must not distinguish an unknown account from a wrong password.

Landed: the capability entropy, its SHA-256-only storage, the constant-time
comparison, the 5-minute non-extendable TTL, the 5-attempt budget, the
single-use terminal transition, and the completion lease. **Not landed:** there
is no route, no cookie, no CSRF token, no security header, and no per-IP limit,
because no HTTP handler exists. A registry entry is bound to its principal and to
nothing else; binding it to the browser session, client, redirect URI, resource,
and PKCE challenge belongs with the M2 OAuth transaction that creates those
values.

### 7. Untrusted Garmin JSON and files, oversized or compressed payloads, path traversal

Garmin responses are untrusted. Reads must use tolerant decoding, so unknown
fields cannot fail an otherwise useful response, with bounded response sizes and
safe connect, header, and body timeouts. Uploads and downloads must cap raw and
decompressed sizes, parse defensively, reject traversal, and sanitize filenames
and content types. Fuzz tests must cover Garmin union and positional JSON, date
and range parsing, filenames, FIT and GPX input, OAuth parameters, URLs, and
token JSON. A remote tool must never be able to write an arbitrary server
filesystem path; downloads must return a bounded MCP resource or blob, or a
short-lived principal-bound handle.

Landed: bounded token and segment sizes in the JWT reader and the token
document, and component-by-component path resolution for the store and key paths.
**Not landed:** no fuzz target exists anywhere in the repository, and there is no
upload, download, or filename-taking path to guard.

### 8. Reverse-proxy host and header spoofing

An explicit canonical public URL is required. Issuer, callback, and resource URLs
must never be derived from `Host` or `X-Forwarded-*`. Forwarded headers may be
trusted only from configured proxy CIDRs. Browser form requests must require the
expected Origin and CSRF protections; Streamable HTTP requests that carry
`Origin` must match the configured allowlist, while standards-compliant
non-browser token requests may omit it. CORS must default to deny. Production
remote mode must refuse cleartext public binding unless an explicit development
override is set.

Landed in part, and lexically only: `internal/config` requires an explicit bind
address and public URL, validates the TLS pair and the proxy-trust CIDRs, and
refuses an unprotected non-loopback listener. Nothing binds, and no header is
ever read, because there is no listener.

### 9. Concurrent refresh races and stale-token overwrite

Refresh must be serialized per principal. Persistence must use optimistic version
or CAS semantics, so a rotated token cannot be overwritten by a concurrent
writer, and writes must be atomic. Refresh must happen before expiry with a
configurable safety window whose default is 15 minutes. After a `401` the client
must retry at most once, only after a successful refresh, and only for safe or
idempotent calls. Concurrent linking of the same Garmin account through two
browser flows must have a defined and tested transactional outcome.

Landed: per-principal collapsing of concurrent refreshes over a `sync.Mutex` and
an in-flight map with a done channel — there is no `singleflight` package
involved — CAS save, atomic writes, the 15-minute default window, and the
single bounded retry after a `401` that never replays a `POST` or `PATCH`.
**Not landed:** one shared `auth.TokenGate` is not wired, so login serializes
against login and refresh against refresh; cross-process CAS does not exist; and
concurrent account linking has no flow to be transactional about.

### 10. Database and file theft, master-key rotation

Garmin tokens and sensitive identity fields must use versioned AEAD envelope
encryption with `crypto/rand` nonces and authenticated additional data binding the
principal ID and record type, so a record cannot be moved between principals or
record types. The shipped backend must be an owner-only file holding a versioned
key ID and a base64 32-byte master key; remote mode must refuse to start on
missing, malformed, or overly permissive key material. The key must never be
logged or printed. Staged key rotation and a tested migration path are required.
Local token files must use `0700` directories and `0600` files, reject symlinks,
write atomically, and be tested against a hostile umask in isolated subprocesses
on Unix and against user-only ACL or key protection on Windows. Inline token JSON
is an explicitly insecure compatibility override and must be rejected in remote
production mode. Deleting local tokens is unlinking, not remote revocation, and
the documentation must state the difference. Encrypted-store tamper and wrong-key
tests are required, together with backup and restore tests.

Landed: the envelope format, the AAD binding including the schema and CAS
version, the owner-only key file with exclusive link-based install, the `0600`
in `0700` modes re-checked on read, symlink and `~user` refusal across the full
ancestry, atomic writes, the hostile-umask subprocess test, tamper, wrong-key,
wrong-principal and wrong-record-type tests, and staged rotation proven inside
`internal/cryptostore`. **Not landed:** there is no SQLite backend; nothing opens
the key at start-up and acts on the sentinels, so the refusal to start on bad key
material is only the lexical half; `FileStore` holds one key and re-seals
nothing, so rotation is a library capability and not an operator procedure; and
there is no backup or restore test. Inline token JSON is refused unless
explicitly enabled, and no caller connects it yet.

### 11. Malicious tool arguments and accidental destructive actions

No tool exists, so none of this is in force.

Every tool must have a strict JSON schema with ranges, formats, and defaults, and
must declare all four annotation hints. Scope and operator policy must be
enforced before any Garmin call. Write and destructive tiers must require the
intersection of operator enablement and granted scope; remote deployments must
default to read-only. Explicit `writeTools` and `destructiveTools` name lists must
be validated against the registered set at startup, so a typo fails fast.
Allowlists and denylists must reject unknown names at startup and be intersected
with scopes, never used to bypass them. Destructive operations must request MCP
elicitation confirmation with a bounded timeout and **fail closed**: without
confirmation the operation is refused and the refusal names the reason. An
optional safety delay with progress notifications must precede write and
destructive execution. This is also the control against prompt injection in
Garmin-sourced text: no model-authored argument may reach a destructive path
without scope, enablement, and human confirmation.

### 12. Denial of service and Garmin account rate limiting

No rate limiter exists.

Layered limits must apply: global concurrency, per-IP login attempts,
per-transaction MFA attempts, per-client authorization attempts, per-principal
Garmin calls, per-tool cost, body and response byte caps, and timeouts. Rate
limiting must be handler middleware that returns a caller-actionable error result
instead of a transport error. Garmin `429` must be classified distinctly, must
never be reported as a bad password, and must honor `Retry-After` with bounded
jitter and an account-level cooldown. Only transport failures and selected `5xx`
responses may be retried, with bounded exponential backoff and full jitter;
non-idempotent mutations, deletes, ordinary `4xx` responses, and password or MFA
submissions must never be retried automatically. Expired transactions, codes, and
tokens must be cleaned by a bounded periodic job and by on-access checks.
Graceful shutdown must stop accepting requests, cancel or expire login
transactions, finish bounded in-flight calls, flush safe telemetry, and close
stores.

Landed: the per-transaction MFA attempt budget, the bounded registry with its
entry cap and TTL, distinct rate-limit classification in
`internal/garmin/protocol` with an early stop on rate limiting during DI ticket
exchange, and the single bounded post-`401` retry. Everything else waits for a
server.

## Revocation and unlink [TARGET]

Nothing here exists: there is no consent record, no token family, no transport
session, and no unlink path.

Revocation must be transactional and idempotent. Revoking a client consent must
revoke that client's token families for the principal and close its active
transport sessions. Unlinking a Garmin account must revoke every MCP token family
for the principal, delete the encrypted Garmin tokens and pending transactions,
stop background refresh, and evict clients and caches. Partial deletion must fail
closed and emit only a redacted audit event.

## Operational exposure [TARGET]

There are no operational endpoints, no metrics, and no audit events.

`/livez` and `/readyz` must expose no secret detail. Metrics are optional and
must use bounded-cardinality labels only; raw user IDs, emails, activity IDs, and
tool arguments must never appear in labels. Administration and metrics endpoints
must run on a separate listener or be explicitly protected. Audit events must
contain no credentials and no health or location payloads.
