package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// rotation builds a RefreshRotation with the standard lifetimes.
func rotation(presented store.Secret, nextAccess, nextRefresh string) store.RefreshRotation {
	return store.RefreshRotation{
		Presented:        presented,
		NextAccessToken:  store.NewSecret(nextAccess),
		NextRefreshToken: store.NewSecret(nextRefresh),
		AccessLifetime:   10 * time.Minute,
		RefreshLifetime:  24 * time.Hour,
	}
}

func TestIssueTokenFamilyAndLookupAccessToken(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	access, err := opened.LookupAccessToken(ctx, grant.access)
	if err != nil {
		t.Fatalf("LookupAccessToken: %v", err)
	}
	if access.PrincipalID != grant.principal.ID {
		t.Errorf("PrincipalID = %q, want %q", access.PrincipalID, grant.principal.ID)
	}
	if access.ClientID != grant.client.ID {
		t.Errorf("ClientID = %q, want %q", access.ClientID, grant.client.ID)
	}
	if access.FamilyID != grant.familyID {
		t.Errorf("FamilyID = %q, want %q", access.FamilyID, grant.familyID)
	}
	if access.Audience != testAudience {
		t.Errorf("Audience = %q, want %q", access.Audience, testAudience)
	}
	if len(access.Scopes) != 1 || access.Scopes[0] != testScope {
		t.Errorf("Scopes = %v, want [%s]", access.Scopes, testScope)
	}
}

// TestAccessAndRefreshTokensAreNotInterchangeable is the purpose-derivation property: a
// refresh token presented as an access token must not match, and vice versa, even
// though both rows live in the same table.
func TestAccessAndRefreshTokensAreNotInterchangeable(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	_, err := opened.LookupAccessToken(ctx, grant.refresh)
	if !errors.Is(err, store.ErrTokenNotFound) {
		t.Errorf("refresh token presented as an access token: err = %v, want ErrTokenNotFound", err)
	}

	_, err = opened.RotateRefreshToken(ctx, rotation(grant.access, "next-access", "next-refresh"))
	if !errors.Is(err, store.ErrTokenNotFound) {
		t.Errorf("access token presented as a refresh token: err = %v, want ErrTokenNotFound", err)
	}
}

func TestLookupAccessTokenRefusesUnknownAndAbsentMaterial(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	seedGrant(t, opened)

	_, err := opened.LookupAccessToken(ctx, store.NewSecret("never-issued"))
	if !errors.Is(err, store.ErrTokenNotFound) {
		t.Errorf("unknown token: err = %v, want ErrTokenNotFound", err)
	}
	_, err = opened.LookupAccessToken(ctx, store.Secret{})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("zero secret: err = %v, want ErrInvalidArgument", err)
	}
}

// TestExpiredAccessTokenIsRefusedBeforeCleanupRuns is the on-access expiry check. No
// Cleanup call happens anywhere in this test.
func TestExpiredAccessTokenIsRefusedBeforeCleanupRuns(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	if _, err := opened.LookupAccessToken(ctx, grant.access); err != nil {
		t.Fatalf("the token must be valid before time moves: %v", err)
	}

	// The seeded access lifetime is ten minutes.
	clock.advance(11 * time.Minute)

	_, err := opened.LookupAccessToken(ctx, grant.access)
	if !errors.Is(err, store.ErrTokenExpired) {
		t.Fatalf("expired token: err = %v, want ErrTokenExpired", err)
	}
}

func TestIssueTokenFamilyRequiresAnActiveConsent(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	draft := store.TokenGrant{
		PrincipalID:     principal.ID,
		ClientID:        client.ID,
		Scopes:          []string{testScope},
		Audience:        testAudience,
		AccessToken:     store.NewSecret("access-without-consent"),
		RefreshToken:    store.NewSecret("refresh-without-consent"),
		AccessLifetime:  10 * time.Minute,
		RefreshLifetime: time.Hour,
	}
	if _, err := opened.IssueTokenFamily(ctx, draft); !errors.Is(err, store.ErrConsentNotFound) {
		t.Fatalf("IssueTokenFamily with no consent: err = %v, want ErrConsentNotFound", err)
	}

	if err := opened.GrantConsent(ctx, principal.ID, client.ID, []string{testScope}); err != nil {
		t.Fatalf("GrantConsent: %v", err)
	}
	if _, err := opened.IssueTokenFamily(ctx, draft); err != nil {
		t.Fatalf("IssueTokenFamily after consent: %v", err)
	}
}

