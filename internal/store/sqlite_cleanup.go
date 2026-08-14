package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Bounded periodic cleanup.
//
// Cleanup is a housekeeping job, not a security mechanism. Nothing depends on it
// having run: every read applies its own expiry predicate, so an expired transaction,
// code or token is refused even on a database that has never been cleaned. What
// cleanup buys is bounded growth, and it is deliberately bounded per call so a large
// backlog cannot turn one maintenance tick into a long write transaction that starves
// live requests behind SQLite's single writer.

// Cleanup bounds.
const (
	// defaultCleanupLimit is how many rows one table gives up per call when the
	// caller passes zero.
	defaultCleanupLimit = 500

	// maxCleanupLimit caps what a caller may ask for, so no single call can hold
	// the write lock for an unbounded time.
	maxCleanupLimit = 5000

	// revokedTokenRetention is how long a revoked token row is kept past its expiry,
	// so an operator investigating a revocation can still see it.
	revokedTokenRetention = 24 * time.Hour
)

// CleanupStats counts what one Cleanup call removed. A caller that sees any field at
// the limit should call again: the backlog was larger than one pass.
type CleanupStats struct {
	// Transactions and Codes are expired authorization state.
	Transactions int
	Codes        int

	// Tokens are expired access and refresh token rows.
	Tokens int

	// Families are token families left with no token rows.
	Families int
}

// AtLimit reports whether any table gave up a full batch, which means more remains.
func (c CleanupStats) AtLimit(limit int) bool {
	return c.Transactions >= limit || c.Codes >= limit || c.Tokens >= limit || c.Families >= limit
}

// Cleanup removes expired authorization transactions, authorization codes and tokens,
// and then any token family left with no tokens.
//
// limit bounds how many rows each table gives up; zero selects the default and a value
// over the cap is refused. Every delete happens in one transaction, so a failure
// part-way removes nothing.
//
// Cleanup is safe to run concurrently with live traffic and safe to run twice. It is
// not coordinated across processes: two instances would each do the work, which is
// wasteful but not incorrect.
func (s *SQLiteStore) Cleanup(ctx context.Context, limit int) (CleanupStats, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return CleanupStats{}, err
	}
	if limit < 0 || limit > maxCleanupLimit {
		return CleanupStats{}, fmt.Errorf("store: cleanup limit %d is outside [0, %d]: %w",
			limit, maxCleanupLimit, ErrInvalidArgument)
	}
	if limit == 0 {
		limit = defaultCleanupLimit
	}

	var stats CleanupStats
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		collected, err := s.collectExpired(ctx, tx, limit)
		stats = collected
		return err
	})
	if err != nil {
		return CleanupStats{}, err
	}
	return stats, nil
}

// collectExpired is the transactional body of Cleanup.
//
// The deletes use "rowid IN (SELECT rowid ... LIMIT ?)" rather than a bare predicate,
// because SQLite accepts LIMIT on DELETE only when it was compiled with an optional
// feature. The subquery form is portable and gives the same bound.
func (s *SQLiteStore) collectExpired(ctx context.Context, tx *sql.Tx, limit int,
) (CleanupStats, error) {
	now := formatTime(s.now())
	transactions, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_transactions WHERE rowid IN
		     (SELECT rowid FROM auth_transactions WHERE expires_at <= ? LIMIT ?)`, now, limit)
	if err != nil {
		return CleanupStats{}, err
	}
	codes, err := deleteCounted(ctx, tx,
		`DELETE FROM auth_codes WHERE rowid IN
		     (SELECT rowid FROM auth_codes WHERE expires_at <= ? LIMIT ?)`, now, limit)
	if err != nil {
		return CleanupStats{}, err
	}
	tokens, err := s.deleteExpiredTokens(ctx, tx, limit)
	if err != nil {
		return CleanupStats{}, err
	}
	families, err := deleteCounted(ctx, tx,
		`DELETE FROM token_families WHERE rowid IN
		     (SELECT f.rowid FROM token_families f
		       WHERE NOT EXISTS (SELECT 1 FROM mcp_tokens t WHERE t.family_id = f.id) LIMIT ?)`,
		limit)
	if err != nil {
		return CleanupStats{}, err
	}
	return CleanupStats{
		Transactions: transactions,
		Codes:        codes,
		Tokens:       tokens,
		Families:     families,
	}, nil
}

// deleteExpiredTokens removes expired token rows. A revoked row is kept for a grace
// period past its expiry, so the row an operator would want to look at after a family
// revocation is still there.
func (s *SQLiteStore) deleteExpiredTokens(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	now := formatTime(s.now())
	cutoff := formatTime(s.now().Add(-revokedTokenRetention))
	return deleteCounted(ctx, tx,
		`DELETE FROM mcp_tokens WHERE rowid IN
		     (SELECT rowid FROM mcp_tokens
		       WHERE expires_at <= ? AND (revoked_at IS NULL OR expires_at <= ?) LIMIT ?)`,
		now, cutoff, limit)
}
