package oauthstore_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/oauthserver"
)

// The contract obligations the interface documents, exercised against the real
// SQLite store with real goroutines. A fake would prove nothing here: the claims
// are about what the database does when two requests arrive at once.

// racers is how many goroutines contend for one record. Eight is enough to lose
// reliably and small enough to stay inside the connection pool's busy timeout.
const racers = 8

// outcome is what one racing goroutine got back.
type outcome struct {
	err error
}

// race runs call in racers goroutines released together and collects the results.
func race(call func(index int) error) []outcome {
	var (
		start    sync.WaitGroup
		finished sync.WaitGroup
	)
	start.Add(1)
	outcomes := make([]outcome, racers)
	for index := range racers {
		finished.Go(func() {
			start.Wait()
			outcomes[index] = outcome{err: call(index)}
		})
	}
	start.Done()
	finished.Wait()
	return outcomes
}

// countWinners reports how many goroutines succeeded and checks every loser against
// the sentinel the contract names.
func countWinners(t *testing.T, outcomes []outcome, loserSentinel error) int {
	t.Helper()
	winners := 0
	for _, got := range outcomes {
		switch {
		case got.err == nil:
			winners++
		case errors.Is(got.err, loserSentinel):
		default:
			t.Errorf("a loser got %v, want %v", got.err, loserSentinel)
		}
	}
	return winners
}

// ConsumeCode is atomic: two concurrent redemptions give exactly one success and one
// already-used error.
func TestConsumeCodeElectsExactlyOneRedeemer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code := f.code("race-code")
	if err := f.adapter.SaveCode(ctx, code); err != nil {
		t.Fatalf("SaveCode: %v", err)
	}

	outcomes := race(func(int) error {
		_, err := f.adapter.ConsumeCode(ctx, code.Lookup)
		return err
	})

	if winners := countWinners(t, outcomes, oauthserver.ErrCodeAlreadyUsed); winners != 1 {
		t.Fatalf("%d goroutines redeemed one code, want exactly 1", winners)
	}
}

// ConsumeTransaction is an atomic read-and-delete: exactly one concurrent caller
// gets the record. A compare-and-set is explicitly not sufficient, because two
// callers can serialize and each win its own compare-and-set in turn.
func TestConsumeTransactionElectsExactlyOneCaller(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	transaction := f.transaction("race-transaction", oauthserver.ClientState{})
	if err := f.adapter.CreateTransaction(ctx, transaction); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	var (
		mu      sync.Mutex
		records []oauthserver.Transaction
	)
	outcomes := race(func(int) error {
		consumed, err := f.adapter.ConsumeTransaction(ctx, transaction.Lookup)
		if err == nil {
			mu.Lock()
			records = append(records, consumed)
			mu.Unlock()
		}
		return err
	})

	if winners := countWinners(t, outcomes, oauthserver.ErrTransactionNotFound); winners != 1 {
		t.Fatalf("%d goroutines consumed one transaction, want exactly 1", winners)
	}
	if len(records) != 1 || records[0].Lookup != transaction.Lookup {
		t.Fatalf("%d records came back, want exactly the one that was stored", len(records))
	}
}

// RotateRefreshToken is atomic, and when the presented token is already consumed it
// revokes the whole family in the same transaction, reports reuse, and stores
// nothing.
func TestRotateRefreshTokenElectsOneWinnerAndKillsTheFamily(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access, presented := f.seedFamily(t, "family-race-rotate", "race-rotate")

	replacements := make([]oauthserver.RefreshToken, racers)
	outcomes := race(func(index int) error {
		nextAccess, nextRefresh := f.pair(presented.Family,
			"race-rotate-"+strconv.Itoa(index), 1)
		replacements[index] = nextRefresh
		return f.adapter.RotateRefreshToken(ctx, presented.Lookup, nextAccess, nextRefresh)
	})

	if winners := countWinners(t, outcomes, oauthserver.ErrRefreshTokenReused); winners != 1 {
		t.Fatalf("%d goroutines rotated one refresh token, want exactly 1", winners)
	}
	if _, err := f.adapter.AccessToken(ctx, access.Lookup); !errors.Is(
		err, oauthserver.ErrTokenRevoked) {
		t.Fatalf("the family survived the replay: %v", err)
	}
	assertLosersStoredNothing(t, f, outcomes, replacements)
}

// assertLosersStoredNothing checks that every goroutine that reported reuse left no
// row behind: a reuse that stored a pair would hand an attacker a live token.
func assertLosersStoredNothing(t *testing.T, f fixture, outcomes []outcome,
	replacements []oauthserver.RefreshToken,
) {
	t.Helper()
	ctx := context.Background()
	for index, got := range outcomes {
		if got.err == nil {
			continue
		}
		_, err := f.adapter.RefreshToken(ctx, replacements[index].Lookup)
		if !errors.Is(err, oauthserver.ErrTokenNotFound) {
			t.Errorf("a refused rotation stored a refresh token: %v", err)
		}
	}
}

// RevokeConsent cascades transactionally and fails closed, under contention.
func TestRevokeConsentIsSafeUnderContention(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access, _ := f.seedFamily(t, "family-race-consent", "race-consent")

	outcomes := race(func(int) error { return f.adapter.RevokeConsent(ctx, f.consentKey()) })
	for _, got := range outcomes {
		if got.err != nil {
			t.Fatalf("a concurrent revocation failed: %v", got.err)
		}
	}

	if _, err := f.adapter.Consent(ctx, f.consentKey()); !errors.Is(
		err, oauthserver.ErrConsentNotFound) {
		t.Fatalf("the consent survived: %v", err)
	}
	if _, err := f.adapter.AccessToken(ctx, access.Lookup); !errors.Is(
		err, oauthserver.ErrTokenRevoked) {
		t.Fatalf("a token survived the cascade: %v", err)
	}
}

// RevokePrincipal cascades transactionally and fails closed, under contention.
func TestRevokePrincipalIsSafeUnderContention(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access, refresh := f.seedFamily(t, "family-race-principal", "race-principal")

	outcomes := race(func(int) error { return f.adapter.RevokePrincipal(ctx, f.principal) })
	for _, got := range outcomes {
		if got.err != nil {
			t.Fatalf("a concurrent revocation failed: %v", got.err)
		}
	}

	if err := readRevoked(f, access.Lookup, "access"); err != nil {
		t.Error(err)
	}
	if err := readRevoked(f, refresh.Lookup, "refresh"); err != nil {
		t.Error(err)
	}
}

// readRevoked reports whether the named token reads as revoked.
func readRevoked(f fixture, lookup oauthserver.Lookup, kind string) error {
	ctx := context.Background()
	var err error
	if kind == "access" {
		_, err = f.adapter.AccessToken(ctx, lookup)
	} else {
		_, err = f.adapter.RefreshToken(ctx, lookup)
	}
	if !errors.Is(err, oauthserver.ErrTokenRevoked) {
		return fmt.Errorf("the %s token reports %v, want ErrTokenRevoked", kind, err)
	}
	return nil
}
