package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

// Advancing and consuming an authorization transaction.
//
// Two operations that DeleteAuthTransaction and AttachPrincipal cannot express:
//
//   - A compare-and-set, so a caller that read a transaction can write it back only
//     if nothing advanced it in between. Without it a concurrent submission silently
//     overwrites the one that raced it.
//   - An atomic read-and-delete, so completing an authorization is single-use. A
//     compare-and-set is not enough on its own: two submissions can serialize so that
//     each wins its own compare-and-set, one after the other, and both proceed. Only
//     a delete with a one-row requirement elects exactly one winner.

// clientStateRecordType is the record type bound into the AEAD additional data of a
// sealed client state, so the envelope cannot be replayed as any other kind of record.
const clientStateRecordType = "oauth_client_state"

// sealClientState seals the client's opaque state, bound to the transaction it belongs
// to. The binding is the handle's lookup value — a keyed HMAC, not the handle — so an
// envelope lifted out of one row cannot be pasted into another.
func (s *SQLiteStore) sealClientState(handleHash string, state Secret,
) ([]byte, sql.NullInt64, error) {
	if state.IsZero() {
		return nil, sql.NullInt64{}, nil
	}
	version, err := keyVersionOf(s.key)
	if err != nil {
		return nil, sql.NullInt64{}, err
	}
	sealed, err := cryptostore.Encrypt(s.key, handleHash, clientStateRecordType,
		[]byte(state.Reveal()))
	if err != nil {
		return nil, sql.NullInt64{}, fmt.Errorf("store: seal client state: %w", err)
	}
	return sealed, sql.NullInt64{Int64: int64(version), Valid: true}, nil
}

// openClientState opens a sealed client state. An absent envelope is a request that
// carried no state, which is the zero Secret rather than an error.
func (s *SQLiteStore) openClientState(handleHash string, sealed []byte) (Secret, error) {
	if len(sealed) == 0 {
		return Secret{}, nil
	}
	opened, err := cryptostore.Decrypt(s.key, handleHash, clientStateRecordType, sealed)
	if err != nil {
		// The cause names versions and sizes only, never material.
		return Secret{}, fmt.Errorf("store: open client state: %w: %w", ErrCorruptRecord, err)
	}
	return NewSecret(string(opened)), nil
}

// AuthTransactionUpdate is the input to UpdateAuthTransaction. It carries only the
// columns a transaction moves through: the resolved principal, the scope set, the
// resource, and the client's state.
//
// The request-defining columns — the client, the redirect URI, the PKCE challenge and
// the expiry — are deliberately absent. They are what the authorization request was,
// not state the flow advances, and a write that changed them would be a different
// transaction wearing the same handle.
type AuthTransactionUpdate struct {
	// Handle is the opaque value the browser carries. It is hashed, never stored.
	Handle Secret

	// PrincipalID is the resolved principal. An empty value clears it, which is what
	// a transaction that has not yet identified a user carries.
	PrincipalID string

	// Scopes may be empty: a request may end up asking for nothing.
	Scopes []string

	// Resource is the RFC 8707 resource indicator, empty when there is none.
	Resource string

	// ClientState is the opaque client state. The zero Secret stores no state.
	ClientState Secret
}

// UpdateAuthTransaction writes a transaction back only if it is still at expectVersion,
// and advances the stored version by one. It returns the new version.
//
// It reports ErrTransactionConflict when the row is there but has moved on, and
// ErrTransactionNotFound when the row is gone. The two are distinguished by asking
// whether the row still exists after the update matched nothing, inside the same
// transaction: one UPDATE ... WHERE handle_hash = ? AND version = ? cannot tell them
// apart on its own, because both produce zero affected rows.
//
// Expiry is not part of the predicate. An expired transaction may still be advanced,
// which changes nothing that can be used: every other read applies its own expiry test,
// so the advanced row stays as unreadable as it was.
func (s *SQLiteStore) UpdateAuthTransaction(ctx context.Context, update AuthTransactionUpdate,
	expectVersion uint64,
) (uint64, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return 0, err
	}
	prepared, err := s.prepareUpdate(update)
	if err != nil {
		return 0, err
	}

	next := expectVersion + 1
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		return s.applyTransactionUpdate(ctx, tx, update, prepared, expectVersion, next)
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// preparedUpdate is a validated update with its lookup value and its sealed state.
type preparedUpdate struct {
	hash            string
	scopes          string
	principalID     sql.NullString
	sealedState     []byte
	stateKeyVersion sql.NullInt64
}

