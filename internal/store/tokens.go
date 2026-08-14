// Package store persists the Garmin DI token set for a principal.
//
// The shipped implementation is FileStore: one AEAD-encrypted file per principal
// under an owner-only directory, with compare-and-set versioning so a rotated
// refresh token cannot be lost. Encryption comes from internal/cryptostore, and
// every record is bound to its principal and to its record type, so a record
// cannot be replayed under another principal.
//
// # What deleting tokens does and does not do
//
// Delete unlinks the local record. It does NOT revoke anything at Garmin: the DI
// refresh token stays valid at Garmin's service until Garmin expires or revokes
// it, and anyone who already copied the file keeps working access. Remote
// revocation is a Garmin-side operation this package cannot perform, so a caller
// must never report "tokens revoked" after a Delete. It may report "local tokens
// removed".
//
// # Compatibility
//
// The 0.3.x garmin_tokens.json format can be imported and exported (compat.go).
// Inline token JSON is an explicitly insecure override, refused unless a caller
// opts in (inline.go).
//
// # Adapting to internal/garmin/auth
//
// FileStore's method set is exactly auth.TokenStore's, but with this package's
// TokenSet and sentinels. The two TokenSet types have identical constructors and
// accessors, so the adapter is mechanical and belongs in the wiring layer, which
// keeps this package free of a dependency on the auth package:
//
//	type authTokenStore struct{ inner *store.FileStore }
//
//	func (a authTokenStore) Load(ctx context.Context, principal string,
//	) (auth.TokenSet, int64, error) {
//	    set, version, err := a.inner.Load(ctx, principal)
//	    if err != nil {
//	        if errors.Is(err, store.ErrNoTokens) {
//	            return auth.TokenSet{}, 0, fmt.Errorf("%w: %w", auth.ErrNoTokens, err)
//	        }
//	        return auth.TokenSet{}, 0, err
//	    }
//	    return auth.NewTokenSet(set.Token(), set.RefreshToken(), set.ClientID(), set.ExpiresAt()), version, nil
//	}
//
//	func (a authTokenStore) Save(ctx context.Context, principal string,
//	    set auth.TokenSet, expected int64) (int64, error) {
//	    version, err := a.inner.Save(ctx, principal,
//	        store.NewTokenSet(set.Token(), set.RefreshToken(), set.ClientID(), set.ExpiresAt()), expected)
//	    if errors.Is(err, store.ErrVersionConflict) {
//	        return 0, fmt.Errorf("%w: %w", auth.ErrVersionConflict, err)
//	    }
//	    return version, err
//	}
//
//	func (a authTokenStore) Delete(ctx context.Context, principal string) error {
//	    return a.inner.Delete(ctx, principal)
//	}
//
// Wrapping keeps both sentinel chains intact, so errors.Is works against either
// package's sentinel.
package store

import "time"

// TokenSet is the Garmin DI token set for one principal.
//
// Field mapping to the 0.3.x wire names, and to the plain-struct equivalent in
// internal/garmin/auth:
//
//	Token()        di_token
//	RefreshToken() di_refresh_token
//	ClientID()     di_client_id
//	ExpiresAt()    the unverified exp claim of the DI token
//
// ExpiresAt is scheduling metadata only. It comes from a JWT this process never
// verifies, so it may be wrong or hostile: use it to decide when to refresh, never
// to authorize anything.
//
// A TokenSet is secret-bearing. Its material sits behind two levels of unexported
// indirection, so a reflective logger, a direct field print and a method-stripping
// alias (type Raw store.TokenSet) all see an address rather than the tokens. String,
// GoString, MarshalJSON and LogValue report presence, not content.
//
// A TokenSet is immutable: the With* methods return a copy. The zero value is
// inert and reports IsZero.
type TokenSet struct {
	// parts is a pointer on purpose. fmt follows a pointer only at the top level,
	// so a nested unexported pointer renders as an address, whereas a nested
	// unexported struct renders its field values.
	parts *tokenParts
}

// secret is one secret-bearing value under its own type, so the field that holds it
// can be a pointer.
type secret string

// tokenParts holds the material. It is copied, never mutated.
//
// Every string field is a pointer, not a plain string. fmt's %s and %q on a value
// with no String method fall into badVerb, which re-prints the value at depth zero,
// and depth zero dereferences a pointer to a struct and prints its unexported
// fields — the token verbatim. A pointer field renders as an address there instead.
// The client id is held the same way: it arrives from an imported file and is
// therefore unverified text, not a value worth printing raw.
type tokenParts struct {
	token        *secret
	refreshToken *secret
	clientID     *secret
	expiresAt    time.Time
}

// heldSecret wraps value so it can be stored behind a pointer.
func heldSecret(value string) *secret {
	held := secret(value)
	return &held
}

// secretValue reads a held value, reporting "" for an absent one.
func secretValue(held *secret) string {
	if held == nil {
		return ""
	}
	return string(*held)
}

// NewTokenSet builds a TokenSet. expiresAt is the unverified exp claim; pass the
// zero time when it is unknown.
func NewTokenSet(token, refreshToken, clientID string, expiresAt time.Time) TokenSet {
	return TokenSet{parts: &tokenParts{
		token:        heldSecret(token),
		refreshToken: heldSecret(refreshToken),
		clientID:     heldSecret(clientID),
		expiresAt:    expiresAt,
	}}
}

// Token returns the di_token, or "" for the zero TokenSet.
func (s TokenSet) Token() string {
	if s.parts == nil {
		return ""
	}
	return secretValue(s.parts.token)
}

// RefreshToken returns the di_refresh_token, or "" for the zero TokenSet. This is
// the long-lived credential: it grants persistent account access.
func (s TokenSet) RefreshToken() string {
	if s.parts == nil {
		return ""
	}
	return secretValue(s.parts.refreshToken)
}

// ClientID returns the di_client_id, or "" for the zero TokenSet. It names the
// Garmin OAuth client and is not a secret.
func (s TokenSet) ClientID() string {
	if s.parts == nil {
		return ""
	}
	return secretValue(s.parts.clientID)
}

// ExpiresAt returns the unverified exp claim as scheduling metadata, or the zero
// time when unknown. It must never be used to authorize a caller.
func (s TokenSet) ExpiresAt() time.Time {
	if s.parts == nil {
		return time.Time{}
	}
	return s.parts.expiresAt
}

// IsZero reports whether the TokenSet carries nothing.
func (s TokenSet) IsZero() bool { return s.parts == nil }

// WithToken returns a copy whose di_token is token. The receiver is unchanged, so
// a refreshed token never mutates a value another goroutine holds.
func (s TokenSet) WithToken(token string) TokenSet {
	return s.with(func(parts *tokenParts) { parts.token = heldSecret(token) })
}

// WithRefreshToken returns a copy whose di_refresh_token is refreshToken. Garmin
// rotates the refresh token, and the rotated value must be persisted.
func (s TokenSet) WithRefreshToken(refreshToken string) TokenSet {
	return s.with(func(parts *tokenParts) { parts.refreshToken = heldSecret(refreshToken) })
}

// WithExpiresAt returns a copy whose scheduling expiry is expiresAt.
func (s TokenSet) WithExpiresAt(expiresAt time.Time) TokenSet {
	return s.with(func(parts *tokenParts) { parts.expiresAt = expiresAt })
}

// with copies the parts, applies change to the copy and returns the new value.
func (s TokenSet) with(change func(*tokenParts)) TokenSet {
	copied := tokenParts{}
	if s.parts != nil {
		copied = *s.parts
	}
	change(&copied)
	return TokenSet{parts: &copied}
}
