package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// mustGenerateKey builds a throwaway encryption key at an explicit version, for
// the reseal tests that move a store from one key version to the next.
func mustGenerateKey(t *testing.T, version int) cryptostore.Key {
	t.Helper()
	key, err := cryptostore.GenerateKey(version)
	if err != nil {
		t.Fatalf("GenerateKey(%d): %v", version, err)
	}
	return key
}

// openStoreWithKeys opens (or reopens) the database at path with an explicit
// active key and retired-key list, so a test can move a store from one key
// version to the next without going through cryptostore.LoadOrCreateKey's
// directory layout: the reseal tests care about which keys a store holds, not
// about where a real deployment would keep the files on disk.
func openStoreWithKeys(
	t *testing.T, path string, active cryptostore.Key, retired []cryptostore.Key,
) *store.SQLiteStore {
	t.Helper()
	opened, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{
		Path:        path,
		Key:         active,
		RetiredKeys: retired,
	})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() {
		if err := opened.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return opened
}

// rawSealed reads the raw sealed bytes of one principal's garmin_token_sets row
// through a second connection to the same file, so a test can prove a reseal did
// or did not rewrite a row without going through this package's own encryption.
func rawSealed(t *testing.T, path, principalID string) []byte {
	t.Helper()
	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	var sealed []byte
	err = db.QueryRow(`SELECT sealed FROM garmin_token_sets WHERE principal_id = ?`, principalID).Scan(&sealed)
	if err != nil {
		t.Fatalf("read raw sealed bytes: %v", err)
	}
	return sealed
}

// TestReadingARecordStillUnderARetiredKeyDuringTheWindow is the required mixed-
// version-read property at the store level: once a rotation has made a new
// version active but before anything has been resealed, a record sealed under the
// retiring key must still be readable.
func TestReadingARecordStillUnderARetiredKeyDuringTheWindow(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	oldKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, oldKey, nil)
	ctx := context.Background()
	principal := seedPrincipal(t, seed)
	if _, err := seed.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	// The window: the active key is now v2, but nothing has been resealed yet.
	targetKey := mustGenerateKey(t, 2)
	windowed, err := store.OpenSQLite(ctx, store.SQLiteConfig{
		Path: path, Key: targetKey, RetiredKeys: []cryptostore.Key{oldKey},
	})
	if err != nil {
		t.Fatalf("OpenSQLite during the window: %v", err)
	}
	defer func() { _ = windowed.Close() }()

	loaded, _, err := windowed.Load(ctx, principal.ID)
	if err != nil {
		t.Fatalf("Load during the mixed-version window: %v", err)
	}
	if loaded.RefreshToken() != sqlTestRefreshToken {
		t.Fatalf("loaded refresh token = %q, want %q", loaded.RefreshToken(), sqlTestRefreshToken)
	}
}

// TestReadingARecordFailsClosedWhenNoConfiguredKeyOpensIt is the required
// unknown-key-version property: a record sealed under a version this store was
// never given must be refused, never silently produce garbage or the wrong
// account's data.
func TestReadingARecordFailsClosedWhenNoConfiguredKeyOpensIt(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	oldKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, oldKey, nil)
	ctx := context.Background()
	principal := seedPrincipal(t, seed)
	if _, err := seed.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	// Skip straight to v3 with no retired key at all: nothing this store holds
	// opens a v1 envelope. The database-wide index root is itself sealed under
	// v1, so opening the store at all is the first thing that fails closed, and
	// it fails before any row-level Load is even reached.
	_, err := store.OpenSQLite(ctx, store.SQLiteConfig{Path: path, Key: mustGenerateKey(t, 3)})
	if !errors.Is(err, store.ErrCorruptRecord) {
		t.Fatalf("OpenSQLite with no key that opens the index root: err = %v, want ErrCorruptRecord", err)
	}
}

// TestReadingAGarminTokenSetFailsClosedWhenNoConfiguredKeyOpensIt is the same
// unknown-key-version property one level deeper: even once a store is open (its
// index root opened under a key it does hold), an individual row sealed under a
// version outside the configured set must still fail closed rather than being
// silently skipped or misread.
//
// The stray row is inserted through a second raw connection to the same file,
// sealed under a key version (99) no store in this test ever holds, so the
// index root — sealed under v1 and left there — opens normally and only the one
// row is unreadable.
func TestReadingAGarminTokenSetFailsClosedWhenNoConfiguredKeyOpensIt(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	firstKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, firstKey, nil)
	ctx := context.Background()
	principal := seedPrincipal(t, seed)

	strayKey := mustGenerateKey(t, 99)
	strayPayload, err := cryptostore.Encrypt(strayKey, principal.ID, "garmin_di_tokens/schema=2/version=1",
		[]byte(`{"di_token":"t","di_refresh_token":"r"}`))
	if err != nil {
		t.Fatalf("Encrypt the stray row: %v", err)
	}
	insertRawGarminTokenSet(t, path, principal.ID, strayPayload, 99)
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	reopened := openStoreWithKeys(t, path, firstKey, nil)
	if _, _, err := reopened.Load(ctx, principal.ID); !errors.Is(err, store.ErrCorruptRecord) {
		t.Fatalf("Load of a row sealed under an unconfigured key version: err = %v, want ErrCorruptRecord", err)
	}
}

