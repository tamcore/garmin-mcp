package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// The record-shaped issue path and the reads that hand the judgement back to the caller.

const (
	callerFamilyID     = "family-alpha"
	recordAccessToken  = "record-access-token"
	recordRefreshToken = "record-refresh-token"
	testRevokeReason   = "operator_request"
)

// seededRecord is the state the record tests start from.
type seededRecord struct {
	principal store.Principal
	client    store.Client
	familyID  string
	access    store.Secret
	refresh   store.Secret
	issuedAt  time.Time
}

// seedRecordGrant issues one family from caller-minted records.
func seedRecordGrant(t *testing.T, s *store.SQLiteStore, clock *fakeClock,
	familyID string, generation uint64,
) seededRecord {
	t.Helper()
	ctx := context.Background()
	principal := seedPrincipal(t, s)
	client := seedClient(t, s)
	key := store.ConsentKey{
		PrincipalID: principal.ID, ClientID: client.ID,
		RedirectURI: testRedirectURI, Resource: testAudience,
	}
	if err := s.GrantConsentFor(ctx, key, []string{testScope}); err != nil {
		t.Fatalf("GrantConsentFor: %v", err)
	}

	record := seededRecord{
		principal: principal,
		client:    client,
		access:    store.NewSecret(recordAccessToken),
		refresh:   store.NewSecret(recordRefreshToken),
		issuedAt:  clock.Now().Add(-time.Minute),
	}
	issued, err := s.IssueTokenFamilyRecord(ctx, store.TokenFamilyGrant{
		FamilyID:         familyID,
		PrincipalID:      principal.ID,
		ClientID:         client.ID,
		Scopes:           []string{testScope},
		Resource:         testAudience,
		Generation:       generation,
		AccessToken:      record.access,
		RefreshToken:     record.refresh,
		IssuedAt:         record.issuedAt,
		AccessExpiresAt:  record.issuedAt.Add(10 * time.Minute),
		RefreshExpiresAt: record.issuedAt.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("IssueTokenFamilyRecord: %v", err)
	}
	record.familyID = issued
	return record
}

// TestIssueTokenFamilyRecordKeepsTheCallerIdentity: the family id, the generation and
// the absolute instants must be stored as given. A store that minted its own id would
// leave every later revocation addressing an id the database never saw.
func TestIssueTokenFamilyRecordKeepsTheCallerIdentity(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	record := seedRecordGrant(t, opened, clock, callerFamilyID, 4)

	if record.familyID != callerFamilyID {
		t.Fatalf("family id = %q, want the caller's %q", record.familyID, callerFamilyID)
	}
	stored, err := opened.ReadAccessToken(ctx, record.access)
	if err != nil {
		t.Fatalf("ReadAccessToken: %v", err)
	}
	if stored.FamilyID != callerFamilyID || stored.Generation != 4 {
		t.Fatalf("stored family %q generation %d, want %q and 4",
			stored.FamilyID, stored.Generation, callerFamilyID)
	}
	if !stored.IssuedAt.Equal(record.issuedAt.UTC()) {
		t.Errorf("IssuedAt = %s, want the caller's %s", stored.IssuedAt, record.issuedAt.UTC())
	}
	if !stored.ExpiresAt.Equal(record.issuedAt.Add(10 * time.Minute).UTC()) {
		t.Errorf("ExpiresAt = %s, want the caller's instant rather than a re-derived one",
			stored.ExpiresAt)
	}
	if stored.Resource != testAudience {
		t.Errorf("Resource = %q, want %q", stored.Resource, testAudience)
	}
}

// TestIssueTokenFamilyRecordMintsOnlyWhenAsked: an empty id is the request to mint one,
// and a used id is refused rather than merged into.
func TestIssueTokenFamilyRecordMintsOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	record := seedRecordGrant(t, opened, clock, "", 0)

	if record.familyID == "" {
		t.Fatal("an empty family id was not replaced by a minted one")
	}

	_, err := opened.IssueTokenFamilyRecord(ctx, store.TokenFamilyGrant{
		FamilyID:         record.familyID,
		PrincipalID:      record.principal.ID,
		ClientID:         record.client.ID,
		Resource:         testAudience,
		AccessToken:      store.NewSecret("second-access"),
		RefreshToken:     store.NewSecret("second-refresh"),
		IssuedAt:         clock.Now(),
		AccessExpiresAt:  clock.Now().Add(time.Minute),
		RefreshExpiresAt: clock.Now().Add(time.Hour),
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("reusing a family id: err = %v, want ErrInvalidArgument", err)
	}
}

// TestIssueTokenFamilyRecordAcceptsNoScopeAndNoResource: an empty scope set and a zero
// resource are states, and a store that refused them would force an invented value.
func TestIssueTokenFamilyRecordAcceptsNoScopeAndNoResource(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)
	if err := opened.GrantConsentFor(ctx,
		store.ConsentKey{PrincipalID: principal.ID, ClientID: client.ID}, nil); err != nil {
		t.Fatalf("GrantConsentFor: %v", err)
	}

	access := store.NewSecret("scopeless-access")
	if _, err := opened.IssueTokenFamilyRecord(ctx, store.TokenFamilyGrant{
		PrincipalID:      principal.ID,
		ClientID:         client.ID,
		AccessToken:      access,
		RefreshToken:     store.NewSecret("scopeless-refresh"),
		IssuedAt:         clock.Now(),
		AccessExpiresAt:  clock.Now().Add(time.Minute),
		RefreshExpiresAt: clock.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("IssueTokenFamilyRecord with no scopes and no resource: %v", err)
	}

	stored, err := opened.ReadAccessToken(ctx, access)
	if err != nil {
		t.Fatalf("ReadAccessToken: %v", err)
	}
	if len(stored.Scopes) != 0 || stored.Audience != "" {
		t.Fatalf("stored scopes %v audience %q, want none and empty", stored.Scopes, stored.Audience)
	}
}

// TestReadRefreshTokenReturnsAConsumedRecord is the reason the read exists: a replay has
// to reach RotateRefreshToken, which revokes the family in the same transaction that
// detects it. A read that refused early would report the replay and leave the family
// alive.
func TestReadRefreshTokenReturnsAConsumedRecord(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	record := seedRecordGrant(t, opened, clock, callerFamilyID, 0)

	next := rotation(record.refresh, "rotated-access", "rotated-refresh")
	if _, err := opened.RotateRefreshToken(ctx, next); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	consumed, err := opened.ReadRefreshToken(ctx, record.refresh)
	if err != nil {
		t.Fatalf("ReadRefreshToken on a rotated token: %v", err)
	}
	if !consumed.Consumed || consumed.ConsumedAt.IsZero() {
		t.Fatal("the rotated token does not report itself consumed")
	}
	if consumed.Generation != 0 {
		t.Errorf("Generation = %d, want 0 for the first token of the family", consumed.Generation)
	}

	rotated, err := opened.ReadRefreshToken(ctx, next.NextRefreshToken)
	if err != nil {
		t.Fatalf("ReadRefreshToken on the replacement: %v", err)
	}
	if rotated.Generation != 1 || rotated.Consumed {
		t.Fatalf("the replacement is generation %d consumed %t, want generation 1 and unconsumed",
			rotated.Generation, rotated.Consumed)
	}
}

// TestReadTokensRefuseUnknownAndRevokedMaterial: what a caller may not judge for itself.
func TestReadTokensRefuseUnknownAndRevokedMaterial(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	record := seedRecordGrant(t, opened, clock, callerFamilyID, 0)

	if _, err := opened.ReadAccessToken(ctx, store.NewSecret("never-issued")); !errors.Is(
		err, store.ErrTokenNotFound) {
		t.Errorf("ReadAccessToken on unknown material: err = %v, want ErrTokenNotFound", err)
	}
	if _, err := opened.ReadRefreshToken(ctx, store.Secret{}); !errors.Is(
		err, store.ErrInvalidArgument) {
		t.Errorf("ReadRefreshToken with no material: err = %v, want ErrInvalidArgument", err)
	}
	// An access token cannot be presented as a refresh token: the lookup values are
	// derived under different purposes.
	if _, err := opened.ReadRefreshToken(ctx, record.access); !errors.Is(
		err, store.ErrTokenNotFound) {
		t.Errorf("ReadRefreshToken with access material: err = %v, want ErrTokenNotFound", err)
	}

	if _, err := opened.RevokeTokenFamily(ctx, record.familyID, testRevokeReason); err != nil {
		t.Fatalf("RevokeTokenFamily: %v", err)
	}
	for name, err := range map[string]error{
		"access":  readAccessError(ctx, opened, record.access),
		"refresh": readRefreshError(ctx, opened, record.refresh),
	} {
		if !errors.Is(err, store.ErrTokenRevoked) {
			t.Errorf("reading a revoked %s token: err = %v, want ErrTokenRevoked", name, err)
		}
	}
}

func readAccessError(ctx context.Context, s *store.SQLiteStore, token store.Secret) error {
	_, err := s.ReadAccessToken(ctx, token)
	return err
}

func readRefreshError(ctx context.Context, s *store.SQLiteStore, token store.Secret) error {
	_, err := s.ReadRefreshToken(ctx, token)
	return err
}

// TestReadAccessTokenReportsExpiryRatherThanEnforcingIt: the enforcing read still
// refuses, and the record-returning read hands back the window so the caller can.
func TestReadAccessTokenReportsExpiryRatherThanEnforcingIt(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	record := seedRecordGrant(t, opened, clock, callerFamilyID, 0)

	clock.advance(30 * time.Minute)
	if _, err := opened.LookupAccessToken(ctx, record.access); !errors.Is(
		err, store.ErrTokenExpired) {
		t.Fatalf("LookupAccessToken on an expired token: err = %v, want ErrTokenExpired", err)
	}

	stored, err := opened.ReadAccessToken(ctx, record.access)
	if err != nil {
		t.Fatalf("ReadAccessToken on an expired token: %v", err)
	}
	if !stored.IsExpired(clock.Now()) {
		t.Fatal("the returned record does not report itself expired")
	}
}
