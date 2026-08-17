package oauthstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
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

	if err := f.adapter.RevokeFamily(ctx, access.Family, oauthserver.RevokeReasonClient); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}
	if err := f.adapter.RevokeFamily(ctx, access.Family, oauthserver.RevokeReasonClient); err != nil {
		t.Fatalf("second RevokeFamily: %v", err)
	}
	if _, err := f.adapter.AccessToken(ctx, access.Lookup); !errors.Is(
		err, oauthserver.ErrTokenRevoked) {
		t.Fatalf("error is %v, want ErrTokenRevoked", err)
	}

	err := f.adapter.RevokeFamily(ctx, oauthserver.FamilyID("no-such-family"), oauthserver.RevokeReasonClient)
	if !errors.Is(err, oauthserver.ErrTokenNotFound) {
		t.Fatalf("error is %v, want ErrTokenNotFound", err)
	}
}

// TestRevokeFamilyRefusesAnUnrecognisedReasonAndDoesNotRevoke is problem 2 of the
// round-two review: reasonFor used to map the zero value and any future
// RevokeReason this switch was never updated for onto familyRevocationReason,
// which would silently file a security event under the wrong audit reason. An
// unrecognised reason must be an error, and the family must not be revoked, so a
// caller cannot end up with a successful revocation filed under a fabricated
// reason.
func TestRevokeFamilyRefusesAnUnrecognisedReasonAndDoesNotRevoke(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access, _ := f.seedFamily(t, "family-bad-reason", "bad-reason")

	unrecognised := oauthserver.RevokeReason(999)
	err := f.adapter.RevokeFamily(ctx, access.Family, unrecognised)
	if err == nil {
		t.Fatal("RevokeFamily with an unrecognised reason: err = nil, want an error")
	}
	if !errors.Is(err, oauthserver.ErrStorage) {
		t.Fatalf("error is %v, want it to wrap oauthserver.ErrStorage", err)
	}

	// The family must be untouched: a rejected reason must not become a silent
	// no-op revocation.
	if _, err := f.adapter.AccessToken(ctx, access.Lookup); err != nil {
		t.Fatalf("the family was revoked despite the rejected reason: %v", err)
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

// TestRefreshTokenReportsWhetherItWasConsumed is the plumbing the refreshGrant
// pre-check in internal/oauthserver depends on: a refresh token that was already
// rotated away must report Consumed true when read back, and one that never was
// must report false. Without this, a presented token that is both consumed and
// past its own expiry cannot be told apart from one that is merely expired, which
// is exactly the defect this pass fixes.
func TestRefreshTokenReportsWhetherItWasConsumed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, presented := f.seedFamily(t, "family-consumed-flag", "consumed-flag")

	unread, err := f.adapter.RefreshToken(ctx, presented.Lookup)
	if err != nil {
		t.Fatalf("RefreshToken before rotation: %v", err)
	}
	if unread.Consumed {
		t.Fatal("a freshly issued refresh token reports itself consumed")
	}

	nextAccess, nextRefresh := f.pair(presented.Family, "consumed-flag-2", 1)
	if err := f.adapter.RotateRefreshToken(ctx, presented.Lookup, nextAccess, nextRefresh); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	rotatedAway, err := f.adapter.RefreshToken(ctx, presented.Lookup)
	if err != nil {
		t.Fatalf("RefreshToken after rotation: %v", err)
	}
	if !rotatedAway.Consumed {
		t.Fatal("a rotated-away refresh token does not report itself consumed")
	}

	stillLive, err := f.adapter.RefreshToken(ctx, nextRefresh.Lookup)
	if err != nil {
		t.Fatalf("RefreshToken for the replacement: %v", err)
	}
	if stillLive.Consumed {
		t.Fatal("the freshly issued replacement reports itself consumed")
	}
}

// TestRevokeFamilyPublishesAnEventForAConsumedReplay is test 5 of the refresh-reuse
// fix: the oauthserver pre-check for a consumed-and-expired refresh token revokes
// through Adapter.RevokeFamily, the exact same call every other revocation path in
// this package uses. This proves that call still announces itself through the real
// SQLite-backed cascade (RevokeTokenFamily -> publishRevocation), so a live session
// actually closes rather than surviving until its next request.
func TestRevokeFamilyPublishesAnEventForAConsumedReplay(t *testing.T) {
	f, sink := newFixtureWithSink(t)
	ctx := context.Background()
	_, presented := f.seedFamily(t, "family-consumed-replay", "consumed-replay")

	nextAccess, nextRefresh := f.pair(presented.Family, "consumed-replay-2", 1)
	if err := f.adapter.RotateRefreshToken(ctx, presented.Lookup, nextAccess, nextRefresh); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	// Read it back the way refreshGrant's pre-check does, confirm it reports itself
	// consumed, then revoke through the exact call the pre-check makes.
	replayed, err := f.adapter.RefreshToken(ctx, presented.Lookup)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if !replayed.Consumed {
		t.Fatal("the presented token does not report itself consumed")
	}

	if err := f.adapter.RevokeFamily(ctx, replayed.Family, oauthserver.RevokeReasonReplay); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}

	events := sink.recorded()
	if len(events) != 1 {
		t.Fatalf("got %d revocation events, want 1", len(events))
	}
	if events[0].FamilyID != string(replayed.Family) {
		t.Errorf("event family is %q, want %q", events[0].FamilyID, replayed.Family)
	}
	if events[0].PrincipalID != f.principal.ID() {
		t.Errorf("event principal is %q, want %q", events[0].PrincipalID, f.principal.ID())
	}
	// The whole point of the pre-check is preserving the reuse signal: it must be
	// filed under the exact reason code the transactional RotateRefreshToken reuse
	// path uses, not the generic client-revocation reason, or an audit consumer
	// cannot tell a caught replay from a client hanging up.
	if events[0].Reason != store.ReasonRefreshReuse {
		t.Errorf("event reason is %q, want %q", events[0].Reason, store.ReasonRefreshReuse)
	}
}

