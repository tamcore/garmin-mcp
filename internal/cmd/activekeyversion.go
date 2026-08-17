package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/securefile"
)

// The active key version marker: which cryptostore key version every new write
// is sealed under.
//
// This is key SELECTION metadata, not a record of rotation progress. It answers
// exactly one question — which key file should a write use today — and nothing
// else reads it to decide whether a rotation has finished: that answer comes only
// from the per-row key_version columns and the reseal scan (internal/store),
// which is what keeps a killed rotation resumable without a second source of
// truth that could disagree with the rows themselves.
//
// It exists because internal/cryptostore's staged rotation supports any number of
// key versions coexisting under one directory, but nothing before this feature
// recorded which one was current: this package carried a hardcoded constant
// instead. Both deployment shapes — the local FileStore and the multi-user SQLite
// store — read the same marker, so the bootstrap decision is identical regardless
// of backend.

// activeKeyVersionFileName is the marker file inside the key directory.
const activeKeyVersionFileName = "active-key-version.json"

// maxActiveVersionBytes bounds a marker read. The document is a handful of bytes.
const maxActiveVersionBytes = 1 << 10

// maxPlausibleKeyVersion bounds what resolveActiveKeyVersion will accept as an
// active key version, far tighter than cryptostore.MaxKeyVersion.
//
// loadKeyRing feeds the resolved version straight to loadRetiredKeys as the
// upper bound of a linear probe: one filesystem lookup per version below it.
// cryptostore.MaxKeyVersion alone (1<<32-1) bounds only what the envelope
// format can represent, not what is a plausible number of lookups to run on
// every start-up and every rotation. Rotation is manual and one version at a
// time (see AGENTS.md): an operator rotating once a day would need over 270
// years to reach this bound, so a marker naming a version above it is not a
// plausible operator state — it is corruption or an attempt to force an
// unbounded filesystem scan — and is refused the same way any other
// malformed marker is.
const maxPlausibleKeyVersion = 100_000

// activeVersionMode is the marker's owner-only file mode. The value is not secret
// material — it is a small positive integer — but it lives beside key files and is
// held to the same owner-only standard as everything else in that directory.
const activeVersionMode = 0o600

// keysDirMode is the owner-only mode the key directory is created with, matching
// internal/cryptostore's own directory mode for the key files that live beside
// this marker.
const keysDirMode = 0o700

// activeKeyVersionDocument is the on-disk shape. json.Number keeps a float or a
// quoted value from being coerced silently, the same defense LoadKey's own key
// document uses.
type activeKeyVersionDocument struct {
	Version json.Number `json:"version"`
}

// defaultActiveKeyVersion is what every deployment used before this marker
// existed, and what a fresh deployment starts at.
const defaultActiveKeyVersion = 1

// activeKeyVersionPath is the marker file inside paths.keys.
func activeKeyVersionPath(paths statePaths) string {
	return filepath.Join(paths.keys, activeKeyVersionFileName)
}

// resolveActiveKeyVersion reports the active key version for paths, and
// whether a marker was actually found on disk. An absent marker is not an
// error: it is the state of every deployment before rotation was ever used, or
// of a fresh one, and it resolves to defaultActiveKeyVersion with present
// false. present distinguishes that legitimate bootstrap from a marker that
// names a version whose key file has gone missing, which loadKeyRing must
// refuse rather than silently re-provision.
func resolveActiveKeyVersion(paths statePaths) (version int, present bool, err error) {
	raw, err := securefile.ReadFile(activeKeyVersionPath(paths), maxActiveVersionBytes)
	switch {
	case errors.Is(err, securefile.ErrNotFound):
		return defaultActiveKeyVersion, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("reading the active key version marker: %w", err)
	}

	var doc activeKeyVersionDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, false, fmt.Errorf("the active key version marker is not a JSON document: %w", ErrUnresolvedState)
	}
	parsed, err := doc.Version.Int64()
	if err != nil || parsed <= 0 {
		return 0, false, fmt.Errorf("the active key version marker does not hold a positive integer: %w",
			ErrUnresolvedState)
	}
	if parsed > maxPlausibleKeyVersion {
		return 0, false, fmt.Errorf(
			"the active key version marker holds %d, which exceeds the plausible maximum %d "+
				"for a manually rotated key: %w",
			parsed, maxPlausibleKeyVersion, ErrUnresolvedState)
	}
	return int(parsed), true, nil
}

