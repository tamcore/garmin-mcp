package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Capability encoding. A capability is 256 bits of entropy in canonical base64url
// without padding, which is exactly 43 characters.
const (
	// capabilityBytes is the entropy of a transaction capability: 256 bits.
	capabilityBytes = 32
	// capabilityEncodedLen is the only accepted encoded length.
	capabilityEncodedLen = 43
)

// ErrMalformedCapability reports a capability that is not in the canonical
// encoding. It is always reported together with ErrUnknownTransaction, so the
// distinction is not an oracle for whether a well-formed capability exists; it
// exists so the package can state that a malformed value is refused before it is
// hashed.
var ErrMalformedCapability = errors.New("garmin auth: malformed login transaction capability")

// capabilityDigest validates the canonical encoding of capability and returns its
// SHA-256.
//
// The length and the encoding are checked first, so an arbitrarily large string is
// never hashed: a caller cannot turn the registry into a hashing oracle by
// presenting a megabyte of data. Strict decoding also rejects a non-canonical
// final character, so two spellings of one value cannot both be accepted.
func capabilityDigest(capability string) ([]byte, error) {
	if len(capability) != capabilityEncodedLen {
		return nil, malformedCapability()
	}

	raw, err := base64.RawURLEncoding.Strict().DecodeString(capability)
	if err != nil || len(raw) != capabilityBytes {
		return nil, malformedCapability()
	}

	sum := sha256.Sum256([]byte(capability))
	return sum[:], nil
}

// malformedCapability is the sanitized rejection of a non-canonical capability. It
// renders neither the value nor its length.
func malformedCapability() error {
	return fmt.Errorf("%w: %w", ErrUnknownTransaction, ErrMalformedCapability)
}

// capabilityKeyFor is the map key for a digest. Hashing means the registry never
// holds a usable capability.
func capabilityKeyFor(digest []byte) string { return hex.EncodeToString(digest) }

// sameDigest compares two digests in constant time.
//
// The map lookup that precedes it is not constant time, and no in-process map can
// be; the digest is the hash of a 256-bit random value, so the lookup leaks no
// guessable material. This comparison is the check that decides, so a future keyed
// or externalized index cannot regress into an early-exit compare.
func sameDigest(stored, presented []byte) bool {
	return subtle.ConstantTimeCompare(stored, presented) == 1
}

// newCapability returns a fresh 256-bit opaque capability, base64url encoded
// without padding so it is safe in a cookie value.
func newCapability(source io.Reader) (string, error) {
	raw := make([]byte, capabilityBytes)
	if _, err := io.ReadFull(source, raw); err != nil {
		return "", fmt.Errorf("garmin auth: generate login transaction capability: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
