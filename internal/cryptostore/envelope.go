package cryptostore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// Envelope wire format, version 1:
//
//	byte  0      envelope format version
//	bytes 1..4   key version, big endian uint32
//	bytes 5..16  AES-GCM nonce
//	bytes 17..n  ciphertext with the appended GCM tag
//
// The key version sits outside the ciphertext on purpose: a reader must pick the
// right key before it can authenticate anything. It is still covered by the
// additional data, so it cannot be edited undetected.
const (
	envelopeFormatVersion = 1

	formatVersionLen = 1
	keyVersionLen    = 4
	headerLen        = formatVersionLen + keyVersionLen
	nonceLen         = 12 // AES-GCM standard nonce size
	tagLen           = 16 // AES-GCM tag size
)

// aadContext is the domain separator in the additional data. It keeps a
// cryptostore envelope from being confused with any other AEAD ciphertext this
// project may produce later.
const aadContext = "garmin-mcp/cryptostore/v1"

// Encrypt seals plaintext for one principal and one record type under key.
//
// principal is the internal principal id and recordType names the kind of record
// (for example "garmin_di_tokens"). Both are authenticated but not encrypted, so
// a ciphertext cannot be replayed under a different principal or as a different
// kind of record: such a Decrypt fails with ErrAuthentication.
//
// recordType is an opaque label, not an enumeration, so a caller may append the
// wrapper state a reader must see before it can decrypt — a schema and a record
// version, say. Appending it here is what authenticates it. Every part is
// length-prefixed in the additional data, so no two different pairs can produce the
// same binding.
//
// The returned envelope is self-describing: it carries the format version and
// the key version, so a later rotation can find the key that opens it.
func Encrypt(key Key, principal, recordType string, plaintext []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("cryptostore: generate nonce: %w", err)
	}

	sealed := make([]byte, 0, headerLen+nonceLen+len(plaintext)+tagLen)
	sealed = append(sealed, encodeHeader(key.version)...)
	sealed = append(sealed, nonce...)
	sealed = aead.Seal(sealed, nonce, plaintext, additionalData(key.version, principal, recordType))
	return sealed, nil
}

// Decrypt opens an envelope produced by Encrypt.
//
// It reports ErrMalformedEnvelope for a byte slice that is not an envelope,
// ErrKeyVersionMismatch when the envelope was sealed under another key version —
// the signal to load that version during staged rotation — and ErrAuthentication
// when authentication fails, which covers tampering, a wrong key of the same
// version, and a replay under a different principal or record type.
func Decrypt(key Key, principal, recordType string, sealed []byte) ([]byte, error) {
	env, err := parseEnvelope(sealed)
	if err != nil {
		return nil, err
	}
	if int(env.keyVersion) != key.version {
		return nil, fmt.Errorf("cryptostore: envelope key version %d, key version %d: %w",
			env.keyVersion, key.version, ErrKeyVersionMismatch)
	}

	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, env.sealed.nonce, env.sealed.ciphertext,
		additionalData(key.version, principal, recordType))
	if err != nil {
		// The AEAD error text is discarded: the failure causes must stay
		// indistinguishable, and only this package can promise that.
		return nil, fmt.Errorf("cryptostore: open envelope of key version %d: %w", env.keyVersion, ErrAuthentication)
	}
	return plaintext, nil
}

// SealedKeyVersion reports the key version an envelope declares in its header,
// without needing any key and without authenticating anything.
//
// The header is covered by the additional data, so a caller that goes on to
// Decrypt still gets an authenticated answer; this function alone only reports
// what the bytes claim. That makes it safe for inventory and completion
// bookkeeping — "is this record still under a retired key version" — but never
// for an authorization decision: a tampered header is only caught by Decrypt.
func SealedKeyVersion(sealed []byte) (int, error) {
	env, err := parseEnvelope(sealed)
	if err != nil {
		return 0, err
	}
	return int(env.keyVersion), nil
}

// envelope is a parsed envelope. It is secret-bearing, so the nonce and the
// ciphertext sit behind an unexported pointer and every rendering reports only
// versions and lengths.
type envelope struct {
	formatVersion byte
	keyVersion    uint32

	// sealed is a pointer for the same reason Key.secret is: fmt follows a
	// pointer only at the top level.
	sealed *sealedParts
}

// sealedParts holds the material. It is never mutated after construction.
type sealedParts struct {
	nonce      []byte
	ciphertext []byte
}

// parseEnvelope splits raw into its header, nonce and ciphertext without
// authenticating anything. It validates only lengths and the format version, so
// that a caller can select a key.
func parseEnvelope(raw []byte) (envelope, error) {
	minLen := headerLen + nonceLen + tagLen
	if len(raw) < minLen {
		return envelope{}, fmt.Errorf("cryptostore: envelope is %d bytes, want at least %d: %w",
			len(raw), minLen, ErrMalformedEnvelope)
	}
	if raw[0] != envelopeFormatVersion {
		return envelope{}, fmt.Errorf("cryptostore: envelope format version %d is not supported: %w",
			raw[0], ErrMalformedEnvelope)
	}
	return envelope{
		formatVersion: raw[0],
		keyVersion:    binary.BigEndian.Uint32(raw[formatVersionLen:headerLen]),
		sealed: &sealedParts{
			nonce:      raw[headerLen : headerLen+nonceLen],
			ciphertext: raw[headerLen+nonceLen:],
		},
	}, nil
}

// encodeHeader renders the format version and the key version. A negative or
// oversized version cannot reach it: GenerateKey and LoadKey reject those.
func encodeHeader(keyVersion int) []byte {
	header := make([]byte, headerLen)
	header[0] = envelopeFormatVersion
	binary.BigEndian.PutUint32(header[formatVersionLen:], uint32(keyVersion)) //nolint:gosec // validated positive
	return header
}

// additionalData binds the envelope to its context. Each variable-length part is
// length-prefixed, so ("ab", "c") and ("a", "bc") cannot produce identical
// additional data.
func additionalData(keyVersion int, principal, recordType string) []byte {
	out := make([]byte, 0, len(aadContext)+headerLen+8+len(principal)+len(recordType))
	out = append(out, aadContext...)
	out = append(out, encodeHeader(keyVersion)...)
	out = appendLengthPrefixed(out, principal)
	return appendLengthPrefixed(out, recordType)
}

func appendLengthPrefixed(out []byte, value string) []byte {
	out = binary.BigEndian.AppendUint32(out, uint32(len(value))) //nolint:gosec // bounded by input length
	return append(out, value...)
}

// newAEAD builds the AES-256-GCM instance for key. The zero Key is refused: an
// absent key must never encrypt anything.
func newAEAD(key Key) (cipher.AEAD, error) {
	material := key.bytes()
	if len(material) != keyLen {
		return nil, fmt.Errorf("cryptostore: key holds %d bytes, want %d: %w",
			len(material), keyLen, ErrMalformedKey)
	}
	block, err := aes.NewCipher(material)
	if err != nil {
		return nil, fmt.Errorf("cryptostore: build cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptostore: build AEAD: %w", err)
	}
	return aead, nil
}
