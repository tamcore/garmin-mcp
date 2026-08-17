package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/store"
)

func TestRegisterClientStoresExactRedirectURIs(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	uris := []string{testRedirectURI, "http://127.0.0.1:7777/cb"}
	client, err := opened.RegisterClient(ctx, store.ClientRegistration{
		Name:         testClientName,
		RedirectURIs: uris,
		Secret:       store.NewSecret("test-client-secret"),
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if client.IsPublic {
		t.Error("a client registered with a secret must not be public")
	}

	loaded, err := opened.ClientByID(ctx, client.ID)
	if err != nil {
		t.Fatalf("ClientByID: %v", err)
	}
	if len(loaded.RedirectURIs) != len(uris) {
		t.Fatalf("loaded %d redirect uris, want %d", len(loaded.RedirectURIs), len(uris))
	}
	for index, want := range uris {
		if loaded.RedirectURIs[index] != want {
			t.Errorf("redirect uri %d = %q, want %q", index, loaded.RedirectURIs[index], want)
		}
	}

	// The registration slice must have been copied: mutating it afterwards must not
	// change what the store holds.
	uris[0] = "https://attacker.test/callback"
	again, err := opened.ClientByID(ctx, client.ID)
	if err != nil || again.RedirectURIs[0] != testRedirectURI {
		t.Errorf("the stored uri followed a mutation of the caller's slice: %v", again.RedirectURIs)
	}
}

// TestRedirectURIMatchingIsExact is the anti-bypass test. Every candidate below is a
// classic prefix-matching or normalization bypass, and every one must be refused.
func TestRedirectURIMatchingIsExact(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)

	if err := opened.CheckRedirectURI(ctx, client.ID, testRedirectURI); err != nil {
		t.Fatalf("the registered uri was refused: %v", err)
	}

	for _, candidate := range []string{
		testRedirectURI + "/extra",
		testRedirectURI + "?code=1",
		testRedirectURI + ".attacker.test",
		"https://client.example/Callback",
		"https://client.example:443/callback",
		"https://client.example/callback/",
		"http://client.example/callback",
		"https://client.example.attacker.test/callback",
		"",
	} {
		err := opened.CheckRedirectURI(ctx, client.ID, candidate)
		if !errors.Is(err, store.ErrRedirectURIMismatch) {
			t.Errorf("CheckRedirectURI(%q): err = %v, want ErrRedirectURIMismatch", candidate, err)
		}
	}
}

func TestRegisterClientRefusesUnsafeRedirectURIs(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	cases := map[string][]string{
		"no uri at all":       {},
		"an empty uri":        {""},
		"a relative uri":      {"/callback"},
		"a fragment":          {"https://client.example/callback#part"},
		"plain http off-host": {"http://client.example/callback"},
		"a non-http scheme":   {"javascript:alert(1)"},
		"a duplicate":         {testRedirectURI, testRedirectURI},
		"an oversized uri":    {"https://client.example/" + strings.Repeat("a", 2100)},
		"too many uris":       make([]string, 17),
		"a custom app scheme": {"myapp://callback"},
	}

	for name, uris := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := opened.RegisterClient(ctx, store.ClientRegistration{
				Name:         testClientName,
				RedirectURIs: uris,
			})
			if !errors.Is(err, store.ErrInvalidArgument) {
				t.Fatalf("RegisterClient with %s: err = %v, want ErrInvalidArgument", name, err)
			}
		})
	}
}

