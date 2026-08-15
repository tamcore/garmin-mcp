package mcpserver

import (
	"fmt"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

// AuthorizationRateLimit returns HTTP middleware that bounds how fast one caller
// may reach the authorization endpoints — the token endpoint, the revocation
// endpoint, and the metadata documents.
//
// Those endpoints are the ones an unauthenticated caller may reach, so they are
// where guessing happens. Without a limit in front of them the only defences are
// the body cap and the structural bounds, neither of which slows a caller down,
// and credential stuffing becomes something an operator has to solve at a proxy
// this project does not ship.
//
// The client address is taken from [HTTPTransport.ClientIP] and from nowhere
// else. That is the entire reason this constructor lives on the transport rather
// than at the mount point: the transport already knows which peers are trusted
// proxies, so a forwarded header counts only when it came from one. A limiter
// wired to a raw X-Forwarded-For would give a fresh budget to every caller
// willing to set a header, which is a limiter that reports numbers and stops
// nobody.
//
// The returned middleware answers a limited request itself, with an RFC 6749
// error object and a Retry-After header. The wrapped handler is not called, so a
// limited request never reaches the authorization server.
func (t *HTTPTransport) AuthorizationRateLimit(
	cfg ratelimit.HTTPGateConfig,
) (func(http.Handler) http.Handler, error) {
	gate, err := ratelimit.NewHTTPGate(cfg, t.ClientIP, nil)
	if err != nil {
		return nil, fmt.Errorf("building the authorization rate limiter: %w", err)
	}
	return gate.Middleware(), nil
}
