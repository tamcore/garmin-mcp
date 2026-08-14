package oauthserver

import (
	"fmt"
	"time"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// The records in this file are the persistence shapes. Unlike the validated value
// types elsewhere in the package, their fields are exported: a storage adapter has
// to be able to build one from a row, and take one apart into columns, without
// this package granting it a special constructor.
//
// That is safe because every field that carries meaning is itself a validated or
// redacting type. A record cannot hold an unvalidated redirect URI, and printing a
// record cannot reveal a state, a challenge or a token, because Lookup is a digest
// and ClientState and CodeChallenge redact themselves.

// A TransactionStage is how far an authorization transaction has got. The stages
// are ordered and a transaction only ever moves forward.
type TransactionStage int

// The transaction stages.
const (
	// StagePending is a validated authorization request with no principal yet. The
	// browser has been given the capability and nothing else has happened.
	StagePending TransactionStage = iota + 1
	// StageAuthenticated means the Garmin login completed and the principal is
	// resolved, but the user has not yet consented for this client.
	StageAuthenticated
)

// String renders the stage for a log line.
func (s TransactionStage) String() string {
	switch s {
	case StagePending:
		return "pending"
	case StageAuthenticated:
		return "authenticated"
	default:
		return "unknown"
	}
}

// IsValid reports whether s is a defined stage.
func (s TransactionStage) IsValid() bool {
	return s == StagePending || s == StageAuthenticated
}

// A Transaction is the server-owned state that connects a validated authorization
// request to a completed Garmin login.
//
// It is the only place the authorization request parameters live between the
// authorization endpoint and the issuing of a code, and it is addressed by the
// digest of a capability held in a browser cookie. Nothing in it is client-owned
// except State, which is echoed back untouched.
//
// Version carries the compare-and-set token. A caller that advances a transaction
// passes the version it read, and the storage layer refuses the write if anything
// else advanced it first.
type Transaction struct {
	// Lookup is the digest of the transaction capability. The capability itself is
	// never stored.
	Lookup Lookup
	// ClientID is the validated client this transaction belongs to.
	ClientID string
	// RedirectURI is the exact registered redirect URI, already matched.
	RedirectURI RedirectURI
	// Scopes is the requested scope set, already narrowed to what the client may
	// ask for.
	Scopes ScopeSet
	// Resource is the requested RFC 8707 resource indicator.
	Resource Resource
	// Challenge is the PKCE S256 challenge the eventual code is bound to.
	Challenge CodeChallenge
	// State is the client's opaque state, byte for byte.
	State ClientState
	// Stage is how far the transaction has got.
	Stage TransactionStage
	// Principal is the resolved principal, zero until the Garmin login completes.
	Principal identity.Principal
	// CreatedAt and ExpiresAt bound the transaction's life.
	CreatedAt time.Time
	ExpiresAt time.Time
	// Version is the compare-and-set version. A freshly created transaction is
	// version 0.
	Version uint64
}

// IsExpired reports whether the transaction is past its expiry at now.
func (t Transaction) IsExpired(now time.Time) bool {
	return !now.Before(t.ExpiresAt)
}

// String reports the transaction without its state or its challenge.
func (t Transaction) String() string {
	return fmt.Sprintf("oauthserver.Transaction{client:%s stage:%s principal:%s expires:%s}",
		t.ClientID, t.Stage, principalLabel(t.Principal), t.ExpiresAt.UTC().Format(time.RFC3339))
}

// GoString satisfies the %#v verb with the same rendering.
func (t Transaction) GoString() string { return t.String() }

// principalLabel renders a principal for a log line, or "unresolved" for the zero
// value. The identifier is pseudonymous and the redaction rules permit it.
func principalLabel(principal identity.Principal) string {
	if !principal.IsValid() {
		return "unresolved"
	}
	return principal.ID()
}

// An AuthorizationCode is a single-use code, addressed by its digest, with every
// binding the token endpoint has to verify.
//
// The bindings are the whole point of the record: a code that leaks is useless
// without the client id, the exact redirect URI and the PKCE verifier it was
// issued against, and it can never be redeemed for a different resource, a wider
// scope or another principal.
type AuthorizationCode struct {
	Lookup      Lookup
	ClientID    string
	RedirectURI RedirectURI
	Scopes      ScopeSet
	Resource    Resource
	Challenge   CodeChallenge
	Principal   identity.Principal
	IssuedAt    time.Time
	ExpiresAt   time.Time
}

// IsExpired reports whether the code is past its expiry at now.
func (c AuthorizationCode) IsExpired(now time.Time) bool {
	return !now.Before(c.ExpiresAt)
}

// String reports the record without anything that identifies the code.
func (c AuthorizationCode) String() string {
	return fmt.Sprintf("oauthserver.AuthorizationCode{client:%s principal:%s expires:%s}",
		c.ClientID, principalLabel(c.Principal), c.ExpiresAt.UTC().Format(time.RFC3339))
}

// GoString satisfies the %#v verb with the same rendering.
func (c AuthorizationCode) GoString() string { return c.String() }

// A FamilyID names one refresh token family: the chain of rotations descending
// from a single authorization. It is an opaque random identifier rather than a
// secret, because it is only ever a key for revocation.
type FamilyID string

// NewFamilyID returns a fresh family identifier with 256 bits of entropy.
func NewFamilyID() (FamilyID, error) {
	secret, err := NewSecret()
	if err != nil {
		return "", fmt.Errorf("generating a token family id: %w", err)
	}
	return FamilyID(secret.Reveal()), nil
}

// An AccessToken is the stored form of an issued access token: a digest plus the
// grant it was minted from. The token itself is never stored.
type AccessToken struct {
	Lookup    Lookup
	ClientID  string
	Principal identity.Principal
	Scopes    ScopeSet
	Resource  Resource
	Family    FamilyID
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// IsExpired reports whether the token is past its expiry at now.
func (t AccessToken) IsExpired(now time.Time) bool {
	return !now.Before(t.ExpiresAt)
}

// String reports the record without anything that identifies the token. The family
// is omitted too, because it is the revocation key for every token in the chain.
func (t AccessToken) String() string {
	return fmt.Sprintf("oauthserver.AccessToken{client:%s principal:%s scopes:%d expires:%s}",
		t.ClientID, principalLabel(t.Principal), t.Scopes.Len(),
		t.ExpiresAt.UTC().Format(time.RFC3339))
}

// GoString satisfies the %#v verb with the same rendering.
func (t AccessToken) GoString() string { return t.String() }

// A RefreshToken is the stored form of an issued refresh token. Generation counts
// rotations within the family, so an audit record can say how deep a chain got
// without holding any token.
type RefreshToken struct {
	Lookup     Lookup
	ClientID   string
	Principal  identity.Principal
	Scopes     ScopeSet
	Resource   Resource
	Family     FamilyID
	Generation uint64
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

// IsExpired reports whether the token is past its expiry at now.
func (t RefreshToken) IsExpired(now time.Time) bool {
	return !now.Before(t.ExpiresAt)
}

// String reports the record without anything that identifies the token.
func (t RefreshToken) String() string {
	return fmt.Sprintf(
		"oauthserver.RefreshToken{client:%s principal:%s generation:%d scopes:%d expires:%s}",
		t.ClientID, principalLabel(t.Principal), t.Generation, t.Scopes.Len(),
		t.ExpiresAt.UTC().Format(time.RFC3339))
}

// GoString satisfies the %#v verb with the same rendering.
func (t RefreshToken) GoString() string { return t.String() }

// A ConsentKey is the tuple consent is bound to.
//
// It is deliberately the tuple the MCP security guidance names for the
// confused-deputy risk: principal, client id, exact redirect URI and resource. A
// change to any of them is a different key, so it finds no record and forces a
// fresh decision. Scope is not part of the key — it is the value — because scope
// has to be compared for containment rather than for equality.
//
// ConsentKey is comparable, so it works directly as a map key.
type ConsentKey struct {
	Principal   identity.Principal
	ClientID    string
	RedirectURI RedirectURI
	Resource    Resource
}

// A Consent is a persisted grant: the tuple, the scopes the user actually agreed
// to, and when.
type Consent struct {
	Key       ConsentKey
	Scopes    ScopeSet
	GrantedAt time.Time
}

// Covers reports whether this consent authorizes the requested scopes. Containment
// rather than equality is what makes a narrower later request pass and a wider one
// fail.
func (c Consent) Covers(requested ScopeSet) bool {
	return requested.IsSubsetOf(c.Scopes)
}

// String reports the consent without any credential material.
func (c Consent) String() string {
	return fmt.Sprintf("oauthserver.Consent{client:%s principal:%s scopes:%s granted:%s}",
		c.Key.ClientID, principalLabel(c.Key.Principal), c.Scopes,
		c.GrantedAt.UTC().Format(time.RFC3339))
}

// GoString satisfies the %#v verb with the same rendering.
func (c Consent) GoString() string { return c.String() }
