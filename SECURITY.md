# Security policy

## Reporting a vulnerability

Report privately. **Do not open a public issue, pull request, or discussion for a
security problem.**

Use GitHub private vulnerability reporting: go to the repository's **Security**
tab and choose **Report a vulnerability**. That opens a private advisory visible
only to you and the maintainers.

Include, as far as you can:

- what an attacker gains, and what they need to start;
- the affected version or commit, and the transport (stdio or Streamable HTTP);
- reproduction steps, a minimal configuration, and the observed against the
  expected behavior;
- any log excerpt — with credentials, tokens, and key material removed.

**Never include a real credential, token, or key in a report.** If a report would
only make sense with one, say so and send a synthetic equivalent.

Expect an acknowledgement within a few days and an assessment after that. Fixes
are released before the advisory is published. Please allow a reasonable
disclosure window. There is no bounty.

### In scope

- Authentication and authorization: token verification, audience and scope
  checks, PKCE, redirect URI matching, consent, revocation.
- Cross-account data exposure: any path by which one principal reads, writes, or
  deletes another's data.
- Credential and key handling: leakage of a Garmin password, an MFA code, a
  Garmin token set, an MCP token, a client secret digest, or master key material
  — through a log, an error, a response body, a page, or a file mode.
- Injection, request smuggling, SSRF, and path traversal in this server's own
  code.
- Denial of service that one unauthenticated caller can cause.

### Out of scope

- Vulnerabilities in Garmin Connect itself. Report those to Garmin.
- Rate limiting of Garmin's own service, and WAF behavior.
- The absence of features this project documents as absent — horizontal scaling,
  scheduled cleanup, key rotation, a health endpoint, dynamic client
  registration. See the last section of [docs/operations.md](docs/operations.md).
  A report that one of those gaps has a concrete exploit is in scope; a report
  that it is missing is not.
- Findings that need an already-compromised host. A master key stored beside the
  database protects a stolen backup, not a compromised host, and that is a stated
  design limit rather than a defect.
- Results from automated scanners with no demonstrated impact.

## Supported versions

No release has been tagged yet. Until the first release, the supported version is
the current default branch, and nothing is backported.

Once releases exist, the most recent release receives security fixes. Older
releases do not. Upgrade before reporting a problem you cannot reproduce on the
latest.

Dependency and base-image updates are part of a security fix: the container base
image is pinned by multi-architecture index digest, GitHub Actions are pinned by
commit SHA, and `govulncheck` runs in CI.

## What this server holds, and where

### It never holds

- **Garmin passwords and MFA codes.** They are entered in the login form or at
  the terminal, forwarded to Garmin for one call, and dropped. No configuration
  key, flag, environment variable, or MCP tool argument accepts one.
- **Client secrets.** Only the hex SHA-256 digest of one, read from an owner-only
  file at start-up. The database column for a client secret is written as NULL on
  every reconciliation.
- **Recoverable MCP tokens.** Access and refresh tokens are stored as keyed
  HMACs. A stolen database yields no usable token.
- **Raw session identifiers.** The live session table is keyed by the SHA-256 of
  the session id, so a raw id cannot reach a log, an error, or a panic dump.

### Local stdio deployment

Everything is under the state directory — `--state-dir`, or the platform's
per-user configuration directory plus `garmin-mcp/`:

| Location | Contents | Mode |
|----------|----------|------|
| `keys/key-v<N>.json` | The AES-256 master key, base64 in a small JSON document | `0600` in a `0700` directory |
| `tokens/` | One encrypted record per principal, holding the Garmin DI token set | `0600` in a `0700` directory |

### Remote deployment

The state directory holds the key as above. The SQLite database at
`database-path` — file and its `-wal` and `-shm` sidecars forced to `0600`, in a
`0700` directory — holds:

| Table | Contents |
|-------|----------|
| `schema_meta` | The encryption key version and the sealed index root every lookup key derives from |
| `principals` | An internal random id, a normalized email (display and login handle only), a keyed HMAC of the Garmin account id, and the AEAD-sealed Garmin identity |
| `garmin_token_sets` | The AEAD-sealed Garmin DI token set, one per principal, with a compare-and-set version |
| `oauth_clients`, `oauth_client_redirect_uris` | Client identity, display name, public flag, and the exact redirect URIs |
| `consents` | Which principal granted which client which scopes, for which redirect URI and resource, and when it was revoked |
| `auth_transactions`, `auth_codes` | In-flight authorization state: handle and code as keyed HMACs, the S256 PKCE challenge, and the sealed client state |
| `token_families`, `mcp_tokens` | Token lineage and revocation, with every token as a keyed HMAC |
| `audit_events` | Fixed-vocabulary event records with short reason codes, never free text from a request |

