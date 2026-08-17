# ADR 0004 — Token format and storage backend

## Status

**Implemented.** Both halves have landed. `internal/store` persists encrypted
Garmin DI token sets through `FileStore` for the stdio deployment, and the
migration-backed SQLite backend carries the whole multi-user record set. What
remains open is operational rather than structural: a store-level key-rotation
driver. Backup and restore are the operator's responsibility and are documented
rather than tested. See the two lists in the Decision below.

## Context

Two token families must be persisted and must never mix:

- the Garmin DI token set (`di_token`, `di_refresh_token`, `di_client_id`), held
  only by the per-principal Garmin client;
- MCP authorization codes, access tokens, and refresh tokens issued by this
  service.

Constraints already fixed by the brief: MCP token material is opaque, at least
256 bits of entropy, and only its SHA-256/HMAC lookup value is stored; refresh
tokens rotate on every use with family revocation on reuse; authorization from a
decoded-but-unverified JWT is prohibited; Garmin rotated refresh tokens must
survive concurrent writers; release binaries stay `CGO_ENABLED=0`, so a SQLite
driver must be pure Go.

The v1 SQLite deployment is single-active-instance. Horizontal scaling and HA are
not claimed.

## Decision

Define a storage interface in `internal/store` and ship a migration-backed SQLite
implementation with a maintained pure-Go driver. Design the interface and
migrations so a later PostgreSQL implementation is possible, without expanding v1
to add one.

### Landed

- The storage interface and the `FileStore` implementation in `internal/store`,
  which is the only backend today.
- The DI token record layout: an encrypted record per principal with a schema
  version and a per-principal CAS version, written atomically and stored `0600`
  in a `0700` directory. `TestRecordOnDiskHoldsNoPlaintextToken` proves the
  principal is absent from the file bytes as well as the tokens.
- Optimistic CAS on every rotating-token write, in process. `FileStore.Save`
  yields to a newer stored token set on conflict.
- The local `garmin_tokens.json` 0.3.x import and export contract: structure-based
  detection, `0700`/`0600` modes, `~user` and symlink rejection across the full
  ancestry, and atomic writes. There is no Windows side: the platform is not
  supported, and `internal/securefile` compiles on unix only rather than shipping
  a weaker owner-only guarantee where it cannot enforce one.
- Inline token JSON is refused unless explicitly enabled.
- The record **schema version moved from 1 to 2** when the wrapper's schema and
  version were bound into the AEAD as additional data. A schema-1 record now
  reports corruption instead of decoding, because its additional data no longer
  matches. **No migration exists and none is needed: nothing has shipped, so no
  schema-1 record exists outside a discarded working tree.** The next bump after
  a release carries schema 2 does need one.

- The SQLite backend, on `modernc.org/sqlite` `v1.56.0`, BSD-3-Clause, pure Go so
  `CGO_ENABLED=0` cross-compilation keeps working. Version, license and
  maintenance notes are in `docs/dependencies.md`, and `modernc.org/libc` moves
  only together with it.
- The migrations themselves: `migrations/0001_initial.sql` and
  `migrations/0002_oauth_contract.sql`, embedded, monotonically numbered, applied
  by a migrator that records a SHA-256 checksum per file in `schema_migrations`.
- The whole persisted record set: principals keyed by a random internal UUID with
  a unique keyed hash of the Garmin account and a sealed identity blob;
  registered OAuth clients and their exact redirect URIs; per-principal client
  consents on the full tuple; hashed authorization transactions and codes; hashed
  MCP token material with family, generation, expiry, scopes, audience and
  revocation state; the encryption-key version column; and audit events with no
  credentials and no health or location payloads.
- The concurrency contract: WAL, foreign keys, busy timeout, bounded connections
  and transactions, **asserted by querying the pragmas** on every pooled
  connection rather than by inspecting the DSN string. Asserting on the DSN would
  only prove that the string was built, not that SQLite accepted it.
- Real compare-and-set on the SQLite side, through
  `UPDATE ... WHERE principal_id = ? AND version = ?` returning
  `ErrVersionConflict`.
- Start-up refusal on bad key material. The composition root opens the key before
  it serves and `doctor` branches on the `internal/cryptostore` sentinels; see
  ADR 0005.
- A caller for `ParseInlineTokenJSON`, in `internal/cmd/components.go`.
- **Down migrations are not supported, and that is now decided rather than open.**
  A down migration that drops a column silently destroys token material.

### Still open

- Forward-migration atomicity has no dedicated test. Backup and restore are
  **out of scope by decision**: the database sits on an operator-controlled
  volume, backing it up is the operator's job, and `docs/operations.md` carries
  the procedure and the warning that the database and the key are two halves of
  one backup.
- A store-level key-rotation driver. Nothing re-seals existing records; see
  ADR 0005.
- Cross-process compare-and-set is **not** provided by `FileStore`, which holds a
  per-process mutex and no file lock, so the file store stays safe for a single
  active instance only. That is the design, not a defect, and the SQLite backend
  is the answer for anything else. The v1 SQLite deployment is itself
  single-active-instance.

Pending browser and Garmin cookie jars plus MFA transaction state stay in a
bounded in-memory registry for v1. A restart loses them safely and requires a new
login. Passwords and MFA codes never enter that registry.

## Consequences

- Restart is safe but interrupts in-flight logins.
- Multi-replica configurations are rejected or clearly documented as unsupported
  until transactions, token rotation, cleanup locks, and cache invalidation use
  shared coordination.
- Inline token JSON remains an explicitly insecure compatibility override and is
  rejected in remote production mode.
- Deleting local tokens is unlinking, not revocation at Garmin. The documentation
  must state the difference.
