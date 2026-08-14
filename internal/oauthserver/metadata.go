package oauthserver

import (
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// metadataCacheControl is what both documents are served with. They change only when the
// deployment is reconfigured, and clients are expected to cache them.
const metadataCacheControl = "public, max-age=3600"

// clientAuthMethodsSupported is the set advertised for both the token and the revocation
// endpoint, and it is exactly what [Client.Authenticate] implements.
func clientAuthMethodsSupported() []string {
	return []string{
		string(AuthMethodNone), string(AuthMethodSecretBasic), string(AuthMethodSecretPost),
	}
}

// ProtectedResourceMetadata returns the RFC 9728 document for this resource.
//
// Every value comes from the validated configuration, never from a request header: a
// metadata document assembled from Host or X-Forwarded-Proto lets whoever controls those
// headers point clients at an authorization server of their choosing.
//
// BearerMethodsSupported names only "header", which is the rule
// [Server.RequireBearerToken] enforces. There is no JWKS URI, because the tokens are
// opaque and there is no signature to verify.
func (s *Server) ProtectedResourceMetadata() *oauthex.ProtectedResourceMetadata {
	return &oauthex.ProtectedResourceMetadata{
		Resource:               s.resource.String(),
		AuthorizationServers:   []string{s.issuer},
		ScopesSupported:        s.scopesSupported.Strings(),
		BearerMethodsSupported: []string{"header"},
		ResourceName:           s.resourceName,
	}
}

// AuthorizationServerMetadata returns the RFC 8414 document for this server.
//
// It advertises exactly what is implemented and nothing more. There is no registration
// endpoint, because anonymous dynamic client registration is not offered; the only
// challenge method is S256, so no client can conclude that "plain" might be accepted; and
// the grant list names the two grants that exist.
func (s *Server) AuthorizationServerMetadata() *oauthex.AuthServerMeta {
	return &oauthex.AuthServerMeta{
		Issuer:                                 s.issuer,
		AuthorizationEndpoint:                  s.authorizationEndpoint,
		TokenEndpoint:                          s.tokenEndpoint,
		RevocationEndpoint:                     s.revocationEndpoint,
		ScopesSupported:                        s.scopesSupported.Strings(),
		ResponseTypesSupported:                 []string{paramCode},
		GrantTypesSupported:                    []string{GrantAuthorizationCode, GrantRefreshToken},
		TokenEndpointAuthMethodsSupported:      clientAuthMethodsSupported(),
		RevocationEndpointAuthMethodsSupported: clientAuthMethodsSupported(),
		CodeChallengeMethodsSupported:          []string{string(MethodS256)},
	}
}

// authServerDocument is the wire shape of the RFC 8414 document.
//
// The SDK's AuthServerMeta is the source of truth for the field set, but it tags JWKSURI
// as `json:"jwks_uri"` with no omitempty, so marshalling it directly would emit
// "jwks_uri":"" and send a conforming client off to fetch the empty string. This server
// issues opaque tokens and has no JWKS, so the key has to be absent rather than empty.
type authServerDocument struct {
	Issuer                 string   `json:"issuer"`
	AuthorizationEndpoint  string   `json:"authorization_endpoint"`
	TokenEndpoint          string   `json:"token_endpoint"`
	RevocationEndpoint     string   `json:"revocation_endpoint,omitempty"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported []string `json:"response_types_supported"`
	GrantTypesSupported    []string `json:"grant_types_supported,omitempty"`
	TokenAuthMethods       []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	RevocationAuthMethods  []string `json:"revocation_endpoint_auth_methods_supported,omitempty"`
	CodeChallengeMethods   []string `json:"code_challenge_methods_supported,omitempty"`
}

func authServerWire(meta *oauthex.AuthServerMeta) authServerDocument {
	return authServerDocument{
		Issuer:                 meta.Issuer,
		AuthorizationEndpoint:  meta.AuthorizationEndpoint,
		TokenEndpoint:          meta.TokenEndpoint,
		RevocationEndpoint:     meta.RevocationEndpoint,
		ScopesSupported:        meta.ScopesSupported,
		ResponseTypesSupported: meta.ResponseTypesSupported,
		GrantTypesSupported:    meta.GrantTypesSupported,
		TokenAuthMethods:       meta.TokenEndpointAuthMethodsSupported,
		RevocationAuthMethods:  meta.RevocationEndpointAuthMethodsSupported,
		CodeChallengeMethods:   meta.CodeChallengeMethodsSupported,
	}
}

// ProtectedResourceMetadataHandler serves the RFC 9728 document.
//
// It is readable from any origin. The document is public discovery information by design,
// and a client that has not authenticated yet must be able to fetch it in order to learn
// where to authenticate.
func (s *Server) ProtectedResourceMetadataHandler() http.Handler {
	return metadataHandler(s.ProtectedResourceMetadata(), true)
}

// AuthorizationServerMetadataHandler serves the RFC 8414 document.
func (s *Server) AuthorizationServerMetadataHandler() http.Handler {
	return metadataHandler(authServerWire(s.AuthorizationServerMetadata()), false)
}

// metadataHandler renders one document. It answers GET and HEAD and refuses everything
// else: a metadata endpoint that accepts a write verb invites confusion about whether it
// is configurable at runtime. It is not.
func metadataHandler(document any, cors bool) http.Handler {
	body, err := json.Marshal(document)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "metadata unavailable", http.StatusInternalServerError)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if cors {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", metadataCacheControl)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
