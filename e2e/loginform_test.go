//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// This file drives the remote browser login profile the real binary serves,
// with an http.Client rather than a headless browser: the disclosure page, the
// transaction cookie, the CSRF token that guards every step, and the safe
// failure of a credential submission.
//
// It stops at the credential form on purpose. Submitting credentials calls the
// real Garmin authenticator (internal/cmd/remotelogin.go), which this package
// must never let reach the real Garmin service. Reaching a genuine MFA
// continuation would require a completed login, which is exactly the step this
// package cannot take; see AGENTS.md/docs/implementation-status.md for that gap.

// loginSSOHost is the one authority every login strategy's JSON and widget
// flows address (internal/garmin/protocol/hosts.go: Hosts.sso for the default
// "garmin.com" domain). Every strategy in the fallback chain (mobile iOS, the
// SSO embed widget, the portal) posts or gets against this same host, only the
// path differs, so a recording proxy that captures the CONNECT authority alone
// is enough to prove which host, if any, a credential submission reached.
const loginSSOHost = "sso.garmin.com:443"

// csrfTokenPattern extracts the rotating form token every remote login page
// embeds. It is the one place a test reads state the server generated, because
// the token is never predictable from outside. It is a function rather than a
// package-level var per AGENTS.md's rule against package-level mutable state:
// a compiled *regexp.Regexp is not a value a const can hold.
func csrfTokenPattern() *regexp.Regexp {
	return regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
}

// extractCSRFToken pulls the form token out of a rendered page, failing the
// test if the page carries none.
func extractCSRFToken(t *testing.T, body string) string {
	t.Helper()

	match := csrfTokenPattern().FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no csrf_token field in the page body: %s", body)
	}
	return match[1]
}

