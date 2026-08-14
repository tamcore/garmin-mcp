package store_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// The concurrency contract, exercised with real goroutines so -race has something to
// observe. What each test asserts is the contract stated on SQLiteStore: within one
// process, a contended compare-and-set, a contended refresh rotation and a contended
// authorization-code redemption each elect exactly one winner, and a revocation is
// idempotent no matter how many callers run it at once.
//
// None of this is a claim about two processes. The store is single-active-instance;
// cross-process coordination is explicitly not provided.

// saveOutcome is one racing writer's result.
type saveOutcome struct {
	token   string
	version int64
	err     error
}

// electSaveWinner asserts exactly one writer committed and the rest were told to retry.
func electSaveWinner(t *testing.T, results []saveOutcome) saveOutcome {
	t.Helper()
	var winners, conflicts int
	winner := saveOutcome{}
	for _, result := range results {
		switch {
		case result.err == nil:
			winners++
			winner = result
		case errors.Is(result.err, store.ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected Save error: %v", result.err)
		}
	}
	if winners != 1 || conflicts != len(results)-1 {
		t.Fatalf("got %d winners and %d conflicts across %d writers, want 1 winner",
			winners, conflicts, len(results))
	}
	return winner
}

// TestConcurrentRotatedGarminWritesElectOneWinner is the requirement that a rotated
// Garmin refresh token cannot be lost. Four writers read version 1 and all try to replace
// it: exactly one commits and the others are told to reload.
func TestConcurrentRotatedGarminWritesElectOneWinner(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	base, err := opened.Save(ctx, principal.ID, newSQLTestTokens(), 0)
	if err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	const writers = 4
	results := make([]saveOutcome, writers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range writers {
		token := "rotated-by-writer-" + strconv.Itoa(index)
		done.Go(func() {
			start.Wait()
			set := newSQLTestTokens().WithRefreshToken(token)
			version, err := opened.Save(ctx, principal.ID, set, base)
			results[index] = saveOutcome{token: token, version: version, err: err}
		})
	}
	start.Done()
	done.Wait()

	winner := electSaveWinner(t, results)
	if winner.version != base+1 {
		t.Fatalf("winning version = %d, want %d", winner.version, base+1)
	}

	loaded, version, err := opened.Load(ctx, principal.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if version != winner.version || loaded.RefreshToken() != winner.token {
		t.Fatalf("stored record is version %d refresh %q, want version %d refresh %q",
			version, loaded.RefreshToken(), winner.version, winner.token)
	}
}

// TestConcurrentGarminWritersNeverLoseARotation is the read-modify-write loop a refresher
// actually runs: on conflict, reload and retry. Every writer must eventually commit, and
// the version must advance exactly once per commit.
func TestConcurrentGarminWritersNeverLoseARotation(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	if _, err := opened.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	const writers = 4
	const roundsEach = 5
	var group sync.WaitGroup
	for index := range writers {
		group.Go(func() {
			for round := range roundsEach {
				token := "writer-" + strconv.Itoa(index) + "-round-" + strconv.Itoa(round)
				if err := saveWithRetry(ctx, opened, principal.ID, token); err != nil {
					t.Errorf("writer %d round %d: %v", index, round, err)
					return
				}
			}
		})
	}
	group.Wait()

	_, version, err := opened.Load(ctx, principal.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := int64(1 + writers*roundsEach); version != want {
		t.Fatalf("final version = %d, want %d: every commit must advance it exactly once",
			version, want)
	}
}

// saveWithRetry is the read-modify-write loop with the retry the CAS contract requires.
func saveWithRetry(ctx context.Context, s *store.SQLiteStore, principalID, token string) error {
	const attempts = 100
	for range attempts {
		_, version, err := s.Load(ctx, principalID)
		if err != nil {
			return err
		}
		_, err = s.Save(ctx, principalID, newSQLTestTokens().WithRefreshToken(token), version)
		if err == nil {
			return nil
		}
		if !errors.Is(err, store.ErrVersionConflict) {
			return err
		}
	}
	return errors.New("gave up after too many version conflicts")
}

// TestConcurrentReadersAndWriterNeverSeeATornGarminRecord exercises transaction isolation:
// a reader sees a committed record, never a half-written one.
func TestConcurrentReadersAndWriterNeverSeeATornGarminRecord(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	if _, err := opened.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	const rounds = 15
	var group sync.WaitGroup
	group.Go(func() {
		for round := range rounds {
			err := saveWithRetry(ctx, opened, principal.ID, "round-"+strconv.Itoa(round))
			if err != nil {
				t.Errorf("Save round %d: %v", round, err)
				return
			}
		}
	})
	for range 3 {
		group.Go(func() {
			for range rounds {
				set, _, err := opened.Load(ctx, principal.ID)
				if err != nil {
					t.Errorf("Load: %v", err)
					return
				}
				// The di_token never changes; only the refresh token rotates. Seeing
				// anything else here would mean a partially visible write.
				if set.Token() != sqlTestToken {
					t.Errorf("Load saw a torn record: token %q", set.Token())
					return
				}
			}
		})
	}
	group.Wait()
}

// TestSeparatePrincipalsDoNotBlockEachOther documents that the CAS is per principal.
func TestSeparatePrincipalsDoNotBlockEachOther(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	const principals = 4
	ids := make([]string, principals)
	for index := range principals {
		principal, err := opened.CreatePrincipal(ctx, "rider"+strconv.Itoa(index)+"@example.com")
		if err != nil {
			t.Fatalf("CreatePrincipal %d: %v", index, err)
		}
		ids[index] = principal.ID
	}

	var group sync.WaitGroup
	for _, id := range ids {
		group.Go(func() {
			if _, err := opened.Save(ctx, id, newSQLTestTokens(), 0); err != nil {
				t.Errorf("Save for %s: %v", id, err)
			}
		})
	}
	group.Wait()

	for _, id := range ids {
		if _, version, err := opened.Load(ctx, id); err != nil || version != 1 {
			t.Fatalf("Load for %s: version %d err %v", id, version, err)
		}
	}
}

// TestConcurrentRefreshRotationsElectOneWinner is the rotation contract under contention.
// Several clients present the same refresh token at once — the retry storm a flaky network
// produces. Exactly one may receive a new generation. Every loser is refused, and because
// a loser's refusal means the token was presented twice, the family is revoked: a
// concurrent presentation is indistinguishable from a leaked token being replayed, and the
// store fails closed rather than guessing which it was.
func TestConcurrentRefreshRotationsElectOneWinner(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	const callers = 4
	results := make([]error, callers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range callers {
		suffix := strconv.Itoa(index)
		done.Go(func() {
			start.Wait()
			_, err := opened.RotateRefreshToken(ctx,
				rotation(grant.refresh, "access-"+suffix, "refresh-"+suffix))
			results[index] = err
		})
	}
	start.Done()
	done.Wait()

	var winners, refusals int
	for _, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, store.ErrRefreshTokenReuse), errors.Is(err, store.ErrTokenRevoked):
			refusals++
		default:
			t.Fatalf("unexpected RotateRefreshToken error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("got %d winners across %d concurrent rotations, want exactly 1", winners, callers)
	}
	if refusals != callers-1 {
		t.Fatalf("got %d refusals, want %d", refusals, callers-1)
	}

	if _, err := opened.LookupAccessToken(ctx, grant.access); !errors.Is(err, store.ErrTokenRevoked) {
		t.Errorf("after a contended rotation: err = %v, want ErrTokenRevoked", err)
	}
}

// TestConcurrentAuthCodeRedemptionsElectOneWinner: an authorization code redeems once.
func TestConcurrentAuthCodeRedemptionsElectOneWinner(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)
	code := seedCode(t, opened, principal.ID, client.ID, testCode)

	const callers = 4
	results := make([]error, callers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range callers {
		done.Go(func() {
			start.Wait()
			_, err := opened.ConsumeAuthCode(ctx, code)
			results[index] = err
		})
	}
	start.Done()
	done.Wait()

	var winners, replays int
	for _, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, store.ErrCodeAlreadyUsed):
			replays++
		default:
			t.Fatalf("unexpected ConsumeAuthCode error: %v", err)
		}
	}
	if winners != 1 || replays != callers-1 {
		t.Fatalf("got %d winners and %d replays, want 1 winner", winners, replays)
	}
}

// TestConcurrentRevocationsAreIdempotent: several callers revoking at once must all
// succeed, and exactly one of them may report having done the work.
func TestConcurrentRevocationsAreIdempotent(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	const callers = 4
	results := make([]store.RevocationResult, callers)
	errs := make([]error, callers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range callers {
		done.Go(func() {
			start.Wait()
			result, err := opened.RevokeConsent(ctx, grant.principal.ID, grant.client.ID)
			results[index] = result
			errs[index] = err
		})
	}
	start.Done()
	done.Wait()

	effective := 0
	for index, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: RevokeConsent returned %v; a revocation must be idempotent",
				index, err)
		}
		if results[index].FamiliesRevoked > 0 {
			effective++
		}
	}
	if effective != 1 {
		t.Fatalf("%d callers reported revoking the family, want exactly 1", effective)
	}

	if _, err := opened.LookupAccessToken(ctx, grant.access); !errors.Is(err, store.ErrTokenRevoked) {
		t.Errorf("access token after the contended revocation: err = %v, want ErrTokenRevoked", err)
	}
}

