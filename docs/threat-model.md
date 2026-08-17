# Threat model

**Read this first.** The MCP server, the OAuth authorization server, the browser
login pages, the logger, the SQLite store and 47 tools all exist now, and most of
the controls below have landed. Sections marked **[TARGET]** are requirements
written with `must` and `will`; a `must` sentence is a requirement, never a claim
that the code exists. The section
[Mitigations that have landed](#mitigations-that-have-landed-now), marked
**[NOW]**, is the list of controls that exist and are covered by tests, each with
the file that proves it.

Never cite a **[TARGET]** paragraph as evidence that a control is in place.
`docs/implementation-status.md` is the authoritative gap list.

The model covers assets, trust boundaries, attacker capabilities, and one
mitigation set per threat category. The threat coverage is complete on purpose:
the point of the split below is honesty about status, not a shorter analysis.

The operator of a remote deployment occupies a sensitive trust position: users
type their Garmin credentials into a page that the operator serves. Self-hosting
or a trusted operator is the recommended deployment.

## Mitigations that have landed [NOW]

Each item below is implemented and tested in this repository today, with the
file that proves it. A control that appears only in a **[TARGET]** section below
is still a requirement.

| Mitigation | Where | Threat category |
|------------|-------|-----------------|
| Secret redaction on every render path (`String`, `GoString`, `MarshalJSON`, `slog.LogValuer`), including the **method-stripping alias case**: a defined type over a secret-bearing type has no methods, so `fmt` reflects over the fields instead, and `alias_leak_test.go` and `strippedalias_test.go` prove nothing readable comes out under `%v`, `%+v`, `%#v`, `%s`, or `%q` | `internal/config/redact.go`, `internal/cryptostore/redact.go`, `internal/store/redact.go`, `internal/garmin/protocol/redact.go`, `internal/garmin/auth/secret.go` | 1 |
| Sanitized failure messages and login-error query redaction: a `protocol.Error` carries a fixed endpoint label, never a URL with a query string, and no credential, token, cookie, header, or raw body text reaches an error string | `internal/garmin/protocol` | 1 |
| Encrypted owner-only token records. AES-256-GCM with `crypto/rand` nonces, a versioned key ID in the envelope header and inside the AAD, and AAD binding the principal, the record type, **and the wrapper's schema and CAS version** with length prefixes, so a record cannot be moved between principals or record types and neither the schema nor the version can be edited on disk without the record failing to open | `internal/cryptostore/envelope.go`, `internal/store/tokens.go` (`recordAAD`) | 10 |
| Symlink and regular-file defenses. Every path is resolved component by component against directory file descriptors with `os.Root`, each component is `Lstat`-checked before it is opened and identity-checked with `os.SameFile` after, reads open `O_NONBLOCK` and require a regular file before and after the open, and owner-only modes are enforced by an explicit `chmod` on the open descriptor. `~user` paths and a symlink anywhere in the ancestry are refused | `internal/securefile`, `internal/store/path.go` | 10 |
| Exclusive key install. A completed temporary file is **hard-linked** into place rather than renamed, so a taken name reports `ErrExists` and two creators agree on one winner instead of clobbering each other | `internal/securefile.InstallNewFile` | 10 |
| Atomic writes with a hostile umask. Content lands in a random-suffixed temporary sibling, is `fsync`ed, and is renamed over the target with a directory sync; the subprocess test uses mask `0o277`, which strips the owner write bit, so the assertions can only hold if the explicit `chmod` ran | `internal/securefile`, `internal/store/filestore.go` | 9, 10 |
| Single completion lease per pending MFA transaction: the lease holder alone may claim the terminal transition, a second submission of the same capability does no work, and a wrong code releases the lease | `internal/garmin/auth/attempt.go` | 6 |
| Constant-time capability comparison. The 256-bit MFA transaction capability is stored only as its SHA-256 and compared with `crypto/subtle.ConstantTimeCompare` | `internal/garmin/auth/capability.go` | 6 |
| Per-transaction pending MFA state in a bounded registry: a 5-minute absolute TTL that is never extended, a 5-attempt budget charged before the principal check, a 1024-entry cap, and an immutable deep-copied `Pending` per attempt, so interleaved logins cannot overwrite each other | `internal/garmin/auth/registry.go`, `pending.go` | 6 |
| Domain allowlist. Only `garmin.com` and `garmin.cn` parse into a `ValidatedDomain`, and every URL the auth package builds is derived from a `protocol.Hosts` created from one | `internal/garmin/protocol/domain.go`, `hosts.go` | 5 |
| Unverified-JWT hardening. `exp` is read for scheduling only and never for authorization; `alg:none` (case-folded), a missing or empty signature segment, a boolean, string, object or null `exp`, non-finite and overflowing values, and oversized tokens and segments are all rejected | `internal/garmin/auth/jwt_unverified.go`, `internal/store/document.go` | 3 |
| Configuration validation before anything is opened. Every check in `internal/config` is lexical: nothing binds, resolves, or opens a socket or a file, and every command validates the effective configuration before it opens anything. Secret settings have no flag at all, so they cannot appear in a process listing, and `Config` has no password, MFA, email, or account-selector field, kept that way by two reflective guard tests | `internal/config/validate.go`, `validate_network.go`, `internal/cmd/serve.go` | 1, 8 |
| Refresh serialized per principal and rotating-token CAS, both asserted under `-race`. Concurrent refreshes for one principal collapse into one flight; different principals do not serialize; a save yields to a newer stored token set | `internal/garmin/auth/refresh.go`, `internal/store/filestore.go` | 9 |
| One shared `auth.TokenGate` per process, so a login cannot overwrite the token set a concurrent refresh rotated. The composition root passes the same pointer to `auth.Config` and `auth.RefreshConfig`, and a test asserts the identity | `internal/cmd/wiring.go`, `internal/garmin/auth/gate.go`, `internal/cmd/wiring_test.go` (`TestServeSharesOneTokenGateBetweenLoginAndRefresh`) | 9 |
| Request-time host guard. A caller-supplied request whose host is not a validated Garmin host is refused with `ErrForeignHost`, on the first attempt and on the post-`401` replay, so the Garmin bearer token cannot be attached to a foreign host | `internal/garmin/auth/hostguard.go` | 5 |
| PKCE S256 only. `plain` exists solely to be refused, a zero challenge fails, and the schema repeats the rule as `CHECK (code_challenge_method = 'S256')` | `internal/oauthserver/pkce.go`, `migrations/0001_initial.sql` | 2 |
| Exact issuer and byte-exact redirect matching. Host case, default port and trailing slash are not folded, and every binding is revalidated at redemption | `internal/oauthserver/config.go`, `internal/oauthserver/uri.go`, `internal/oauthserver/codegrant.go` | 2 |
| Client `state` echoed byte for byte and never reused as server state. The transaction capability, the browser cookie and the form CSRF token are three independent server-generated values | `internal/oauthserver/state.go`, `internal/oauthserver/authorize.go`, `internal/loginweb/remotesession.go` | 2, 6 |
| Single-use authorization codes bound to client, exact redirect, PKCE challenge, resource, scopes and principal, with a 60-second default TTL under a 5-minute ceiling. Redemption consumes the code atomically before anything else, and one redeemer wins under contention | `internal/oauthserver/codegrant.go`, `internal/oauthserver/records.go`, `internal/oauthstore/race_test.go` (`TestConsumeCodeElectsExactlyOneRedeemer`) | 2 |
| Opaque MCP credentials with 256 bits of entropy from `crypto/rand`, persisted and compared only as a SHA-256 lookup value. The stored columns are `code_hash`, `handle_hash`, `secret_hash` and `token_hash` | `internal/oauthserver/secret.go`, `migrations/0001_initial.sql` | 1, 3 |
| Refresh-token rotation on every use, with reuse detection that revokes the whole family transactionally and commits the revocation even on the error path | `internal/oauthserver/refreshgrant.go`, `internal/store/sqlite_rotate.go`, `internal/oauthstore/race_test.go` (`TestRotateRefreshTokenElectsOneWinnerAndKillsTheFamily`) | 2 |
| Consent bound to `(principal, client, exact redirect, resource)` with the consented scopes as the value. A request is admitted only when the requested set is a subset, so scope widening or a redirect change needs fresh consent | `internal/oauthserver/records.go`, `migrations/0002_oauth_contract.sql`, `internal/store/sqlite_consents.go` | 3 |
| The principal is a random internal UUID from `crypto/rand`, never derived from an email or a Garmin id, with the Garmin account linkage stored as a unique keyed hash beside a sealed identity blob | `internal/store/sqlite_principals.go`, `migrations/0001_initial.sql` | 4 |
| The principal comes only from a verified bearer token. A principal already on the request context is deliberately not consulted, and every failure collapses to `ErrNoPrincipal` | `internal/identity/bearer.go`, `internal/cmd/remote_test.go` (`TestRemotePrincipalComesOnlyFromAVerifiedToken`) | 3, 4 |
| The bearer is read from the `Authorization` header and nowhere else: not a query parameter, not a cookie, not a body field. Proven over the real binary | `internal/oauthserver/verify.go`, `e2e/remote_test.go` | 1, 3 |
| Sessions bound to principal, client, resource and scopes, with the session id stored only as a hash and treated as a routing label rather than a credential. A revocation terminates the sessions it covers | `internal/mcpserver/httpsession.go`, `internal/mcpserver/http.go`, `internal/mcpserver/httpsession_test.go` (`TestSessionIsTerminatedByRevocation`) | 4 |
| Protected Resource Metadata and the RFC 6750 challenge, with `realm`, `resource_metadata`, and `invalid_token` / `insufficient_scope`, and a bare challenge when no credential was presented. `bearer_methods_supported` is exactly `["header"]` | `internal/mcpserver/http.go`, `internal/oauthserver/verify.go`, `e2e/remote_test.go` | 2, 8 |
| Origin allowlist with CORS defaulting to deny, forwarded headers trusted only from configured proxy CIDRs, and a cleartext public bind refused without an explicit development override | `internal/mcpserver/httporigin.go`, `internal/mcpserver/http.go` (`validateBind`) | 8 |
| The remote browser profile: a `__Host-` cookie, HSTS, a capability that never appears in a path, a query, a page or a log line, the disclosure page before credential entry, an independent CSRF token that is constant-time compared and rotated, and MFA continuation held server-side | `internal/loginweb/remote.go`, `headers.go`, `remoteflow.go`, `remotesession.go`, `remotehandlers.go`, with `TestTheCapabilityNeverAppearsInAURLOrAPage` and `TestRemoteMFAKeepsTheContinuationServerSide` | 6 |
| Tool policy as the intersection of operator enablement and granted scope, with explicit tier name lists validated against the registered set in both directions at start-up, an allowlist that can only narrow, and a refusal reason that never names the tool | `internal/policy/policy.go`, `internal/policy/tier.go`, `internal/tools/register.go` (`validateTierLists`), `internal/mcpserver/middleware.go` | 11 |
| Destructive confirmation that fails closed. A client that cannot be asked, a user who declines and a wait that elapses all refuse the call | `internal/policy/confirm.go`, `internal/mcpserver/confirm.go` | 11 |
| Bounded reads. Wire and decompressed response sizes, page size, page start and date windows are all capped, and a caller-chosen page size is narrowed to the configured cap rather than honored | `internal/garmin/client/limits.go`, `internal/garmin/client/models.go`, `internal/tools/args.go` | 7, 12 |
| No caller-supplied server filesystem path exists on the tool surface. `download_activity_file` takes an activity id and a format only and returns a bounded embedded resource, refusing an oversized payload rather than truncating it; `set_fit_download_dir` is not registered at all | `internal/tools/downloads.go` | 7 |
| Structured `slog` logging with an allowlisted field set, on stderr, refusing stdout so stdio frames stay clean | `internal/mcplog/logger.go`, `internal/mcplog/event.go` | 1 |
| Per-principal rate limiting as handler middleware that returns a caller-actionable error result rather than a transport error | `internal/ratelimit/limiter.go`, `internal/ratelimit/middleware.go` | 12 |
| Transactional revocation and unlink cascades that fail closed on partial deletion, proven under contention | `internal/store/sqlite_revoke.go`, `internal/store/sqlite_unlink.go`, `internal/oauthstore/race_test.go` (`TestRevokeConsentIsSafeUnderContention`, `TestRevokePrincipalIsSafeUnderContention`) | 4 |
| The SQLite concurrency contract asserted by **querying** the pragmas on every pooled connection — WAL, foreign keys, busy timeout, synchronous — rather than by inspecting the DSN string | `internal/store/sqlite_db.go`, `internal/store/sqlite_pragma_test.go` | 10 |
| Start-up refusal on bad key material. The composition root opens the key before it serves, and `doctor` branches on `ErrKeyNotFound` and `ErrInsecureKeyPermissions` | `internal/cmd/components.go`, `internal/cmd/remote.go`, `internal/cmd/doctor.go` | 10 |
| Mode isolation inside one process: the stdio and remote shapes share no token gate, token store, policy, limiter, principal resolver or file store | `internal/cmd/remote_test.go` (`TestRemoteAndStdioShareNoState`) | 4 |

Six limits on the list above, stated so it cannot be over-read:

- Cross-process compare-and-set does not exist for the file store.
  `FileStore.Save` compares the version under a per-process mutex with no file
  locking, so it is safe for a **single active instance** only. The SQLite
  backend has real CAS, and its v1 deployment is single-active-instance too.
- `mcpserver.Revocation` has no resource selector, so revoking one consent closes
  slightly more sessions than that grant covered. The direction is fail-safe.
- A revocation event dropped under buffer pressure costs the affected session its
  early termination only. The database stays the authority and the token check
  refuses the next request.
- Consent scopes are compared by containment, not held in the consent key. That
  is what makes scope widening need fresh consent while narrowing does not; it is
  a deliberate design, not an approximation.
- No MCP resource exists, so the resource half of every control below is
  untested by construction.

## Assets [TARGET]

Every row now has a real placement. The remote rows depend on the SQLite
backend, which the stdio deployment does not open.

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

All six boundaries exist now. Boundaries 1, 2 and 5 arrived with the transports
and the HTTP handlers, and boundary 4 covers the SQLite backend as well as the
file store and the key file. The `must` wording below is kept as the standing
requirement for each one.

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

Every attacker in this list now has a surface to be tested against, because the
network-facing code exists. The remote unauthenticated attacker and the
malicious MCP client are covered by the OAuth negative matrix, the transport
tests and `e2e/remote_test.go`.

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

Landed: transient credential handling, the encrypted DI token set, the redaction
suite including the method-stripping alias case, hashed-only MCP token material,
and the logger. `internal/mcplog` is structured `slog` with an allowlisted field
set on stderr, and `internal/tools` and `internal/mcpserver` carry their own
redaction tests over the tool result, error and HTTP paths. Bodies are not
logged.

Target: the safe-debugging policy for exact tool names, and a stated retention
period. Neither exists as a written policy.

### 2. Authorization-code, state, PKCE, CSRF, redirect, and refresh-token replay

Landed in full, in `internal/oauthserver` with `internal/oauthstore` and the
`migrations` schema behind it, and the negative OAuth matrix passes. The named
files are in the landed table above. One deliberate difference from the wording
below: the code TTL is 60 seconds by default under a 5-minute ceiling, which is
stricter than the requirement.

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

Landed in full. The unverified-JWT reader is quarantined by naming and
documentation to scheduling and diagnostics and rejects `alg:none` and unsigned
payloads; MCP tokens are opaque, random, hashed and server-stored; the MCP token
is never forwarded to Garmin and a Garmin DI token is never accepted as an MCP
bearer; and consent is bound to the full tuple with scopes compared by
containment, so a dynamically registered client cannot inherit sticky consent.

### 4. Cross-tenant object and handle access

Landed, with two exceptions named at the end. Sessions, the principal key, the
tool surface and the isolation tests all exist. There is still **no client
cache** and **no download handle**: a download returns a bounded embedded
resource in the same response, so there is no handle to bind or expire.

The primary principal key must be a random internal UUID. No tool may accept
`user_id`, email, token path, or an account selector. Garmin clients must be
cached by internal principal only, with a bounded size and idle lifetime, and each
entry must own its cookies, token view, and refresh lock. `Mcp-Session-Id` and
`Last-Event-ID` must never be authentication: every session and event buffer must
be bound to the verified principal, client, resource, and scopes, and
cross-principal resume, read, or delete attempts must be rejected. Download
handles must be short-lived and principal-bound. Race-detector tests must prove
concurrent principals cannot share clients, tokens, cookies, results, or errors.

Landed: the random internal UUID principal key, the absence of any account
selector on the tool surface, a session bound to principal, client, resource and
scopes with the session id stored only as a hash, transactional revocation
cascades, and the race-detector isolation tests including
`TestRemoteAndStdioShareNoState`. Each login and continuation builds its own
session and cookie jar.

**Not landed:** the bounded, idle-expiring per-principal Garmin client cache. No
cache exists at all, which is safe but means the requirement is unmet rather than
satisfied. `mcpserver.Revocation` also carries no resource selector, so a
revocation closes slightly more sessions than the grant covered.

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

Landed: the domain allowlist and the request-time host check.
Only `garmin.com` and `garmin.cn` parse into a `ValidatedDomain`, every URL is
built from a `Hosts` derived from one, and `internal/garmin/auth/hostguard.go`
refuses a caller-supplied request whose host is not a validated Garmin host, on
the first attempt and on the post-`401` replay. Registration is preregistration
only: `internal/config/oauthclient.go` takes operator-written clients with exact
redirect URIs and a secret digest supplied through a file, and there is no RFC
7591 endpoint. No user-controlled URL is ever fetched.

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
comparison, the 5-minute non-extendable TTL, the 5-attempt budget, the single-use
terminal transition, the completion lease, and now the whole browser surface —
the transaction-gated route, the `__Host-` cookie with `Secure`, `HttpOnly`,
`Path=/`, no `Domain` and `SameSite=Lax`, the independent form CSRF token that is
constant-time compared and rotated, the security headers with HSTS, and the
authorization transaction that binds the capability to the client, the exact
redirect URI, the resource and the PKCE challenge. The loopback profile is
separate and separately tested.

**`SameSite=Lax`, not `Strict`, is deliberate**: `Strict` is not sent on the
cross-site top-level navigation that starts the flow, so the flow would break.

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

Landed: tolerant decoding on every Garmin read, bounded wire and decompressed
response sizes, bounded page size, page start and date windows, bounded token and
segment sizes in the JWT reader and the token document, and
component-by-component path resolution for the store and key paths. The download
path takes no caller-supplied filename at all and returns a bounded embedded
resource, refusing an oversized payload rather than truncating it.

**Not landed:** no fuzz target exists anywhere in the repository. That is the one
outstanding requirement in this category.

### 8. Reverse-proxy host and header spoofing

An explicit canonical public URL is required. Issuer, callback, and resource URLs
must never be derived from `Host` or `X-Forwarded-*`. Forwarded headers may be
trusted only from configured proxy CIDRs. Browser form requests must require the
expected Origin and CSRF protections; Streamable HTTP requests that carry
`Origin` must match the configured allowlist, while standards-compliant
non-browser token requests may omit it. CORS must default to deny. Production
remote mode must refuse cleartext public binding unless an explicit development
override is set.

Landed. `internal/config` requires an explicit bind address and public URL,
validates the TLS pair and the proxy-trust CIDRs, and refuses an unprotected
non-loopback listener; `internal/mcpserver` then enforces the runtime half. The
issuer, callback and resource URLs come from the configured public URL and never
from `Host` or `X-Forwarded-*`, forwarded headers are trusted only from the
configured proxy CIDRs, CORS defaults to deny, and a cleartext public bind is
refused unless an explicit development override is set.

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
Also landed: one shared `auth.TokenGate` is wired by the composition root and
asserted by test, so a login cannot overwrite a rotated token set, and the SQLite
backend gives real cross-connection CAS with `ErrVersionConflict`.

**Not landed:** cross-process CAS for the **file** store, which stays
single-active-instance; and a test for concurrent linking of the same Garmin
account through two browser flows.

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
on Unix. Windows is not a supported platform, so there is no ACL requirement here:
`internal/securefile` compiles on unix only, which is the honest outcome for a
package whose purpose is refusing to hold a secret under permissions it cannot
verify. Inline token JSON
is an explicitly insecure compatibility override and must be rejected in remote
production mode. Deleting local tokens is unlinking, not remote revocation, and
the documentation must state the difference. Encrypted-store tamper and wrong-key
tests are required. Backup and restore are **deliberately not tested here**: the
database lives on an operator-controlled volume and backing it up is the
operator's responsibility. `docs/operations.md` documents the procedure, including
that the database and the master key are two halves of one backup and that a
restore rolls consents back to the backup's moment.

Landed: the envelope format, the AAD binding including the schema and CAS
version, the owner-only key file with exclusive link-based install, the `0600` in
`0700` modes re-checked on read, symlink and `~user` refusal across the full
ancestry, atomic writes, the hostile-umask subprocess test, tamper, wrong-key,
wrong-principal and wrong-record-type tests, staged rotation proven inside
`internal/cryptostore`, the migration-backed SQLite backend with its pragmas
asserted by query, and start-up refusal on bad key material now that the
composition root opens the key and `doctor` branches on the sentinels. Inline
token JSON is refused unless explicitly enabled, and it now has exactly one
caller.

**Not landed:** no store re-seals existing records, so key rotation is a library
capability and not an operator procedure. Backup and restore are out of scope by
decision rather than missing — see above.
`docs/operations.md` does exist, and its "Key management" section states the same
limitation in an operator's terms — that a staged rotation is supported by
`internal/cryptostore`, that nothing drives it, and that rotation should therefore
be treated as unavailable rather than attempted.

### 11. Malicious tool arguments and accidental destructive actions

Landed for the 100 registered tools. Each declares all four annotation hints and a
strict schema, scope and operator policy are enforced before any Garmin call, the
three tier name lists are validated against the registered set in both directions
at start-up, allowlist and denylist are intersected with the tiers, and
destructive confirmation fails closed. The optional safety delay has landed as
`safety-delay`, default `0`: it pauses write and destructive calls after every gate
and before the handler, and the wait is interruptible, so a caller that cancels
during it stops the call before anything reaches Garmin. **Not landed:** the
progress notifications that were to accompany that delay. A paused call is
indistinguishable from a slow one to the client, which blunts the delay for a human
watching, and is part of why the setting is off by default.

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
optional safety delay must precede write and destructive execution, must be
interruptible — a pause nothing can interrupt is latency rather than safety — and
must sit after every gate so a refused call never waits. The progress notifications
during that pause are still required and still missing. This is also the control against prompt injection in
Garmin-sourced text: no model-authored argument may reach a destructive path
without scope, enablement, and human confirmation.

### 12. Denial of service and Garmin account rate limiting

Landed in large part; the remaining gaps are named after the requirement.

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
exchange, the single bounded post-`401` retry that never replays a `POST` or
`PATCH`, the per-principal limiter as handler middleware returning a
caller-actionable error result, the request-body cap, the response byte caps, and
bounded expiry cleanup in the SQLite store.

**Not landed:** global concurrency limiting, per-tool cost accounting, and a
documented graceful-shutdown sequence.

## Revocation and unlink [TARGET]

Landed. Consent records, token families, transport sessions and the unlink path
all exist, revocation is transactional and idempotent, and the cascades are
proven under contention in `internal/oauthstore/race_test.go`. The one accepted
imprecision is that `mcpserver.Revocation` carries no resource selector, so a
consent revocation closes slightly more sessions than the grant covered.

Revocation must be transactional and idempotent. Revoking a client consent must
revoke that client's token families for the principal and close its active
transport sessions. Unlinking a Garmin account must revoke every MCP token family
for the principal, delete the encrypted Garmin tokens and pending transactions,
stop background refresh, and evict clients and caches. Partial deletion must fail
closed and emit only a redacted audit event.

## Operational exposure

Audit events exist: the SQLite store writes them with no credentials and no
health or location payloads. `/livez` and `/readyz` exist too
(`internal/mcpserver/httpprobe.go`): the paths are constants rather than
operator-renameable options, the bodies are a fixed `ok` / `not ready`, and the
readiness check is injected and bounded by a two-second timeout, so a wedged
store answers honestly instead of hanging. A real MCP route published on either
path still wins, so a probe cannot shadow the server's own surface.

There are still **no** metrics and **no** separate administration listener.

`/livez` and `/readyz` must expose no secret detail. Metrics are optional and
must use bounded-cardinality labels only; raw user IDs, emails, activity IDs, and
tool arguments must never appear in labels. Administration and metrics endpoints
must run on a separate listener or be explicitly protected. Audit events must
contain no credentials and no health or location payloads.
