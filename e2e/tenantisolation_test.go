//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// This file proves tenant isolation at the binary level: two principals, each
// with their own bearer token minted through the real /token endpoint; a proof
// that the two tokens actually resolve to two distinct principals, which is the
// property "tenant isolation" names; and a proof that revoking one principal's
// token never reaches the other's.
//
// Minting the two tokens uses the same seeded-authorization-code technique
// oauthflow_test.go uses and explains: a completed Garmin login is not
// reachable without contacting the real Garmin service, so the authorization
// code a granted consent would have issued is seeded directly, once per
// principal, and redeemed over real HTTP.

// twoPrincipalTokens is the fixture: a running deployment, the two seeded
// principal ids, and one real, working bearer token per principal.
type twoPrincipalTokens struct {
	server                 remoteServer
	stateDir               string
	principalA, principalB string
	tokenA, tokenB         string
}

// seededCode is one seeded authorization code, bound to one principal, plus
// the PKCE verifier the code's challenge requires at redemption.
type seededCode struct {
	principalID, code, verifier string
}

// seedTwoPrincipalsAndCodes seeds two distinct principals and one
// authorization code for each, all before the server process starts.
//
// seedClient installs the OAuth client row the server's own start-up
// reconciliation would otherwise install first (internal/cmd/remoteclients.go),
// so a code naming that client's id can satisfy its own foreign key before the
// process that would normally create the row has even started. See
// seed_test.go's seedClient and the note at the top of that file for why
// nothing here may be seeded after launch instead.
func seedTwoPrincipalsAndCodes(t *testing.T) (server remoteServer, dir string, a, b seededCode) {
	t.Helper()

	server = startRemoteServerSeeded(t, func(seedDir, origin string) {
		dir = seedDir
		sqlite := openSeedStore(t, seedDir)
		defer func() { _ = sqlite.Close() }()

		seedClient(t, sqlite)
		principalA := seedPrincipal(t, sqlite, "e2e-tenant-a@example.test")
		principalB := seedPrincipal(t, sqlite, "e2e-tenant-b@example.test")
		a = seedOneCode(t, sqlite, origin, principalA)
		b = seedOneCode(t, sqlite, origin, principalB)
	})
	return server, dir, a, b
}

// seedOneCode seeds one consent and one authorization code for principalID,
// bound to the MCP resource at origin.
func seedOneCode(t *testing.T, sqlite *store.SQLiteStore, origin, principalID string) seededCode {
	t.Helper()

	verifier, challenge := pkcePair(t)
	params := seedAuthCodeParams{
		principalID: principalID,
		clientID:    remoteClientID,
		redirectURI: remoteRedirectURI,
		resource:    mcpURLFor(origin),
		scopes:      []string{remoteScope},
		challenge:   challenge,
	}
	seedConsent(t, sqlite, params)
	code := seedAuthCode(t, sqlite, params)
	return seededCode{principalID: principalID, code: code, verifier: verifier}
}

// redeemCode exchanges a seeded code for a real access token over real HTTP.
func redeemCode(t *testing.T, server remoteServer, s seededCode) string {
	t.Helper()

	response, body := postToken(t, server, tokenForm(s.code, remoteRedirectURI, s.verifier))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mint a token for principal %s: status = %d, error = %s",
			s.principalID, response.StatusCode, safeTokenFailure(body))
	}
	var success tokenSuccessResponse
	if err := json.Unmarshal(body, &success); err != nil {
		t.Fatalf("decode the minted token for principal %s: %v", s.principalID, err)
	}
	if success.AccessToken == "" {
		t.Fatalf("minted token for principal %s is empty", s.principalID)
	}
	return success.AccessToken
}

// setUpTwoPrincipals seeds two distinct principals, mints one authorization
// code for each, and redeems both over real HTTP, so the two tokens this test
// compares are exactly what the running authorization server issued.
func setUpTwoPrincipals(t *testing.T) twoPrincipalTokens {
	t.Helper()

	server, dir, a, b := seedTwoPrincipalsAndCodes(t)
	return twoPrincipalTokens{
		server:     server,
		stateDir:   dir,
		principalA: a.principalID,
		principalB: b.principalID,
		tokenA:     redeemCode(t, server, a),
		tokenB:     redeemCode(t, server, b),
	}
}

// initializeStatus makes the MCP handshake call with the given bearer token
// and returns its status code.
func initializeStatus(t *testing.T, server remoteServer, token string) int {
	t.Helper()

	response := postStatefulInitialize(t, server, token)
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode
}

// revokeToken calls the real RFC 7009 revocation endpoint for a public client.
func revokeToken(t *testing.T, server remoteServer, token string) *http.Response {
	t.Helper()

	form := url.Values{}
	form.Set("client_id", remoteClientID)
	form.Set("token", token)
	response, err := server.client.PostForm(server.origin+"/revoke", form)
	if err != nil {
		t.Fatalf("post to the revocation endpoint: %v", err)
	}
	return response
}

