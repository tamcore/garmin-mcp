package loginweb_test

import (
	"net/http"
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
// whose registered redirect URI is redirectURI, rather than the harness default,
// and returns the GET response that rendered the form together with its body.
func reachConsentWithRedirect(h *remoteHarness, clientID, redirectURI string) (*http.Response, string) {
	h.t.Helper()

	h.b.get(pathAuthorize + "?" + authorizeQueryWithRedirect(clientID, redirectURI).Encode())
	h.submitRemoteCredentials(h.continueToCredentials())
	return h.b.get(pathConsent)
}

// TestConsentFormCSPNamesTheRedirectOrigin covers the bug directly: form-action is
// enforced against the policy of the document that CONTAINS the Allow/Deny form —
// checked before the form's POST is sent, and re-checked on every redirect the
// resulting navigation takes — so the client's redirect origin must be on the GET
// response that renders that form, or Chrome blocks the eventual redirect and the
// code is never delivered. The POST response's own headers are never consulted for
// this check at all.
func TestConsentFormCSPNamesTheRedirectOrigin(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	getResp, _ := reachConsentWithRedirect(h, testClientID, testRedirectURI)

	assertBrowserHeaders(t, getResp)
	assertFormAction(t, getResp.Header.Get("Content-Security-Policy"), "'self'", testRedirectOrigin)
}

// TestConsentCSPOriginIsOriginOnly proves a registered redirect URI carrying a
// path, a non-default port and a query string yields exactly its origin — scheme,
// host and port — and nothing else, on the GET response that renders the form.
func TestConsentCSPOriginIsOriginOnly(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	getResp, _ := reachConsentWithRedirect(h, testClientID, testRedirectURIWithPathAndQuery)

	policy := getResp.Header.Get("Content-Security-Policy")
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
// per-transaction: one client's consent-form response never names another
// client's redirect origin.
func TestTwoTransactionsGetOnlyTheirOwnRedirectOrigin(t *testing.T) {
	first := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})
	second := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	firstResp, _ := reachConsentWithRedirect(first, testClientID, testRedirectURI)
	secondResp, _ := reachConsentWithRedirect(second, testOtherClient, testRedirectURIWithPathAndQuery)

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

// TestHostileRedirectFieldCannotInfluenceTheCSP drives the GET that renders the
// consent form with a forged redirect_uri query parameter — the header is set on
// this response, so this is the one a caller-supplied value could reach — and
// proves the response still names only the transaction's own registered origin.
func TestHostileRedirectFieldCannotInfluenceTheCSP(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})
	h.authorize()
	h.submitRemoteCredentials(h.continueToCredentials())

	resp, _ := h.b.get(pathConsent + "?redirect_uri=" + url.QueryEscape(testHostileOrigin+"/cb"))

	policy := resp.Header.Get("Content-Security-Policy")
	if strings.Contains(policy, testHostileOrigin) {
		t.Fatalf("Content-Security-Policy %q carries the caller-supplied origin", policy)
	}
	if !strings.Contains(policy, testRedirectOrigin) {
		t.Fatalf("Content-Security-Policy %q lacks the registered origin %q", policy, testRedirectOrigin)
	}
}

// assertFormAction checks that policy's form-action directive contains exactly
// wantSources, in any order, and no more — and that every non-keyword source is
// one a CSP3-enforcing browser can actually match against a URL. A source this
// server ever emits for form-action beyond the 'self' keyword is always an
// origin built by redirectOrigin: "scheme://host[:port]", read from an
// already-validated redirect URI. Checking token equality against the expected
// string is not enough on its own — a source can be an exact string match here
// and still be dead syntax no browser will ever apply, which is exactly how the
// IPv6-literal-host defect would have passed this helper.
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
	for _, got := range sources {
		if got == "'self'" {
			continue
		}
		if !isSyntacticHostSource(got) {
			t.Errorf("form-action source %q is not a syntactically valid CSP3 host-source; "+
				"a browser would drop it and match nothing, silently reproducing the original bug", got)
		}
	}
}

// isSyntacticHostSource reports whether src has the shape every non-keyword
// form-action source this server ever emits must have: an explicit scheme, a
// bracket-free host, an optional port, and nothing past it.
//
// This does not implement the whole CSP3 host-source grammar — in particular it
// does not check host-part character-by-character against `host-char = ALPHA /
// DIGIT / "-"`. It asserts the specific properties that separate a value this
// codebase could legitimately add from one that would silently fail to match in
// a CSP-enforcing browser: a scheme is present, the host carries no bracketed
// IPv6 literal (host-source has no bracket production at all), and there is no
// path, query, fragment or trailing slash — redirectOrigin strips all of those,
// so their presence here would itself be a regression.
func isSyntacticHostSource(src string) bool {
	scheme, rest, ok := strings.Cut(src, "://")
	if !ok || (scheme != "http" && scheme != "https") || rest == "" {
		return false
	}
	return !strings.ContainsAny(rest, "[]/?#")
}

// TestIsSyntacticHostSourceRejectsWhatCSPRejects proves the test helper itself
// would have caught the IPv6-literal defect: a bracketed IPv6 host fails this
// check even though it is byte-for-byte what redirectOrigin used to emit for
// such a redirect URI, and a plain hostname or IPv4 origin still passes.
func TestIsSyntacticHostSourceRejectsWhatCSPRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want bool
	}{
		{"https hostname", "https://client.example.test", true},
		{"https hostname with port", "https://client.example.test:8443", true},
		{"http IPv4 loopback with port", "http://127.0.0.1:53682", true},
		{"http IPv6 loopback literal", "http://[::1]:53682", false},
		{"https IPv6 literal, no port", "https://[2001:db8::1]", false},
		{"no scheme", "client.example.test", false},
		{"unsupported scheme", "ftp://client.example.test", false},
		{"carries a path", "https://client.example.test/cb", false},
		{"carries a query", "https://client.example.test?x=1", false},
		{"trailing slash", "https://client.example.test/", false},
		{"the 'self' keyword is not a host-source at all", "'self'", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isSyntacticHostSource(tc.src); got != tc.want {
				t.Errorf("isSyntacticHostSource(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}
