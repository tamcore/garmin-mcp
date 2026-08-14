package auth

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// originAllowlist is the set of scheme://host origins that may receive a
// principal's DI bearer token.
//
// It is derived from the protocol.Hosts a Refresher is configured with, so it
// follows the configured region — a garmin.cn Refresher permits the .cn bases and
// nothing else — and it follows a test's protocol.Overrides, so a testkit fake
// origin stays reachable without widening the rule for production.
//
// The value is immutable once built: the map is created by newOriginAllowlist and
// never written again, so it is safe to share across goroutines.
type originAllowlist struct {
	origins map[string]struct{}
}

// newOriginAllowlist collects the origins of every base hosts exposes: SSO,
// Connect, ConnectAPI, DIAuth and the mobile integration host. A base that is
// empty or unparsable contributes nothing, so a zero Hosts yields an allowlist
// that permits nothing.
func newOriginAllowlist(hosts protocol.Hosts) originAllowlist {
	bases := [...]string{
		hosts.SSOBase(),
		hosts.ConnectBase(),
		hosts.ConnectAPIBase(),
		hosts.DIAuthBase(),
		hosts.MobileIntegrationBase(),
	}

	origins := make(map[string]struct{}, len(bases))
	for _, base := range bases {
		parsed, err := url.Parse(base)
		if err != nil {
			continue
		}
		if key, ok := originKey(parsed); ok {
			origins[key] = struct{}{}
		}
	}
	return originAllowlist{origins: origins}
}

// permits reports whether u names one of the allowed origins.
//
// Scheme and host are compared exactly, after parsing, so neither a suffix
// ("sso.garmin.com.attacker.example") nor a plaintext downgrade of an https base
// matches. A URL that carries userinfo is never permitted: the credential in it
// would be sent to the host, and userinfo is also the classic way to make a
// hostile URL read like a Garmin one.
func (a originAllowlist) permits(u *url.URL) bool {
	key, ok := originKey(u)
	if !ok {
		return false
	}
	_, allowed := a.origins[key]
	return allowed
}

// check refuses req unless its URL names an allowed origin. The refusal names the
// operation only: the caller owns the request and can inspect the URL it built, so
// rendering the host here would only risk copying attacker-chosen text into a log.
func (a originAllowlist) check(req *http.Request) error {
	if req == nil {
		return errNilRequest
	}
	if !a.permits(req.URL) {
		return fmt.Errorf("garmin auth: authorized call: %w", ErrForeignHost)
	}
	return nil
}

// originKey renders the comparable scheme://host form of u, reporting false for a
// URL that cannot be compared: no scheme, no host, an opaque URL, or userinfo.
// The host keeps its port, so an explicit port never matches a base without one.
func originKey(u *url.URL) (string, bool) {
	if u == nil || u.Scheme == "" || u.Host == "" || u.Opaque != "" || u.User != nil {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), true
}
