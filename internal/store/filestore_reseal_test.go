package store

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

// newFileStoreWithKeys opens a FileStore at dir with an explicit active key and
// retired-key list, so a test can move a store between key versions directly
// rather than through the on-disk key-file layout a real deployment uses.
func newFileStoreWithKeys(t *testing.T, dir string, active cryptostore.Key, retired []cryptostore.Key) *FileStore {
	t.Helper()
	s, err := NewFileStore(Config{Dir: dir, Key: active, RetiredKeys: retired})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return s
}

// TestFileStoreRecordStillUnderARetiredKeyDuringTheWindow is the required
// mixed-version-read property for the local backend.
func TestFileStoreRecordStillUnderARetiredKeyDuringTheWindow(t *testing.T) {
	dir := tempDir(t)
	oldKey := mustGenerateKey(t, 1)
	ctx := context.Background()

	seed := newFileStoreWithKeys(t, dir, oldKey, nil)
	if _, err := seed.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)
	windowed := newFileStoreWithKeys(t, dir, targetKey, []cryptostore.Key{oldKey})

	loaded, _, err := windowed.Load(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("Load during the mixed-version window: %v", err)
	}
	if loaded.RefreshToken() != testRefreshToken {
		t.Fatalf("loaded refresh token = %q, want %q", loaded.RefreshToken(), testRefreshToken)
	}
}

// TestFileStoreLoadFailsClosedWhenNoConfiguredKeyOpensIt is the required
// unknown-key-version property for the local backend.
func TestFileStoreLoadFailsClosedWhenNoConfiguredKeyOpensIt(t *testing.T) {
	dir := tempDir(t)
	oldKey := mustGenerateKey(t, 1)
	ctx := context.Background()

	seed := newFileStoreWithKeys(t, dir, oldKey, nil)
	if _, err := seed.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	orphaned := newFileStoreWithKeys(t, dir, mustGenerateKey(t, 3), nil)
	if _, _, err := orphaned.Load(ctx, testPrincipal); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Load with no key that opens the record: err = %v, want ErrCorruptRecord", err)
	}
}

// TestFileStoreResealResumesWithoutDoubleSealing is the required kill-and-resume
// property for the local backend: a second Reseal call after the record is
// already at the active version must change nothing and must not rewrite the file
// a second time.
func TestFileStoreResealResumesWithoutDoubleSealing(t *testing.T) {
	dir := tempDir(t)
	oldKey := mustGenerateKey(t, 1)
	ctx := context.Background()

	seed := newFileStoreWithKeys(t, dir, oldKey, nil)
	if _, err := seed.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)
	rotating := newFileStoreWithKeys(t, dir, targetKey, []cryptostore.Key{oldKey})

	outcome, err := rotating.Reseal(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("first Reseal: %v", err)
	}
	if outcome != ResealRewrote {
		t.Fatalf("first Reseal outcome = %v, want ResealRewrote", outcome)
	}
	afterFirst := readRawRecordFile(t, rotating, testPrincipal)

	// Simulate a resumed run after the process was killed.
	outcome, err = rotating.Reseal(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("second (resumed) Reseal: %v", err)
	}
	if outcome != ResealAlreadyCurrent {
		t.Fatalf("resumed Reseal outcome = %v, want ResealAlreadyCurrent", outcome)
	}
	afterSecond := readRawRecordFile(t, rotating, testPrincipal)
	if string(afterFirst) != string(afterSecond) {
		t.Fatal("the resumed reseal rewrote the file even though nothing needed resealing: double-sealed")
	}

	targetOnly := newFileStoreWithKeys(t, dir, targetKey, nil)
	loaded, _, err := targetOnly.Load(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("Load with only the target key after the reseal completed: %v", err)
	}
	if loaded.RefreshToken() != testRefreshToken {
		t.Fatalf("loaded refresh token = %q, want %q", loaded.RefreshToken(), testRefreshToken)
	}
}

