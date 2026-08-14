package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Hashed authorization transactions and codes.
//
// Both the transaction handle the browser carries and the authorization code the
// client redeems are opaque, caller-generated, high-entropy values. Neither is stored:
// only its keyed-HMAC lookup value is, under its own purpose, so neither can be
// recovered from the database and neither can be presented as the other.
//
// Only state the flow cannot rebuild lives here. Cookie jars and MFA state stay in a
// bounded in-memory registry by design, so a restart loses an in-flight login safely
// and requires a new one. Passwords and MFA codes never reach this package.
//
// PKCE is required: code_challenge_method is constrained to S256 by the schema, so a
// plain challenge cannot be stored at all.

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

	// CodeChallenge is the PKCE S256 challenge.
	CodeChallenge string

	// Lifetime is how long the transaction may be resumed for.
	Lifetime time.Duration
}

// AuthTransaction is stored authorization request state.
type AuthTransaction struct {
	ClientID string

	// PrincipalID is empty until the browser flow has identified the user.
	PrincipalID string

	RedirectURI   string
	Scopes        []string
	CodeChallenge string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// PutAuthTransaction stores authorization request state under the hash of its handle.
func (s *SQLiteStore) PutAuthTransaction(ctx context.Context, draft AuthTransactionDraft) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	if err := checkIdentifier("client id", draft.ClientID); err != nil {
		return err
	}
	if err := checkChallenge(draft.CodeChallenge); err != nil {
		return err
	}
	if err := checkAuthLifetime(draft.Lifetime); err != nil {
		return err
	}
	scopes, err := encodeScopes(draft.Scopes)
	if err != nil {
		return err
	}
	hash, err := s.keys.requireLookup(purposeTransaction, draft.Handle)
	if err != nil {
		return err
	}

	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO auth_transactions
		     (handle_hash, client_id, principal_id, redirect_uri, scopes,
		      code_challenge, code_challenge_method, created_at, expires_at)
		 VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?)`,
		hash, draft.ClientID, draft.RedirectURI, scopes, draft.CodeChallenge,
		challengeMethodS256, formatTime(now), formatTime(now.Add(draft.Lifetime)))
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

// AuthTransaction returns the state stored under a handle.
//
// The expiry predicate is in the query, so an expired transaction reports
// ErrTransactionNotFound whether or not Cleanup has run. An expired transaction and an
// unknown handle share one error: the distinction would tell a caller that a handle
// once existed.
func (s *SQLiteStore) AuthTransaction(ctx context.Context, handle Secret) (AuthTransaction, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return AuthTransaction{}, err
	}
	hash, err := s.keys.requireLookup(purposeTransaction, handle)
	if err != nil {
		return AuthTransaction{}, err
	}

	var (
		transaction AuthTransaction
		principalID sql.NullString
		scopes      string
		createdText string
		expiresText string
	)
	err = s.db.QueryRowContext(ctx,
		`SELECT client_id, principal_id, redirect_uri, scopes, code_challenge, created_at, expires_at
		   FROM auth_transactions
		  WHERE handle_hash = ? AND expires_at > ?`,
		hash, formatTime(s.now())).Scan(
		&transaction.ClientID, &principalID, &transaction.RedirectURI, &scopes,
		&transaction.CodeChallenge, &createdText, &expiresText)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return AuthTransaction{}, fmt.Errorf(
			"store: no live authorization transaction matches: %w", ErrTransactionNotFound)
	case err != nil:
		return AuthTransaction{}, fmt.Errorf("store: read authorization transaction: %w", err)
	}

	transaction.PrincipalID = principalID.String
	transaction.Scopes = decodeScopes(scopes)
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
		`UPDATE auth_transactions SET principal_id = ?
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

// AuthCodeDraft is the input to PutAuthCode.
type AuthCodeDraft struct {
	// Code is the opaque authorization code. It is hashed, never stored.
	Code Secret

	PrincipalID   string
	ClientID      string
	RedirectURI   string
	Audience      string
	Scopes        []string
	CodeChallenge string
	Lifetime      time.Duration
}

// AuthCode is what a redeemed authorization code proves.
type AuthCode struct {
	PrincipalID   string
	ClientID      string
	RedirectURI   string
	Audience      string
	Scopes        []string
	CodeChallenge string
	ExpiresAt     time.Time
}

