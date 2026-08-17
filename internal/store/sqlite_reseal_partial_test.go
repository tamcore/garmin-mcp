package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// TestResealToActiveKeyReportsPartialProgressOnAnUnreadableRow is item 8(b) of
// the fix list: one unreadable row anywhere must not discard the counts
// ResealToActiveKey already accumulated for tables it finished cleanly before
// reaching it. Without the fix, ResealToActiveKey returned the zero-value
// ResealReport alongside the error the moment ANY table's scan failed, so an
// operator whose garmin_token_sets and principal identities tables both fully
// resealed would see 0 for both the instant one corrupt auth_transactions row
// made the whole call fail — indistinguishable from a run that touched
// nothing, with no way to tell that only the last table needs another look
// and the retiring key can never be confirmed clear for the ones already done.
func TestResealToActiveKeyReportsPartialProgressOnAnUnreadableRow(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	oldKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, oldKey, nil)
	ctx := context.Background()
	principal := seedPrincipal(t, seed)
	if _, err := seed.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := seed.LinkGarminAccount(ctx, principal.ID, store.GarminIdentity{
		AccountID: store.NewSecret(testGarminAccount), DisplayName: testDisplayName,
	}); err != nil {
		t.Fatalf("LinkGarminAccount: %v", err)
	}

	// A pending authorization transaction whose client state gets corrupted
	// below, so resealAllAuthTransactionStates — the THIRD table
	// ResealToActiveKey re-seals, after garmin_token_sets and principals —
	// fails while the first two tables have already fully succeeded.
	client := seedClient(t, seed)
	handle := store.NewSecret("transaction-handle-under-test")
	if err := seed.PutAuthTransaction(ctx, store.AuthTransactionDraft{
		Handle:        handle,
		ClientID:      client.ID,
		RedirectURI:   testRedirectURI,
		Scopes:        []string{testScope},
		ClientState:   store.NewSecret("opaque-client-state"),
		CodeChallenge: testChallenge,
		Lifetime:      10 * time.Minute,
	}); err != nil {
		t.Fatalf("PutAuthTransaction: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	// Corrupt the transaction's client state: sealed under a key version no
	// store opened below ever holds, so decrypting it during the reseal scan
	// fails closed rather than silently skipping the row.
	strayKey := mustGenerateKey(t, 99)
	handleHash := handleHashForTest(t, path)
	strayPayload, err := cryptostore.Encrypt(strayKey, handleHash, "oauth_client_state", []byte("plaintext-placeholder"))
	if err != nil {
		t.Fatalf("Encrypt the stray client state: %v", err)
	}
	updateRawAuthTransactionStateSealed(t, path, handleHash, strayPayload, 99)

	targetKey := mustGenerateKey(t, 2)
	rotating := openStoreWithKeys(t, path, targetKey, []cryptostore.Key{oldKey})

	report, err := rotating.ResealToActiveKey(ctx)
	if err == nil {
		t.Fatal("ResealToActiveKey with an unreadable auth transaction succeeded, want an error")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("ResealToActiveKey failed on context, not the corrupt row: %v", err)
	}

	// The decisive assertions: the two tables that fully succeeded before the
	// failure must still be reported, not zeroed out by the later failure.
	if report.GarminTokenSets != 1 {
		t.Errorf("report.GarminTokenSets = %d, want 1 (that table fully resealed before the failure)",
			report.GarminTokenSets)
	}
	if report.PrincipalIdentities != 1 {
		t.Errorf("report.PrincipalIdentities = %d, want 1 (that table fully resealed before the failure)",
			report.PrincipalIdentities)
	}
}