// TestFileStoreResealOfAnAbsentRecordIsANoOp documents that a principal with no
// stored record has nothing to reseal.
func TestFileStoreResealOfAnAbsentRecordIsANoOp(t *testing.T) {
	dir := tempDir(t)
	rotating := newFileStoreWithKeys(t, dir, mustGenerateKey(t, 2), []cryptostore.Key{mustGenerateKey(t, 1)})

	outcome, err := rotating.Reseal(context.Background(), testPrincipal)
	if err != nil {
		t.Fatalf("Reseal of an absent record: %v", err)
	}
	if outcome != ResealNoRecord {
		t.Fatalf("Reseal of an absent record outcome = %v, want ResealNoRecord", outcome)
	}
}

// readRawRecordFile reads the raw on-disk bytes of principal's record, for
// proving a reseal did or did not rewrite the file.
func readRawRecordFile(t *testing.T, s *FileStore, principal string) []byte {
	t.Helper()
	raw, err := readOwnerOnlyFile(s.recordPath(principal), ErrNoTokens)
	if err != nil {
		t.Fatalf("read raw record file: %v", err)
	}
	return raw
}

// TestFileStoreSaveWaitsForTheCrossProcessRecordLock and
// TestFileStoreResealWaitsForTheCrossProcessRecordLock independently prove
// that Save and Reseal each actually take the cross-process record lock,
// modeled directly on TestFileStoreDeleteWaitsForTheCrossProcessRecordLock
// below: the record lock is taken directly by the test, held while the
// operation under test runs in a goroutine, and the operation must block for
// as long as the lock is held and complete once it is released.
//
// TestFileStoreConcurrentRefreshRacingTheResealerNeverLosesEitherWrite further
// down races Save and Reseal against each other with no forced interleaving,
// which still exercises the compare-and-set predicate but proves nothing about
// either side's flock call on its own: the race detector and the version
// assertion at the end can both stay green with the flock call deleted from
// either operation, since the two goroutines have no guaranteed reason to
// actually overlap. These two tests close that gap for each operation
// separately.

