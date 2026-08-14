package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Hashed authorization transactions.
//
// The handle the browser carries is opaque, caller-generated, high-entropy material. It
// is not stored: only its keyed-HMAC lookup value is, under its own purpose, so it
// cannot be recovered from the database and cannot be presented as an authorization
// code.
//
// Only state the flow cannot rebuild lives here. Cookie jars and MFA state stay in a
// bounded in-memory registry by design, so a restart loses an in-flight login safely
// and requires a new one. Passwords and MFA codes never reach this package.
//
// PKCE is required: code_challenge_method is constrained to S256 by the schema, so a
// plain challenge cannot be stored at all.
//
// The client's opaque state is the one value here that belongs to someone else. It is
// echoed back byte for byte and is load-bearing for the client's own CSRF defence, so
// it is sealed rather than stored in the clear, like every other third-party value in
// this schema.

// maxChallengeLength bounds a PKCE challenge. A base64url SHA-256 digest is 43
// characters; the bound leaves room and refuses anything absurd.
const maxChallengeLength = 128

// challengeMethodS256 is the only method the schema accepts.
const challengeMethodS256 = "S256"

// maxAuthLifetime bounds a transaction or code lifetime. Both are short-lived by
// nature: a ten-minute authorization code is already generous.
const maxAuthLifetime = 30 * time.Minute

// AuthTransactionDraft is the input to PutAuthTransaction.
type AuthTransactionDraft struct {
	// Handle is the opaque value the browser carries. It is hashed, never stored.
	Handle Secret

	ClientID string

	// RedirectURI must already have been validated against the client with
	// CheckRedirectURI. It is stored so the code that follows inherits exactly the
	// URI the authorization request named.
	RedirectURI string

	Scopes []string

	// Resource is the RFC 8707 resource indicator the request named. It may be empty.
	Resource string

	// ClientState is the client's opaque state. It is sealed, never stored in the
	// clear, and echoed back byte for byte. The zero Secret means the request carried
	// no state.
	ClientState Secret

	// CodeChallenge is the PKCE S256 challenge.
	CodeChallenge string

	// Lifetime is how long the transaction may be resumed for. It is used only when
	// ExpiresAt is zero.
	Lifetime time.Duration

	// CreatedAt and ExpiresAt are the absolute instants of the record. A caller that
	// already stamped its own record passes them, so the stored expiry cannot drift
	// from what the caller returned; a caller with a lifetime leaves them zero.
	CreatedAt time.Time
	ExpiresAt time.Time
}

// AuthTransaction is stored authorization request state.
type AuthTransaction struct {
	ClientID string

	// PrincipalID is empty until the browser flow has identified the user.
	PrincipalID string

	RedirectURI string

	// Resource is the RFC 8707 resource indicator, empty when the request named none.
	Resource string

	Scopes        []string
	CodeChallenge string

	// ClientState is the opaque client state, opened from its envelope. It is a
	// Secret so a print or a log of the transaction cannot reveal it.
	ClientState Secret

	CreatedAt time.Time
	ExpiresAt time.Time

	// Version is the compare-and-set counter. A freshly created transaction is at
	// version 0, and UpdateAuthTransaction advances it by one.
	Version uint64
}

// IsExpired reports whether the transaction is past its expiry at now.
func (t AuthTransaction) IsExpired(now time.Time) bool { return !now.Before(t.ExpiresAt) }

// PutAuthTransaction stores authorization request state under the hash of its handle.
func (s *SQLiteStore) PutAuthTransaction(ctx context.Context, draft AuthTransactionDraft) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	prepared, err := s.prepareTransaction(draft)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO auth_transactions
		     (handle_hash, client_id, principal_id, redirect_uri, scopes, resource,
		      code_challenge, code_challenge_method, client_state_sealed,
		      client_state_key_version, version, created_at, expires_at)
		 VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		prepared.hash, draft.ClientID, draft.RedirectURI, prepared.scopes, draft.Resource,
		draft.CodeChallenge, challengeMethodS256, prepared.sealedState, prepared.stateKeyVersion,
		formatTime(prepared.createdAt), formatTime(prepared.expiresAt))
	switch {
	case isUniqueViolation(err):
		return fmt.Errorf("store: transaction handle is already in use: %w", ErrInvalidArgument)
	case isForeignKeyViolation(err):
		return fmt.Errorf("store: client %s does not exist: %w", draft.ClientID, ErrClientNotFound)
	case err != nil:
		return fmt.Errorf("store: insert authorization transaction: %w", err)
	}
	return nil
}

