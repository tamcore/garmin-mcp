package testkit

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// realGarminOrigin is the origin no testkit client may ever reach. It is only
// ever used as a refusal target, never dialed.
const realGarminOrigin = "https://sso.garmin.com"

// networkMarkers returns the substrings dial and DNS failures carry. Their
// absence from a refusal proves no resolution or connection was attempted.
func networkMarkers() []string {
	return []string{"dial", "lookup", "no such host", "connection refused", "i/o timeout"}
}

// doRaw performs req and closes any body, returning only the error.
func doRaw(doer Doer, req *http.Request) error {
	resp, err := doer.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

func requestTo(t *testing.T, rawURL string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

// assertOffOrigin checks err is an *OffOriginError for origin and that no
// network attempt leaked into it.
func assertOffOrigin(t *testing.T, err error, origin, attempt string) {
	t.Helper()

	if err == nil {
		t.Fatal("request succeeded, want an off-origin refusal")
	}
	var refused *OffOriginError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v (%T), want *OffOriginError", err, err)
	}
	if refused.Origin != origin {
		t.Fatalf("Origin = %q, want %q", refused.Origin, origin)
	}
	if refused.Attempt != attempt {
		t.Fatalf("Attempt = %q, want %q", refused.Attempt, attempt)
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range networkMarkers() {
		if strings.Contains(lower, marker) {
			t.Fatalf("err = %v, mentions %q so a network attempt was made", err, marker)
		}
	}
	if !strings.Contains(refused.Error(), attempt) || !strings.Contains(refused.Error(), origin) {
		t.Fatalf("Error() = %q, must name both the refused and the allowed origin", refused.Error())
	}
}

func TestDoerRefusesRealGarminHost(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())

	err := doRaw(srv.Doer(), requestTo(t, realGarminOrigin+"/sso/signin"))

	assertOffOrigin(t, err, srv.BaseURL(), realGarminOrigin)
	if got := srv.Requests(); len(got) != 0 {
		t.Fatalf("len(Requests()) = %d, want 0", len(got))
	}
}

func TestDoerAllowsTheFakeOrigin(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript().With(protocol.PathMobileLogin,
		JSON(http.StatusOK, LoginSuccessJSON("ST-fake-3001"))))

	resp := post(t, srv.Doer(), srv.Hosts(protocol.DomainGlobal).MobileLoginURL(), ContentTypeJSON, "{}")
	if got := protocol.ClassifyJSONLogin(resp); got.ServiceTicket() != "ST-fake-3001" {
		t.Fatalf("ServiceTicket = %q, want ST-fake-3001", got.ServiceTicket())
	}
	if got := srv.Requests(); len(got) != 1 {
		t.Fatalf("len(Requests()) = %d, want 1", len(got))
	}
}

func TestDoerRefusesOffOriginRedirect(t *testing.T) {
	t.Parallel()

	header := make(http.Header, 1)
	header.Set("Location", realGarminOrigin+"/sso/signin")
	srv := NewServer(t, NewScript().With(protocol.PathWidgetEmbed,
		Behavior{Status: http.StatusFound, Header: header}))

	err := doRaw(srv.Doer(), requestTo(t, srv.Hosts(protocol.DomainGlobal).WidgetEmbedURL()))

	assertOffOrigin(t, err, srv.BaseURL(), realGarminOrigin)
	if got := srv.Requests(); len(got) != 1 {
		t.Fatalf("len(Requests()) = %d, want 1 (the redirect itself only)", len(got))
	}
}

func TestDoerFollowsSameOriginRedirect(t *testing.T) {
	t.Parallel()

	header := make(http.Header, 1)
	header.Set("Location", protocol.PathMobileLogin)
	srv := NewServer(t, NewScript().
		With(protocol.PathWidgetEmbed, Behavior{Status: http.StatusFound, Header: header}).
		With(protocol.PathMobileLogin, JSON(http.StatusOK, LoginSuccessJSON("ST-fake-3002"))))

	resp := get(t, srv.Doer(), srv.Hosts(protocol.DomainGlobal).WidgetEmbedURL())
	if got := protocol.ClassifyJSONLogin(resp); got.ServiceTicket() != "ST-fake-3002" {
		t.Fatalf("ServiceTicket = %q, want ST-fake-3002", got.ServiceTicket())
	}
}

func TestDoerRefusesOtherLoopbackPort(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())
	other := NewServer(t, NewScript().With("/anything", JSON(http.StatusOK, "{}")))

	err := doRaw(srv.Doer(), requestTo(t, other.BaseURL()+"/anything"))

	assertOffOrigin(t, err, srv.BaseURL(), other.BaseURL())
	if got := other.Requests(); len(got) != 0 {
		t.Fatalf("the other fake recorded %d requests, want 0", len(got))
	}
}

func TestDoerRefusesClosedLoopbackPort(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())

	parsed, err := url.Parse(srv.BaseURL())
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	parsed.Host = parsed.Hostname() + ":1" // never the fake's port

	assertOffOrigin(t, doRaw(srv.Doer(), requestTo(t, parsed.String())), srv.BaseURL(), parsed.Scheme+"://"+parsed.Host)
	if got := srv.Requests(); len(got) != 0 {
		t.Fatalf("len(Requests()) = %d, want 0", len(got))
	}
}

// TestGuardedTransportRefusesOffOriginRequests checks the layer closest to the
// network on its own: even handed a request the outer Do never saw, the round
// tripper refuses before it consults the real transport.
func TestGuardedTransportRefusesOffOriginRequests(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())
	guard := originGuard{origin: srv.BaseURL(), next: failingTransport{t: t}}

	resp, err := guard.RoundTrip(requestTo(t, realGarminOrigin+"/sso/signin"))
	if resp != nil {
		_ = resp.Body.Close()
	}

	assertOffOrigin(t, err, srv.BaseURL(), realGarminOrigin)
}

// failingTransport fails the test if it is ever reached.
type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Helper()
	f.t.Fatalf("transport reached for %s, so the guard let a request through", req.URL)
	return nil, nil
}
