# ADR 0005 — Encryption and key management

## Status

Open. Decided in phase 2, together with ADR 0004 and before any secret is
written to disk.

## Context

Garmin DI tokens and sensitive identity fields must be encrypted at rest. No
house reference implementation exists, so this is built from the requirements.
Release binaries stay `CGO_ENABLED=0`, so an OS keyring must never become a hard
build dependency.

## Decision

Keep encryption in one focused package, `internal/cryptostore`, with a narrow
exported API: `GenerateKey`, `LoadOrCreateKey`, `LoadKey`, `Encrypt`, `Decrypt`,
and nothing else.

Fixed properties:

- AES-GCM or an equivalent AEAD, with `crypto/rand` nonces.
- Versioned key IDs, so a record names the key that produced it.
- Authenticated additional data binding the principal ID and the record type, so
  a ciphertext cannot be moved between principals or record types.
- Staged key rotation with a tested migration path.
- The always-available backend is an owner-only (`0600`) key file under an
  explicit config directory, holding a versioned key ID and a base64 32-byte
  master key.
- Remote mode refuses to start on missing, malformed, or world-readable key
  material.
- The key is never logged or printed.
- Optional OS keyring support lives in build-tagged files (`keyring_darwin.go`,
  `keyring_linux.go`, `keyring_other.go`) with a no-op fallback, so
  `CGO_ENABLED=0` cross-compilation keeps working.
- A platform secret manager or a documented KMS adapter may implement the same
  interface.

Completing this ADR requires the selected AEAD, the exact key-file format, the
additional-data encoding, the rotation procedure with its operator commands, and
the tamper and wrong-key test list.

## Consequences

- A key colocated with the database protects backups and file disclosure, not a
  compromise of the running host. The documentation must say so plainly.
- Local single-user mode may use an owner-only token file or the OS keyring, but
  keeps the same store abstraction.
- Rotation is an operator procedure documented in `docs/operations.md`, with
  forward-migration and restore tests.
