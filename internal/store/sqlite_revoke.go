package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Revocation cascades.
//
// Every cascade here is one transaction and is idempotent. Running it twice reaches
// the same state and reports no error the second time, because a revocation is
// something an operator, a user and a security response may all trigger at once, and
// none of them should see a failure for being second.
//
// Each cascade also fails closed: after the writes, the transaction re-counts what
// must be gone or revoked, and rolls back with ErrIncompleteUnlink if anything is
// left. A half-unlinked principal — Garmin tokens deleted but MCP tokens still live,
// or the reverse — is the one outcome that must be impossible.

// Revocation reason codes. They are constants rather than caller text, so the column
// can never carry a token, an email or a free-form message from a request.
const (
	reasonRefreshReuse   = "refresh_token_reuse"
	reasonConsentRevoked = "consent_revoked"
	reasonGarminUnlinked = "garmin_unlinked"
)

// maxReasonLength bounds a reason code.
const maxReasonLength = 64

// RevocationResult counts what a cascade changed. Every field is zero on a repeat
// run, which is how a caller tells an effective revocation from an idempotent no-op
// without the distinction being an error.
type RevocationResult struct {
	// FamiliesRevoked is the number of token families this call revoked.
	FamiliesRevoked int

	// TokensRevoked is the number of individual access and refresh tokens marked
	// revoked.
	TokensRevoked int

	// TransactionsDeleted and CodesDeleted count the pending authorization state
	// this call removed.
	TransactionsDeleted int
	CodesDeleted        int

	// GarminTokensDeleted is 1 when an encrypted Garmin DI token record was
	// removed. Deleting it is unlinking, never revocation at Garmin.
	GarminTokensDeleted int
}

// RevokeTokenFamily revokes one family and every token in it.
//
// It is idempotent: revoking an already revoked family reports no error and a zero
// RevocationResult. An unknown family reports ErrTokenNotFound, because silently
// succeeding on a family id nobody recognizes would hide a caller bug.
func (s *SQLiteStore) RevokeTokenFamily(ctx context.Context, familyID, reason string,
) (RevocationResult, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return RevocationResult{}, err
	}
	if err := checkIdentifier("family id", familyID); err != nil {
		return RevocationResult{}, err
	}
	if err := checkReasonCode(reason); err != nil {
		return RevocationResult{}, err
	}

	var result RevocationResult
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if err := requireFamilyExists(ctx, tx, familyID); err != nil {
			return err
		}
		revoked, err := s.revokeFamilyIn(ctx, tx, familyID, reason)
		result = revoked
		return err
	})
	if err != nil {
		return RevocationResult{}, err
	}
	return result, nil
}

// checkReasonCode requires a short lowercase snake-case code.
//
// The column is part of the audit trail, so it must never become a place where
// request text lands. The grammar is narrow enough that nothing interesting fits.
func checkReasonCode(reason string) error {
	if reason == "" || len(reason) > maxReasonLength {
		return fmt.Errorf("store: revocation reason has length %d: %w",
			len(reason), ErrInvalidArgument)
	}
	if strings.HasSuffix(reason, "_") {
		return fmt.Errorf("store: revocation reason %q ends in an underscore: %w",
			reason, ErrInvalidArgument)
	}
	for index, char := range reason {
		lower := char >= 'a' && char <= 'z'
		digit := char >= '0' && char <= '9'
		interiorUnderscore := char == '_' && index > 0
		if !lower && !digit && !interiorUnderscore {
			return fmt.Errorf("store: revocation reason %q is not a lowercase snake-case code: %w",
				reason, ErrInvalidArgument)
		}
	}
	return nil
}

