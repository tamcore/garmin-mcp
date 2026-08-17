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

	// retainedConsumedGenerations bounds how many of a live family's most recent
	// consumed-and-expired generations survive a sweep. Without this bound, a
	// continuously rotating client renews its family's liveness on every refresh,
	// so every past generation would be retained for as long as the client keeps
	// refreshing: an hourly refresh cadence alone means roughly 8,760 retained rows
	// per family per year, which is unbounded growth for an authenticated client.
	// 200 is in the low hundreds, which caps per-family overhead hard while still
	// covering many days of rotation at any realistic cadence — at one rotation per
	// hour, 200 generations is over 8 days of replay-detection coverage beyond each
	// row's own expiry.
	retainedConsumedGenerations = 200
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
//
// A consumed refresh row (one already rotated away) is additionally kept past its own
// expiry for as long as BOTH of the following hold: its family holds any OTHER token
// that is neither expired nor revoked, AND the row is within the family's most recent
// retainedConsumedGenerations generations. refreshGrant's pre-check for a
// consumed-and-expired replay (internal/oauthserver/refreshgrant.go) depends on a
// recently-consumed row still existing, and a sweep is not a security boundary that
// gets to decide reuse detection stops mattering on its own schedule — but "the family
// is live" alone is not a bound: a continuously rotating client renews its family's
// liveness on every refresh, so without the generation cap every past generation of an
// active family would be retained forever. The generation cap turns that into bounded
// growth: at most retainedConsumedGenerations extra rows per live family, regardless
// of how long or how often it keeps refreshing.
//
// Both the EXISTS and the MAX(generation) subqueries are indexed lookups by family_id
// (idx_mcp_tokens_family), not table scans: for each expired candidate row they read
// only the handful of sibling rows in the same family.
func (s *SQLiteStore) deleteExpiredTokens(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	now := formatTime(s.now())
	cutoff := formatTime(s.now().Add(-revokedTokenRetention))
	return deleteCounted(ctx, tx,
		`DELETE FROM mcp_tokens WHERE rowid IN
		     (SELECT t.rowid FROM mcp_tokens t
		       WHERE t.expires_at <= ? AND (t.revoked_at IS NULL OR t.expires_at <= ?)
		         AND NOT (
		           t.consumed_at IS NOT NULL
		           AND EXISTS (
		             SELECT 1 FROM mcp_tokens live
		              WHERE live.family_id = t.family_id
		                AND live.revoked_at IS NULL
		                AND live.expires_at > ?
		           )
		           AND t.generation > (
		             SELECT MAX(g.generation) FROM mcp_tokens g WHERE g.family_id = t.family_id
		           ) - ?
		         )
		       LIMIT ?)`,
		now, cutoff, now, retainedConsumedGenerations, limit)
}
