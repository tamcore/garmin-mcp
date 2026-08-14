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
// under /var is refused, since /var is a symlink to /private/var.
package cryptostore

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// keyLen is the master key size in bytes. 32 bytes selects AES-256-GCM.
const keyLen = 32

// File and directory modes for key material. Both are enforced with an explicit
// chmod after creation, because the modes passed to os.MkdirAll and
// os.OpenFile are masked by the process umask, which is global and may be
// hostile.
const (
	keyFileMode = 0o600
	keyDirMode  = 0o700
)

// Key is one versioned master key.
//
// It is secret-bearing. The material sits behind an unexported pointer, so a
// reflective logger, a direct field print and a method-stripping alias
// (type Raw cryptostore.Key) all see an address rather than the key. String,
// GoString, MarshalJSON and LogValue report only the version.
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

// keySecret holds the raw key. It is never mutated after construction.
type keySecret struct {
	material []byte
}

// bytes returns the raw key material, or nil for the zero Key. It is unexported:
// nothing outside this package may hold the key.
func (k Key) bytes() []byte {
	if k.secret == nil {
		return nil
	}
	return k.secret.material
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
	return Key{version: version, secret: &keySecret{material: material}}, nil
}

// LoadKey reads the key file for version from dir. It refuses material that is
// missing (ErrKeyNotFound), not owner-only (ErrInsecureKeyPermissions), reached
// through a symlink (ErrInsecureKeyPath), or not a version id plus a base64
// 32-byte key (ErrMalformedKey). No error text ever contains key material.
func LoadKey(dir string, version int) (Key, error) {
	if version <= 0 {
		return Key{}, fmt.Errorf("cryptostore: load key version %d: %w", version, ErrInvalidKeyVersion)
	}
	path := keyFilePath(dir, version)
	if err := checkNoSymlinkAncestry(path, ErrInsecureKeyPath); err != nil {
		return Key{}, err
	}

	raw, err := readOwnerOnlyKeyFile(path)
	if err != nil {
		return Key{}, err
	}
	return decodeKeyFile(raw, version, path)
}

// LoadOrCreateKey returns the key for version from dir, creating it if absent.
// The directory is created with mode 0700 and the key file with mode 0600, both
// enforced by an explicit chmod so a permissive umask cannot widen them. An
// existing file that is malformed or not owner-only is an error, never silently
// replaced: overwriting it would destroy the only way to read existing records.
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
	if err := writeKeyFile(dir, generated); err != nil {
		return Key{}, err
	}
	// Read back through the same validation the load path uses, so a created key
	// is provably loadable and owner-only.
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
	return Key{version: wantVersion, secret: &keySecret{material: material}}, nil
}

// writeKeyFile persists key under dir. It writes a temporary sibling first and
// renames it, so a concurrent reader never sees a half-written key.
func writeKeyFile(dir string, key Key) error {
	if err := os.MkdirAll(dir, keyDirMode); err != nil {
		return fmt.Errorf("cryptostore: create key directory %q: %w", dir, err)
	}
	// MkdirAll's mode is masked by the umask and is a no-op for a directory that
	// already exists; chmod enforces 0700 unconditionally.
	if err := os.Chmod(dir, keyDirMode); err != nil {
		return fmt.Errorf("cryptostore: secure key directory %q: %w", dir, err)
	}

	content, err := json.Marshal(keyFileContent{
		Version: json.Number(strconv.Itoa(key.version)),
		Key:     base64.StdEncoding.EncodeToString(key.bytes()),
	})
	if err != nil {
		return fmt.Errorf("cryptostore: encode key document: %w", err)
	}

	path := keyFilePath(dir, key.version)
	if err := checkNoSymlinkAncestry(path, ErrInsecureKeyPath); err != nil {
		return err
	}
	if err := writeFileAtomically(path, content, keyFileMode); err != nil {
		return fmt.Errorf("cryptostore: write key file %q: %w", path, err)
	}
	return nil
}

// readOwnerOnlyKeyFile reads path, refusing to follow a symlink and refusing
// material any other local account can reach.
func readOwnerOnlyKeyFile(path string) ([]byte, error) {
	file, err := openNoFollow(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("cryptostore: key file %q: %w: %w", path, ErrKeyNotFound, err)
		}
		return nil, fmt.Errorf("cryptostore: open key file %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("cryptostore: stat key file %q: %w", path, err)
	}
	if err := checkOwnerOnly(info.Mode(), path, ErrInsecureKeyPermissions); err != nil {
		return nil, err
	}
	raw, err := readBounded(file, maxKeyFileBytes)
	if err != nil {
		return nil, fmt.Errorf("cryptostore: read key file %q: %w", path, err)
	}
	return raw, nil
}
