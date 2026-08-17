package store_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

func TestCleanupRemovesExpiredStateAndIsIdempotent(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)
	seedTransaction(t, opened, grant.client.ID)
	seedCode(t, opened, grant.principal.ID, grant.client.ID, testCode)

	// Nothing has expired yet, so a sweep must remove nothing.
	before, err := opened.Cleanup(ctx, 0)
	if err != nil {
		t.Fatalf("Cleanup before anything expired: %v", err)
	}
	if before != (store.CleanupStats{}) {
		t.Errorf("Cleanup removed %+v while everything was still live", before)
	}

	// Past every seeded lifetime: transaction and code 10 minutes, access 10 minutes,
	// refresh 24 hours, plus the 24-hour retention on revoked rows.
	clock.advance(72 * time.Hour)

	stats, err := opened.Cleanup(ctx, 0)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if stats.Transactions != 1 {
		t.Errorf("Transactions = %d, want 1", stats.Transactions)
	}
	if stats.Codes != 1 {
		t.Errorf("Codes = %d, want 1", stats.Codes)
	}
	if stats.Tokens != 2 {
		t.Errorf("Tokens = %d, want 2 (one access, one refresh)", stats.Tokens)
	}
	if stats.Families != 1 {
		t.Errorf("Families = %d, want 1 once its tokens are gone", stats.Families)
	}

	second, err := opened.Cleanup(ctx, 0)
	if err != nil {
		t.Fatalf("second Cleanup: %v", err)
	}
	if second != (store.CleanupStats{}) {
		t.Errorf("second Cleanup removed %+v, want nothing left", second)
	}
}

// TestCleanupKeepsLiveRows is the other half: a sweep must not touch anything that has
// not expired.
func TestCleanupKeepsLiveRows(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)
	seedCode(t, opened, grant.principal.ID, grant.client.ID, testCode)

	// Past the code's ten minutes but well inside the refresh token's 24 hours.
	clock.advance(20 * time.Minute)

	stats, err := opened.Cleanup(ctx, 0)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if stats.Codes != 1 {
		t.Errorf("Codes = %d, want the expired code removed", stats.Codes)
	}
	if stats.Families != 0 {
		t.Errorf("Families = %d, want 0: the family still holds a live refresh token", stats.Families)
	}

	// The refresh token still rotates, so the family and its rows survived.
	_, err = opened.RotateRefreshToken(ctx,
		rotation(grant.refresh, "post-cleanup-access", "post-cleanup-refresh"))
	if err != nil {
		t.Fatalf("the live refresh token was swept: %v", err)
	}
}

// TestCleanupIsBounded proves the per-call limit is honored, which is what keeps one
// maintenance tick from holding SQLite's single write lock for an unbounded time.
func TestCleanupIsBounded(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	const codes = 5
	for index := range codes {
		seedCode(t, opened, principal.ID, client.ID, "bounded-code-"+strconv.Itoa(index))
	}
	clock.advance(time.Hour)

	first, err := opened.Cleanup(ctx, 2)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if first.Codes != 2 {
		t.Fatalf("Codes = %d, want exactly the limit of 2", first.Codes)
	}
	if !first.AtLimit(2) {
		t.Error("AtLimit(2) = false, want true so a caller knows to sweep again")
	}

	// Sweeping until it reports nothing removes the rest, in bounded batches.
	removed := first.Codes
	for range codes {
		stats, err := opened.Cleanup(ctx, 2)
		if err != nil {
			t.Fatalf("Cleanup: %v", err)
		}
		removed += stats.Codes
		if stats == (store.CleanupStats{}) {
			break
		}
	}
	if removed != codes {
		t.Errorf("removed %d codes in total, want %d", removed, codes)
	}
}

func TestCleanupRefusesAnUnboundedLimit(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	for _, limit := range []int{-1, 5001} {
		if _, err := opened.Cleanup(ctx, limit); !errors.Is(err, store.ErrInvalidArgument) {
			t.Errorf("Cleanup(%d): err = %v, want ErrInvalidArgument", limit, err)
		}
	}
}

