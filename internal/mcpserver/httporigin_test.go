package mcpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginAllowlistGovernsBrowserRequests(t *testing.T) {
	tests := map[string]struct {
		origin     string
		allowed    []string
		wantStatus int
		wantACAO   string
	}{
		"absent Origin is a standards-compliant non-browser client": {
			allowed: []string{testOrigin}, wantStatus: http.StatusOK,
		},
		"allowlisted Origin passes and is echoed": {
			origin: testOrigin, allowed: []string{testOrigin},
			wantStatus: http.StatusOK, wantACAO: testOrigin,
		},
		"unlisted Origin is refused": {
			origin: evilOrigin, allowed: []string{testOrigin},
			wantStatus: http.StatusForbidden,
		},
		"an empty allowlist denies every Origin": {
			origin: testOrigin, wantStatus: http.StatusForbidden,
		},
		"a null Origin is refused": {
			origin: "null", allowed: []string{testOrigin}, wantStatus: http.StatusForbidden,
		},
		"scheme is part of the match": {
			origin: "http://client.example.test", allowed: []string{testOrigin},
			wantStatus: http.StatusForbidden,
		},
		"port is part of the match": {
			origin: "https://client.example.test:8443", allowed: []string{testOrigin},
			wantStatus: http.StatusForbidden,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			authorizer := newFakeAuthorizer(t)
			opts := testHTTPOptions(authorizer)
			opts.AllowedOrigins = tc.allowed
			transport := newTestTransport(t, opts)

			req := mcpPOST(t, initializeBody(), tokenAlice, "")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			recorder := httptest.NewRecorder()

			// Act
			transport.ServeHTTP(recorder, req)

			// Assert
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)",
					recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != tc.wantACAO {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantACAO)
			}
		})
	}
}

func TestCORSDefaultsToDeny(t *testing.T) {
	// With no allowlist configured, no response may carry a CORS grant — not even
	// a successful one from a client that sent no Origin at all.

	// Arrange
	authorizer := newFakeAuthorizer(t)
	opts := testHTTPOptions(authorizer)
	opts.AllowedOrigins = nil
	transport := newTestTransport(t, opts)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, mcpPOST(t, initializeBody(), tokenAlice, ""))

	// Assert
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	for _, header := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Credentials",
		"Access-Control-Allow-Headers",
	} {
		if got := recorder.Header().Get(header); got != "" {
			t.Fatalf("%s = %q on a deny-by-default deployment", header, got)
		}
	}
}

func TestPreflightIsAnsweredOnlyForAnAllowlistedOrigin(t *testing.T) {
	tests := map[string]struct {
		origin     string
		wantStatus int
	}{
		"allowlisted": {origin: testOrigin, wantStatus: http.StatusNoContent},
		"unlisted":    {origin: evilOrigin, wantStatus: http.StatusForbidden},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			transport := newBoundTransport(t)
			req := httptest.NewRequest(http.MethodOptions, testPublicURL, nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Access-Control-Request-Method", http.MethodPost)
			recorder := httptest.NewRecorder()

			// Act
			transport.ServeHTTP(recorder, req)

			// Assert
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			if tc.wantStatus != http.StatusNoContent {
				return
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != tc.origin {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, tc.origin)
			}
			if got := recorder.Header().Get("Vary"); got == "" {
				t.Fatalf("no Vary header on a CORS response")
			}
		})
	}
}

func TestPreflightIsNeverAuthenticated(t *testing.T) {
	// A preflight carries no credentials by definition; answering it must not
	// require one, and answering it must not create a session.

	// Arrange
	transport := newBoundTransport(t)
	req := httptest.NewRequest(http.MethodOptions, testPublicURL, nil)
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, req)

	// Assert
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
	if recorder.Header().Get("Mcp-Session-Id") != "" {
		t.Fatalf("a preflight created a session")
	}
}