func TestRegisterClientRefusesAnEmptyName(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)

	_, err := opened.RegisterClient(context.Background(), store.ClientRegistration{
		Name:         "   ",
		RedirectURIs: []string{testRedirectURI},
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestAuthenticateClientChecksTheSecret(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)

	_, err := opened.AuthenticateClient(ctx, client.ID, store.NewSecret("test-client-secret"))
	if err != nil {
		t.Fatalf("AuthenticateClient with the right secret: %v", err)
	}

	// Every failure below reports the same sentinel on purpose: distinguishing "no such
	// client" from "wrong secret" would be a client-enumeration oracle.
	cases := map[string]struct {
		id     string
		secret store.Secret
	}{
		"the wrong secret": {client.ID, store.NewSecret("not-the-secret")},
		"no secret":        {client.ID, store.Secret{}},
		"an unknown id": {
			testUnknownID, store.NewSecret("test-client-secret"),
		},
	}
	for name, testCase := range cases {
		_, err := opened.AuthenticateClient(ctx, testCase.id, testCase.secret)
		if !errors.Is(err, store.ErrClientNotFound) {
			t.Errorf("AuthenticateClient with %s: err = %v, want ErrClientNotFound", name, err)
		}
	}
}

func TestAuthenticateAPublicClient(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	client, err := opened.RegisterClient(ctx, store.ClientRegistration{
		Name:         "Public Client",
		RedirectURIs: []string{"http://localhost:9999/cb"},
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if !client.IsPublic {
		t.Fatal("a client registered with no secret must be public")
	}

	if _, err := opened.AuthenticateClient(ctx, client.ID, store.Secret{}); err != nil {
		t.Fatalf("AuthenticateClient with no secret: %v", err)
	}
	// A public client that presents a secret is refused: it is either a confused client
	// or an attempt to probe for one.
	_, err = opened.AuthenticateClient(ctx, client.ID, store.NewSecret("invented"))
	if !errors.Is(err, store.ErrClientNotFound) {
		t.Errorf("public client with a secret: err = %v, want ErrClientNotFound", err)
	}
}

func TestDisableClientHidesItFromEveryLookup(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)

	if err := opened.DisableClient(ctx, client.ID); err != nil {
		t.Fatalf("DisableClient: %v", err)
	}
	// Idempotent.
	if err := opened.DisableClient(ctx, client.ID); err != nil {
		t.Fatalf("second DisableClient: %v", err)
	}

	if _, err := opened.ClientByID(ctx, client.ID); !errors.Is(err, store.ErrClientNotFound) {
		t.Errorf("ClientByID after disable: err = %v, want ErrClientNotFound", err)
	}
	_, err := opened.AuthenticateClient(ctx, client.ID, store.NewSecret("test-client-secret"))
	if !errors.Is(err, store.ErrClientNotFound) {
		t.Errorf("AuthenticateClient after disable: err = %v, want ErrClientNotFound", err)
	}
	err = opened.CheckRedirectURI(ctx, client.ID, testRedirectURI)
	if !errors.Is(err, store.ErrClientNotFound) {
		t.Errorf("CheckRedirectURI after disable: err = %v, want ErrClientNotFound", err)
	}

	err = opened.DisableClient(ctx, testUnknownID)
	if !errors.Is(err, store.ErrClientNotFound) {
		t.Errorf("DisableClient on an unknown id: err = %v, want ErrClientNotFound", err)
	}
}

func TestConsentGrantReadAndRegrant(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	_, err := opened.Consent(ctx, principal.ID, client.ID)
	if !errors.Is(err, store.ErrConsentNotFound) {
		t.Fatalf("Consent before any grant: err = %v, want ErrConsentNotFound", err)
	}

	if err := opened.GrantConsent(ctx, principal.ID, client.ID, []string{testScope}); err != nil {
		t.Fatalf("GrantConsent: %v", err)
	}
	consent, err := opened.Consent(ctx, principal.ID, client.ID)
	if err != nil {
		t.Fatalf("Consent: %v", err)
	}
	if len(consent.Scopes) != 1 || consent.Scopes[0] != testScope {
		t.Errorf("Scopes = %v, want [%s]", consent.Scopes, testScope)
	}

	// A re-grant replaces the scope list, in order.
	wider := []string{testScope, otherScope}
	if err := opened.GrantConsent(ctx, principal.ID, client.ID, wider); err != nil {
		t.Fatalf("second GrantConsent: %v", err)
	}
	consent, err = opened.Consent(ctx, principal.ID, client.ID)
	if err != nil {
		t.Fatalf("Consent after re-grant: %v", err)
	}
	if len(consent.Scopes) != 2 || consent.Scopes[1] != "garmin.write" {
		t.Errorf("Scopes = %v, want %v", consent.Scopes, wider)
	}
}

func TestGrantConsentRefusesBadScopesAndUnknownRows(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	badScopes := map[string][]string{
		"no scope":         {},
		"an empty scope":   {""},
		"a duplicate":      {testScope, testScope},
		"embedded space":   {"garmin read"},
		"embedded quote":   {`garmin"read`},
		"a newline":        {"garmin\nread"},
		"an oversized one": {strings.Repeat("s", 200)},
	}
	for name, scopes := range badScopes {
		err := opened.GrantConsent(ctx, principal.ID, client.ID, scopes)
		if !errors.Is(err, store.ErrInvalidArgument) {
			t.Errorf("GrantConsent with %s: err = %v, want ErrInvalidArgument", name, err)
		}
	}

	err := opened.GrantConsent(ctx, testUnknownID,
		client.ID, []string{testScope})
	if !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("GrantConsent for an unknown principal: err = %v, want ErrPrincipalNotFound", err)
	}
}
