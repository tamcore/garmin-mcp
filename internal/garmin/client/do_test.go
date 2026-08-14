package client_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

const (
	testPrincipal = "principal-0001"
	// fakeDisplayName is the synthetic display name every fixture in this package
	// uses.
	fakeDisplayName = "fake-tester"
	// testCalendarDate is a synthetic Garmin calendar date, YYYY-MM-DD.
	testCalendarDate = "2026-01-31"
	// jsonNullLiteral is the JSON null literal the union-decoder tests script.
	jsonNullLiteral = "null"
	// mediaTypeJSON is the media type every scripted API response carries.
	mediaTypeJSON = "application/json"
	// labelUnknown is what an unrecognized label renders as.
	labelUnknown = "unknown"
	profileBody  = `{"displayName":"` + fakeDisplayName + `","profileId":900001}`
)

// stubCaller records the requests it receives and replays scripted outcomes. It
// stands in for auth.Refresher, whose only relevant contract is Do.
type stubCaller struct {
	mu         sync.Mutex
	principals []string
	requests   []*http.Request
	outcomes   []stubOutcome
	cursor     int
}

type stubOutcome struct {
	status  int
	header  http.Header
	body    []byte
	err     error
	observe func(ctx context.Context)
}

func (c *stubCaller) Do(ctx context.Context, principal string, req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.principals = append(c.principals, principal)
	c.requests = append(c.requests, req)
	index := min(c.cursor, len(c.outcomes)-1)
	c.cursor++
	c.mu.Unlock()

	if len(c.outcomes) == 0 {
		return nil, errors.New("stubCaller: no scripted outcome")
	}
	outcome := c.outcomes[index]
	if outcome.observe != nil {
		outcome.observe(ctx)
	}
	if outcome.err != nil {
		return nil, outcome.err
	}

	status := outcome.status
	if status == 0 {
		status = http.StatusOK
	}
	header := outcome.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(outcome.body)),
		Request:    req,
	}, nil
}

func (c *stubCaller) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cursor
}

func (c *stubCaller) principalAt(index int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if index >= len(c.principals) {
		return ""
	}
	return c.principals[index]
}

func (c *stubCaller) lastRequest(t *testing.T) *http.Request {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("no request was dispatched")
	}
	return c.requests[len(c.requests)-1]
}

// newTestClient builds a Client whose hosts are the real Garmin bases, with no
// real sleeping and a jitter source pinned to the top of its range.
func newTestClient(t *testing.T, limits client.Limits) *client.Client {
	t.Helper()

	c, err := client.New(client.Config{
		Hosts:   testHosts(t),
		Limits:  limits,
		Sleeper: client.SleeperFunc(func(context.Context, time.Duration) error { return nil }),
		Jitter:  func() float64 { return 1 },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return c
}

func testHosts(t *testing.T) protocol.Hosts {
	t.Helper()

	hosts, err := protocol.NewHosts(protocol.DomainGlobal)
	if err != nil {
		t.Fatalf("NewHosts() = %v", err)
	}
	return hosts
}

func profileRequest() client.Request {
	return client.Request{
		Op:       client.OpGetSocialProfile,
		Endpoint: client.EndpointSocialProfile,
		Path:     client.PathSocialProfile,
	}
}

func mustSession(t *testing.T, caller client.Caller) client.Session {
	t.Helper()

	session, err := client.NewSession(caller, testPrincipal)
	if err != nil {
		t.Fatalf("NewSession() = %v", err)
	}
	return session
}

func TestDoSendsAPrincipalScopedGETWithNativeHeaders(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK,
		header: jsonHeader(),
		body:   []byte(profileBody),
	}}}
	payload, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())
	if err != nil {
		t.Fatalf("Do() = %v, want nil", err)
	}

	if got := string(payload.Bytes()); got != profileBody {
		t.Errorf("payload = %q, want the scripted body", got)
	}
	if payload.Status() != http.StatusOK {
		t.Errorf("Status() = %d, want 200", payload.Status())
	}
	if payload.NoContent() {
		t.Error("NoContent() = true for a 200 with a body")
	}

	req := caller.lastRequest(t)
	if req.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", req.Method)
	}
	if got := req.URL.String(); got != "https://connectapi.garmin.com"+client.PathSocialProfile {
		t.Errorf("URL = %q, want the API-tier profile URL", got)
	}
	if got := req.Header.Get("Accept-Encoding"); !strings.Contains(got, "gzip") {
		t.Errorf("Accept-Encoding = %q, want gzip so decompression stays bounded here", got)
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("the request must carry no Authorization header: the caller attaches the token")
	}
	if got := caller.principalAt(0); got != testPrincipal {
		t.Errorf("principal = %q, want %q", got, testPrincipal)
	}
}