// writeActiveKeyVersion atomically records version as active for paths.
//
// This is the moment a staged rotation's mixed-version read window opens: once
// this returns, every subsequent write is sealed under version, and every read of
// a record still at an older version depends on that older key being loaded
// alongside the active one. It must be called before re-sealing begins, not
// after, or a concurrent write during the reseal would still use the retiring key.
func writeActiveKeyVersion(paths statePaths, version int) error {
	if version <= 0 {
		return fmt.Errorf("%w: active key version %d must be positive", ErrUnresolvedState, version)
	}
	if err := securefile.EnsureDir(paths.keys, keysDirMode); err != nil {
		return fmt.Errorf("preparing the key directory: %w", err)
	}
	content, err := json.Marshal(activeKeyVersionDocument{Version: json.Number(strconv.Itoa(version))})
	if err != nil {
		return fmt.Errorf("encoding the active key version marker: %w", err)
	}
	if err := securefile.WriteFile(activeKeyVersionPath(paths), content, activeVersionMode); err != nil {
		return fmt.Errorf("writing the active key version marker: %w", err)
	}
	return nil
}

// loadKeyRing resolves the active key version and loads every lower version
// still present on disk as a retired key.
//
// The active key is created only when no marker was found at all: that is the
// sole legitimate bootstrap, the state of a deployment that has never served
// and never rotated. Once a marker exists, any version it names is reachable
// only because a rotation actually activated it, so a missing key file at that
// version means the key was lost — never that one should be minted in its
// place. Fabricating a fresh key there would silently strand every record still
// sealed under the real one and make it unrecoverable once the fabricated file
// is on disk, so that case fails closed instead.
//
// A retired key is loaded on a best-effort basis: a version that was never
// created, or one an operator has already deleted after confirming every record
// was re-sealed past it, is simply absent from the result. Any other failure to
// load a present file — unsafe permissions, a malformed document — is reported,
// because that is an operator problem to fix rather than a version to skip.
func loadKeyRing(paths statePaths) (active cryptostore.Key, retired []cryptostore.Key, err error) {
	version, present, err := resolveActiveKeyVersion(paths)
	if err != nil {
		return cryptostore.Key{}, nil, err
	}

	if present {
		active, err = cryptostore.LoadKey(paths.keys, version)
		if err != nil {
			return cryptostore.Key{}, nil, fmt.Errorf(
				"opening the encryption key the active-key-version marker names: %w", err)
		}
	} else {
		active, err = cryptostore.LoadOrCreateKey(paths.keys, version)
		if err != nil {
			return cryptostore.Key{}, nil, fmt.Errorf("opening the active encryption key: %w", err)
		}
	}

	retired, err = loadRetiredKeys(paths, version)
	if err != nil {
		return cryptostore.Key{}, nil, err
	}
	return active, retired, nil
}

// loadRetiredKeys loads every key version below upper (exclusive) that is
// still present on disk, in ascending order.
//
// A version that was never created is simply absent from the result: that is
// the ordinary shape once an operator has retired a key after confirming every
// record was re-sealed past it. A version whose file exists but cannot be
// loaded — unsafe permissions, a malformed document — is reported by name
// instead of silently skipped, because a caller resealing records depends on
// that exact key to read what has not moved yet, and a silent skip there turns
// into an opaque decrypt failure with no named cause.
func loadRetiredKeys(paths statePaths, upper int) ([]cryptostore.Key, error) {
	var retired []cryptostore.Key
	for older := 1; older < upper; older++ {
		key, err := cryptostore.LoadKey(paths.keys, older)
		switch {
		case err == nil:
			retired = append(retired, key)
		case errors.Is(err, cryptostore.ErrKeyNotFound):
			continue
		default:
			return nil, fmt.Errorf("opening retired encryption key version %d: %w", older, err)
		}
	}
	return retired, nil
}
