package oauthserver

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/tamcore/garmin-mcp/internal/identity"
)

// The RFC 6750 §3.1 error codes this server puts in a challenge.
const (
	challengeInvalidToken      = "invalid_token"
	challengeInsufficientScope = "insufficient_scope"
)

// A TokenInfo is what a verified access token authorizes.
//
// It is the only way a request acquires a principal. Nothing else in the server may
// derive one from a session id, a cookie, a header or a tool argument.
type TokenInfo struct {
	Principal identity.Principal
	ClientID  string
	Scopes    ScopeSet
	Resource  Resource
	Family    FamilyID
	ExpiresAt time.Time
}

// tokenInfoKey is the private context key. It is an unexported struct type, so no other
// package can forge a value under it.
type tokenInfoKey struct{}

// TokenInfoFromContext returns the token info the middleware put in ctx.
//
// It fails with ErrMissingToken rather than returning a zero value, so a handler wired
// without the middleware refuses instead of running with an invalid principal.
func TokenInfoFromContext(ctx context.Context) (TokenInfo, error) {
	info, ok := ctx.Value(tokenInfoKey{}).(TokenInfo)
	if !ok {
		return TokenInfo{}, fmt.Errorf(
			"the request context carries no verified token: %w", ErrMissingToken)
	}
	return info, nil
}

// VerifyAccessToken verifies a presented access token.
//
// The distinction the RFC 6750 challenge depends on starts here: an absent credential is
// ErrMissingToken, and a credential that is present but unusable is ErrInvalidToken with
// the specific reason wrapped underneath for the log.
//
// The audience check is exact. A token minted for another resource is refused even though
// it is otherwise perfectly valid, which is what stops a token issued for one MCP
// deployment being replayed at another.
//
// There is no JWT path. Tokens are opaque and verified by a storage lookup of their
// digest, so there is no decoded-but-unverified claim anywhere to authorize from.
func (s *Server) VerifyAccessToken(ctx context.Context, presented Secret) (TokenInfo, error) {
	if presented.IsZero() {
		return TokenInfo{}, fmt.Errorf("no access token presented: %w", ErrMissingToken)
	}
	record, err := s.store.AccessToken(ctx, presented.Lookup())
	if err != nil {
		return TokenInfo{}, fmt.Errorf("%w: %w", ErrInvalidToken,
			storageOrCause(err, ErrTokenNotFound, ErrTokenRevoked))
	}
	if record.IsExpired(s.now()) {
		return TokenInfo{}, fmt.Errorf("%w: %w", ErrInvalidToken, ErrTokenExpired)
	}
	// oauthex.MatchesResource is the SDK's audience comparison. Both sides are already
	// canonical here, so it reduces to equality; using it keeps this server's audience
	// semantics identical to the SDK client's.
	if !oauthex.MatchesResource([]string{record.Resource.String()}, s.resource.String()) {
		return TokenInfo{}, fmt.Errorf("%w: token audience is another resource: %w",
			ErrInvalidToken, ErrResourceNotAllowed)
	}
	return TokenInfo{
		Principal: record.Principal,
		ClientID:  record.ClientID,
		Scopes:    record.Scopes,
		Resource:  record.Resource,
		Family:    record.Family,
		ExpiresAt: record.ExpiresAt,
	}, nil
}

// TokenVerifier adapts VerifyAccessToken to the official SDK's verifier signature, for a
// caller that would rather use auth.RequireBearerToken.
//
// Note what that costs. The SDK middleware emits a challenge carrying only
// resource_metadata and scope, never an RFC 6750 error code, so it cannot distinguish a
// missing token from an invalid one on the wire. [Server.RequireBearerToken] is the
// middleware that satisfies that requirement.
func (s *Server) TokenVerifier() auth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		info, err := s.VerifyAccessToken(ctx, SecretFromString(token))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", auth.ErrInvalidToken, err)
		}
		return &auth.TokenInfo{
			Scopes:     info.Scopes.Strings(),
			Expiration: info.ExpiresAt,
			UserID:     info.Principal.ID(),
			Extra: map[string]any{
				paramClientID: info.ClientID,
				paramResource: info.Resource.String(),
			},
		}, nil
	}
}

// RequireBearerToken is the protected-resource middleware.
//
// A bearer token is read from the Authorization header and nowhere else: not a query
// parameter, not a cookie, not a body field. A URL-borne credential ends up in proxy logs
// and browser history, and a cookie-borne one is reachable by a cross-site request.
//
// The challenge follows RFC 6750 §3. A request with no credentials gets a bare challenge
// with no error code, because there is no error to report yet — the client has simply not
// tried. A request with an unusable credential gets error="invalid_token", and one whose
// grant is too narrow gets 403 with error="insufficient_scope" naming what was needed.
func (s *Server) RequireBearerToken(required ...Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			presented, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				s.writeChallenge(w, http.StatusUnauthorized, "", nil)
				return
			}
			info, err := s.VerifyAccessToken(r.Context(), presented)
			if err != nil {
				s.writeChallenge(w, http.StatusUnauthorized, challengeInvalidToken, nil)
				return
			}
			if missing := missingScopes(info.Scopes, required); len(missing) > 0 {
				s.writeChallenge(w, http.StatusForbidden, challengeInsufficientScope, missing)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenInfoKey{}, info)))
		})
	}
}

// bearerToken extracts the credential. Exactly two whitespace-separated fields with a
// case-insensitive "bearer" scheme are accepted; anything else counts as no credential
// presented, which is how RFC 6750 §3.1 treats a request that did not really attempt
// authentication.
func bearerToken(authorization string) (Secret, bool) {
	fields := strings.Fields(authorization)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return Secret{}, false
	}
	token := SecretFromString(fields[1])
	return token, !token.IsZero()
}

// missingScopes reports which required scopes the grant lacks.
func missingScopes(granted ScopeSet, required []Scope) []Scope {
	missing := make([]Scope, 0, len(required))
	for _, scope := range required {
		if !granted.Contains(scope) {
			missing = append(missing, scope)
		}
	}
	return missing
}

// writeChallenge emits the WWW-Authenticate header and a fixed body.
func (s *Server) writeChallenge(w http.ResponseWriter, status int, code string, missing []Scope) {
	params := []string{`realm="` + s.issuer + `"`}
	if s.resourceMetadataURL != "" {
		params = append(params, `resource_metadata="`+s.resourceMetadataURL+`"`)
	}
	if code != "" {
		params = append(params,
			`error="`+code+`"`, `error_description="`+challengeDescription(code)+`"`)
	}
	if len(missing) > 0 {
		names := make([]string, len(missing))
		for i, scope := range missing {
			names[i] = string(scope)
		}
		params = append(params, `scope="`+strings.Join(names, " ")+`"`)
	}
	body := challengeBody(code)
	w.Header().Set("WWW-Authenticate", "Bearer "+strings.Join(params, ", "))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// challengeDescription and challengeBody are fixed strings per outcome. They are
// deliberately uninformative: the header carries the machine-readable answer, and neither
// may become a place where a presented credential is reflected back.
func challengeDescription(code string) string {
	switch code {
	case challengeInvalidToken:
		return "The access token is not valid."
	case challengeInsufficientScope:
		return "The token does not carry the required scope."
	default:
		return "Authorization is required."
	}
}

func challengeBody(code string) string {
	switch code {
	case challengeInvalidToken:
		return "invalid token\n"
	case challengeInsufficientScope:
		return "insufficient scope\n"
	default:
		return "authorization required\n"
	}
}