No index and no unique constraint uses an email address. The isolation key is
always the internal principal id.

### In memory, for the life of the process

Pending MFA continuations (a capability, a staging key, and the login handle — no
password, no code, no token; bounded at 256 and expiring in 10 minutes), the live
session table, the immutable client registry with its secret digests, and the
bounded rate-limit table.

### Logs

Structured, with a closed event vocabulary. Secret-bearing values render as
`[redacted]` through every printing, serialization, and logging path, including
`%v`, `%#v`, JSON, and `slog`. Errors do not echo a rejected configuration value,
a submitted credential, a file's content, or a presented token. Logs never go to
standard output, so an MCP frame stream cannot carry one.

### Data sent outward

Only to Garmin, only over the configured region's hosts, with an allowlist
enforced on the request. Redirects are never followed, so an Authorization header
cannot be carried to a host the caller did not choose. There is no telemetry, no
analytics, and no crash reporting.

## Deployment checklist

Work through this before exposing a remote deployment. Each item says whether the
server enforces it or whether it is yours to keep.

### Transport

- [ ] `public-url` is `https` and is the URL clients really reach. *Enforced: a
      cleartext issuer fails at start-up.*
- [ ] The path in `public-url` is the path this process serves, with no proxy
      rewrite in between. *Not enforced.*
- [ ] TLS is terminated either by this process (`tls-cert-file` and
      `tls-key-file`) or by a proxy that is the only thing able to reach the bind
      address. *Enforced: a non-loopback cleartext bind without trusted proxies
      is refused.*
- [ ] `allow-insecure-http` is **not** set.
- [ ] `trusted-proxy-cidrs` names only the networks the proxy terminates on. No
      `0.0.0.0/0`. *Not enforced.*
- [ ] `allowed-origins` is empty unless a browser client genuinely needs the
      endpoint, in which case it lists exact origins. *Enforced: an empty list
      denies every request carrying an `Origin`.*

### Authorization

- [ ] Every registered client is one you put there. There is no dynamic
      registration.
- [ ] Redirect URIs are exact, and every withdrawn URI is gone from
      configuration. *Enforced: reconciliation replaces the list, it does not
      merge.*
- [ ] Clients that cannot keep a secret are `public: true`; confidential clients
      supply `secret-hash-file` pointing at an owner-only file. *Enforced.*
- [ ] Each client's `scopes` is the narrowest set it needs. *Not enforced.*
- [ ] `resources` matches the deployment's resource indicator exactly.
      *Enforced at every authorization request.*

### Keys and data

- [ ] `master-key-file` points at a directory holding an owner-only
      `key-v<N>.json`, on a path with no symlinked component. *Enforced: unsafe
      or malformed key material refuses to start.*
- [ ] No inline `master-key` and no inline `garmin-tokens` value anywhere.
      *Enforced.*
- [ ] The database is on local storage, not a network filesystem. *Not
      enforced.*
- [ ] The database and the key are both backed up, stored separately, and a
      restore has been tested. *Not enforced.*
- [ ] Exactly one process runs against the database file, and the deploy stops
      the old one before starting the new one. *Not enforced — there is no
      lock.*

### Tools

- [ ] `enable-write-tools` and `enable-destructive-tools` stay off unless writes
      are genuinely wanted; destructive requires write. *Enforced.*
- [ ] Clients that only read are granted only read scopes. Enablement and scope
      are both required, and neither alone is enough. *Enforced.*
- [ ] `tool-denylist` names anything that must never be exposed. *Enforced: the
      denylist beats enablement and scope.*

### Operations

- [ ] `garmin-mcp doctor` reports no `unsafe` state.
- [ ] Log level and format are set deliberately, and logs go somewhere with
      access control.
- [ ] Database growth is monitored, because no cleanup is scheduled today.
- [ ] There is a plan for out-of-band revocation, given that no `revoke` and no
      `unlink` command exists yet — and an understanding that revoking here never
      revokes anything at Garmin.
