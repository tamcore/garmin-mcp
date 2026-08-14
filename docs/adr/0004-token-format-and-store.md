# ADR 0004 — Token format and storage backend

## Status

Open. Decided in phase 2, before the first token is persisted.

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

Completing this ADR requires:

- the selected pure-Go SQLite driver with version, license, and maintenance note;
- the persisted record set: principals and encrypted Garmin identity linkage,
  versioned encrypted DI token sets, registered OAuth clients and their exact
  redirect URIs, per-principal client consents, hashed authorization transactions
  and codes, hashed MCP token material with family, expiry, scopes, audience, and
  revocation state, schema version, encryption-key version, and audit events with
  no credentials or health/location payloads;
- the concurrency contract: WAL, foreign keys, busy timeout, bounded connections,
  transactions, and optimistic version or CAS on every rotating-token write;
- whether down migrations are supported, and the forward-migration atomicity and
  backup/restore test plan;
- the local `garmin_tokens.json` 0.3.x import and export contract, including
  `0700`/`0600` modes, symlink rejection, atomic writes, and the Windows ACL
  equivalent.

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
