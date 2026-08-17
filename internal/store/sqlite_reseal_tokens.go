package store

import (
	"context"
	"fmt"
)

// resealAllGarminTokenSets pages through every garmin_token_sets row exactly
// once, oldest principal_id first, and returns the total rows actually
// rewritten.
//
// The scan does not filter on the key_version column: that column is exactly
// what a killed pass can leave disagreeing with the sealed content (recorded
// before the content was rewritten, or the reverse), so trusting it to select
// candidates can leave a row silently unsealed forever. Every row is read once
// and handed to rewriteGarminTokenSet, which authenticates the actual content
// version through planReseal and only ever changes what disagrees with active.
func (s *SQLiteStore) resealAllGarminTokenSets(ctx context.Context) (int, error) {
	active, err := s.crypt.activeVersion()
	if err != nil {
		return 0, err
	}

	total := 0
	after := ""
	for {
		candidates, err := s.scanGarminTokenSetCandidates(ctx, after)
		if err != nil {
			return total, err
		}
		if len(candidates) == 0 {
			return total, nil
		}
		for _, c := range candidates {
			rewritten, err := s.rewriteGarminTokenSet(ctx, active, c)
			if err != nil {
				return total, err
			}
			// Zero affected rows means a concurrent write already moved this row
			// past what was just read — either a refresh committed under the
			// active key, or another reseal pass got here first. Either way the
			// row no longer needs this rewrite, so it is not an error.
			total += rewritten
			after = c.principal
		}
	}
}

// garminTokenSetCandidate is one row read while paging through the table.
type garminTokenSetCandidate struct {
	principal  string
	schema     int
	version    int64
	keyVersion int
	sealed     []byte
}

// scanGarminTokenSetCandidates reads up to resealBatchSize rows whose
// principal_id sorts after the cursor. It does not filter on key_version: see
// resealAllGarminTokenSets for why the column cannot be trusted to select
// candidates.
func (s *SQLiteStore) scanGarminTokenSetCandidates(
	ctx context.Context, after string,
) ([]garminTokenSetCandidate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT principal_id, record_schema, version, key_version, sealed
		   FROM garmin_token_sets WHERE principal_id > ? ORDER BY principal_id LIMIT ?`,
		after, resealBatchSize)
	if err != nil {
		return nil, fmt.Errorf("store: scan garmin token sets to reseal: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []garminTokenSetCandidate
	for rows.Next() {
		var c garminTokenSetCandidate
		if err := rows.Scan(&c.principal, &c.schema, &c.version, &c.keyVersion, &c.sealed); err != nil {
			return nil, fmt.Errorf("store: read garmin token set to reseal: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: scan garmin token sets to reseal: %w", err)
	}
	return candidates, nil
}

// rewriteGarminTokenSet re-seals one candidate onto active, and reports how many
// rows the UPDATE actually changed (0 or 1).
func (s *SQLiteStore) rewriteGarminTokenSet(
	ctx context.Context, active int, c garminTokenSetCandidate,
) (int, error) {
	plan, err := s.crypt.planReseal(c.principal, recordAAD(c.schema, c.version), c.sealed)
	if err != nil {
		return 0, fmt.Errorf("store: reseal garmin token set for a principal: %w", err)
	}
	if !plan.changed {
		// The content is already sealed under the active key, but the column
		// this batch selects on still names a retired version — a previous
		// pass rewrote the content and was interrupted before recording that.
		// Reconciling the column is what keeps the batch scan from selecting
		// this exact row forever: without it, resealAllGarminTokenSets would
		// loop with zero progress on every pass whenever a row disagrees this
		// way.
		if err := s.reconcileGarminTokenSetKeyVersion(ctx, active, c); err != nil {
			return 0, err
		}
		return 0, nil
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE garmin_token_sets SET sealed = ?, key_version = ?
		  WHERE principal_id = ? AND version = ? AND key_version = ?`,
		plan.sealed, active, c.principal, c.version, c.keyVersion)
	if err != nil {
		return 0, fmt.Errorf("store: write resealed garmin token set: %w", err)
	}
	return affectedRows(result)
}

// reconcileGarminTokenSetKeyVersion brings c's key_version column in line with
// active when the sealed content already matches it. The row's own version
// counter is the compare-and-set predicate, the same one an actual reseal
// write uses, so a concurrent write to the row is never silently overwritten
// here either.
func (s *SQLiteStore) reconcileGarminTokenSetKeyVersion(
	ctx context.Context, active int, c garminTokenSetCandidate,
) error {
	if c.keyVersion == active {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE garmin_token_sets SET key_version = ?
		  WHERE principal_id = ? AND version = ? AND key_version = ?`,
		active, c.principal, c.version, c.keyVersion); err != nil {
		return fmt.Errorf("store: record the active key version for a garmin token set: %w", err)
	}
	return nil
}
