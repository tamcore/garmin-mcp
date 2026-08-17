# ADR 0005 — Encryption and key management

## Status

**Implemented.** `internal/cryptostore` exists and both stores use it, so
secrets are written to disk today. The cryptography, the key file, staged
rotation, start-up refusal on bad key material, the operator-facing rotation
driver, and store-level re-sealing for both backends have all landed. A
platform secret manager or KMS adapter is the one item still open. See the two
lists below.

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

### Landed since the previous revision

- **An operator-facing rotation driver.** `garmin-mcp rotate-key` (`internal/cmd`)
  requires an explicit `--target-version`: one past the active version to start a
  new rotation, or the active version itself to resume one a previous run already
  activated and was killed before finishing — a killed run's marker already names
  the target, so refusing that value as "not active+1" would make resuming
  impossible. Loading the active-key-version marker distinguishes bootstrap from a
  completed rotation: the marker is created only when none exists yet, and a
  present marker whose key file has gone missing fails closed rather than minting
  a replacement, because any version a marker names is reachable only because a
  rotation actually activated it. Documented procedure in `docs/operations.md`
  section 4.
- **Store-level re-sealing, both backends.** `internal/store` gained a `keySet`
  (one active key plus zero or more retired keys) used by every read and write in
  the package. `SQLiteStore.ResealToActiveKey` re-seals the database index root,
  every principal's Garmin identity linkage, every Garmin token set, and every
  pending authorization transaction's client state, batched and resumable with no
  checkpoint table: each record's own key-version column is the resume mechanism,
  reconciled back in line with the active version when a row's content already
  matches it but the column still names a retired one (the shape an interrupted
  pass leaves behind), so the batch scan cannot loop on such a row forever. Reading
  a record still at a retired version during the window, an unknown key version
  failing closed, and a killed-and-resumed rotation not double-sealing are all
  covered by tests run under `-race`.
  `SQLiteStore.RemainingToReseal` is the completion proof, and it is a
  point-in-time scan: nothing re-checks the marker afterward, so it says nothing
  about a `serve` process that starts, or commits a write, immediately after the
  scan returns.
- **`FileStore.Reseal` covers the local backend's single bound principal's token
  record.** A concurrent application write racing the resealer needs more than the
  in-process `principalLocks` mutex, because `rotate-key` and a live `serve`
  process are separate processes, each opening its own `*FileStore` over the same
  directory, sharing that mutex with nothing else. The fix is two things together:
  an OS-level advisory lock (`flock(2)`, unix-only) held for the entire
  read-modify-write critical section of both `Save` and `Reseal`, which is what
  actually makes the section atomic across processes, plus a content-equality
  re-check of the record immediately before the write, the same defense already
  used for SQLite rows with no separate version counter. A test rewritten to use
  two separate `*FileStore` instances over one directory — rather than sharing
  one, which would only exercise the in-process mutex — failed reliably against
  the content-equality check alone and passes reliably with the lock added.
- **The `internal/securefile` hard-link requirement** is now documented for
  operators in `docs/operations.md` section 4.

### Still open

- A platform secret manager or a documented KMS adapter behind the same
  interface. None exists.

## Consequences

- A key colocated with the database protects backups and file disclosure, not a
  compromise of the running host. The documentation must say so plainly.
- Local single-user mode may use an owner-only token file or the OS keyring, but
  keeps the same store abstraction.
- Rotation is an operator procedure documented in `docs/operations.md`, with
  forward-migration and restore tests.