// TestConsumedRowSurvivesCleanupAndReplayStillRevokesTheLiveFamily is test 5 of the
// review: problem 1's fake-store tests and refreshgrant_test.go's fake-store tests
// both stop short of the property that actually matters, which only the real
// SQLite-backed cleanup and the real SQLite-backed adapter can prove together. It
// drives, against the real store: a consumed generation-0 row, a live generation-1
// row, a Cleanup run in between, and then a replay of the consumed row — the exact
// sequence refreshGrant's pre-check depends on — and asserts the still-live family
// is revoked afterward, not merely that the row used to detect it survived.
func TestConsumedRowSurvivesCleanupAndReplayStillRevokesTheLiveFamily(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	access0, refresh0 := f.seedFamily(t, "family-cleanup-replay", "cleanup-replay-0")

	// Rotate while generation 0 is still comfortably live.
	f.clock.advance(20 * time.Hour)
	access1, refresh1 := f.pair(access0.Family, "cleanup-replay-1", 1)
	if err := f.adapter.RotateRefreshToken(ctx, refresh0.Lookup, access1, refresh1); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	// Generation 0 (expires t0+24h) is now expired; generation 1 (issued at
	// t0+20h, expires t0+44h) is not.
	f.clock.advance(5 * time.Hour)

	if _, err := f.sqlite.Cleanup(ctx, 0); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// The replay: read the consumed row exactly as refreshGrant's pre-check does.
	replayed, err := f.adapter.RefreshToken(ctx, refresh0.Lookup)
	if err != nil {
		t.Fatalf("the consumed generation-0 row did not survive Cleanup while "+
			"generation 1 is live: %v", err)
	}
	if !replayed.Consumed || !replayed.IsExpired(f.clock.Now()) {
		t.Fatalf("replayed = %+v, want it to still read as consumed and expired", replayed)
	}

	if err := f.adapter.RevokeFamily(ctx, replayed.Family, oauthserver.RevokeReasonReplay); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}

	// Generation 1's refresh token is the live anchor the whole test is about: it
	// must now be revoked. (Its sibling access token is not asserted here: at these
	// compressed test timescales its own 10-minute access lifetime has already
	// elapsed by the time Cleanup ran, so Cleanup — correctly, and independently of
	// this fix — already swept it as an ordinary expired, unconsumed row.)
	if _, err := f.adapter.RefreshToken(ctx, refresh1.Lookup); !errors.Is(err, oauthserver.ErrTokenRevoked) {
		t.Fatalf("generation 1's refresh token survived: err = %v", err)
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

// TestRotateRefreshTokenPersistsTheNarrowedScopeOfTheNewAccessToken guards the
// adapter seam where a narrowed refresh used to be silently widened again.
//
// OAuth lets a refresh narrow scope. internal/oauthserver narrows it and reports
// the narrow set in the token response, but verification reads the scopes off the
// PERSISTED row. This adapter is the only thing that carries the narrowed set into
// the store, and it used not to: the rotation inherited the consumed token's
// scopes, so a client that deliberately narrowed a token to hand to a lower-trust
// consumer was told it was read-only while the row still granted write and
// destructive scope.
//
// The mutant this kills: dropping the Scopes field from the store.RefreshRotation
// this adapter builds. Note that oauthserver's own fake store cannot catch it —
// the fake persists the AccessToken it is handed, so it is faithful to the
// intended contract and blind to the adapter that broke it.
func TestRotateRefreshTokenPersistsTheNarrowedScopeOfTheNewAccessToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// The family being consumed must be WIDER than what the rotation narrows to,
	// or there is nothing to detect: the fixture's own scope set is the narrow one,
	// so seeding through seedFamily would make this test pass either way. That is
	// the first version of this test, and it passed with the fix removed.
	narrower := mustScopes(t, "garmin.read")
	wider := mustScopes(t, "garmin.read garmin.write")
	if len(wider.Strings()) <= len(narrower.Strings()) {
		t.Fatal("the test's wide set is not wider than its narrow set")
	}
	if err := f.adapter.SaveConsent(ctx, oauthserver.Consent{
		Key:       f.consentKey(),
		Scopes:    wider,
		GrantedAt: f.clock.Now(),
	}); err != nil {
		t.Fatalf("SaveConsent: %v", err)
	}
	wideAccess, presented := f.pair("family-narrowing", "narrowing", 0)
	wideAccess.Scopes = wider
	presented.Scopes = wider
	if err := f.adapter.SaveTokenPair(ctx, wideAccess, presented); err != nil {
		t.Fatalf("SaveTokenPair: %v", err)
	}

	// The rotation the server produces after narrowing: same family, fewer scopes
	// than the token being consumed.
	nextAccess, nextRefresh := f.pair(presented.Family, "narrowing-next", 1)
	nextAccess.Scopes = narrower
	nextRefresh.Scopes = narrower

	if err := f.adapter.RotateRefreshToken(ctx, presented.Lookup, nextAccess, nextRefresh); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	stored, err := f.adapter.AccessToken(ctx, nextAccess.Lookup)
	if err != nil {
		t.Fatalf("AccessToken after the narrowing rotation: %v", err)
	}
	if got := strings.Join(stored.Scopes.Strings(), " "); got != "garmin.read" {
		t.Fatalf("the rotated access token verifies with scopes %q, want %q: the client was "+
			"told its refreshed token was narrowed, and verification reads this row rather "+
			"than the response it was given", got, "garmin.read")
	}
}
