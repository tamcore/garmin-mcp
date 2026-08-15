package store

import (
	"context"
	"database/sql"
	"errors"
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
// can never carry a token, an email or a free-form message from a request. The
// vocabulary is exported by revocationfeed.go, because a caller has to name one and
// a consumer of the feed reads them.
const (
	reasonRefreshReuse   = ReasonRefreshReuse
	reasonConsentRevoked = ReasonConsentRevoked
	reasonGarminUnlinked = ReasonGarminUnlinked
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

	var (
		result RevocationResult
		event  RevocationEvent
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		owner, err := familyOwner(ctx, tx, familyID)
		if err != nil {
			return err
		}
		revoked, err := s.revokeFamilyIn(ctx, tx, familyID, reason)
		result, event = revoked, owner.event(reason)
		return err
	})
	if err != nil {
		return RevocationResult{}, err
	}
	s.publishRevocation(event)
	return result, nil
}

// familyOwnership is the tuple a family revocation is announced under.
type familyOwnership struct {
	familyID    string
	principalID string
	clientID    string
}

// event projects the ownership onto the announcement for one reason code.
func (f familyOwnership) event(reason string) RevocationEvent {
	return RevocationEvent{
		PrincipalID: f.principalID,
		ClientID:    f.clientID,
		FamilyID:    f.familyID,
		Reason:      reason,
	}
}

// familyOwner reads the principal and client a family belongs to, and reports
// ErrTokenNotFound for a family id nobody recognizes.
//
// The read is inside the revoking transaction on purpose: it is both the existence
// check and the source of the announcement, so the two cannot disagree.
func familyOwner(ctx context.Context, tx *sql.Tx, familyID string) (familyOwnership, error) {
	owner := familyOwnership{familyID: familyID}
	err := tx.QueryRowContext(ctx,
		`SELECT principal_id, client_id FROM token_families WHERE id = ?`, familyID).
		Scan(&owner.principalID, &owner.clientID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return familyOwnership{}, fmt.Errorf("store: token family %s is unknown: %w",
			familyID, ErrTokenNotFound)
	case err != nil:
		return familyOwnership{}, fmt.Errorf("store: read token family: %w", err)
	}
	return owner, nil
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
	s.publishRevocation(RevocationEvent{
		PrincipalID: principalID,
		ClientID:    clientID,
		Reason:      reasonConsentRevoked,
	})
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
