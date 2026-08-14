// Package cryptostore provides versioned AEAD envelope encryption for the
// credential material this server persists: Garmin DI token sets today, and any
// other secret-bearing record later.
//
// The API is deliberately narrow. Exactly five functions are exported:
// GenerateKey, LoadOrCreateKey and LoadKey obtain a master key, and Encrypt and
// Decrypt seal and open one record.
//
// # Envelope binding
//
// Every envelope carries the key version that sealed it, and every seal
// authenticates the principal id and the record type as additional data. A
// ciphertext therefore cannot be replayed under a different principal or as a
// different kind of record: Decrypt fails with ErrAuthentication instead of
// returning someone else's tokens.
//
// # Staged key rotation
//
// Key versions are positive integers stored one file per version, so version N
// stays readable while records migrate to N+1:
//
//	oldKey, err := cryptostore.LoadKey(dir, n)            // retained, read-only
//	newKey, err := cryptostore.LoadOrCreateKey(dir, n+1)  // created once
//	for each record:
//	    plain, err := cryptostore.Decrypt(oldKey, principal, recordType, sealed)
//	    sealed, err := cryptostore.Encrypt(newKey, principal, recordType, plain)
//	    // persist sealed with its new key version, transactionally
//
// A record still sealed under version N reports ErrKeyVersionMismatch when
// opened with the version N+1 key, which is the signal to fetch the older key
// rather than to discard the record. Delete a retired key file only after every
// record has been re-sealed.
//
// # One key per version, forever
//
// Key material for a version is installed exactly once. LoadOrCreateKey creates
// the file with no-replace semantics, so two processes that start together elect
// one winner and the loser discards its own material and loads the winner's.
// Replacing an existing key file would leave every record already sealed under the
// old material unreadable, so it is never done, not even when the existing file is
// malformed or has unsafe permissions: that is an error for an operator to resolve.
//
// # Threat model
//
// A key file that sits next to the database protects backups and file
// disclosure. It does not protect against compromise of the running host, which
// can read both. Never log or print key material; the Key type exposes no
// accessor that returns it.
//
// The key path must contain no symlinked component, including in its parent
// directories, because a planted symlink is how key material gets redirected.
// The configured directory therefore has to be a real path: on macOS a path
// under /var is refused, since /var is a symlink to /private/var. See
// internal/securefile for how every path component is verified.
package cryptostore

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/securefile"
)

// keyLen is the master key size in bytes. 32 bytes selects AES-256-GCM.
const keyLen = 32

// maxKeyFileBytes bounds a key document read. A key file is a few hundred bytes;
// anything larger is a mistake or an attempt to exhaust memory.
const maxKeyFileBytes = 4 << 10

// File and directory modes for key material. Both are enforced with an explicit
// chmod after creation, because the modes passed to mkdir and open are masked by
// the process umask, which is global and may be hostile.
const (
	keyFileMode = 0o600
	keyDirMode  = 0o700
)

// Key is one versioned master key.
//
// It is secret-bearing. The material sits behind two levels of unexported
// indirection, so a reflective logger, a direct field print and a
// method-stripping alias (type Raw cryptostore.Key) all see an address rather
// than the key. String, GoString, MarshalJSON and LogValue report only the
// version.
//
// A Key is immutable after construction and safe to copy and share. The zero
// value is inert: Encrypt and Decrypt reject it.
type Key struct {
	version int

	// secret is a pointer on purpose. fmt follows a pointer only at the top
	// level, so a nested unexported pointer renders as an address, whereas a
	// nested unexported struct renders its field values.
	secret *keySecret
}

// keyMaterial is the raw key bytes under their own type, so the field that holds
// them can be a pointer.
type keyMaterial []byte

// keySecret holds the raw key. It is never mutated after construction.
//
// material is a pointer, not a plain slice. fmt's %s and %q on a value with no
// String method fall into badVerb, which re-prints the value at depth zero, and
// depth zero dereferences a pointer to a struct and prints its unexported fields.
// A plain []byte field would surface there as its decimal bytes; a pointer field
// renders as an address at that depth.
type keySecret struct {
	material *keyMaterial
}

// bytes returns the raw key material, or nil for the zero Key. It is unexported:
// nothing outside this package may hold the key.
func (k Key) bytes() []byte {
	if k.secret == nil || k.secret.material == nil {
		return nil
	}
	return []byte(*k.secret.material)
}

// newKey wraps material in the indirection every Key uses.
func newKey(version int, material []byte) Key {
	held := keyMaterial(material)
	return Key{version: version, secret: &keySecret{material: &held}}
}

// GenerateKey returns a new random key with the given version. The key exists
// only in memory; use LoadOrCreateKey to persist one. version must be positive.
func GenerateKey(version int) (Key, error) {
	if version <= 0 {
		return Key{}, fmt.Errorf("cryptostore: generate key version %d: %w", version, ErrInvalidKeyVersion)
	}
	material := make([]byte, keyLen)
	if _, err := rand.Read(material); err != nil {
		return Key{}, fmt.Errorf("cryptostore: generate key material: %w", err)
	}
	return newKey(version, material), nil
}

