package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Item 4 of the fix list: reconcilePrincipalIdentityKeyVersion,
// reconcileAuthTransactionStateKeyVersion and resealIndexRoot's own inline
// reconcile had no test at all, unlike garmin_token_sets'
// TestResealReconcilesAStaleKeyVersionColumnWithoutLoopingForever. A broken
// reconcile leaves the batch loop spinning with zero progress forever (the
// principal and auth-transaction cases) or leaves RemainingToReseal
// permanently non-zero (the index root case), so every reseal call here runs
// under a bounded context: a regression must fail the test rather than
// hanging the whole suite.

// updateRawPrincipalIdentitySealed overwrites a principals row's
// garmin_identity_sealed bytes and key_version column directly, bypassing
// this package's own re-sealing, so a test can plant the exact shape a pass
// interrupted between rewriting the content and recording the column leaves
// behind: content already at the target key, column still naming the retired
// one.
func updateRawPrincipalIdentitySealed(t *testing.T, path, principalID string, sealed []byte, keyVersion int) {
	t.Helper()
	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(
		`UPDATE principals SET garmin_identity_sealed = ?, key_version = ? WHERE id = ?`,
		sealed, keyVersion, principalID); err != nil {
		t.Fatalf("update raw principals row: %v", err)
	}
}

// rawPrincipalKeyVersion reads a principals row's key_version column
// directly, so a test can prove the reconcile actually rewrote the column
// rather than merely observing RemainingToReseal, which is content-based and
// would report done regardless of whether the column was ever touched.
func rawPrincipalKeyVersion(t *testing.T, path, principalID string) int {
	t.Helper()
	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	var version int
	if err := db.QueryRow(`SELECT key_version FROM principals WHERE id = ?`, principalID).Scan(&version); err != nil {
		t.Fatalf("read key_version: %v", err)
	}
	return version
}

// TestResealReconcilesAStalePrincipalIdentityKeyVersionColumnWithoutLoopingForever
// mirrors TestResealReconcilesAStaleKeyVersionColumnWithoutLoopingForever for
// the principals table.
func TestResealReconcilesAStalePrincipalIdentityKeyVersionColumnWithoutLoopingForever(t *testing.T) {
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
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)

	// Plant the disagreement: the content is already sealed under the target
	// key, but the key_version column still names the retired one.
	sealed, err := cryptostore.Encrypt(targetKey, principal.ID, "garmin_identity", []byte("plaintext-placeholder"))
	if err != nil {
		t.Fatalf("Encrypt with the target key: %v", err)
	}
	updateRawPrincipalIdentitySealed(t, path, principal.ID, sealed, 1)

	rotating := openStoreWithKeys(t, path, targetKey, []cryptostore.Key{oldKey})

	resealCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := rotating.ResealToActiveKey(resealCtx)
	if err != nil {
		t.Fatalf("ResealToActiveKey with a stale principal key_version column: %v, want it to terminate", err)
	}
	if report.PrincipalIdentities != 0 {
		t.Errorf("principal identities rewritten = %d, want 0 (only the column needed fixing, not the content)",
			report.PrincipalIdentities)
	}

	remaining, err := rotating.RemainingToReseal(ctx)
	if err != nil {
		t.Fatalf("RemainingToReseal: %v", err)
	}
	if remaining.PrincipalIdentities != 0 {
		t.Fatalf("RemainingToReseal.PrincipalIdentities = %d, want 0 once the column was reconciled",
			remaining.PrincipalIdentities)
	}

	// The decisive assertion: RemainingToReseal is content-based and would
	// report done even if the column reconcile silently did nothing, since the
	// content was already at the target key before this test ever called
	// ResealToActiveKey. Only reading the raw column back proves the reconcile
	// itself ran.
	if got := rawPrincipalKeyVersion(t, path, principal.ID); got != 2 {
		t.Fatalf("principals.key_version after reseal = %d, want 2 (the reconcile did not rewrite the column)", got)
	}
}

// updateRawAuthTransactionStateSealed overwrites an auth_transactions row's
// client_state_sealed bytes and client_state_key_version column directly,
// bypassing this package's own re-sealing.
func updateRawAuthTransactionStateSealed(t *testing.T, path, handleHash string, sealed []byte, keyVersion int) {
	t.Helper()
	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(
		`UPDATE auth_transactions SET client_state_sealed = ?, client_state_key_version = ? WHERE handle_hash = ?`,
		sealed, keyVersion, handleHash); err != nil {
		t.Fatalf("update raw auth_transactions row: %v", err)
	}
}

// handleHashForTest reads the handle_hash an AuthTransactionDraft's Handle
// hashed to, so the raw-update helper above can target the same row
// AuthTransaction itself would look up by the plaintext handle.
func handleHashForTest(t *testing.T, path string) string {
	t.Helper()
	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	var hash string
	if err := db.QueryRow(`SELECT handle_hash FROM auth_transactions LIMIT 1`).Scan(&hash); err != nil {
		t.Fatalf("read handle_hash: %v", err)
	}
	return hash
}

// rawAuthTransactionKeyVersion reads an auth_transactions row's
// client_state_key_version column directly, for the same reason
// rawPrincipalKeyVersion does.
func rawAuthTransactionKeyVersion(t *testing.T, path, handleHash string) int {
	t.Helper()
	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	var version int
	if err := db.QueryRow(
		`SELECT client_state_key_version FROM auth_transactions WHERE handle_hash = ?`, handleHash,
	).Scan(&version); err != nil {
		t.Fatalf("read client_state_key_version: %v", err)
	}
	return version
}

