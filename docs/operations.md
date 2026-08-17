# Operations

This document is for the person who runs `garmin-mcp`, not for the person who
develops it. It covers deploying the remote server, registering OAuth clients,
the database, key management, revocation, lifetimes, and upgrades.

Every setting named here is defined in [configuration.md](configuration.md).

Where this server deliberately does less than an operator might expect, this
document says so and says why. It does not describe a feature that does not
exist.

## 1. Deploying the remote server

The remote shape is `transport: streamable-http`. It serves many accounts, each
one linked through its own browser login and addressed by its own OAuth
authorization.

### The canonical public URL

`public-url` is the one URL the deployment publishes. Everything else is derived
from it: the OAuth issuer, the RFC 8707 resource indicator that every access
token is minted for, the authorization, token, revocation, and metadata URLs, and
the path the MCP endpoint is served on.

It is never taken from a request. Not from `Host`, not from `X-Forwarded-Host`,
not from anything else a caller controls. A metadata document or a token audience
built from an attacker-supplied host is token confusion, not a convenience: a
caller who can steer the issuer can make this server mint a token for an audience
it does not own, or point a client at an authorization server it does not own.
`HTTPTransport.PublicURL()` takes no request argument, which is how that rule
stays structural rather than advisory.

Consequences an operator must plan for:

- The path component of `public-url` **is** the MCP endpoint path.
  `https://host/mcp` serves MCP at `/mcp`. A URL with no path serves it at `/`.
- The path may not be one this deployment already serves:
  `/.well-known/oauth-protected-resource`,
  `/.well-known/oauth-authorization-server`, `/token`, `/revoke`, `/authorize`,
  or `/login`. Start-up refuses it.
- A proxy that rewrites the path breaks discovery. The path a client is told to
  use is the path this process routes on. Map the external path to the same
  internal path, or make them equal.
- Changing `public-url` changes the issuer and the audience. Existing access and
  refresh tokens are minted for the old resource and stop verifying. Treat a
  public URL change as a re-authorization event for every client.

### The issuer must be https

The authorization server refuses to name a cleartext issuer. `oauthserver.New`
rejects any issuer that is not `https`, and the composition root refuses a
cleartext public URL before that.

This means a plain-http public URL **passes configuration validation and then
fails at start-up**. `allow-insecure-http` moves the configuration-layer check
out of the way; it does not make the authorization server accept a cleartext
issuer. There is no development mode in which the remote transport publishes an
`http` origin. For a local experiment, generate a certificate and use
`https://127.0.0.1:<port>/mcp`.

### TLS

Two supported shapes:

1. **This process terminates TLS.** Set `tls-cert-file` and `tls-key-file`. Both
   are required together; one alone is refused, because starting anyway would
   silently serve cleartext. The floor is TLS 1.2. A certificate or key that
   cannot be loaded is a start-up failure, and the cause is deliberately not
   printed — a PEM parse error can quote a private key.
2. **A trusted proxy terminates TLS.** Leave the TLS settings unset and list the
   proxy networks in `trusted-proxy-cidrs`. The process then serves cleartext on
   its bind address, which must not be reachable from anywhere but the proxy.

Server-side bounds are fixed, not configurable: a 15-second request-header
timeout (a zero value here is the slowloris hole), a 2-minute idle timeout for a
kept-alive connection carrying no request, and a 20-second graceful shutdown.

### Refusing to bind cleartext in public

A non-loopback bind address is refused unless at least one of the following is
true: a TLS certificate is configured, one or more trusted proxy networks are
configured, or `allow-insecure-http` is set. `0.0.0.0` and `[::]` are not
loopback.

The override exists for development. It is not a production setting, and it buys
nothing in production anyway, because the https issuer rule still applies.

### Trusted proxy networks

`trusted-proxy-cidrs` lists the networks whose `X-Forwarded-For` header may be
read. The empty default trusts nobody. When the immediate peer is inside a listed
network, the client-most forwarded entry is used; otherwise the peer address is
used.

Nothing security-relevant is derived from a forwarded header. The forwarded
address is a log label. Authorization comes from the bearer token, and the
deployment's identity comes from `public-url`.