// TestConcurrentUnlinksAreIdempotent is the same property for the wider cascade, and it
// also exercises the fail-closed post-condition under contention: no caller may see
// ErrIncompleteUnlink for losing a race.
func TestConcurrentUnlinksAreIdempotent(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	err := opened.LinkGarminAccount(ctx, grant.principal.ID, store.GarminIdentity{
		AccountID: store.NewSecret(testGarminAccount),
	})
	if err != nil {
		t.Fatalf("LinkGarminAccount: %v", err)
	}
	if _, err := opened.Save(ctx, grant.principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	const callers = 4
	deletions := make([]int, callers)
	errs := make([]error, callers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range callers {
		done.Go(func() {
			start.Wait()
			result, err := opened.UnlinkGarminAccount(ctx, grant.principal.ID)
			deletions[index] = result.GarminTokensDeleted
			errs[index] = err
		})
	}
	start.Done()
	done.Wait()

	total := 0
	for index, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: UnlinkGarminAccount returned %v; it must be idempotent", index, err)
		}
		total += deletions[index]
	}
	if total != 1 {
		t.Fatalf("the garmin token record was reported deleted %d times, want exactly 1", total)
	}

	if _, err := opened.GarminIdentity(ctx, grant.principal.ID); !errors.Is(err, store.ErrNoTokens) {
		t.Errorf("garmin identity after the contended unlink: err = %v, want ErrNoTokens", err)
	}
}