// preparedTransaction is a validated draft with its lookup value, its sealed state and
// its absolute instants ready for the insert.
type preparedTransaction struct {
	hash            string
	scopes          string
	sealedState     []byte
	stateKeyVersion sql.NullInt64
	createdAt       time.Time
	expiresAt       time.Time
}

// prepareTransaction validates a draft, hashes its handle and seals its client state.
func (s *SQLiteStore) prepareTransaction(draft AuthTransactionDraft) (preparedTransaction, error) {
	if err := checkIdentifier("client id", draft.ClientID); err != nil {
		return preparedTransaction{}, err
	}
	if err := checkLocator("resource", draft.Resource); err != nil {
		return preparedTransaction{}, err
	}
	if err := checkChallenge(draft.CodeChallenge); err != nil {
		return preparedTransaction{}, err
	}
	scopes, err := encodeScopes(draft.Scopes)
	if err != nil {
		return preparedTransaction{}, err
	}
	hash, err := s.keys.requireLookup(purposeTransaction, draft.Handle)
	if err != nil {
		return preparedTransaction{}, err
	}
	createdAt, expiresAt, err := s.authWindow(draft.CreatedAt, draft.ExpiresAt, draft.Lifetime)
	if err != nil {
		return preparedTransaction{}, err
	}
	sealed, keyVersion, err := s.sealClientState(hash, draft.ClientState)
	if err != nil {
		return preparedTransaction{}, err
	}
	return preparedTransaction{
		hash:            hash,
		scopes:          scopes,
		sealedState:     sealed,
		stateKeyVersion: keyVersion,
		createdAt:       createdAt,
		expiresAt:       expiresAt,
	}, nil
}

// authWindow resolves the created and expiry instants of a transaction or a code.
//
// Absolute instants win when the caller supplied them, so a record the caller already
// stamped is stored exactly as stamped rather than re-derived from this store's clock
// at whatever moment the call arrives. Otherwise the lifetime is applied to now.
func (s *SQLiteStore) authWindow(createdAt, expiresAt time.Time, lifetime time.Duration,
) (time.Time, time.Time, error) {
	created := createdAt
	if created.IsZero() {
		created = s.now()
	}
	created = created.UTC()

	if expiresAt.IsZero() {
		if err := checkAuthLifetime(lifetime); err != nil {
			return time.Time{}, time.Time{}, err
		}
		return created, created.Add(lifetime), nil
	}
	if err := checkAuthLifetime(expiresAt.Sub(created)); err != nil {
		return time.Time{}, time.Time{}, err
	}
	return created, expiresAt.UTC(), nil
}

// checkChallenge requires a non-empty, bounded, base64url-shaped challenge.
func checkChallenge(challenge string) error {
	if challenge == "" || len(challenge) > maxChallengeLength {
		return fmt.Errorf("store: pkce challenge has length %d: %w",
			len(challenge), ErrInvalidArgument)
	}
	for _, char := range challenge {
		unreserved := char == '-' || char == '_' || char == '.' || char == '~'
		alnum := (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9')
		if !alnum && !unreserved {
			return fmt.Errorf("store: pkce challenge holds a character that is not allowed: %w",
				ErrInvalidArgument)
		}
	}
	return nil
}

func checkAuthLifetime(lifetime time.Duration) error {
	if lifetime <= 0 || lifetime > maxAuthLifetime {
		return fmt.Errorf("store: authorization lifetime %s is outside (0, %s]: %w",
			lifetime, maxAuthLifetime, ErrInvalidArgument)
	}
	return nil
}

// transactionColumns is the select list every transaction read shares.
const transactionColumns = `client_id, principal_id, redirect_uri, scopes, resource,
	code_challenge, client_state_sealed, version, created_at, expires_at`

// AuthTransaction returns the state stored under a handle.
//
// The expiry predicate is in the query, so an expired transaction reports
// ErrTransactionNotFound whether or not Cleanup has run. An expired transaction and an
// unknown handle share one error: the distinction would tell a caller that a handle
// once existed. ConsumeAuthTransaction is the one read that returns an expired record,
// because discarding it is the point of that call.
func (s *SQLiteStore) AuthTransaction(ctx context.Context, handle Secret) (AuthTransaction, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return AuthTransaction{}, err
	}
	hash, err := s.keys.requireLookup(purposeTransaction, handle)
	if err != nil {
		return AuthTransaction{}, err
	}

	row := s.db.QueryRowContext(ctx,
		`SELECT `+transactionColumns+`
		   FROM auth_transactions
		  WHERE handle_hash = ? AND expires_at > ?`,
		hash, formatTime(s.now()))
	return s.scanTransaction(hash, row)
}