// TestFileStoreSaveWaitsForTheCrossProcessRecordLock is the mutual-exclusion
// property Save's cross-process lock exists for: without it, a concurrent
// writer in another process — modeled here by a second descriptor on the same
// lock file — could interleave its own read-modify-write with this Save's and
// silently lose an update.
func TestFileStoreSaveWaitsForTheCrossProcessRecordLock(t *testing.T) {
	dir := tempDir(t)
	key := mustGenerateKey(t, 1)
	ctx := context.Background()

	seeded := newFileStoreWithKeys(t, dir, key, nil)
	if _, err := seeded.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	// Stand in for the other process: hold the record's lock directly.
	held, err := lockRecord(context.Background(), seeded.lockPath(testPrincipal))
	if err != nil {
		t.Fatalf("take the record lock: %v", err)
	}

	saved := make(chan error, 1)
	go func() {
		_, err := seeded.Save(ctx, testPrincipal, newTestTokens().WithToken("second"), 1)
		saved <- err
	}()

	select {
	case err := <-saved:
		_ = held.release()
		t.Fatalf("Save returned %v while another descriptor held the record lock: "+
			"it is not taking the cross-process lock, so a concurrent writer in another "+
			"process can interleave a read-modify-write with it and lose an update", err)
	case <-time.After(150 * time.Millisecond):
		// Correct: blocked on the lock.
	}

	if err := held.release(); err != nil {
		t.Fatalf("release the record lock: %v", err)
	}
	select {
	case err := <-saved:
		if err != nil {
			t.Fatalf("Save after the record lock was released: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Save never completed after the record lock was released")
	}
}

// TestFileStoreResealWaitsForTheCrossProcessRecordLock is the same
// mutual-exclusion property for Reseal: without the cross-process lock, a
// concurrent Save in another process could read the pre-reseal content,
// re-seal onto the active key here, and then have the other process commit a
// write derived from the stale content underneath it.
func TestFileStoreResealWaitsForTheCrossProcessRecordLock(t *testing.T) {
	dir := tempDir(t)
	oldKey := mustGenerateKey(t, 1)
	ctx := context.Background()

	seed := newFileStoreWithKeys(t, dir, oldKey, nil)
	if _, err := seed.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)
	rotating := newFileStoreWithKeys(t, dir, targetKey, []cryptostore.Key{oldKey})

	// Stand in for the other process: hold the record's lock directly.
	held, err := lockRecord(context.Background(), rotating.lockPath(testPrincipal))
	if err != nil {
		t.Fatalf("take the record lock: %v", err)
	}

	resealed := make(chan error, 1)
	go func() {
		_, err := rotating.Reseal(ctx, testPrincipal)
		resealed <- err
	}()

	select {
	case err := <-resealed:
		_ = held.release()
		t.Fatalf("Reseal returned %v while another descriptor held the record lock: "+
			"it is not taking the cross-process lock, so a concurrent Save in another "+
			"process can interleave with it", err)
	case <-time.After(150 * time.Millisecond):
		// Correct: blocked on the lock.
	}

	if err := held.release(); err != nil {
		t.Fatalf("release the record lock: %v", err)
	}
	select {
	case err := <-resealed:
		if err != nil {
			t.Fatalf("Reseal after the record lock was released: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Reseal never completed after the record lock was released")
	}
}

// TestFileStoreConcurrentRefreshRacingTheResealerNeverLosesEitherWrite is the
// required concurrency property for the local backend, modeling the REAL
// two-process shape: `rotate-key` and a live `serve` process each open their
// own *FileStore over the same directory, sharing no lock at all —
// principalLocks is per-*FileStore (filestore.go), not per-directory. Two
// SEPARATE *FileStore instances are used here on purpose: sharing one instance
// between the two goroutines would only exercise the per-process mutex and
// would pass even against a Reseal with no compare-and-set whatsoever. Run
// this under -race.
func TestFileStoreConcurrentRefreshRacingTheResealerNeverLosesEitherWrite(t *testing.T) {
	dir := tempDir(t)
	oldKey := mustGenerateKey(t, 1)
	ctx := context.Background()

	seed := newFileStoreWithKeys(t, dir, oldKey, nil)
	if _, err := seed.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)
	// refresher models the live serve process: it never advanced past the
	// retiring key as its ACTIVE (sealing) key, because loadKeyRing only
	// re-reads the marker at start-up. It is handed the target key only as a
	// retired (read-only) key here, purely so the refresh loop's reads do not
	// fail with an unrelated "wrong key" error once a reseal has moved the
	// record forward — that is a real and separate consequence of running
	// rotate-key against a live server, not the lost-update bug this test
	// isolates.
	refresher := newFileStoreWithKeys(t, dir, oldKey, []cryptostore.Key{targetKey})
	// rotator models the separate rotate-key process.
	rotator := newFileStoreWithKeys(t, dir, targetKey, []cryptostore.Key{oldKey})
	// verifier can open a record sealed under either key, so the final
	// assertion holds regardless of which process happened to write last.
	verifier := newFileStoreWithKeys(t, dir, oldKey, []cryptostore.Key{targetKey})

	const refreshRounds = 30
	var group sync.WaitGroup
	var refreshErr, resealErr error

	group.Go(func() {
		current := int64(1)
		for round := range refreshRounds {
			next, err := refresher.Save(ctx, testPrincipal,
				newTestTokens().WithToken("refreshed-"+strconv.Itoa(round)), current)
			if err != nil {
				refreshErr = err
				return
			}
			current = next
		}
	})
	group.Go(func() {
		for range 200 {
			if _, err := rotator.Reseal(ctx, testPrincipal); err != nil {
				resealErr = err
				return
			}
		}
	})
	group.Wait()

	if refreshErr != nil {
		t.Fatalf("concurrent refresh loop: %v", refreshErr)
	}
	if resealErr != nil {
		t.Fatalf("concurrent reseal loop: %v", resealErr)
	}

	loaded, version, err := verifier.Load(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("Load after the race: %v", err)
	}
	if want := int64(1 + refreshRounds); version != want {
		t.Fatalf("final version = %d, want %d: the refresh loop's writes must not have been lost "+
			"(a reseal clobbering a concurrent refresh would report an older version here)", version, want)
	}
	if want := "refreshed-" + strconv.Itoa(refreshRounds-1); loaded.Token() != want {
		t.Fatalf("final token = %q, want %q (the last refresh round's value)", loaded.Token(), want)
	}
}

