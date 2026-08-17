//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// This file drives the OAuth authorization-code grant against the real binary,
// over real HTTPS, exactly as a client would. remote_test.go already covers an
// unauthenticated /authorize/MCP caller; this file goes further and completes
// a grant.
//
// One step is deliberately not driven over HTTP: granting consent requires a
// completed Garmin login, and this package must never let a subprocess reach
// the real Garmin service. Seeding rather than redirecting the binary's own
// traffic is a cost and platform choice, not a claim that redirecting is
// impossible — see e2e/seed_test.go for the working seam and why it is not
// used here. The authorization code that a granted consent would have issued
// is seeded directly into the database instead, with the exact bindings
// BeginAuthorization and GrantConsent would have produced. Everything
// downstream of that point — PKCE verification, the exact redirect-URI match,
// the single-use code, and token issuance — runs through the real HTTP /token
// endpoint of the real process, and the token it returns is then presented to
// the real MCP endpoint.

// remoteRedirectURI is the exact redirect URI writeRemoteConfig registers for
// remoteClientID. Both the config and every seeded code must name the
// identical string for the exact-match check to have anything to prove.
const remoteRedirectURI = "http://127.0.0.1:33418/callback"

// tokenErrorResponse is the RFC 6749 §5.2 shape.
type tokenErrorResponse struct {
	Error string `json:"error"`
}

// tokenSuccessResponse is the RFC 6749 §5.1 shape this test needs.
type tokenSuccessResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// postToken submits a token request and returns the raw response and body, so
// a caller can assert on the status, the headers and the decoded JSON.
func postToken(t *testing.T, server remoteServer, form url.Values) (*http.Response, []byte) {
	t.Helper()

	response, err := server.client.PostForm(server.origin+"/token", form)
	if err != nil {
		t.Fatalf("post to the token endpoint: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the token response: %v", err)
	}
	return response, body
}

// oauthFlowFixture is one running deployment, the seeded principal every
// sub-test's codes are bound to, and the pool of pre-seeded, single-use
// authorization codes setUpOAuthFlow minted before the server started.
type oauthFlowFixture struct {
	server      remoteServer
	principalID string
	verifier    string
	challenge   string
	codes       []string
}

// redeemForToken redeems a code once and returns the decoded success response,
// failing the test on any non-200 or an empty token. It exists so a test whose
// subject is what a valid token can do does not restate the redemption itself.
func redeemForToken(t *testing.T, server remoteServer, form url.Values) tokenSuccessResponse {
	t.Helper()

	response, body := postToken(t, server, form)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("redeem the code: status = %d, want 200 (%s)",
			response.StatusCode, safeTokenFailure(body))
	}

	var success tokenSuccessResponse
	if err := json.Unmarshal(body, &success); err != nil {
		t.Fatalf("decode the token response: %v", err)
	}
	if success.AccessToken == "" {
		t.Fatal("access_token is empty")
	}
	if success.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", success.TokenType)
	}
	return success
}

// setUpOAuthFlow starts a deployment, seeds one principal, and pre-seeds n
// fresh, single-use authorization codes, all bound to one freshly generated
// PKCE challenge. The principal stands in for a Garmin login this package
// must never attempt.
//
// n must cover every redemption the caller will go on to make, including
// every concurrent one: every code is minted here, before the server process
// starts, because seeding a code after launch reopens the two-writer defect
// this package no longer has (see the note at the top of seed_test.go). A
// caller that needs more codes than it first estimated is undercounting, not
// hitting a real limit — raise n.
func setUpOAuthFlow(t *testing.T, n int) oauthFlowFixture {
	t.Helper()

	verifier, challenge := pkcePair(t)
	var principalID string
	var codes []string
	server := startRemoteServerSeeded(t, func(seedDir, origin string) {
		sqlite := openSeedStore(t, seedDir)
		defer func() { _ = sqlite.Close() }()

		seedClient(t, sqlite)
		principalID = seedPrincipal(t, sqlite, "e2e-oauth@example.test")
		params := seedAuthCodeParams{
			principalID: principalID,
			clientID:    remoteClientID,
			redirectURI: remoteRedirectURI,
			resource:    mcpURLFor(origin),
			scopes:      []string{remoteScope},
			challenge:   challenge,
		}
		seedConsent(t, sqlite, params)
		for i := 0; i < n; i++ {
			codes = append(codes, seedAuthCode(t, sqlite, params))
		}
	})
	return oauthFlowFixture{
		server:      server,
		principalID: principalID,
		verifier:    verifier,
		challenge:   challenge,
		codes:       codes,
	}
}

// nextCode pops one pre-seeded, single-use authorization code from the pool
// setUpOAuthFlow minted before launch. It fails the test rather than seeding
// one on demand: seeding after launch is the two-writer defect this package
// exists to avoid, so a caller that runs out asked setUpOAuthFlow for too few.
func (f *oauthFlowFixture) nextCode(t *testing.T) string {
	t.Helper()

	if len(f.codes) == 0 {
		t.Fatal("no pre-seeded authorization codes left: increase setUpOAuthFlow's n")
	}
	code := f.codes[0]
	f.codes = f.codes[1:]
	return code
}

// tokenForm builds the standard authorization_code grant request, so each
// sub-test only overrides what it is testing.
func tokenForm(code, redirectURI, verifier string) url.Values {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", remoteClientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	return form
}

// statefulInitializeRequest is an MCP initialize call at a protocol version
// this deployment actually completes in stateful mode.
//
// remote_test.go's newInitializeRequest deliberately names the newest
// protocol version, "2026-07-28", to probe the authorization boundary: every
// existing use of it is refused for a missing, misplaced or invalid bearer
// token before the server ever reads the body, so the version never matters
// there. It does matter here, because this file's requests carry a token the
// server actually accepts and lets through to protocol negotiation — and the
// go-sdk server refuses "2026-07-28" itself on anything but a stateless
// deployment (mcp.StreamableHTTPOptions.Stateless), which this one is not.
// "2025-11-25" is the newest version a stateful deployment completes, so a
// genuinely authenticated call uses that one instead.
func statefulInitializeRequest(t *testing.T, url string) *http.Request {
	t.Helper()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-11-25","capabilities":{},` +
		`"clientInfo":{"name":"garmin-mcp-e2e","version":"0.0.0"}}}`

	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the MCP request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	return request
}

// postStatefulInitialize sends an authenticated initialize call this
// deployment can actually complete. See statefulInitializeRequest for why it
// exists alongside remote_test.go's postInitialize.
func postStatefulInitialize(t *testing.T, server remoteServer, token string) *http.Response {
	t.Helper()

	request := statefulInitializeRequest(t, server.mcpURL)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.client.Do(request)
	if err != nil {
		t.Fatalf("send the authenticated MCP request: %v", err)
	}
	return response
}

// safeTokenFailure reports only the RFC 6749 error code from a failure body,
// never the raw body: a failure message must not echo a body that turns out to
// carry a secret-bearing success payload if the caller's status assumption was
// itself wrong (AGENTS.md: secrets must not be printable).
func safeTokenFailure(body []byte) string {
	var failure tokenErrorResponse
	if err := json.Unmarshal(body, &failure); err != nil || failure.Error == "" {
		return "(unparseable error body)"
	}
	return failure.Error
}

// decodeTokenError decodes a failure body, failing the test on malformed JSON.
func decodeTokenError(t *testing.T, body []byte) tokenErrorResponse {
	t.Helper()

	var failure tokenErrorResponse
	if err := json.Unmarshal(body, &failure); err != nil {
		t.Fatalf("decode the failure body %s: %v", body, err)
	}
	return failure
}
