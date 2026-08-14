package oauthstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/oauthserver"
)

func TestSaveAndConsumeCodeKeepsEveryBinding(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	written := f.code("redeem")
	if err := f.adapter.SaveCode(ctx, written); err != nil {
		t.Fatalf("SaveCode: %v", err)
	}

	redeemed, err := f.adapter.ConsumeCode(ctx, written.Lookup)
	if err != nil {
		t.Fatalf("ConsumeCode: %v", err)
	}
	if redeemed.Lookup != written.Lookup {
		t.Error("the lookup was not re-attached to the record")
	}
	if redeemed.ClientID != written.ClientID {
		t.Errorf("client id is %q, want %q", redeemed.ClientID, written.ClientID)
	}
	if !redeemed.RedirectURI.Equal(written.RedirectURI) {
		t.Errorf("redirect uri is %q, want %q", redeemed.RedirectURI, written.RedirectURI)
	}
	if !redeemed.Resource.Equal(written.Resource) {
		t.Errorf("resource is %q, want %q", redeemed.Resource, written.Resource)
	}
	if redeemed.Challenge.Value() != written.Challenge.Value() {
		t.Error("the PKCE challenge did not round trip")
	}
	if redeemed.Principal.ID() != written.Principal.ID() {
		t.Errorf("principal is %q, want %q", redeemed.Principal.ID(), written.Principal.ID())
	}
	if !redeemed.ExpiresAt.Equal(written.ExpiresAt) {
		t.Errorf("expiry is %s, want %s", redeemed.ExpiresAt, written.ExpiresAt)
	}
}

func TestConsumeCodeIsSingleUse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	written := f.code("replay")
	if err := f.adapter.SaveCode(ctx, written); err != nil {
		t.Fatalf("SaveCode: %v", err)
	}
	if _, err := f.adapter.ConsumeCode(ctx, written.Lookup); err != nil {
		t.Fatalf("ConsumeCode: %v", err)
	}

	_, err := f.adapter.ConsumeCode(ctx, written.Lookup)
	if !errors.Is(err, oauthserver.ErrCodeAlreadyUsed) {
		t.Fatalf("error is %v, want ErrCodeAlreadyUsed", err)
	}
}

func TestConsumeCodeReportsUnknownAndExpired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.adapter.ConsumeCode(ctx, lookupOf("never-issued")); !errors.Is(
		err, oauthserver.ErrCodeNotFound) {
		t.Fatalf("error is %v, want ErrCodeNotFound", err)
	}

	written := f.code("stale")
	if err := f.adapter.SaveCode(ctx, written); err != nil {
		t.Fatalf("SaveCode: %v", err)
	}
	f.clock.advance(time.Hour)

	_, err := f.adapter.ConsumeCode(ctx, written.Lookup)
	if !errors.Is(err, oauthserver.ErrCodeExpired) {
		t.Fatalf("error is %v, want ErrCodeExpired", err)
	}
	if !errors.Is(err, oauthserver.ErrCodeNotFound) {
		t.Fatalf("error is %v, want ErrCodeNotFound alongside ErrCodeExpired", err)
	}
}

func TestSaveTokenPairHonoursTheCallersFamily(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access, refresh := f.seedFamily(t, "family-honoured", "honoured")

	storedAccess, err := f.adapter.AccessToken(ctx, access.Lookup)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if storedAccess.Family != access.Family {
		t.Errorf("family is %q, want %q", storedAccess.Family, access.Family)
	}
	if storedAccess.Lookup != access.Lookup {
		t.Error("the lookup was not re-attached to the access record")
	}
	if !storedAccess.Scopes.Equal(access.Scopes) {
		t.Errorf("scopes are %q, want %q", storedAccess.Scopes, access.Scopes)
	}
	if !storedAccess.ExpiresAt.Equal(access.ExpiresAt) {
		t.Errorf("expiry is %s, want %s", storedAccess.ExpiresAt, access.ExpiresAt)
	}

	storedRefresh, err := f.adapter.RefreshToken(ctx, refresh.Lookup)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if storedRefresh.Family != refresh.Family {
		t.Errorf("family is %q, want %q", storedRefresh.Family, refresh.Family)
	}
	if storedRefresh.Generation != 0 {
		t.Errorf("generation is %d, want 0 for the first pair", storedRefresh.Generation)
	}
}

func TestSaveTokenPairNeedsAConsent(t *testing.T) {
	f := newFixture(t)

	access, refresh := f.pair("family-unconsented", "unconsented", 0)
	err := f.adapter.SaveTokenPair(context.Background(), access, refresh)
	if !errors.Is(err, oauthserver.ErrConsentNotFound) {
		t.Fatalf("error is %v, want ErrConsentNotFound", err)
	}
}