// scanTransaction reads one transaction row and opens its sealed client state.
func (s *SQLiteStore) scanTransaction(hash string, row *sql.Row) (AuthTransaction, error) {
	var (
		transaction AuthTransaction
		principalID sql.NullString
		scopes      string
		sealedState []byte
		version     int64
		createdText string
		expiresText string
	)
	err := row.Scan(&transaction.ClientID, &principalID, &transaction.RedirectURI, &scopes,
		&transaction.Resource, &transaction.CodeChallenge, &sealedState, &version,
		&createdText, &expiresText)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return AuthTransaction{}, fmt.Errorf(
			"store: no live authorization transaction matches: %w", ErrTransactionNotFound)
	case err != nil:
		return AuthTransaction{}, fmt.Errorf("store: read authorization transaction: %w", err)
	case version < 0:
		return AuthTransaction{}, fmt.Errorf("store: transaction version %d is negative: %w",
			version, ErrCorruptRecord)
	}

	transaction.PrincipalID = principalID.String
	transaction.Scopes = decodeScopes(scopes)
	transaction.Version = uint64(version)
	if transaction.ClientState, err = s.openClientState(hash, sealedState); err != nil {
		return AuthTransaction{}, err
	}
	if transaction.CreatedAt, err = parseTime(createdText); err != nil {
		return AuthTransaction{}, err
	}
	if transaction.ExpiresAt, err = parseTime(expiresText); err != nil {
		return AuthTransaction{}, err
	}
	return transaction, nil
}

// AttachPrincipal records which principal a transaction belongs to, once the browser
// flow has identified one. An expired or unknown transaction reports
// ErrTransactionNotFound.
//
// It does not take a version and does not advance one: it is the narrow write the
// login flow needs. UpdateAuthTransaction is the compare-and-set for a caller that
// holds a whole record.
func (s *SQLiteStore) AttachPrincipal(ctx context.Context, handle Secret, principalID string) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	if err := checkIdentifier("principal id", principalID); err != nil {
		return err
	}
	hash, err := s.keys.requireLookup(purposeTransaction, handle)
	if err != nil {
		return err
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE auth_transactions SET principal_id = ?, version = version + 1
		  WHERE handle_hash = ? AND expires_at > ?`,
		principalID, hash, formatTime(s.now()))
	if err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("store: principal %s does not exist: %w",
				principalID, ErrPrincipalNotFound)
		}
		return fmt.Errorf("store: attach principal to transaction: %w", err)
	}
	return requireOneRow(result, fmt.Errorf(
		"store: no live authorization transaction matches: %w", ErrTransactionNotFound))
}

// DeleteAuthTransaction removes a transaction. An absent one is not an error, so
// cleaning up after a completed or abandoned flow is always safe.
//
// It is therefore not a way to make completing an authorization single-use: two
// callers deleting the same handle both succeed. ConsumeAuthTransaction is the call
// that elects exactly one winner.
func (s *SQLiteStore) DeleteAuthTransaction(ctx context.Context, handle Secret) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	hash, err := s.keys.requireLookup(purposeTransaction, handle)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM auth_transactions WHERE handle_hash = ?`, hash); err != nil {
		return fmt.Errorf("store: delete authorization transaction: %w", err)
	}
	return nil
}