// insertRawGarminTokenSet writes a garmin_token_sets row directly, bypassing the
// store's own encryption, so a test can plant a row sealed under a key version no
// SQLiteStore instance in the test holds.
func insertRawGarminTokenSet(t *testing.T, path, principalID string, sealed []byte, keyVersion int) {
	t.Helper()
	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, err = db.Exec(
		`INSERT INTO garmin_token_sets (principal_id, record_schema, version, key_version, sealed, updated_at)
		 VALUES (?, 2, 1, ?, ?, '2026-08-14T12:00:00Z')`,
		principalID, keyVersion, sealed)
	if err != nil {
		t.Fatalf("insert stray garmin_token_sets row: %v", err)
	}
}

// TestResealResumesWithoutDoubleSealing is the required kill-and-resume property.
// A first ResealToActiveKey call re-seals the record onto the active key; a
// second call, standing in for a resumed run after the first was killed, must
// change nothing and must not rewrite the bytes a second time.
func TestResealResumesWithoutDoubleSealing(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	oldKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, oldKey, nil)
	ctx := context.Background()
	principal := seedPrincipal(t, seed)
	if _, err := seed.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)
	rotating := openStoreWithKeys(t, path, targetKey, []cryptostore.Key{oldKey})

	firstReport, err := rotating.ResealToActiveKey(ctx)
	if err != nil {
		t.Fatalf("first ResealToActiveKey: %v", err)
	}
	if firstReport.GarminTokenSets != 1 {
		t.Fatalf("first reseal rewrote %d garmin token sets, want 1", firstReport.GarminTokenSets)
	}
	remaining, err := rotating.RemainingToReseal(ctx)
	if err != nil {
		t.Fatalf("RemainingToReseal after the first pass: %v", err)
	}
	if !remaining.Done() {
		t.Fatalf("RemainingToReseal after the first pass = %+v, want everything done", remaining)
	}
	afterFirst := rawSealed(t, path, principal.ID)

	// Simulate the resumed run: the process was killed and rotate-key was
	// invoked again. Nothing should be rewritten a second time.
	secondReport, err := rotating.ResealToActiveKey(ctx)
	if err != nil {
		t.Fatalf("second (resumed) ResealToActiveKey: %v", err)
	}
	if secondReport.GarminTokenSets != 0 {
		t.Fatalf("resumed reseal rewrote %d garmin token sets, want 0 (already at the active version)",
			secondReport.GarminTokenSets)
	}
	afterSecond := rawSealed(t, path, principal.ID)
	if string(afterFirst) != string(afterSecond) {
		t.Fatal("the resumed reseal changed the stored bytes even though nothing needed resealing: double-sealed")
	}

	// And the record is now readable by the target key alone.
	targetOnly := openStoreWithKeys(t, path, targetKey, nil)
	loaded, _, err := targetOnly.Load(ctx, principal.ID)
	if err != nil {
		t.Fatalf("Load with only the target key after the reseal completed: %v", err)
	}
	if loaded.RefreshToken() != sqlTestRefreshToken {
		t.Fatalf("loaded refresh token = %q, want %q", loaded.RefreshToken(), sqlTestRefreshToken)
	}
}

// updateRawGarminTokenSetSealed overwrites a garmin_token_sets row's sealed
// bytes and key_version column directly, bypassing this package's own
// re-sealing, so a test can plant a row whose content and column disagree
// about which key version sealed it — the exact shape a pass interrupted
// between rewriting the content and recording the column would leave behind.
func updateRawGarminTokenSetSealed(t *testing.T, path, principalID string, sealed []byte, keyVersion int) {
	t.Helper()
	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(
		`UPDATE garmin_token_sets SET sealed = ?, key_version = ? WHERE principal_id = ?`,
		sealed, keyVersion, principalID); err != nil {
		t.Fatalf("update raw garmin_token_sets row: %v", err)
	}
}

