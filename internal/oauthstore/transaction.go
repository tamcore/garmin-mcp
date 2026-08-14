package oauthstore

import (
	"context"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Authorization transactions.
//
// Three of the four operations map to one store call each. The fourth, the
// compare-and-set, maps to the store's own: it advances only the columns a
// transaction moves through, because the request-defining columns are not state the
// flow advances, and a write that changed them would be a different transaction
// wearing the same handle.

// CreateTransaction stores a new transaction at version 0. A digest that already
// exists is a collision and is refused rather than overwritten.
func (a *Adapter) CreateTransaction(ctx context.Context, transaction oauthserver.Transaction) error {
	const op = "create authorization transaction"
	handle, err := material(op, transaction.Lookup)
	if err != nil {
		return err
	}
	return translate(op, a.sqlite.PutAuthTransaction(ctx, store.AuthTransactionDraft{
		Handle:        handle,
		ClientID:      transaction.ClientID,
		RedirectURI:   transaction.RedirectURI.String(),
		Scopes:        transaction.Scopes.Strings(),
		Resource:      transaction.Resource.String(),
		ClientState:   store.NewSecret(transaction.State.Reveal()),
		CodeChallenge: transaction.Challenge.Value(),
		CreatedAt:     transaction.CreatedAt,
		ExpiresAt:     transaction.ExpiresAt,
	}))
}

// Transaction returns the live transaction with the given capability digest. An
// expired transaction is indistinguishable from one that never existed, which is
// the store's rule and the interface's.
func (a *Adapter) Transaction(ctx context.Context, lookup oauthserver.Lookup,
) (oauthserver.Transaction, error) {
	const op = "read authorization transaction"
	handle, err := material(op, lookup)
	if err != nil {
		return oauthserver.Transaction{}, err
	}
	stored, err := a.sqlite.AuthTransaction(ctx, handle)
	if err != nil {
		return oauthserver.Transaction{}, translate(op, err)
	}
	return transactionFrom(op, lookup, stored)
}

// UpdateTransaction writes the transaction back only if it is still at
// expectVersion. The new version is the store's business: the interface does not
// return it, and a caller that needs it reads the record back.
func (a *Adapter) UpdateTransaction(ctx context.Context, transaction oauthserver.Transaction,
	expectVersion uint64,
) error {
	const op = "update authorization transaction"
	handle, err := material(op, transaction.Lookup)
	if err != nil {
		return err
	}
	_, err = a.sqlite.UpdateAuthTransaction(ctx, store.AuthTransactionUpdate{
		Handle:      handle,
		PrincipalID: principalIDOf(transaction.Principal),
		Scopes:      transaction.Scopes.Strings(),
		Resource:    transaction.Resource.String(),
		ClientState: store.NewSecret(transaction.State.Reveal()),
	}, expectVersion)
	return translate(op, err)
}

// ConsumeTransaction returns the transaction and deletes it, atomically.
//
// Exactly one of any number of concurrent callers receives the record; every other
// receives ErrTransactionNotFound. An expired transaction comes back too, and is
// deleted, because discarding it is the point of the call.
func (a *Adapter) ConsumeTransaction(ctx context.Context, lookup oauthserver.Lookup,
) (oauthserver.Transaction, error) {
	const op = "consume authorization transaction"
	handle, err := material(op, lookup)
	if err != nil {
		return oauthserver.Transaction{}, err
	}
	stored, err := a.sqlite.ConsumeAuthTransaction(ctx, handle)
	if err != nil {
		return oauthserver.Transaction{}, translate(op, err)
	}
	return transactionFrom(op, lookup, stored)
}

// transactionFrom rebuilds a validated transaction from a row, re-attaching the
// digest the store does not keep and deriving the stage the schema has no column
// for.
func transactionFrom(op string, lookup oauthserver.Lookup, stored store.AuthTransaction,
) (oauthserver.Transaction, error) {
	parts, err := readTransactionParts(op, stored)
	if err != nil {
		return oauthserver.Transaction{}, err
	}
	state, err := oauthserver.ParseClientState(stored.ClientState.Reveal())
	if err != nil {
		return oauthserver.Transaction{}, corrupt(op, err)
	}
	return oauthserver.Transaction{
		Lookup:      lookup,
		ClientID:    stored.ClientID,
		RedirectURI: parts.redirect,
		Scopes:      parts.scopes,
		Resource:    parts.resource,
		Challenge:   parts.challenge,
		State:       state,
		Stage:       stageOf(stored.PrincipalID),
		Principal:   parts.principal,
		CreatedAt:   stored.CreatedAt,
		ExpiresAt:   stored.ExpiresAt,
		Version:     stored.Version,
	}, nil
}

// transactionParts holds the values that have to be re-validated on the way out of
// a row. It exists so transactionFrom stays one readable expression.
type transactionParts struct {
	redirect  oauthserver.RedirectURI
	scopes    oauthserver.ScopeSet
	resource  oauthserver.Resource
	challenge oauthserver.CodeChallenge
	principal identity.Principal
}

// readTransactionParts re-validates every column a transaction carries.
func readTransactionParts(op string, stored store.AuthTransaction) (transactionParts, error) {
	redirect, err := redirectOf(op, stored.RedirectURI)
	if err != nil {
		return transactionParts{}, err
	}
	scopes, err := scopesOf(op, stored.Scopes)
	if err != nil {
		return transactionParts{}, err
	}
	resource, err := resourceOf(op, stored.Resource)
	if err != nil {
		return transactionParts{}, err
	}
	challenge, err := challengeOf(op, stored.CodeChallenge)
	if err != nil {
		return transactionParts{}, err
	}
	holder, err := principalOf(op, stored.PrincipalID)
	if err != nil {
		return transactionParts{}, err
	}
	return transactionParts{
		redirect:  redirect,
		scopes:    scopes,
		resource:  resource,
		challenge: challenge,
		principal: holder,
	}, nil
}
