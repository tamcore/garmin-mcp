package cmd

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

// The fixed paths this deployment serves beside the MCP endpoint.
//
// They are constants rather than settings because a client discovers them from
// the metadata documents this server writes itself. An operator able to move them
// could only break that discovery.
const (
	// authServerMetadataPath is the RFC 8414 authorization server metadata
	// document.
	authServerMetadataPath = "/.well-known/oauth-authorization-server"
	// tokenPath is the OAuth token endpoint.
	tokenPath = "/token"
	// revocationPath is the RFC 7009 revocation endpoint.
	revocationPath = "/revoke"
	// loginPath is the browser login profile's first page.
	loginPath = "/login"
	// loginSubtreePath is the rest of that profile: the credential form, the
	// one-time code form, the consent page, and the stylesheet.
	loginSubtreePath = loginPath + "/"
	// schemeHTTPS is the only scheme a public deployment may publish.
	schemeHTTPS = "https"
)

// reservedPaths are the paths this deployment serves itself. The MCP endpoint may
// not be placed on one of them: two handlers on one path is a routing conflict,
// and resolving it silently would make one of the two unreachable.
var reservedPaths = []string{
	mcpserver.DefaultResourceMetadataPath,
	authServerMetadataPath,
	tokenPath,
	revocationPath,
	loginweb.RemoteAuthorizePath,
	loginPath,
}

// remoteEndpoints are the absolute URLs a remote deployment publishes, all derived
// from the one canonical public URL an operator configured.
//
// Nothing here is ever built from a request. The issuer, the resource indicator,
// and every endpoint URL come from configuration, because a metadata document or a
// token audience assembled from an attacker-controlled Host header is a token
// confusion vulnerability rather than a convenience.
type remoteEndpoints struct {
	// issuer is the exact issuer identifier: the bare origin of the public URL.
	issuer string
	// resource is the RFC 8707 resource indicator of the MCP endpoint, and the
	// audience every issued token is minted for.
	resource string
	// mcpPath is the path the MCP endpoint is served on.
	mcpPath string

	authorization    string
	token            string
	revocation       string
	resourceMetadata string
}

// newRemoteEndpoints derives the published URLs from the canonical public URL.
//
// It refuses a cleartext deployment. The authorization server names the issuer in
// its metadata and mints tokens for the resource, so a cleartext origin publishes
// every bearer token in plaintext; the transport enforces the same rule for the
// bind address, and this is the half that covers the issuer.
func newRemoteEndpoints(publicURL string) (remoteEndpoints, error) {
	parsed, err := url.Parse(strings.TrimSpace(publicURL))
	switch {
	case err != nil, parsed.Host == "":
		return remoteEndpoints{}, fmt.Errorf(
			"the public URL is not an absolute URL with a host: %w", ErrInsecureDeployment)
	case parsed.User != nil:
		// The URL is not echoed: userinfo carries a password, and an error that
		// printed it would put the password in the operator's log.
		return remoteEndpoints{}, fmt.Errorf(
			"the public URL carries userinfo: %w", ErrInsecureDeployment)
	case parsed.Scheme != schemeHTTPS:
		return remoteEndpoints{}, fmt.Errorf(
			"the public URL %q is cleartext, and the authorization server will not "+
				"name a cleartext issuer: %w", parsed.Redacted(), ErrInsecureDeployment)
	}

	path := parsed.Path
	if path == "" {
		path = "/"
	}
	if slices.Contains(reservedPaths, strings.TrimSuffix(path, "/")) {
		return remoteEndpoints{}, fmt.Errorf(
			"the public URL path %q is one this deployment serves itself: %w",
			path, ErrInsecureDeployment)
	}

	issuer := parsed.Scheme + "://" + parsed.Host
	return remoteEndpoints{
		issuer:           issuer,
		resource:         parsed.String(),
		mcpPath:          path,
		authorization:    issuer + loginweb.RemoteAuthorizePath,
		token:            issuer + tokenPath,
		revocation:       issuer + revocationPath,
		resourceMetadata: issuer + mcpserver.DefaultResourceMetadataPath,
	}, nil
}
