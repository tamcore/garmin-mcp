# ADR 0004 — Token format and storage backend

## Status

**Partly implemented.** The file-store half has landed and is in use:
`internal/store` persists encrypted Garmin DI token sets today, so this ADR is no
longer a decision taken ahead of the first persisted token. The SQLite backend and
the MCP-side record set are still open. See the two lists in the Decision below.

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
  ancestry, and atomic writes. The Windows side is the ACL rule in
  `internal/securefile`, whose decision function is a pure rule executed by the
  Linux test run, and whose syscall layer only type-checks under `GOOS=windows`.
- Inline token JSON is refused unless explicitly enabled.
- The record **schema version moved from 1 to 2** when the wrapper's schema and
  version were bound into the AEAD as additional data. A schema-1 record now
  reports corruption instead of decoding, because its additional data no longer
  matches. **No migration exists and none is needed: nothing has shipped, so no
  schema-1 record exists outside a discarded working tree.** The next bump after
  a release carries schema 2 does need one.

### Still open

- The SQLite backend itself, with the selected pure-Go driver and its version,
  license, and maintenance note. Nothing SQLite-related exists.
- The rest of the persisted record set: principals and encrypted Garmin identity
  linkage, registered OAuth clients and their exact redirect URIs, per-principal
  client consents, hashed authorization transactions and codes, hashed MCP token
  material with family, expiry, scopes, audience, and revocation state, the
  encryption-key version column, and audit events with no credentials or
  health/location payloads.
- The concurrency contract for that backend: WAL, foreign keys, busy timeout,
  bounded connections, and transactions. Cross-process compare-and-set is **not**
  provided by `FileStore`, which holds a per-process mutex and no file lock, so
  the file store is safe for a single active instance only.
- Whether down migrations are supported, and the forward-migration atomicity and
  backup/restore test plan. No backup or restore test exists.
- Start-up refusal on bad key material. `internal/cmd` builds no store, so the
  `internal/cryptostore` key sentinels are never observed; see ADR 0005.
- A caller for `ParseInlineTokenJSON`, which is exposed and unconnected.

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
