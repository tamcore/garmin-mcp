# Threat model

This model covers assets, trust boundaries, attacker capabilities, and one
mitigation set per threat category. Each category states the control and then
any known limit. The table under
[Implemented controls](#implemented-controls) names the file that proves each
control.

The operator of a remote deployment occupies a sensitive trust position: users
type their Garmin credentials into a page that the operator serves. Self-hosting
or a trusted operator is the recommended deployment.

## Implemented controls

Each item below is implemented and tested, with the file that proves it.

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
| Exact issuer and byte-exact redirect matching, except that a loopback redirect URI admits any port per RFC 8252 §7.3. Host case and trailing slash are not folded, and every binding is revalidated at redemption | `internal/oauthserver/config.go`, `internal/oauthserver/uri.go`, `internal/oauthserver/codegrant.go` | 2 |
| Opt-in trailing-path redirect wildcard, off by default. `oauth-allow-redirect-wildcards` is the operator's acknowledgement; the pattern must satisfy every exact-URI origin rule, carry no query, and have its remainder free of `.`, `..`, and empty path segments, so a match can never resolve outside the registered prefix. `MatchRedirectURI` still returns the concrete presented URI, so consent and the issued code stay bound to it, not to the pattern | `internal/oauthserver/redirectpattern.go`, `internal/oauthserver/client.go` | 2 |
| Login-time account allowlist. `login-allowed-emails` is checked before the Garmin login call, refusing an unlisted address with the same generic message a rejected credential produces so the check cannot be used to enumerate accounts; empty admits any account and the setting gates login, not an existing principal | `internal/loginweb/allowlist.go`, `internal/loginweb/remotehandlers.go` | 4, 6 |
| Client `state` echoed byte for byte and never reused as server state. The transaction capability, the browser cookie and the form CSRF token are three independent server-generated values | `internal/oauthserver/state.go`, `internal/oauthserver/authorize.go`, `internal/loginweb/remotesession.go` | 2, 6 |
| Single-use authorization codes bound to client, exact redirect, PKCE challenge, resource, scopes and principal, with a 60-second default TTL under a 5-minute ceiling. Redemption consumes the code atomically before anything else, and one redeemer wins under contention | `internal/oauthserver/codegrant.go`, `internal/oauthserver/records.go`, `internal/oauthstore/race_test.go` (`TestConsumeCodeElectsExactlyOneRedeemer`) | 2 |
| Opaque MCP credentials with 256 bits of entropy from `crypto/rand`, persisted and compared only as a SHA-256 lookup value. The stored columns are `code_hash`, `handle_hash`, `secret_hash` and `token_hash` | `internal/oauthserver/secret.go`, `migrations/0001_initial.sql` | 1, 3 |
| Refresh-token rotation on every use. A replay of a still-live consumed token is caught by `RotateRefreshToken`, which revokes the whole family transactionally and commits the revocation even on the error path. A replay of a consumed token that has itself already expired is caught by a read-time pre-check in `refreshGrant` instead, which revokes the family through a separate call after the read, not inside a shared transaction; cleanup retains the consumed row while the family is live and it is within the family's most recent 200 generations, so that pre-check keeps working without unbounded per-family growth | `internal/oauthserver/refreshgrant.go`, `internal/store/sqlite_rotate.go`, `internal/store/sqlite_cleanup.go`, `internal/oauthstore/race_test.go` (`TestRotateRefreshTokenElectsOneWinnerAndKillsTheFamily`) | 2 |
| Consent bound to `(principal, client, exact redirect, resource)` with the consented scopes as the value. A request is admitted only when the requested set is a subset, so scope widening or a redirect change needs fresh consent | `internal/oauthserver/records.go`, `migrations/0002_oauth_contract.sql`, `internal/store/sqlite_consents.go` | 3 |
| The principal is a random internal UUID from `crypto/rand`, never derived from an email or a Garmin id, with the Garmin account linkage stored as a unique keyed hash beside a sealed identity blob | `internal/store/sqlite_principals.go`, `migrations/0001_initial.sql` | 4 |
| The principal comes only from a verified bearer token. A principal already on the request context is deliberately not consulted, and every failure collapses to `ErrNoPrincipal` | `internal/identity/bearer.go`, `internal/cmd/remote_test.go` (`TestRemotePrincipalComesOnlyFromAVerifiedToken`) | 3, 4 |
| The bearer is read from the `Authorization` header and nowhere else: not a query parameter, not a cookie, not a body field. Proven over the real binary | `internal/oauthserver/verify.go`, `e2e/remote_test.go` | 1, 3 |
| Sessions bound to principal, client, resource and scopes, with the session id stored only as a hash and treated as a routing label rather than a credential. A revocation terminates the sessions it covers | `internal/mcpserver/httpsession.go`, `internal/mcpserver/http.go`, `internal/mcpserver/httpsession_test.go` (`TestSessionIsTerminatedByRevocation`) | 4 |
| Protected Resource Metadata and the RFC 6750 challenge, with `realm`, `resource_metadata`, and `invalid_token` / `insufficient_scope`, and a bare challenge when no credential was presented. `bearer_methods_supported` is exactly `["header"]` | `internal/mcpserver/http.go`, `internal/oauthserver/verify.go`, `e2e/remote_test.go` | 2, 8 |
| Origin allowlist with CORS defaulting to deny, forwarded headers trusted only from configured proxy CIDRs, and a cleartext public bind refused without an explicit development override | `internal/mcpserver/httporigin.go`, `internal/mcpserver/http.go` (`validateBind`) | 8 |
| The remote browser profile: a `__Host-` cookie, HSTS, a capability that never appears in a path, a query, a page or a log line, the disclosure page before credential entry, an independent CSRF token that is constant-time compared and rotated, and MFA continuation held server-side | `internal/loginweb/remote.go`, `headers.go`, `remoteflow.go`, `remotesession.go`, `remotehandlers.go`, with `TestTheCapabilityNeverAppearsInAURLOrAPage` and `TestRemoteMFAKeepsTheContinuationServerSide` | 6 |
| Tool policy using explicit operator authority on local stdio and the intersection of operator enablement and granted scope remotely, with tier name lists validated against the registered set in both directions at start-up, an allowlist that can only narrow, and a refusal reason that never names the tool | `internal/policy/policy.go`, `internal/policy/tier.go`, `internal/tools/register.go` (`validateTierLists`), `internal/mcpserver/middleware.go` | 11 |
| Destructive confirmation that fails closed. A client that cannot be asked, a user who declines and a wait that elapses all refuse the call | `internal/policy/confirm.go`, `internal/mcpserver/confirm.go` | 11 |
| Bounded reads. Wire and decompressed response sizes, page size, page start and date windows are all capped, and a caller-chosen page size is narrowed to the configured cap rather than honored | `internal/garmin/client/limits.go`, `internal/garmin/client/models.go`, `internal/tools/args.go` | 7, 12 |
| No caller-supplied server filesystem path exists on the tool surface. `download_activity_file` takes an activity id and a format only and returns a bounded embedded resource, refusing an oversized payload rather than truncating it; `set_fit_download_dir` is not registered at all | `internal/tools/downloads.go` | 7 |
| Structured `slog` logging with an allowlisted field set, on stderr, refusing stdout so stdio frames stay clean | `internal/mcplog/logger.go`, `internal/mcplog/event.go` | 1 |
| Per-principal rate limiting as handler middleware that returns a caller-actionable error result rather than a transport error | `internal/ratelimit/limiter.go`, `internal/ratelimit/middleware.go` | 12 |
| Transactional revocation and unlink cascades that fail closed on partial deletion, proven under contention | `internal/store/sqlite_revoke.go`, `internal/store/sqlite_unlink.go`, `internal/oauthstore/race_test.go` (`TestRevokeConsentIsSafeUnderContention`, `TestRevokePrincipalIsSafeUnderContention`) | 4 |
| The SQLite concurrency contract asserted by **querying** the pragmas on every pooled connection — WAL, foreign keys, busy timeout, synchronous — rather than by inspecting the DSN string | `internal/store/sqlite_db.go`, `internal/store/sqlite_pragma_test.go` | 10 |
| Start-up refusal on bad key material. The composition root opens the key before it serves, and `doctor` branches on `ErrKeyNotFound` and `ErrInsecureKeyPermissions` | `internal/cmd/components.go`, `internal/cmd/remote.go`, `internal/cmd/doctor.go` | 10 |
| Mode isolation inside one process: the stdio and remote shapes share no token gate, token store, policy, limiter, principal resolver or file store | `internal/cmd/remote_test.go` (`TestRemoteAndStdioShareNoState`) | 4 |

Limits on the list above, stated so it cannot be over-read:

- The file store's read-modify-write is serialized across processes.
  `FileStore.Save`, `Delete` and `Reseal` hold an `flock(2)` advisory lock on a
  sibling `.lock` file for the whole critical section, on top of the per-principal
  in-process mutex, which covers goroutines inside one `*FileStore` only.
  `rotate-key` is a separate process from `serve`, and a Go-level
  re-read-then-write is two operations, so the lock is what closes that window.
  The lock is advisory and host-local, so the store must not sit on a network
  filesystem (see `docs/operations.md`). Both deployments are
  single-active-instance by design.
- `mcpserver.Revocation` has no resource selector, so revoking one consent closes
  slightly more sessions than that grant covered. The direction is fail-safe.
- A revocation event dropped under buffer pressure costs the affected session its
  early termination only. The database stays the authority and the token check
  refuses the next request.
- Consent scopes are compared by containment, not held in the consent key. That
  is what makes scope widening need fresh consent while narrowing does not; it is
  a deliberate design, not an approximation.
- Five MCP resources are registered: four workout templates and the structure
  reference. They are compile-time constants, rendered once at registration and
  served with no request and no principal, so they carry no per-principal data
  and no caller input reaches them.

## Assets

The remote rows depend on the SQLite backend, which the stdio deployment does
not open.

| Asset | Sensitivity | Where it lives |
|-------|-------------|----------------|
| Garmin email, password, MFA code | Highest. Never persisted | Transient request memory during one login attempt |
| Garmin DI token set (`di_token`, `di_refresh_token`, `di_client_id`) | Highest | Encrypted at rest in the store; decrypted only in the per-principal client |
| Master encryption key | Highest | Owner-only key file |
| MCP access and refresh tokens | High | Only SHA-256/HMAC lookup values are stored |
| Authorization codes and login transactions | High | Hashed, or in a bounded in-memory registry with a short TTL |
| Consent records and registered clients | Medium | Store |
| Principal identity and Garmin account linkage | High (personal data) | Store, with the Garmin account identifier keyed-HMAC'd or encrypted |
| Garmin health, nutrition, menstrual, location, and device data | Highest (special category) | Passed to the calling principal only; not persisted or shared-cached |
| Audit and application logs | Medium | Pseudonymous IDs and coarse categories only |
| Database file and its backups | Highest in aggregate | Operator-controlled volume |

## Trust boundaries

1. **MCP client to server.** Crossed by Streamable HTTP requests and stdio
   frames. The principal comes only from a verified bearer token in the
   `Authorization` header. Tool arguments are untrusted.
2. **Browser to server.** Crossed by the login, MFA, and consent forms. Requests
   are untrusted and need the transaction cookie plus the form CSRF token.
3. **Server to Garmin.** Crossed by outbound HTTPS. Garmin responses are
   untrusted input.
4. **Server to store and key material.** Crossed by local file and SQLite
   access. Protected by owner-only modes and envelope encryption.
5. **Reverse proxy to server.** Crossed by forwarded headers. Untrusted unless
   the peer is in a configured proxy CIDR.
6. **Between principals.** Crossed by nothing. Isolation is enforced by
   per-principal keying of every client, token, cookie jar, and result.

## Attacker capabilities assumed

- **Remote unauthenticated network attacker**: reaches every public route,
  replays captured requests, forges headers, and enumerates URLs.
- **Malicious or compromised MCP client**: holds a valid token for one
  principal and sends arbitrary tool arguments.
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

The remote unauthenticated attacker and the malicious MCP client are covered by
the OAuth negative matrix, the transport tests and `e2e/remote_test.go`.

Out of scope: full compromise of the running host, a malicious operator, and
compromise of Garmin itself. A key colocated with the database protects backups
and file disclosure, not a compromised running host.

## Threat categories and mitigations

### 1. Credential and token theft, log leakage

Passwords and MFA codes exist only for the duration of one Garmin login attempt,
with references dropped immediately after. They are never persisted, never a
tool argument, never a CLI flag, and never an environment variable. DI tokens are
encrypted with versioned AEAD before storage and never returned to an MCP client.
Only hashed MCP token material is stored. Secret-bearing structs do not print
their fields through `String`, `MarshalJSON`, error, or debug paths, including
the method-stripping alias case.

`internal/mcplog` is structured `slog` with an allowlisted field set on stderr,
and `internal/tools` and `internal/mcpserver` carry their own redaction tests
over the tool result, error and HTTP paths. Bodies are not logged. The exact tool
name is logged only behind `log-tool-names`, off by default.

Known limit: this server states no log retention period. Retention belongs to
the operator's log pipeline.

### 2. Authorization-code, state, PKCE, CSRF, redirect, and refresh-token replay

PKCE S256 is mandatory; implicit and resource-owner-password grants do not
exist. Authorization codes carry 256 bits of entropy, live 60 seconds by default
under a 5-minute ceiling, are single-use, and are bound to client ID, exact
redirect URI, PKCE challenge, resource, scopes, and principal; token exchange
revalidates every binding. The client's `state` is preserved byte for byte and
never reused as the server's CSRF or session state; the server generates an
independent transaction capability, browser cookie, and form CSRF token. Issuer
and audience (RFC 8707 `resource`) always use exact matching, with no wildcard
admitted under any setting. Redirect URI matching is exact, except that a
loopback redirect URI admits any port per RFC 8252 §7.3, and except where the
operator sets `oauth-allow-redirect-wildcards`; with it set, one trailing-path
wildcard per registration is admitted under the parse and normalization rules in
`internal/oauthserver/redirectpattern.go`, and every other wildcard shape — a
host wildcard, a mid-path wildcard, more than one `*` — stays refused.
Fragments, userinfo, and non-HTTPS redirects are rejected except
standards-compliant loopback. Duplicate or conflicting security parameters are
rejected. Refresh tokens rotate on every use, are bound to principal, client,
resource, and family, never expand scope or change resource, and reuse triggers
family revocation. Errors redirect only after the client and exact redirect URI
are validated; otherwise a local sanitized error page is rendered. The negative
OAuth matrix is a required test class.

The wildcard setting exists because a hosted MCP client can carry a
per-installation or per-conversation redirect path that the operator cannot know
in advance, and dynamic client registration is refused. A trailing-path redirect
wildcard is weaker than exact matching, and it is off by default for that
reason. Where it is enabled, any open redirector or any endpoint serving
attacker-influenced content **under the wildcarded prefix** lets an attacker
craft an authorization request whose redirect the pattern admits and receive the
victim's authorization code. PKCE does not mitigate this: the attacker generates
their own challenge. `login-allowed-emails` does not mitigate it either, because
the victim is an allowlisted user logging in with their own credentials. The
mitigations that remain are the operator's choice of prefix and the consent
page, which names the redirect host. The widest prefix the grammar admits,
`https://host/*`, matches every path on that host: scheme, host, and the absence
of userinfo or fragment stay exact, but this is the widest form the feature can
express, and an operator who registers it is trusting every path that host ever
serves.

### 3. Confused deputy and token passthrough

The MCP access token is never forwarded to Garmin, and a Garmin DI token is never
accepted or emitted as an MCP bearer token. The server never authorizes from a
decoded-but-unverified JWT: the unverified-JWT reader is restricted to scheduling
and diagnostics and rejects `alg:none` and unsigned payloads. MCP tokens are
opaque, random, hashed, and server-stored. Consent is bound to
`(principal, client_id, exact redirect_uri, resource)` with scopes compared by
containment, so a client cannot inherit another client's consent, and scope
expansion or a redirect change requires fresh consent.

### 4. Cross-tenant object and handle access

The primary principal key is a random internal UUID. No tool accepts `user_id`,
email, token path, or an account selector. `Mcp-Session-Id` and `Last-Event-ID`
are never authentication: every session is bound to the verified principal,
client, resource, and scopes, with the session id stored only as a hash, and
cross-principal resume, read, or delete attempts are rejected. Each login and
continuation builds its own session and cookie jar. Race-detector tests,
including `TestRemoteAndStdioShareNoState`, prove concurrent principals share no
clients, tokens, cookies, results, or errors.

There is no per-principal Garmin client cache and no download handle: a download
returns a bounded embedded resource in the same response, so there is no handle
to bind or expire.

Known limit: `mcpserver.Revocation` carries no resource selector, so a revocation
closes slightly more sessions than the grant covered.

### 5. Malicious dynamic registration, client metadata, SSRF, and DNS rebinding

Registration is preregistration only: `internal/config/oauthclient.go` takes
operator-written clients with exact redirect URIs and a secret digest supplied
through a file, and there is no RFC 7591 endpoint. No vendor client ID is
hardcoded. Only `garmin.com` and `garmin.cn` parse into a `ValidatedDomain`,
every URL is built from a `Hosts` derived from one, and
`internal/garmin/auth/hostguard.go` refuses a caller-supplied request whose host
is not a validated Garmin host, on the first attempt and on the post-`401`
replay. No user-controlled URL is ever fetched. The one anonymous read outside
the API tier — Garmin's published exercise catalog — uses one compiled-in URL, a
dedicated client with no cookie jar, and no redirect following.

Any future fetcher, and any future dynamic registration, must be a dedicated
component with a scheme allowlist, DNS and IP controls, redirect revalidation,
quotas, metadata size limits, rate limits, audit events, and operator revocation.

### 6. Session fixation, login CSRF, clickjacking, brute force, account enumeration

The login route is transaction-gated: without a valid transaction cookie and a
matching form CSRF value it returns a generic 404 or expired page with no account
disclosure. The transaction capability carries 256 bits, is stored only as a
SHA-256 lookup value, is compared in constant time, is delivered as a
short-lived host-only cookie, and never appears in a path or query. It is bound
to the original authorization request, client ID, exact redirect URI, resource,
and PKCE challenge, with a 5-minute non-extendable TTL, a 5-attempt budget, a
completion lease, and a single-use terminal transition; cross-user,
cross-client, expired, replayed, and out-of-order transitions are rejected.
Remote cookies use a `__Host-` name with `Secure`, `HttpOnly`, `Path=/`, no
`Domain`, and `SameSite=Lax`; the one-shot loopback profile uses a per-run
host-only `HttpOnly` cookie and is tested separately. The form CSRF token is
independent, constant-time compared, and rotated. Responses set a restrictive
CSP with `frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, `Cache-Control: no-store`, and HSTS on public
HTTPS. MFA attempts are limited per transaction. Error text does not
distinguish an unknown account from a wrong password, and an address the login
allowlist refuses gets the same generic message.

Every page's `form-action` is `'self'` only, with one narrow, load-bearing
exception: the response that **renders the consent form** — `GET
/login/consent` — adds the client's redirect URI's origin — scheme, host and
port, never the path or query — to `form-action`. That is the only place the
addition can have any effect: `form-action` is enforced against the policy of
the document that *contains* the form, checked before the form's `POST` is sent
and re-checked on every redirect hop the resulting navigation takes, all
against that same document's policy. The form's action is same-origin, so
`'self'` on the GET response permits the `POST`: it is sent, and the server
processes it and mints the authorization code. Only then does the browser
check the redirect hop to the client's origin against that same GET-response
policy, and — without the addition — blocks it there, which discards a minted
code and consumes the transaction. The `POST /login/consent` response's own
headers are never consulted for this check. The `/authorize` refusal redirect
needs no addition: the browser arrives there by the client's own top-level
navigation, with no form of this server's in that chain.
`TestConsentFormCSPNamesTheRedirectOrigin` and its siblings in
`internal/loginweb/consentcsp_test.go` pin this arrangement.

The added origin is read only from the transaction's already-validated,
registered redirect URI, never from a request-supplied value, so the addition
never widens the policy beyond the one client the browser is already being
sent to. Every other response keeps the unmodified `form-action 'self'`.

**`SameSite=Lax`, not `Strict`, is deliberate**: `Strict` is not sent on the
cross-site top-level navigation that starts the flow, so the flow would break.

### 7. Untrusted Garmin JSON and files, oversized or compressed payloads, path traversal

Garmin responses are untrusted. Reads use tolerant decoding, so unknown fields
cannot fail an otherwise useful response, with bounded wire and decompressed
response sizes, bounded page size, page start and date windows, and bounded
token and segment sizes in the JWT reader and the token document. Store and key
paths are resolved component by component. A remote tool cannot write an
arbitrary server filesystem path: the download path takes no caller-supplied
filename and returns a bounded embedded resource, refusing an oversized payload
rather than truncating it.

Eight fuzz targets cover the untrusted parsers — `internal/tools`
(`FuzzSanitizeUntyped`), `internal/garmin/protocol` (`FuzzClassifyJSONLogin`,
`FuzzClassifyWidgetPages`, `FuzzParseWidgetMFAVars`), `internal/garmin/api`
(`FuzzParseFITActivity`) and `internal/garmin/client` (`FuzzNumberUnmarshalJSON`,
`FuzzTextUnmarshalJSON`, `FuzzParseDate`) — with seeded corpora (one committed
under `testdata/fuzz`) and a
`fuzz-smoke` CI job that discovers every target and fails loudly if it finds none.

### 8. Reverse-proxy host and header spoofing

`internal/config` requires an explicit bind address and public URL, validates the
TLS pair and the proxy-trust CIDRs, and refuses an unprotected non-loopback
listener; `internal/mcpserver` enforces the runtime half. The issuer, callback
and resource URLs come from the configured public URL and never from `Host` or
`X-Forwarded-*`. Forwarded headers are trusted only from the configured proxy
CIDRs. Streamable HTTP requests that carry `Origin` must match the configured
allowlist, while non-browser token requests may omit it. CORS defaults to deny,
and a cleartext public bind is refused unless an explicit development override
is set.

### 9. Concurrent refresh races and stale-token overwrite

Refresh is serialized per principal by collapsing concurrent refreshes over a
`sync.Mutex` and an in-flight map with a done channel. Persistence uses
compare-and-set, so a rotated token cannot be overwritten by a concurrent writer,
and writes are atomic. Refresh happens 15 minutes before expiry. After a `401` the client retries at most
once, only after a successful refresh, and never replays a `POST` or `PATCH`. One
shared `auth.TokenGate` is wired by the composition root and asserted by test,
so a login cannot overwrite a rotated token set. The SQLite backend gives
cross-connection CAS with `ErrVersionConflict`; the file store serializes across
processes with an advisory lock and stays single-active-instance.

Known limit: concurrent linking of the same Garmin account through two browser
flows has no dedicated test.

### 10. Database and file theft, master-key rotation

Garmin tokens and sensitive identity fields use versioned AEAD envelope
encryption with `crypto/rand` nonces and additional data binding the principal
ID, the record type, and the wrapper's schema and CAS version, so a record cannot
be moved between principals or record types. The key is an owner-only file
holding a versioned key ID and a base64 32-byte master key, installed
exclusively by hard link. Start-up refuses missing, malformed, or overly
permissive key material, and `doctor` names the reason. The key is never logged
or printed. Local token files use `0700` directories and `0600` files re-checked
on read, reject symlinks and `~user` paths across the full ancestry, write
atomically, and are tested against a hostile umask in isolated subprocesses.
Tamper, wrong-key, wrong-principal and wrong-record-type tests exist. Inline
token JSON (`garmin-tokens`) is refused in remote mode; on stdio it is accepted
as an explicitly insecure compatibility override. Deleting local tokens is unlinking, not remote
revocation.

Windows is not a supported platform, so there is no ACL requirement here:
`internal/securefile` compiles on unix only, because its purpose is refusing to
hold a secret under permissions it cannot verify.

`garmin-mcp rotate-key` re-seals every sealed record in both backends — the
index root, Garmin identities, Garmin token sets, OAuth client state, and the
FileStore record — reads through the retired key while any record still needs
it, fails closed on an unknown key version, and is resumable because each
record's own envelope is the progress marker. `docs/operations.md` documents
the procedure and its limits: rotation is offline, the retiring key is never
deleted automatically, and a FileStore run can only speak for the principal the
configuration binds.

Backup and restore are **deliberately not tested here**: the database lives on
an operator-controlled volume and backing it up is the operator's
responsibility. `docs/operations.md` documents the procedure, including that the
database and the master key are two halves of one backup and that a restore
rolls consents back to the backup's moment.

### 11. Malicious tool arguments and accidental destructive actions

Every registered tool declares all four annotation hints and a strict JSON schema with ranges, formats, and
defaults. Operator policy and scope are enforced before any Garmin call. Local
stdio higher tiers require explicit operator enablement; remote higher tiers
require its intersection with the caller's granted scope and default to
read-only. The write and destructive tiers come from `writeRegistrations()` and
`destructiveRegistrations()` in `internal/tools/register.go` (exported as
`WriteTools()` and `DestructiveTools()`), and `validateTierLists` checks them
against the registered set in both directions at start-up. Allowlists and denylists reject unknown names at start-up and only narrow
authorization. Destructive operations request MCP elicitation confirmation with
a bounded timeout and **fail closed**: without confirmation the operation is
refused and the refusal names the reason.

The optional `safety-delay`, default `0`, pauses write and destructive calls
after every gate and before the handler. The wait is interruptible, so a caller
that cancels during it stops the call before anything reaches Garmin, and a
refused call never waits.

This is also the control against prompt injection in Garmin-sourced text: no
model-authored argument reaches a destructive path without local operator
authority or remote scope, tier enablement, and human confirmation.

Known limit: the delay sends no progress notification, so a paused call is
indistinguishable from a slow one to the client. That blunts the delay for a
human watching, and is part of why it is off by default.

### 12. Denial of service and Garmin account rate limiting

Rate limiting is per-principal handler middleware that returns a
caller-actionable error result instead of a transport error. MFA attempts are
budgeted per transaction in a bounded registry with an entry cap and TTL.
Garmin rate limiting is classified distinctly in `internal/garmin/protocol`,
is never reported as a bad password, and stops DI ticket exchange early. The
only automatic retry is the single bounded post-`401` retry, which never replays
a `POST` or `PATCH`; password and MFA submissions are never retried. Request
bodies and responses are byte-capped, and expired transactions, codes, and
tokens are cleaned by bounded expiry in the SQLite store and by on-access
checks.

Known limits: there is no global concurrency limit and no per-tool cost
accounting.

## Revocation and unlink

Revocation is transactional and idempotent. Revoking a client consent revokes
that client's token families for the principal and closes its active transport
sessions. Unlinking a Garmin account revokes every MCP token family for the
principal and deletes the encrypted Garmin tokens and pending transactions.
Partial deletion fails closed and emits only a redacted audit event. The
cascades are proven under contention in `internal/oauthstore/race_test.go`. The
one accepted imprecision is that `mcpserver.Revocation` carries no resource
selector, so a consent revocation closes slightly more sessions than the grant
covered.

## Operational exposure

Audit events contain no credentials and no health or location payloads.
`/livez` and `/readyz` (`internal/mcpserver/httpprobe.go`) use constant paths,
return a fixed `ok` / `not ready` body, and run an injected readiness check
bounded by a two-second timeout, so a wedged store answers honestly instead of
hanging. A real MCP route published on either path still wins, so a probe cannot
shadow the server's own surface.

Metrics (`internal/metrics`) are served on their own `http.Server` bound to
`metrics-address` (empty, the default, disables the listener) under both
transports. There is no separate administration listener.

The metrics port is unauthenticated, plain HTTP, and carries labels including
the pseudonymous principal ID and the exact tool name. An attacker who reaches
it learns which principal called which tool, and on this server a tool name can
itself name a medical domain (`get_sleep_data`, `get_blood_pressure`,
`get_menstrual_calendar_data`). The mitigation is the network boundary alone —
the port must stay off any Ingress, HTTPRoute, or LoadBalancer, and both the
listener setting and the chart's `ServiceMonitor`/`PrometheusRule` default off —
with no authentication layered on top. This is a deliberate decision: the
per-tool failure-rate alert is the feature's purpose, and a principal or tool
label with reduced cardinality could not drive it. See `docs/operations.md` for
the exposure rule and the full metric table.

Metric labels never carry raw user IDs, emails, activity IDs, or tool
arguments: neither `ToolEvent.Arguments` nor `ToolEvent.Reason` is ever rendered
as a label, and `TestToolCallNeverRendersArgumentsOrReason` pins it.