// TestRevokedTokensSurviveOneRetentionWindow: a revoked row is kept past its expiry so an
// operator investigating the revocation can still see it.
func TestRevokedTokensSurviveOneRetentionWindow(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	if _, err := opened.RevokeTokenFamily(ctx, grant.familyID, "operator_revoked"); err != nil {
		t.Fatalf("RevokeTokenFamily: %v", err)
	}

	// Past the access token's expiry but inside the 24-hour retention window.
	clock.advance(time.Hour)
	stats, err := opened.Cleanup(ctx, 0)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if stats.Tokens != 0 {
		t.Errorf("Tokens = %d, want 0 while the revoked rows are still in retention", stats.Tokens)
	}

	// Past the retention window as well.
	clock.advance(48 * time.Hour)
	stats, err = opened.Cleanup(ctx, 0)
	if err != nil {
		t.Fatalf("second Cleanup: %v", err)
	}
	if stats.Tokens != 2 {
		t.Errorf("Tokens = %d, want both rows removed after retention", stats.Tokens)
	}
}

// TestCleanupRetainsAConsumedRowWhileItsFamilyIsLive is problem 1 of the
// refresh-reuse-detection review: a consumed, expired refresh row must survive a
// cleanup sweep for as long as its family holds any token that is neither expired
// nor revoked. Without this, the periodic sweep removes the very row
// refreshGrant's pre-check depends on to detect a replay, so the fix in
// internal/oauthserver/refreshgrant.go only works until the next cleanup tick.
func TestCleanupRetainsAConsumedRowWhileItsFamilyIsLive(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened) // generation 0: access 10m, refresh 24h, from t0.

	// Rotate while generation 0 is still comfortably live, so generation 1's own
	// 24h refresh lifetime runs from a later instant than generation 0's did.
	clock.advance(20 * time.Hour)
	if _, err := opened.RotateRefreshToken(ctx,
		rotation(grant.refresh, "retain-gen1-access", "retain-gen1-refresh"),
	); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	// Now generation 0's refresh row (expires at t0+24h) is expired, but
	// generation 1's (expires at t0+20h+24h = t0+44h) is not.
	clock.advance(5 * time.Hour)

	stats, err := opened.Cleanup(ctx, 0)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// The two access tokens (generation 0 and generation 1) are unconsumed rows
	// past their own 10-minute expiry, and this fix does not touch their
	// retention: they are swept exactly as before.
	if stats.Tokens != 2 {
		t.Fatalf("Tokens removed = %d, want 2 (only the two expired access tokens)", stats.Tokens)
	}
	if stats.Families != 0 {
		t.Fatalf("Families removed = %d, want 0: the family still holds live and consumed rows", stats.Families)
	}

	// The consumed, expired generation-0 refresh row must still be readable: a
	// replay of it is exactly what the pre-check in refreshGrant must still be
	// able to see.
	stored, err := opened.ReadRefreshToken(ctx, grant.refresh)
	if err != nil {
		t.Fatalf("the consumed generation-0 refresh row did not survive cleanup "+
			"while generation 1 is live: %v", err)
	}
	if !stored.Consumed {
		t.Fatal("the surviving row lost its Consumed flag")
	}
}

// TestCleanupSweepsAConsumedRowOnceItsFamilyIsDead is the other half: once every
// token in the family is expired or revoked, the consumed row is no longer
// special and is swept like any other expired row.
func TestCleanupSweepsAConsumedRowOnceItsFamilyIsDead(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	clock.advance(20 * time.Hour)
	if _, err := opened.RotateRefreshToken(ctx,
		rotation(grant.refresh, "sweep-gen1-access", "sweep-gen1-refresh"),
	); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	// Past generation 1's own expiry (t0+44h) too, and past the retention window
	// that would otherwise apply if it were revoked instead of merely expired:
	// t0 + 20h + 5h + 45h = t0+70h.
	clock.advance(5*time.Hour + 45*time.Hour)

	stats, err := opened.Cleanup(ctx, 0)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// Both access tokens were already swept in a real deployment's earlier ticks,
	// but nothing here ran one, so this single sweep removes everything: both
	// access tokens and both refresh generations, once generation 1 died too.
	if stats.Tokens != 4 {
		t.Fatalf("Tokens removed = %d, want 4 (both access tokens and both refresh generations)",
			stats.Tokens)
	}
	if stats.Families != 1 {
		t.Fatalf("Families removed = %d, want 1 once every token in it is gone", stats.Families)
	}
	if _, err := opened.ReadRefreshToken(ctx, grant.refresh); !errors.Is(err, store.ErrTokenNotFound) {
		t.Fatalf("the generation-0 row survived after its family died: err = %v", err)
	}
}