// TestDistinctPrincipalsBackEachMintedToken is the mutant every other test in
// this file is blind to: a build that bound every issued token to a fixed
// principal — the first row in the principals table, rather than the seeded
// code's own code.Principal — would still pass every HTTP-level test here.
// Both tokens would still authenticate, and revoking one token's family would
// still leave the other's alone, because codeGrant mints a fresh family per
// exchange and revocation is family-keyed rather than principal-keyed. Every
// principal would share one Garmin identity and this file would stay green.
// What the other tests prove is revocation blast radius, which is real but
// narrower than tenant isolation.
//
// This test closes that gap directly: it stops the server, reopens the same
// database the server held (single-writer for the whole life of this
// package — see the note atop seed_test.go — so the reopen happens only once
// nothing else can write to it), and resolves both minted tokens through
// SQLiteStore.LookupAccessToken. storeLookupSecret applies the same
// oauthserver.Lookup conversion the running server's own oauthstore adapter
// applies before a token ever reaches the store, which is what an earlier
// attempt at this test got wrong: looking up the raw bearer token directly
// reports ErrTokenNotFound regardless of whether the server or this process
// wrote it, because the store never indexes a token under its raw bytes.
func TestDistinctPrincipalsBackEachMintedToken(t *testing.T) {
	fixture := setUpTwoPrincipals(t)
	fixture.server.stop()

	sqlite := openSeedStore(t, fixture.server.stateDir)
	defer func() { _ = sqlite.Close() }()

	grantA, err := sqlite.LookupAccessToken(context.Background(), storeLookupSecret(fixture.tokenA))
	if err != nil {
		t.Fatalf("look up principal A's token: %v", err)
	}
	grantB, err := sqlite.LookupAccessToken(context.Background(), storeLookupSecret(fixture.tokenB))
	if err != nil {
		t.Fatalf("look up principal B's token: %v", err)
	}

	if grantA.PrincipalID != fixture.principalA {
		t.Errorf("token A resolves to principal %q, want the seeded principal %q",
			grantA.PrincipalID, fixture.principalA)
	}
	if grantB.PrincipalID != fixture.principalB {
		t.Errorf("token B resolves to principal %q, want the seeded principal %q",
			grantB.PrincipalID, fixture.principalB)
	}
	if grantA.PrincipalID == grantB.PrincipalID {
		t.Fatalf("both tokens resolve to the same principal %q: tenant isolation is broken",
			grantA.PrincipalID)
	}
}

// TestTwoPrincipalsGetIndependentlyWorkingTokens is the mutant this test
// catches: a build that resolved the wrong principal from a seeded code — for
// example by keying a token off the client id or the resource rather than the
// principal the code was minted for — could still issue two tokens that both
// happen to authenticate, but would fail the revocation test below, which
// needs a working baseline to fail against. This test establishes that
// baseline: both tokens work before either is touched.
func TestTwoPrincipalsGetIndependentlyWorkingTokens(t *testing.T) {
	fixture := setUpTwoPrincipals(t)

	if status := initializeStatus(t, fixture.server, fixture.tokenA); status != http.StatusOK {
		t.Errorf("principal A initialize status = %d, want 200", status)
	}
	if status := initializeStatus(t, fixture.server, fixture.tokenB); status != http.StatusOK {
		t.Errorf("principal B initialize status = %d, want 200", status)
	}
}

// TestRevokingOnePrincipalsTokenNeverInvalidatesTheOthers is the mutant this
// test catches: a build whose revocation cascade revoked by the wrong key —
// the client id, the resource, or every family instead of the one token's own
// family — would take principal B's still-valid session down as collateral
// damage from an action principal A alone took on A's own token. That is
// exactly the cross-tenant blast radius tenant isolation exists to rule out.
func TestRevokingOnePrincipalsTokenNeverInvalidatesTheOthers(t *testing.T) {
	fixture := setUpTwoPrincipals(t)

	revoked := revokeToken(t, fixture.server, fixture.tokenA)
	defer func() { _ = revoked.Body.Close() }()
	if revoked.StatusCode != http.StatusOK {
		t.Fatalf("revoke principal A's token: status = %d, want 200", revoked.StatusCode)
	}

	afterA := postInitialize(t, fixture.server, map[string]string{"Authorization": "Bearer " + fixture.tokenA})
	defer func() { _ = afterA.Body.Close() }()
	if afterA.StatusCode != http.StatusUnauthorized {
		t.Errorf("principal A after its own revocation: status = %d, want 401", afterA.StatusCode)
	}
	if challenge := afterA.Header.Get("WWW-Authenticate"); !strings.Contains(challenge, `error="invalid_token"`) {
		t.Errorf("principal A challenge = %q, want error=\"invalid_token\"", challenge)
	}

	if status := initializeStatus(t, fixture.server, fixture.tokenB); status != http.StatusOK {
		t.Errorf("principal B after A's revocation: status = %d, want 200 (untouched)", status)
	}
}
