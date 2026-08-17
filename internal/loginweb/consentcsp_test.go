package loginweb_test

import (
	"net/url"
	"strings"
	"testing"
)

// The redirect origins used to prove the per-transaction CSP addition, kept
// distinct from testRedirectURI so a test that mixes them up is caught.
const (
	testRedirectURIWithPathAndQuery = "https://other-client.example.test:8443/cb?foo=bar"
	testRedirectOrigin              = "https://client.example.test"
	testOtherRedirectOrigin         = "https://other-client.example.test:8443"
	testHostileOrigin               = "https://evil.example.test"
)

// authorizeQueryWithRedirect is authorizeQuery for clientID with redirectURI
// substituted for the usual fixed one.
func authorizeQueryWithRedirect(clientID, redirectURI string) url.Values {
	q := authorizeQuery(clientID)
	q.Set("redirect_uri", redirectURI)
	return q
}

// reachConsentWithRedirect runs the flow to the consent page for a transaction
// whose registered redirect URI is redirectURI, rather than the harness default.
func reachConsentWithRedirect(h *remoteHarness, clientID, redirectURI string) string {
	h.t.Helper()

	h.b.get(pathAuthorize + "?" + authorizeQueryWithRedirect(clientID, redirectURI).Encode())
	h.submitRemoteCredentials(h.continueToCredentials())
	_, page := h.b.get(pathConsent)
	return page
}

// TestConsentApprovalCSPNamesTheRedirectOrigin covers the bug directly: the
// response that redirects the browser to the client's redirect URI must permit
// that origin as a form-action target, in addition to 'self', or Chrome blocks the
// redirect and the code is never delivered.
func TestConsentApprovalCSPNamesTheRedirectOrigin(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	page := h.reachConsent()
	resp := h.decide(page, decisionAllow)

	assertBrowserHeaders(t, resp)
	assertFormAction(t, resp.Header.Get("Content-Security-Policy"), "'self'", testRedirectOrigin)
}

// TestConsentDenialCSPNamesTheRedirectOrigin covers the other outcome: a denial
// also redirects to the client, with error=access_denied, so it needs the same
// treatment.
func TestConsentDenialCSPNamesTheRedirectOrigin(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	page := h.reachConsent()
	resp := h.decide(page, decisionDeny)

	assertBrowserHeaders(t, resp)
	assertFormAction(t, resp.Header.Get("Content-Security-Policy"), "'self'", testRedirectOrigin)
}

// TestConsentCSPOriginIsOriginOnly proves a registered redirect URI carrying a
// path, a non-default port and a query string yields exactly its origin — scheme,
// host and port — and nothing else.
func TestConsentCSPOriginIsOriginOnly(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	page := reachConsentWithRedirect(h, testClientID, testRedirectURIWithPathAndQuery)
	resp := h.decide(page, decisionAllow)

	policy := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(policy, testOtherRedirectOrigin) {
		t.Fatalf("Content-Security-Policy %q lacks the redirect origin %q", policy, testOtherRedirectOrigin)
	}
	for _, forbidden := range []string{"/cb", "foo=bar", "?"} {
		if strings.Contains(policy, forbidden) {
			t.Errorf("Content-Security-Policy %q carries more than the origin (%q)", policy, forbidden)
		}
	}
}

// TestTwoTransactionsGetOnlyTheirOwnRedirectOrigin proves the addition is
// per-transaction: one client's consent response never names another client's
// redirect origin.
func TestTwoTransactionsGetOnlyTheirOwnRedirectOrigin(t *testing.T) {
	first := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})
	second := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	firstPage := reachConsentWithRedirect(first, testClientID, testRedirectURI)
	firstResp := first.decide(firstPage, decisionAllow)

	secondPage := reachConsentWithRedirect(second, testOtherClient, testRedirectURIWithPathAndQuery)
	secondResp := second.decide(secondPage, decisionAllow)

	firstPolicy := firstResp.Header.Get("Content-Security-Policy")
	secondPolicy := secondResp.Header.Get("Content-Security-Policy")

	if !strings.Contains(firstPolicy, testRedirectOrigin) {
		t.Errorf("the first transaction's CSP %q lacks its own origin %q", firstPolicy, testRedirectOrigin)
	}
	if strings.Contains(firstPolicy, testOtherRedirectOrigin) {
		t.Errorf("the first transaction's CSP %q carries the other transaction's origin", firstPolicy)
	}
	if !strings.Contains(secondPolicy, testOtherRedirectOrigin) {
		t.Errorf("the second transaction's CSP %q lacks its own origin %q", secondPolicy, testOtherRedirectOrigin)
	}
	if strings.Contains(secondPolicy, testRedirectOrigin) {
		t.Errorf("the second transaction's CSP %q carries the first transaction's origin", secondPolicy)
	}
}

// TestOtherRemotePagesKeepTheConstantCSP proves the extension is scoped to the
// pages that actually redirect outward: every other page — including the
// disclosure, the credential form, the MFA form, and the generic 404 — keeps
// exactly today's constant policy, with no extra form-action source.
func TestOtherRemotePagesKeepTheConstantCSP(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: challenged()})
	h.authorize()
	h.continueToCredentials()

	const wantConstant = "default-src 'none'; style-src 'self'; form-action 'self'; " +
		"base-uri 'none'; frame-ancestors 'none'"

	for _, path := range []string{pathLogin, pathCredentials, pathNothingHere, pathStylesheet} {
		t.Run(path, func(t *testing.T) {
			resp, _ := h.b.get(path)
			policy := resp.Header.Get("Content-Security-Policy")
			if policy != wantConstant {
				t.Errorf("%s Content-Security-Policy = %q, want the unmodified constant", path, policy)
			}
		})
	}
}

// TestHostileRedirectFieldCannotInfluenceTheCSP drives the consent submission with
// a forged redirect_uri in both the query string and the form body — the two
// places a caller controls — and proves the response still names only the
// transaction's own registered origin.
func TestHostileRedirectFieldCannotInfluenceTheCSP(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})
	page := h.reachConsent()

	resp, _ := h.b.post(pathConsent+"?redirect_uri="+url.QueryEscape(testHostileOrigin), url.Values{
		fieldCSRF:      {csrfToken(t, page)},
		fieldDecision:  {decisionAllow},
		"redirect_uri": {testHostileOrigin + "/cb"},
	})

	policy := resp.Header.Get("Content-Security-Policy")
	if strings.Contains(policy, testHostileOrigin) {
		t.Fatalf("Content-Security-Policy %q carries the caller-supplied origin", policy)
	}
	if !strings.Contains(policy, testRedirectOrigin) {
		t.Fatalf("Content-Security-Policy %q lacks the registered origin %q", policy, testRedirectOrigin)
	}
}

// assertFormAction checks that policy's form-action directive contains exactly
// wantSources, in any order, and no more.
func assertFormAction(t *testing.T, policy string, wantSources ...string) {
	t.Helper()

	_, after, ok := strings.Cut(policy, "form-action ")
	if !ok {
		t.Fatalf("Content-Security-Policy %q has no form-action directive", policy)
	}
	rest := after
	end := strings.Index(rest, ";")
	if end == -1 {
		end = len(rest)
	}
	directive := strings.TrimSpace(rest[:end])
	sources := strings.Fields(directive)

	if len(sources) != len(wantSources) {
		t.Fatalf("form-action = %q, want exactly %v", directive, wantSources)
	}
	for _, want := range wantSources {
		found := false
		for _, got := range sources {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("form-action = %q, missing %q", directive, want)
		}
	}
}
