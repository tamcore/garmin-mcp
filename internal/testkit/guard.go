package testkit

import (
	"net/http"
	"net/url"
)

// OffOriginError reports a request refused by a testkit Doer because its scheme
// and host did not match the fake server's origin. Nothing was resolved,
// dialed, or sent.
type OffOriginError struct {
	// Origin is the only origin this Doer can reach, as scheme://host.
	Origin string
	// Attempt is the refused request origin, as scheme://host.
	Attempt string
}

func (e *OffOriginError) Error() string {
	return "testkit: refused request to " + e.Attempt +
		": a testkit Doer may only reach the fake Garmin origin " + e.Origin
}

// originGuard is an http.RoundTripper that refuses every request whose
// scheme and host are not exactly origin. The refusal happens before next is
// consulted, so no DNS lookup and no dial can occur.
type originGuard struct {
	origin string
	next   http.RoundTripper
}

func (g originGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if got := originOf(req.URL); got != g.origin {
		return nil, &OffOriginError{Origin: g.origin, Attempt: got}
	}
	return g.next.RoundTrip(req)
}

// checkRedirect refuses a redirect that leaves origin. It reports the hop
// before the transport is entered, so an off-origin Location yields the same
// *OffOriginError as an off-origin first request.
func checkRedirect(origin string) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, _ []*http.Request) error {
		if got := originOf(req.URL); got != origin {
			return &OffOriginError{Origin: origin, Attempt: got}
		}
		return nil
	}
}

// originOf renders scheme://host for u, the granularity the guard compares.
func originOf(u *url.URL) string {
	if u == nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}