// TestResealReconcilesAStaleKeyVersionColumnWithoutLoopingForever is the
// no-progress defect: a row whose sealed content is already at the active key
// but whose key_version column still names a retired version — the shape a
// pass interrupted between rewriting the content and recording the column
// leaves behind — must have its column reconciled, not be rescanned forever.
// Without the fix this call never returns; the bounded context is what turns
// that hang into an observable failure instead of a suite that never
// completes.
func TestResealReconcilesAStaleKeyVersionColumnWithoutLoopingForever(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	oldKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, oldKey, nil)
	ctx := context.Background()
	principal := seedPrincipal(t, seed)
	if _, err := seed.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)

	// Plant the disagreement: the content is already sealed under the target
	// key, but the key_version column still names the retired one.
	sealed, err := cryptostore.Encrypt(targetKey, principal.ID, "garmin_di_tokens/schema=2/version=1",
		[]byte("plaintext-placeholder"))
	if err != nil {
		t.Fatalf("Encrypt with the target key: %v", err)
	}
	updateRawGarminTokenSetSealed(t, path, principal.ID, sealed, 1)

	rotating := openStoreWithKeys(t, path, targetKey, []cryptostore.Key{oldKey})

	resealCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := rotating.ResealToActiveKey(resealCtx)
	if err != nil {
		t.Fatalf("ResealToActiveKey with a stale key_version column: %v, want it to terminate", err)
	}
	if report.GarminTokenSets != 0 {
		t.Errorf("garmin token sets rewritten = %d, want 0 (only the column needed fixing, not the content)",
			report.GarminTokenSets)
	}

	remaining, err := rotating.RemainingToReseal(ctx)
	if err != nil {
		t.Fatalf("RemainingToReseal: %v", err)
	}
	if !remaining.Done() {
		t.Fatalf("RemainingToReseal = %+v, want everything done once the column was reconciled", remaining)
	}
}

// TestResealCatchesContentSealedUnderARetiredKeyWhenTheColumnClaimsActive is the
// dangerous inverse of TestResealReconcilesAStaleKeyVersionColumnWithoutLoopingForever:
// the key_version column already claims the active version, but the sealed
// content itself is still under the retired key — the exact shape that would let
// an operator believe rotation finished, delete the retired key, and lose the
// record. The completion proof and the reseal pass must both be content-based,
// not trust the column.
func TestResealCatchesContentSealedUnderARetiredKeyWhenTheColumnClaimsActive(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	oldKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, oldKey, nil)
	ctx := context.Background()
	principal := seedPrincipal(t, seed)
	if _, err := seed.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)

	// Plant the dangerous disagreement: the content is still sealed under the
	// retired key, but the key_version column already claims the active one —
	// as if a crash recorded the column write but not the content rewrite, or
	// the row was otherwise planted this way.
	sealedUnderOld := rawSealed(t, path, principal.ID)
	updateRawGarminTokenSetSealed(t, path, principal.ID, sealedUnderOld, 2)

	rotating := openStoreWithKeys(t, path, targetKey, []cryptostore.Key{oldKey})

	remainingBefore, err := rotating.RemainingToReseal(ctx)
	if err != nil {
		t.Fatalf("RemainingToReseal before the reseal: %v", err)
	}
	if remainingBefore.Done() {
		t.Fatalf("RemainingToReseal = %+v, want NOT done: the column says active but the content is still retired",
			remainingBefore)
	}

	report, err := rotating.ResealToActiveKey(ctx)
	if err != nil {
		t.Fatalf("ResealToActiveKey: %v", err)
	}
	if report.GarminTokenSets != 1 {
		t.Errorf("garmin token sets rewritten = %d, want 1 (the mismatched row must be re-sealed)",
			report.GarminTokenSets)
	}

	remainingAfter, err := rotating.RemainingToReseal(ctx)
	if err != nil {
		t.Fatalf("RemainingToReseal after the reseal: %v", err)
	}
	if !remainingAfter.Done() {
		t.Fatalf("RemainingToReseal = %+v, want everything done", remainingAfter)
	}

	targetOnly := openStoreWithKeys(t, path, targetKey, nil)
	loaded, _, err := targetOnly.Load(ctx, principal.ID)
	if err != nil {
		t.Fatalf("Load with only the target key after the reseal: %v", err)
	}
	if loaded.RefreshToken() != sqlTestRefreshToken {
		t.Fatalf("loaded refresh token = %q, want %q", loaded.RefreshToken(), sqlTestRefreshToken)
	}
}

// TestConcurrentRefreshRacingTheResealerNeverLosesEitherWrite is the required
// concurrency property. A refresher keeps calling Save while ResealToActiveKey
// runs concurrently against the very same open store: the CAS-and-key-version
// predicate must mean neither side can silently clobber the other. Run this
// under -race.
