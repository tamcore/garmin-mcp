package oauthserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestProtectedResourceMetadataDescribesThisResource(t *testing.T) {
	h := newHarness(t)

	meta := h.srv.ProtectedResourceMetadata()

	if meta.Resource != testResourceURI {
		t.Fatalf("Resource = %q, want %q", meta.Resource, testResourceURI)
	}
	if !slices.Equal(meta.AuthorizationServers, []string{h.srv.Issuer()}) {
		t.Fatalf("AuthorizationServers = %v, want just the issuer", meta.AuthorizationServers)
	}
	if !slices.Equal(meta.BearerMethodsSupported, []string{"header"}) {
		t.Fatalf("BearerMethodsSupported = %v, want only header", meta.BearerMethodsSupported)
	}
	if !slices.Equal(meta.ScopesSupported, h.srv.ScopesSupported().Strings()) {
		t.Fatalf("ScopesSupported = %v", meta.ScopesSupported)
	}
	if meta.ResourceName != "garmin-mcp" {
		t.Fatalf("ResourceName = %q", meta.ResourceName)
	}
	if meta.JWKSURI != "" {
		t.Fatalf("JWKSURI = %q, want empty: this server issues opaque tokens", meta.JWKSURI)
	}
}

func TestAuthorizationServerMetadataAdvertisesOnlyWhatIsImplemented(t *testing.T) {
	h := newHarness(t)
	cfg := testConfig()

	meta := h.srv.AuthorizationServerMetadata()

	if meta.Issuer != h.srv.Issuer() {
		t.Fatalf("Issuer = %q", meta.Issuer)
	}
	if meta.AuthorizationEndpoint != cfg.AuthorizationEndpoint {
		t.Fatalf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
	}
	if meta.TokenEndpoint != cfg.TokenEndpoint {
		t.Fatalf("TokenEndpoint = %q", meta.TokenEndpoint)
	}
	if meta.RevocationEndpoint != cfg.RevocationEndpoint {
		t.Fatalf("RevocationEndpoint = %q", meta.RevocationEndpoint)
	}
	if !slices.Equal(meta.ResponseTypesSupported, []string{paramCode}) {
		t.Fatalf("ResponseTypesSupported = %v, want only code", meta.ResponseTypesSupported)
	}
	wantGrants := []string{GrantAuthorizationCode, GrantRefreshToken}
	if !slices.Equal(meta.GrantTypesSupported, wantGrants) {
		t.Fatalf("GrantTypesSupported = %v, want %v", meta.GrantTypesSupported, wantGrants)
	}
	if !slices.Equal(meta.CodeChallengeMethodsSupported, []string{string(MethodS256)}) {
		t.Fatalf("CodeChallengeMethodsSupported = %v, want only S256",
			meta.CodeChallengeMethodsSupported)
	}
	if meta.RegistrationEndpoint != "" {
		t.Fatalf("RegistrationEndpoint = %q, want empty: no dynamic registration is offered",
			meta.RegistrationEndpoint)
	}
	if meta.JWKSURI != "" {
		t.Fatalf("JWKSURI = %q, want empty", meta.JWKSURI)
	}
	wantMethods := []string{
		string(AuthMethodNone), string(AuthMethodSecretBasic), string(AuthMethodSecretPost),
	}
	if !slices.Equal(meta.TokenEndpointAuthMethodsSupported, wantMethods) {
		t.Fatalf("TokenEndpointAuthMethodsSupported = %v, want %v",
			meta.TokenEndpointAuthMethodsSupported, wantMethods)
	}
}

func serveMetadata(handler http.Handler, method string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, "/.well-known/metadata", nil))
	return rec
}

func TestMetadataHandlersServeJSONWithoutSensitiveFields(t *testing.T) {
	h := newHarness(t)

	for name, handler := range map[string]http.Handler{
		"protected resource":   h.srv.ProtectedResourceMetadataHandler(),
		"authorization server": h.srv.AuthorizationServerMetadataHandler(),
	} {
		t.Run(name, func(t *testing.T) {
			rec := serveMetadata(handler, http.MethodGet)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q", got)
			}
			if rec.Header().Get("Cache-Control") == "" {
				t.Fatal("no Cache-Control on a document clients are told to cache")
			}
			var document map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
				t.Fatalf("the document is not JSON: %v", err)
			}
			// An empty jwks_uri would send a client to fetch "", which is worse than
			// absent. This server issues opaque tokens, so the key must not appear.
			if _, present := document["jwks_uri"]; present {
				t.Fatalf("the document carries a jwks_uri: %s", rec.Body.String())
			}
			if _, present := document["registration_endpoint"]; present {
				t.Fatalf("the document advertises dynamic registration: %s", rec.Body.String())
			}
		})
	}
}

func TestMetadataHandlersRefuseOtherMethods(t *testing.T) {
	h := newHarness(t)

	for name, handler := range map[string]http.Handler{
		"protected resource":   h.srv.ProtectedResourceMetadataHandler(),
		"authorization server": h.srv.AuthorizationServerMetadataHandler(),
	} {
		t.Run(name, func(t *testing.T) {
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
				if rec := serveMetadata(handler, method); rec.Code != http.StatusMethodNotAllowed {
					t.Fatalf("%s status = %d, want 405", method, rec.Code)
				}
			}
			if rec := serveMetadata(handler, http.MethodHead); rec.Code != http.StatusOK {
				t.Fatalf("HEAD status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestProtectedResourceMetadataIsPubliclyReadable(t *testing.T) {
	h := newHarness(t)

	rec := serveMetadata(h.srv.ProtectedResourceMetadataHandler(), http.MethodGet)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want * for discovery", got)
	}
}
