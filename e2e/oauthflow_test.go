//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// TestTokenEndpointRequiresTheMatchingPKCEVerifier is the mutant this test
// catches: a build that skipped, inverted or no-op'd CodeChallenge.Verify would
// accept a wrong or absent verifier and mint a token for whoever holds the
// leaked code alone, which defeats the entire point of PKCE.
//
// codeGrant consumes a code atomically before any binding is checked, so a
// build that returned 400 invalid_grant unconditionally — never actually
// verifying PKCE — would pass the negative cases below for the wrong reason.
// The positive control at the end rules that out: the identical fixture, with
// the correct verifier and a fresh code, must succeed.
//
// All three codes are seeded before the server process starts; see
// setUpOAuthFlow.
func TestTokenEndpointRequiresTheMatchingPKCEVerifier(t *testing.T) {
	fixture := setUpOAuthFlow(t, 3)
	verifier := fixture.verifier

	wrongVerifierCode := fixture.nextCode(t)
	noVerifierCode := fixture.nextCode(t)
	controlCode := fixture.nextCode(t)

	cases := map[string]struct {
		code     string
		verifier string
	}{
		"a wrong verifier": {wrongVerifierCode, "wrong-verifier-0123456789wrong-verifier-012"},
		"no verifier":      {noVerifierCode, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			form := tokenForm(tc.code, remoteRedirectURI, tc.verifier)

			response, body := postToken(t, fixture.server, form)
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for %s (error %s)",
					response.StatusCode, name, safeTokenFailure(body))
			}
			if failure := decodeTokenError(t, body); failure.Error != "invalid_grant" {
				t.Errorf("error = %q, want invalid_grant for %s", failure.Error, name)
			}
		})
	}

	t.Run("positive control: the correct verifier succeeds", func(t *testing.T) {
		form := tokenForm(controlCode, remoteRedirectURI, verifier)

		response, body := postToken(t, fixture.server, form)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 for the correct verifier (error %s)",
				response.StatusCode, safeTokenFailure(body))
		}
	})
}

// TestTokenEndpointRequiresTheExactRedirectURI is the mutant this test catches:
// a build that compared the presented redirect URI loosely (by prefix, by host
// only, or not at all) would let a code stolen from one client's callback be
// redeemed against an attacker's own redirect target.
//
// The positive control at the end is the same reasoning as the PKCE test
// above: an unconditional 400 invalid_grant would otherwise pass the negative
// assertion for the wrong reason, since a not-found or already-consumed code
// yields the identical response.
// Both codes are seeded before the server process starts; see setUpOAuthFlow.
func TestTokenEndpointRequiresTheExactRedirectURI(t *testing.T) {
	fixture := setUpOAuthFlow(t, 2)
	verifier := fixture.verifier

	mismatched := fixture.nextCode(t)
	matching := fixture.nextCode(t)

	form := tokenForm(mismatched, remoteRedirectURI+"-attacker", verifier)
	response, body := postToken(t, fixture.server, form)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a mismatched redirect URI (error %s)",
			response.StatusCode, safeTokenFailure(body))
	}
	if failure := decodeTokenError(t, body); failure.Error != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant for a mismatched redirect URI", failure.Error)
	}

	controlForm := tokenForm(matching, remoteRedirectURI, verifier)
	controlResponse, controlBody := postToken(t, fixture.server, controlForm)
	if controlResponse.StatusCode != http.StatusOK {
		t.Fatalf("positive control: status = %d, want 200 for the exact redirect URI (error %s)",
			controlResponse.StatusCode, safeTokenFailure(controlBody))
	}
}

// concurrentTokenAttempt is the outcome of one goroutine's redemption attempt.
type concurrentTokenAttempt struct {
	status       int
	body         []byte
	cacheControl string
}

