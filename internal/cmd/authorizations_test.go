package cmd

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// The client's half of one PKCE exchange, and the opaque state it expects back
// byte for byte. The verifier is synthetic and authorizes nothing.
const (
	testVerifier    = "synthetic-pkce-verifier-0000000000000000000000000000"
	testClientState = "client-state-0001"
)

// authorizeQuery is one valid authorization request for the registered client.
func authorizeQuery(clientID string) url.Values {
	digest := sha256.Sum256([]byte(testVerifier))
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {remoteRedirectURI},
		"scope":                 {remoteScope},
		"state":                 {testClientState},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(digest[:])},
		"code_challenge_method": {"S256"},
		"resource":              {remotePublicURL},
	}
}

// newTestAuthorizations builds the adapter over the assembled deployment, which is
// the real authorization server over the real SQLite adapter. The point of these
// tests is the wiring, so nothing here is a fake.
func newTestAuthorizations(t *testing.T, remote *remoteDeployment) *authorizations {
	t.Helper()

	grants, err := newAuthorizations(remote.oauth, remote.clients)
	if err != nil {
		t.Fatalf("newAuthorizations returned error: %v", err)
	}
	return grants
}

// TestAuthorizationTransactionRunsEndToEnd drives the sequence the browser pages
// drive: open a transaction, read back what the pages must disclose, bind the
// principal a login resolved, and grant consent. The redirect it ends at is the
// client's registered URI with its state echoed byte for byte.
func TestAuthorizationTransactionRunsEndToEnd(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	grants := newTestAuthorizations(t, remote)

	principal, err := remote.sqlite.CreatePrincipal(t.Context(), testLoginEmail)
	if err != nil {
		t.Fatalf("CreatePrincipal returned error: %v", err)
	}

	authorization, err := grants.Begin(t.Context(), authorizeQuery(remote.cfg.OAuthClients[0].ID))
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	if authorization.Capability == "" {
		t.Fatal("Begin returned no transaction capability")
	}
	if authorization.Disclosure.ClientName != "Example MCP client" {
		t.Errorf("disclosure names client %q", authorization.Disclosure.ClientName)
	}

	disclosure, err := grants.Disclose(t.Context(), authorization.Capability)
	if err != nil {
		t.Fatalf("Disclose returned error: %v", err)
	}
	if disclosure.RedirectURI != remoteRedirectURI || disclosure.RedirectHost != "client.example.test" {
		t.Errorf("disclosure = %+v, want the registered redirect URI and its host", disclosure)
	}
	if len(disclosure.Scopes) != 1 || disclosure.Scopes[0] != remoteScope {
		t.Errorf("disclosure scopes = %v, want the requested scope", disclosure.Scopes)
	}

	if err := grants.AttachPrincipal(t.Context(), authorization.Capability, principal.ID); err != nil {
		t.Fatalf("AttachPrincipal returned error: %v", err)
	}

	completion, err := grants.Grant(t.Context(), authorization.Capability)
	if err != nil {
		t.Fatalf("Grant returned error: %v", err)
	}
	if !strings.HasPrefix(completion.RedirectTo, remoteRedirectURI+"?") {
		t.Errorf("completion redirects to %q, want the registered redirect URI",
			completion.RedirectTo)
	}
	assertCompletionCarriesACode(t, completion.RedirectTo)

	// The transaction is terminal now, so the capability addresses nothing.
	if _, err := grants.Disclose(t.Context(), authorization.Capability); !errors.Is(
		err, loginweb.ErrNoTransaction) {
		t.Errorf("a consumed capability disclosed %v, want ErrNoTransaction", err)
	}
}

// assertCompletionCarriesACode checks the redirect the client is sent to.
func assertCompletionCarriesACode(t *testing.T, redirect string) {
	t.Helper()

	parsed, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("the completion redirect does not parse: %v", err)
	}
	query := parsed.Query()
	if query.Get("code") == "" {
		t.Error("the completion carries no authorization code")
	}
	if got := query.Get("state"); got != testClientState {
		t.Errorf("state = %q, want the client's own %q byte for byte", got, testClientState)
	}
}

// TestAuthorizationRefusesAnUnregisteredClient keeps the registry closed and keeps
// the refusal local: with no validated client there is no redirect URI that may be
// trusted with an error, so the page layer must render it here.
func TestAuthorizationRefusesAnUnregisteredClient(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	grants := newTestAuthorizations(t, remote)

	query := authorizeQuery(remote.cfg.OAuthClients[0].ID)
	query.Set("client_id", "not-registered")

	_, err := grants.Begin(t.Context(), query)
	if err == nil {
		t.Fatal("Begin accepted an unregistered client")
	}

	var refusal loginweb.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error %v is not a loginweb.Refusal, so the pages cannot deliver it", err)
	}
	if refusal.Location() != "" {
		t.Errorf("the refusal names redirect target %q before any client was validated",
			refusal.Location())
	}
}

// TestAuthorizationRefusesAScopeTheClientDidNotRegister proves the narrower of the
// two bounds is applied: the deployment advertises what its clients may hold, and a
// client may not exceed its own registration.
func TestAuthorizationRefusesAScopeTheClientDidNotRegister(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	grants := newTestAuthorizations(t, remote)

	query := authorizeQuery(remote.cfg.OAuthClients[0].ID)
	query.Set("scope", "garmin:destructive")

	if _, err := grants.Begin(t.Context(), query); err == nil {
		t.Fatal("Begin granted a scope the client never registered")
	}
}