Setting `trusted-proxy-cidrs` also satisfies the cleartext bind check, so list
only the networks a proxy actually terminates on. A wide entry such as
`0.0.0.0/0` disables that check and makes every log line's client address
attacker-controlled.

### The origin allowlist

`allowed-origins` is the browser `Origin` allowlist for the MCP endpoint.

- A request with **no** `Origin` header is permitted. A standards-compliant
  non-browser MCP client sends none.
- A request **with** an `Origin` is permitted only when that exact origin is in
  the list. The empty default therefore denies every browser request. That is
  CORS denied by default, which is the correct posture for a server no web page
  should be calling.
- Entries must be bare origins. A path, query, fragment, or userinfo can never
  match a browser's `Origin` header, so such an entry is refused at start-up
  rather than silently matching nothing.
- There is no wildcard and no suffix matching. `*` and `null` are refused.
- A CORS preflight is answered before authentication and creates no session.

For a loopback deployment the SDK additionally refuses a request whose `Host`
header is not loopback while the listener is, which is the standard DNS-rebinding
protection. There is no `Host` allowlist for a non-loopback deployment; the
origin allowlist and the fixed public URL are what stand in for one.

### Bringing it up

```sh
garmin-mcp doctor            # effective configuration, key, store, database
garmin-mcp serve             # binds, migrates, reconciles clients, serves
```

`doctor` creates nothing. The first `serve` run creates the state directory, the
key file, and the database.

## 2. Registering OAuth clients

There is no dynamic client registration. A client exists because an operator
wrote it into `oauth-clients`. No vendor client is ever defaulted.

### From configuration to a database row

A registration has two halves in two places, on purpose:

- the **database** holds the identity and the exact redirect URIs, which an
  authorization transaction references by foreign key;
- **configuration** holds the OAuth policy — the scope bound, the resource
  indicators, and the secret digest — for which the database has no column.

Configuration is the source of truth for both. On every start-up, reconciliation
writes the database half for each configured client and then reads it back with
the same lookup the authorization endpoint uses. A client that reconciles but is
not readable fails at start-up rather than at the first user's login.

The operator's **client id is the key**. It is what reconciliation matches on,
what a client presents at the authorization and token endpoints, and what a
consent record is filed under. Changing an id creates a second client and orphans
every consent filed under the first.

### A withdrawn redirect URI disappears

Redirect URIs are replaced, never merged. Reconciliation deletes the client's
existing rows and re-inserts the configured list in one transaction. Removing a
URI from configuration removes it from the database at the next start. A merge
would let a URI an operator withdrew survive, which is the exact failure a
redirect allowlist exists to prevent.

Matching at authorization time is byte-exact. There is no normalization, no
prefix rule, and no wildcard.

### A disabled client is not silently re-enabled

A client row carries a `disabled_at` column. If it is set, reconciliation refuses
and start-up fails, naming the client. It does not clear the flag.

The alternative would be a restart quietly undoing an operator's decision. To
re-enable a client, clear `disabled_at` deliberately in the database; to retire
one, remove it from configuration as well, or start-up will keep failing.

Removing a client from configuration does **not** delete its database row. The
row and its consents remain for audit, and the client becomes unknown to the
registry, so it can no longer authorize or authenticate.

### A confidential client's secret digest

This deployment never stores a client secret. It stores the hex SHA-256 digest of
one, and it reads that digest from a file:

```yaml
oauth-clients:
  - id: example-service-client
    name: Example Service Client
    redirect-uris:
      - https://client.example.invalid/oauth/callback
    scopes:
      - garmin:read
    resources:
      - https://mcp.example.invalid/mcp
    secret-hash-file: /etc/garmin-mcp/clients/example-service-client.sha256
```

The file holds one line of hex, at most 128 bytes are read, and it must be
owner-only. It is read once, at start-up, through the hardened file layer, so an
unreadable or group-readable digest is a start-up failure and never a mid-flight
token endpoint failure. The digest is never logged and never echoed into an
error.

