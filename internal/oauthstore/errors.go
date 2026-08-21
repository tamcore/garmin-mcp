package oauthstore

import (
	"errors"
	"fmt"
	"slices"

	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Sentinel translation.
//
// The authorization server branches on its own sentinels and treats everything
// else as a storage failure it never shows a client. The store has its own set,
// which is nearly but not exactly the same set: it names an expired code and a
// replayed refresh token differently, and it has failures — a malformed argument,
// an incomplete cascade — that mean nothing at the protocol level.
//
// Every store error therefore leaves this package wrapped in the sentinel the
// consumer expects, in front of the store's own error rather than instead of it,
// so errors.Is finds the protocol meaning and a log still gets the cause. The
// store's messages name principals, clients and families, which are pseudonymous
// identifiers, and never token, code or capability material, which is only ever
// hashed before it reaches a row.

// translate maps a store error onto the sentinel the authorization server branches
// on. A nil error stays nil.
//
// The order of the cases is load-bearing where the store reports two sentinels at
// once: an expired code wraps both ErrCodeExpired and ErrCodeNotFound, and the
// expired case has to be recognized first so both survive the translation.
func translate(op string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrClientNotFound):
		return wrap(op, err, oauthserver.ErrUnknownClient)
	case errors.Is(err, store.ErrTransactionConflict):
		return wrap(op, err, oauthserver.ErrTransactionConflict)
	case errors.Is(err, store.ErrTransactionNotFound):
		return wrap(op, err, oauthserver.ErrTransactionNotFound)
	case errors.Is(err, store.ErrCodeExpired):
		return wrap(op, err, oauthserver.ErrCodeExpired, oauthserver.ErrCodeNotFound)
	case errors.Is(err, store.ErrCodeAlreadyUsed):
		return wrap(op, err, oauthserver.ErrCodeAlreadyUsed)
	case errors.Is(err, store.ErrCodeNotFound):
		return wrap(op, err, oauthserver.ErrCodeNotFound)
	case errors.Is(err, store.ErrRefreshTokenReuse):
		return wrap(op, err, oauthserver.ErrRefreshTokenReused)
	default:
		return translateTokenError(op, err)
	}
}

// translateTokenError is the rest of the table, split out so neither switch runs
// past the length one function may reasonably have.
func translateTokenError(op string, err error) error {
	switch {
	case errors.Is(err, store.ErrTokenRevoked):
		return wrap(op, err, oauthserver.ErrTokenRevoked)
	case errors.Is(err, store.ErrTokenExpired):
		return wrap(op, err, oauthserver.ErrTokenExpired)
	case errors.Is(err, store.ErrTokenNotFound):
		return wrap(op, err, oauthserver.ErrTokenNotFound)
	case errors.Is(err, store.ErrConsentNotFound):
		return wrap(op, err, oauthserver.ErrConsentNotFound)
	default:
		return wrap(op, err, oauthserver.ErrStorage)
	}
}

// wrap builds "oauthstore: <op>: <sentinel>...: <cause>", so errors.Is finds every
// sentinel and the cause.
func wrap(op string, cause error, sentinels ...error) error {
	wrapped := cause
	for _, sentinel := range slices.Backward(sentinels) {
		wrapped = fmt.Errorf("%w: %w", sentinel, wrapped)
	}
	return fmt.Errorf("oauthstore: %s: %w", op, wrapped)
}

// corrupt reports a stored record this package could not turn back into a
// validated value. It is a storage failure and it keeps the validation sentinel,
// so a log says which column was unreadable without the caller having to branch on
// it.
func corrupt(op string, cause error) error {
	return wrap(op, cause, oauthserver.ErrStorage)
}

// invalidLookup reports a lookup that addresses no record.
func invalidLookup(op string) error {
	return fmt.Errorf("oauthstore: %s: the zero lookup addresses no record: %w",
		op, oauthserver.ErrInvalidLookup)
}

// inconsistent reports two records that disagree about a field the store writes
// once for both. The message names the field and nothing else.
func inconsistent(op, field string) error {
	return wrap(op, fmt.Errorf("the access and refresh records disagree about the %s", field),
		oauthserver.ErrStorage)
}