// TestConcurrentPrincipalCreationForOneEmailElectsOneWinner: the unique index on the
// normalized email is what makes a double registration impossible.
func TestConcurrentPrincipalCreationForOneEmailElectsOneWinner(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	const callers = 4
	errs := make([]error, callers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range callers {
		done.Go(func() {
			start.Wait()
			_, err := opened.CreatePrincipal(ctx, testEmail)
			errs[index] = err
		})
	}
	start.Done()
	done.Wait()

	var winners, duplicates int
	for _, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, store.ErrPrincipalExists):
			duplicates++
		default:
			t.Fatalf("unexpected CreatePrincipal error: %v", err)
		}
	}
	if winners != 1 || duplicates != callers-1 {
		t.Fatalf("got %d winners and %d duplicates, want exactly 1 winner", winners, duplicates)
	}
}

// TestConcurrentCleanupAndTrafficDoNotInterfere runs the maintenance sweep against live
// reads and writes, which is how it will actually be scheduled.
func TestConcurrentCleanupAndTrafficDoNotInterfere(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	var group sync.WaitGroup
	group.Go(func() {
		for range 10 {
			if _, err := opened.Cleanup(ctx, 10); err != nil {
				t.Errorf("Cleanup: %v", err)
				return
			}
		}
	})
	group.Go(func() {
		for range 10 {
			if _, err := opened.LookupAccessToken(ctx, grant.access); err != nil {
				t.Errorf("LookupAccessToken: %v", err)
				return
			}
		}
	})
	group.Go(func() {
		for index := range 10 {
			err := opened.RecordAuditEvent(ctx, store.AuditEvent{
				Kind:        "token.checked",
				Outcome:     store.AuditAllowed,
				PrincipalID: grant.principal.ID,
				OccurredAt:  time.Date(2026, 8, 14, 12, index, 0, 0, time.UTC),
			})
			if err != nil {
				t.Errorf("RecordAuditEvent: %v", err)
				return
			}
		}
	})
	group.Wait()
}
