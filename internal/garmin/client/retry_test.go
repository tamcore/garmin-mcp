package client_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

func TestDoParsesRetryAfter(t *testing.T) {
	t.Parallel()

	header := jsonHeader()
	header.Set(protocol.HeaderRetryAfter, "7")
	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusTooManyRequests, header: header}}}

	_, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Do() = %v, want an *APIError", err)
	}
	if apiErr.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", apiErr.RetryAfter)
	}
	if caller.calls() != 1 {
		t.Errorf("caller was used %d times, want 1: a 429 is never retried automatically", caller.calls())
	}
}

func TestDoRetriesTransportFailuresAndSelectedServerErrors(t *testing.T) {
	t.Parallel()

	transport := &url.Error{Op: "Get", URL: "https://connectapi.garmin.com/x?token=SENTINEL", Err: syscall.ECONNRESET}
	cases := map[string]struct {
		first     stubOutcome
		wantCalls int
	}{
		"connection reset":    {stubOutcome{err: transport}, 2},
		"dial timeout":        {stubOutcome{err: &net.OpError{Op: "dial", Err: errTimeout{}}}, 2},
		"service unavailable": {stubOutcome{status: http.StatusServiceUnavailable, header: jsonHeader()}, 2},
		"gateway timeout":     {stubOutcome{status: http.StatusGatewayTimeout, header: jsonHeader()}, 2},
		"not implemented":     {stubOutcome{status: http.StatusNotImplemented, header: jsonHeader()}, 1},
		"bad request":         {stubOutcome{status: http.StatusBadRequest, header: jsonHeader()}, 1},
		"not found":           {stubOutcome{status: http.StatusNotFound, header: jsonHeader()}, 1},
		"unauthorized":        {stubOutcome{status: http.StatusUnauthorized, header: jsonHeader()}, 1},
		"opaque error":        {stubOutcome{err: errors.New("opaque")}, 1},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			caller := &stubCaller{outcomes: []stubOutcome{
				tc.first,
				{status: http.StatusOK, header: jsonHeader(), body: []byte(profileBody)},
			}}
			limits := client.Limits{MaxAttempts: 2}
			payload, err := newTestClient(t, limits).Do(t.Context(), mustSession(t, caller), profileRequest())

			if caller.calls() != tc.wantCalls {
				t.Errorf("caller was used %d times, want %d", caller.calls(), tc.wantCalls)
			}
			if tc.wantCalls == 2 {
				if err != nil {
					t.Errorf("Do() = %v, want the retry to succeed", err)
				}
				if got := string(payload.Bytes()); got != profileBody {
					t.Errorf("payload = %q, want the retried body", got)
				}
				return
			}
			if err == nil {
				t.Fatal("Do() = nil, want the first failure returned unretried")
			}
			if strings.Contains(err.Error(), "SENTINEL") {
				t.Errorf("Do() = %q leaks the URL query of its cause", err.Error())
			}
		})
	}
}

func TestDoNeverRetriesUnsafeEffectsOrCredentialOps(t *testing.T) {
	t.Parallel()

	cases := map[string]client.Request{
		"unsafe write": {
			Op: client.OpListActivities, Endpoint: client.EndpointActivitySearch,
			Method: http.MethodPost, Path: client.PathActivitySearch, Effect: client.EffectUnsafeWrite,
		},
		"delete": {
			Op: client.OpListActivities, Endpoint: client.EndpointActivitySearch,
			Method: http.MethodDelete, Path: client.PathActivitySearch, Effect: client.EffectDelete,
		},
		"credential submission": {
			Op: client.Op(protocol.OpVerifyMFA), Endpoint: client.EndpointSocialProfile,
			Path: client.PathSocialProfile, Effect: client.EffectRead,
		},
	}

	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			caller := &stubCaller{outcomes: []stubOutcome{
				{status: http.StatusServiceUnavailable, header: jsonHeader()},
				{status: http.StatusOK, header: jsonHeader(), body: []byte(profileBody)},
			}}
			limits := client.Limits{MaxAttempts: 3}
			if _, err := newTestClient(t, limits).Do(t.Context(), mustSession(t, caller), req); err == nil {
				t.Error("Do() = nil, want the 503 returned rather than retried")
			}
			if caller.calls() != 1 {
				t.Errorf("caller was used %d times, want exactly 1", caller.calls())
			}
		})
	}
}

func TestDoBacksOffWithFullJitterAndHonorsRetryAfter(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var slept []time.Duration
	retryAfter := jsonHeader()
	retryAfter.Set(protocol.HeaderRetryAfter, "5")

	c, err := client.New(client.Config{
		Hosts:  testHosts(t),
		Limits: client.Limits{MaxAttempts: 4, BaseBackoff: time.Second, MaxBackoff: 8 * time.Second},
		Sleeper: client.SleeperFunc(func(_ context.Context, d time.Duration) error {
			mu.Lock()
			slept = append(slept, d)
			mu.Unlock()
			return nil
		}),
		// A jitter source pinned to half its range proves the delay is jittered
		// rather than the raw exponential step.
		Jitter: func() float64 { return 0.5 },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	caller := &stubCaller{outcomes: []stubOutcome{
		{status: http.StatusServiceUnavailable, header: jsonHeader()},
		{status: http.StatusBadGateway, header: jsonHeader()},
		{status: http.StatusServiceUnavailable, header: retryAfter},
		{status: http.StatusOK, header: jsonHeader(), body: []byte(profileBody)},
	}}
	if _, err := c.Do(t.Context(), mustSession(t, caller), profileRequest()); err != nil {
		t.Fatalf("Do() = %v, want the fourth attempt to succeed", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(slept) != 3 {
		t.Fatalf("slept %d times, want 3", len(slept))
	}
	if slept[0] != 500*time.Millisecond {
		t.Errorf("first backoff = %v, want half of the 1s base step", slept[0])
	}
	if slept[1] != time.Second {
		t.Errorf("second backoff = %v, want half of the doubled step", slept[1])
	}
	if slept[2] != 5*time.Second {
		t.Errorf("third backoff = %v, want the Retry-After hint to win", slept[2])
	}
}

func TestDoStopsRetryingWhenRetryAfterExceedsTheBackoffCap(t *testing.T) {
	t.Parallel()

	header := jsonHeader()
	header.Set(protocol.HeaderRetryAfter, "600")
	caller := &stubCaller{outcomes: []stubOutcome{
		{status: http.StatusServiceUnavailable, header: header},
		{status: http.StatusOK, header: jsonHeader(), body: []byte(profileBody)},
	}}

	limits := client.Limits{MaxAttempts: 3, MaxBackoff: 10 * time.Second}
	_, err := newTestClient(t, limits).Do(t.Context(), mustSession(t, caller), profileRequest())
	if !errors.Is(err, client.ErrServer) {
		t.Errorf("Do() = %v, want the server error returned", err)
	}
	if caller.calls() != 1 {
		t.Errorf("caller was used %d times, want 1: a Retry-After past the cap is not slept off", caller.calls())
	}
}

// errTimeout is a net.Error that reports a timeout, which is the shape a dial or
// header deadline arrives in.
type errTimeout struct{}

func (errTimeout) Error() string { return "synthetic timeout" }
func (errTimeout) Timeout() bool { return true }
