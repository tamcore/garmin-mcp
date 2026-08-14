package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// Revocation that respects the consent key, and revocation of everything one principal
// holds.

// seedKeyedFamily grants a consent for key and issues one family for its resource.
func seedKeyedFamily(t *testing.T, s *store.SQLiteStore, clock *fakeClock,
	key store.ConsentKey, label string,
) store.Secret {
	t.Helper()
	ctx := context.Background()
	if err := s.GrantConsentFor(ctx, key, []string{testScope}); err != nil {
		t.Fatalf("GrantConsentFor %s: %v", label, err)
	}
	access := store.NewSecret("access-" + label)
	if _, err := s.IssueTokenFamilyRecord(ctx, store.TokenFamilyGrant{
		FamilyID:         "family-" + label,
		PrincipalID:      key.PrincipalID,
		ClientID:         key.ClientID,
		Scopes:           []string{testScope},
		Resource:         key.Resource,
		AccessToken:      access,
		RefreshToken:     store.NewSecret("refresh-" + label),
		IssuedAt:         clock.Now(),
		AccessExpiresAt:  clock.Now().Add(10 * time.Minute),
		RefreshExpiresAt: clock.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("IssueTokenFamilyRecord %s: %v", label, err)
	}
	return access
}

// TestRevokeConsentForLeavesTheOtherKeysAlone is the over-revocation the narrow key
// caused: two grants of one client, differing only in resource, must revoke separately.
func TestRevokeConsentForLeavesTheOtherKeysAlone(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	first := store.ConsentKey{PrincipalID: principal.ID, ClientID: client.ID,
		RedirectURI: testRedirectURI, Resource: testAudience}
	second := store.ConsentKey{PrincipalID: principal.ID, ClientID: client.ID,
		RedirectURI: testRedirectURI, Resource: otherResource}
	firstAccess := seedKeyedFamily(t, opened, clock, first, "one")
	secondAccess := seedKeyedFamily(t, opened, clock, second, "two")

	result, err := opened.RevokeConsentFor(ctx, first)
	if err != nil {
		t.Fatalf("RevokeConsentFor: %v", err)
	}
	if result.FamiliesRevoked != 1 || result.TokensRevoked != 2 {
		t.Fatalf("result = %+v, want exactly one family and its two tokens", result)
	}

	if _, err := opened.LookupAccessToken(ctx, firstAccess); !errors.Is(
		err, store.ErrTokenRevoked) {
		t.Errorf("the revoked grant's token still resolves: err = %v", err)
	}
	if _, err := opened.LookupAccessToken(ctx, secondAccess); err != nil {
		t.Errorf("the untouched grant's token was revoked too: %v", err)
	}
	if _, err := opened.ConsentFor(ctx, second); err != nil {
		t.Errorf("the untouched consent was revoked too: %v", err)
	}
	if _, err := opened.ConsentFor(ctx, first); !errors.Is(err, store.ErrConsentNotFound) {
		t.Errorf("the revoked consent is still readable: err = %v", err)
	}
}

// TestRevokeConsentForIsIdempotent: a second call, and an unknown key, report zeros and
// no error.
func TestRevokeConsentForIsIdempotent(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)
	key := store.ConsentKey{PrincipalID: principal.ID, ClientID: client.ID,
		RedirectURI: testRedirectURI, Resource: testAudience}
	seedKeyedFamily(t, opened, clock, key, "one")

	if _, err := opened.RevokeConsentFor(ctx, key); err != nil {
		t.Fatalf("first RevokeConsentFor: %v", err)
	}
	second, err := opened.RevokeConsentFor(ctx, key)
	if err != nil {
		t.Fatalf("second RevokeConsentFor: %v", err)
	}
	if second != (store.RevocationResult{}) {
		t.Errorf("second RevokeConsentFor = %+v, want a zero result", second)
	}

	unknown := key
	unknown.Resource = "https://never-granted.example/"
	if _, err := opened.RevokeConsentFor(ctx, unknown); err != nil {
		t.Errorf("RevokeConsentFor an unknown key: %v", err)
	}
	if _, err := opened.RevokeConsentFor(ctx, store.ConsentKey{ClientID: client.ID}); !errors.Is(
		err, store.ErrInvalidArgument) {
		t.Errorf("RevokeConsentFor with no principal: err = %v, want ErrInvalidArgument", err)
	}
}

