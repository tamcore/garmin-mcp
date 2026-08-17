package store_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// TestBindGarminAccountEndToEnd is the successful path: a first-time account
// mints and links a principal and stores its tokens, and a second login for the
// same account is the same principal with its tokens updated.
func TestBindGarminAccountEndToEnd(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	account := store.NewSecret(testGarminAccount)

	first, err := opened.BindGarminAccount(ctx, store.GarminBindInput{
		Account:     account,
		Email:       testEmail,
		DisplayName: testDisplayName,
		Tokens:      newSQLTestTokens(),
	})
	if err != nil {
		t.Fatalf("BindGarminAccount: %v", err)
	}
	if !first.GarminLinked {
		t.Error("GarminLinked = false after a successful bind")
	}

	loadedTokens, version, err := opened.Load(ctx, first.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loadedTokens.Token() != sqlTestToken || version != 1 {
		t.Errorf("Load = %+v, version %d, want the bound token set at version 1", loadedTokens, version)
	}

	identity, err := opened.GarminIdentity(ctx, first.ID)
	if err != nil {
		t.Fatalf("GarminIdentity: %v", err)
	}
	if identity.AccountID.Reveal() != testGarminAccount || identity.DisplayName != testDisplayName {
		t.Errorf("GarminIdentity = %+v, want the bound linkage", identity)
	}

	// A second login for the same account, under a different email, resolves to
	// the same principal: the account is the key, the email is only the handle.
	second, err := opened.BindGarminAccount(ctx, store.GarminBindInput{
		Account:     account,
		Email:       "renamed@example.com",
		DisplayName: testDisplayName,
		Tokens:      store.NewTokenSet("di-token-second", "di-refresh-second", sqlTestClientID, loadedTokens.ExpiresAt()),
	})
	if err != nil {
		t.Fatalf("second BindGarminAccount: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second bind resolved %q, want the existing principal %q", second.ID, first.ID)
	}

	updated, updatedVersion, err := opened.Load(ctx, first.ID)
	if err != nil {
		t.Fatalf("Load after second bind: %v", err)
	}
	if updated.Token() != "di-token-second" || updatedVersion != 2 {
		t.Errorf("Load after second bind = %+v, version %d, want the new token set at version 2",
			updated, updatedVersion)
	}
}

// TestBindGarminAccountRollsBackOnTokenSaveFailure covers the first, and worst
// documented, half-write: a token-save failure on a brand-new account must leave
// no new principal and no linkage behind, not a linked but tokenless one.
func TestBindGarminAccountRollsBackOnTokenSaveFailure(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	account := store.NewSecret(testGarminAccount)

	_, err := opened.BindGarminAccount(ctx, store.GarminBindInput{
		Account:     account,
		Email:       testEmail,
		DisplayName: testDisplayName,
		Tokens:      store.TokenSet{}, // zero: saveTokensTx refuses this and fails the transaction
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("BindGarminAccount: err = %v, want ErrInvalidArgument", err)
	}

	if _, err := opened.PrincipalByGarminAccount(ctx, account); !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByGarminAccount: err = %v, want ErrPrincipalNotFound — no linkage must survive", err)
	}
	if _, err := opened.PrincipalByEmail(ctx, testEmail); !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByEmail: err = %v, want ErrPrincipalNotFound — no principal must claim the email", err)
	}
}

// TestBindGarminAccountRollsBackOnLinkFailure covers the second documented
// half-write: a failure between minting the principal and completing the linkage
// must leave no principal claiming the email, because that principal would
// otherwise durably block ever registering that email again under a correct
// linkage.
func TestBindGarminAccountRollsBackOnLinkFailure(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	account := store.NewSecret(testGarminAccount)

	// A display name over the stored identity's length limit fails sealing the
	// linkage, after the principal would have been minted and before it is
	// linked or any token is saved.
	overlong := strings.Repeat("x", 257)

	_, err := opened.BindGarminAccount(ctx, store.GarminBindInput{
		Account:     account,
		Email:       testEmail,
		DisplayName: overlong,
		Tokens:      newSQLTestTokens(),
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("BindGarminAccount: err = %v, want ErrInvalidArgument", err)
	}

	if _, err := opened.PrincipalByEmail(ctx, testEmail); !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByEmail: err = %v, want ErrPrincipalNotFound — no principal must claim the email", err)
	}
	if _, err := opened.PrincipalByGarminAccount(ctx, account); !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByGarminAccount: err = %v, want ErrPrincipalNotFound", err)
	}
}