// newBrowserClient is an http.Client that behaves like a browser against this
// deployment: it trusts the generated certificate, it keeps cookies across
// requests, and it stops at the first redirect so a test can inspect exactly
// what one hop returned rather than only where the chain ends.
func newBrowserClient(t *testing.T, server remoteServer) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("build a cookie jar: %v", err)
	}
	return &http.Client{
		Transport:     server.client.Transport,
		Timeout:       server.client.Timeout,
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// getBody performs a GET and returns its decoded body, closing the response
// body itself.
func getBody(t *testing.T, client *http.Client, target string) string {
	t.Helper()

	response, err := client.Get(target)
	if err != nil {
		t.Fatalf("get %s: %v", target, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the response body from %s: %v", target, err)
	}
	return string(body)
}

// postForm performs a POST with a form body and returns the response together
// with its decoded body, closing the response body itself.
func postForm(t *testing.T, client *http.Client, target string, form url.Values) (*http.Response, string) {
	t.Helper()

	response, err := client.PostForm(target, form)
	if err != nil {
		t.Fatalf("post %s: %v", target, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the response body from %s: %v", target, err)
	}
	return response, string(body)
}

// beginAuthorization opens a fresh transaction and returns the response to the
// unfollowed /authorize redirect, so a caller can inspect the cookie it set.
func beginAuthorization(t *testing.T, client *http.Client, server remoteServer, state string) *http.Response {
	t.Helper()

	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", remoteClientID)
	query.Set("redirect_uri", remoteRedirectURI)
	query.Set("scope", remoteScope)
	query.Set("state", state)
	query.Set("resource", server.mcpURL)
	query.Set("code_challenge_method", "S256")
	_, challenge := pkcePair(t)
	query.Set("code_challenge", challenge)

	response, err := client.Get(server.origin + "/authorize?" + query.Encode())
	if err != nil {
		t.Fatalf("get /authorize: %v", err)
	}
	return response
}

// TestRemoteLoginDisclosurePageSetsTheHostPrefixedCookie is the mutant this
// test catches: a build that dropped the __Host- prefix, the Secure or
// HttpOnly flag, set a Domain, or weakened SameSite would let a sibling host,
// a script, or a cross-site request read or ride the transaction cookie that
// is the sole capability addressing this login.
func TestRemoteLoginDisclosurePageSetsTheHostPrefixedCookie(t *testing.T) {
	server := startRemoteServer(t)
	browser := newBrowserClient(t, server)

	response := beginAuthorization(t, browser, server, "e2e-cookie-state")
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 to the disclosure page", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/login" {
		t.Fatalf("Location = %q, want /login", location)
	}

	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want exactly one", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != loginweb.RemoteCookieName {
		t.Errorf("cookie name = %q, want %q", cookie.Name, loginweb.RemoteCookieName)
	}
	if !strings.HasPrefix(cookie.Name, "__Host-") {
		t.Errorf("cookie name %q does not carry the __Host- prefix", cookie.Name)
	}
	if !cookie.Secure {
		t.Error("cookie is not Secure")
	}
	if !cookie.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if cookie.Path != "/" {
		t.Errorf("cookie Path = %q, want /", cookie.Path)
	}
	if cookie.Domain != "" {
		t.Errorf("cookie Domain = %q, want empty: __Host- forbids one", cookie.Domain)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", cookie.SameSite)
	}

	body := getBody(t, browser, server.origin+"/login")
	if !strings.Contains(body, "End-to-end client") {
		t.Errorf("disclosure page does not name the client: %s", body)
	}
	if !strings.Contains(body, remoteRedirectURI) {
		t.Errorf("disclosure page does not state the exact redirect URI: %s", body)
	}
}

// TestRemoteLoginRefusesAWrongCSRFTokenThenAcceptsTheRealOne is the mutant this
// test catches: a build that stopped comparing the submitted csrf_token, or
// compared it non-constant-time against nothing, would let a cross-site
// request forged against the disclosure page's known route advance a
// transaction the victim never confirmed.
func TestRemoteLoginRefusesAWrongCSRFTokenThenAcceptsTheRealOne(t *testing.T) {
	server := startRemoteServer(t)
	browser := newBrowserClient(t, server)

	opened := beginAuthorization(t, browser, server, "e2e-csrf-state")
	_ = opened.Body.Close()

	disclosureBody := getBody(t, browser, server.origin+"/login")
	realToken := extractCSRFToken(t, disclosureBody)

	wrong, wrongBody := postForm(t, browser, server.origin+"/login",
		url.Values{"csrf_token": {"not-the-real-token"}})
	if wrong.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong-token status = %d, want 404 (body %s)", wrong.StatusCode, wrongBody)
	}

	correct, correctBody := postForm(t, browser, server.origin+"/login",
		url.Values{"csrf_token": {realToken}})
	if correct.StatusCode != http.StatusSeeOther {
		t.Fatalf("correct-token status = %d, want 303 to the credential form (body %s)",
			correct.StatusCode, correctBody)
	}
	if location := correct.Header.Get("Location"); location != "/login/credentials" {
		t.Errorf("Location = %q, want /login/credentials", location)
	}
}

// TestRemoteLoginRefusesTheSameCSRFTokenReplayed is the mutant this test
// catches: a build whose token never rotated on an accepted submission would
// let a captured page (a shared proxy log, a browser history entry, a leaked
// referrer) be replayed to re-advance a transaction after the legitimate
// browser already moved past it.
func TestRemoteLoginRefusesTheSameCSRFTokenReplayed(t *testing.T) {
	server := startRemoteServer(t)
	browser := newBrowserClient(t, server)

	opened := beginAuthorization(t, browser, server, "e2e-csrf-replay-state")
	_ = opened.Body.Close()

	disclosureBody := getBody(t, browser, server.origin+"/login")
	realToken := extractCSRFToken(t, disclosureBody)

	first, firstBody := postForm(t, browser, server.origin+"/login", url.Values{"csrf_token": {realToken}})
	if first.StatusCode != http.StatusSeeOther {
		t.Fatalf("first use status = %d, want 303 (body %s)", first.StatusCode, firstBody)
	}

	replay, replayBody := postForm(t, browser, server.origin+"/login", url.Values{"csrf_token": {realToken}})
	if replay.StatusCode != http.StatusNotFound {
		t.Fatalf("replayed-token status = %d, want 404: the token must have rotated (body %s)",
			replay.StatusCode, replayBody)
	}
}

// TestRemoteLoginRefusesASessionsTokenPresentedUnderAnotherSessionsCookie is
// the mutant this test catches: a build that checked the submitted csrf_token
// against a single process-global value, rather than the token belonging to
// the exact session the request's own cookie addresses, would let one victim's
// captured form token advance a completely different browser's transaction.
func TestRemoteLoginRefusesASessionsTokenPresentedUnderAnotherSessionsCookie(t *testing.T) {
	server := startRemoteServer(t)

	browserA := newBrowserClient(t, server)
	openedA := beginAuthorization(t, browserA, server, "e2e-cross-session-a")
	_ = openedA.Body.Close()
	tokenA := extractCSRFToken(t, getBody(t, browserA, server.origin+"/login"))

	browserB := newBrowserClient(t, server)
	openedB := beginAuthorization(t, browserB, server, "e2e-cross-session-b")
	_ = openedB.Body.Close()

	// browserB's cookie jar addresses session B, but the form token presented is
	// session A's. If the check only compared against one shared value, this
	// would succeed; it must not, because the two transactions belong to
	// different browsers.
	cross, crossBody := postForm(t, browserB, server.origin+"/login", url.Values{"csrf_token": {tokenA}})
	if cross.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-session status = %d, want 404: session B must refuse session A's token (body %s)",
			cross.StatusCode, crossBody)
	}
}

