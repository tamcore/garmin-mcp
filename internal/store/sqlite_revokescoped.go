package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Revocation on the exact consent key, and revocation of everything a principal holds.
//
// Both are one transaction, both are idempotent, and both fail closed: after the
// writes the transaction re-counts what must be gone or revoked and rolls back with
// ErrIncompleteUnlink if anything survived. That is the same shape RevokeConsent and
// UnlinkGarminAccount already use.
//
// Why they exist next to those two: RevokeConsent is keyed on the principal and the
// client only, so it takes down every redirect URI and every resource that pair holds.
// That is the right thing for "this client is no longer welcome" and the wrong thing
// for "withdraw this one grant". UnlinkGarminAccount is wider still and deletes the
// Garmin linkage, which revoking a principal's MCP tokens must not do.

// RevokeConsentFor withdraws exactly one grant and cascades to what that grant issued.
//
// In one transaction it marks the consent for the exact key revoked, revokes every
// token family for that principal, client and resource, and deletes the pending
// authorization state that names the same redirect URI and resource. A grant that
// differs in redirect URI or resource is untouched, which is the whole reason the key
// is wide.
//
// It is idempotent: a second call reports a zero result and no error, and an unknown
// key is not an error either. It does not touch the principal's Garmin tokens.
func (s *SQLiteStore) RevokeConsentFor(ctx context.Context, key ConsentKey,
) (RevocationResult, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return RevocationResult{}, err
	}
	if err := key.check(); err != nil {
		return RevocationResult{}, err
	}

	var result RevocationResult
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		revoked, err := s.applyScopedRevocation(ctx, tx, key)
		result = revoked
		return err
	})
	if err != nil {
		return RevocationResult{}, err
	}
	return result, nil
}

// applyScopedRevocation is the transactional body of RevokeConsentFor.
func (s *SQLiteStore) applyScopedRevocation(ctx context.Context, tx *sql.Tx, key ConsentKey,
) (RevocationResult, error) {
	if _, err := tx.ExecContext(ctx,
		`UPDATE consents SET revoked_at = ?
		  WHERE principal_id = ? AND client_id = ? AND redirect_uri = ? AND resource = ?
		    AND revoked_at IS NULL`,
		formatTime(s.now()), key.PrincipalID, key.ClientID, key.RedirectURI,
		key.Resource); err != nil {
		return RevocationResult{}, fmt.Errorf("store: revoke consent: %w", err)
	}

	result, err := s.revokeFamiliesWhere(ctx, tx,
		`principal_id = ? AND client_id = ? AND resource = ?`,
		`family_id IN (SELECT id FROM token_families
		     WHERE principal_id = ? AND client_id = ? AND resource = ?)`,
		reasonConsentRevoked, key.PrincipalID, key.ClientID, key.Resource)
	if err != nil {
		return RevocationResult{}, err
	}
	if err := s.deleteScopedAuthState(ctx, tx, key, &result); err != nil {
		return RevocationResult{}, err
	}
	return result, confirmScopedRevocation(tx, key)
}

// deleteScopedAuthState removes the in-flight authorization state that named the same
// redirect URI and resource. Pending state is deleted rather than marked, because an
// authorization that never completed is not a grant and carries no audit value.
func (s *SQLiteStore) deleteScopedAuthState(ctx context.Context, tx *sql.Tx, key ConsentKey,
	result *RevocationResult,
) error {
	transactions, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_transactions
		  WHERE principal_id = ? AND client_id = ? AND redirect_uri = ? AND resource = ?`,
		key.PrincipalID, key.ClientID, key.RedirectURI, key.Resource)
	if err != nil {
		return err
	}
	codes, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_codes
		  WHERE principal_id = ? AND client_id = ? AND redirect_uri = ? AND audience = ?`,
		key.PrincipalID, key.ClientID, key.RedirectURI, key.Resource)
	if err != nil {
		return err
	}
	result.TransactionsDeleted = transactions
	result.CodesDeleted = codes
	return nil
}

