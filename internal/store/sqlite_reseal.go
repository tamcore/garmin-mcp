package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

// Store-level re-sealing, the SQLite half of ADR 0005's second open item.
//
// # Why there is no checkpoint table
//
// A killed run resumes by simply being invoked again: every table is paged
// through in full, oldest primary key first, and each row is handed to its
// rewrite function regardless of what its key_version column currently says.
// A separate progress table would duplicate that truth and could disagree
// with it after a crash between the row write and the checkpoint write.
//
// # Why the key_version column cannot select candidates
//
// Earlier versions of this scan selected only rows whose key_version column
// disagreed with the active version. That column is exactly what a killed
// pass can leave disagreeing with the row's actual sealed content — recorded
// before the content was rewritten, or the reverse, or planted that way
// directly — so filtering on it can skip a row that still needs resealing
// and never touch it again. Every row is now read every pass; each rewrite
// function still changes nothing when planReseal finds the content already
// at the active version, so a fully resealed table costs one read pass and
// no writes. The completion proof, [SQLiteStore.RemainingToReseal], is
// content-based for the same reason: it reads each envelope's own key
// version header rather than trusting the column, and reports zero only once
// every sealed record's actual content is at the active version.
//
// # Why the compare-and-set predicate differs by table
//
// garmin_token_sets and auth_transactions already carry an application-level
// compare-and-set counter (version). Re-sealing those repeats that counter in the
// UPDATE predicate alongside the old key_version, exactly as a normal write would,
// so a concurrent application write and a concurrent reseal cannot silently
// overwrite each other: whichever commits first changes the row's key_version (a
// write always uses the active key) or its version (a rotation of the CAS
// counter), and the loser's predicate then matches nothing and is skipped rather
// than retried, because the next full pass finds the row already correct.
//
// principals and schema_meta carry no such counter — a principal's identity link
// and the single schema_meta row are not compare-and-set resources anywhere else
// in this package. Content equality on the sealed column stands in for it: a fresh
// nonce makes every seal of the same plaintext produce different bytes, so any
// concurrent write to that column changes the bytes and the reseal's predicate
// correctly matches nothing.
//
// # Batching
//
// Each table is paged through in bounded batches, ordered by primary key, so a
// large database is never fully materialized in memory. A batch's own SELECT
// is keyed off the last primary key its predecessor returned, so a killed
// process resumes correctly by simply being invoked again — from the
// beginning of the table, since resuming a partial pass from a remembered
// cursor would have nowhere safe to store that cursor across a crash either.

// resealBatchSize bounds how many rows one batch reads and rewrites.
const resealBatchSize = 200

// ResealReport counts what one call to ResealToActiveKey actually rewrote, not
// what it scanned.
type ResealReport struct {
	GarminTokenSets       int
	PrincipalIdentities   int
	AuthTransactionStates int
	IndexRoot             bool
}

// ResealCounts reports how many sealed records still reference a key version
// other than the active one, per record kind. It changes nothing, and it is the
// completion proof: an operator retires an old key only once every field here
// reads zero and IndexRootPending is false.
type ResealCounts struct {
	GarminTokenSets       int
	PrincipalIdentities   int
	AuthTransactionStates int
	IndexRootPending      bool
}

// Done reports whether every sealed record in the database is at the active key
// version.
func (c ResealCounts) Done() bool {
	return c.GarminTokenSets == 0 && c.PrincipalIdentities == 0 &&
		c.AuthTransactionStates == 0 && !c.IndexRootPending
}

// ResealToActiveKey re-seals every stored record still under a retired key onto
// the active key, across every table this package encrypts into. It is safe to
// call repeatedly and safe to interrupt: each pass re-scans for what is still
// outdated rather than trusting any record of previous progress, so a killed run
// resumes correctly and never re-seals a record twice.
//
// The active key was chosen when this store was opened (SQLiteConfig.Key); every
// retired key it also holds is used only to read what this call re-seals.
func (s *SQLiteStore) ResealToActiveKey(ctx context.Context) (ResealReport, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return ResealReport{}, err
	}

	var report ResealReport
	tokens, err := s.resealAllGarminTokenSets(ctx)
	if err != nil {
		return report, err
	}
	report.GarminTokenSets = tokens

	identities, err := s.resealAllPrincipalIdentities(ctx)
	if err != nil {
		return report, err
	}
	report.PrincipalIdentities = identities

	states, err := s.resealAllAuthTransactionStates(ctx)
	if err != nil {
		return report, err
	}
	report.AuthTransactionStates = states

	rootChanged, err := s.resealIndexRoot(ctx)
	if err != nil {
		return report, err
	}
	report.IndexRoot = rootChanged

	return report, nil
}

// RemainingToReseal scans every table without changing anything, and is the
// completion proof a rotation is done: every field reads zero once every sealed
// record is at the active key version.
//
// Every count is content-based: it reads the key version an envelope's own
// header declares (cryptostore.SealedKeyVersion, readable without any key) and
// never trusts a key_version column alone. A column can disagree with its
// row's actual content — recorded before the content was rewritten, or the
// reverse — and trusting it here would let a completed-looking scan hide a
// record still sealed under a key an operator is about to retire.
func (s *SQLiteStore) RemainingToReseal(ctx context.Context) (ResealCounts, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return ResealCounts{}, err
	}
	active, err := s.crypt.activeVersion()
	if err != nil {
		return ResealCounts{}, err
	}

	var counts ResealCounts
	counts.GarminTokenSets, err = countSealedMismatches(ctx, s.db,
		`SELECT sealed FROM garmin_token_sets`, active)
	if err != nil {
		return ResealCounts{}, err
	}
	counts.PrincipalIdentities, err = countSealedMismatches(ctx, s.db,
		`SELECT garmin_identity_sealed FROM principals WHERE garmin_identity_sealed IS NOT NULL`, active)
	if err != nil {
		return ResealCounts{}, err
	}
	counts.AuthTransactionStates, err = countSealedMismatches(ctx, s.db,
		`SELECT client_state_sealed FROM auth_transactions WHERE client_state_key_version IS NOT NULL`, active)
	if err != nil {
		return ResealCounts{}, err
	}

	var indexRootSealed []byte
	err = s.db.QueryRowContext(ctx,
		`SELECT index_root_sealed FROM schema_meta WHERE id = 1`).Scan(&indexRootSealed)
	if err != nil {
		return ResealCounts{}, fmt.Errorf("store: read schema metadata: %w", err)
	}
	indexRootVersion, err := cryptostore.SealedKeyVersion(indexRootSealed)
	if err != nil {
		return ResealCounts{}, fmt.Errorf("store: read the index root envelope header: %w", err)
	}
	counts.IndexRootPending = indexRootVersion != active

	return counts, nil
}

// countSealedMismatches runs query, which must select exactly one sealed
// column, and counts how many of its rows declare a key version other than
// active in their own envelope header.
func countSealedMismatches(ctx context.Context, db *sql.DB, query string, active int) (int, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("store: scan sealed content: %w", err)
	}
	defer func() { _ = rows.Close() }()

	count := 0
	for rows.Next() {
		var sealed []byte
		if err := rows.Scan(&sealed); err != nil {
			return 0, fmt.Errorf("store: read sealed content: %w", err)
		}
		version, err := cryptostore.SealedKeyVersion(sealed)
		if err != nil {
			return 0, fmt.Errorf("store: read sealed envelope header: %w", err)
		}
		if version != active {
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: scan sealed content: %w", err)
	}
	return count, nil
}