func TestClientIPTrustsForwardedHeadersOnlyFromAConfiguredProxy(t *testing.T) {
	// X-Forwarded-For is a hint from whoever is talking to us. It is worth
	// something only when that peer is a proxy the operator named.

	tests := map[string]struct {
		remoteAddr string
		forwarded  string
		trusted    []string
		want       string
	}{
		"untrusted peer keeps its own address": {
			remoteAddr: "203.0.113.9:5000", forwarded: forwardedClient,
			trusted: []string{proxyCIDR}, want: "203.0.113.9",
		},
		"trusted proxy is believed": {
			remoteAddr: proxyPeer, forwarded: forwardedClient,
			trusted: []string{proxyCIDR}, want: forwardedClient,
		},
		"no configured proxy means no trust": {
			remoteAddr: proxyPeer, forwarded: forwardedClient,
			want: proxyPeerIP,
		},
		"trusted proxies in the chain are skipped from the right": {
			remoteAddr: proxyPeer, forwarded: "198.51.100.7, 10.1.2.3",
			trusted: []string{proxyCIDR}, want: forwardedClient,
		},
		// The reason the walk goes right to left. A proxy APPENDS and preserves
		// what the client sent, so a caller who sets the header supplies the
		// left-hand entries. Reading the client-most one returns a string the
		// caller chose — and since this value keys the per-address rate-limit
		// budget on the unauthenticated OAuth endpoints, rotating the header
		// would mint a fresh budget per request.
		"a spoofed left-hand entry is ignored in favour of the appended one": {
			remoteAddr: proxyPeer, forwarded: "1.2.3.4, 198.51.100.7",
			trusted: []string{proxyCIDR}, want: forwardedClient,
		},
		"a chain of nothing but trusted proxies names no client": {
			remoteAddr: proxyPeer, forwarded: "10.9.9.9, 10.1.2.3",
			trusted: []string{proxyCIDR}, want: "10.1.2.3",
		},
		"a malformed forwarded value is discarded": {
			remoteAddr: proxyPeer, forwarded: "not-an-ip",
			trusted: []string{proxyCIDR}, want: "10.1.2.3",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			authorizer := newFakeAuthorizer(t)
			opts := testHTTPOptions(authorizer)
			opts.TrustedProxyCIDRs = tc.trusted
			transport := newTestTransport(t, opts)

			req := httptest.NewRequest(http.MethodGet, testPublicURL, nil)
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("X-Forwarded-For", tc.forwarded)

			// Act
			got := transport.ClientIP(req)

			// Assert
			if got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPublicURLIsNeverDerivedFromARequest(t *testing.T) {
	// Whatever a caller puts in Host or X-Forwarded-*, the canonical URL the
	// transport reports is the configured one.

	// Arrange
	transport := newBoundTransport(t)
	req := httptest.NewRequest(http.MethodGet, "http://evil.example.test/mcp", nil)
	req.Host = "evil.example.test"
	req.Header.Set("X-Forwarded-Host", "evil.example.test")
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("Forwarded", "host=evil.example.test;proto=http")
	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, req)

	// Act
	got := transport.PublicURL()

	// Assert
	if got != testPublicURL {
		t.Fatalf("PublicURL = %q, want %q", got, testPublicURL)
	}
}

func TestHTTPOptionsAreCopiedNotAliased(t *testing.T) {
	// A caller that keeps mutating its slices must not be able to widen the
	// allowlist of a transport that is already serving.

	// Arrange
	authorizer := newFakeAuthorizer(t)
	origins := []string{testOrigin}
	opts := testHTTPOptions(authorizer)
	opts.AllowedOrigins = origins
	transport := newTestTransport(t, opts)

	// Act
	origins[0] = evilOrigin
	req := mcpPOST(t, initializeBody(), tokenAlice, "")
	req.Header.Set("Origin", evilOrigin)
	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, req)

	// Assert
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: mutating the caller's slice widened the allowlist",
			recorder.Code)
	}
}
