package mcpserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
)

const (
	oauthIssuer      = "https://mcp.example.test"
	oauthMetadataURL = oauthIssuer + "/.well-known/oauth-protected-resource"
	liveToken        = "live-token"
	readScope        = oauthserver.Scope(scopeRead)
	writeScope       = oauthserver.Scope("garmin.write")
)

// tokenStore is an oauthserver.Store that only answers access-token lookups.
//
// The embedded interface supplies the rest of the method set. Nothing in these
// tests reaches those methods, and a nil call would panic loudly rather than
// silently succeed, which is the behavior a fake should have.
type tokenStore struct {
	oauthserver.Store
	tokens map[oauthserver.Lookup]oauthserver.AccessToken
}

func (s *tokenStore) AccessToken(_ context.Context, lookup oauthserver.Lookup) (
	oauthserver.AccessToken, error,
) {
	record, ok := s.tokens[lookup]
	if !ok {
		return oauthserver.AccessToken{}, oauthserver.ErrTokenNotFound
	}
	return record, nil
}

// newOAuthServer builds a real OAuth server holding one live access token.
func newOAuthServer(t *testing.T, secret, scopes string) *oauthserver.Server {
	t.Helper()

	scopeSet, err := oauthserver.ParseScopeSet(scopes)
	if err != nil {
		t.Fatalf("ParseScopeSet returned error: %v", err)
	}
	resource, err := oauthserver.ParseResource(testResource)
	if err != nil {
		t.Fatalf("ParseResource returned error: %v", err)
	}

	lookup := oauthserver.SecretFromString(secret).Lookup()
	store := &tokenStore{tokens: map[oauthserver.Lookup]oauthserver.AccessToken{
		lookup: {
			Lookup:    lookup,
			ClientID:  clientA,
			Principal: mustPrincipalID(t, principalAlice),
			Scopes:    scopeSet,
			Resource:  resource,
			Family:    familyAlice,
			IssuedAt:  time.Now().Add(-time.Minute),
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}}

	server, err := oauthserver.New(oauthserver.Config{
		Issuer:                oauthIssuer,
		Resource:              testResource,
		AuthorizationEndpoint: oauthIssuer + "/authorize",
		TokenEndpoint:         oauthIssuer + "/token",
		ResourceMetadataURL:   oauthMetadataURL,
		ResourceName:          "Garmin MCP",
		ScopesSupported:       "garmin.read garmin.write",
	}, oauthserver.Deps{Store: store})
	if err != nil {
		t.Fatalf("oauthserver.New returned error: %v", err)
	}
	return server
}

func mustOAuthAuthorizer(
	t *testing.T, server *oauthserver.Server, required ...oauthserver.Scope,
) *mcpserver.OAuthAuthorizer {
	t.Helper()

	authorizer, err := mcpserver.NewOAuthAuthorizer(server, required...)
	if err != nil {
		t.Fatalf("NewOAuthAuthorizer returned error: %v", err)
	}
	return authorizer
}

// oauthTransport is the common arrangement: a real OAuth server holding one live
// token, adapted and mounted behind the transport.
func oauthTransport(t *testing.T, required ...oauthserver.Scope) *mcpserver.HTTPTransport {
	t.Helper()

	authorizer := mustOAuthAuthorizer(t, newOAuthServer(t, liveToken, scopeRead), required...)
	return newTestTransport(t, testHTTPOptions(authorizer))
}

func TestNewOAuthAuthorizerRejectsANilServer(t *testing.T) {
	// Arrange, Act
	authorizer, err := mcpserver.NewOAuthAuthorizer(nil)

	// Assert
	if !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Fatalf("NewOAuthAuthorizer(nil) error = %v, want ErrMissingDependency", err)
	}
	if authorizer != nil {
		t.Fatalf("NewOAuthAuthorizer(nil) returned an authorizer")
	}
}

