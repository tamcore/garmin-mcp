package store

import (
	"context"
	"fmt"
)

// resealAllPrincipalIdentities pages through every linked principal exactly
// once, oldest id first, and returns the total rows actually rewritten.
//
// The scan does not filter on the key_version column: see
// resealAllGarminTokenSets for why that column cannot be trusted to select
// candidates. rewritePrincipalIdentity authenticates the actual content
// version through planReseal and only changes what disagrees with active.
func (s *SQLiteStore) resealAllPrincipalIdentities(ctx context.Context) (int, error) {
	active, err := s.crypt.activeVersion()
	if err != nil {
		return 0, err
	}

	total := 0
	after := ""
	for {
		candidates, err := s.scanPrincipalIdentityCandidates(ctx, after)
		if err != nil {
			return total, err
		}
		if len(candidates) == 0 {
			return total, nil
		}
		for _, c := range candidates {
			rewritten, err := s.rewritePrincipalIdentity(ctx, active, c)
			if err != nil {
				return total, err
			}
			total += rewritten
			after = c.id
		}
	}
}

// principalIdentityCandidate is one linked principal read while paging through
// the table.
type principalIdentityCandidate struct {
	id         string
	keyVersion int
	sealed     []byte
}

// scanPrincipalIdentityCandidates reads up to resealBatchSize linked principal
// rows whose id sorts after the cursor. It does not filter on key_version: see
// resealAllGarminTokenSets for why the column cannot be trusted to select
// candidates.
func (s *SQLiteStore) scanPrincipalIdentityCandidates(
	ctx context.Context, after string,
) ([]principalIdentityCandidate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, key_version, garmin_identity_sealed FROM principals
		  WHERE id > ? AND garmin_identity_sealed IS NOT NULL
		  ORDER BY id LIMIT ?`, after, resealBatchSize)
	if err != nil {
		return nil, fmt.Errorf("store: scan principal identities to reseal: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []principalIdentityCandidate
	for rows.Next() {
		var c principalIdentityCandidate
		if err := rows.Scan(&c.id, &c.keyVersion, &c.sealed); err != nil {
			return nil, fmt.Errorf("store: read principal identity to reseal: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: scan principal identities to reseal: %w", err)
	}
	return candidates, nil
}

// rewritePrincipalIdentity re-seals one candidate onto active, and reports how
// many rows the UPDATE actually changed (0 or 1).
func (s *SQLiteStore) rewritePrincipalIdentity(
	ctx context.Context, active int, c principalIdentityCandidate,
) (int, error) {
	plan, err := s.crypt.planReseal(c.id, garminIdentityRecordType, c.sealed)
	if err != nil {
		return 0, fmt.Errorf("store: reseal a principal identity: %w", err)
	}
	if !plan.changed {
		// The content already matches the active key, but the column this
		// batch selects on still names a retired version — a previous pass
		// rewrote the content and was interrupted before recording that.
		// Reconciling the column is what keeps the batch scan from selecting
		// this exact row forever: without it, resealAllPrincipalIdentities
		// would loop with zero progress on every pass whenever a row
		// disagrees this way.
		if err := s.reconcilePrincipalIdentityKeyVersion(ctx, active, c); err != nil {
			return 0, err
		}
		return 0, nil
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE principals SET garmin_identity_sealed = ?, key_version = ?
		  WHERE id = ? AND key_version = ? AND garmin_identity_sealed = ?`,
		plan.sealed, active, c.id, c.keyVersion, c.sealed)
	if err != nil {
		return 0, fmt.Errorf("store: write resealed principal identity: %w", err)
	}
	return affectedRows(result)
}

// reconcilePrincipalIdentityKeyVersion brings c's key_version column in line
// with active when the sealed content already matches it. A principal carries
// no separate version counter, so content equality on the sealed column is the
// compare-and-set predicate — the same reasoning the package doc comment in
// sqlite_reseal.go gives for why that is sound here.
func (s *SQLiteStore) reconcilePrincipalIdentityKeyVersion(
	ctx context.Context, active int, c principalIdentityCandidate,
) error {
	if c.keyVersion == active {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE principals SET key_version = ?
		  WHERE id = ? AND key_version = ? AND garmin_identity_sealed = ?`,
		active, c.id, c.keyVersion, c.sealed); err != nil {
		return fmt.Errorf("store: record the active key version for a principal identity: %w", err)
	}
	return nil
}