// redeemConcurrently fires n simultaneous redemptions of the same form against
// server and returns each one's outcome, in no particular order. It never
// calls into *testing.T from a goroutine other than the caller's, because
// t.Fatalf is not safe to call from any other one.
func redeemConcurrently(t *testing.T, server remoteServer, form url.Values, n int) []concurrentTokenAttempt {
	t.Helper()

	attempts := make([]concurrentTokenAttempt, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range attempts {
		go func(i int) {
			defer wg.Done()
			response, err := server.client.PostForm(server.origin+"/token", form)
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = response.Body.Close() }()
			body, readErr := io.ReadAll(response.Body)
			if readErr != nil {
				errs[i] = readErr
				return
			}
			attempts[i] = concurrentTokenAttempt{
				status:       response.StatusCode,
				body:         body,
				cacheControl: response.Header.Get("Cache-Control"),
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent redemption %d: %v", i, err)
		}
	}
	return attempts
}

// TestTokenEndpointConsumesOneCodeAtomicallyUnderConcurrency is the mutant this
// test catches: a build whose code consumption is not atomic, or that marks a
// code used only after minting tokens rather than before, would let a captured
// code be redeemed twice and mint two independent token pairs from one
// authorization.
//
// A serial replay cannot distinguish atomic consumption from non-atomic
// consumption, nor "mark used before minting" from "mark used after minting":
// both shapes pass a request that waits for the first response before sending
// the second. Firing every redemption of one code at once is the property that
// actually needs proving, so this test does that.
//
// The surviving token is also asserted to work. An earlier version of this
// test deliberately did not assert that, because two concurrent redemptions of
// one code reliably produced a 200 whose access token the MCP endpoint then
// refused with invalid_token. That was traced to this package's own test
// harness rather than the product: the harness opened the SQLite database from
// the test process while the server subprocess held it open too, and a
// concurrent /token redemption under that two-writer arrangement produced a
// genuine disk I/O error (SQLITE_IOERR_SHORT_READ) on the server's own read
// path. With every code seeded before the server process starts and no write
// to the database after that (see setUpOAuthFlow and the note atop
// seed_test.go), the winning token authenticates reliably, and this assertion
// was restored.
func TestTokenEndpointConsumesOneCodeAtomicallyUnderConcurrency(t *testing.T) {
	const concurrentRedemptions = 8

	fixture := setUpOAuthFlow(t, 1)
	verifier := fixture.verifier
	code := fixture.nextCode(t)
	form := tokenForm(code, remoteRedirectURI, verifier)

	attempts := redeemConcurrently(t, fixture.server, form, concurrentRedemptions)

	var successes int
	var success tokenSuccessResponse
	for i, attempt := range attempts {
		switch attempt.status {
		case http.StatusOK:
			successes++
			if err := json.Unmarshal(attempt.body, &success); err != nil {
				t.Fatalf("decode redemption %d's token response: %v", i, err)
			}
			if attempt.cacheControl != "no-store" {
				t.Errorf("redemption %d Cache-Control = %q, want no-store on a token response",
					i, attempt.cacheControl)
			}
		case http.StatusBadRequest:
			if failure := decodeTokenError(t, attempt.body); failure.Error != "invalid_grant" {
				t.Errorf("redemption %d error = %q, want invalid_grant", i, failure.Error)
			}
		default:
			t.Errorf("redemption %d status = %d, want 200 or 400", i, attempt.status)
		}
	}
	if successes != 1 {
		t.Fatalf("%d of %d concurrent redemptions of one code succeeded, want exactly 1: "+
			"code consumption is not atomic", successes, concurrentRedemptions)
	}
	if success.AccessToken == "" {
		t.Fatal("access_token is empty")
	}
	if success.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", success.TokenType)
	}

	mcpResponse := postStatefulInitialize(t, fixture.server, success.AccessToken)
	defer func() { _ = mcpResponse.Body.Close() }()
	if mcpResponse.StatusCode != http.StatusOK {
		t.Errorf("MCP initialize with the surviving concurrent redemption's token: status = %d, want 200",
			mcpResponse.StatusCode)
	}
}

// TestTokenEndpointIssuedTokenAuthenticatesAnMCPRequest is the mutant this test
// catches: a build that broke the wiring between the authorization server and
// the MCP bearer resolver, so a token this endpoint issues is refused by the
// endpoint it was issued for. One redemption, no concurrency — see the note on
// TestTokenEndpointConsumesOneCodeAtomicallyUnderConcurrency for why the two
// properties are tested separately.
func TestTokenEndpointIssuedTokenAuthenticatesAnMCPRequest(t *testing.T) {
	fixture := setUpOAuthFlow(t, 1)
	verifier := fixture.verifier
	code := fixture.nextCode(t)

	success := redeemForToken(t, fixture.server, tokenForm(code, remoteRedirectURI, verifier))

	mcpResponse := postStatefulInitialize(t, fixture.server, success.AccessToken)
	defer func() { _ = mcpResponse.Body.Close() }()
	if mcpResponse.StatusCode != http.StatusOK {
		t.Errorf("MCP initialize with the issued token: status = %d, want 200",
			mcpResponse.StatusCode)
	}
}

// TestAuthorizeEndpointRefusesAPlainPKCEMethod is the mutant this test catches:
// a build that accepted "plain" alongside "S256", or that stopped validating
// the method at all, would let an attacker who can read the authorization
// request (a referrer leak, a shared proxy log) replay the code without ever
// needing the verifier, which is exactly what PKCE exists to prevent. It runs
// at /authorize, before any login, so it needs no seeded state.
func TestAuthorizeEndpointRefusesAPlainPKCEMethod(t *testing.T) {
	server := startRemoteServer(t)
	_, challenge := pkcePair(t)

	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", remoteClientID)
	query.Set("redirect_uri", remoteRedirectURI)
	query.Set("scope", remoteScope)
	query.Set("state", "e2e-plain-pkce-state")
	query.Set("resource", server.mcpURL)
	query.Set("code_challenge_method", "plain")
	query.Set("code_challenge", challenge)

	noRedirect := &http.Client{
		Transport:     server.client.Transport,
		Timeout:       server.client.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := noRedirect.Get(server.origin + "/authorize?" + query.Encode())
	if err != nil {
		t.Fatalf("get /authorize: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirecting back to the client", response.StatusCode)
	}
	location := response.Header.Get("Location")
	if !strings.HasPrefix(location, remoteRedirectURI) {
		t.Fatalf("Location = %q, want it to start with the registered redirect URI", location)
	}
	redirected, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse the redirect location %q: %v", location, err)
	}
	if got := redirected.Query().Get("error"); got != "invalid_request" {
		t.Errorf("error = %q, want invalid_request for a plain PKCE method", got)
	}
	if got := redirected.Query().Get("state"); got != "e2e-plain-pkce-state" {
		t.Errorf("state = %q, want the client's own state echoed back", got)
	}
}
