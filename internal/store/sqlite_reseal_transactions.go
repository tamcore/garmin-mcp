package store

import (
	"context"
	"fmt"
)

// resealAllAuthTransactionStates pages through every pending authorization
// transaction carrying a client state exactly once, oldest handle_hash first,
// and returns the total rows actually rewritten.
//
// The scan does not filter on the client_state_key_version column: see
// resealAllGarminTokenSets for why that column cannot be trusted to select
// candidates. rewriteAuthTransactionState authenticates the actual content
// version through planReseal and only changes what disagrees with active.
func (s *SQLiteStore) resealAllAuthTransactionStates(ctx context.Context) (int, error) {
	active, err := s.crypt.activeVersion()
	if err != nil {
		return 0, err
	}

	total := 0
	after := ""
	for {
		candidates, err := s.scanAuthTransactionStateCandidates(ctx, after)
		if err != nil {
			return total, err
		}
		if len(candidates) == 0 {
			return total, nil
		}
		for _, c := range candidates {
			rewritten, err := s.rewriteAuthTransactionState(ctx, active, c)
			if err != nil {
				return total, err
			}
			total += rewritten
			after = c.hash
		}
	}
}

// authTransactionStateCandidate is one transaction read while paging through
// the table.
type authTransactionStateCandidate struct {
	hash       string
	version    int64
	keyVersion int
	sealed     []byte
}

// scanAuthTransactionStateCandidates reads up to resealBatchSize transaction
// rows whose handle_hash sorts after the cursor. It does not filter on
// client_state_key_version: see resealAllGarminTokenSets for why the column
// cannot be trusted to select candidates.
func (s *SQLiteStore) scanAuthTransactionStateCandidates(
	ctx context.Context, after string,
) ([]authTransactionStateCandidate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT handle_hash, version, client_state_key_version, client_state_sealed
		   FROM auth_transactions
		  WHERE handle_hash > ? AND client_state_key_version IS NOT NULL
		  ORDER BY handle_hash LIMIT ?`, after, resealBatchSize)
	if err != nil {
		return nil, fmt.Errorf("store: scan authorization transaction states to reseal: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []authTransactionStateCandidate
	for rows.Next() {
		var c authTransactionStateCandidate
		if err := rows.Scan(&c.hash, &c.version, &c.keyVersion, &c.sealed); err != nil {
			return nil, fmt.Errorf("store: read authorization transaction state to reseal: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: scan authorization transaction states to reseal: %w", err)
	}
	return candidates, nil
}

// rewriteAuthTransactionState re-seals one candidate onto active, and reports how
// many rows the UPDATE actually changed (0 or 1).
func (s *SQLiteStore) rewriteAuthTransactionState(
	ctx context.Context, active int, c authTransactionStateCandidate,
) (int, error) {
	plan, err := s.crypt.planReseal(c.hash, clientStateRecordType, c.sealed)
	if err != nil {
		return 0, fmt.Errorf("store: reseal an authorization transaction state: %w", err)
	}
	if !plan.changed {
		// The content already matches the active key, but the column this
		// batch selects on still names a retired version — a previous pass
		// rewrote the content and was interrupted before recording that.
		// Reconciling the column is what keeps the batch scan from selecting
		// this exact row forever: without it,
		// resealAllAuthTransactionStates would loop with zero progress on
		// every pass whenever a row disagrees this way.
		if err := s.reconcileAuthTransactionStateKeyVersion(ctx, active, c); err != nil {
			return 0, err
		}
		return 0, nil
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE auth_transactions SET client_state_sealed = ?, client_state_key_version = ?
		  WHERE handle_hash = ? AND version = ? AND client_state_key_version = ?`,
		plan.sealed, active, c.hash, c.version, c.keyVersion)
	if err != nil {
		return 0, fmt.Errorf("store: write resealed authorization transaction state: %w", err)
	}
	return affectedRows(result)
}

// reconcileAuthTransactionStateKeyVersion brings c's client_state_key_version
// column in line with active when the sealed content already matches it. The
// transaction's own version counter is the compare-and-set predicate, the
// same one an actual reseal write uses.
func (s *SQLiteStore) reconcileAuthTransactionStateKeyVersion(
	ctx context.Context, active int, c authTransactionStateCandidate,
) error {
	if c.keyVersion == active {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE auth_transactions SET client_state_key_version = ?
		  WHERE handle_hash = ? AND version = ? AND client_state_key_version = ?`,
		active, c.hash, c.version, c.keyVersion); err != nil {
		return fmt.Errorf("store: record the active key version for an authorization transaction state: %w", err)
	}
	return nil
}

// resealIndexRoot re-seals the single database-wide index root when it is not
// already sealed under the active key, and reports whether it changed anything.
// The compare-and-set predicate is content equality on the sealed column, the
// same reasoning as resealPrincipalIdentitiesBatch: this row carries no separate
// version counter of its own.
func (s *SQLiteStore) resealIndexRoot(ctx context.Context) (bool, error) {
	active, err := s.crypt.activeVersion()
	if err != nil {
		return false, err
	}
	var (
		recorded int
		sealed   []byte
	)
	err = s.db.QueryRowContext(ctx,
		`SELECT encryption_key_version, index_root_sealed FROM schema_meta WHERE id = 1`).
		Scan(&recorded, &sealed)
	if err != nil {
		return false, fmt.Errorf("store: read schema metadata to reseal: %w", err)
	}

	plan, err := s.crypt.planReseal(indexRootPrincipal, indexRootRecordType, sealed)
	if err != nil {
		return false, fmt.Errorf("store: reseal the index root: %w", err)
	}
	if !plan.changed {
		// The row may still record a stale default version even though its
		// content already matches the active key, if a previous pass rewrote the
		// content but was interrupted before this update. Bring the recorded
		// default in line so the completion scan agrees with the content.
		if recorded != active {
			if _, err := s.db.ExecContext(ctx,
				`UPDATE schema_meta SET encryption_key_version = ? WHERE id = 1 AND index_root_sealed = ?`,
				active, sealed); err != nil {
				return false, fmt.Errorf("store: record the active key version: %w", err)
			}
		}
		return false, nil
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE schema_meta SET index_root_sealed = ?, encryption_key_version = ?
		  WHERE id = 1 AND encryption_key_version = ? AND index_root_sealed = ?`,
		plan.sealed, active, recorded, sealed)
	if err != nil {
		return false, fmt.Errorf("store: write resealed index root: %w", err)
	}
	rewritten, err := affectedRows(result)
	if err != nil {
		return false, err
	}
	return rewritten > 0, nil
}
