# Threat model

This model must be satisfied before remote authentication is enabled. It covers
assets, trust boundaries, attacker capabilities, and one mitigation set per
threat category.

The operator of a remote deployment occupies a sensitive trust position: users
type their Garmin credentials into a page that the operator serves. Self-hosting
or a trusted operator is the recommended deployment.

## Assets

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

Out of scope: full compromise of the running host, a malicious operator, and
compromise of Garmin itself. A key colocated with the database protects backups
and file disclosure, not a compromised running host.

## Threat categories and mitigations

### 1. Credential and token theft, log leakage

Passwords and MFA codes exist only for the duration of one Garmin login attempt,
in mutable buffers where practical, with references dropped immediately after.
They are never persisted, never a tool argument, never a CLI flag, and never an
environment variable. DI tokens are encrypted with versioned AEAD before storage
and never returned to an MCP client. Only hashed MCP token material is stored.
Secret-bearing structs cannot print their fields through `String`, `MarshalJSON`,
error, or debug paths. Logging is structured `slog` with an allowlisted field
set; authorization and cookie headers, tokens, passwords, MFA codes, client
state, transaction capabilities, email, display names, health and nutrition and
menstrual metrics, GPS coordinates, identity-bearing filenames, and Garmin
payloads are redacted. Bodies are never logged by default. Exact tool names are
logged only under an explicit safe-debugging policy, and retention is short.
Redaction has its own test suite.

### 2. Authorization-code, state, PKCE, CSRF, redirect, and refresh-token replay

PKCE S256 is mandatory; implicit and resource-owner-password grants do not exist.
Authorization codes carry at least 256 bits of entropy, live at most five
minutes, are single-use, and are bound to client ID, exact redirect URI, PKCE
challenge, resource, scopes, and principal; token exchange revalidates every
binding. The client's `state` is preserved byte for byte and is never reused as
the server's CSRF or session state; the server generates an independent
transaction capability, browser cookie, and form CSRF token. Issuer, audience
(RFC 8707 `resource`), and redirect URI use exact matching; fragments, userinfo,
wildcards, and non-HTTPS redirects are rejected except standards-compliant
loopback. Duplicate or conflicting security parameters are rejected. Refresh
tokens rotate on every use, are bound to principal, client, resource, and family,
never expand scope or change resource, and reuse triggers transactional family
revocation. Errors redirect only after the client and exact redirect URI are
validated; otherwise a local sanitized error page is rendered. The negative OAuth
matrix is a required test class.

### 3. Confused deputy and token passthrough

The MCP access token is never forwarded to Garmin, and a Garmin DI token is never
accepted or emitted as an MCP bearer token. The server never authorizes from a
decoded-but-unverified JWT; MCP tokens are opaque, random, hashed, and
server-stored. Consent is bound to
`(principal, client_id, exact redirect_uri, exact scopes, resource)`, so a
dynamically registered client cannot inherit another client's sticky consent.
Scope expansion or a redirect change requires fresh consent.

### 4. Cross-tenant object and handle access

The primary principal key is a random internal UUID. No tool accepts `user_id`,
email, token path, or an account selector. Garmin clients are cached by internal
principal only, with a bounded size and idle lifetime, and each entry owns its
cookies, token view, and refresh lock. `Mcp-Session-Id` and `Last-Event-ID` are
never authentication: every session and event buffer is bound to the verified
principal, client, resource, and scopes, and cross-principal resume, read, or
delete attempts are rejected. Download handles are short-lived and
principal-bound. Race-detector tests prove concurrent principals cannot share
clients, tokens, cookies, results, or errors.

### 5. Malicious dynamic registration, client metadata, SSRF, and DNS rebinding

Pre-registered operator clients are the default, and no vendor client ID is
hardcoded. Unrestricted anonymous production registration is prohibited. If
RFC 7591 registration is enabled, it requires an initial-access token or an
explicit production policy, plus quotas, strict redirect schemes and hosts,
metadata size limits, rate limits, audit events, and operator revocation.
Conformance testing uses a separate constrained profile. If Client ID Metadata
Documents are selected, retrieval is SSRF-safe under an explicit trust policy.
The server never fetches a user-controlled URL for uploads. Any future fetcher
must be a dedicated SSRF-safe component with a scheme allowlist, DNS and IP
controls, redirect revalidation, and egress policy.

### 6. Session fixation, login CSRF, clickjacking, brute force, account enumeration

