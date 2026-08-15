package config

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Registry bounds. Each one exists so a configuration file cannot describe a
// registry this deployment would then have to carry through every request.
const (
	// MaxOAuthClients bounds how many clients one deployment may register.
	MaxOAuthClients = 32
	// MaxClientIDLen bounds a client identifier. It matches the authorization
	// server's own bound, so a registration this package accepts is one that
	// server can also build.
	MaxClientIDLen = 256
	// MaxClientNameLen bounds the display name shown on the disclosure page.
	MaxClientNameLen = 128
	// MaxRedirectURIsPerClient bounds the exact redirect URIs of one client.
	MaxRedirectURIsPerClient = 8
	// MaxResourcesPerClient bounds the resource indicators of one client.
	MaxResourcesPerClient = 8
	// MaxURILen bounds a redirect URI or a resource indicator.
	MaxURILen = 2048
)

// An OAuthClient is one operator-registered MCP client.
//
// There is no dynamic registration, and no vendor identifier is ever defaulted: a
// client exists because an operator wrote it here. Every field is plain data, and
// the value is treated as immutable — [Config.Clone] copies the slices rather than
// sharing them.
//
// The secret digest is the hex SHA-256 of the client secret, never the secret, so
// this deployment holds nothing recoverable. It is supplied through
// [OAuthClient.SecretHashPath]; the inline companion exists only for a local
// experiment and is refused outright in remote mode.
type OAuthClient struct {
	// ID is the client identifier. It is required and must be unique.
	ID string
	// Name is the human-readable name shown on the non-binding disclosure page.
	Name string
	// RedirectURIs are the exact redirect URIs this client may use. At least one
	// is required, and matching against them is byte-exact.
	RedirectURIs []string
	// Scopes is the widest scope set this client may ever be granted. At least
	// one is required.
	Scopes []string
	// Resources are the exact RFC 8707 resource indicators this client may
	// request. At least one is required.
	Resources []string
	// Public selects token endpoint authentication method "none", which is safe
	// only because PKCE S256 is mandatory. A public client carries no digest.
	Public bool
	// SecretHashPath is the file holding the hex SHA-256 of a confidential
	// client's secret. It is the supported way to supply the digest.
	SecretHashPath string
	// SecretHash is an inline digest. It is an explicitly insecure compatibility
	// override, permitted only outside remote mode.
	SecretHash Secret
}

// clone returns a copy that shares no slice backing array with the receiver.
func (c OAuthClient) clone() OAuthClient {
	out := c
	out.RedirectURIs = copyStrings(c.RedirectURIs)
	out.Scopes = copyStrings(c.Scopes)
	out.Resources = copyStrings(c.Resources)
	return out
}

// cloneClients copies a whole registry.
func cloneClients(in []OAuthClient) []OAuthClient {
	if in == nil {
		return nil
	}
	out := make([]OAuthClient, len(in))
	for i, client := range in {
		out[i] = client.clone()
	}
	return out
}

// validateRegistry checks the whole registry: that there is one, that it is
// bounded, that no identifier repeats, and that every entry is usable.
//
// Nothing here echoes a configured value. A registry entry can carry a secret
// digest, and a message that quoted the offending entry would put it in the
// operator's log; the ordinal position is enough to find it.
func (c Config) validateRegistry() []error {
	switch {
	case len(c.OAuthClients) == 0:
		return []error{newFieldError(keyOAuthClients,
			"must register at least one OAuth client for the streamable-http transport",
			ErrMissingSetting)}
	case len(c.OAuthClients) > MaxOAuthClients:
		return []error{newFieldError(keyOAuthClients,
			"must register at most "+strconv.Itoa(MaxOAuthClients)+" OAuth clients",
			ErrInvalidConfig)}
	}

	var errs []error
	seen := make(map[string]struct{}, len(c.OAuthClients))
	for i, client := range c.OAuthClients {
		errs = append(errs, client.validate(i)...)
		id := strings.TrimSpace(client.ID)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			errs = append(errs, newFieldError(keyOAuthClients,
				"registers the identifier of client "+ordinal(i)+" twice", ErrInvalidConfig))
		}
		seen[id] = struct{}{}
	}
	return errs
}

// validate checks one registration. position names the entry in every message.
func (c OAuthClient) validate(position int) []error {
	errs := c.validateIdentity(position)
	errs = append(errs, c.validateRedirects(position)...)
	errs = append(errs, c.validateGrantSurface(position)...)
	errs = append(errs, c.validateCredential(position)...)
	return errs
}

