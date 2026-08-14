package oauthstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/oauthserver"
)

const testState = "opaque-client-state"

func TestTransactionRoundTripsEveryField(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	state, err := oauthserver.ParseClientState(testState)
	if err != nil {
		t.Fatalf("ParseClientState: %v", err)
	}
	written := f.transaction("round-trip", state)
	if err := f.adapter.CreateTransaction(ctx, written); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	read, err := f.adapter.Transaction(ctx, written.Lookup)
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
	assertTransactionMatches(t, read, written)
	if read.Version != 0 {
		t.Errorf("version is %d, want 0 for a freshly created transaction", read.Version)
	}
	if read.Stage != oauthserver.StagePending {
		t.Errorf("stage is %v, want pending for a transaction with no principal", read.Stage)
	}
	if read.Principal.IsValid() {
		t.Error("a pending transaction reported a principal")
	}
}

func assertTransactionMatches(t *testing.T, read, written oauthserver.Transaction) {
	t.Helper()
	if read.Lookup != written.Lookup {
		t.Error("the lookup was not re-attached to the record")
	}
	if read.ClientID != written.ClientID {
		t.Errorf("client id is %q, want %q", read.ClientID, written.ClientID)
	}
	if !read.RedirectURI.Equal(written.RedirectURI) {
		t.Errorf("redirect uri is %q, want %q", read.RedirectURI, written.RedirectURI)
	}
	if !read.Scopes.Equal(written.Scopes) {
		t.Errorf("scopes are %q, want %q", read.Scopes, written.Scopes)
	}
	if !read.Resource.Equal(written.Resource) {
		t.Errorf("resource is %q, want %q", read.Resource, written.Resource)
	}
	if read.Challenge.Value() != written.Challenge.Value() {
		t.Error("the PKCE challenge did not round trip")
	}
	if read.State.Reveal() != written.State.Reveal() {
		t.Error("the client state did not round trip byte for byte")
	}
	if !read.ExpiresAt.Equal(written.ExpiresAt) {
		t.Errorf("expiry is %s, want %s", read.ExpiresAt, written.ExpiresAt)
	}
}

func TestTransactionReportsNotFound(t *testing.T) {
	f := newFixture(t)

	_, err := f.adapter.Transaction(context.Background(), lookupOf("never-stored"))
	if !errors.Is(err, oauthserver.ErrTransactionNotFound) {
		t.Fatalf("error is %v, want ErrTransactionNotFound", err)
	}
}

// The stage is derived: the store has no column for it, and an attached principal is
// exactly what "authenticated" means.
func TestUpdateTransactionAdvancesStageAndVersion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	written := f.transaction("advance", oauthserver.ClientState{})
	if err := f.adapter.CreateTransaction(ctx, written); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	advanced := written
	advanced.Principal = f.principal
	advanced.Stage = oauthserver.StageAuthenticated
	if err := f.adapter.UpdateTransaction(ctx, advanced, 0); err != nil {
		t.Fatalf("UpdateTransaction: %v", err)
	}

	read, err := f.adapter.Transaction(ctx, written.Lookup)
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
	if read.Stage != oauthserver.StageAuthenticated {
		t.Errorf("stage is %v, want authenticated", read.Stage)
	}
	if read.Principal.ID() != f.principal.ID() {
		t.Errorf("principal is %q, want %q", read.Principal.ID(), f.principal.ID())
	}
	if read.Version != 1 {
		t.Errorf("version is %d, want 1", read.Version)
	}
}

func TestUpdateTransactionIsACompareAndSet(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	written := f.transaction("cas", oauthserver.ClientState{})
	if err := f.adapter.CreateTransaction(ctx, written); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	if err := f.adapter.UpdateTransaction(ctx, written, 0); err != nil {
		t.Fatalf("UpdateTransaction: %v", err)
	}

	err := f.adapter.UpdateTransaction(ctx, written, 0)
	if !errors.Is(err, oauthserver.ErrTransactionConflict) {
		t.Fatalf("error is %v, want ErrTransactionConflict", err)
	}
}

func TestUpdateTransactionReportsAMissingRow(t *testing.T) {
	f := newFixture(t)

	written := f.transaction("gone", oauthserver.ClientState{})
	err := f.adapter.UpdateTransaction(context.Background(), written, 0)
	if !errors.Is(err, oauthserver.ErrTransactionNotFound) {
		t.Fatalf("error is %v, want ErrTransactionNotFound", err)
	}
}

func TestConsumeTransactionReturnsTheRecordAndDeletesIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	written := f.transaction("consume", oauthserver.ClientState{})
	if err := f.adapter.CreateTransaction(ctx, written); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	consumed, err := f.adapter.ConsumeTransaction(ctx, written.Lookup)
	if err != nil {
		t.Fatalf("ConsumeTransaction: %v", err)
	}
	assertTransactionMatches(t, consumed, written)

	if _, err := f.adapter.ConsumeTransaction(ctx, written.Lookup); !errors.Is(
		err, oauthserver.ErrTransactionNotFound) {
		t.Fatalf("second consume returned %v, want ErrTransactionNotFound", err)
	}
}