// confirmScopedRevocation is the fail-closed post-condition of RevokeConsentFor.
func confirmScopedRevocation(tx *sql.Tx, key ConsentKey) error {
	families, err := countRows(tx,
		`SELECT count(*) FROM token_families
		  WHERE principal_id = ? AND client_id = ? AND resource = ? AND revoked_at IS NULL`,
		key.PrincipalID, key.ClientID, key.Resource)
	if err != nil {
		return err
	}
	if families != 0 {
		return fmt.Errorf("store: %d token families for principal %s and client %s survived "+
			"revocation: %w", families, key.PrincipalID, key.ClientID, ErrIncompleteUnlink)
	}

	consents, err := countRows(tx,
		`SELECT count(*) FROM consents
		  WHERE principal_id = ? AND client_id = ? AND redirect_uri = ? AND resource = ?
		    AND revoked_at IS NULL`,
		key.PrincipalID, key.ClientID, key.RedirectURI, key.Resource)
	if err != nil {
		return err
	}
	if consents != 0 {
		return fmt.Errorf("store: the consent for principal %s and client %s survived "+
			"revocation: %w", key.PrincipalID, key.ClientID, ErrIncompleteUnlink)
	}
	return nil
}

// RevokePrincipalTokens revokes everything one principal has granted and been issued.
//
// In one transaction it revokes every token family the principal holds with any
// client, revokes every one of its consents, and deletes its pending authorization
// transactions and codes. It then re-counts all of them and rolls back with
// ErrIncompleteUnlink if anything survived.
//
// It is idempotent, and a principal that does not exist is a zero result rather than
// an error: a caller revoking what is already gone has got what it asked for.
//
// It is deliberately narrower than UnlinkGarminAccount. It does not delete the Garmin
// DI token record and it does not clear the Garmin identity linkage, so a principal
// whose MCP access has been revoked can log in again without re-linking. It revokes
// nothing at Garmin either.
//
// Consents are marked revoked rather than deleted, which is the convention everywhere
// in this schema: the row recording that a grant once existed survives, and no revoked
// consent is ever returned by a read.
func (s *SQLiteStore) RevokePrincipalTokens(ctx context.Context, principalID string,
) (RevocationResult, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return RevocationResult{}, err
	}
	if err := checkIdentifier("principal id", principalID); err != nil {
		return RevocationResult{}, err
	}

	var result RevocationResult
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		revoked, err := s.applyPrincipalRevocation(ctx, tx, principalID)
		result = revoked
		return err
	})
	if err != nil {
		return RevocationResult{}, err
	}
	return result, nil
}

// applyPrincipalRevocation is the transactional body of RevokePrincipalTokens.
func (s *SQLiteStore) applyPrincipalRevocation(ctx context.Context, tx *sql.Tx, principalID string,
) (RevocationResult, error) {
	result, err := s.revokeFamiliesWhere(ctx, tx,
		`principal_id = ?`,
		`family_id IN (SELECT id FROM token_families WHERE principal_id = ?)`,
		reasonConsentRevoked, principalID)
	if err != nil {
		return RevocationResult{}, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE consents SET revoked_at = ? WHERE principal_id = ? AND revoked_at IS NULL`,
		formatTime(s.now()), principalID); err != nil {
		return RevocationResult{}, fmt.Errorf("store: revoke consents of principal: %w", err)
	}

	transactions, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_transactions WHERE principal_id = ?`, principalID)
	if err != nil {
		return RevocationResult{}, err
	}
	codes, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_codes WHERE principal_id = ?`, principalID)
	if err != nil {
		return RevocationResult{}, err
	}
	result.TransactionsDeleted = transactions
	result.CodesDeleted = codes
	return result, confirmPrincipalRevoked(tx, principalID)
}

// principalRevocationPostConditions is what must be true before a principal revocation
// may commit.
func principalRevocationPostConditions() []struct{ what, query string } {
	return []struct{ what, query string }{
		{"live token families", `SELECT count(*) FROM token_families
		     WHERE principal_id = ? AND revoked_at IS NULL`},
		{"live tokens", `SELECT count(*) FROM mcp_tokens
		     WHERE revoked_at IS NULL AND family_id IN
		           (SELECT id FROM token_families WHERE principal_id = ?)`},
		{"active consents", `SELECT count(*) FROM consents
		     WHERE principal_id = ? AND revoked_at IS NULL`},
		{"authorization transactions", `SELECT count(*) FROM auth_transactions WHERE principal_id = ?`},
		{"authorization codes", `SELECT count(*) FROM auth_codes WHERE principal_id = ?`},
	}
}

// confirmPrincipalRevoked queries rather than trusting the row counts above, because a
// concurrent insert or a mistaken predicate would be invisible to a count.
func confirmPrincipalRevoked(tx *sql.Tx, principalID string) error {
	for _, check := range principalRevocationPostConditions() {
		remaining, err := countRows(tx, check.query, principalID)
		if err != nil {
			return err
		}
		if remaining != 0 {
			return fmt.Errorf("store: %d %s survived the revocation of principal %s: %w",
				remaining, check.what, principalID, ErrIncompleteUnlink)
		}
	}
	return nil
}
