package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// The synthetic account the login seam tests resolve.
const (
	testLoginEmail     = "person@example.test"
	testLoginPrincipal = "11111111-2222-4333-8444-555555555555"
	testContinuation   = "continuation-capability-0001"
	testLoginAccount   = "900001"
	testStagingKey     = "staging-0000000000000000"
)

// testAccount is the confirmed Garmin account a resolved login carries.
func testAccount() store.Secret { return store.NewSecret(testLoginAccount) }

// fakeDirectory is a principal directory with no database behind it. The real
// resolve-link-save logic now lives in store.SQLiteStore.BindGarminAccount, tested
// against a real SQLite database in internal/store; this fake exists only so a
// cmd-level test can supply a principalDirectory without a database, for behavior
// that never reaches it (a login refused before any bind is attempted).
type fakeDirectory struct {
	byAccount map[string]store.Principal
}

func newFakeDirectory() *fakeDirectory {
	return &fakeDirectory{byAccount: make(map[string]store.Principal)}
}

func (f *fakeDirectory) PrincipalByGarminAccount(
	_ context.Context, accountID store.Secret,
) (store.Principal, error) {
	principal, ok := f.byAccount[accountID.Reveal()]
	if !ok {
		return store.Principal{}, store.ErrPrincipalNotFound
	}
	return principal, nil
}

func (f *fakeDirectory) BindGarminAccount(
	_ context.Context, in store.GarminBindInput,
) (store.Principal, error) {
	principal := store.Principal{ID: testLoginPrincipal, Email: in.Email, GarminLinked: true}
	f.byAccount[in.Account.Reveal()] = principal
	return principal, nil
}

// TestAcquireExistingGateSkipsTheGateForANewAccount covers the reason a fresh
// account needs no serialization: nothing can be refreshing a principal that does
// not exist yet, so the bind proceeds without ever taking the gate.
func TestAcquireExistingGateSkipsTheGateForANewAccount(t *testing.T) {
	seam := &remoteLogin{directory: newFakeDirectory(), gate: auth.NewTokenGate()}

	release, err := seam.acquireExistingGate(t.Context(), testAccount())
	if err != nil {
		t.Fatalf("acquireExistingGate returned error: %v", err)
	}
	release() // must not panic on the no-op release
}

// TestAcquireExistingGateTakesTheGateForAReturningAccount covers the other half:
// an account already linked to a principal must queue behind that principal's own
// gate — the same one the authenticator and refresher use — so this bind cannot
// race a concurrent refresh of that principal's token set.
func TestAcquireExistingGateTakesTheGateForAReturningAccount(t *testing.T) {
	directory := newFakeDirectory()
	directory.byAccount[testLoginAccount] = store.Principal{ID: testLoginPrincipal}
	gate := auth.NewTokenGate()
	seam := &remoteLogin{directory: directory, gate: gate}

	release, err := seam.acquireExistingGate(t.Context(), testAccount())
	if err != nil {
		t.Fatalf("acquireExistingGate returned error: %v", err)
	}
	defer release()

	// A second acquire for the same principal must block until release runs; a
	// bounded context proves the slot is actually held rather than a no-op.
	blocked, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := gate.Acquire(blocked, testLoginPrincipal); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a concurrent acquire on the same principal returned %v, want context.DeadlineExceeded", err)
	}
}

// TestRemoteLoginRefusesALoginWithNoGarminAccount is what the isolation key rests
// on: without an account identifier there is nothing to key a principal on, and
// falling back to the email would key it on exactly the value that must never be
// the boundary. The login is refused and nothing is left pending or staged.
func TestRemoteLoginRefusesALoginWithNoGarminAccount(t *testing.T) {
	staging, err := newStagedTokens(&fakeTokenStore{}, 4, time.Minute, nil)
	if err != nil {
		t.Fatalf("newStagedTokens returned error: %v", err)
	}
	seam := &remoteLogin{
		directory: newFakeDirectory(),
		pending:   newPendingLogins(4, time.Minute, nil),
		staging:   staging,
		gate:      auth.NewTokenGate(),
	}

	_, err = seam.attempt(t.Context(), testStagingKey, testLoginEmail, auth.Result{})

	if !errors.Is(err, ErrNoGarminAccount) {
		t.Fatalf("attempt returned %v, want ErrNoGarminAccount", err)
	}
	if seam.pending.size() != 0 {
		t.Errorf("the registry holds %d entries after a refused login, want none",
			seam.pending.size())
	}
	if staging.inFlight() != 0 {
		t.Errorf("%d logins are still staged after a refused login, want none",
			staging.inFlight())
	}
}

// fakeTokenStore is a token store nothing reaches: the refused login above must
// never delegate a write to it.
type fakeTokenStore struct{}

func (fakeTokenStore) Load(context.Context, string) (auth.TokenSet, int64, error) {
	return auth.TokenSet{}, 0, auth.ErrNoTokens
}

func (fakeTokenStore) Save(context.Context, string, auth.TokenSet, int64) (int64, error) {
	return 0, errors.New("cmd: the test token store must not be written to")
}