// retainedGenerationWindowForTest mirrors the unexported
// store.retainedConsumedGenerations. It is duplicated here, rather than imported,
// because this file is package store_test and the production constant is
// deliberately unexported; keep the two literals in sync if the bound ever changes.
const retainedGenerationWindowForTest = 200

// rotateChain rotates a fresh grant's refresh token "count" times, advancing the
// clock by one hour before each rotation. That gives every generation a distinct,
// one-hour-later own expiry, which is what lets a single "now" later put some
// generations past their own expiry while the family's newest generation is still
// live. It returns the refresh secret of every generation from 1 through count,
// indexed by generation number (index 0 is unused).
func rotateChain(t *testing.T, s *store.SQLiteStore, clock *fakeClock, seed store.Secret, count int,
) []store.Secret {
	t.Helper()
	secrets := make([]store.Secret, count+1)
	presented := seed
	for generation := 1; generation <= count; generation++ {
		clock.advance(time.Hour)
		tag := strconv.Itoa(generation)
		next := store.NewSecret("chain-refresh-" + tag)
		if _, err := s.RotateRefreshToken(context.Background(),
			rotation(presented, "chain-access-"+tag, next.Reveal())); err != nil {
			t.Fatalf("RotateRefreshToken to generation %d: %v", generation, err)
		}
		secrets[generation] = next
		presented = next
	}
	return secrets
}

// TestCleanupBoundsConsumedRetentionByGeneration is problem 1 of the round-two
// review: deleteExpiredTokens used to retain a consumed row for as long as its
// family was live, with no bound. A continuously rotating client renews family
// liveness on every refresh, so that was unbounded growth. This proves the bound: a
// consumed, expired row that is within the family's most recent
// retainedConsumedGenerations generations survives, and the same shape of row one
// generation further back does not, even though the family is live in both cases.
func TestCleanupBoundsConsumedRetentionByGeneration(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened) // generation 0.

	generations := rotateChain(t, opened, clock, grant.refresh, retainedGenerationWindowForTest+1)
	// The live (unconsumed) generation is retainedGenerationWindowForTest+1.
	// Generation 1 is exactly retainedGenerationWindowForTest generations behind it,
	// which is outside the retained window (only strictly fewer than
	// retainedGenerationWindowForTest generations behind qualifies). Generation 2 is
	// retainedGenerationWindowForTest-1 behind, which is inside it.
	gen1 := generations[1]
	gen2 := generations[2]

	// Generation 1 was issued at t0+1h (expires t0+25h) and generation 2 at t0+2h
	// (expires t0+26h); both are already past their own expiry two hours after the
	// last rotation, while the newest generation keeps its full 24-hour window ahead
	// of it (it was just issued).
	clock.advance(2 * time.Hour)

	if _, err := opened.Cleanup(ctx, 0); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := opened.ReadRefreshToken(ctx, gen2); err != nil {
		t.Fatalf("generation 2, within the retained window, did not survive cleanup: %v", err)
	}
	if _, err := opened.ReadRefreshToken(ctx, gen1); !errors.Is(err, store.ErrTokenNotFound) {
		t.Fatalf("generation 1, beyond the retained window, survived cleanup: err = %v, "+
			"want ErrTokenNotFound", err)
	}
}

func TestRecordAndReadAuditEvents(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	events := []store.AuditEvent{
		{
			Kind: "token.issued", Outcome: store.AuditAllowed,
			PrincipalID: grant.principal.ID, ClientID: grant.client.ID,
		},
		{
			Kind: "token.rotated", Outcome: store.AuditDenied,
			PrincipalID: grant.principal.ID, ClientID: grant.client.ID,
			Detail: "refresh_token_reuse",
		},
		{Kind: "store.write", Outcome: store.AuditError},
	}
	for _, event := range events {
		if err := opened.RecordAuditEvent(ctx, event); err != nil {
			t.Fatalf("RecordAuditEvent(%q): %v", event.Kind, err)
		}
	}

	read, err := opened.AuditEvents(ctx, 10)
	if err != nil {
		t.Fatalf("AuditEvents: %v", err)
	}
	if len(read) != len(events) {
		t.Fatalf("read %d events, want %d", len(read), len(events))
	}
	// Newest first.
	if read[0].Kind != "store.write" || read[0].Outcome != store.AuditError {
		t.Errorf("newest event = %+v, want the last one recorded", read[0])
	}
	if read[1].Detail != "refresh_token_reuse" {
		t.Errorf("Detail = %q, want the recorded reason code", read[1].Detail)
	}
	if read[2].PrincipalID != grant.principal.ID {
		t.Errorf("PrincipalID = %q, want %q", read[2].PrincipalID, grant.principal.ID)
	}
	if read[0].OccurredAt.IsZero() {
		t.Error("OccurredAt is zero; the store's clock must fill it in")
	}
}