// prepareUpdate validates an update, hashes its handle and seals its client state.
func (s *SQLiteStore) prepareUpdate(update AuthTransactionUpdate) (preparedUpdate, error) {
	if update.PrincipalID != "" {
		if err := checkIdentifier("principal id", update.PrincipalID); err != nil {
			return preparedUpdate{}, err
		}
	}
	if err := checkLocator("resource", update.Resource); err != nil {
		return preparedUpdate{}, err
	}
	scopes, err := encodeOptionalScopes(update.Scopes)
	if err != nil {
		return preparedUpdate{}, err
	}
	hash, err := s.keys.requireLookup(purposeTransaction, update.Handle)
	if err != nil {
		return preparedUpdate{}, err
	}
	sealed, keyVersion, err := s.sealClientState(hash, update.ClientState)
	if err != nil {
		return preparedUpdate{}, err
	}
	return preparedUpdate{
		hash:            hash,
		scopes:          scopes,
		principalID:     nullableString(update.PrincipalID),
		sealedState:     sealed,
		stateKeyVersion: keyVersion,
	}, nil
}

// applyTransactionUpdate is the transactional body of UpdateAuthTransaction.
func (s *SQLiteStore) applyTransactionUpdate(ctx context.Context, tx *sql.Tx,
	update AuthTransactionUpdate, prepared preparedUpdate, expectVersion, next uint64,
) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE auth_transactions
		    SET principal_id = ?, scopes = ?, resource = ?, client_state_sealed = ?,
		        client_state_key_version = ?, version = ?
		  WHERE handle_hash = ? AND version = ?`,
		prepared.principalID, prepared.scopes, update.Resource, prepared.sealedState,
		prepared.stateKeyVersion, int64(next), prepared.hash, int64(expectVersion))
	if err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("store: principal %s does not exist: %w",
				update.PrincipalID, ErrPrincipalNotFound)
		}
		return fmt.Errorf("store: update authorization transaction: %w", err)
	}

	affected, err := affectedRows(result)
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	return transactionMissOrConflict(ctx, tx, prepared.hash, expectVersion)
}

// transactionMissOrConflict says whether an update that matched nothing lost a race or
// addressed a row that is no longer there.
func transactionMissOrConflict(ctx context.Context, tx *sql.Tx, hash string, expectVersion uint64,
) error {
	var stored int64
	err := tx.QueryRowContext(ctx,
		`SELECT version FROM auth_transactions WHERE handle_hash = ?`, hash).Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("store: authorization transaction is gone: %w", ErrTransactionNotFound)
	case err != nil:
		return fmt.Errorf("store: read authorization transaction version: %w", err)
	}
	return fmt.Errorf("store: transaction is at version %d, caller expected %d: %w",
		stored, expectVersion, ErrTransactionConflict)
}

// ConsumeAuthTransaction returns a transaction and deletes it, atomically.
//
// It is what makes completing an authorization single-use: the read and the delete are
// one transaction and the delete requires exactly one row, so of two concurrent callers
// exactly one receives the record and every other receives ErrTransactionNotFound. A
// compare-and-set on the version cannot provide that, because two callers can serialize
// and each win its own compare-and-set in turn.
//
// It is also how an expired transaction is discarded, so expiry is reported rather than
// enforced here: the record comes back with its window — AuthTransaction.IsExpired
// answers the question — and the row is gone either way, which is the point.
func (s *SQLiteStore) ConsumeAuthTransaction(ctx context.Context, handle Secret,
) (AuthTransaction, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return AuthTransaction{}, err
	}
	hash, err := s.keys.requireLookup(purposeTransaction, handle)
	if err != nil {
		return AuthTransaction{}, err
	}

	var consumed AuthTransaction
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		transaction, err := s.consumeTransactionIn(ctx, tx, hash)
		consumed = transaction
		return err
	})
	if err != nil {
		return AuthTransaction{}, err
	}
	return consumed, nil
}

// consumeTransactionIn is the transactional body of ConsumeAuthTransaction.
func (s *SQLiteStore) consumeTransactionIn(ctx context.Context, tx *sql.Tx, hash string,
) (AuthTransaction, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT `+transactionColumns+` FROM auth_transactions WHERE handle_hash = ?`, hash)
	transaction, err := s.scanTransaction(hash, row)
	if err != nil {
		return AuthTransaction{}, err
	}

	result, err := tx.ExecContext(ctx,
		`DELETE FROM auth_transactions WHERE handle_hash = ?`, hash)
	if err != nil {
		return AuthTransaction{}, fmt.Errorf("store: consume authorization transaction: %w", err)
	}
	err = requireOneRow(result, fmt.Errorf(
		"store: authorization transaction was consumed concurrently: %w", ErrTransactionNotFound))
	if err != nil {
		return AuthTransaction{}, err
	}
	return transaction, nil
}
