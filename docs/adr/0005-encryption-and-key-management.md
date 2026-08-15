# ADR 0005 — Encryption and key management

## Status

**Partly implemented.** `internal/cryptostore` exists and both stores use it, so
secrets are written to disk today. The cryptography, the key file, staged
rotation, and start-up refusal on bad key material have landed. An
operator-facing rotation driver and store-level re-sealing are still open. See
the two lists below.

## Context

Garmin DI tokens and sensitive identity fields must be encrypted at rest. No
house reference implementation exists, so this is built from the requirements.
Release binaries stay `CGO_ENABLED=0`, so an OS keyring must never become a hard
build dependency.

## Decision

Keep encryption in one focused package, `internal/cryptostore`, with a narrow
exported API: `GenerateKey`, `LoadOrCreateKey`, `LoadKey`, `Encrypt`, `Decrypt`,
and nothing else.

### Landed

- The AEAD is **AES-256-GCM** with `crypto/rand` nonces. The exported surface is
  the five named functions and nothing else, pinned by an AST-based surface test.
- The envelope format: a five-byte header — one format-version byte plus the
  four-byte key version — then the 12-byte nonce and the ciphertext with its
  16-byte tag. The key version sits outside the ciphertext because a reader must
  pick the key before it can authenticate anything, and it is inside the
  additional data, so it cannot be edited undetected.
- The additional-data encoding: a fixed context string, the key version, and the
  principal and record type appended **length-prefixed**, so adjacent fields
  cannot be confused. `internal/store` extends the same binding with the record
  wrapper's schema and CAS version. Tampering, a wrong key of the same version, a
  wrong principal, and a wrong record type all collapse to `ErrAuthentication`.
- The key-file format: one file per version, `key-v<N>.json` under an explicit
  config directory, holding a version ID and a base64 32-byte master key, in a
  `0700` directory at mode `0600`, bounded to 4 KiB on read, with a `json.Number`
  version so a float or quoted value is not coerced silently. Creation installs
  the file exclusively by hard link, so two creators agree on one winner.
- Staged key rotation, proven end to end inside the package by
  `TestStagedRotationReencryptsRecords`.
- The key is never logged or printed: `Key` seals the raw bytes in a nested
  unexported struct whose render paths are all covered.
- Optional OS keyring support lives in build-tagged files (`keyring_darwin.go`,
  `keyring_linux.go`, `keyring_other.go`). All three are cgo-free **no-ops** that
  report unavailable, which keeps `CGO_ENABLED=0` cross-compilation working. The
  owner-only key file is the only real backend.

- **Start-up refusal on bad key material**, both halves. The lexical half is
  `Config.validateRemoteState`, which requires a database path and a master key
  for the streamable-http transport and refuses inline material there. The
  runtime half is the composition root: `internal/cmd/components.go` and
  `internal/cmd/remote.go` call `cryptostore.LoadOrCreateKey` before anything
  serves, and `internal/cmd/doctor.go` loads the key and branches on
  `ErrKeyNotFound` and `ErrInsecureKeyPermissions` so an operator gets a named
  cause rather than a generic failure.

### Still open

- **An operator-facing rotation driver.** Rotation is a library capability with no
  command, no procedure, and no `docs/operations.md`.
- **Store-level re-sealing.** `FileStore` holds exactly one key and re-seals
  nothing, so no existing record is migrated to a new key version.
- A platform secret manager or a documented KMS adapter behind the same
  interface. None exists.
- The `internal/securefile` hard-link requirement is undocumented for operators:
  key material on a filesystem without hard links fails loudly on creation, and
  there is no rename fallback on purpose.

## Consequences

- A key colocated with the database protects backups and file disclosure, not a
  compromise of the running host. The documentation must say so plainly.
- Local single-user mode may use an owner-only token file or the OS keyring, but
  keeps the same store abstraction.
- Rotation is an operator procedure documented in `docs/operations.md`, with
  forward-migration and restore tests.