func TestTokenReadsReportUnknownMaterial(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.adapter.AccessToken(ctx, lookupOf("unknown-access")); !errors.Is(
		err, oauthserver.ErrTokenNotFound) {
		t.Fatalf("error is %v, want ErrTokenNotFound", err)
	}
	if _, err := f.adapter.RefreshToken(ctx, lookupOf("unknown-refresh")); !errors.Is(
		err, oauthserver.ErrTokenNotFound) {
		t.Fatalf("error is %v, want ErrTokenNotFound", err)
	}
}

// An expired access token still comes back as a record: the interface's expiry
// judgement belongs to the caller, which has IsExpired for it.
func TestAccessTokenReturnsAnExpiredRecord(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access, _ := f.seedFamily(t, "family-expired", "expired-token")
	f.clock.advance(time.Hour)

	read, err := f.adapter.AccessToken(ctx, access.Lookup)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if !read.IsExpired(f.clock.Now()) {
		t.Error("the record does not report itself expired")
	}
}

func TestRevokeFamilyIsIdempotentAndReportsAnUnknownFamily(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access, _ := f.seedFamily(t, "family-revoked", "revoked")

	if err := f.adapter.RevokeFamily(ctx, access.Family); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}
	if err := f.adapter.RevokeFamily(ctx, access.Family); err != nil {
		t.Fatalf("second RevokeFamily: %v", err)
	}
	if _, err := f.adapter.AccessToken(ctx, access.Lookup); !errors.Is(
		err, oauthserver.ErrTokenRevoked) {
		t.Fatalf("error is %v, want ErrTokenRevoked", err)
	}

	err := f.adapter.RevokeFamily(ctx, oauthserver.FamilyID("no-such-family"))
	if !errors.Is(err, oauthserver.ErrTokenNotFound) {
		t.Fatalf("error is %v, want ErrTokenNotFound", err)
	}
}

func TestRotateRefreshTokenIssuesTheNextGeneration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, presented := f.seedFamily(t, "family-rotate", "rotate")

	nextAccess, nextRefresh := f.pair(presented.Family, "rotate-2", 1)
	if err := f.adapter.RotateRefreshToken(ctx, presented.Lookup, nextAccess, nextRefresh); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	read, err := f.adapter.RefreshToken(ctx, nextRefresh.Lookup)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if read.Generation != 1 {
		t.Errorf("generation is %d, want 1", read.Generation)
	}
	if read.Family != presented.Family {
		t.Errorf("family is %q, want %q", read.Family, presented.Family)
	}
	if _, err := f.adapter.AccessToken(ctx, nextAccess.Lookup); err != nil {
		t.Fatalf("the replacement access token is not readable: %v", err)
	}
}

// The read deliberately does not judge reuse: a rotated-away refresh token comes
// back, so detection stays in RotateRefreshToken where the family dies in the same
// transaction.
func TestRefreshTokenReturnsARotatedRecord(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, presented := f.seedFamily(t, "family-rotated-read", "rotated-read")

	nextAccess, nextRefresh := f.pair(presented.Family, "rotated-read-2", 1)
	if err := f.adapter.RotateRefreshToken(ctx, presented.Lookup, nextAccess, nextRefresh); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	read, err := f.adapter.RefreshToken(ctx, presented.Lookup)
	if err != nil {
		t.Fatalf("RefreshToken on a rotated token: %v", err)
	}
	if read.Lookup != presented.Lookup {
		t.Error("the rotated record came back under another lookup")
	}
}

func TestRevokePrincipalRevokesEverythingAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access, _ := f.seedFamily(t, "family-principal", "principal")

	if err := f.adapter.RevokePrincipal(ctx, f.principal); err != nil {
		t.Fatalf("RevokePrincipal: %v", err)
	}
	if _, err := f.adapter.AccessToken(ctx, access.Lookup); !errors.Is(
		err, oauthserver.ErrTokenRevoked) {
		t.Fatalf("the token survived: %v", err)
	}
	if _, err := f.adapter.Consent(ctx, f.consentKey()); !errors.Is(
		err, oauthserver.ErrConsentNotFound) {
		t.Fatalf("the consent survived: %v", err)
	}
	if err := f.adapter.RevokePrincipal(ctx, f.principal); err != nil {
		t.Fatalf("second RevokePrincipal: %v", err)
	}
}
