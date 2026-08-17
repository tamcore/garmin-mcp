package store_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

func TestConcurrentRefreshRacingTheResealerNeverLosesEitherWrite(t *testing.T) {
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

	const refreshRounds = 30
	var group sync.WaitGroup
	var refreshErr, resealErr error

	group.Go(func() {
		for round := range refreshRounds {
			token := "refreshed-" + strconv.Itoa(round)
			if err := saveWithRetry(ctx, rotating, principal.ID, token); err != nil {
				refreshErr = err
				return
			}
		}
	})
	group.Go(func() {
		for range 5 {
			if _, err := rotating.ResealToActiveKey(ctx); err != nil {
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

	// One final pass proves completion even if the last refresh committed after
	// the last reseal pass had already finished its scan.
	if _, err := rotating.ResealToActiveKey(ctx); err != nil {
		t.Fatalf("final ResealToActiveKey: %v", err)
	}
	remaining, err := rotating.RemainingToReseal(ctx)
	if err != nil {
		t.Fatalf("RemainingToReseal: %v", err)
	}
	if !remaining.Done() {
		t.Fatalf("RemainingToReseal after the race = %+v, want everything done", remaining)
	}

	loaded, version, err := rotating.Load(ctx, principal.ID)
	if err != nil {
		t.Fatalf("Load after the race: %v", err)
	}
	if want := int64(1 + refreshRounds); version != want {
		t.Fatalf("final version = %d, want %d: the refresh loop's writes must not have been lost", version, want)
	}
	if want := "refreshed-" + strconv.Itoa(refreshRounds-1); loaded.RefreshToken() != want {
		t.Fatalf("final refresh token = %q, want %q (the last round's value)", loaded.RefreshToken(), want)
	}
}

// TestResealCoversPrincipalIdentitiesAndAuthTransactionStates proves the other two
// sealed record kinds this package encrypts into — the Garmin identity linkage and
// an authorization transaction's client state — are re-sealed too, not just the
// Garmin token set, and that the database-wide index root is covered as well.
func TestResealCoversPrincipalIdentitiesAndAuthTransactionStates(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	oldKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, oldKey, nil)
	ctx := context.Background()
	principal := seedPrincipal(t, seed)
	if err := seed.LinkGarminAccount(ctx, principal.ID, store.GarminIdentity{
		AccountID: store.NewSecret(testGarminAccount), DisplayName: testDisplayName,
	}); err != nil {
		t.Fatalf("LinkGarminAccount: %v", err)
	}
	client := seedClient(t, seed)
	handle := store.NewSecret("transaction-handle-under-test")
	if err := seed.PutAuthTransaction(ctx, store.AuthTransactionDraft{
		Handle:        handle,
		ClientID:      client.ID,
		RedirectURI:   testRedirectURI,
		Scopes:        []string{testScope},
		ClientState:   store.NewSecret("opaque-client-state"),
		CodeChallenge: "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		Lifetime:      10 * time.Minute,
	}); err != nil {
		t.Fatalf("PutAuthTransaction: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)
	rotating := openStoreWithKeys(t, path, targetKey, []cryptostore.Key{oldKey})

	report, err := rotating.ResealToActiveKey(ctx)
	if err != nil {
		t.Fatalf("ResealToActiveKey: %v", err)
	}
	if report.PrincipalIdentities != 1 {
		t.Errorf("principal identities resealed = %d, want 1", report.PrincipalIdentities)
	}
	if report.AuthTransactionStates != 1 {
		t.Errorf("authorization transaction states resealed = %d, want 1", report.AuthTransactionStates)
	}
	if !report.IndexRoot {
		t.Errorf("index root resealed = false, want true")
	}

	remaining, err := rotating.RemainingToReseal(ctx)
	if err != nil {
		t.Fatalf("RemainingToReseal: %v", err)
	}
	if !remaining.Done() {
		t.Fatalf("RemainingToReseal = %+v, want everything done", remaining)
	}

	targetOnly := openStoreWithKeys(t, path, targetKey, nil)
	identity, err := targetOnly.GarminIdentity(ctx, principal.ID)
	if err != nil {
		t.Fatalf("GarminIdentity with only the target key: %v", err)
	}
	if identity.AccountID.Reveal() != testGarminAccount {
		t.Errorf("resealed garmin identity account = %q, want %q", identity.AccountID.Reveal(), testGarminAccount)
	}

	transaction, err := targetOnly.AuthTransaction(ctx, handle)
	if err != nil {
		t.Fatalf("AuthTransaction with only the target key: %v", err)
	}
	if transaction.ClientState.Reveal() != "opaque-client-state" {
		t.Errorf("resealed client state = %q, want %q", transaction.ClientState.Reveal(), "opaque-client-state")
	}
}