// TestResealReconcilesAStaleAuthTransactionStateKeyVersionColumnWithoutLoopingForever
// mirrors the same property for the auth_transactions table.
func TestResealReconcilesAStaleAuthTransactionStateKeyVersionColumnWithoutLoopingForever(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	oldKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, oldKey, nil)
	ctx := context.Background()
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

	handleHash := handleHashForTest(t, path)
	targetKey := mustGenerateKey(t, 2)

	sealed, err := cryptostore.Encrypt(targetKey, handleHash, "oauth_client_state",
		[]byte("plaintext-placeholder"))
	if err != nil {
		t.Fatalf("Encrypt with the target key: %v", err)
	}
	updateRawAuthTransactionStateSealed(t, path, handleHash, sealed, 1)

	rotating := openStoreWithKeys(t, path, targetKey, []cryptostore.Key{oldKey})

	resealCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := rotating.ResealToActiveKey(resealCtx)
	if err != nil {
		t.Fatalf("ResealToActiveKey with a stale auth transaction key_version column: %v, want it to terminate", err)
	}
	if report.AuthTransactionStates != 0 {
		t.Errorf("auth transaction states rewritten = %d, want 0 (only the column needed fixing, not the content)",
			report.AuthTransactionStates)
	}

	remaining, err := rotating.RemainingToReseal(ctx)
	if err != nil {
		t.Fatalf("RemainingToReseal: %v", err)
	}
	if remaining.AuthTransactionStates != 0 {
		t.Fatalf("RemainingToReseal.AuthTransactionStates = %d, want 0 once the column was reconciled",
			remaining.AuthTransactionStates)
	}

	// The decisive assertion, for the same reason given in the principal
	// identity test above: RemainingToReseal alone cannot distinguish a real
	// reconcile from one that silently did nothing.
	if got := rawAuthTransactionKeyVersion(t, path, handleHash); got != 2 {
		t.Fatalf("auth_transactions.client_state_key_version after reseal = %d, want 2 "+
			"(the reconcile did not rewrite the column)", got)
	}
}

// updateRawIndexRootSealed overwrites schema_meta's index_root_sealed bytes
// and encryption_key_version column directly, bypassing this package's own
// re-sealing.
func updateRawIndexRootSealed(t *testing.T, db *sql.DB, sealed []byte, keyVersion int) {
	t.Helper()
	if _, err := db.Exec(
		`UPDATE schema_meta SET index_root_sealed = ?, encryption_key_version = ? WHERE id = 1`,
		sealed, keyVersion); err != nil {
		t.Fatalf("update raw schema_meta row: %v", err)
	}
}

// rawIndexRootKeyVersion reads schema_meta's encryption_key_version column
// directly, for the same reason rawPrincipalKeyVersion does.
func rawIndexRootKeyVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	var version int
	if err := db.QueryRow(`SELECT encryption_key_version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil {
		t.Fatalf("read encryption_key_version: %v", err)
	}
	return version
}

// TestResealReconcilesAStaleIndexRootKeyVersionColumnWithoutLoopingForever
// mirrors the same property for resealIndexRoot's own inline reconcile: the
// index root's content is already at the target key, but
// encryption_key_version still names the retired one. Without the fix,
// RemainingToReseal would stay permanently non-zero because it is
// content-based and would keep disagreeing with a column that a broken
// reconcile never brought in line — the operator-facing consequence is a
// rotation that can never report done.
func TestResealReconcilesAStaleIndexRootKeyVersionColumnWithoutLoopingForever(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)
	oldKey := mustGenerateKey(t, 1)

	seed := openStoreWithKeys(t, path, oldKey, nil)
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	targetKey := mustGenerateKey(t, 2)

	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	sealed, err := cryptostore.Encrypt(targetKey, "-database-", "store_index_root",
		make([]byte, 32))
	if err != nil {
		_ = db.Close()
		t.Fatalf("Encrypt with the target key: %v", err)
	}
	updateRawIndexRootSealed(t, db, sealed, 1)
	if err := db.Close(); err != nil {
		t.Fatalf("close raw connection: %v", err)
	}

	rotating := openStoreWithKeys(t, path, targetKey, []cryptostore.Key{oldKey})
	ctx := context.Background()

	resealCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := rotating.ResealToActiveKey(resealCtx)
	if err != nil {
		t.Fatalf("ResealToActiveKey with a stale index root key_version column: %v, want it to terminate", err)
	}
	if report.IndexRoot {
		t.Errorf("index root reported rewritten = true, want false (only the column needed fixing, not the content)")
	}

	remaining, err := rotating.RemainingToReseal(ctx)
	if err != nil {
		t.Fatalf("RemainingToReseal: %v", err)
	}
	if remaining.IndexRootPending {
		t.Fatal("RemainingToReseal.IndexRootPending = true, want false once the column was reconciled")
	}

	// The decisive assertion, for the same reason given in the principal
	// identity test above: RemainingToReseal alone cannot distinguish a real
	// reconcile from one that silently did nothing.
	if got := rawIndexRootKeyVersion(t, path); got != 2 {
		t.Fatalf("schema_meta.encryption_key_version after reseal = %d, "+
			"want 2 (the reconcile did not rewrite the column)", got)
	}
}
