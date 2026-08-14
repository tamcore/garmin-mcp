package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// The confused-deputy mitigation, asserted: four grants that differ only in redirect URI
// or resource are four rows, not one.

// otherRedirectURI, otherResource and otherScope are the second value of each key part.
const (
	otherRedirectURI = "https://client.example/other-callback"
	otherResource    = "https://other.example/"
	otherScope       = "garmin.write"
)

// consentKeyMatrix is every combination of the two redirect URIs and the two resources.
func consentKeyMatrix(principalID, clientID string) []store.ConsentKey {
	return []store.ConsentKey{
		{PrincipalID: principalID, ClientID: clientID,
			RedirectURI: testRedirectURI, Resource: testAudience},
		{PrincipalID: principalID, ClientID: clientID,
			RedirectURI: testRedirectURI, Resource: otherResource},
		{PrincipalID: principalID, ClientID: clientID,
			RedirectURI: otherRedirectURI, Resource: testAudience},
		{PrincipalID: principalID, ClientID: clientID,
			RedirectURI: otherRedirectURI, Resource: otherResource},
	}
}

// TestConsentKeysDoNotCollide: under the narrow key these four grants would be one row,
// so the last write would silently replace three unrelated decisions.
func TestConsentKeysDoNotCollide(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	keys := consentKeyMatrix(principal.ID, client.ID)
	for index, key := range keys {
		scopes := []string{testScope}
		if index%2 == 1 {
			scopes = []string{testScope, otherScope}
		}
		if err := opened.GrantConsentFor(ctx, key, scopes); err != nil {
			t.Fatalf("GrantConsentFor %+v: %v", key, err)
		}
	}

	for index, key := range keys {
		consent, err := opened.ConsentFor(ctx, key)
		if err != nil {
			t.Fatalf("ConsentFor %+v: %v", key, err)
		}
		want := 1
		if index%2 == 1 {
			want = 2
		}
		if len(consent.Scopes) != want {
			t.Errorf("consent %+v holds %d scopes, want %d; the keys collided",
				key, len(consent.Scopes), want)
		}
		if consent.Key() != key {
			t.Errorf("consent reports key %+v, want %+v", consent.Key(), key)
		}
	}
}

// TestGrantConsentForAcceptsAnEmptyScopeSet: authorizing a client for nothing is a
// decision a user can make, so it has to be storable.
func TestGrantConsentForAcceptsAnEmptyScopeSet(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)
	key := store.ConsentKey{PrincipalID: principal.ID, ClientID: client.ID}

	if err := opened.GrantConsentFor(ctx, key, nil); err != nil {
		t.Fatalf("GrantConsentFor with no scopes: %v", err)
	}
	consent, err := opened.ConsentFor(ctx, key)
	if err != nil {
		t.Fatalf("ConsentFor: %v", err)
	}
	if len(consent.Scopes) != 0 {
		t.Fatalf("Scopes = %v, want none", consent.Scopes)
	}
}

// TestNarrowGrantIsTheEmptyKeyRatherThanAWildcard: the narrow methods address the row
// with an empty redirect URI and an empty resource, and nothing else.
func TestNarrowGrantIsTheEmptyKeyRatherThanAWildcard(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	if err := opened.GrantConsent(ctx, principal.ID, client.ID, []string{testScope}); err != nil {
		t.Fatalf("GrantConsent: %v", err)
	}
	if _, err := opened.Consent(ctx, principal.ID, client.ID); err != nil {
		t.Fatalf("Consent: %v", err)
	}

	wide := store.ConsentKey{
		PrincipalID: principal.ID, ClientID: client.ID,
		RedirectURI: testRedirectURI, Resource: testAudience,
	}
	if _, err := opened.ConsentFor(ctx, wide); !errors.Is(err, store.ErrConsentNotFound) {
		t.Fatalf("ConsentFor a key the narrow grant did not name: err = %v, "+
			"want ErrConsentNotFound", err)
	}
}

// TestConsentForRefusesAnUnusableKey keeps the boundary validation on the new path.
func TestConsentForRefusesAnUnusableKey(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	cases := map[string]store.ConsentKey{
		"an empty principal id": {ClientID: client.ID},
		"an empty client id":    {PrincipalID: principal.ID},
		"a control character in the resource": {
			PrincipalID: principal.ID, ClientID: client.ID, Resource: "https://x\x00/",
		},
	}
	for name, key := range cases {
		if err := opened.GrantConsentFor(ctx, key, nil); !errors.Is(err, store.ErrInvalidArgument) {
			t.Errorf("GrantConsentFor with %s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
}
