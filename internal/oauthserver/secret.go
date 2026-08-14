package oauthserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
)

// SecretBytes is the raw length of every credential this package generates: an
// authorization code, an access token, a refresh token and a transaction
// capability. 32 bytes is 256 bits, the floor the security brief sets.
const SecretBytes = 32

// A Lookup is the SHA-256 digest of a [Secret]. It is the only form of a
// credential that is ever persisted or compared, so a stolen database yields no
// usable credential: an attacker would have to find the preimage of a 256-bit
// random value.
//
// Lookup is a comparable array type, so it works directly as a map key.
type Lookup [sha256.Size]byte

// Hex renders the lookup value as lower-case hexadecimal, the form a storage
// adapter persists.
func (l Lookup) Hex() string { return hex.EncodeToString(l[:]) }

// ParseLookup parses the form Hex produces. It exists for the storage adapter,
// which reads lookup values back out of a database column.
func ParseLookup(encoded string) (Lookup, error) {
	if len(encoded) != sha256.Size*2 {
		return Lookup{}, fmt.Errorf("lookup value is %d characters, want %d: %w",
			len(encoded), sha256.Size*2, ErrInvalidLookup)
	}
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return Lookup{}, fmt.Errorf("lookup value is not hexadecimal: %w", ErrInvalidLookup)
	}
	var lookup Lookup
	copy(lookup[:], raw)
	return lookup, nil
}

// IsZero reports whether l is the zero Lookup, which no credential hashes to.
func (l Lookup) IsZero() bool { return l == Lookup{} }

// Equal compares two lookup values in constant time. Lookup values are digests
// rather than credentials, so this is belt and braces; it costs nothing, and it
// means no comparison in this package leaks timing.
func (l Lookup) Equal(other Lookup) bool {
	return subtle.ConstantTimeCompare(l[:], other[:]) == 1
}

// secretString is the credential under its own type, so the field that holds it
// can be a pointer.
type secretString string

// secretMaterial holds the credential behind a second pointer, which is not
// belt-and-braces but load-bearing: fmt's %s and %q on a value with no String
// method fall into badVerb, and badVerb re-prints the value at depth zero, where
// a pointer to a struct *is* dereferenced and its unexported fields printed. A
// plain string field would surface there; a pointer field renders as an address.
// internal/cryptostore.Key uses the same two-level shape for the same reason.
type secretMaterial struct {
	value *secretString
}

// newSecret wraps a credential in the indirection every Secret uses.
func newSecret(value string) Secret {
	held := secretString(value)
	return Secret{m: &secretMaterial{value: &held}}
}

// A Secret is a high-entropy opaque credential: an authorization code, an access
// token, a refresh token, a transaction capability or a presented client secret.
//
// The zero Secret is the absence of a credential and reports so through IsZero.
// Reveal is the only way to read the value, and it has exactly two legitimate
// callers: the response that hands a freshly issued credential to its owner, and
// the digest computation in Lookup. Every rendering path — String, GoString,
// MarshalJSON and LogValue — is redacted.
type Secret struct {
	m *secretMaterial
}

// NewSecret returns a fresh credential with SecretBytes of crypto/rand entropy,
// encoded as unpadded base64url so it is safe in a URL, a header and a form body.
func NewSecret() (Secret, error) {
	raw := make([]byte, SecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return Secret{}, fmt.Errorf("generating %d random bytes: %w", SecretBytes, err)
	}
	return newSecret(base64.RawURLEncoding.EncodeToString(raw)), nil
}

// SecretFromString wraps a credential presented by a caller, so an inbound token
// or code is redacted from the moment it is parsed. An empty string yields the
// zero Secret rather than a Secret holding "".
func SecretFromString(presented string) Secret {
	if presented == "" {
		return Secret{}
	}
	return newSecret(presented)
}

// Reveal returns the credential, or the empty string for the zero Secret.
func (s Secret) Reveal() string {
	if s.m == nil || s.m.value == nil {
		return ""
	}
	return string(*s.m.value)
}

// IsZero reports whether s holds no credential.
func (s Secret) IsZero() bool { return s.Reveal() == "" }

// Lookup returns the persistable digest of s. The zero Secret hashes to the zero
// Lookup, so an absent credential can never match a stored record.
func (s Secret) Lookup() Lookup {
	if s.IsZero() {
		return Lookup{}
	}
	return sha256.Sum256([]byte(s.Reveal()))
}

// redactedSecret is the only shape a Secret is ever rendered or serialized in.
type redactedSecret struct {
	Type    string `json:"type"`
	Present bool   `json:"present"`
	Length  int    `json:"length"`
}

func (s Secret) redacted() redactedSecret {
	return redactedSecret{Type: "oauthserver.Secret", Present: !s.IsZero(), Length: len(s.Reveal())}
}

// String reports the presence and length of the credential, never its value.
func (s Secret) String() string {
	red := s.redacted()
	return "oauthserver.Secret{material:" + presence(red.Present) +
		" length:" + strconv.Itoa(red.Length) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (s Secret) GoString() string { return s.String() }

// MarshalJSON serializes the redacted form, so a Secret embedded in a response
// body or an audit record cannot leak.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.redacted())
}

// LogValue implements slog.LogValuer, so structured logging is safe by default.
func (s Secret) LogValue() slog.Value {
	red := s.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.Bool("present", red.Present),
		slog.Int("length", red.Length),
	)
}

func presence(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}
