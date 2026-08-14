package loginweb_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The synthetic authorization request. Nothing here is a real client, a real
// redirect target, or a real account.
const (
	testClientID     = "client-alpha"
	testOtherClient  = "client-beta"
	testRedirectURI  = "https://client.example.test/callback"
	testRedirectHost = "client.example.test"
	testResource     = "https://mcp.example.test/mcp"
	testScopeA       = "garmin:read:profile"
	testScopeB       = "garmin:read:activities"
	testState        = "opaque state/with&odd=bytes"
	testPrincipal    = "principal-01"
	testCodeParam    = "test-authorization-code"

	// wrongToken is a form token no page ever issued.
	wrongToken = "not-the-token"
)

// The fixed remote routes and the form vocabulary.
const (
	pathAuthorize   = "/authorize"
	pathLogin       = "/login"
	pathCredentials = "/login/credentials"
	pathMFA         = "/login/mfa"
	pathConsent     = "/login/consent"

	fieldDecision = "decision"
	decisionAllow = "allow"
	decisionDeny  = "deny"
)

// clientName is the operator-registered display name the disclosure names.
func clientName(clientID string) string { return "MCP Client " + clientID }

// authorizeQuery builds one authorization request for clientID.
func authorizeQuery(clientID string) url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {testRedirectURI},
		"scope":                 {testScopeA + " " + testScopeB},
		"state":                 {testState},
		"code_challenge":        {"synthetic-challenge"},
		"code_challenge_method": {"S256"},
		"resource":              {testResource},
	}
}

// remoteHarness is one remote profile under test, with its browser.
type remoteHarness struct {
	t      *testing.T
	authz  *fakeAuthorizations
	garmin *fakeAuthenticator
	clock  *testkit.FakeClock
	server *loginweb.RemoteServer
	b      *browser
}

func newRemote(t *testing.T, garmin *fakeAuthenticator) *remoteHarness {
	t.Helper()

	clock := testkit.NewFakeClock(time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	authz := newFakeAuthorizations(clock.Now)
	server, err := loginweb.NewRemote(loginweb.RemoteConfig{
		Authorizations: authz,
		Authenticator:  garmin,
		Now:            clock.Now,
	})
	if err != nil {
		t.Fatalf("loginweb.NewRemote returned error: %v", err)
	}
	return &remoteHarness{
		t: t, authz: authz, garmin: garmin, clock: clock, server: server,
		b: newBrowser(t, server.Handler()),
	}
}

// authorize starts one authorization request and returns the redirect response.
func (h *remoteHarness) authorize() *http.Response {
	h.t.Helper()

	resp, _ := h.b.get(pathAuthorize + "?" + authorizeQuery(testClientID).Encode())
	return resp
}

// continueToCredentials walks the disclosure page and returns the credential form.
func (h *remoteHarness) continueToCredentials() string {
	h.t.Helper()

	_, disclosure := h.b.get(pathLogin)
	h.b.post(pathLogin, url.Values{fieldCSRF: {csrfToken(h.t, disclosure)}})
	_, form := h.b.get(pathCredentials)
	return form
}

// submitRemoteCredentials posts the credential form once.
func (h *remoteHarness) submitRemoteCredentials(form string) *http.Response {
	h.t.Helper()

	resp, _ := h.b.post(pathCredentials, url.Values{
		fieldCSRF:     {csrfToken(h.t, form)},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	})
	return resp
}

// reachConsent runs the whole flow up to the consent page and returns it.
func (h *remoteHarness) reachConsent() string {
	h.t.Helper()

	h.authorize()
	h.submitRemoteCredentials(h.continueToCredentials())
	_, page := h.b.get(pathConsent)
	return page
}

// decide posts the consent decision.
func (h *remoteHarness) decide(page, decision string) *http.Response {
	h.t.Helper()

	resp, _ := h.b.post(pathConsent, url.Values{
		fieldCSRF:     {csrfToken(h.t, page)},
		fieldDecision: {decision},
	})
	return resp
}

func wantStatus(t *testing.T, resp *http.Response, want int, what string) {
	t.Helper()

	if resp.StatusCode != want {
		t.Fatalf("%s = %d, want %d", what, resp.StatusCode, want)
	}
}

// TestRemoteAuthorizationReachesTheClientRedirect is the whole flow with no MFA:
// authorize, disclose, credentials, consent, and back to the client.
func TestRemoteAuthorizationReachesTheClientRedirect(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	resp := h.authorize()
	wantStatus(t, resp, http.StatusSeeOther, "GET "+pathAuthorize)
	if got := resp.Header.Get("Location"); got != pathLogin {
		t.Fatalf("Location = %q, want %q", got, pathLogin)
	}

	_, disclosure := h.b.get(pathLogin)
	for _, want := range []string{clientName(testClientID), testRedirectHost, testScopeA, testScopeB} {
		if !strings.Contains(disclosure, want) {
			t.Errorf("the disclosure page does not name %q:\n%s", want, disclosure)
		}
	}

	page := h.reachConsent()
	if !strings.Contains(page, clientName(testClientID)) {
		t.Fatalf("the consent page does not name the client:\n%s", page)
	}

	resp = h.decide(page, decisionAllow)
	wantStatus(t, resp, http.StatusSeeOther, "POST "+pathConsent)

	want := testRedirectURI + "?code=" + testCodeParam + "&state=" + url.QueryEscape(testState)
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	attaches, grants, denials := h.authz.counts()
	if attaches != 1 || grants != 1 || denials != 0 {
		t.Errorf("attaches=%d grants=%d denials=%d, want 1/1/0", attaches, grants, denials)
	}
}

// TestRemoteMFAKeepsTheContinuationServerSide covers the second leg: the OTP form is
// separate, and the Garmin continuation capability never reaches the browser.
func TestRemoteMFAKeepsTheContinuationServerSide(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{
		loginAttempt: challenged(),
		mfaAttempt:   remoteSucceeded(),
	})

	h.authorize()
	resp := h.submitRemoteCredentials(h.continueToCredentials())
	if got := resp.Header.Get("Location"); got != pathMFA {
		t.Fatalf("Location = %q, want %q", got, pathMFA)
	}

	resp, mfaForm := h.b.get(pathMFA)
	wantStatus(t, resp, http.StatusOK, "GET "+pathMFA)
	if strings.Contains(mfaForm, testTxnID) {
		t.Error("the OTP page carries the Garmin continuation capability")
	}

	resp, _ = h.b.post(pathMFA, url.Values{
		fieldCSRF: {csrfToken(t, mfaForm)},
		fieldCode: {testCode},
	})
	wantStatus(t, resp, http.StatusSeeOther, "POST "+pathMFA)
	if got := resp.Header.Get("Location"); got != pathConsent {
		t.Errorf("Location = %q, want %q", got, pathConsent)
	}
	if h.garmin.mfaCalls != 1 || h.garmin.lastTxnID != testTxnID {
		t.Errorf("CompleteMFA calls=%d txn=%q, want 1 and the server-side capability",
			h.garmin.mfaCalls, h.garmin.lastTxnID)
	}
}