func TestDoCarriesTheRequestTimeoutAsADeadline(t *testing.T) {
	t.Parallel()

	var deadline time.Time
	var hasDeadline bool
	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK,
		header: jsonHeader(),
		body:   []byte(profileBody),
		observe: func(ctx context.Context) {
			deadline, hasDeadline = ctx.Deadline()
		},
	}}}

	limits := client.Limits{RequestTimeout: 2 * time.Second}
	if _, err := newTestClient(t, limits).Do(t.Context(), mustSession(t, caller), profileRequest()); err != nil {
		t.Fatalf("Do() = %v", err)
	}

	if !hasDeadline {
		t.Fatal("the dispatched request context carries no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > 2*time.Second {
		t.Errorf("deadline is %v away, want it inside the 2s request timeout", remaining)
	}
}

func TestDoHonorsACancelledContextWithoutDispatching(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusOK, body: []byte(profileBody)}}}
	_, err := newTestClient(t, client.Limits{}).Do(ctx, mustSession(t, caller), profileRequest())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Do() = %v, want a context.Canceled error", err)
	}
	if caller.calls() != 0 {
		t.Errorf("caller was used %d times, want 0 for an already cancelled context", caller.calls())
	}
}

func TestDoNormalizesNoContent(t *testing.T) {
	t.Parallel()

	// Source: the 204 branch of Client._run_request, which normalizes an empty
	// response to an empty JSON document rather than an error.
	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusNoContent}}}
	payload, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())
	if err != nil {
		t.Fatalf("Do() = %v, want nil for a 204", err)
	}
	if !payload.NoContent() {
		t.Error("NoContent() = false for a 204")
	}
	if payload.Len() != 0 {
		t.Errorf("Len() = %d, want 0 for a 204", payload.Len())
	}

	var out map[string]any
	if err := client.DecodeJSON(payload, &out); err != nil {
		t.Errorf("DecodeJSON() = %v, want a 204 to decode as an empty document", err)
	}
}

func TestDoClassifiesStatusesIntoDistinguishableKinds(t *testing.T) {
	t.Parallel()

	cases := map[int]struct {
		kind     client.Kind
		sentinel error
	}{
		http.StatusNotFound:             {client.KindNotFound, client.ErrNotFound},
		http.StatusGone:                 {client.KindNotFound, client.ErrNotFound},
		http.StatusUnauthorized:         {client.KindAuthentication, client.ErrAuthentication},
		http.StatusForbidden:            {client.KindAuthentication, client.ErrAuthentication},
		http.StatusTooManyRequests:      {client.KindRateLimited, client.ErrRateLimited},
		http.StatusUnsupportedMediaType: {client.KindInvalidFile, client.ErrInvalidFile},
		http.StatusBadRequest:           {client.KindValidation, client.ErrValidation},
		http.StatusRequestTimeout:       {client.KindTemporaryConnection, client.ErrTemporaryConnection},
		http.StatusInternalServerError:  {client.KindServer, client.ErrServer},
		http.StatusMovedPermanently:     {client.KindUnknown, client.ErrUnexpectedResponse},
	}

	for status, want := range cases {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			caller := &stubCaller{outcomes: []stubOutcome{{status: status, header: jsonHeader()}}}
			limits := client.Limits{MaxAttempts: 1}
			_, err := newTestClient(t, limits).Do(t.Context(), mustSession(t, caller), profileRequest())

			var apiErr *client.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("Do() = %v, want an *APIError", err)
			}
			if apiErr.Kind != want.kind {
				t.Errorf("Kind = %v, want %v", apiErr.Kind, want.kind)
			}
			if apiErr.Status != status {
				t.Errorf("Status = %d, want %d", apiErr.Status, status)
			}
			if !errors.Is(err, want.sentinel) {
				t.Errorf("Do() = %v, want it to match %v", err, want.sentinel)
			}
		})
	}
}

func jsonHeader() http.Header {
	header := make(http.Header, 1)
	header.Set("Content-Type", mediaTypeJSON)
	return header
}
