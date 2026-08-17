package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// maxBindGarminAttempts bounds the retry BindGarminAccount takes after losing a
// concurrent linkage. One retry is enough: the second attempt's own hash lookup
// runs inside a fresh transaction and therefore sees the winner's already
// committed row, so it resolves the existing principal instead of racing again.
const maxBindGarminAttempts = 2

// GarminBindInput is what BindGarminAccount needs to bind one completed Garmin
// login to a principal.
type GarminBindInput struct {
	// Account is Garmin's stable account identifier — the isolation key.
	Account Secret
	// Email is the login handle. It is used only when no principal is linked to
	// Account yet, either to find a principal already registered under it or to
	// mint a new one.
	Email string
	// DisplayName is what Garmin reports as the account's name.
	DisplayName string
	// Tokens is the token set the login produced.
	Tokens TokenSet
}

// BindGarminAccount resolves or creates the principal for a Garmin account, links
// the account to it, and stores the token set the login produced — all as one
// atomic operation.
//
// # Why this exists
//
// A caller that ran these three steps as three separately committed writes could
// be interrupted between any two of them, leaving a durable row behind: a
// principal that claims an email but holds no linkage, or a linked principal that
// holds no token set. Neither is a security defect — an orphan grants no access —
// but both are durable identity inconsistency that nothing ever heals. Running
// every step inside one transaction means any failure, at any point, rolls all of
// it back and leaves nothing new behind.
//
// # Concurrency
//
// Two calls that name the same Garmin account cannot both create a principal for
// it. Every write transaction against this database begins immediately
// (_txlock=immediate, see sqlite_db.go), so the two calls' transactions cannot
// interleave: whichever begins second does not run a single statement until the
// first has committed or rolled back, and its own lookup of the account then
// already sees the first's result. The account-hash unique constraint and
// ErrGarminAccountLinked below are kept as defense in depth, not as the primary
// mechanism — a call that still manages to lose that race rolls back its own
// half-built principal and retries once, which resolves to the winner instead of
// leaving a second, unlinked row behind.
//
// A returning principal — one Garmin account already linked — keeps its existing
// token set until this call's write actually commits: the compare-and-set
// baseline is read inside the same transaction that performs the write, so a
// failure anywhere in the transaction leaves the previous token set exactly as it
// was.
func (s *SQLiteStore) BindGarminAccount(ctx context.Context, in GarminBindInput) (Principal, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return Principal{}, err
	}
	if in.Account.IsZero() {
		return Principal{}, fmt.Errorf("store: no garmin account id: %w", ErrInvalidArgument)
	}
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return Principal{}, err
	}
	hash, err := s.keys.requireLookup(purposeGarminAccount, in.Account)
	if err != nil {
		return Principal{}, err
	}

	var (
		principal Principal
		bindErr   error
	)
	for range maxBindGarminAttempts {
		principal, bindErr = s.bindGarminAccountTx(ctx, hash, email, in)
		if !errors.Is(bindErr, ErrGarminAccountLinked) {
			return principal, bindErr
		}
	}
	return principal, bindErr
}

// bindGarminAccountTx is the transactional body of BindGarminAccount: resolve,
// link, and save, or roll back all three together.
func (s *SQLiteStore) bindGarminAccountTx(
	ctx context.Context, hash, email string, in GarminBindInput,
) (Principal, error) {
	var principal Principal
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		resolved, err := s.resolvePrincipalTx(ctx, tx, hash, email)
		if err != nil {
			return err
		}
		principal = resolved

		sealed, err := s.sealGarminIdentity(principal.ID,
			GarminIdentity{AccountID: in.Account, DisplayName: in.DisplayName})
		if err != nil {
			return err
		}
		keyVersion, err := s.crypt.activeVersion()
		if err != nil {
			return err
		}
		if err := s.applyGarminLink(ctx, tx, principal.ID, hash, sealed, keyVersion); err != nil {
			return err
		}
		return s.saveTokensTx(ctx, tx, principal.ID, in.Tokens)
	})
	if err != nil {
		return Principal{}, err
	}
	principal.GarminLinked = true
	return principal, nil
}

// resolvePrincipalTx finds the principal a Garmin account is already linked to,
// or finds-or-creates one for the login handle when it is not.
//
// The lookup by account hash runs first and inside the same transaction the
// caller uses to link and save, so a principal is only ever created when this
// call itself has established — under the transaction's own write lock — that no
// principal already claims the account.
func (s *SQLiteStore) resolvePrincipalTx(
	ctx context.Context, tx *sql.Tx, hash, email string,
) (Principal, error) {
	switch linked, err := s.principalByPredicateTx(ctx, tx, `garmin_account_hash = ?`, hash); {
	case err == nil:
		return linked, nil
	case !errors.Is(err, ErrPrincipalNotFound):
		return Principal{}, fmt.Errorf("store: check garmin linkage: %w", err)
	}

	created, err := s.createPrincipalTx(ctx, tx, email)
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, ErrPrincipalExists) {
		return Principal{}, err
	}

	existing, err := s.principalByPredicateTx(ctx, tx, `email_normalized = ?`, email)
	if err != nil {
		return Principal{}, fmt.Errorf("store: resolving the principal for a registered handle: %w", err)
	}
	return existing, nil
}

// principalByPredicateTx is principalWhere's transactional counterpart. The
// predicate is a constant from this file; only the bound value comes from a
// caller.
func (s *SQLiteStore) principalByPredicateTx(
	ctx context.Context, tx *sql.Tx, predicate string, value any,
) (Principal, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+principalColumns+` FROM principals WHERE `+predicate, value)
	principal, err := scanPrincipal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, fmt.Errorf("store: no principal matches: %w", ErrPrincipalNotFound)
	}
	return principal, err
}

// createPrincipalTx is CreatePrincipal's transactional counterpart.
func (s *SQLiteStore) createPrincipalTx(ctx context.Context, tx *sql.Tx, email string) (Principal, error) {
	id, err := newPrincipalID()
	if err != nil {
		return Principal{}, err
	}
	version, err := s.crypt.activeVersion()
	if err != nil {
		return Principal{}, err
	}

	now := s.now().UTC()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO principals (id, email_normalized, garmin_account_hash, garmin_identity_sealed,
		     key_version, created_at, updated_at)
		 VALUES (?, ?, NULL, NULL, ?, ?, ?)`,
		id, nullableString(email), version, formatTime(now), formatTime(now))
	if err != nil {
		if isUniqueViolation(err) {
			return Principal{}, fmt.Errorf("store: email is already registered: %w", ErrPrincipalExists)
		}
		return Principal{}, fmt.Errorf("store: create principal: %w", err)
	}
	return Principal{ID: id, Email: email, CreatedAt: now, UpdatedAt: now}, nil
}

// saveTokensTx is Save's transactional counterpart: it reads the current version
// and writes the next one inside the caller's transaction, so the baseline can
// never go stale between the read and the write.
func (s *SQLiteStore) saveTokensTx(ctx context.Context, tx *sql.Tx, principalID string, set TokenSet) error {
	if set.IsZero() {
		return fmt.Errorf("store: refusing to store an empty token set: %w", ErrInvalidArgument)
	}
	current, err := currentTokenVersion(ctx, tx, principalID)
	if err != nil {
		return err
	}
	return s.commitTokenRecord(ctx, tx, principalID, set, current, current+1)
}