// PutAuthCode stores an authorization code under its hash.
func (s *SQLiteStore) PutAuthCode(ctx context.Context, draft AuthCodeDraft) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	for kind, value := range map[string]string{
		"principal id": draft.PrincipalID, "client id": draft.ClientID, "audience": draft.Audience,
	} {
		if err := checkIdentifier(kind, value); err != nil {
			return err
		}
	}
	if err := checkChallenge(draft.CodeChallenge); err != nil {
		return err
	}
	if err := checkAuthLifetime(draft.Lifetime); err != nil {
		return err
	}
	scopes, err := encodeScopes(draft.Scopes)
	if err != nil {
		return err
	}
	hash, err := s.keys.requireLookup(purposeAuthCode, draft.Code)
	if err != nil {
		return err
	}

	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO auth_codes
		     (code_hash, principal_id, client_id, redirect_uri, scopes, audience,
		      code_challenge, created_at, expires_at, consumed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		hash, draft.PrincipalID, draft.ClientID, draft.RedirectURI, scopes, draft.Audience,
		draft.CodeChallenge, formatTime(now), formatTime(now.Add(draft.Lifetime)))
	switch {
	case isUniqueViolation(err):
		return fmt.Errorf("store: authorization code is already stored: %w", ErrInvalidArgument)
	case isForeignKeyViolation(err):
		return fmt.Errorf("store: principal %s or client %s does not exist: %w",
			draft.PrincipalID, draft.ClientID, ErrPrincipalNotFound)
	case err != nil:
		return fmt.Errorf("store: insert authorization code: %w", err)
	}
	return nil
}

// ConsumeAuthCode redeems an authorization code exactly once.
//
// The read and the consumption happen in one immediate transaction, and the UPDATE
// repeats the not-consumed precondition, so two concurrent redemptions of one code
// produce exactly one success and one ErrCodeAlreadyUsed. A replay is reported
// distinctly from an unknown code, because a replay is a security event a caller
// should audit; an expired code reports ErrCodeNotFound, so expiry does not become an
// oracle for whether a code ever existed.
func (s *SQLiteStore) ConsumeAuthCode(ctx context.Context, code Secret) (AuthCode, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return AuthCode{}, err
	}
	hash, err := s.keys.requireLookup(purposeAuthCode, code)
	if err != nil {
		return AuthCode{}, err
	}

	var redeemed AuthCode
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		consumed, err := s.consumeCodeIn(ctx, tx, hash)
		redeemed = consumed
		return err
	})
	if err != nil {
		return AuthCode{}, err
	}
	return redeemed, nil
}

// consumeCodeIn is the transactional body of ConsumeAuthCode.
func (s *SQLiteStore) consumeCodeIn(ctx context.Context, tx *sql.Tx, hash string) (AuthCode, error) {
	code, err := readAuthCode(ctx, tx, hash)
	if err != nil {
		return AuthCode{}, err
	}
	if !code.ExpiresAt.After(s.now().UTC()) {
		return AuthCode{}, fmt.Errorf("store: authorization code expired at %s: %w",
			code.ExpiresAt.Format(timeLayout), ErrCodeNotFound)
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE auth_codes SET consumed_at = ? WHERE code_hash = ? AND consumed_at IS NULL`,
		formatTime(s.now()), hash)
	if err != nil {
		return AuthCode{}, fmt.Errorf("store: consume authorization code: %w", err)
	}
	err = requireOneRow(result, fmt.Errorf(
		"store: authorization code was redeemed concurrently: %w", ErrCodeAlreadyUsed))
	if err != nil {
		return AuthCode{}, err
	}
	return code, nil
}

// readAuthCode loads a code row and refuses one that was already redeemed.
func readAuthCode(ctx context.Context, tx *sql.Tx, hash string) (AuthCode, error) {
	var (
		code        AuthCode
		scopes      string
		expiresText string
		consumedAt  sql.NullString
	)
	err := tx.QueryRowContext(ctx,
		`SELECT principal_id, client_id, redirect_uri, scopes, audience, code_challenge,
		        expires_at, consumed_at
		   FROM auth_codes WHERE code_hash = ?`, hash).Scan(
		&code.PrincipalID, &code.ClientID, &code.RedirectURI, &scopes, &code.Audience,
		&code.CodeChallenge, &expiresText, &consumedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return AuthCode{}, fmt.Errorf("store: no authorization code matches: %w", ErrCodeNotFound)
	case err != nil:
		return AuthCode{}, fmt.Errorf("store: read authorization code: %w", err)
	case consumedAt.Valid:
		return AuthCode{}, fmt.Errorf("store: authorization code was already redeemed: %w",
			ErrCodeAlreadyUsed)
	}

	code.Scopes = decodeScopes(scopes)
	if code.ExpiresAt, err = parseTime(expiresText); err != nil {
		return AuthCode{}, err
	}
	return code, nil
}