// TestAuthorizationRefusesADuplicatedParameter covers the parameter an attacker
// adds: two redirect URIs mean two parsers can disagree, and the disagreement is
// the attacker's to choose.
func TestAuthorizationRefusesADuplicatedParameter(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	grants := newTestAuthorizations(t, remote)

	query := authorizeQuery(remote.cfg.OAuthClients[0].ID)
	query.Add("redirect_uri", "https://evil.example.test/callback")

	if _, err := grants.Begin(t.Context(), query); err == nil {
		t.Fatal("Begin accepted a duplicated redirect URI")
	}
}

// TestUnknownCapabilitiesAreIndistinguishable keeps discoverability out of the
// security boundary: nothing about a capability that addresses no transaction
// distinguishes it from one that addresses somebody else's.
func TestUnknownCapabilitiesAreIndistinguishable(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	grants := newTestAuthorizations(t, remote)

	for _, capability := range []string{"", "not-a-capability"} {
		if _, err := grants.Disclose(t.Context(), capability); !errors.Is(
			err, loginweb.ErrNoTransaction) {
			t.Errorf("Disclose(%q) = %v, want ErrNoTransaction", capability, err)
		}
		if _, err := grants.Grant(t.Context(), capability); !errors.Is(
			err, loginweb.ErrNoTransaction) {
			t.Errorf("Grant(%q) = %v, want ErrNoTransaction", capability, err)
		}
		if _, err := grants.Deny(t.Context(), capability); !errors.Is(
			err, loginweb.ErrNoTransaction) {
			t.Errorf("Deny(%q) = %v, want ErrNoTransaction", capability, err)
		}
		if err := grants.AttachPrincipal(t.Context(), capability, testLoginPrincipal); err == nil {
			t.Errorf("AttachPrincipal(%q) accepted an unknown capability", capability)
		}
	}
}

// TestAuthorizationRefusesAnUnusablePrincipal keeps the transaction from being
// bound to something that is not a principal, whatever the page layer passes.
func TestAuthorizationRefusesAnUnusablePrincipal(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	grants := newTestAuthorizations(t, remote)

	authorization, err := grants.Begin(t.Context(), authorizeQuery(remote.cfg.OAuthClients[0].ID))
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	if err := grants.AttachPrincipal(
		t.Context(), authorization.Capability, testLoginEmail); err == nil {
		t.Error("AttachPrincipal accepted an email address as a principal")
	}
}

// TestDenialEndsTheTransactionWithoutACode is the other terminal outcome: the user
// declined, so the client learns that and receives nothing else.
func TestDenialEndsTheTransactionWithoutACode(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	grants := newTestAuthorizations(t, remote)

	principal, err := remote.sqlite.CreatePrincipal(t.Context(), testLoginEmail)
	if err != nil {
		t.Fatalf("CreatePrincipal returned error: %v", err)
	}
	authorization, err := grants.Begin(t.Context(), authorizeQuery(remote.cfg.OAuthClients[0].ID))
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	if err := grants.AttachPrincipal(t.Context(), authorization.Capability, principal.ID); err != nil {
		t.Fatalf("AttachPrincipal returned error: %v", err)
	}

	completion, err := grants.Deny(t.Context(), authorization.Capability)
	if err != nil {
		t.Fatalf("Deny returned error: %v", err)
	}
	parsed, err := url.Parse(completion.RedirectTo)
	if err != nil {
		t.Fatalf("the denial redirect does not parse: %v", err)
	}
	if parsed.Query().Get("code") != "" {
		t.Error("a denied authorization issued a code")
	}
	if got := parsed.Query().Get("error"); got != "access_denied" {
		t.Errorf("error = %q, want access_denied", got)
	}
}

// TestConfiguredScopesAreTheUnionOfTheRegistry keeps the advertised bound and the
// registration bound from contradicting each other.
func TestConfiguredScopesAreTheUnionOfTheRegistry(t *testing.T) {
	cfg := remoteConfig(t)
	second := cfg.OAuthClients[0]
	second.ID = "second-client"
	second.Scopes = []string{remoteScope, "garmin:write"}
	cfg.OAuthClients = append(cfg.OAuthClients, second)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the two-client configuration does not validate: %v", err)
	}

	clients, err := newConfigClients(cfg)
	if err != nil {
		t.Fatalf("newConfigClients returned error: %v", err)
	}
	if scopes := strings.Fields(clients.Scopes()); len(scopes) != 2 {
		t.Fatalf("advertised scopes = %v, want the union of both registrations", scopes)
	}
}

// TestDuplicateClientRegistrationsAreRefused fails closed on a registry that would
// otherwise leave one of the two registrations unreachable.
func TestDuplicateClientRegistrationsAreRefused(t *testing.T) {
	cfg := remoteConfig(t)
	cfg.OAuthClients = append(cfg.OAuthClients, cfg.OAuthClients[0])

	if _, err := newConfigClients(cfg); err == nil {
		t.Fatal("newConfigClients accepted two registrations of one identifier")
	}
}

// TestAnInlineClientDigestIsAccepted keeps the inline digest usable where a
// future caller would reach it first, not only in configuration validation.
func TestAnInlineClientDigestIsAccepted(t *testing.T) {
	cfg := remoteConfig(t)
	cfg.OAuthClients[0].Public = false
	cfg.OAuthClients[0].SecretHash = config.NewSecret(
		"5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8")

	if _, err := newConfigClients(cfg); err != nil {
		t.Errorf("newConfigClients(inline digest) = %v, want it accepted", err)
	}
}