func TestOAuthAuthorizerDistinguishesMissingFromInvalidTokens(t *testing.T) {
	// RFC 6750 §3.1: a request that did not attempt authentication gets a bare
	// challenge, and one that presented an unusable credential gets
	// error="invalid_token". A client cannot tell "log in" from "log in again"
	// otherwise.

	tests := map[string]struct {
		authorization string
		wantError     string
	}{
		"no credential":      {},
		"empty bearer":       {authorization: "Bearer "},
		"another scheme":     {authorization: "Basic dXNlcjpwdw=="},
		"malformed value":    {authorization: "Bearer a b c"},
		"unknown credential": {authorization: "Bearer nope", wantError: `error="invalid_token"`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			transport := oauthTransport(t)
			req := mcpPOST(t, initializeBody(), "", "")
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}
			recorder := httptest.NewRecorder()

			// Act
			transport.ServeHTTP(recorder, req)

			// Assert
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
			challenge := recorder.Header().Get("WWW-Authenticate")
			if !strings.Contains(challenge, `resource_metadata="`+oauthMetadataURL+`"`) {
				t.Fatalf("challenge %q carries no resource_metadata", challenge)
			}
			if tc.wantError == "" && strings.Contains(challenge, "error=") {
				t.Fatalf("challenge %q reports an error for a request that presented nothing",
					challenge)
			}
			if tc.wantError != "" && !strings.Contains(challenge, tc.wantError) {
				t.Fatalf("challenge %q does not contain %q", challenge, tc.wantError)
			}
		})
	}
}

func TestOAuthAuthorizerNeverReflectsTheCredential(t *testing.T) {
	// Arrange
	const presented = "a-secret-token-value"
	transport := oauthTransport(t)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, mcpPOST(t, initializeBody(), presented, ""))

	// Assert
	rendered := recorder.Body.String() + recorder.Header().Get("WWW-Authenticate")
	if strings.Contains(rendered, presented) {
		t.Fatalf("the response reflected the presented credential: %q", rendered)
	}
}

func TestOAuthAuthorizerRefusesAnInsufficientScope(t *testing.T) {
	// Arrange: the live token carries only the read scope.
	transport := oauthTransport(t, writeScope)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, mcpPOST(t, initializeBody(), liveToken, ""))

	// Assert
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	challenge := recorder.Header().Get("WWW-Authenticate")
	if !strings.Contains(challenge, `error="insufficient_scope"`) {
		t.Fatalf("challenge %q does not report insufficient_scope", challenge)
	}
}

func TestOAuthAuthorizerAcceptsALiveToken(t *testing.T) {
	// Arrange
	transport := oauthTransport(t, readScope)

	// Act
	sessionID := initSession(t, transport, liveToken)

	// Assert
	if sessionID == "" {
		t.Fatalf("no session issued for a live token")
	}
}

func TestOAuthAuthorizerYieldsTheTokenPrincipal(t *testing.T) {
	// Arrange
	transport := oauthTransport(t)
	sessionID := initSession(t, transport, liveToken)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, mcpPOST(t, callToolBody(2), liveToken, sessionID))

	// Assert
	if !strings.Contains(recorder.Body.String(), principalAlice) {
		t.Fatalf("handler did not see the token principal; body = %q", recorder.Body.String())
	}
}

func TestOAuthAuthorizerServesProtectedResourceMetadata(t *testing.T) {
	// Arrange
	transport := oauthTransport(t)
	req := httptest.NewRequest(http.MethodGet, oauthMetadataURL, nil)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, req)

	// Assert
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	for _, want := range []string{testResource, oauthIssuer, "header"} {
		if !strings.Contains(body, want) {
			t.Fatalf("metadata %q does not contain %q", body, want)
		}
	}
}

func TestOAuthAuthorizerGrantRequiresTheMiddleware(t *testing.T) {
	// A handler wired without the middleware must refuse, not run unattributed.

	// Arrange
	authorizer := mustOAuthAuthorizer(t, newOAuthServer(t, liveToken, scopeRead))

	// Act
	_, grantErr := authorizer.Grant(t.Context())
	principal, principalErr := mcpserver.PrincipalSource(authorizer).PrincipalFromToken(t.Context())

	// Assert
	if grantErr == nil {
		t.Fatalf("Grant on a bare context returned no error")
	}
	if principalErr == nil {
		t.Fatalf("PrincipalFromToken on a bare context returned no error")
	}
	if principal.IsValid() {
		t.Fatalf("PrincipalFromToken returned a valid principal without a token")
	}
}

func TestOAuthAuthorizerSatisfiesTheSeams(t *testing.T) {
	// Arrange
	authorizer := mustOAuthAuthorizer(t, newOAuthServer(t, liveToken, scopeRead))

	// Assert: assignment to both interfaces is the assertion. The second is what
	// lets the composition root feed identity.NewBearerResolver from the same
	// authorizer the transport authenticates with.
	var httpAuthorizer mcpserver.HTTPAuthorizer = authorizer
	tokenSource := mcpserver.PrincipalSource(httpAuthorizer)
	if _, err := tokenSource.PrincipalFromToken(t.Context()); err == nil {
		t.Fatalf("the token source accepted a context with no verified token")
	}
}
