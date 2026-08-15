package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// recordingSink is a RevocationSink that keeps what it was told. It is safe for
// concurrent use because the store publishes from whichever goroutine ran the
// cascade.
type recordingSink struct {
	mu     sync.Mutex
	events []store.RevocationEvent
}

func (r *recordingSink) PublishRevocation(event store.RevocationEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordingSink) recorded() []store.RevocationEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]store.RevocationEvent(nil), r.events...)
}

// newFeedStore opens a store that publishes its revocations into a recording sink.
func newFeedStore(t *testing.T) (*store.SQLiteStore, *recordingSink) {
	t.Helper()
	sink := &recordingSink{}
	clock := newFakeClock()
	opened, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{
		Path:        testDBPath(t),
		Key:         testKey(t),
		Now:         clock.Now,
		Revocations: sink,
	})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() {
		if err := opened.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return opened, sink
}

// Every cascade that withdraws an authorization must announce it, or a session that
// is already open survives the revocation until its next request.
func TestRevocationCascadesPublishAnEvent(t *testing.T) {
	tests := map[string]struct {
		revoke func(t *testing.T, s *store.SQLiteStore, grant seededGrant)
		want   func(grant seededGrant) store.RevocationEvent
	}{
		"consent": {
			revoke: func(t *testing.T, s *store.SQLiteStore, grant seededGrant) {
				t.Helper()
				if _, err := s.RevokeConsent(context.Background(),
					grant.principal.ID, grant.client.ID); err != nil {
					t.Fatalf("RevokeConsent: %v", err)
				}
			},
			want: func(grant seededGrant) store.RevocationEvent {
				return store.RevocationEvent{
					PrincipalID: grant.principal.ID,
					ClientID:    grant.client.ID,
					Reason:      store.ReasonConsentRevoked,
				}
			},
		},
		"one grant": {
			revoke: func(t *testing.T, s *store.SQLiteStore, grant seededGrant) {
				t.Helper()
				if _, err := s.RevokeConsentFor(context.Background(), store.ConsentKey{
					PrincipalID: grant.principal.ID,
					ClientID:    grant.client.ID,
					Resource:    testAudience,
				}); err != nil {
					t.Fatalf("RevokeConsentFor: %v", err)
				}
			},
			want: func(grant seededGrant) store.RevocationEvent {
				return store.RevocationEvent{
					PrincipalID: grant.principal.ID,
					ClientID:    grant.client.ID,
					Reason:      store.ReasonConsentRevoked,
				}
			},
		},
		"every token of a principal": {
			revoke: func(t *testing.T, s *store.SQLiteStore, grant seededGrant) {
				t.Helper()
				if _, err := s.RevokePrincipalTokens(context.Background(),
					grant.principal.ID); err != nil {
					t.Fatalf("RevokePrincipalTokens: %v", err)
				}
			},
			want: func(grant seededGrant) store.RevocationEvent {
				return store.RevocationEvent{
					PrincipalID: grant.principal.ID,
					Reason:      store.ReasonConsentRevoked,
				}
			},
		},
		"one family": {
			revoke: func(t *testing.T, s *store.SQLiteStore, grant seededGrant) {
				t.Helper()
				if _, err := s.RevokeTokenFamily(context.Background(),
					grant.familyID, store.ReasonConsentRevoked); err != nil {
					t.Fatalf("RevokeTokenFamily: %v", err)
				}
			},
			want: func(grant seededGrant) store.RevocationEvent {
				return store.RevocationEvent{
					PrincipalID: grant.principal.ID,
					ClientID:    grant.client.ID,
					FamilyID:    grant.familyID,
					Reason:      store.ReasonConsentRevoked,
				}
			},
		},
		"a garmin unlink": {
			revoke: func(t *testing.T, s *store.SQLiteStore, grant seededGrant) {
				t.Helper()
				if _, err := s.UnlinkGarminAccount(context.Background(),
					grant.principal.ID); err != nil {
					t.Fatalf("UnlinkGarminAccount: %v", err)
				}
			},
			want: func(grant seededGrant) store.RevocationEvent {
				return store.RevocationEvent{
					PrincipalID: grant.principal.ID,
					Reason:      store.ReasonGarminUnlinked,
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			sqlite, sink := newFeedStore(t)
			grant := seedGrant(t, sqlite)

			tc.revoke(t, sqlite, grant)

			events := sink.recorded()
			if len(events) != 1 {
				t.Fatalf("published %d events, want exactly 1: %v", len(events), events)
			}
			if events[0] != tc.want(grant) {
				t.Errorf("published %+v, want %+v", events[0], tc.want(grant))
			}
		})
	}
}

// A replayed refresh token revokes its family, and that revocation is exactly the
// one a live session must not survive.
func TestRefreshTokenReusePublishesTheFamilyRevocation(t *testing.T) {
	sqlite, sink := newFeedStore(t)
	ctx := context.Background()
	grant := seedGrant(t, sqlite)

	rotation := store.RefreshRotation{
		Presented:        grant.refresh,
		NextAccessToken:  store.NewSecret("rotated-access-token"),
		NextRefreshToken: store.NewSecret("rotated-refresh-token"),
		AccessLifetime:   10 * time.Minute,
		RefreshLifetime:  24 * time.Hour,
	}
	if _, err := sqlite.RotateRefreshToken(ctx, rotation); err != nil {
		t.Fatalf("the first rotation returned error: %v", err)
	}
	if events := sink.recorded(); len(events) != 0 {
		t.Fatalf("a successful rotation published %v", events)
	}

	replay := rotation
	replay.NextAccessToken = store.NewSecret("replayed-access-token")
	replay.NextRefreshToken = store.NewSecret("replayed-refresh-token")
	if _, err := sqlite.RotateRefreshToken(ctx, replay); !errors.Is(err, store.ErrRefreshTokenReuse) {
		t.Fatalf("the replay returned %v, want ErrRefreshTokenReuse", err)
	}

	events := sink.recorded()
	if len(events) != 1 {
		t.Fatalf("published %d events, want exactly 1: %v", len(events), events)
	}
	want := store.RevocationEvent{
		PrincipalID: grant.principal.ID,
		ClientID:    grant.client.ID,
		FamilyID:    grant.familyID,
		Reason:      store.ReasonRefreshReuse,
	}
	if events[0] != want {
		t.Errorf("published %+v, want %+v", events[0], want)
	}
}

// A cascade that did not commit must announce nothing: a session closed for a
// revocation that never happened is a disconnection with no authorization change
// behind it.
func TestARefusedRevocationPublishesNothing(t *testing.T) {
	sqlite, sink := newFeedStore(t)
	ctx := context.Background()
	seedGrant(t, sqlite)

	if _, err := sqlite.RevokeTokenFamily(ctx, testUnknownID,
		store.ReasonConsentRevoked); !errors.Is(err, store.ErrTokenNotFound) {
		t.Fatalf("RevokeTokenFamily on an unknown family = %v, want ErrTokenNotFound", err)
	}
	if _, err := sqlite.UnlinkGarminAccount(ctx, testUnknownID); !errors.Is(
		err, store.ErrPrincipalNotFound) {
		t.Fatalf("UnlinkGarminAccount on an unknown principal = %v, want ErrPrincipalNotFound", err)
	}

	if events := sink.recorded(); len(events) != 0 {
		t.Errorf("a refused revocation published %v", events)
	}
}

// A store with no sink is the stdio-shaped deployment, and every cascade must still
// work there, so the publisher is never a dependency a caller has to supply.
func TestRevocationsWorkWithNoSink(t *testing.T) {
	sqlite, _ := newTestStore(t)
	grant := seedGrant(t, sqlite)

	if _, err := sqlite.RevokeConsent(context.Background(),
		grant.principal.ID, grant.client.ID); err != nil {
		t.Fatalf("RevokeConsent with no sink: %v", err)
	}
}
