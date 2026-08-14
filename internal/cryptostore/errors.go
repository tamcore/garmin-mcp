package cryptostore

import "errors"

// Sentinel errors. Every failure this package reports is comparable with
// errors.Is, because callers must be able to distinguish "the operator has not
// provisioned a key yet" from "the key material on disk is unsafe" and from
// "this ciphertext is not ours".
var (
	// ErrInvalidKeyVersion means a key version was not a positive integer.
	// Version numbers start at 1 and only ever increase, so 0 and negative
	// values indicate a configuration mistake.
	ErrInvalidKeyVersion = errors.New("cryptostore: key version must be positive")

	// ErrKeyNotFound means no key file exists for the requested version. It
	// wraps fs.ErrNotExist, so a caller may also test for that.
	ErrKeyNotFound = errors.New("cryptostore: key material not found")

	// ErrMalformedKey means the key file exists but does not hold a usable
	// version id plus a base64 32-byte key. The error never quotes the file
	// content, because a partially valid file still contains key material.
	ErrMalformedKey = errors.New("cryptostore: key material is malformed")

	// ErrInsecureKeyPermissions means the key file is readable or writable by
	// group or other. Key material another local account can read is treated as
	// compromised, so loading fails instead of warning.
	ErrInsecureKeyPermissions = errors.New("cryptostore: key material is not owner-only")

	// ErrInsecureKeyPath means a component of the key path is a symlink, or could
	// not be checked. A planted symlink must not redirect a read or a write of
	// key material.
	//
	// Source: token_file_path in client.py (0.3.10), which rejects symlinks
	// across the full path ancestry rather than relying on O_NOFOLLOW, which
	// covers the last component only.
	ErrInsecureKeyPath = errors.New("cryptostore: key path is not safe")

	// ErrKeyVersionMismatch means the envelope was sealed under a different key
	// version than the key supplied. During staged rotation the caller loads the
	// envelope's version with LoadKey and retries.
	ErrKeyVersionMismatch = errors.New("cryptostore: envelope key version does not match the key")

	// ErrMalformedEnvelope means the byte slice is not a cryptostore envelope:
	// too short, or an unknown envelope format version.
	ErrMalformedEnvelope = errors.New("cryptostore: envelope is malformed")

	// ErrAuthentication means AEAD authentication failed. The ciphertext was
	// tampered with, sealed under a different key of the same version, or is
	// being replayed under a different principal or record type. Those cases are
	// deliberately indistinguishable to the caller.
	ErrAuthentication = errors.New("cryptostore: envelope failed authentication")
)
