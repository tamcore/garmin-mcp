package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Unlinking a Garmin account: the widest cascade in the store, and the only one that
// deletes the encrypted Garmin token record and the identity linkage.

// UnlinkGarminAccount removes a principal's Garmin linkage and everything that
// depended on it.
//
// In one transaction it revokes every token family the principal has with any client,
// deletes the encrypted Garmin DI token record, deletes every pending authorization
// transaction and code for the principal, and clears the Garmin account hash and the
// sealed identity. It then re-counts all six and rolls back with ErrIncompleteUnlink
// if anything survived, so a partial unlink cannot be committed.
//
// It is idempotent: unlinking an already unlinked principal reports no error and a
// zero result. An unknown principal reports ErrPrincipalNotFound.
//
// This is not revocation at Garmin. The DI refresh token stays valid at Garmin's
// service until Garmin expires or revokes it, and any copy that was already taken
// keeps working. A caller must report "local tokens removed", never "tokens revoked".
func (s *SQLiteStore) UnlinkGarminAccount(ctx context.Context, principalID string,
) (RevocationResult, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return RevocationResult{}, err
	}
	if err := checkIdentifier("principal id", principalID); err != nil {
		return RevocationResult{}, err
	}

	var result RevocationResult
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		unlinked, err := s.applyUnlink(ctx, tx, principalID)
		result = unlinked
		return err
	})
	if err != nil {
		return RevocationResult{}, err
	}
	s.publishRevocation(RevocationEvent{
		PrincipalID: principalID,
		Reason:      reasonGarminUnlinked,
	})
	return result, nil
}

// applyUnlink is the transactional body of UnlinkGarminAccount.
func (s *SQLiteStore) applyUnlink(ctx context.Context, tx *sql.Tx, principalID string,
) (RevocationResult, error) {
	if err := requirePrincipalExists(ctx, tx, principalID); err != nil {
		return RevocationResult{}, err
	}

	result, err := s.revokeFamiliesWhere(ctx, tx,
		`principal_id = ?`,
		`family_id IN (SELECT id FROM token_families WHERE principal_id = ?)`,
		reasonGarminUnlinked, principalID)
	if err != nil {
		return RevocationResult{}, err
	}
	if err := s.deletePrincipalAuthState(ctx, tx, principalID, &result); err != nil {
		return RevocationResult{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE principals
		    SET garmin_account_hash = NULL, garmin_identity_sealed = NULL, updated_at = ?
		  WHERE id = ?`, formatTime(s.now()), principalID); err != nil {
		return RevocationResult{}, fmt.Errorf("store: clear garmin linkage: %w", err)
	}
	if err := confirmUnlinked(tx, principalID); err != nil {
		return RevocationResult{}, err
	}
	return result, nil
}

// deletePrincipalAuthState removes the Garmin token record and the pending
// authorization state of one principal, accumulating the counts into result.
func (s *SQLiteStore) deletePrincipalAuthState(ctx context.Context, tx *sql.Tx,
	principalID string, result *RevocationResult,
) error {
	tokens, err := deleteCounted(ctx, tx,
		`DELETE FROM garmin_token_sets WHERE principal_id = ?`, principalID)
	if err != nil {
		return err
	}
	transactions, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_transactions WHERE principal_id = ?`, principalID)
	if err != nil {
		return err
	}
	codes, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_codes WHERE principal_id = ?`, principalID)
	if err != nil {
		return err
	}
	result.GarminTokensDeleted = tokens
	result.TransactionsDeleted = transactions
	result.CodesDeleted = codes
	return nil
}

// unlinkPostConditions is what must be true before an unlink may commit.
func unlinkPostConditions() []struct{ what, query string } {
	return []struct{ what, query string }{
		{"live token families", `SELECT count(*) FROM token_families
		     WHERE principal_id = ? AND revoked_at IS NULL`},
		{"live tokens", `SELECT count(*) FROM mcp_tokens
		     WHERE revoked_at IS NULL AND family_id IN
		           (SELECT id FROM token_families WHERE principal_id = ?)`},
		{"garmin token records", `SELECT count(*) FROM garmin_token_sets WHERE principal_id = ?`},
		{"authorization transactions", `SELECT count(*) FROM auth_transactions WHERE principal_id = ?`},
		{"authorization codes", `SELECT count(*) FROM auth_codes WHERE principal_id = ?`},
		{"garmin linkages", `SELECT count(*) FROM principals
		     WHERE id = ? AND (garmin_account_hash IS NOT NULL OR garmin_identity_sealed IS NOT NULL)`},
	}
}

// confirmUnlinked is the fail-closed post-condition: nothing that had to go may
// remain. It queries rather than trusting the row counts above, because a concurrent
// insert or a mistaken predicate would be invisible to a count.
func confirmUnlinked(tx *sql.Tx, principalID string) error {
	for _, check := range unlinkPostConditions() {
		remaining, err := countRows(tx, check.query, principalID)
		if err != nil {
			return err
		}
		if remaining != 0 {
			return fmt.Errorf("store: %d %s survived the unlink of principal %s: %w",
				remaining, check.what, principalID, ErrIncompleteUnlink)
		}
	}
	return nil
}

func requirePrincipalExists(ctx context.Context, tx *sql.Tx, principalID string) error {
	var count int
	err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM principals WHERE id = ?`, principalID).Scan(&count)
	if err != nil {
		return fmt.Errorf("store: read principal: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("store: principal %s does not exist: %w", principalID, ErrPrincipalNotFound)
	}
	return nil
}
