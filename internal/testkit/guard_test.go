package testkit

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

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
func doRaw(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
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

func TestClientRefusesRealGarminHost(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())

	err := doRaw(srv.Client(), requestTo(t, realGarminOrigin+"/sso/signin"))

	assertOffOrigin(t, err, srv.BaseURL(), realGarminOrigin)
	if got := srv.Requests(); len(got) != 0 {
		t.Fatalf("len(Requests()) = %d, want 0", len(got))
	}
}

func TestClientAllowsTheFakeOrigin(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript().With(protocol.PathMobileLogin,
		JSON(http.StatusOK, LoginSuccessJSON("ST-fake-3001"))))

	resp := post(t, srv.Client(), srv.Hosts(protocol.DomainGlobal).MobileLoginURL(), ContentTypeJSON, "{}")
	if got := protocol.ClassifyJSONLogin(resp); got.ServiceTicket != "ST-fake-3001" {
		t.Fatalf("ServiceTicket = %q, want ST-fake-3001", got.ServiceTicket)
	}
	if got := srv.Requests(); len(got) != 1 {
		t.Fatalf("len(Requests()) = %d, want 1", len(got))
	}
}

func TestClientRefusesOffOriginRedirect(t *testing.T) {
	t.Parallel()

	header := make(http.Header, 1)
	header.Set("Location", realGarminOrigin+"/sso/signin")
	srv := NewServer(t, NewScript().With(protocol.PathWidgetEmbed,
		Behavior{Status: http.StatusFound, Header: header}))

	err := doRaw(srv.Client(), requestTo(t, srv.Hosts(protocol.DomainGlobal).WidgetEmbedURL()))

	assertOffOrigin(t, err, srv.BaseURL(), realGarminOrigin)
	if got := srv.Requests(); len(got) != 1 {
		t.Fatalf("len(Requests()) = %d, want 1 (the redirect itself only)", len(got))
	}
}

func TestClientFollowsSameOriginRedirect(t *testing.T) {
	t.Parallel()

	header := make(http.Header, 1)
	header.Set("Location", protocol.PathMobileLogin)
	srv := NewServer(t, NewScript().
		With(protocol.PathWidgetEmbed, Behavior{Status: http.StatusFound, Header: header}).
		With(protocol.PathMobileLogin, JSON(http.StatusOK, LoginSuccessJSON("ST-fake-3002"))))

	resp := get(t, srv.Client(), srv.Hosts(protocol.DomainGlobal).WidgetEmbedURL())
	if got := protocol.ClassifyJSONLogin(resp); got.ServiceTicket != "ST-fake-3002" {
		t.Fatalf("ServiceTicket = %q, want ST-fake-3002", got.ServiceTicket)
	}
}

func TestClientRefusesOtherLoopbackPort(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())
	other := NewServer(t, NewScript().With("/anything", JSON(http.StatusOK, "{}")))

	err := doRaw(srv.Client(), requestTo(t, other.BaseURL()+"/anything"))

	assertOffOrigin(t, err, srv.BaseURL(), other.BaseURL())
	if got := other.Requests(); len(got) != 0 {
		t.Fatalf("the other fake recorded %d requests, want 0", len(got))
	}
}

func TestClientRefusesClosedLoopbackPort(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())

	parsed, err := url.Parse(srv.BaseURL())
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	parsed.Host = parsed.Hostname() + ":1" // never the fake's port

	assertOffOrigin(t, doRaw(srv.Client(), requestTo(t, parsed.String())), srv.BaseURL(), parsed.Scheme+"://"+parsed.Host)
	if got := srv.Requests(); len(got) != 0 {
		t.Fatalf("len(Requests()) = %d, want 0", len(got))
	}
}

func TestClientsDoNotShareState(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())

	first := srv.Client()
	first.Timeout = time.Millisecond
	first.CheckRedirect = nil

	second := srv.Client()
	if second.Timeout != 0 {
		t.Fatalf("second client Timeout = %v, want 0", second.Timeout)
	}
	if second.CheckRedirect == nil {
		t.Fatal("second client lost its redirect guard")
	}
	if second.Transport == nil {
		t.Fatal("second client has no transport")
	}
}