// validateIdentity checks the identifier and the display name, both of which
// reach a log line and a rendered page.
func (c OAuthClient) validateIdentity(position int) []error {
	id := strings.TrimSpace(c.ID)
	var errs []error
	switch {
	case id == "":
		errs = append(errs, c.reject(position, "must have an identifier", ErrMissingSetting))
	case id != c.ID:
		errs = append(errs, c.reject(position,
			"must have an identifier without surrounding whitespace", ErrInvalidConfig))
	case len(id) > MaxClientIDLen:
		errs = append(errs, c.reject(position,
			"must have an identifier of at most "+strconv.Itoa(MaxClientIDLen)+" bytes",
			ErrInvalidConfig))
	case strings.ContainsFunc(id, isControlRune):
		errs = append(errs, c.reject(position,
			"must have an identifier without a control character", ErrInvalidConfig))
	}

	if len(c.Name) > MaxClientNameLen {
		errs = append(errs, c.reject(position,
			"must have a display name of at most "+strconv.Itoa(MaxClientNameLen)+" bytes",
			ErrInvalidConfig))
	}
	if strings.ContainsFunc(c.Name, isControlRune) {
		errs = append(errs, c.reject(position,
			"must have a display name without a control character", ErrInvalidConfig))
	}
	return errs
}

// validateRedirects requires at least one exactly-matchable redirect URI.
func (c OAuthClient) validateRedirects(position int) []error {
	switch {
	case len(c.RedirectURIs) == 0:
		return []error{c.reject(position, "must register at least one redirect URI", ErrMissingSetting)}
	case len(c.RedirectURIs) > MaxRedirectURIsPerClient:
		return []error{c.reject(position,
			"must register at most "+strconv.Itoa(MaxRedirectURIsPerClient)+" redirect URIs",
			ErrInvalidConfig)}
	}

	var errs []error
	seen := make(map[string]struct{}, len(c.RedirectURIs))
	for _, uri := range c.RedirectURIs {
		if err := c.checkRedirectURI(position, uri); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, duplicate := seen[uri]; duplicate {
			errs = append(errs, c.reject(position, "must not repeat a redirect URI", ErrInvalidConfig))
		}
		seen[uri] = struct{}{}
	}
	return errs
}

// checkRedirectURI applies the exact-match rules. Each one closes a known attack:
// a relative target riding on this origin, a plaintext downgrade, a fragment the
// server never sees, the userinfo spoof, and a wildcard registration.
func (c OAuthClient) checkRedirectURI(position int, raw string) error {
	const label = "redirect URI"
	if err := c.checkURIShape(position, raw, label); err != nil {
		return err
	}

	parsed, err := url.Parse(raw)
	switch {
	case err != nil, parsed.Host == "":
		return c.reject(position, "must register absolute redirect URIs with a host", ErrInvalidConfig)
	case parsed.User != nil:
		return c.reject(position, "must register redirect URIs without userinfo", ErrInvalidConfig)
	case parsed.Fragment != "", strings.Contains(raw, "#"):
		return c.reject(position, "must register redirect URIs without a fragment", ErrInvalidConfig)
	case parsed.Scheme == schemeHTTPS:
		return nil
	case parsed.Scheme != schemeHTTP:
		return c.reject(position,
			"must register https redirect URIs, or http for a literal loopback address",
			ErrInvalidConfig)
	case !isLiteralLoopback(parsed.Hostname()):
		return c.reject(position,
			"must not register a cleartext redirect URI for a non-loopback host", ErrInsecureSetting)
	default:
		return nil
	}
}

// checkURIShape rejects the bytes a URI must never contain, before it is parsed.
func (c OAuthClient) checkURIShape(position int, raw, label string) error {
	switch {
	case strings.TrimSpace(raw) == "":
		return c.reject(position, "must not register a blank "+label, ErrMissingSetting)
	case len(raw) > MaxURILen:
		return c.reject(position,
			"must register a "+label+" of at most "+strconv.Itoa(MaxURILen)+" bytes", ErrInvalidConfig)
	case strings.Contains(raw, "*"):
		return c.reject(position, "must not register a wildcard "+label, ErrInvalidConfig)
	case strings.ContainsFunc(raw, isControlRune), strings.ContainsAny(raw, " \t"):
		return c.reject(position,
			"must register a "+label+" without a control character or a space", ErrInvalidConfig)
	default:
		return nil
	}
}

// isLiteralLoopback reports whether host is a literal loopback address. The name
// "localhost" is deliberately not accepted: it resolves through a resolver an
// attacker may influence, which is the reason RFC 8252 §8.3 prefers the literal.
func isLiteralLoopback(host string) bool {
	trimmed := strings.Trim(strings.TrimSpace(host), "[]")
	ip := net.ParseIP(trimmed)
	return ip != nil && ip.IsLoopback()
}

