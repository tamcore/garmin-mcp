package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// What a redeemed code reports, and how an expired one is reported.

// TestRedeemedAuthCodeCarriesItsWholeWindow: a record with only an expiry cannot be
// rebuilt by a caller that has to return one.
func TestRedeemedAuthCodeCarriesItsWholeWindow(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)
	code := seedCode(t, opened, principal.ID, client.ID, testCode)

	redeemed, err := opened.ConsumeAuthCode(ctx, code)
	if err != nil {
		t.Fatalf("ConsumeAuthCode: %v", err)
	}
	if !redeemed.IssuedAt.Equal(clock.Now().UTC()) {
		t.Errorf("IssuedAt = %s, want %s", redeemed.IssuedAt, clock.Now().UTC())
	}
	if !redeemed.ExpiresAt.Equal(clock.Now().Add(10 * time.Minute).UTC()) {
		t.Errorf("ExpiresAt = %s, want ten minutes on", redeemed.ExpiresAt)
	}
}

// TestPutAuthCodeStoresTheCallerInstants: a caller that stamped its own record must not
// have the expiry re-derived from this store's clock, or the two disagree by the latency
// of the call.
func TestPutAuthCodeStoresTheCallerInstants(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	issuedAt := clock.Now().Add(-90 * time.Second)
	expiresAt := issuedAt.Add(5 * time.Minute)
	code := store.NewSecret("absolute-window-code")
	if err := opened.PutAuthCode(ctx, store.AuthCodeDraft{
		Code:          code,
		PrincipalID:   principal.ID,
		ClientID:      client.ID,
		RedirectURI:   testRedirectURI,
		Audience:      testAudience,
		Scopes:        []string{testScope},
		CodeChallenge: testChallenge,
		IssuedAt:      issuedAt,
		ExpiresAt:     expiresAt,
	}); err != nil {
		t.Fatalf("PutAuthCode: %v", err)
	}

	redeemed, err := opened.ConsumeAuthCode(ctx, code)
	if err != nil {
		t.Fatalf("ConsumeAuthCode: %v", err)
	}
	if !redeemed.IssuedAt.Equal(issuedAt.UTC()) || !redeemed.ExpiresAt.Equal(expiresAt.UTC()) {
		t.Fatalf("stored window %s..%s, want the caller's %s..%s",
			redeemed.IssuedAt, redeemed.ExpiresAt, issuedAt.UTC(), expiresAt.UTC())
	}
}

// TestConsumeAuthCodeReportsExpiryWithoutBecomingAnOracle: the expired case reports both
// sentinels, so a caller whose contract names an expired code can tell, and a caller that
// must not distinguish expiry from an unknown code keeps matching what it already
// matched.
func TestConsumeAuthCodeReportsExpiryWithoutBecomingAnOracle(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)
	code := seedCode(t, opened, principal.ID, client.ID, testCode)

	clock.advance(11 * time.Minute)
	_, err := opened.ConsumeAuthCode(ctx, code)
	if !errors.Is(err, store.ErrCodeExpired) {
		t.Errorf("an expired code: err = %v, want ErrCodeExpired", err)
	}
	if !errors.Is(err, store.ErrCodeNotFound) {
		t.Errorf("an expired code: err = %v, want ErrCodeNotFound as well", err)
	}

	// An unknown code reports only the not-found sentinel, so the two are not the same
	// answer wearing one error.
	_, err = opened.ConsumeAuthCode(ctx, store.NewSecret("never-issued-code"))
	if !errors.Is(err, store.ErrCodeNotFound) || errors.Is(err, store.ErrCodeExpired) {
		t.Errorf("an unknown code: err = %v, want ErrCodeNotFound alone", err)
	}
}