// connectProxySeenTargets asserts that every authority proxy recorded from
// index skip onward is want, that at least one such connection was seen, and
// that none of them carried a credential header. skip excludes the anonymous
// start-up exercise-catalog read (e2e/exercisecatalog_test.go's catalogHost,
// connect.garmin.com), which this same recording proxy also observes and
// which is not part of what this test drives.
func connectProxySeenTargets(t *testing.T, proxy *connectProxy, want string, skip int) {
	t.Helper()

	allTargets, allHeaders := proxy.seen()
	if skip > len(allTargets) {
		skip = len(allTargets)
	}
	targets, headers := allTargets[skip:], allHeaders[skip:]
	if len(targets) == 0 {
		t.Fatal("the login attempt never asked to reach any host through the proxy")
	}
	for index, target := range targets {
		if target != want {
			t.Errorf("the login attempt asked to reach %q; the only permitted host is %q", target, want)
			continue
		}
		for _, header := range []string{"Proxy-Authorization", "Authorization", "Cookie"} {
			if value := headers[index].Get(header); value != "" {
				t.Errorf("the login connection to %q carried %s: %q", target, header, value)
			}
		}
	}
}

// TestRemoteLoginCredentialSubmissionReachesOnlyGarminSSOAndFailsSafely is the
// mutant this test catches: a build whose credential handler swallowed a
// network error from the Garmin authenticator as a success, advanced the
// transaction before Garmin actually answered, or simply never called the
// authenticator at all, would grant (or appear to grant) a login nobody's
// Garmin account approved.
//
// Earlier this test relied only on launchRemote's blackhole proxy, which
// proves the deployment cannot reach Garmin but not that it tried to: a build
// that deleted the authenticator.Login call outright would pass identically.
// Pointing this deployment at a recording, always-refusing CONNECT proxy
// (the same proxy e2e/exercisecatalog_test.go uses for the start-up catalog
// read) closes that gap: the proxy records the CONNECT authority of every
// tunnel attempt, so this test can assert the attempt was actually made,
// against exactly the Garmin SSO host, carrying no Authorization or Cookie
// header — turning "fails safely without reaching Garmin" from an assumption
// of the harness into a proven property of this login attempt.
func TestRemoteLoginCredentialSubmissionReachesOnlyGarminSSOAndFailsSafely(t *testing.T) {
	proxy := &connectProxy{}
	recorder := httptest.NewServer(proxy)
	t.Cleanup(recorder.Close)

	server := startRemoteServerConfigured(t, recorder.URL, nil)
	startupTargets, _ := proxy.seen()
	baseline := len(startupTargets)
	browser := newBrowserClient(t, server)

	opened := beginAuthorization(t, browser, server, "e2e-credentials-state")
	_ = opened.Body.Close()

	disclosureBody := getBody(t, browser, server.origin+"/login")
	continueResponse, continueBody := postForm(t, browser, server.origin+"/login",
		url.Values{"csrf_token": {extractCSRFToken(t, disclosureBody)}})
	if continueResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("continue status = %d, want 303 to the credential form (body %s)",
			continueResponse.StatusCode, continueBody)
	}
	if location := continueResponse.Header.Get("Location"); location != "/login/credentials" {
		t.Errorf("Location = %q, want /login/credentials", location)
	}

	credentialBody := getBody(t, browser, server.origin+"/login/credentials")
	submitToken := extractCSRFToken(t, credentialBody)

	submit, submitBody := postForm(t, browser, server.origin+"/login/credentials", url.Values{
		"csrf_token": {submitToken},
		"email":      {"e2e-login-form@example.test"},
		"password":   {"does-not-matter-never-reaches-garmin"},
	})
	if submit.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a login Garmin never confirmed (body %s)",
			submit.StatusCode, submitBody)
	}
	if location := submit.Header.Get("Location"); location != "" {
		t.Errorf("Location = %q, want no redirect: the transaction must not advance", location)
	}
	if !strings.Contains(submitBody, "did not accept those credentials") {
		t.Errorf("body does not report the rejection: %s", submitBody)
	}

	connectProxySeenTargets(t, proxy, loginSSOHost, baseline)
}
