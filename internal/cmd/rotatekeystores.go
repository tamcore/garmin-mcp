package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// runRotateKeySQLite re-seals every table the multi-user store encrypts into,
// then proves completion with a read-only scan.
func runRotateKeySQLite(
	ctx context.Context, cfg config.Config, target cryptostore.Key, retired []cryptostore.Key, out io.Writer,
) error {
	sqlite, err := store.OpenSQLite(ctx, store.SQLiteConfig{
		Path:        cfg.DatabasePath,
		Key:         target,
		RetiredKeys: retired,
	})
	if err != nil {
		return fmt.Errorf("opening the database to reseal: %w", err)
	}
	defer func() { _ = sqlite.Close() }()

	// report is printed unconditionally, BEFORE either error below is checked:
	// one unreadable row (or a cancelled context) aborts ResealToActiveKey
	// partway through, and an operator seeing neither what succeeded nor what
	// remains has no way to tell the rotation apart from one that touched
	// nothing at all — with one corrupt row, the retiring key could then never
	// be retired, because there is nothing to confirm against. The counts
	// above are themselves point-in-time: a mid-table hard failure can leave
	// counts assigned per table but zero anywhere later than the failure, so
	// this is a floor on progress, never a ceiling.
	report, err := sqlite.ResealToActiveKey(ctx)
	writeSQLiteResealCounts(out, cfg.DatabasePath, report)
	if err != nil {
		return fmt.Errorf("resealing stored records: %w", err)
	}

	remaining, err := sqlite.RemainingToReseal(ctx)
	if err != nil {
		return fmt.Errorf("verifying the reseal completed: %w", err)
	}
	writeSQLiteRemainingSummary(out, remaining)
	if !remaining.Done() {
		return ErrRotationIncomplete
	}
	return nil
}

// writeSQLiteResealCounts states what one ResealToActiveKey call rewrote,
// regardless of whether it ran to completion or stopped partway through on an
// error.
func writeSQLiteResealCounts(out io.Writer, path string, report store.ResealReport) {
	_, _ = fmt.Fprintf(out, "database: %s\n", path)
	_, _ = fmt.Fprintf(out, "garmin token sets resealed: %d\n", report.GarminTokenSets)
	_, _ = fmt.Fprintf(out, "principal identities resealed: %d\n", report.PrincipalIdentities)
	_, _ = fmt.Fprintf(out, "authorization transaction states resealed: %d\n", report.AuthTransactionStates)
	_, _ = fmt.Fprintf(out, "index root resealed: %t\n", report.IndexRoot)
}

// writeSQLiteRemainingSummary states what, if anything, is still left, in the
// order an operator reads it after writeSQLiteResealCounts.
//
// The scan behind remaining is point-in-time: nothing here re-checks the
// marker or excludes a server that started serving after the scan ran, so a
// clean report is evidence for this instant, not a guarantee that stays true
// forever. See runRotateKeyFileStore's report for why the same caveat applies
// to the local backend.
func writeSQLiteRemainingSummary(out io.Writer, remaining store.ResealCounts) {
	if remaining.Done() {
		_, _ = fmt.Fprintln(out, "no record needed the retiring key as of this scan; "+
			"confirm no server was running throughout this run, then re-run before retiring the key")
		return
	}
	_, _ = fmt.Fprintf(out, "still at a retired key version: %d garmin token sets, "+
		"%d principal identities, %d authorization transaction states, index root pending=%t\n",
		remaining.GarminTokenSets, remaining.PrincipalIdentities,
		remaining.AuthTransactionStates, remaining.IndexRootPending)
	_, _ = fmt.Fprintln(out, "the retiring key must stay in place; run rotate-key again to finish")
}

// runRotateKeyFileStore re-seals the single principal this configuration
// binds. principal was already resolved by the caller, before the marker was
// activated, so a deployment that binds no principal is refused before
// anything durable happens rather than after.
func runRotateKeyFileStore(
	ctx context.Context, principal identity.Principal, paths statePaths,
	target cryptostore.Key, retired []cryptostore.Key, out io.Writer,
) error {
	files, err := store.NewFileStore(store.Config{
		Dir:         paths.tokens,
		Key:         target,
		RetiredKeys: retired,
	})
	if err != nil {
		return fmt.Errorf("opening the token store to reseal: %w", err)
	}

	outcome, err := files.Reseal(ctx, principal.ID())
	if err != nil {
		return fmt.Errorf("resealing the stored token record: %w", err)
	}
	return reportFileStoreReseal(out, outcome)
}

// reportFileStoreReseal writes the operator-facing report for a FileStore
// reseal outcome and reports ErrRotationIncomplete when the outcome does not
// let the affirmative "at the active key version" line be printed honestly.
//
// Pulled out of runRotateKeyFileStore so the printed-message-per-outcome
// mapping is testable without a real store, a lock, or any concurrency: the
// defect this guards used to collapse store.ResealRaced into the same silent
// "already matched" case as store.ResealAlreadyCurrent, which let an operator
// read the affirmative line and retire a key that still protected a live
// record.
func reportFileStoreReseal(out io.Writer, outcome store.ResealOutcome) error {
	switch outcome {
	case store.ResealRewrote:
		_, _ = fmt.Fprintln(out, "the stored token record was resealed onto the target key")
	case store.ResealNoRecord:
		_, _ = fmt.Fprintln(out, "there is no stored token record for the bound principal")
	case store.ResealRaced:
		// Another writer moved the record between this attempt's read and its
		// write. Its key version as of now is unknown, so the affirmative line
		// below must not be printed: doing so is exactly the false all-clear
		// this outcome exists to prevent.
		_, _ = fmt.Fprintln(out, "the stored token record changed under a concurrent writer "+
			"during this attempt; its key version is NOT confirmed and the retiring key must "+
			"stay in place; run rotate-key again to confirm")
		return ErrRotationIncomplete
	case store.ResealAlreadyCurrent:
		_, _ = fmt.Fprintln(out, "the stored token record already matched the target key")
	}
	// This backend has no completion scan: a FileStore holds exactly one
	// record, the bound principal's, so re-sealing it (or finding nothing to
	// reseal) is the whole story for THAT principal. It says nothing about any
	// other file under the records directory: the record file name is a
	// one-way SHA-256 digest of a principal id, so a record for a principal
	// this configuration does not bind cannot be enumerated, let alone
	// re-sealed, and is not covered by this run.
	_, _ = fmt.Fprintf(out, "the bound principal's record is at the active key version "+
		"as of this scan; any record for a principal this configuration does not bind "+
		"is NOT covered by rotation and needs its own configuration re-run\n")
	return nil
}
