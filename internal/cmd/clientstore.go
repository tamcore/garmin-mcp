package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/securefile"
)

// maxDigestFileBytes bounds a client secret digest file. A hex SHA-256 is 64
// bytes and a trailing newline is common, so anything beyond a short line is a
// file the operator did not mean to point at.
const maxDigestFileBytes = 128

// configClients is the operator-registered client registry.
//
// It is built once, at start-up, from validated configuration and from the digest
// files that configuration names. After construction it is immutable and safe for
// concurrent use, which is why it can answer a lookup on the request path without
// touching the filesystem: a registration cannot change under a live authorization
// request, and a digest file cannot be swapped for one the operator did not
// install.
//
// There is no write side. This deployment offers no dynamic client registration,
// so a client exists because an operator wrote it into configuration.
type configClients struct {
	clients map[string]oauthserver.Client
	ids     []string
}

// The assertion this type exists for: the authorization server's storage adapter
// takes exactly this interface.
var _ oauthserver.ClientStore = (*configClients)(nil)

// newConfigClients builds the registry from cfg.
//
// Every digest file is read here, at start-up, through the hardened file layer, so
// an unreadable or group-readable digest is a start-up failure rather than a
// mid-flight token endpoint failure. The digest is a hash and not a credential,
// but it is the value an attacker would want in order to test a guessed secret
// offline, so it is treated as secret material: it is never rendered, never
// logged, and never echoed into an error.
func newConfigClients(cfg config.Config) (*configClients, error) {
	registry := &configClients{
		clients: make(map[string]oauthserver.Client, len(cfg.OAuthClients)),
		ids:     make([]string, 0, len(cfg.OAuthClients)),
	}

	for _, registration := range cfg.OAuthClients {
		client, err := buildClient(registration)
		if err != nil {
			return nil, err
		}
		if _, duplicate := registry.clients[client.ID()]; duplicate {
			return nil, fmt.Errorf("client %q is registered twice: %w",
				client.ID(), oauthserver.ErrInvalidClient)
		}
		registry.clients[client.ID()] = client
		registry.ids = append(registry.ids, client.ID())
	}
	return registry, nil
}

// buildClient converts one configured registration into a validated client.
//
// The authorization server re-validates everything the configuration package
// already checked. That duplication is deliberate: the server's rules are the
// authoritative ones, and a registration this composition root accepted but the
// server would refuse must fail at start-up rather than at the first request.
func buildClient(registration config.OAuthClient) (oauthserver.Client, error) {
	digest, err := clientDigest(registration)
	if err != nil {
		return oauthserver.Client{}, err
	}

	client, err := oauthserver.NewClient(oauthserver.ClientSpec{
		ID:                      registration.ID,
		Name:                    registration.Name,
		RedirectURIs:            registration.RedirectURIs,
		Scopes:                  strings.Join(registration.Scopes, " "),
		Resources:               registration.Resources,
		TokenEndpointAuthMethod: authMethodOf(registration),
		SecretHashHex:           digest,
	})
	if err != nil {
		return oauthserver.Client{}, fmt.Errorf("registering client %q: %w", registration.ID, err)
	}
	return client, nil
}

// authMethodOf maps the configured shape onto a token endpoint authentication
// method. A public client is method "none", which is safe only because PKCE S256
// is mandatory on every authorization request; a confidential client presents its
// secret in the Authorization header.
func authMethodOf(registration config.OAuthClient) string {
	if registration.Public {
		return string(oauthserver.AuthMethodNone)
	}
	return string(oauthserver.AuthMethodSecretBasic)
}

// clientDigest reads a confidential client's secret digest.
//
// A public client has none by construction. The inline form is refused here as
// well as in configuration validation, because this function is what a future
// caller would reach first, and a check that lives only in the layer above is a
// check that stops running when a new caller appears.
func clientDigest(registration config.OAuthClient) (string, error) {
	switch {
	case registration.Public:
		return "", nil
	case registration.SecretHash.IsSet():
		return "", fmt.Errorf(
			"client %q supplies its secret digest inline, which remote mode refuses: %w",
			registration.ID, ErrInsecureDeployment)
	case registration.SecretHashPath == "":
		return "", fmt.Errorf("confidential client %q registers no secret digest: %w",
			registration.ID, oauthserver.ErrInvalidClient)
	}

	content, err := securefile.ReadFile(registration.SecretHashPath, maxDigestFileBytes)
	if err != nil {
		// The cause names the path and the permission fault, never the content:
		// securefile reports what it refused, not what it read.
		return "", fmt.Errorf("reading the secret digest of client %q: %w",
			registration.ID, err)
	}
	return strings.TrimSpace(string(content)), nil
}

// Client returns the registration for clientID.
//
// An unknown identifier is ErrUnknownClient, which is the same answer for a
// misspelled client and for one that was never registered: the authorization
// endpoint renders both locally, without a redirect, so neither discloses whether
// the other exists.
func (c *configClients) Client(_ context.Context, clientID string) (oauthserver.Client, error) {
	client, ok := c.clients[clientID]
	if !ok {
		return oauthserver.Client{}, fmt.Errorf("no client is registered under that identifier: %w",
			oauthserver.ErrUnknownClient)
	}
	return client, nil
}

// Scopes returns every scope any registered client may be granted.
//
// It is the deployment's advertised scope set: the authorization server refuses a
// request for a scope outside it, and refuses a client whose registration exceeds
// it, so deriving it from the registry keeps the two bounds from contradicting
// each other.
func (c *configClients) Scopes() string {
	seen := make(map[oauthserver.Scope]struct{})
	var ordered []string
	for _, id := range c.ids {
		for _, scope := range c.clients[id].MaxScopes().Slice() {
			if _, known := seen[scope]; known {
				continue
			}
			seen[scope] = struct{}{}
			ordered = append(ordered, string(scope))
		}
	}
	return strings.Join(ordered, " ")
}