// TestFileStoreDeleteWaitsForTheCrossProcessRecordLock is the mutual-exclusion
// property Delete needs for the same reason Save needs it.
//
// Without the cross-process lock, a Save running in a rotate-key or a second
// serve process can read the current version, have a Delete remove the record
// underneath it, and then write — resurrecting a record an operator deliberately
// unlinked. The in-process mutex cannot order those two, because each process
// holds its own.
//
// flock(2) associates a lock with the open file description rather than the
// process, so two descriptors on one path conflict even inside one test binary.
// That is what makes this property testable here at all, and it is the same
// reason the two-instance Save race test above models the real two-process shape.
func TestFileStoreDeleteWaitsForTheCrossProcessRecordLock(t *testing.T) {
	dir := tempDir(t)
	key := mustGenerateKey(t, 1)
	ctx := context.Background()

	seeded := newFileStoreWithKeys(t, dir, key, nil)
	if _, err := seeded.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	// Stand in for the other process: hold the record's lock directly.
	held, err := lockRecord(context.Background(), seeded.lockPath(testPrincipal))
	if err != nil {
		t.Fatalf("take the record lock: %v", err)
	}

	deleted := make(chan error, 1)
	go func() { deleted <- seeded.Delete(ctx, testPrincipal) }()

	select {
	case err := <-deleted:
		_ = held.release()
		t.Fatalf("Delete returned %v while another descriptor held the record lock: "+
			"it is not taking the cross-process lock, so a concurrent Save in another "+
			"process can resurrect the deleted record", err)
	case <-time.After(150 * time.Millisecond):
		// Correct: blocked on the lock.
	}

	if err := held.release(); err != nil {
		t.Fatalf("release the record lock: %v", err)
	}
	select {
	case err := <-deleted:
		if err != nil {
			t.Fatalf("Delete after the lock was released: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Delete never completed after the record lock was released")
	}

	if _, _, err := seeded.Load(ctx, testPrincipal); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("Load after Delete: err = %v, want ErrNoTokens", err)
	}
}

// TestRecordMovedUnderResealDetectsEveryKindOfConcurrentWrite covers the decision
// that guards Reseal's write.
//
// The branch itself cannot be reached deterministically from a test — it needs a
// write to land between Reseal's two reads while Reseal holds the record lock,
// which only a writer ignoring the lock can do — so the decision is tested
// directly instead. That is a real limit and worth stating: this proves the rule
// is right, not that Reseal consults it. The surrounding Reseal tests cover the
// paths that do not race.
//
// The mutant each case kills: comparing only the version misses a writer that
// rewrote the payload without advancing the counter, and comparing only the
// payload misses a rollback that restored earlier bytes under a new version.
// Either one lets a reseal overwrite somebody else's committed record.
func TestRecordMovedUnderResealDetectsEveryKindOfConcurrentWrite(t *testing.T) {
	planned := storedRecord{Schema: recordSchema, Version: 7, Payload: "cGF5bG9hZA=="}

	cases := []struct {
		name    string
		current storedRecord
		moved   bool
	}{
		{
			name:    "untouched",
			current: planned,
			moved:   false,
		},
		{
			name:    "version advanced by a concurrent refresh",
			current: storedRecord{Schema: recordSchema, Version: 8, Payload: planned.Payload},
			moved:   true,
		},
		{
			name:    "payload rewritten without advancing the version",
			current: storedRecord{Schema: recordSchema, Version: 7, Payload: "b3RoZXI="},
			moved:   true,
		},
		{
			name:    "earlier payload restored under a newer version",
			current: storedRecord{Schema: recordSchema, Version: 9, Payload: "b2xkZXI="},
			moved:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recordMovedUnderReseal(planned, tc.current); got != tc.moved {
				t.Fatalf("recordMovedUnderReseal = %v, want %v: a reseal would %s",
					got, tc.moved,
					map[bool]string{true: "refuse a write it should have made",
						false: "overwrite a record another writer committed"}[got])
			}
		})
	}
}