// TestBindGarminAccountReturningPrincipalKeepsItsTokensOnFailure is the fourth
// scenario: a returning principal that already holds a token set must keep it
// intact when a later bind's own token write fails, rather than losing it to a
// half-applied overwrite.
func TestBindGarminAccountReturningPrincipalKeepsItsTokensOnFailure(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	account := store.NewSecret(testGarminAccount)

	bound, err := opened.BindGarminAccount(ctx, store.GarminBindInput{
		Account:     account,
		Email:       testEmail,
		DisplayName: testDisplayName,
		Tokens:      newSQLTestTokens(),
	})
	if err != nil {
		t.Fatalf("first BindGarminAccount: %v", err)
	}

	_, err = opened.BindGarminAccount(ctx, store.GarminBindInput{
		Account:     account,
		Email:       testEmail,
		DisplayName: testDisplayName,
		Tokens:      store.TokenSet{}, // zero: fails the transaction after resolving the existing principal
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("second BindGarminAccount: err = %v, want ErrInvalidArgument", err)
	}

	stillThere, version, err := opened.Load(ctx, bound.ID)
	if err != nil {
		t.Fatalf("Load after the failed bind: %v", err)
	}
	if stillThere.Token() != sqlTestToken || version != 1 {
		t.Errorf("Load after the failed bind = %+v, version %d, want the original token set at version 1",
			stillThere, version)
	}
}

// TestBindGarminAccountConcurrentLoginsElectOnePrincipal drives two real
// goroutines binding the same Garmin account under two different emails at once.
// Exactly one principal must exist afterward, both callers must resolve to it,
// and the loser must leave no extra row — the concurrent half of the durable
// half-write defect.
func TestBindGarminAccountConcurrentLoginsElectOnePrincipal(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	account := store.NewSecret(testGarminAccount)

	emails := []string{"first@example.com", "second@example.com"}
	results := make([]store.Principal, len(emails))
	errs := make([]error, len(emails))

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index, email := range emails {
		done.Go(func() {
			start.Wait()
			results[index], errs[index] = opened.BindGarminAccount(ctx, store.GarminBindInput{
				Account:     account,
				Email:       email,
				DisplayName: testDisplayName,
				Tokens: store.NewTokenSet(
					"di-token-"+email, "di-refresh-"+email, sqlTestClientID, newSQLTestTokens().ExpiresAt()),
			})
		})
	}
	start.Done()
	done.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("BindGarminAccount(%s): %v", emails[index], err)
		}
	}
	if results[0].ID != results[1].ID {
		t.Fatalf("the two logins resolved %q and %q: one account became two principals",
			results[0].ID, results[1].ID)
	}

	owner, err := opened.PrincipalByGarminAccount(ctx, account)
	if err != nil {
		t.Fatalf("PrincipalByGarminAccount: %v", err)
	}
	if owner.ID != results[0].ID {
		t.Errorf("the linked owner is %q, want the resolved principal %q", owner.ID, results[0].ID)
	}

	// Neither loser's email may have minted a second, orphaned principal: each
	// email must resolve to the same one principal both bind calls returned.
	for _, email := range emails {
		byEmail, err := opened.PrincipalByEmail(ctx, email)
		if err != nil {
			continue // the losing goroutine's email is never registered, which is correct
		}
		if byEmail.ID != owner.ID {
			t.Errorf("email %q resolves to %q, a second principal besides the owner %q",
				email, byEmail.ID, owner.ID)
		}
	}
}

// TestBindGarminAccountRefusesAZeroAccount is the boundary check: without an
// account identifier there is nothing to key isolation on.
func TestBindGarminAccountRefusesAZeroAccount(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	_, err := opened.BindGarminAccount(ctx, store.GarminBindInput{
		Email:  testEmail,
		Tokens: newSQLTestTokens(),
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("BindGarminAccount with a zero account: err = %v, want ErrInvalidArgument", err)
	}
}
