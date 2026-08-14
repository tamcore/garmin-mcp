package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Per-principal, per-client consent.
//
// A revocation sets revoked_at rather than deleting the row, so the record that a
// grant once existed survives a later re-grant. The audit trail matters more than
// the disk space.

// Consent is one principal's grant to one client.
type Consent struct {
	PrincipalID string
	ClientID    string

	// Scopes are the granted scopes, in the order granted.
	Scopes []string

	GrantedAt time.Time
}

// There is deliberately no Revoked field. A revoked consent is never returned: Consent
// reports ErrConsentNotFound instead. A flag here would be a field that can only ever be
// false, and a caller that checked the flag rather than the error would authorize a
// withdrawn grant the first time it forgot. The revocation instant is still recorded in
// the row, for the audit trail.

// GrantConsent records or re-records a principal's grant to a client.
//
// Re-granting replaces the scope list and clears any earlier revocation, which is
// what a user re-approving a consent screen means. It is idempotent: granting the
// same scopes twice reaches the same state.
func (s *SQLiteStore) GrantConsent(ctx context.Context, principalID, clientID string,
	scopes []string,
) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	if err := checkIdentifier("principal id", principalID); err != nil {
		return err
	}
	if err := checkIdentifier("client id", clientID); err != nil {
		return err
	}
	encoded, err := encodeScopes(scopes)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO consents (principal_id, client_id, scopes, granted_at, revoked_at)
		 VALUES (?, ?, ?, ?, NULL)
		 ON CONFLICT (principal_id, client_id)
		 DO UPDATE SET scopes = excluded.scopes, granted_at = excluded.granted_at, revoked_at = NULL`,
		principalID, clientID, encoded, formatTime(s.now()))
	if err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("store: principal %s or client %s does not exist: %w",
				principalID, clientID, ErrPrincipalNotFound)
		}
		return fmt.Errorf("store: grant consent: %w", err)
	}
	return nil
}

// Consent returns an active grant. A grant that was never made, or that has been
// revoked, reports ErrConsentNotFound.
func (s *SQLiteStore) Consent(ctx context.Context, principalID, clientID string) (Consent, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return Consent{}, err
	}
	var (
		encoded     string
		grantedText string
		revokedAt   sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT scopes, granted_at, revoked_at FROM consents WHERE principal_id = ? AND client_id = ?`,
		principalID, clientID).Scan(&encoded, &grantedText, &revokedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Consent{}, fmt.Errorf("store: principal %s never granted client %s: %w",
			principalID, clientID, ErrConsentNotFound)
	case err != nil:
		return Consent{}, fmt.Errorf("store: read consent: %w", err)
	case revokedAt.Valid:
		return Consent{}, fmt.Errorf("store: principal %s revoked client %s: %w",
			principalID, clientID, ErrConsentNotFound)
	}

	grantedAt, err := parseTime(grantedText)
	if err != nil {
		return Consent{}, err
	}
	return Consent{
		PrincipalID: principalID,
		ClientID:    clientID,
		Scopes:      decodeScopes(encoded),
		GrantedAt:   grantedAt,
	}, nil
}