// LoadKey reads the key file for version from dir. It refuses material that is
// missing (ErrKeyNotFound), not owner-only (ErrInsecureKeyPermissions), reached
// through a symlink or found as something other than a regular file
// (ErrInsecureKeyPath), or not a version id plus a base64 32-byte key
// (ErrMalformedKey). No error text ever contains key material.
func LoadKey(dir string, version int) (Key, error) {
	if version <= 0 {
		return Key{}, fmt.Errorf("cryptostore: load key version %d: %w", version, ErrInvalidKeyVersion)
	}

	path := keyFilePath(dir, version)
	raw, err := securefile.ReadFile(path, maxKeyFileBytes)
	if err != nil {
		return Key{}, keyFileError("read", path, err)
	}
	return decodeKeyFile(raw, version, path)
}

// LoadOrCreateKey returns the key for version from dir, creating it if absent.
//
// The directory is created with mode 0700 and the key file with mode 0600, both
// enforced by an explicit chmod so a permissive umask cannot widen them. An
// existing file that is malformed or not owner-only is an error, never silently
// replaced: overwriting it would destroy the only way to read existing records.
//
// Creation is a no-replace install. When another process wins the race, this call
// discards the material it generated and returns the winner's, so no caller ever
// holds a key that a restart will not load.
func LoadOrCreateKey(dir string, version int) (Key, error) {
	key, err := LoadKey(dir, version)
	switch {
	case err == nil:
		return key, nil
	case !errors.Is(err, ErrKeyNotFound):
		return Key{}, err
	}

	generated, err := GenerateKey(version)
	if err != nil {
		return Key{}, err
	}
	if err := installKeyFile(dir, generated); err != nil && !errors.Is(err, fs.ErrExist) {
		return Key{}, err
	}
	// Read back through the same validation the load path uses, so a created key
	// is provably loadable and owner-only, and so a creator that lost the race
	// returns the material that is actually on disk.
	return LoadKey(dir, version)
}

// keyFilePath is the deterministic name of one key version inside dir.
func keyFilePath(dir string, version int) string {
	return filepath.Join(dir, "key-v"+strconv.Itoa(version)+".json")
}

// keyFileContent is the on-disk shape: a version id plus a base64 32-byte key.
// json.Number keeps a float or a quoted version from being coerced silently.
type keyFileContent struct {
	Version json.Number `json:"version"`
	Key     string      `json:"key"`
}

// decodeKeyFile parses and validates raw. path is reported for operator
// diagnosis; raw never is.
func decodeKeyFile(raw []byte, wantVersion int, path string) (Key, error) {
	malformed := func(reason string) (Key, error) {
		return Key{}, fmt.Errorf("cryptostore: key file %q: %s: %w", path, reason, ErrMalformedKey)
	}

	var content keyFileContent
	if err := json.Unmarshal(raw, &content); err != nil {
		return malformed("not a key document")
	}
	version, err := content.Version.Int64()
	if err != nil || version <= 0 {
		return malformed("version is not a positive integer")
	}
	if int(version) != wantVersion {
		return malformed("holds version " + content.Version.String() + ", not " + strconv.Itoa(wantVersion))
	}
	material, err := base64.StdEncoding.DecodeString(content.Key)
	if err != nil {
		return malformed("key is not standard base64")
	}
	if len(material) != keyLen {
		return malformed("key is " + strconv.Itoa(len(material)) + " bytes, want " + strconv.Itoa(keyLen))
	}
	return newKey(wantVersion, material), nil
}

// installKeyFile persists key under dir without replacing anything.
//
// The content is written and synced into a temporary sibling and then linked into
// place, which fails when the name is already taken. A collision reports an error
// wrapping fs.ErrExist, which is the caller's signal to discard its material and
// load the winner's.
func installKeyFile(dir string, key Key) error {
	if err := securefile.EnsureDir(dir, keyDirMode); err != nil {
		return keyFileError("prepare key directory", dir, err)
	}

	content, err := json.Marshal(keyFileContent{
		Version: json.Number(strconv.Itoa(key.version)),
		Key:     base64.StdEncoding.EncodeToString(key.bytes()),
	})
	if err != nil {
		return fmt.Errorf("cryptostore: encode key document: %w", err)
	}

	path := keyFilePath(dir, key.version)
	if err := securefile.InstallNewFile(path, content, keyFileMode); err != nil {
		return keyFileError("install", path, err)
	}
	return nil
}

// keyFileError translates a securefile failure onto this package's sentinels. The
// cause is kept in the chain: it names paths, modes and sizes only, never
// material.
func keyFileError(operation, path string, err error) error {
	sentinel := func(specific error) error {
		return fmt.Errorf("cryptostore: %s key material %q: %w: %w", operation, path, specific, err)
	}

	switch {
	case errors.Is(err, securefile.ErrNotFound):
		return sentinel(ErrKeyNotFound)
	case errors.Is(err, securefile.ErrExists):
		return fmt.Errorf("cryptostore: %s key material %q: material is already present: %w",
			operation, path, err)
	case errors.Is(err, securefile.ErrInsecurePermissions):
		return sentinel(ErrInsecureKeyPermissions)
	case errors.Is(err, securefile.ErrInsecurePath):
		return sentinel(ErrInsecureKeyPath)
	}
	return fmt.Errorf("cryptostore: %s key material %q: %w", operation, path, err)
}
