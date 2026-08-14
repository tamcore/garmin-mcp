package store_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/store"
)

func TestCreatePrincipalMintsAUUIDAndNormalizesTheEmail(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	principal, err := opened.CreatePrincipal(ctx, "  Rider@Example.COM  ")
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if principal.Email != testEmailNormalized {
		t.Errorf("Email = %q, want %q", principal.Email, testEmailNormalized)
	}
	// A version-4 UUID: 36 characters with '4' as the version nibble. The point is
	// that the id is random, not derived from the email.
	if len(principal.ID) != 36 || principal.ID[14] != '4' {
		t.Errorf("ID = %q, want a version 4 UUID", principal.ID)
	}
	if strings.Contains(principal.ID, "rider") || strings.Contains(principal.ID, "example") {
		t.Errorf("ID = %q: the isolation key must not be derived from the email", principal.ID)
	}
	if principal.GarminLinked {
		t.Error("a fresh principal must not report a linked Garmin account")
	}

	byID, err := opened.PrincipalByID(ctx, principal.ID)
	if err != nil || byID.ID != principal.ID {
		t.Fatalf("PrincipalByID: %+v err = %v", byID, err)
	}
	// Lookup by email accepts any spelling, because it normalizes the same way.
	byEmail, err := opened.PrincipalByEmail(ctx, "RIDER@example.com")
	if err != nil || byEmail.ID != principal.ID {
		t.Fatalf("PrincipalByEmail: %+v err = %v", byEmail, err)
	}
}

func TestCreatePrincipalRefusesADuplicateEmail(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := opened.CreatePrincipal(ctx, testEmail); err != nil {
		t.Fatalf("first CreatePrincipal: %v", err)
	}
	_, err := opened.CreatePrincipal(ctx, strings.ToUpper(testEmail))
	if !errors.Is(err, store.ErrPrincipalExists) {
		t.Fatalf("second CreatePrincipal: err = %v, want ErrPrincipalExists", err)
	}
}

func TestCreatePrincipalRefusesAnUnusableEmail(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	for _, email := range []string{
		"", "   ", "no-at-sign", "@leading", "trailing@", "two@at@signs",
		"has space@example.com", strings.Repeat("a", 250) + "@example.com",
	} {
		if _, err := opened.CreatePrincipal(ctx, email); !errors.Is(err, store.ErrInvalidArgument) {
			t.Errorf("CreatePrincipal(%q): err = %v, want ErrInvalidArgument", email, err)
		}
	}
}

func TestPrincipalLookupsReportAnUnknownPrincipal(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := opened.PrincipalByID(ctx, "nobody"); !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByID: err = %v, want ErrPrincipalNotFound", err)
	}
	_, err := opened.PrincipalByEmail(ctx, "nobody@example.com")
	if !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByEmail: err = %v, want ErrPrincipalNotFound", err)
	}
	_, err = opened.PrincipalByGarminAccount(ctx, store.NewSecret("unlinked-account"))
	if !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByGarminAccount: err = %v, want ErrPrincipalNotFound", err)
	}
}

func TestLinkGarminAccountStoresAnEncryptedLinkage(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	identity := store.GarminIdentity{
		AccountID:   store.NewSecret(testGarminAccount),
		DisplayName: testDisplayName,
	}
	if err := opened.LinkGarminAccount(ctx, principal.ID, identity); err != nil {
		t.Fatalf("LinkGarminAccount: %v", err)
	}

	loaded, err := opened.GarminIdentity(ctx, principal.ID)
	if err != nil {
		t.Fatalf("GarminIdentity: %v", err)
	}
	if loaded.AccountID.Reveal() != testGarminAccount {
		t.Error("the round-tripped account id does not match")
	}
	if loaded.DisplayName != testDisplayName {
		t.Errorf("DisplayName = %q, want %q", loaded.DisplayName, testDisplayName)
	}

	found, err := opened.PrincipalByGarminAccount(ctx, store.NewSecret(testGarminAccount))
	if err != nil || found.ID != principal.ID {
		t.Fatalf("PrincipalByGarminAccount: %+v err = %v", found, err)
	}
	if !found.GarminLinked {
		t.Error("GarminLinked = false after a successful link")
	}
}

// TestDatabaseHoldsNoPlaintextGarminAccountID mirrors the file store's
// TestRecordOnDiskHoldsNoPlaintextToken: the account id must be absent from the bytes,
// both as the HMAC column and inside the envelope.
func TestDatabaseHoldsNoPlaintextGarminAccountID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := testDBPath(t)

	opened, err := store.OpenSQLite(ctx, store.SQLiteConfig{Path: path, Key: testKey(t)})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	principal, err := opened.CreatePrincipal(ctx, testEmail)
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	err = opened.LinkGarminAccount(ctx, principal.ID, store.GarminIdentity{
		AccountID:   store.NewSecret(testGarminAccount),
		DisplayName: testDisplayName,
	})
	if err != nil {
		t.Fatalf("LinkGarminAccount: %v", err)
	}
	if _, err := opened.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Closing checkpoints the write-ahead log into the main database file, so the
	// scan below sees everything that was written.
	if err := opened.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatalf("read database: %v", err)
	}
	for label, needle := range map[string]string{
		"garmin account id": testGarminAccount,
		"di refresh token":  sqlTestRefreshToken,
		"di token":          sqlTestToken,
	} {
		if strings.Contains(string(raw), needle) {
			t.Errorf("the database file holds the %s in the clear", label)
		}
	}
}