func (fakeTokenStore) Delete(context.Context, string) error { return nil }

// TestRemoteLoginRefusesAnUnknownContinuation is the rule that keeps one browser's
// one-time code from answering another's login: the principal comes from the
// registry, so a capability the registry does not know cannot be continued at all.
func TestRemoteLoginRefusesAnUnknownContinuation(t *testing.T) {
	seam := &remoteLogin{pending: newPendingLogins(4, time.Minute, nil)}

	if _, err := seam.CompleteMFA(t.Context(), testContinuation, "000000"); err == nil {
		t.Fatal("CompleteMFA accepted a continuation no login opened")
	}
}

// TestPendingLoginsExpireAndAreBounded covers the registry's two protections: an
// abandoned challenge stops resolving on its own, and a flood cannot grow the
// process without limit.
func TestPendingLoginsExpireAndAreBounded(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	pending := newPendingLogins(2, time.Minute, clock)

	if err := pending.put(testContinuation, testLoginPrincipal, testLoginEmail); err != nil {
		t.Fatalf("put returned error: %v", err)
	}
	entry, ok := pending.get(testContinuation)
	if !ok || entry.principal != testLoginPrincipal || entry.email != testLoginEmail {
		t.Fatalf("get = %+v, %v, want the recorded login", entry, ok)
	}

	if err := pending.put("second", testLoginPrincipal, testLoginEmail); err != nil {
		t.Fatalf("put returned error: %v", err)
	}
	if err := pending.put("third", testLoginPrincipal, testLoginEmail); !errors.Is(
		err, errTooManyPendingLogins) {
		t.Errorf("a full registry returned %v, want errTooManyPendingLogins", err)
	}

	now = now.Add(2 * time.Minute)
	if _, ok := pending.get(testContinuation); ok {
		t.Error("an expired continuation still resolves")
	}
	if err := pending.put("third", testLoginPrincipal, testLoginEmail); err != nil {
		t.Errorf("the registry did not recover after its entries expired: %v", err)
	}
}

// TestPendingLoginsForgetATerminalContinuation keeps a completed login from
// leaving an association behind that a later capability could match.
func TestPendingLoginsForgetATerminalContinuation(t *testing.T) {
	pending := newPendingLogins(4, time.Minute, nil)

	if err := pending.put(testContinuation, testLoginPrincipal, testLoginEmail); err != nil {
		t.Fatalf("put returned error: %v", err)
	}
	pending.drop(testContinuation)

	if _, ok := pending.get(testContinuation); ok {
		t.Error("a dropped continuation still resolves")
	}
	if pending.size() != 0 {
		t.Errorf("the registry holds %d entries, want none", pending.size())
	}
}

// TestSQLiteTokensRoundTripUnderCompareAndSet covers the adapter the remote graph
// stores Garmin tokens through, including the guarantee the shared gate exists to
// protect: a write against a stale version is refused rather than applied, so a
// login cannot clobber a token set a concurrent refresh just rotated.
func TestSQLiteTokensRoundTripUnderCompareAndSet(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	tokens := remote.deps.tokens

	principal, err := remote.sqlite.CreatePrincipal(t.Context(), testLoginEmail)
	if err != nil {
		t.Fatalf("CreatePrincipal returned error: %v", err)
	}
	if _, _, err := tokens.Load(t.Context(), principal.ID); !errors.Is(err, auth.ErrNoTokens) {
		t.Errorf("Load of an unlinked principal = %v, want auth.ErrNoTokens", err)
	}

	expiry := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	set := auth.NewTokenSet("di-token-synthetic", "di-refresh-synthetic", "DI_CLIENT_TEST", expiry)
	version, err := tokens.Save(t.Context(), principal.ID, set, 0)
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded, loadedVersion, err := tokens.Load(t.Context(), principal.ID)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loadedVersion != version || loaded.Token() != "di-token-synthetic" {
		t.Errorf("the token set did not survive the round trip: version=%d", loadedVersion)
	}

	if _, err := tokens.Save(t.Context(), principal.ID, set, version-1); !errors.Is(
		err, auth.ErrVersionConflict) {
		t.Errorf("a stale write returned %v, want auth.ErrVersionConflict", err)
	}
	if err := tokens.Delete(t.Context(), principal.ID); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, _, err := tokens.Load(t.Context(), principal.ID); !errors.Is(err, auth.ErrNoTokens) {
		t.Errorf("Load after Delete = %v, want auth.ErrNoTokens", err)
	}
}

// TestGrantedScopesFailClosedWithoutAToken is the policy's half of the bearer
// rule: a request that carries no verified grant yields an error, not an empty
// scope set, because the policy fails closed on an error and would read an empty
// set as a caller who legitimately holds nothing.
func TestGrantedScopesFailClosedWithoutAToken(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))

	scopes, err := remote.deps.scopes.GrantedScopes(t.Context())
	if err == nil {
		t.Fatalf("GrantedScopes returned %v and no error for an unauthenticated context", scopes)
	}
	if scopes != nil {
		t.Errorf("GrantedScopes returned %v alongside its error", scopes)
	}
}