The inline `secret-hash` field is refused in remote mode, and the registry
applies to remote mode only, so `secret-hash-file` is the only usable form.

Produce a digest from a secret you hold elsewhere:

```sh
printf '%s' "$CLIENT_SECRET" | shasum -a 256 | cut -d' ' -f1 \
  > /etc/garmin-mcp/clients/example-service-client.sha256
chmod 600 /etc/garmin-mcp/clients/example-service-client.sha256
```

### Scopes

A client's `scopes` is the widest set it may ever be granted, and the deployment
advertises the union of every registered client's scopes. Two names are
meaningful to the tool policy: `garmin:write` gates the write tier, and
`garmin:destructive` gates the destructive tier. Neither implies the other. The
read tier is not scope-gated, so a read-only client still needs at least one
scope but the name is the operator's own; `garmin:read` is the convention this
repository uses.

Grant `garmin:write` and `garmin:destructive` only to a client that must have
them, and remember that the operator flags `enable-write-tools` and
`enable-destructive-tools` must be set as well. Neither half alone opens a tier.

### Public and confidential clients

Prefer a **public** client where the client cannot keep a secret — a desktop or
CLI MCP client. `public: true` selects token endpoint authentication method
`none`, which is safe here only because PKCE S256 is mandatory on every
authorization request. A public client must register no digest.

## 3. The database

### Where it lives

`database-path` names a SQLite file. Its parent directory is forced to `0700`,
and the database file together with its `-wal` and `-shm` sidecars is forced to
`0600` after creation, because the driver's create mode is subject to the process
umask.

The path may not traverse a symlink at any level. On macOS this means a path
under `/var` is refused, because `/var` is a symlink to `/private/var`; use
`/private/var`.

A **network filesystem is not supported**. SQLite's locking is unreliable there,
which invalidates every atomicity guarantee the revocation and unlink cascades
depend on.

Connection settings are fixed: WAL journal mode, foreign keys on, `synchronous`
NORMAL, a 5-second busy timeout, immediate transactions, and a pool of 4
connections.

### Migrations

Migrations are embedded in the binary and **forward-only**. There are no down
migrations, on purpose: a down migration that drops a column silently destroys
token material. Rolling a deployment back means restoring a backup.

Each migration runs in its own transaction together with its bookkeeping row in
`schema_migrations`, so it applies completely or not at all. Each file's SHA-256
is recorded; an applied migration that was edited afterwards is refused, and a
database migrated by a newer build is refused rather than downgraded.

**How to run them:** they run automatically, at every `serve` start-up, before
anything is served. Migration is idempotent, so this costs nothing on an
already-current database.

There is no separate migration step today. `garmin-mcp migrate` exists in the
command tree but is a stub: it validates configuration and then reports that the
subsystem is not implemented. An operator who wants to migrate without serving
has no supported way to do it. Plan the schema change as part of the deploy.

### Backup and restore