func TestIssueTokenFamilyRefusesBadGrants(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	base := func() store.TokenGrant {
		return store.TokenGrant{
			PrincipalID:     grant.principal.ID,
			ClientID:        grant.client.ID,
			Scopes:          []string{testScope},
			Audience:        testAudience,
			AccessToken:     store.NewSecret("another-access"),
			RefreshToken:    store.NewSecret("another-refresh"),
			AccessLifetime:  time.Minute,
			RefreshLifetime: time.Hour,
		}
	}

	cases := map[string]func(g *store.TokenGrant){
		"no audience":           func(g *store.TokenGrant) { g.Audience = "" },
		caseNoScopes:            func(g *store.TokenGrant) { g.Scopes = nil },
		"no access material":    func(g *store.TokenGrant) { g.AccessToken = store.Secret{} },
		"no refresh material":   func(g *store.TokenGrant) { g.RefreshToken = store.Secret{} },
		caseZeroLifetime:        func(g *store.TokenGrant) { g.AccessLifetime = 0 },
		"a negative lifetime":   func(g *store.TokenGrant) { g.RefreshLifetime = -time.Second },
		"an unbounded lifetime": func(g *store.TokenGrant) { g.RefreshLifetime = 365 * 24 * time.Hour },
		"an empty principal id": func(g *store.TokenGrant) { g.PrincipalID = "" },
		"an empty client id":    func(g *store.TokenGrant) { g.ClientID = "" },
	}

	for name, mutate := range cases {
		draft := base()
		mutate(&draft)
		if _, err := opened.IssueTokenFamily(ctx, draft); !errors.Is(err, store.ErrInvalidArgument) {
			t.Errorf("IssueTokenFamily with %s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
}

// TestReusingTokenMaterialIsRefused keeps two grants from sharing a lookup value, which
// would make one token resolve to the wrong family.
func TestReusingTokenMaterialIsRefused(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	_, err := opened.IssueTokenFamily(ctx, store.TokenGrant{
		PrincipalID:     grant.principal.ID,
		ClientID:        grant.client.ID,
		Scopes:          []string{testScope},
		Audience:        testAudience,
		AccessToken:     grant.access,
		RefreshToken:    store.NewSecret("a-fresh-refresh-token"),
		AccessLifetime:  time.Minute,
		RefreshLifetime: time.Hour,
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("reusing access material: err = %v, want ErrInvalidArgument", err)
	}
}

func TestRotateRefreshTokenIssuesTheNextGeneration(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	next := rotation(grant.refresh, "second-generation-access", "second-generation-refresh")
	issued, err := opened.RotateRefreshToken(ctx, next)
	if err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	if issued.FamilyID != grant.familyID {
		t.Errorf("the rotation left the family: %q, want %q", issued.FamilyID, grant.familyID)
	}
	if issued.PrincipalID != grant.principal.ID {
		t.Errorf("PrincipalID = %q, want %q", issued.PrincipalID, grant.principal.ID)
	}

	// The new access token works, and the previous one is untouched by the rotation: an
	// access token is invalidated by expiry or revocation, not by a refresh.
	if _, err := opened.LookupAccessToken(ctx, next.NextAccessToken); err != nil {
		t.Fatalf("the new access token does not resolve: %v", err)
	}
	if _, err := opened.LookupAccessToken(ctx, grant.access); err != nil {
		t.Fatalf("the previous access token was invalidated by the rotation: %v", err)
	}

	// The new refresh token rotates again.
	_, err = opened.RotateRefreshToken(ctx,
		rotation(next.NextRefreshToken, "third-access", "third-refresh"))
	if err != nil {
		t.Fatalf("second RotateRefreshToken: %v", err)
	}
}

// TestReplayingARotatedRefreshTokenRevokesTheWholeFamily is the reuse-detection
// requirement. The replay must revoke everything descended from the grant, including the
// access token the legitimate client is holding, because in the leak case both parties
// hold material from that family.
func TestReplayingARotatedRefreshTokenRevokesTheWholeFamily(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	next := rotation(grant.refresh, "generation-two-access", "generation-two-refresh")
	if _, err := opened.RotateRefreshToken(ctx, next); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	// The replay of the already rotated token.
	_, err := opened.RotateRefreshToken(ctx,
		rotation(grant.refresh, "attacker-access", "attacker-refresh"))
	if !errors.Is(err, store.ErrRefreshTokenReuse) {
		t.Fatalf("replay: err = %v, want ErrRefreshTokenReuse", err)
	}

	for label, token := range map[string]store.Secret{
		"the first generation access token":  grant.access,
		"the second generation access token": next.NextAccessToken,
	} {
		if _, err := opened.LookupAccessToken(ctx, token); !errors.Is(err, store.ErrTokenRevoked) {
			t.Errorf("%s: err = %v, want ErrTokenRevoked", label, err)
		}
	}

	// The live refresh token is dead too, so the chain cannot be continued.
	_, err = opened.RotateRefreshToken(ctx,
		rotation(next.NextRefreshToken, "post-revocation-access", "post-revocation-refresh"))
	if !errors.Is(err, store.ErrTokenRevoked) {
		t.Errorf("rotation after family revocation: err = %v, want ErrTokenRevoked", err)
	}

	// The attacker's replacement material was never stored: the reuse path revokes and
	// refuses, it does not issue.
	_, err = opened.LookupAccessToken(ctx, store.NewSecret("attacker-access"))
	if !errors.Is(err, store.ErrTokenNotFound) {
		t.Errorf("the replay issued a token: err = %v, want ErrTokenNotFound", err)
	}
}

func TestRotateRefreshTokenRefusesAnExpiredToken(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	// The seeded refresh lifetime is 24 hours.
	clock.advance(25 * time.Hour)

	_, err := opened.RotateRefreshToken(ctx,
		rotation(grant.refresh, "too-late-access", "too-late-refresh"))
	if !errors.Is(err, store.ErrTokenExpired) {
		t.Fatalf("expired refresh token: err = %v, want ErrTokenExpired", err)
	}
}

func TestRotateRefreshTokenRefusesUnknownMaterial(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	seedGrant(t, opened)

	_, err := opened.RotateRefreshToken(ctx,
		rotation(store.NewSecret("never-issued"), "a", "b"))
	if !errors.Is(err, store.ErrTokenNotFound) {
		t.Errorf("unknown refresh token: err = %v, want ErrTokenNotFound", err)
	}
	_, err = opened.RotateRefreshToken(ctx, rotation(store.Secret{}, "a", "b"))
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("zero refresh token: err = %v, want ErrInvalidArgument", err)
	}
}

func TestRevokeTokenFamilyIsIdempotent(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	first, err := opened.RevokeTokenFamily(ctx, grant.familyID, "operator_revoked")
	if err != nil {
		t.Fatalf("RevokeTokenFamily: %v", err)
	}
	if first.FamiliesRevoked != 1 || first.TokensRevoked != 2 {
		t.Errorf("first revocation = %+v, want 1 family and 2 tokens", first)
	}

	second, err := opened.RevokeTokenFamily(ctx, grant.familyID, "operator_revoked")
	if err != nil {
		t.Fatalf("second RevokeTokenFamily: %v", err)
	}
	if second.FamiliesRevoked != 0 || second.TokensRevoked != 0 {
		t.Errorf("second revocation = %+v, want zeros: it must be a no-op", second)
	}

	if _, err := opened.LookupAccessToken(ctx, grant.access); !errors.Is(err, store.ErrTokenRevoked) {
		t.Errorf("access token after revocation: err = %v, want ErrTokenRevoked", err)
	}
}

func TestRevokeTokenFamilyRefusesBadInput(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	_, err := opened.RevokeTokenFamily(ctx,
		testUnknownID, "operator_revoked")
	if !errors.Is(err, store.ErrTokenNotFound) {
		t.Errorf("unknown family: err = %v, want ErrTokenNotFound", err)
	}

	for _, reason := range []string{
		"", "Has Capitals", "has space", "trailing_", "_leading", "with-dash",
	} {
		_, err := opened.RevokeTokenFamily(ctx, grant.familyID, reason)
		if !errors.Is(err, store.ErrInvalidArgument) {
			t.Errorf("reason %q: err = %v, want ErrInvalidArgument", reason, err)
		}
	}
}
