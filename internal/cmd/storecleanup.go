package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// Periodic removal of expired authorization state.
//
// This is housekeeping, not a security control. Every read in internal/store applies
// its own expiry predicate, so an expired transaction, code or token is refused even
// on a database that has never been swept. What the sweep buys is bounded growth:
// without it a long-running deployment keeps every transaction, code and token it
// ever issued.
//
// It runs in the remote deployment only. A stdio process has no multi-user database
// — its state is the single-user encrypted file store — so there is nothing to sweep
// and no cleaner is built.

// Cleanup bounds.
//
// The interval is a constant rather than a setting because internal/config has no
// natural place for it: that package's duration settings are the request and session
// bounds a caller observes, and this one is invisible to every caller. Adding an
// operator knob would add a setting whose only correct value is "often enough that
// the backlog stays small", which is what this constant is.
const (
	// defaultCleanupInterval is how often the sweep runs. It is short enough that a
	// busy deployment never accumulates more than a few thousand expired rows, and
	// long enough that an idle one spends effectively nothing on it.
	defaultCleanupInterval = 15 * time.Minute

	// cleanupRowLimit is how many rows one table gives up per pass. It bounds the
	// write transaction, so one sweep cannot hold SQLite's single writer long enough
	// to stall live requests.
	cleanupRowLimit = 500

	// maxCleanupPasses bounds how many passes one tick may run when a table came
	// back full. It is what keeps a large backlog from turning one tick into an
	// unbounded loop: the remainder waits for the next interval.
	maxCleanupPasses = 4

	// CleanupLogMessage is the record one non-empty sweep writes. It is exported so
	// a test in another package can assert on its absence.
	CleanupLogMessage = "expired authorization state removed"

	// cleanupMessage is CleanupLogMessage under the name this package uses.
	cleanupMessage = CleanupLogMessage

	// cleanupFailureMessage is the record a failed sweep writes.
	cleanupFailureMessage = "periodic cleanup of expired authorization state failed"
)

// ErrMissingCleanupStore reports a cleaner built without a store to sweep. It is a
// wiring mistake, and it is refused rather than tolerated: a cleaner that ticks over
// nothing looks exactly like a working one.
var ErrMissingCleanupStore = errors.New("periodic cleanup needs a store to sweep")

// expiredStateStore is the one method the cleanup needs. It is an interface so the
// loop can be exercised without a database, and it is declared with its consumer.
type expiredStateStore interface {
	Cleanup(ctx context.Context, limit int) (store.CleanupStats, error)
}

// storeCleaner runs the bounded periodic sweep.
//
// The composition root builds one and the serve loop runs it; nothing mutates it
// afterwards, and it holds no state between ticks.
type storeCleaner struct {
	store  expiredStateStore
	logger *slog.Logger

	// interval is the tick period. It is a field rather than the constant so a test
	// can run the real loop without waiting a quarter of an hour.
	interval time.Duration
	// limit is how many rows one table gives up per pass.
	limit int
	// passes bounds how many passes one tick may run.
	passes int
}

// newStoreCleaner validates the store and returns the cleaner. A nil logger selects
// slog.Default, which is what a caller without one would have used anyway.
func newStoreCleaner(target expiredStateStore, logger *slog.Logger) (*storeCleaner, error) {
	if target == nil {
		return nil, fmt.Errorf("building the store cleanup: %w", ErrMissingCleanupStore)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &storeCleaner{
		store:    target,
		logger:   logger,
		interval: defaultCleanupInterval,
		limit:    cleanupRowLimit,
		passes:   maxCleanupPasses,
	}, nil
}

// Run sweeps every interval until ctx ends.
//
// A cancelled context is a graceful stop, not a failure, so it returns nil: the
// deployment joins this result with the serve error, and a cancellation reported
// here would make every clean shutdown look like a crash. A failed sweep does not
// end the loop either — the next tick tries again, and nothing depends on any single
// one having succeeded.
func (c *storeCleaner) Run(ctx context.Context) error {
	if c == nil {
		return nil
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.sweep(ctx)
		}
	}
}

// sweep runs up to passes bounded passes and logs what they removed.
//
// The record carries the four counts and nothing else. A removed row belonged to
// some principal and some client, and naming either would put into a log line
// exactly what the rest of this server keeps out of one. A sweep that removed
// nothing writes nothing, so an idle deployment stays quiet.
func (c *storeCleaner) sweep(ctx context.Context) {
	var total store.CleanupStats
	for range c.passes {
		stats, err := c.store.Cleanup(ctx, c.limit)
		if err != nil {
			c.logFailure(ctx, err)
			return
		}
		total = addCleanupStats(total, stats)
		if !stats.AtLimit(c.limit) {
			break
		}
	}
	if total == (store.CleanupStats{}) {
		return
	}
	c.logger.LogAttrs(ctx, slog.LevelInfo, cleanupMessage,
		slog.Int("transactions", total.Transactions),
		slog.Int("codes", total.Codes),
		slog.Int("tokens", total.Tokens),
		slog.Int("families", total.Families),
	)
}

// logFailure reports a failed sweep without its cause text.
//
// The cause is a database error, and a database error can quote a statement, a
// column and, through a constraint, a stored value. The Go type of the cause is
// authored by a package rather than received, so it is the one detail that can be
// carried safely. The operator's next step is the same either way, because the next
// tick retries.
func (c *storeCleaner) logFailure(ctx context.Context, err error) {
	if ctx.Err() != nil {
		// The sweep was cut short by the shutdown, which is not a failure.
		return
	}
	c.logger.LogAttrs(ctx, slog.LevelWarn, cleanupFailureMessage,
		slog.String("cause", fmt.Sprintf("%T", err)))
}

// addCleanupStats totals two passes. It returns a new value and mutates neither.
func addCleanupStats(a, b store.CleanupStats) store.CleanupStats {
	return store.CleanupStats{
		Transactions: a.Transactions + b.Transactions,
		Codes:        a.Codes + b.Codes,
		Tokens:       a.Tokens + b.Tokens,
		Families:     a.Families + b.Families,
	}
}