The database and the master key are two halves of one backup. A database without
its key is unreadable; a key without its database is useless. Back up both, and
store them separately — see [key management](#4-key-management).

Back up with the process stopped, or with SQLite's own online backup. If you copy
raw files, take the `-wal` and `-shm` sidecars with the main file. Never copy a
live database with `cp` alone.

```sh
# offline, process stopped
sqlite3 /data/garmin.db ".backup '/backup/garmin-2026-08-15.db'"

# restore
systemctl stop garmin-mcp
cp /backup/garmin-2026-08-15.db /data/garmin.db
chmod 600 /data/garmin.db
rm -f /data/garmin.db-wal /data/garmin.db-shm
systemctl start garmin-mcp
```

Restore the master key of the same generation. A restored database opens only
under the key whose version sealed its records; the wrong key fails the open with
a corrupt-record error rather than serving garbage.

A restore rolls tokens and consents back to the backup's moment. A consent
revoked after the backup is live again after the restore. Re-apply any revocation
that happened in that window.

### One active instance

This is a single-active-instance design. It does not scale horizontally.

- **Two processes on one database file is not supported.** Each individual
  transaction would still be atomic, so the compare-and-set writes and the
  cascades would stay correct, but the busy timeout is the only backpressure, and
  a second writer turns every contended write into a timeout. Nothing elects a
  leader, and nothing enforces the constraint: there is no lock file, no advisory
  lock, and no single-connection pool. It is a deployment rule the operator has
  to keep.
- **Two processes migrating one file at the same time is outside the design.**
  Migration is safe against concurrent goroutines in one process; it is not a
  distributed lock.
- Deploy so the old process stops before the new one starts. In Kubernetes that
  is one replica with the `Recreate` strategy, not `RollingUpdate`.

What horizontal scaling would need, and what does not exist: a shared database
with real multi-writer semantics, leader election or a distributed lock for
migrations, cleanup coordination so instances do not each pay a full scan, and
cross-instance invalidation for the in-memory client registry and the live
session table — a revocation is delivered in-process today, so a second instance
would not close the streams the first one revoked.

## 4. Key management

### The master key

Encryption key material lives in `<master-key-file directory>/key-v<N>.json`.
Note that the `master-key-file` setting selects the **directory**; the file name
is owned by `internal/cryptostore`. The file is a small JSON document holding a
version and a base64 32-byte key, and it is created on the first `serve` or
`auth` run if it is absent.

The file is created `0600` inside a `0700` directory, and both modes are applied
by an explicit `chmod` after creation, so the process umask cannot widen them.
That directory must sit on a filesystem that supports hard links: creating a key
file installs it by hard link so two processes racing to create the same version
agree on one winner, and there is deliberately no rename fallback. A filesystem
without hard link support (some network filesystems, some container overlay
configurations) makes key creation fail loudly rather than silently falling back
to a less safe install. If `serve`, `auth`, or `rotate-key` refuses with an
installation error naming the key directory, move the directory to a filesystem
that supports hard links; do not work around it by disabling the check.

Which key version is **active** — the one every new write is sealed under — is
recorded in a small marker file next to the key files,
`<master-key-file directory>/active-key-version.json`. This is key *selection*
metadata only, never a record of rotation progress: it answers "which key file
does a write use today" and nothing else depends on it. A deployment that has
never rotated has no marker at all and resolves to version 1, which is what
every deployment before this file existed already used.

### What refuses to start

Key material is read through a hardened file layer. Each of these is a start-up
failure, not a warning:

- the file, or any directory component of its path, is a symlink;
- the file is group- or world-readable or writable (any bit in `0077`);
- the file is not a regular file;
- the document is not valid JSON, the version is not a positive integer, the
  version in the file does not match the version requested, the key is not
  standard base64, or the key is not exactly 32 bytes;
- the file is larger than 4 KiB;
- inline key material was configured, in either transport.

An existing key file is **never** replaced, not even when it is malformed or
unsafely permissioned. Overwriting it would make every stored record unreadable.
Fixing it is an operator action.

`garmin-mcp doctor` reports the key as `ok`, `absent`, or `unsafe`, and creates
nothing.

### Rotation

Rotation is a **staged, offline** procedure driven by `garmin-mcp rotate-key`.
Offline means exactly that: it is a one-shot command, not a background service,
and it is not meant to run alongside a live `serve` process. There is no
zero-downtime online rotation, no distributed locking, and no automatic or
scheduled rotation — running the command is always a deliberate operator action.

```sh
garmin-mcp rotate-key --state-dir=/var/lib/garmin-mcp --database-path=/var/lib/garmin-mcp/state.db \
    --target-version=2
```

`--target-version` is required and is never inferred: it must be exactly the
active version plus one to start a new rotation, **or the active version
itself to resume one a previous run already started and activated** — see
"Interrupting and resuming" below. Omit `--database-path` for a local stdio
deployment, where the command re-seals the single bound principal's
`FileStore` record instead of a database.

What one run does, in order:

1. **Reads the active version** from the marker described above (defaulting to 1
   if it has never rotated). A fresh rotation refuses unless `--target-version`
   is exactly one higher; resuming one already in progress accepts the target
   equal to the active version instead of refusing it as a skipped version.
2. **Resolves the backend and, for a local `FileStore` deployment, the bound
   principal**, before anything below is durable — a deployment that binds no
   principal is refused here, not partway through.
3. **Loads the retiring key** (the version one below the target — it must
   already exist) **and creates the target key** if it is not already present.
4. **Activates the target version** by writing the marker, unless this run is a
   resume, in which case the marker already names the target and is left
   alone. This is the moment the mixed-version window opens: from here on every
   new write from THIS process is sealed under the target key, and reading a
   record still at the retiring version depends on that retiring key staying in
   place. **A `serve` process already running when this happens does not pick
   this up.** It read the key ring once at its own start-up and keeps sealing
   under whatever key was active then for its entire lifetime — rotation is
   offline precisely because nothing here reaches into a running process and
   makes it reload.
5. **Re-seals every sealed record** onto the target key: the database index root,
   every principal's Garmin identity linkage, every Garmin token set, and every
   pending authorization transaction's client state (SQLite); or the one bound
   principal's token record (local `FileStore`). Each is re-encrypted with the
   same plaintext and the same binding. SQLite additionally checks the row's own
   compare-and-set counter in the same `UPDATE` statement that rewrites it, which
   is atomic at the database engine; the local `FileStore` backend has no such
   engine to lean on, so it closes the same gap with an OS-level advisory lock
   held for the entire read-modify-write section, plus a content-equality
   re-check immediately before the write. Either way, a concurrent write from a
   live server process can never be silently clobbered by the reseal, or vice
   versa.
6. **Reports what it rewrote**, then — for SQLite — reads back a completion scan
   and reports whether every sealed record is now at the target version. That
   scan is a snapshot of the instant it ran: it does not re-check the marker or
   detect a server that starts serving, or commits a write, immediately
   afterward. A clean report means **no record needed the retiring key as of
   this scan** — confirm no server was running throughout the run before
   treating that as a green light to retire the key, and re-run the command
   afterward to be sure. The local `FileStore` backend has no completion scan at
   all: it re-seals exactly the one record this configuration binds and reports
   that, but it cannot enumerate — let alone reseal — a record for any other
   principal, because the record file name is a one-way SHA-256 digest of the
   principal id. A record for a principal this configuration does not bind is
   simply not covered by any `rotate-key` run and needs its own configuration to
   reach it.

**The retiring key must stay on disk until the command reports every record
resealed.** Deleting it earlier makes any record it still holds unrecoverable.
Retiring it — removing `key-v<N>.json` for the old version — is a separate,
manual step this command never takes for you.

Running `rotate-key` again with the same `--target-version` **after** the
retiring key has already been removed is safe: if every record was genuinely
resealed, the command verifies that against the target key alone and reports
the rotation already complete, rather than treating the missing retiring key
as an error. It still refuses, naming the missing key, if anything is actually
left sealed under it — that combination means the key was deleted too early,
and whatever it still held is unrecoverable.

**Interrupting and resuming.** There is no progress checkpoint by design: each
record's own key-version column (SQLite) or the record itself (`FileStore`) is
the resume mechanism. A killed run is simply invoked again with the same
`--target-version`; it re-scans for what is still at the retiring version and
does not re-seal — does not double-encrypt — anything already moved. If a run
reports it did not finish (some records still needed the retiring key), run the
same command again; it resumes correctly.

The honest fallback if this all sounds like too much for a suspected key
compromise remains available: stand up a new deployment with a new key and have
every user link their account again. `rotate-key` is for the ordinary case —
routine rotation, not an incident response shortcut.

### What a key beside the database actually protects

A key file stored next to the database protects a **stolen backup** and stray
file disclosure. It does not protect against a compromised host: a process that
can read the database can read the key that opens it.

Say this plainly to whoever accepts the risk. If the threat you care about is
host compromise, this design does not address it, and no configuration of it
does. There is no OS keyring backend: the keyring code is a placeholder that
reports "unsupported" on every platform, and an unsupported keyring means "use
the key file", never "start without encryption".

## 5. Revocation and unlinking

These are three different actions with three different blast radii. Do not
confuse them.

### Revoking a consent

Revoking one consent — one principal, one client, one exact redirect URI, one
resource — does the following in a single transaction:

- marks the consent row revoked (the row is kept, so the audit trail survives a
  later re-grant);
- revokes every token family for that principal, client, and resource, and every
  access and refresh token in them;
- deletes the principal's pending authorization transactions and authorization
  codes for the same grant.

It then re-counts what should be gone and rolls back with an incomplete-unlink
error rather than reporting a success it did not achieve. Grants that differ in
redirect URI or resource are untouched.

**Revocation closes open streams.** After the transaction commits, the store
publishes a revocation event that the HTTP transport consumes; the transport
closes every live session the event matches, rather than only refusing the next
request. Without that, a held-open event stream would keep delivering to a caller
whose authorization is already gone. The event is keyed on principal and client
rather than on resource, so it deliberately closes more sessions than the grant
covered — the safe direction.

The event bus is bounded at 256 undelivered events and never blocks the
revocation. If it overflows, the affected session loses its early termination,
not its revocation: the database is authoritative, and that session's next
request fails token verification.

### What an operator can actually do today

- **The `/revoke` endpoint** (RFC 7009) is the wired path. A client posts a token
  there; the whole token family behind it is revoked. Unknown, already-dead, and
  other clients' tokens are answered `200` without disclosure.
- **The consent page** lets a user deny an authorization before it completes,
  which persists nothing.
- **There is no `garmin-mcp revoke` and no `garmin-mcp unlink` command.** The
  store implements consent revocation, principal-wide revocation, and Garmin
  unlinking, and the authorization server exposes revoke-consent and
  revoke-principal operations, but nothing in the command tree calls them. An
  operator who must revoke out of band has only direct database access today,
  which does not publish a revocation event and therefore does not close live
  sessions — restart the process after such a change.

### Unlinking a Garmin account

Unlinking is the widest cascade in the store. In one transaction it revokes every
token family the principal holds with every client, deletes the encrypted Garmin
DI token record, deletes every pending authorization transaction and code for the
principal, and clears the Garmin account hash and the sealed identity. It
re-counts all six and rolls back if anything survived, so a partial unlink cannot
commit. It is idempotent.

**Deleting local tokens does not revoke anything at Garmin.** The Garmin DI
refresh token stays valid at Garmin's own service until Garmin expires or revokes
it, and any copy taken earlier keeps working. Report this as "local tokens
removed", never as "tokens revoked". A user who wants their Garmin session ended
must do that in Garmin Connect.

A local stdio deployment has no unlink command either. The equivalent is deleting
the principal's record under `<state-dir>/tokens/`, with the same caveat: it
removes the local copy and revokes nothing at Garmin.

## 6. Lifetimes, cleanup, and rate limits

### Token and session lifetimes

These are the authorization server's defaults. The remote composition root sets
no overrides, so a deployment always runs them; they are not operator-tunable
today.

| Item | Lifetime |
|------|----------|
| Authorization code | 60s (ceiling 5m) |
| Access token | 15m |
| Refresh token | 30 days, renewed in full at each rotation |
| Authorization transaction (the browser login window) | 10m, absolute, never extended |
| Browser login session | the earlier of 10m and the transaction's own expiry |
| Pending MFA continuation | 10m, absolute, never extended, at most 256 concurrent |
| Idle Streamable HTTP session | `session-timeout`, default 30m |
| Consent record | no expiry; it lives until revoked |

Refresh tokens rotate on every use. Presenting a refresh token that was already
consumed counts as reuse and revokes the whole family inside the detecting
transaction; the client sees `invalid_grant`. A refresh can never widen scope,
change resource, or cross clients.

A session id is a routing label, never a credential. A session is bound to the
authorization that created it, and a request presenting another authorization's
session id is refused. The binding deliberately excludes the token family, so a
session survives a refresh rotation.

Store-side upper bounds, which reject a caller-supplied lifetime rather than
setting one: 90 days for a token, 30 minutes for an authorization transaction or
code.

### The cleanup job

Cleanup removes, in one transaction and bounded per call: expired authorization
transactions, expired authorization codes, expired MCP tokens, and token families
with no tokens left. A revoked token row is kept for 24 hours past its expiry so
a revocation can still be investigated. Consents are never removed by cleanup.
The default bound is 500 rows per table per call, the maximum is 5000, and the
returned counts say whether another pass is needed.

**Nothing schedules it.** There is no ticker, no janitor goroutine, and no CLI
command; the method has no caller outside tests. An operator today gets no
cleanup at all, and the database grows with expired transactions, codes, and
tokens.

That is a housekeeping gap, not a security one. Every read applies its own expiry
predicate, so an expired transaction, code, or token is refused on a database
that has never been cleaned. Monitor the database file size, and budget for its
growth until a scheduler ships.

### Rate limits

`read-rate-limit` (default 120/min) and `write-rate-limit` (default 30/min) are
**per principal**, not per IP and not global — a global budget would let one
caller deny service to everyone else. The burst allowances are 20 and 5, and they
follow the rate down if you lower it. Write and destructive calls share the write
bucket, so destructive work cannot get a fresh allowance after writes ran out.

The limiter gates MCP `tools/call` only, and it runs before the policy gate, so a
caller probing tools it may not use is still throttled. A request with no
resolved principal is refused, not pooled. The principal table is bounded at 1024
entries with LRU eviction; an evicted principal starts with a fresh budget.

Exhaustion is reported as a tool result marked as an error, with a retry hint —
not as an HTTP 429 and not as a JSON-RPC transport error, because a transport
error is invisible to the model that has to react to it.

**The OAuth endpoints have no rate limit.** `/token`, `/revoke`, and the metadata
documents are mounted without limiting middleware. What they do have is 8 KiB
body caps, strict structural bounds, and duplicate-parameter refusal. The browser
login flow has its own budget of 5 attempts per transaction and at most 256
concurrent sessions. To bound credential-stuffing traffic against `/token` or
`/login`, do it at the proxy.

## 7. Upgrades

### Before

- Read the release notes for schema changes. Migrations are forward-only; a
  rollback is a restore.
- Back up the database **and** the key file, and verify the backup opens.
- Run `garmin-mcp doctor` with the new binary and the existing configuration. It
  creates nothing, and it reports a rejected setting, unsafe key material, or an
  unsafe database mode before you restart anything.
- Check that no configured OAuth client was disabled in the database. Start-up
  refuses to re-enable one, so the upgrade would fail on it.
- Confirm the deploy stops the old process before starting the new one. Two
  instances on one database file is unsupported, and an upgrade is where that
  usually happens by accident.

### After

- Confirm the process is serving and that the schema version advanced as
  expected.
- Fetch the two metadata documents and confirm the issuer and the resource still
  match `public-url`.
- Complete one authorization end to end with a real client. A changed public URL
  invalidates existing tokens.
- Watch for `invalid_token` responses, which mean the audience moved.

### The tool contract is pinned

`compat/tools.json` is the pinned manifest of the upstream tool surface: 138
tools extracted statically at a pinned upstream commit, each with its
implementation status. Contract tests check the registered tools against it, so a
renamed tool, a changed schema, or a dropped argument fails the build rather than
surfacing as a client breakage after a release.

When you upgrade, the tool names and schemas a client depends on either match
that manifest or the build did not ship. See `docs/parity.md` for what each
release covers.

## 8. What this deployment does not do

Stated once, so an operator does not go looking:

- No horizontal scaling, and no coordination that would allow it.
- No scheduled cleanup.
- No `migrate`, `revoke`, `unlink`, or `tools list` command.
- No key rotation path, despite the underlying support for a staged one.
- No health or readiness endpoint. The MCP transport answers `404` for every path
  it does not own, so a probe must target a path this server serves, or an
  operator must front it with something that provides one.
- No dynamic OAuth client registration.
- No OS keyring integration.
- Write and destructive tools are refused over stdio regardless of
  `enable-write-tools`, because the local deployment supplies no scope source and
  those tiers require a granted scope as well as operator enablement. Remotely
  they need both the operator flag and the granted scope.