// TestRevokePrincipalTokensRevokesTokensAndConsents, and leaves the Garmin linkage
// alone: revoking a principal's MCP access is not unlinking its Garmin account.
func TestRevokePrincipalTokensRevokesTokensAndConsents(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	if _, err := opened.Save(ctx, grant.principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	result, err := opened.RevokePrincipalTokens(ctx, grant.principal.ID)
	if err != nil {
		t.Fatalf("RevokePrincipalTokens: %v", err)
	}
	if result.FamiliesRevoked != 1 || result.TokensRevoked != 2 {
		t.Fatalf("result = %+v, want one family and its two tokens", result)
	}
	if result.GarminTokensDeleted != 0 {
		t.Fatalf("result deleted %d garmin token records, want none", result.GarminTokensDeleted)
	}

	if _, err := opened.LookupAccessToken(ctx, grant.access); !errors.Is(
		err, store.ErrTokenRevoked) {
		t.Errorf("the access token survived: err = %v", err)
	}
	if _, err := opened.Consent(ctx, grant.principal.ID, grant.client.ID); !errors.Is(
		err, store.ErrConsentNotFound) {
		t.Errorf("the consent survived: err = %v", err)
	}
	if _, _, err := opened.Load(ctx, grant.principal.ID); err != nil {
		t.Errorf("the garmin token record was removed: %v", err)
	}
}

// TestRevokePrincipalTokensIsIdempotentAndForgivesAnUnknownPrincipal is the difference
// from UnlinkGarminAccount, which reports an error for a principal it does not know.
func TestRevokePrincipalTokensIsIdempotentAndForgivesAnUnknownPrincipal(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	if _, err := opened.RevokePrincipalTokens(ctx, grant.principal.ID); err != nil {
		t.Fatalf("first RevokePrincipalTokens: %v", err)
	}
	second, err := opened.RevokePrincipalTokens(ctx, grant.principal.ID)
	if err != nil {
		t.Fatalf("second RevokePrincipalTokens: %v", err)
	}
	if second != (store.RevocationResult{}) {
		t.Errorf("second RevokePrincipalTokens = %+v, want a zero result", second)
	}

	unknown, err := opened.RevokePrincipalTokens(ctx, testUnknownID)
	if err != nil {
		t.Fatalf("RevokePrincipalTokens for an unknown principal: %v", err)
	}
	if unknown != (store.RevocationResult{}) {
		t.Errorf("an unknown principal = %+v, want a zero result", unknown)
	}
	if _, err := opened.RevokePrincipalTokens(ctx, ""); !errors.Is(
		err, store.ErrInvalidArgument) {
		t.Errorf("RevokePrincipalTokens with no principal: err = %v, want ErrInvalidArgument", err)
	}
}

// TestRevokePrincipalTokensLeavesOtherPrincipalsAlone keeps the isolation boundary.
func TestRevokePrincipalTokensLeavesOtherPrincipalsAlone(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	bystander, err := opened.CreatePrincipal(ctx, "bystander@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	key := store.ConsentKey{PrincipalID: bystander.ID, ClientID: grant.client.ID,
		RedirectURI: testRedirectURI, Resource: testAudience}
	bystanderAccess := seedKeyedFamily(t, opened, clock, key, "bystander")

	if _, err := opened.RevokePrincipalTokens(ctx, grant.principal.ID); err != nil {
		t.Fatalf("RevokePrincipalTokens: %v", err)
	}
	if _, err := opened.LookupAccessToken(ctx, bystanderAccess); err != nil {
		t.Fatalf("another principal's token was revoked: %v", err)
	}
}