func requireFamilyExists(ctx context.Context, tx *sql.Tx, familyID string) error {
	var count int
	err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM token_families WHERE id = ?`, familyID).Scan(&count)
	if err != nil {
		return fmt.Errorf("store: read token family: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("store: token family %s is unknown: %w", familyID, ErrTokenNotFound)
	}
	return nil
}

// revokeFamilyIn revokes one family and its tokens inside an existing transaction.
// Both statements skip rows that are already revoked, which is what makes a repeat run
// report zero rather than fail.
func (s *SQLiteStore) revokeFamilyIn(ctx context.Context, tx *sql.Tx, familyID, reason string,
) (RevocationResult, error) {
	return s.revokeFamiliesWhere(ctx, tx, `id = ?`, `family_id = ?`, reason, familyID)
}

// RevokeConsent withdraws a principal's grant to one client and cascades.
//
// In one transaction it marks the consent revoked, revokes every token family that
// client holds for that principal, and deletes the client's pending authorization
// transactions and unconsumed codes for that principal. It fails closed: if any family
// for the pair is still unrevoked afterwards, the transaction rolls back with
// ErrIncompleteUnlink.
//
// It is idempotent, and it does not touch the principal's Garmin tokens: revoking one
// client's access is not unlinking the Garmin account. Use UnlinkGarminAccount for
// that.
func (s *SQLiteStore) RevokeConsent(ctx context.Context, principalID, clientID string,
) (RevocationResult, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return RevocationResult{}, err
	}
	if err := checkIdentifier("principal id", principalID); err != nil {
		return RevocationResult{}, err
	}
	if err := checkIdentifier("client id", clientID); err != nil {
		return RevocationResult{}, err
	}

	var result RevocationResult
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		revoked, err := s.applyConsentRevocation(ctx, tx, principalID, clientID)
		result = revoked
		return err
	})
	if err != nil {
		return RevocationResult{}, err
	}
	return result, nil
}

// applyConsentRevocation is the transactional body of RevokeConsent.
func (s *SQLiteStore) applyConsentRevocation(ctx context.Context, tx *sql.Tx,
	principalID, clientID string,
) (RevocationResult, error) {
	if _, err := tx.ExecContext(ctx,
		`UPDATE consents SET revoked_at = ?
		  WHERE principal_id = ? AND client_id = ? AND revoked_at IS NULL`,
		formatTime(s.now()), principalID, clientID); err != nil {
		return RevocationResult{}, fmt.Errorf("store: revoke consent: %w", err)
	}

	result, err := s.revokeFamiliesWhere(ctx, tx,
		`principal_id = ? AND client_id = ?`,
		`family_id IN (SELECT id FROM token_families WHERE principal_id = ? AND client_id = ?)`,
		reasonConsentRevoked, principalID, clientID)
	if err != nil {
		return RevocationResult{}, err
	}

	pending, err := s.deletePendingAuthState(ctx, tx, principalID, clientID)
	if err != nil {
		return RevocationResult{}, err
	}
	result.TransactionsDeleted = pending.TransactionsDeleted
	result.CodesDeleted = pending.CodesDeleted

	remaining, err := countRows(tx,
		`SELECT count(*) FROM token_families
		  WHERE principal_id = ? AND client_id = ? AND revoked_at IS NULL`,
		principalID, clientID)
	if err != nil {
		return RevocationResult{}, err
	}
	if remaining != 0 {
		return RevocationResult{}, fmt.Errorf(
			"store: %d token families for principal %s and client %s survived revocation: %w",
			remaining, principalID, clientID, ErrIncompleteUnlink)
	}
	return result, nil
}

// revokeFamiliesWhere revokes every family matching familyPredicate, and every token
// matching tokenPredicate.
//
// Both predicates are literals from this file and both take the same bound arguments;
// only the values come from a caller. Two predicates rather than one because the
// family table is filtered directly while the token table has to be filtered through
// a subquery on the same columns.
func (s *SQLiteStore) revokeFamiliesWhere(ctx context.Context, tx *sql.Tx,
	familyPredicate, tokenPredicate, reason string, args ...any,
) (RevocationResult, error) {
	now := formatTime(s.now())
	familyArgs := append([]any{now, reason}, args...)
	familyResult, err := tx.ExecContext(ctx,
		`UPDATE token_families SET revoked_at = ?, revocation_reason = ?
		  WHERE `+familyPredicate+` AND revoked_at IS NULL`, familyArgs...)
	if err != nil {
		return RevocationResult{}, fmt.Errorf("store: revoke token families: %w", err)
	}
	families, err := affectedRows(familyResult)
	if err != nil {
		return RevocationResult{}, err
	}

	tokenArgs := append([]any{now}, args...)
	tokenResult, err := tx.ExecContext(ctx,
		`UPDATE mcp_tokens SET revoked_at = ?
		  WHERE revoked_at IS NULL AND `+tokenPredicate, tokenArgs...)
	if err != nil {
		return RevocationResult{}, fmt.Errorf("store: revoke tokens of families: %w", err)
	}
	tokens, err := affectedRows(tokenResult)
	if err != nil {
		return RevocationResult{}, err
	}
	return RevocationResult{FamiliesRevoked: families, TokensRevoked: tokens}, nil
}

// deletePendingAuthState removes a client's in-flight authorization state for one
// principal. Pending state is deleted rather than marked, because it carries no audit
// value: an authorization that never completed is not a grant.
func (s *SQLiteStore) deletePendingAuthState(ctx context.Context, tx *sql.Tx,
	principalID, clientID string,
) (RevocationResult, error) {
	transactions, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_transactions WHERE principal_id = ? AND client_id = ?`,
		principalID, clientID)
	if err != nil {
		return RevocationResult{}, err
	}
	codes, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_codes WHERE principal_id = ? AND client_id = ?`,
		principalID, clientID)
	if err != nil {
		return RevocationResult{}, err
	}
	return RevocationResult{TransactionsDeleted: transactions, CodesDeleted: codes}, nil
}

func deleteCounted(ctx context.Context, tx *sql.Tx, query string, args ...any) (int, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("store: delete rows: %w", err)
	}
	return affectedRows(result)
}

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
