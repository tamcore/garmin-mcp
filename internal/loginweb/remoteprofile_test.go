package loginweb_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// TestTheRemoteCookieIsAHostPrefixedSecureCookie pins the remote cookie profile:
// the __Host- prefix, Secure, HttpOnly, Path=/, no Domain, and a SameSite value a
// cross-site top-level navigation still delivers.
func TestTheRemoteCookieIsAHostPrefixedSecureCookie(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	resp := h.authorize()

	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("the authorization response set %d cookies, want exactly 1", len(cookies))
	}
	cookie := cookies[0]

	switch {
	case cookie.Name != loginweb.RemoteCookieName:
		t.Errorf("cookie name = %q, want %q", cookie.Name, loginweb.RemoteCookieName)
	case !strings.HasPrefix(cookie.Name, "__Host-"):
		t.Errorf("cookie name %q lacks the __Host- prefix", cookie.Name)
	case !cookie.Secure:
		t.Error("the remote cookie is not Secure")
	case !cookie.HttpOnly:
		t.Error("the remote cookie is not HttpOnly")
	case cookie.Domain != "":
		t.Errorf("the cookie names a domain (%q); __Host- forbids one", cookie.Domain)
	case cookie.Path != "/":
		t.Errorf("cookie path = %q, want /", cookie.Path)
	case cookie.MaxAge <= 0:
		t.Errorf("cookie MaxAge = %d, want a bounded lifetime", cookie.MaxAge)
	case cookie.SameSite != http.SameSiteLaxMode:
		t.Errorf("cookie SameSite = %v, want Lax", cookie.SameSite)
	}
}

// TestEveryRemoteResponseCarriesHSTSAndTheBrowserHeaders covers the header rule on
// every response, including the generic 404 and the stylesheet.
func TestEveryRemoteResponseCarriesHSTSAndTheBrowserHeaders(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})
	h.authorize()

	for _, path := range []string{pathLogin, pathCredentials, "/nothing-here", "/login/style.css"} {
		t.Run(path, func(t *testing.T) {
			resp, _ := h.b.get(path)

			assertBrowserHeaders(t, resp)
			hsts := resp.Header.Get("Strict-Transport-Security")
			if !strings.Contains(hsts, "max-age=") {
				t.Errorf("Strict-Transport-Security = %q, want a max-age", hsts)
			}
			if !strings.Contains(hsts, "includeSubDomains") {
				t.Errorf("Strict-Transport-Security = %q, want includeSubDomains", hsts)
			}
		})
	}
}

// assertBrowserHeaders checks the protections both profiles share.
func assertBrowserHeaders(t *testing.T, resp *http.Response) {
	t.Helper()

	for header, value := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
		"X-Frame-Options":        "DENY",
	} {
		if got := resp.Header.Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	policy := resp.Header.Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'none'", "frame-ancestors 'none'", "form-action 'self'",
		"base-uri 'none'", "style-src 'self'",
	} {
		if !strings.Contains(policy, directive) {
			t.Errorf("Content-Security-Policy %q lacks %q", policy, directive)
		}
	}
}

// TestBothProfilesCoexistWithoutSharingACookieOrAHeaderPolicy runs a loopback server
// and a remote server side by side and proves neither profile's cookie or header
// policy leaks into the other.
func TestBothProfilesCoexistWithoutSharingACookieOrAHeaderPolicy(t *testing.T) {
	loopback := newBrowser(t, newServer(t, &fakeAuthenticator{loginAttempt: succeeded()}).Handler())
	remote := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	loopbackResp, _ := loopback.get("/")
	remoteResp := remote.authorize()

	loopbackCookie := loopbackResp.Cookies()
	if len(loopbackCookie) != 1 || loopbackCookie[0].Name == loginweb.RemoteCookieName {
		t.Fatalf("the loopback profile set %d cookies, and must not use the remote name",
			len(loopbackCookie))
	}
	if loopbackCookie[0].Secure || strings.HasPrefix(loopbackCookie[0].Name, "__Host-") {
		t.Error("the remote cookie policy leaked into the loopback profile")
	}
	if got := loopbackResp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("the loopback profile sent HSTS (%q) on a plain-HTTP listener", got)
	}

	remoteCookie := remoteResp.Cookies()
	if len(remoteCookie) != 1 || !remoteCookie[0].Secure {
		t.Fatal("the loopback cookie policy leaked into the remote profile")
	}

	// Each profile still completes its own run with the other one live.
	if resp := submitCredentials(t, loopback); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("the loopback login stopped working: %d", resp.StatusCode)
	}
	page := remote.reachConsent()
	if resp := remote.decide(page, decisionAllow); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("the remote login stopped working: %d", resp.StatusCode)
	}
}

// TestTheRemoteCredentialPageNamesThisDeployment is the honesty requirement: the
// page says whose it is, that Garmin does not host it, and what it does with the
// credentials.
func TestTheRemoteCredentialPageNamesThisDeployment(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})
	h.authorize()

	form := h.continueToCredentials()

	for _, want := range []string{"not", "Garmin", "forward", "this one login"} {
		if !strings.Contains(form, want) {
			t.Errorf("the credential page does not say %q:\n%s", want, form)
		}
	}
	for _, want := range []string{
		`<label for="email"`, `<label for="password"`, `type="password"`,
		`autocomplete="username"`, `autocomplete="current-password"`, `method="post"`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("the credential form lacks %q:\n%s", want, form)
		}
	}
	for _, forbidden := range []string{schemeHTTP, schemeHTTPS + "cdn", "<script"} {
		if strings.Contains(form, forbidden) {
			t.Errorf("the credential page references %q", forbidden)
		}
	}
}