// validateGrantSurface requires the scope set and the resource indicators. An
// empty set is refused rather than defaulted: a defaulted scope is a grant the
// operator never wrote, and a defaulted audience is a token usable elsewhere.
func (c OAuthClient) validateGrantSurface(position int) []error {
	var errs []error
	if len(c.Scopes) == 0 {
		errs = append(errs, c.reject(position, "must register at least one scope", ErrMissingSetting))
	}
	for _, scope := range c.Scopes {
		if strings.TrimSpace(scope) == "" {
			errs = append(errs, c.reject(position, "must not register a blank scope", ErrMissingSetting))
		}
	}

	switch {
	case len(c.Resources) == 0:
		errs = append(errs, c.reject(position,
			"must register at least one resource indicator", ErrMissingSetting))
	case len(c.Resources) > MaxResourcesPerClient:
		errs = append(errs, c.reject(position,
			"must register at most "+strconv.Itoa(MaxResourcesPerClient)+" resource indicators",
			ErrInvalidConfig))
	}
	for _, resource := range c.Resources {
		if err := c.checkResourceIndicator(position, resource); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// checkResourceIndicator applies the origin rules a resource indicator shares
// with a redirect URI: it is the audience a token is minted for, so it must be
// absolute, exact, and not plaintext outside loopback.
func (c OAuthClient) checkResourceIndicator(position int, raw string) error {
	const label = "resource indicator"
	if err := c.checkURIShape(position, raw, label); err != nil {
		return err
	}
	parsed, err := url.Parse(raw)
	switch {
	case err != nil, parsed.Host == "":
		return c.reject(position, "must register absolute resource indicators with a host",
			ErrInvalidConfig)
	case parsed.User != nil, parsed.Fragment != "", strings.Contains(raw, "#"):
		return c.reject(position, "must register resource indicators without userinfo or a fragment",
			ErrInvalidConfig)
	case parsed.Scheme == schemeHTTPS:
		return nil
	case parsed.Scheme != schemeHTTP:
		return c.reject(position,
			"must register https resource indicators, or http for a literal loopback address",
			ErrInvalidConfig)
	case !isLiteralLoopback(parsed.Hostname()):
		return c.reject(position,
			"must not register a cleartext resource indicator for a non-loopback host",
			ErrInsecureSetting)
	default:
		return nil
	}
}

// validateCredential enforces the structural rule that a confidential client
// carries a digest and a public one does not.
func (c OAuthClient) validateCredential(position int) []error {
	switch {
	case c.Public && (c.SecretHashPath != "" || c.SecretHash.IsSet()):
		return []error{c.reject(position,
			"is public and must not register a secret digest", ErrInvalidConfig)}
	case c.Public:
		return nil
	case c.SecretHashPath != "" && c.SecretHash.IsSet():
		return []error{c.reject(position,
			"must supply its secret digest either inline or by file, not both", ErrSecretConflict)}
	case c.SecretHash.IsSet():
		return []error{c.reject(position,
			"is confidential and must supply its secret digest through "+
				keyClientSecretHashFile+", never inline", ErrInsecureSetting)}
	case c.SecretHashPath == "":
		return []error{c.reject(position,
			"is confidential and registers no secret digest", ErrMissingSetting)}
	case checkPath(keyOAuthClients, c.SecretHashPath) != nil:
		return []error{c.reject(position, "names an unusable secret digest file", ErrInvalidConfig)}
	default:
		return nil
	}
}

// reject builds a FieldError naming the offending entry by position.
func (c OAuthClient) reject(position int, reason string, sentinel error) error {
	return newFieldError(keyOAuthClients, "client "+ordinal(position)+" "+reason, sentinel)
}

// ordinal renders a zero-based position as a one-based label, because an operator
// counts the entries in a configuration file from one.
func ordinal(position int) string { return strconv.Itoa(position + 1) }

// validateAllowedOrigins requires bare origins. A path, a query, or a fragment in
// an Origin allowlist entry can never match a browser's Origin header, so it would
// silently allow nothing.
func (c Config) validateAllowedOrigins() []error {
	var errs []error
	for _, origin := range c.AllowedOrigins {
		parsed, err := url.Parse(strings.TrimSpace(origin))
		switch {
		case err != nil, parsed.Host == "",
			parsed.Scheme != schemeHTTP && parsed.Scheme != schemeHTTPS:
			errs = append(errs, newFieldError(keyAllowedOrigins,
				"must contain absolute http or https origins", ErrInvalidConfig))
		case parsed.Path != "", parsed.RawQuery != "", parsed.Fragment != "", parsed.User != nil:
			errs = append(errs, newFieldError(keyAllowedOrigins,
				"must contain bare origins without a path, a query, a fragment, or userinfo",
				ErrInvalidConfig))
		}
	}
	return errs
}

// validateSessionTimeout keeps the idle-session bound inside its documented
// window. A zero value never reaches here: Default sets one.
func (c Config) validateSessionTimeout() []error {
	if c.SessionTimeout < MinSessionTimeout || c.SessionTimeout > MaxSessionTimeout {
		return []error{newFieldError(keySessionTimeout,
			"must be between "+MinSessionTimeout.String()+" and "+MaxSessionTimeout.String(),
			ErrInvalidConfig)}
	}
	return nil
}
