package oauthserver

import (
	"context"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// The interfaces in this file are consumer-side: they say what this package needs,
// not what any particular database offers. The SQLite implementation lives
// elsewhere and is adapted to these signatures, so nothing in the OAuth logic
// depends on a storage library.
//
// Two promises are load-bearing and cannot be weakened by an implementation
// without breaking the security properties this package claims:
//
//   - ConsumeCode is atomic. Two concurrent redemptions of one code must produce
//     exactly one success and one ErrCodeAlreadyUsed.
//   - RotateRefreshToken is atomic, and it revokes the family in the same
//     transaction in which it detects reuse. A caller that receives
//     ErrRefreshTokenReused may assume the family is already dead.
//
// UpdateTransaction carries the third: it is a compare-and-set, and it must fail
// with ErrTransactionConflict rather than overwrite a concurrently advanced row.
//
// Every method returns a sentinel from errors.go where this package has to branch
// on the outcome. Any other error is treated as a storage failure, is never shown
// to a client, and never results in a token.

// A ClientStore reads operator-registered clients. There is no write side: this
// server does not offer anonymous dynamic client registration, so a client exists
// only because an operator put it in configuration.
type ClientStore interface {
	// Client returns the registration for clientID, or an error wrapping
	// ErrUnknownClient if there is none.
	Client(ctx context.Context, clientID string) (Client, error)
}

// A TransactionStore persists authorization transactions, addressed by the digest
// of their capability.
type TransactionStore interface {
	// CreateTransaction stores a new transaction at version 0. A digest that
	// already exists is a collision and must fail rather than overwrite.
	CreateTransaction(ctx context.Context, tx Transaction) error

	// Transaction returns the transaction with the given capability digest, or an
	// error wrapping ErrTransactionNotFound.
	Transaction(ctx context.Context, lookup Lookup) (Transaction, error)

	// UpdateTransaction replaces the stored transaction only if its version is
	// still expectVersion, and increments the stored version. It returns an error
	// wrapping ErrTransactionConflict when the versions differ, and one wrapping
	// ErrTransactionNotFound when the row is gone.
	UpdateTransaction(ctx context.Context, tx Transaction, expectVersion uint64) error

	// ConsumeTransaction atomically returns the transaction and deletes it.
	//
	// It is the claim that makes completing an authorization single-use. A
	// compare-and-set on the version is not sufficient on its own: two concurrent
	// submissions can serialize so that both win their own compare-and-set, one
	// after the other. Exactly one caller must receive the record, and every other
	// must receive an error wrapping ErrTransactionNotFound.
	//
	// It is also how an expired transaction is discarded, in which case the caller
	// ignores the returned record.
	ConsumeTransaction(ctx context.Context, lookup Lookup) (Transaction, error)
}

// A ConsentStore persists per-principal, per-client consent and revokes it
// transactionally.
type ConsentStore interface {
	// Consent returns the consent for the exact tuple, or an error wrapping
	// ErrConsentNotFound.
	Consent(ctx context.Context, key ConsentKey) (Consent, error)

	// SaveConsent stores or replaces the consent for its tuple.
	SaveConsent(ctx context.Context, consent Consent) error

	// RevokeConsent deletes the consent and, in the same transaction, revokes every
	// token family issued for that principal, client and resource. It is
	// idempotent, and it must fail closed: a partial deletion returns an error
	// rather than reporting success.
	RevokeConsent(ctx context.Context, key ConsentKey) error
}

// A CodeStore persists single-use authorization codes by digest.
type CodeStore interface {
	// SaveCode stores a freshly issued code.
	SaveCode(ctx context.Context, code AuthorizationCode) error

	// ConsumeCode atomically marks the code used and returns it. A second call for
	// the same digest must return an error wrapping ErrCodeAlreadyUsed, and an
	// unknown digest one wrapping ErrCodeNotFound. Expiry is the caller's business;
	// atomicity is the implementation's.
	ConsumeCode(ctx context.Context, lookup Lookup) (AuthorizationCode, error)
}

// A TokenStore persists hashed token material, token families and revocation
// state.
type TokenStore interface {
	// SaveTokenPair stores the first access and refresh token of a new family.
	SaveTokenPair(ctx context.Context, access AccessToken, refresh RefreshToken) error

	// AccessToken returns the record for a presented access token digest. It
	// returns an error wrapping ErrTokenNotFound when there is none, and one
	// wrapping ErrTokenRevoked when the token's family has been revoked.
	AccessToken(ctx context.Context, lookup Lookup) (AccessToken, error)

	// RefreshToken returns the record for a presented refresh token digest. It returns
	// an error wrapping ErrTokenNotFound when there is none, and one wrapping
	// ErrTokenRevoked when the token's family has been revoked.
	//
	// It deliberately does not judge whether the token has already been rotated away.
	// That is RotateRefreshToken's business, because detecting a replay is only useful
	// if the family dies in the same transaction that detects it; a read that refused
	// early would report the replay and leave the family alive.
	RefreshToken(ctx context.Context, lookup Lookup) (RefreshToken, error)

	// RotateRefreshToken consumes the presented refresh token and stores its
	// replacement pair, atomically.
	//
	// If the presented token is already consumed, or its family is revoked, the
	// implementation must revoke the whole family inside the same transaction and
	// return an error wrapping ErrRefreshTokenReused, having stored nothing. That is
	// the reuse detection the rotation scheme depends on: detection and revocation
	// cannot be two steps, or a racing attacker slips between them.
	RotateRefreshToken(
		ctx context.Context, presented Lookup, access AccessToken, refresh RefreshToken,
	) error

	// RevokeFamily revokes every token in the family. It is idempotent. reason
	// says why, so the storage adapter can record the same audit-trail
	// vocabulary the transactional reuse path already uses instead of a single
	// reason code covering every caller of this method.
	RevokeFamily(ctx context.Context, family FamilyID, reason RevokeReason) error

	// RevokePrincipal revokes every token family belonging to the principal and
	// deletes the principal's consents. It is the token half of unlinking a Garmin
	// account, it is idempotent, and it must fail closed on partial deletion.
	RevokePrincipal(ctx context.Context, principal identity.Principal) error
}

// A Store is everything this package needs from persistence. An adapter over the
// SQLite store implements it; the tests use an in-memory fake. Nothing in the
// package holds a narrower or a wider dependency, so there is exactly one seam.
type Store interface {
	ClientStore
	TransactionStore
	ConsentStore
	CodeStore
	TokenStore
}
