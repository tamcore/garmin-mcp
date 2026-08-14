package oauthserver

import (
	"fmt"
	"net/url"
	"strings"
)

// Bounds and schemes for the URI-shaped protocol parameters.
const (
	// MaxURILen bounds every URI this package accepts. One bound covers them all
	// because they are all origins plus a short path, and a single number is one
	// fewer thing to get inconsistent.
	MaxURILen = 2048
	// MaxRedirectURILen bounds a registered or presented redirect URI.
	MaxRedirectURILen = MaxURILen
	// MaxResourceLen bounds an RFC 8707 resource indicator.
	MaxResourceLen = MaxURILen

	schemeHTTPS = "https"
	schemeHTTP  = "http"
)

// isLoopbackHost reports whether host is one of the two literal loopback
// addresses. RFC 8252 §8.3 recommends the literal address over the name
// "localhost", because a name resolves through a resolver an attacker may
// influence, so "localhost" is refused here. The rule is a function rather than
// a package-level map because this package holds no mutable package state.
func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1"
}

// A RedirectURI is a redirect URI that passed every structural check this server
// makes. It keeps the exact bytes it was parsed from, because registration
// matching is byte-exact: normalizing here would silently widen what an operator
// registered.
//
// The zero RedirectURI is invalid and never matches anything.
type RedirectURI struct {
	raw string
}

// ParseRedirectURI validates a redirect URI and returns it unchanged.
//
// The rules are deliberately narrow, and each one closes a known attack:
//
//   - absolute, with a host, so a relative target cannot ride on this origin;
//   - scheme exactly "https", or "http" for a literal loopback address, so a
//     redirect cannot downgrade to plaintext;
//   - no fragment, not even an empty one, because a fragment is invisible to the
//     server and can smuggle a second target past a substring check;
//   - no userinfo, which is the classic "https://good.example@evil.example" spoof;
//   - no "*", so a registration can never express a wildcard;
//   - no control character or space anywhere, so the value cannot split a header
//     or a log line.
func ParseRedirectURI(raw string) (RedirectURI, error) {
	if err := checkURIShape(raw, ErrInvalidRedirectURI); err != nil {
		return RedirectURI{}, err
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return RedirectURI{}, fmt.Errorf("redirect URI does not parse: %w", ErrInvalidRedirectURI)
	}
	if err := checkOrigin(raw, parsed, ErrInvalidRedirectURI); err != nil {
		return RedirectURI{}, err
	}
	return RedirectURI{raw: raw}, nil
}

// String returns the exact bytes the URI was registered or presented as.
func (u RedirectURI) String() string { return u.raw }

// IsZero reports whether u is the zero value.
func (u RedirectURI) IsZero() bool { return u.raw == "" }

// Equal reports byte-exact equality. Nothing is folded: not the host case, not a
// default port, not a trailing slash. A client that wants two targets registers
// two redirect URIs.
func (u RedirectURI) Equal(other RedirectURI) bool {
	return u.raw != "" && u.raw == other.raw
}

// WithParams returns the URI with params added to its query string, which is how
// an authorization response and a redirected authorization error are delivered.
// A parameter already present in the registered URI is a conflict and is refused
// rather than overwritten.
func (u RedirectURI) WithParams(params map[string]string) (string, error) {
	parsed, err := url.Parse(u.raw)
	if err != nil {
		return "", fmt.Errorf("redirect URI does not parse: %w", ErrInvalidRedirectURI)
	}
	query := parsed.Query()
	for key, value := range params {
		if query.Has(key) {
			return "", fmt.Errorf("redirect URI already carries the %q parameter: %w",
				key, ErrInvalidRedirectURI)
		}
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// A Resource is an RFC 8707 resource indicator: the canonical URL of the
// protected resource a token is minted for. It is the audience, and it is
// compared exactly.
//
// The zero Resource is invalid.
type Resource struct {
	raw string
}

// ParseResource validates a resource indicator under the same origin rules as a
// redirect URI, and canonicalizes exactly one thing: a bare root path. RFC 9728
// §3.3 writes the resource identifier with a trailing slash while an RFC 8707
// request often omits it, so "https://mcp.example" and "https://mcp.example/"
// name the same resource. Every other path difference is a different resource.
func ParseResource(raw string) (Resource, error) {
	if err := checkURIShape(raw, ErrInvalidResource); err != nil {
		return Resource{}, err
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return Resource{}, fmt.Errorf("resource indicator does not parse: %w", ErrInvalidResource)
	}
	if err := checkOrigin(raw, parsed, ErrInvalidResource); err != nil {
		return Resource{}, err
	}
	return Resource{raw: canonicalResource(raw, parsed)}, nil
}

// canonicalResource drops a trailing slash only when the path is the bare root
// and there is no query, which is the single equivalence RFC 3986 §6.2.3 allows
// without knowing the scheme's semantics.
func canonicalResource(raw string, parsed *url.URL) string {
	if parsed.Path == "/" && parsed.RawQuery == "" {
		return strings.TrimSuffix(raw, "/")
	}
	return raw
}

// String returns the canonical resource indicator.
func (r Resource) String() string { return r.raw }

// IsZero reports whether r is the zero value.
func (r Resource) IsZero() bool { return r.raw == "" }

// Equal reports exact audience equality over the canonical forms.
func (r Resource) Equal(other Resource) bool {
	return r.raw != "" && r.raw == other.raw
}

// checkURIShape applies the checks that do not need the URI parsed: bounds, and
// the byte classes that must not appear anywhere in it.
func checkURIShape(raw string, sentinel error) error {
	if raw == "" {
		return fmt.Errorf("URI is empty: %w", sentinel)
	}
	if len(raw) > MaxURILen {
		return fmt.Errorf("URI is %d bytes, the limit is %d: %w", len(raw), MaxURILen, sentinel)
	}
	for i := range len(raw) {
		if raw[i] <= 0x20 || raw[i] >= 0x7F {
			return fmt.Errorf("URI carries a disallowed byte at offset %d: %w", i, sentinel)
		}
	}
	if strings.Contains(raw, "*") {
		return fmt.Errorf("URI carries a wildcard: %w", sentinel)
	}
	if strings.Contains(raw, "#") {
		return fmt.Errorf("URI carries a fragment: %w", sentinel)
	}
	return nil
}

// checkOrigin applies the checks that need the parsed URI: userinfo, host and
// scheme. The scheme is compared against the raw prefix rather than
// parsed.Scheme, because url.Parse lower-cases the scheme and would otherwise
// let "HTTPS://" through as https.
func checkOrigin(raw string, parsed *url.URL, sentinel error) error {
	if parsed.User != nil {
		return fmt.Errorf("URI carries userinfo: %w", sentinel)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf("URI has no host: %w", sentinel)
	}
	if !strings.HasPrefix(raw, parsed.Scheme+"://") {
		return fmt.Errorf("URI scheme is not lower-case or not hierarchical: %w", sentinel)
	}
	switch parsed.Scheme {
	case schemeHTTPS:
		return nil
	case schemeHTTP:
		if isLoopbackHost(parsed.Hostname()) {
			return nil
		}
		return fmt.Errorf("plain http is accepted only for a literal loopback address: %w", sentinel)
	default:
		return fmt.Errorf("URI scheme %q is not accepted: %w", parsed.Scheme, sentinel)
	}
}
