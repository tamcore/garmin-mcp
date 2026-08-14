package mcpserver_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

func TestNewHTTPTransportRejectsMissingDependencies(t *testing.T) {
	authorizer := newFakeAuthorizer(t)
	server := newTestServer(t, httpDeps(t, authorizer))

	tests := map[string]struct {
		mutate func(*mcpserver.HTTPOptions)
		server *mcpserver.Server
		want   error
	}{
		"nil server": {
			mutate: func(*mcpserver.HTTPOptions) {},
			want:   mcpserver.ErrMissingDependency,
		},
		"nil authorizer": {
			mutate: func(o *mcpserver.HTTPOptions) { o.Authorizer = nil },
			server: server,
			want:   mcpserver.ErrMissingDependency,
		},
		"empty public URL": {
			mutate: func(o *mcpserver.HTTPOptions) { o.PublicURL = "" },
			server: server,
			want:   mcpserver.ErrInvalidHTTPOptions,
		},
		"relative public URL": {
			mutate: func(o *mcpserver.HTTPOptions) { o.PublicURL = mcpPath },
			server: server,
			want:   mcpserver.ErrInvalidHTTPOptions,
		},
		"public URL with a fragment": {
			mutate: func(o *mcpserver.HTTPOptions) { o.PublicURL = "https://mcp.example.test/mcp#x" },
			server: server,
			want:   mcpserver.ErrInvalidHTTPOptions,
		},
		"empty bind address": {
			mutate: func(o *mcpserver.HTTPOptions) { o.BindAddress = "" },
			server: server,
			want:   mcpserver.ErrInvalidHTTPOptions,
		},
		"origin with a path": {
			mutate: func(o *mcpserver.HTTPOptions) {
				o.AllowedOrigins = []string{"https://client.example.test/app"}
			},
			server: server,
			want:   mcpserver.ErrInvalidHTTPOptions,
		},
		"unparseable proxy CIDR": {
			mutate: func(o *mcpserver.HTTPOptions) { o.TrustedProxyCIDRs = []string{"not-a-cidr"} },
			server: server,
			want:   mcpserver.ErrInvalidHTTPOptions,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			opts := testHTTPOptions(authorizer)
			tc.mutate(&opts)

			// Act
			transport, err := mcpserver.NewHTTPTransport(tc.server, opts)

			// Assert
			if !errors.Is(err, tc.want) {
				t.Fatalf("NewHTTPTransport error = %v, want %v", err, tc.want)
			}
			if transport != nil {
				t.Fatalf("NewHTTPTransport returned a transport alongside an error")
			}
		})
	}
}

func TestNewHTTPTransportRefusesCleartextPublicBind(t *testing.T) {
	tests := map[string]struct {
		publicURL string
		bind      string
		override  bool
		wantErr   bool
	}{
		"cleartext public URL": {
			publicURL: cleartextURL, bind: "0.0.0.0:8080", wantErr: true,
		},
		"cleartext wildcard bind": {
			publicURL: cleartextURL, bind: ":8080", wantErr: true,
		},
		"cleartext loopback is development": {
			publicURL: "http://127.0.0.1:8080/mcp", bind: "127.0.0.1:8080",
		},
		"cleartext localhost is development": {
			publicURL: "http://localhost:8080/mcp", bind: "localhost:8080",
		},
		"explicit override permits it": {
			publicURL: cleartextURL, bind: "0.0.0.0:8080", override: true,
		},
		"TLS public bind": {publicURL: testPublicURL, bind: "0.0.0.0:8443"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			authorizer := newFakeAuthorizer(t)
			server := newTestServer(t, httpDeps(t, authorizer))
			opts := testHTTPOptions(authorizer)
			opts.PublicURL = tc.publicURL
			opts.BindAddress = tc.bind
			opts.AllowInsecureCleartext = tc.override

			// Act
			_, err := mcpserver.NewHTTPTransport(server, opts)

			// Assert
			if tc.wantErr && !errors.Is(err, mcpserver.ErrInsecureBind) {
				t.Fatalf("NewHTTPTransport error = %v, want ErrInsecureBind", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("NewHTTPTransport returned error: %v", err)
			}
		})
	}
}

func TestHTTPTransportChallengesAnUnauthenticatedRequest(t *testing.T) {
	// Arrange
	transport := newBoundTransport(t)

	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, testPublicURL, strings.NewReader(initializeBody()))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			recorder := httptest.NewRecorder()

			// Act
			transport.ServeHTTP(recorder, req)

			// Assert
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
			if challenge := recorder.Header().Get("WWW-Authenticate"); challenge == "" {
				t.Fatalf("no WWW-Authenticate header on a 401")
			}
		})
	}
}

func TestHTTPTransportIgnoresATokenOutsideTheAuthorizationHeader(t *testing.T) {
	// A credential in a URL lands in proxy logs and browser history, and a
	// cookie-borne one is reachable by a cross-site request. Neither may
	// authenticate anything: the request must read as unauthenticated.

	tests := map[string]func(*http.Request){
		"query parameter": func(r *http.Request) {
			r.URL.RawQuery = "access_token=" + tokenAlice
		},
		"cookie": func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "access_token", Value: tokenAlice})
		},
		"__Host- cookie": func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "__Host-session", Value: tokenAlice})
		},
		"non-standard header": func(r *http.Request) {
			r.Header.Set("X-Access-Token", tokenAlice)
		},
	}

	for name, carry := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			transport := newBoundTransport(t)
			req := mcpPOST(t, initializeBody(), "", "")
			carry(req)
			recorder := httptest.NewRecorder()

			// Act
			transport.ServeHTTP(recorder, req)

			// Assert
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: a token outside the Authorization header was honored",
					recorder.Code)
			}
			if sessionID := recorder.Header().Get("Mcp-Session-Id"); sessionID != "" {
				t.Fatalf("an unauthenticated request was given a session")
			}
		})
	}
}

