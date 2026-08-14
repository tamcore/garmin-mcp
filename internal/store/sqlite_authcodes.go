package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Hashed single-use authorization codes.
//
// The code is opaque, caller-generated, high-entropy material and is never stored:
// only its keyed-HMAC lookup value is, under its own purpose, so it cannot be
// recovered from the database and cannot be presented as a transaction handle.

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

	// Lifetime is used only when ExpiresAt is zero.
	Lifetime time.Duration

	// IssuedAt and ExpiresAt are the absolute instants of the record. A caller that
	// already stamped its own record passes them, so the stored expiry cannot drift
	// from what the caller returned by the latency of this call.
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// AuthCode is what a redeemed authorization code proves.
type AuthCode struct {
	PrincipalID   string
	ClientID      string
	RedirectURI   string
	Audience      string
	Scopes        []string
	CodeChallenge string

	// IssuedAt is when the code was stored, so a redeemed code reports its whole
	// window rather than only its end.
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// IsExpired reports whether the code is past its expiry at now.
func (c AuthCode) IsExpired(now time.Time) bool { return !now.Before(c.ExpiresAt) }

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
	scopes, err := encodeScopes(draft.Scopes)
	if err != nil {
		return err
	}
	hash, err := s.keys.requireLookup(purposeAuthCode, draft.Code)
	if err != nil {
		return err
	}
	issuedAt, expiresAt, err := s.authWindow(draft.IssuedAt, draft.ExpiresAt, draft.Lifetime)
	if err != nil {
		return err
	}
	return s.insertAuthCode(ctx, draft, hash, scopes, issuedAt, expiresAt)
}

// insertAuthCode is the write half of PutAuthCode.
func (s *SQLiteStore) insertAuthCode(ctx context.Context, draft AuthCodeDraft,
	hash, scopes string, issuedAt, expiresAt time.Time,
) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_codes
		     (code_hash, principal_id, client_id, redirect_uri, scopes, audience,
		      code_challenge, created_at, expires_at, consumed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		hash, draft.PrincipalID, draft.ClientID, draft.RedirectURI, scopes, draft.Audience,
		draft.CodeChallenge, formatTime(issuedAt), formatTime(expiresAt))
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
// should audit.
//
// An expired code reports both ErrCodeExpired and ErrCodeNotFound: a caller whose own
// contract names an expired-code error can distinguish it, and a caller that must not
// turn expiry into an oracle for whether a code ever existed keeps matching the one
// sentinel it already matched. An expired code is not consumed; Cleanup removes it.
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
	if code.IsExpired(s.now().UTC()) {
		return AuthCode{}, fmt.Errorf("store: authorization code expired at %s: %w: %w",
			code.ExpiresAt.Format(timeLayout), ErrCodeExpired, ErrCodeNotFound)
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
		createdText string
		expiresText string
		consumedAt  sql.NullString
	)
	err := tx.QueryRowContext(ctx,
		`SELECT principal_id, client_id, redirect_uri, scopes, audience, code_challenge,
		        created_at, expires_at, consumed_at
		   FROM auth_codes WHERE code_hash = ?`, hash).Scan(
		&code.PrincipalID, &code.ClientID, &code.RedirectURI, &scopes, &code.Audience,
		&code.CodeChallenge, &createdText, &expiresText, &consumedAt)
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
	if code.IssuedAt, err = parseTime(createdText); err != nil {
		return AuthCode{}, err
	}
	if code.ExpiresAt, err = parseTime(expiresText); err != nil {
		return AuthCode{}, err
	}
	return code, nil
}