The login route is transaction-gated: without a valid transaction cookie and a
matching form CSRF value it returns a generic 404 or expired page with no account
disclosure. The transaction capability carries at least 256 bits, is stored only
as a SHA-256/HMAC lookup value, is delivered as a short-lived host-only cookie,
and never appears in a path or query. It is bound to the original authorization
request, browser session, client ID, redirect URI, resource, and PKCE challenge,
has a short absolute TTL, a bounded attempt count, and a single-use terminal
transition; cross-user, cross-client, expired, replayed, and out-of-order
transitions are rejected. Remote cookies use a `__Host-` name with `Secure`,
`HttpOnly`, `Path=/`, no `Domain`, and an appropriate `SameSite`; the one-shot
loopback profile uses a per-run host-only `HttpOnly` cookie and is tested
separately. Responses set a restrictive CSP with `frame-ancestors 'none'`,
`X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
`Cache-Control: no-store`, and HSTS on public HTTPS. Login attempts are limited
per IP and MFA attempts per transaction. Error text does not distinguish an
unknown account from a wrong password.

### 7. Untrusted Garmin JSON and files, oversized or compressed payloads, path traversal

Garmin responses are untrusted. Reads use tolerant decoding, so unknown fields
cannot fail an otherwise useful response, with bounded response sizes and safe
connect, header, and body timeouts. Uploads and downloads cap raw and
decompressed sizes, parse defensively, reject traversal, and sanitize filenames
and content types. Fuzz tests cover Garmin union and positional JSON, date and
range parsing, filenames, FIT and GPX input, OAuth parameters, URLs, and token
JSON. A remote tool can never write an arbitrary server filesystem path;
downloads return a bounded MCP resource or blob, or a short-lived
principal-bound handle.

### 8. Reverse-proxy host and header spoofing

An explicit canonical public URL is required. Issuer, callback, and resource URLs
are never derived from `Host` or `X-Forwarded-*`. Forwarded headers are trusted
only from configured proxy CIDRs. Browser form requests require the expected
Origin and CSRF protections; Streamable HTTP requests that carry `Origin` must
match the configured allowlist, while standards-compliant non-browser token
requests may omit it. CORS defaults to deny. Production remote mode refuses
cleartext public binding unless an explicit development override is set.

### 9. Concurrent refresh races and stale-token overwrite

Refresh is serialized per principal with a per-user mutex or `singleflight`.
Persistence uses optimistic version or CAS semantics, so a rotated token cannot
be overwritten by a concurrent writer, and writes are atomic. Refresh happens
before expiry with a configurable safety window whose default matches the current
15-minute behavior. After a `401` the client retries at most once, only after a
successful refresh, and only for safe or idempotent calls. Concurrent linking of
the same Garmin account through two browser flows has a defined and tested
transactional outcome.

### 10. Database and file theft, master-key rotation

Garmin tokens and sensitive identity fields use versioned AEAD envelope
encryption with `crypto/rand` nonces and authenticated additional data binding
the principal ID and record type, so a record cannot be moved between principals
or record types. The shipped backend is an owner-only file holding a versioned
key ID and a base64 32-byte master key; remote mode refuses to start on missing,
malformed, or overly permissive key material. The key is never logged or printed.
Staged key rotation and a tested migration path are required. Local token files
use `0700` directories and `0600` files, reject symlinks, write atomically, and
are tested against a hostile umask in isolated subprocesses on Unix and against
user-only ACL or key protection on Windows. Inline token JSON is an explicitly
insecure compatibility override and is rejected in remote production mode.
Deleting local tokens is unlinking, not remote revocation, and the documentation
states the difference. Encrypted-store tamper and wrong-key tests are required,
together with backup and restore tests.

### 11. Malicious tool arguments and accidental destructive actions

Every tool has a strict JSON schema with ranges, formats, and defaults, and
declares all four annotation hints. Scope and operator policy are enforced before
any Garmin call. Write and destructive tiers require the intersection of operator
enablement and granted scope; remote deployments default to read-only. Explicit
`writeTools` and `destructiveTools` name lists are validated against the
registered set at startup, so a typo fails fast. Allowlists and denylists reject
unknown names at startup and are intersected with scopes, never used to bypass
them. Destructive operations request MCP elicitation confirmation with a bounded
timeout and **fail closed**: without confirmation the operation is refused and
the refusal names the reason. An optional safety delay with progress
notifications precedes write and destructive execution. This is also the control
against prompt injection in Garmin-sourced text: no model-authored argument can
reach a destructive path without scope, enablement, and human confirmation.

### 12. Denial of service and Garmin account rate limiting

Layered limits apply: global concurrency, per-IP login attempts, per-transaction
MFA attempts, per-client authorization attempts, per-principal Garmin calls,
per-tool cost, body and response byte caps, and timeouts. Rate limiting is
handler middleware that returns a caller-actionable error result instead of a
transport error. Garmin `429` is classified distinctly, is never reported as a
bad password, and honors `Retry-After` with bounded jitter and an account-level
cooldown. Only transport failures and selected `5xx` responses are retried, with
bounded exponential backoff and full jitter; non-idempotent mutations, deletes,
ordinary `4xx` responses, and password or MFA submissions are never retried
automatically. Expired transactions, codes, and tokens are cleaned by a bounded
periodic job and by on-access checks. Graceful shutdown stops accepting requests,
cancels or expires login transactions, finishes bounded in-flight calls, flushes
safe telemetry, and closes stores.

## Revocation and unlink

Revocation is transactional and idempotent. Revoking a client consent revokes
that client's token families for the principal and closes its active transport
sessions. Unlinking a Garmin account revokes every MCP token family for the
principal, deletes the encrypted Garmin tokens and pending transactions, stops
background refresh, and evicts clients and caches. Partial deletion fails closed
and emits only a redacted audit event.

## Operational exposure

`/livez` and `/readyz` expose no secret detail. Metrics are optional and use
bounded-cardinality labels only; raw user IDs, emails, activity IDs, and tool
arguments never appear in labels. Administration and metrics endpoints run on a
separate listener or are explicitly protected. Audit events contain no
credentials and no health or location payloads.