// An expired transaction comes back from ConsumeTransaction — the caller ignores the
// record and the row is gone either way, which is how expiry is discarded.
func TestConsumeTransactionDiscardsAnExpiredRecord(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	written := f.transaction("expired", oauthserver.ClientState{})
	if err := f.adapter.CreateTransaction(ctx, written); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	f.clock.advance(time.Hour)

	consumed, err := f.adapter.ConsumeTransaction(ctx, written.Lookup)
	if err != nil {
		t.Fatalf("ConsumeTransaction: %v", err)
	}
	if !consumed.IsExpired(f.clock.Now()) {
		t.Error("the consumed record does not report itself expired")
	}
	if _, err := f.adapter.Transaction(ctx, written.Lookup); !errors.Is(
		err, oauthserver.ErrTransactionNotFound) {
		t.Fatalf("the expired transaction survived: %v", err)
	}
}

func TestConsentRoundTripsOnTheExactKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.seedConsent(t)

	consent, err := f.adapter.Consent(ctx, f.consentKey())
	if err != nil {
		t.Fatalf("Consent: %v", err)
	}
	if consent.Key != f.consentKey() {
		t.Error("the consent came back under a different key")
	}
	if !consent.Scopes.Equal(f.scopes) {
		t.Errorf("scopes are %q, want %q", consent.Scopes, f.scopes)
	}
	if !consent.Covers(f.scopes) {
		t.Error("the consent does not cover the scopes it was granted")
	}
}

// The key is the whole tuple. A different resource is a different grant, never a
// wildcard over the one that exists.
func TestConsentIsNotFoundUnderADifferentResource(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.seedConsent(t)

	key := f.consentKey()
	key.Resource = mustResource(t, "https://other.example/")
	if _, err := f.adapter.Consent(ctx, key); !errors.Is(err, oauthserver.ErrConsentNotFound) {
		t.Fatalf("error is %v, want ErrConsentNotFound", err)
	}
}

func TestRevokeConsentIsIdempotentAndCascades(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access, _ := f.seedFamily(t, "family-revoke-consent", "revoke-consent")

	if err := f.adapter.RevokeConsent(ctx, f.consentKey()); err != nil {
		t.Fatalf("RevokeConsent: %v", err)
	}
	if _, err := f.adapter.Consent(ctx, f.consentKey()); !errors.Is(
		err, oauthserver.ErrConsentNotFound) {
		t.Fatalf("the consent survived revocation: %v", err)
	}
	if _, err := f.adapter.AccessToken(ctx, access.Lookup); !errors.Is(
		err, oauthserver.ErrTokenRevoked) {
		t.Fatalf("the token survived the cascade: %v", err)
	}
	if err := f.adapter.RevokeConsent(ctx, f.consentKey()); err != nil {
		t.Fatalf("second RevokeConsent: %v", err)
	}
}

// A family written before migration 0002 carries an empty resource, so it sits under
// a different consent key. The adapter revokes the key it is given and no other: the
// alternative — treating an empty resource as a wildcard — is the confused-deputy
// failure the wide key exists to prevent. Reaching a legacy family means revoking its
// own key, or revoking the principal.
func TestRevokeConsentDoesNotReachAPreMigrationFamily(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	legacyKey := f.consentKey()
	legacyKey.Resource = oauthserver.Resource{}
	legacy := oauthserver.Consent{Key: legacyKey, Scopes: f.scopes, GrantedAt: f.clock.Now()}
	if err := f.adapter.SaveConsent(ctx, legacy); err != nil {
		t.Fatalf("SaveConsent: %v", err)
	}
	access, refresh := f.pair("family-legacy", "legacy", 0)
	access.Resource = oauthserver.Resource{}
	refresh.Resource = oauthserver.Resource{}
	if err := f.adapter.SaveTokenPair(ctx, access, refresh); err != nil {
		t.Fatalf("SaveTokenPair: %v", err)
	}

	f.seedConsent(t)
	if err := f.adapter.RevokeConsent(ctx, f.consentKey()); err != nil {
		t.Fatalf("RevokeConsent: %v", err)
	}
	if _, err := f.adapter.AccessToken(ctx, access.Lookup); err != nil {
		t.Fatalf("the legacy token was revoked by another key's revocation: %v", err)
	}

	if err := f.adapter.RevokeConsent(ctx, legacyKey); err != nil {
		t.Fatalf("RevokeConsent on the legacy key: %v", err)
	}
	if _, err := f.adapter.AccessToken(ctx, access.Lookup); !errors.Is(
		err, oauthserver.ErrTokenRevoked) {
		t.Fatalf("the legacy token survived its own key's revocation: %v", err)
	}
}
