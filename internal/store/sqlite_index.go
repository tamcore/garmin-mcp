package store

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Lookup values.
//
// The store must be able to find a row by a credential — an MCP access token, a
// refresh token, an authorization code, a transaction handle, a client secret, a
// stable Garmin account identifier — without holding the credential. A plain
// SHA-256 would do that, but it is unkeyed: anyone who obtains the database can
// confirm a guess offline, and for a low-entropy value such as a Garmin numeric
// account id that guess space is small enough to enumerate. A keyed HMAC removes
// that: without the key the column is useless.
//
// # Purpose derivation
//
// One random 32-byte root is generated when the database is created and stored in
// schema_meta as an AEAD envelope, sealed by internal/cryptostore with the record
// type as additional data. Every lookup key is derived from that root for one
// purpose only:
//
//	lookupKey(purpose) = HMAC-SHA256(root, "garmin-mcp/store/index/v1/" + purpose)
//	lookupValue        = hex(HMAC-SHA256(lookupKey(purpose), value))
//
// Per-purpose derivation is what makes the columns non-comparable across kinds: a
// refresh token and an access token with the same bytes produce different lookup
// values, so a stolen access token cannot be presented as a refresh token by
// matching a hash, and a Garmin account id cannot be tested against the token
// table.
//
// The root is not a cryptostore key and never becomes one. It authenticates
// nothing and decrypts nothing; it only makes the lookup columns keyed. Keeping it
// separate means a cryptostore key rotation does not invalidate every index, which
// it would if the lookup keys were derived from the encryption key.

// indexRootLen is the size of the derivation root.
const indexRootLen = 32

// indexContext is the domain separator for every derived key.
const indexContext = "garmin-mcp/store/index/v1/"

// Purposes. Each one is a separate derived key, so two columns never share a
// keyspace. They are string constants rather than an enumeration because they are
// part of the on-disk contract: changing a value invalidates every stored lookup
// value for that purpose.
const (
	purposeGarminAccount = "garmin_account"
	purposeAccessToken   = "mcp_access_token"
	purposeRefreshToken  = "mcp_refresh_token"
	purposeAuthCode      = "oauth_authorization_code"
	purposeTransaction   = "oauth_authorization_transaction"
	purposeClientSecret  = "oauth_client_secret"
)

// indexRootRecordType is the record type bound into the AEAD additional data of the
// sealed root, so the envelope cannot be replayed as any other kind of record.
const indexRootRecordType = "store_index_root"

// indexRootPrincipal is the principal bound into that additional data. The root is
// database-wide rather than per principal, and the binding still has to be a fixed,
// non-empty string so the additional data is unambiguous.
const indexRootPrincipal = "-database-"

// indexKeys derives lookup keys from the root. It is created once per store, holds
// no mutable state, and is safe for concurrent use.
type indexKeys struct {
	// root is a pointer so a print of the enclosing struct renders an address
	// rather than the bytes, the same reason tokenParts uses pointers.
	root *indexRoot
}

// indexRoot holds the derivation material.
type indexRoot struct {
	material []byte
}

// newIndexRootMaterial generates a fresh root.
func newIndexRootMaterial() ([]byte, error) {
	material := make([]byte, indexRootLen)
	if _, err := rand.Read(material); err != nil {
		return nil, fmt.Errorf("store: generate index root: %w", err)
	}
	return material, nil
}

// newIndexKeys wraps root material, refusing a wrong size so a truncated envelope
// cannot silently weaken every lookup value.
func newIndexKeys(material []byte) (indexKeys, error) {
	if len(material) != indexRootLen {
		return indexKeys{}, fmt.Errorf("store: index root holds %d bytes, want %d: %w",
			len(material), indexRootLen, ErrCorruptRecord)
	}
	// The material is copied so a caller's slice cannot be mutated underneath the
	// store, and so the store cannot mutate the caller's.
	copied := make([]byte, indexRootLen)
	copy(copied, material)
	return indexKeys{root: &indexRoot{material: copied}}, nil
}

// derive returns the lookup key for one purpose.
func (k indexKeys) derive(purpose string) []byte {
	mac := hmac.New(sha256.New, k.root.material)
	mac.Write([]byte(indexContext + purpose))
	return mac.Sum(nil)
}

// lookup returns the hex lookup value of value under one purpose. An absent secret
// yields "", which every caller must treat as "no match" rather than as a value to
// query with.
func (k indexKeys) lookup(purpose string, value Secret) string {
	material := value.Reveal()
	if material == "" {
		return ""
	}
	mac := hmac.New(sha256.New, k.derive(purpose))
	mac.Write([]byte(material))
	return hex.EncodeToString(mac.Sum(nil))
}

// requireLookup is lookup with the empty case turned into a refusal, for the paths
// where an absent credential is a caller mistake rather than a miss.
func (k indexKeys) requireLookup(purpose string, value Secret) (string, error) {
	computed := k.lookup(purpose, value)
	if computed == "" {
		return "", fmt.Errorf("store: no %s material supplied: %w", purpose, ErrInvalidArgument)
	}
	return computed, nil
}
