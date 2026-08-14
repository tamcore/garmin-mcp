package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// issueFor grants consent and issues one family for a principal and client, and returns
// the access token it minted.
func issueFor(t *testing.T, s *store.SQLiteStore, principalID, clientID, label string) store.Secret {
	t.Helper()
	ctx := context.Background()
	if err := s.GrantConsent(ctx, principalID, clientID, []string{testScope}); err != nil {
		t.Fatalf("GrantConsent for %s: %v", label, err)
	}
	access := store.NewSecret(label + "-access")
	_, err := s.IssueTokenFamily(ctx, store.TokenGrant{
		PrincipalID:     principalID,
		ClientID:        clientID,
		Scopes:          []string{testScope},
		Audience:        testAudience,
		AccessToken:     access,
		RefreshToken:    store.NewSecret(label + "-refresh"),
		AccessLifetime:  10 * time.Minute,
		RefreshLifetime: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("IssueTokenFamily for %s: %v", label, err)
	}
	return access
}

// TestRevokeConsentRevokesThatClientsFamiliesOnly is the first cascade. Two clients hold
// tokens for one principal; revoking one consent must reach that client's family and
// nothing else.
func TestRevokeConsentRevokesThatClientsFamiliesOnly(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	other, err := opened.RegisterClient(ctx, store.ClientRegistration{
		Name:         "Other Client",
		RedirectURIs: []string{"https://other.example/callback"},
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	otherAccess := issueFor(t, opened, grant.principal.ID, other.ID, "other-client")

	// Pending authorization state for the revoked client, which must be deleted, and
	// for the other client, which must survive.
	seedCode(t, opened, grant.principal.ID, grant.client.ID, "code-of-revoked-client")
	seedCode(t, opened, grant.principal.ID, other.ID, "code-of-other-client")
	seedTransaction(t, opened, grant.client.ID)
	if err := opened.AttachPrincipal(ctx, store.NewSecret(testHandle), grant.principal.ID); err != nil {
		t.Fatalf("AttachPrincipal: %v", err)
	}

	result, err := opened.RevokeConsent(ctx, grant.principal.ID, grant.client.ID)
	if err != nil {
		t.Fatalf("RevokeConsent: %v", err)
	}
	if result.FamiliesRevoked != 1 || result.TokensRevoked != 2 {
		t.Errorf("result = %+v, want 1 family and 2 tokens revoked", result)
	}
	if result.CodesDeleted != 1 || result.TransactionsDeleted != 1 {
		t.Errorf("result = %+v, want 1 code and 1 transaction deleted", result)
	}

	if _, err := opened.LookupAccessToken(ctx, grant.access); !errors.Is(err, store.ErrTokenRevoked) {
		t.Errorf("the revoked client's access token: err = %v, want ErrTokenRevoked", err)
	}
	_, err = opened.Consent(ctx, grant.principal.ID, grant.client.ID)
	if !errors.Is(err, store.ErrConsentNotFound) {
		t.Errorf("the revoked consent: err = %v, want ErrConsentNotFound", err)
	}

	// The other client is untouched: its token still works, its consent still stands,
	// and its pending code is still redeemable.
	if _, err := opened.LookupAccessToken(ctx, otherAccess); err != nil {
		t.Errorf("the other client's access token was revoked too: %v", err)
	}
	if _, err := opened.Consent(ctx, grant.principal.ID, other.ID); err != nil {
		t.Errorf("the other client's consent was revoked too: %v", err)
	}
	if _, err := opened.ConsumeAuthCode(ctx, store.NewSecret("code-of-other-client")); err != nil {
		t.Errorf("the other client's authorization code was deleted too: %v", err)
	}
	_, err = opened.ConsumeAuthCode(ctx, store.NewSecret("code-of-revoked-client"))
	if !errors.Is(err, store.ErrCodeNotFound) {
		t.Errorf("the revoked client's code survived: err = %v, want ErrCodeNotFound", err)
	}
}

// TestRevokeConsentIsIdempotent: the second call must report zeros and no error.
func TestRevokeConsentIsIdempotent(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	if _, err := opened.RevokeConsent(ctx, grant.principal.ID, grant.client.ID); err != nil {
		t.Fatalf("first RevokeConsent: %v", err)
	}
	second, err := opened.RevokeConsent(ctx, grant.principal.ID, grant.client.ID)
	if err != nil {
		t.Fatalf("second RevokeConsent: %v", err)
	}
	if second != (store.RevocationResult{}) {
		t.Errorf("second RevokeConsent = %+v, want a zero result", second)
	}

	// Revoking a pair that never had a consent is also not an error: a revocation that
	// finds nothing to do has still reached the state the caller asked for.
	_, err = opened.RevokeConsent(ctx, grant.principal.ID, testUnknownID)
	if err != nil {
		t.Errorf("RevokeConsent for an unknown client: %v", err)
	}
	if _, err := opened.RevokeConsent(ctx, "", grant.client.ID); !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("empty principal id: err = %v, want ErrInvalidArgument", err)
	}
}

// TestRevokeConsentDoesNotTouchTheGarminTokens draws the boundary: withdrawing one
// client's access is not unlinking the Garmin account.
func TestRevokeConsentDoesNotTouchTheGarminTokens(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	if _, err := opened.Save(ctx, grant.principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := opened.RevokeConsent(ctx, grant.principal.ID, grant.client.ID); err != nil {
		t.Fatalf("RevokeConsent: %v", err)
	}

	if _, _, err := opened.Load(ctx, grant.principal.ID); err != nil {
		t.Errorf("the Garmin token record was removed by a consent revocation: %v", err)
	}
}

// TestUnlinkGarminAccountCascadesEverything is the second cascade: every family for the
// principal, the encrypted Garmin tokens, every pending transaction and code, and the
// linkage columns.
func TestUnlinkGarminAccountCascadesEverything(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	err := opened.LinkGarminAccount(ctx, grant.principal.ID, store.GarminIdentity{
		AccountID:   store.NewSecret(testGarminAccount),
		DisplayName: testDisplayName,
	})
	if err != nil {
		t.Fatalf("LinkGarminAccount: %v", err)
	}
	if _, err := opened.Save(ctx, grant.principal.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	seedTransaction(t, opened, grant.client.ID)
	if err := opened.AttachPrincipal(ctx, store.NewSecret(testHandle), grant.principal.ID); err != nil {
		t.Fatalf("AttachPrincipal: %v", err)
	}
	seedCode(t, opened, grant.principal.ID, grant.client.ID, testCode)

	result, err := opened.UnlinkGarminAccount(ctx, grant.principal.ID)
	if err != nil {
		t.Fatalf("UnlinkGarminAccount: %v", err)
	}
	if result.FamiliesRevoked != 1 || result.TokensRevoked != 2 {
		t.Errorf("result = %+v, want 1 family and 2 tokens revoked", result)
	}
	if result.GarminTokensDeleted != 1 {
		t.Errorf("GarminTokensDeleted = %d, want 1", result.GarminTokensDeleted)
	}
	if result.TransactionsDeleted != 1 || result.CodesDeleted != 1 {
		t.Errorf("result = %+v, want 1 transaction and 1 code deleted", result)
	}
	assertFullyUnlinked(t, opened, grant)
}

// assertFullyUnlinked checks every consequence of an unlink through the public API.
func assertFullyUnlinked(t *testing.T, opened *store.SQLiteStore, grant seededGrant) {
	t.Helper()
	ctx := context.Background()

	if _, err := opened.LookupAccessToken(ctx, grant.access); !errors.Is(err, store.ErrTokenRevoked) {
		t.Errorf("access token: err = %v, want ErrTokenRevoked", err)
	}
	if _, _, err := opened.Load(ctx, grant.principal.ID); !errors.Is(err, store.ErrNoTokens) {
		t.Errorf("garmin token record: err = %v, want ErrNoTokens", err)
	}
	if _, err := opened.GarminIdentity(ctx, grant.principal.ID); !errors.Is(err, store.ErrNoTokens) {
		t.Errorf("garmin identity: err = %v, want ErrNoTokens", err)
	}
	_, err := opened.PrincipalByGarminAccount(ctx, store.NewSecret(testGarminAccount))
	if !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("lookup by garmin account: err = %v, want ErrPrincipalNotFound", err)
	}
	_, err = opened.AuthTransaction(ctx, store.NewSecret(testHandle))
	if !errors.Is(err, store.ErrTransactionNotFound) {
		t.Errorf("pending transaction: err = %v, want ErrTransactionNotFound", err)
	}
	_, err = opened.ConsumeAuthCode(ctx, store.NewSecret(testCode))
	if !errors.Is(err, store.ErrCodeNotFound) {
		t.Errorf("pending code: err = %v, want ErrCodeNotFound", err)
	}

	// The principal itself survives: unlinking is not deletion.
	principal, err := opened.PrincipalByID(ctx, grant.principal.ID)
	if err != nil {
		t.Fatalf("PrincipalByID after unlink: %v", err)
	}
	if principal.GarminLinked {
		t.Error("GarminLinked = true after an unlink")
	}
}

func TestUnlinkGarminAccountIsIdempotentAndChecksThePrincipal(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	if _, err := opened.UnlinkGarminAccount(ctx, grant.principal.ID); err != nil {
		t.Fatalf("first UnlinkGarminAccount: %v", err)
	}
	second, err := opened.UnlinkGarminAccount(ctx, grant.principal.ID)
	if err != nil {
		t.Fatalf("second UnlinkGarminAccount: %v", err)
	}
	if second != (store.RevocationResult{}) {
		t.Errorf("second unlink = %+v, want a zero result", second)
	}

	_, err = opened.UnlinkGarminAccount(ctx, testUnknownID)
	if !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("unknown principal: err = %v, want ErrPrincipalNotFound", err)
	}
	if _, err := opened.UnlinkGarminAccount(ctx, ""); !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("empty principal id: err = %v, want ErrInvalidArgument", err)
	}
}

// TestUnlinkLeavesOtherPrincipalsAlone: the cascade is scoped by the isolation key.
func TestUnlinkLeavesOtherPrincipalsAlone(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	bystander, err := opened.CreatePrincipal(ctx, "bystander@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	bystanderAccess := issueFor(t, opened, bystander.ID, grant.client.ID, "bystander")
	if _, err := opened.Save(ctx, bystander.ID, newSQLTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := opened.UnlinkGarminAccount(ctx, grant.principal.ID); err != nil {
		t.Fatalf("UnlinkGarminAccount: %v", err)
	}

	if _, err := opened.LookupAccessToken(ctx, bystanderAccess); err != nil {
		t.Errorf("the bystander's access token was revoked: %v", err)
	}
	if _, _, err := opened.Load(ctx, bystander.ID); err != nil {
		t.Errorf("the bystander's garmin tokens were deleted: %v", err)
	}
}