func TestHTTPTransportAcceptsAnAuthenticatedInitialize(t *testing.T) {
	// Arrange
	transport := newBoundTransport(t)

	// Act
	sessionID := initSession(t, transport, tokenAlice)

	// Assert
	if sessionID == "" {
		t.Fatalf("no session id issued")
	}
}

func TestHTTPTransportServesProtectedResourceMetadataUnauthenticated(t *testing.T) {
	// RFC 9728 metadata is public by definition: an unauthenticated client has to
	// read it to learn where to authenticate.

	// Arrange
	transport := newBoundTransport(t)
	req := httptest.NewRequest(http.MethodGet,
		"https://mcp.example.test/.well-known/oauth-protected-resource", nil)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, req)

	// Assert
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), testResource) {
		t.Fatalf("metadata body %q does not name the resource", recorder.Body.String())
	}
}

func TestHTTPTransportIgnoresHostAndForwardedHeadersForRouting(t *testing.T) {
	// Nothing about the served document may be derived from a header a client
	// controls. A hostile Host or X-Forwarded-Host must change nothing.

	// Arrange
	transport := newBoundTransport(t)
	req := httptest.NewRequest(http.MethodGet,
		"https://mcp.example.test/.well-known/oauth-protected-resource", nil)
	req.Host = "evil.example.test"
	req.Header.Set("X-Forwarded-Host", "evil.example.test")
	req.Header.Set("X-Forwarded-Proto", "http")
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, req)

	// Assert
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if strings.Contains(body, "evil.example.test") {
		t.Fatalf("metadata body reflected a client-controlled header: %q", body)
	}
}

func TestHTTPTransportRoutesUnknownPathsToNotFound(t *testing.T) {
	// Arrange
	transport := newBoundTransport(t)
	req := httptest.NewRequest(http.MethodGet, "https://mcp.example.test/admin", nil)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, req)

	// Assert
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestHTTPTransportResolvesThePrincipalFromTheToken(t *testing.T) {
	// The handler must see the token's principal. Nothing else — not a header,
	// not a cookie, not the session id, not a tool argument — may change it.

	// Arrange
	transport := newBoundTransport(t)
	sessionID := initSession(t, transport, tokenAlice)

	req := mcpPOST(t, callToolBody(2), tokenAlice, sessionID)
	req.Header.Set("X-Principal-Id", principalBob)
	req.AddCookie(&http.Cookie{Name: "principal", Value: principalBob})
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, req)

	// Assert
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, body)
	}
	if !strings.Contains(body, principalAlice) {
		t.Fatalf("handler did not see the token principal; body = %q", body)
	}
	if strings.Contains(body, principalBob) {
		t.Fatalf("a header or cookie influenced the principal; body = %q", body)
	}
}

func TestHTTPOptionsNormalizePaths(t *testing.T) {
	// The metadata path defaults, tolerates a missing leading slash, and the MCP
	// path is whatever the public URL says — including the root.

	tests := map[string]struct {
		publicURL    string
		metadataPath string
		wantMCP      string
		wantMetadata string
	}{
		"defaults": {
			publicURL:    testPublicURL,
			wantMCP:      mcpPath,
			wantMetadata: mcpserver.DefaultResourceMetadataPath,
		},
		"public URL at the root": {
			publicURL:    "https://mcp.example.test",
			wantMCP:      "/",
			wantMetadata: mcpserver.DefaultResourceMetadataPath,
		},
		"metadata path without a leading slash": {
			publicURL:    testPublicURL,
			metadataPath: "prm",
			wantMCP:      mcpPath,
			wantMetadata: "/prm",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			authorizer := newFakeAuthorizer(t)
			opts := testHTTPOptions(authorizer)
			opts.PublicURL = tc.publicURL
			opts.ResourceMetadataPath = tc.metadataPath
			transport := newTestTransport(t, opts)

			// Act: the metadata route answers unauthenticated, the MCP route
			// challenges. That difference is what identifies each path.
			metadata := httptest.NewRecorder()
			transport.ServeHTTP(metadata, httptest.NewRequest(
				http.MethodGet, "https://mcp.example.test"+tc.wantMetadata, nil))

			endpoint := httptest.NewRecorder()
			transport.ServeHTTP(endpoint, httptest.NewRequest(
				http.MethodDelete, "https://mcp.example.test"+tc.wantMCP, nil))

			// Assert
			if metadata.Code != http.StatusOK {
				t.Fatalf("metadata path %q status = %d, want 200", tc.wantMetadata, metadata.Code)
			}
			if endpoint.Code != http.StatusUnauthorized {
				t.Fatalf("MCP path %q status = %d, want 401", tc.wantMCP, endpoint.Code)
			}
		})
	}
}

func TestNewHTTPTransportRejectsAPublicURLWithUserinfo(t *testing.T) {
	// Arrange
	authorizer := newFakeAuthorizer(t)
	server := newTestServer(t, httpDeps(t, authorizer))
	opts := testHTTPOptions(authorizer)
	opts.PublicURL = "https://user:pass@mcp.example.test/mcp"

	// Act
	_, err := mcpserver.NewHTTPTransport(server, opts)

	// Assert
	if !errors.Is(err, mcpserver.ErrInvalidHTTPOptions) {
		t.Fatalf("NewHTTPTransport error = %v, want ErrInvalidHTTPOptions", err)
	}
	if err != nil && strings.Contains(err.Error(), "pass") {
		t.Fatalf("the error reflected the embedded credential: %v", err)
	}
}
