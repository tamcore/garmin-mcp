package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Consent, keyed on the tuple that makes it a confused-deputy mitigation.
//
// A revocation sets revoked_at rather than deleting the row, so the record that a
// grant once existed survives a later re-grant. The audit trail matters more than
// the disk space.

// ConsentKey is the tuple a consent is bound to: the principal, the client, the
// exact redirect URI and the resource indicator.
//
// All four are part of the key on purpose. A narrower key collapses grants that
// differ in redirect URI or resource onto one row, so recording one would overwrite
// the others and revoking one would revoke them all — the confused-deputy failure
// the MCP guidance names. A change to any part of the key finds no row, which forces
// a fresh decision instead of reusing someone else's.
//
// RedirectURI and Resource may be empty. Empty is a state: a grant recorded without
// a redirect URI, or for a request that named no resource. It is a different key
// from any non-empty value, never a wildcard.
//
// ConsentKey is comparable and immutable.
type ConsentKey struct {
	PrincipalID string
	ClientID    string
	RedirectURI string
	Resource    string
}

// check validates a key at the boundary.
func (k ConsentKey) check() error {
	if err := checkIdentifier("principal id", k.PrincipalID); err != nil {
		return err
	}
	if err := checkIdentifier("client id", k.ClientID); err != nil {
		return err
	}
	if err := checkLocator("redirect uri", k.RedirectURI); err != nil {
		return err
	}
	return checkLocator("resource", k.Resource)
}

// Consent is one principal's grant to one client, for one redirect URI and one
// resource.
type Consent struct {
	PrincipalID string
	ClientID    string

	// RedirectURI and Resource complete the key. Both may be empty.
	RedirectURI string
	Resource    string

	// Scopes are the granted scopes, in the order granted. It may be empty: a user
	// may authorize a client for nothing.
	Scopes []string

	GrantedAt time.Time
}

// Key returns the tuple this consent is stored under.
func (c Consent) Key() ConsentKey {
	return ConsentKey{
		PrincipalID: c.PrincipalID,
		ClientID:    c.ClientID,
		RedirectURI: c.RedirectURI,
		Resource:    c.Resource,
	}
}

// There is deliberately no Revoked field. A revoked consent is never returned:
// ConsentFor reports ErrConsentNotFound instead. A flag here would be a field that can
// only ever be false, and a caller that checked the flag rather than the error would
// authorize a withdrawn grant the first time it forgot. The revocation instant is still
// recorded in the row, for the audit trail.

// GrantConsentFor records or re-records a grant for one exact key.
//
// Re-granting replaces the scope list and clears any earlier revocation, which is
// what a user re-approving a consent screen means. It is idempotent: granting the
// same scopes twice reaches the same state. An empty scope list is accepted, because
// a grant of no scope is a decision a user can make and has to be storable.
func (s *SQLiteStore) GrantConsentFor(ctx context.Context, key ConsentKey, scopes []string) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	if err := key.check(); err != nil {
		return err
	}
	encoded, err := encodeOptionalScopes(scopes)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO consents
		     (principal_id, client_id, redirect_uri, resource, scopes, granted_at, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)
		 ON CONFLICT (principal_id, client_id, redirect_uri, resource)
		 DO UPDATE SET scopes = excluded.scopes, granted_at = excluded.granted_at, revoked_at = NULL`,
		key.PrincipalID, key.ClientID, key.RedirectURI, key.Resource, encoded, formatTime(s.now()))
	if err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("store: principal %s or client %s does not exist: %w",
				key.PrincipalID, key.ClientID, ErrPrincipalNotFound)
		}
		return fmt.Errorf("store: grant consent: %w", err)
	}
	return nil
}

// ConsentFor returns the active grant stored under one exact key. A grant that was
// never made, or that has been revoked, reports ErrConsentNotFound.
func (s *SQLiteStore) ConsentFor(ctx context.Context, key ConsentKey) (Consent, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return Consent{}, err
	}
	var (
		encoded     string
		grantedText string
		revokedAt   sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT scopes, granted_at, revoked_at FROM consents
		  WHERE principal_id = ? AND client_id = ? AND redirect_uri = ? AND resource = ?`,
		key.PrincipalID, key.ClientID, key.RedirectURI, key.Resource).
		Scan(&encoded, &grantedText, &revokedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Consent{}, fmt.Errorf("store: principal %s never granted client %s for that "+
			"redirect uri and resource: %w", key.PrincipalID, key.ClientID, ErrConsentNotFound)
	case err != nil:
		return Consent{}, fmt.Errorf("store: read consent: %w", err)
	case revokedAt.Valid:
		return Consent{}, fmt.Errorf("store: principal %s revoked client %s: %w",
			key.PrincipalID, key.ClientID, ErrConsentNotFound)
	}

	grantedAt, err := parseTime(grantedText)
	if err != nil {
		return Consent{}, err
	}
	return Consent{
		PrincipalID: key.PrincipalID,
		ClientID:    key.ClientID,
		RedirectURI: key.RedirectURI,
		Resource:    key.Resource,
		Scopes:      decodeScopes(encoded),
		GrantedAt:   grantedAt,
	}, nil
}

// GrantConsent records a grant with no redirect URI and no resource.
//
// It is the narrow form, kept for callers that have no such context, and it is
// exactly GrantConsentFor with an empty redirect URI and an empty resource — not a
// wildcard over them. Unlike GrantConsentFor it refuses an empty scope list, because
// a caller with no tuple to name and no scope to grant is recording nothing.
func (s *SQLiteStore) GrantConsent(ctx context.Context, principalID, clientID string,
	scopes []string,
) error {
	if _, err := encodeScopes(scopes); err != nil {
		return err
	}
	return s.GrantConsentFor(ctx,
		ConsentKey{PrincipalID: principalID, ClientID: clientID}, scopes)
}

// Consent returns the active grant recorded with no redirect URI and no resource. It
// is the read side of GrantConsent and matches that one row only.
func (s *SQLiteStore) Consent(ctx context.Context, principalID, clientID string) (Consent, error) {
	return s.ConsentFor(ctx, ConsentKey{PrincipalID: principalID, ClientID: clientID})
}

// hasActiveConsentSQL asks whether a principal still authorizes a client for anything
// at all. It is deliberately not keyed on the redirect URI or the resource: the paths
// that use it — issuing into a family, reading a token — know the client and the
// resource but never the redirect URI, so an exact-key test is impossible there. The
// exact key is enforced where the authorization decision is made; this is the
// fail-closed backstop that keeps a token from outliving every grant that justified it.
const hasActiveConsentSQL = `SELECT EXISTS (SELECT 1 FROM consents
     WHERE principal_id = ? AND client_id = ? AND revoked_at IS NULL)`

// requireActiveConsent refuses to issue against a withdrawn or absent grant.
func requireActiveConsent(ctx context.Context, tx *sql.Tx, principalID, clientID string) error {
	var active bool
	err := tx.QueryRowContext(ctx, hasActiveConsentSQL, principalID, clientID).Scan(&active)
	if err != nil {
		return fmt.Errorf("store: read consent: %w", err)
	}
	if !active {
		return fmt.Errorf("store: principal %s has no active grant for client %s: %w",
			principalID, clientID, ErrConsentNotFound)
	}
	return nil
}