// TestConcurrentGarminLinkElectsOneOwner is the defined behavior when the same Garmin
// account is linked through two flows at once: the first writer keeps the linkage, the
// second is refused with ErrGarminAccountLinked, and the account never ends up split
// across two principals.
func TestConcurrentGarminLinkElectsOneOwner(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	first, err := opened.CreatePrincipal(ctx, "first@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal first: %v", err)
	}
	second, err := opened.CreatePrincipal(ctx, "second@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal second: %v", err)
	}

	identity := store.GarminIdentity{AccountID: store.NewSecret(testGarminAccount)}
	results := make([]error, 2)
	principals := []string{first.ID, second.ID}

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index, principalID := range principals {
		done.Go(func() {
			start.Wait()
			results[index] = opened.LinkGarminAccount(ctx, principalID, identity)
		})
	}
	start.Done()
	done.Wait()

	winner := electLinkWinner(t, principals, results)
	owner, err := opened.PrincipalByGarminAccount(ctx, identity.AccountID)
	if err != nil {
		t.Fatalf("PrincipalByGarminAccount: %v", err)
	}
	if owner.ID != winner {
		t.Errorf("the account belongs to %s, want the winner %s", owner.ID, winner)
	}

	loser := principals[0]
	if loser == winner {
		loser = principals[1]
	}
	if _, err := opened.GarminIdentity(ctx, loser); !errors.Is(err, store.ErrNoTokens) {
		t.Errorf("the losing principal has a linkage: err = %v, want ErrNoTokens", err)
	}
}

// electLinkWinner asserts exactly one link succeeded and returns its principal.
func electLinkWinner(t *testing.T, principals []string, results []error) string {
	t.Helper()
	var winners, refusals int
	winner := ""
	for index, err := range results {
		switch {
		case err == nil:
			winners++
			winner = principals[index]
		case errors.Is(err, store.ErrGarminAccountLinked):
			refusals++
		default:
			t.Fatalf("unexpected LinkGarminAccount error: %v", err)
		}
	}
	if winners != 1 || refusals != 1 {
		t.Fatalf("got %d winners and %d refusals, want exactly 1 of each", winners, refusals)
	}
	return winner
}

// TestLinkingTheSameAccountToTheSamePrincipalIsIdempotent covers the repeated-login
// case: it must not be an error, and it must refresh the sealed identity.
func TestLinkingTheSameAccountToTheSamePrincipalIsIdempotent(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	account := store.NewSecret(testGarminAccount)
	for _, name := range []string{"First Name", "Renamed Later"} {
		err := opened.LinkGarminAccount(ctx, principal.ID, store.GarminIdentity{
			AccountID:   account,
			DisplayName: name,
		})
		if err != nil {
			t.Fatalf("LinkGarminAccount(%q): %v", name, err)
		}
	}

	loaded, err := opened.GarminIdentity(ctx, principal.ID)
	if err != nil {
		t.Fatalf("GarminIdentity: %v", err)
	}
	if loaded.DisplayName != "Renamed Later" {
		t.Errorf("DisplayName = %q, want the refreshed value", loaded.DisplayName)
	}
}

func TestLinkGarminAccountRefusesBadInput(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	err := opened.LinkGarminAccount(ctx, principal.ID, store.GarminIdentity{})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("no account id: err = %v, want ErrInvalidArgument", err)
	}
	err = opened.LinkGarminAccount(ctx, "", store.GarminIdentity{
		AccountID: store.NewSecret(testGarminAccount),
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("empty principal id: err = %v, want ErrInvalidArgument", err)
	}
	err = opened.LinkGarminAccount(ctx, testUnknownID,
		store.GarminIdentity{AccountID: store.NewSecret("other-account")})
	if !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("unknown principal: err = %v, want ErrPrincipalNotFound", err)
	}
}

// TestOpeningWithTheWrongKeyRefuses proves the store fails closed rather than creating
// a second derivation root, which would silently orphan every existing lookup value.
func TestOpeningWithTheWrongKeyRefuses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := testDBPath(t)

	first, err := store.OpenSQLite(ctx, store.SQLiteConfig{Path: path, Key: testKey(t)})
	if err != nil {
		t.Fatalf("first OpenSQLite: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// testKey generates fresh random material each call, so this is a different key
	// of the same version — exactly the operator mistake that must fail closed.
	second, err := store.OpenSQLite(ctx, store.SQLiteConfig{Path: path, Key: testKey(t)})
	if second != nil {
		t.Cleanup(func() { _ = second.Close() })
	}
	if !errors.Is(err, store.ErrCorruptRecord) {
		t.Fatalf("reopen with a different key: err = %v, want ErrCorruptRecord", err)
	}
}