// TestRemoteCredentialsAreUsedOnceAndNeverRendered proves the credential rule: one
// Garmin call, and nothing submitted appears in a page.
func TestRemoteCredentialsAreUsedOnceAndNeverRendered(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	h.authorize()
	h.submitRemoteCredentials(h.continueToCredentials())
	_, consent := h.b.get(pathConsent)

	if h.garmin.logins != 1 {
		t.Errorf("Garmin login was called %d times, want 1", h.garmin.logins)
	}
	if h.garmin.lastEmail != testEmail || h.garmin.lastPass != testPassword {
		t.Error("the credentials did not reach the login call intact")
	}
	for _, forbidden := range []string{testEmail, testPassword} {
		if strings.Contains(consent, forbidden) {
			t.Errorf("a page renders a submitted credential:\n%s", consent)
		}
	}
}

// TestTheCapabilityNeverAppearsInAURLOrAPage keeps the transaction capability out of
// every path, query and body, because proxies and access logs capture those.
func TestTheCapabilityNeverAppearsInAURLOrAPage(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	resp := h.authorize()
	capability := capabilityCookie(resp)
	if capability == "" {
		t.Fatal("the authorization response set no capability cookie")
	}

	page := h.reachConsent()
	final := h.decide(page, decisionAllow)
	for _, seen := range []string{
		resp.Header.Get("Location"), page, final.Header.Get("Location"),
	} {
		if strings.Contains(seen, capability) {
			t.Errorf("the capability appears in %q", seen)
		}
	}
}

// TestConsentDenialDiscardsThePendingTokens covers the other half of the consent
// step: nothing is persisted, and the transaction is unusable afterwards.
func TestConsentDenialDiscardsThePendingTokens(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	page := h.reachConsent()
	resp := h.decide(page, decisionDeny)
	wantStatus(t, resp, http.StatusSeeOther, "POST "+pathConsent)
	if got := resp.Header.Get("Location"); !strings.Contains(got, "error=access_denied") {
		t.Errorf("Location = %q, want an access_denied redirect", got)
	}

	_, grants, denials := h.authz.counts()
	if grants != 0 || denials != 1 {
		t.Errorf("grants=%d denials=%d, want 0/1", grants, denials)
	}
	if resp, _ := h.b.get(pathConsent); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s after a denial = %d, want 404", pathConsent, resp.StatusCode)
	}
}

// remoteSucceeded is a Garmin login that resolved a principal.
func remoteSucceeded() loginweb.Attempt {
	return loginweb.Attempt{Strategy: "mobile_ios", Principal: testPrincipal}
}

// capabilityCookie returns the capability the response set, or "".
func capabilityCookie(resp *http.Response) string {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == loginweb.RemoteCookieName {
			return cookie.Value
		}
	}
	return ""
}