// TestAuditEventsRefuseAnythingThatIsNotAReasonCode is the enforcement that keeps a
// credential, an email, a coordinate or a health payload out of the audit log. Every
// value below is refused because it needs a character the grammar rejects.
func TestAuditEventsRefuseAnythingThatIsNotAReasonCode(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	details := map[string]string{
		"a bearer token":      "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.c2ln",
		"an email address":    "rider@example.com",
		"a health payload":    `{"heartRate":142}`,
		"a coordinate pair":   "48.1372,11.5756",
		"a url":               "https://connect.garmin.com/activity/1",
		"a capitalized word":  "TokenReuse",
		"a sentence":          "the refresh token was replayed",
		"a trailing dot":      "token.reuse.",
		"a leading digit":     "1token",
		"an oversized detail": strings.Repeat("a", 100),
	}
	for name, detail := range details {
		err := opened.RecordAuditEvent(ctx, store.AuditEvent{
			Kind:    "token.rotated",
			Outcome: store.AuditDenied,
			Detail:  detail,
		})
		if !errors.Is(err, store.ErrInvalidAuditDetail) {
			t.Errorf("detail %s: err = %v, want ErrInvalidAuditDetail", name, err)
		}
	}

	for name, kind := range map[string]string{
		"an empty kind":     "",
		"a capitalized one": "Token.Issued",
		"one with a space":  "token issued",
	} {
		err := opened.RecordAuditEvent(ctx, store.AuditEvent{Kind: kind, Outcome: store.AuditAllowed})
		if !errors.Is(err, store.ErrInvalidAuditDetail) {
			t.Errorf("kind %s: err = %v, want ErrInvalidAuditDetail", name, err)
		}
	}

	err := opened.RecordAuditEvent(ctx, store.AuditEvent{
		Kind:    "token.issued",
		Outcome: store.AuditOutcome("invented"),
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("an unknown outcome: err = %v, want ErrInvalidArgument", err)
	}

	// Nothing above may have been stored.
	read, err := opened.AuditEvents(ctx, 0)
	if err != nil {
		t.Fatalf("AuditEvents: %v", err)
	}
	if len(read) != 0 {
		t.Errorf("the audit log holds %d events, want 0: every insert was refused", len(read))
	}
}

func TestAuditEventsRefusesAnUnboundedPage(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	for _, limit := range []int{-1, 501} {
		if _, err := opened.AuditEvents(ctx, limit); !errors.Is(err, store.ErrInvalidArgument) {
			t.Errorf("AuditEvents(%d): err = %v, want ErrInvalidArgument", limit, err)
		}
	}
}

// TestAuditEventsSurviveAnUnlink: the audit trail must outlive the rows it describes,
// which is why audit_events carries no foreign key.
func TestAuditEventsSurviveAnUnlink(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	grant := seedGrant(t, opened)

	err := opened.RecordAuditEvent(ctx, store.AuditEvent{
		Kind:        "garmin.unlinked",
		Outcome:     store.AuditAllowed,
		PrincipalID: grant.principal.ID,
	})
	if err != nil {
		t.Fatalf("RecordAuditEvent: %v", err)
	}
	if _, err := opened.UnlinkGarminAccount(ctx, grant.principal.ID); err != nil {
		t.Fatalf("UnlinkGarminAccount: %v", err)
	}

	read, err := opened.AuditEvents(ctx, 10)
	if err != nil {
		t.Fatalf("AuditEvents: %v", err)
	}
	if len(read) != 1 || read[0].PrincipalID != grant.principal.ID {
		t.Errorf("read %d events; the audit record must survive the cascade", len(read))
	}
}
