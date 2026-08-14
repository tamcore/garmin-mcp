//go:build fakegarmin

package client_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// fakeCaller is the principal-scoped caller for the fake service. In production
// this is *auth.Refresher, which additionally attaches the DI bearer token and
// enforces the host allowlist; here the testkit Doer enforces the origin, so the
// request layer is exercised over real HTTP with no credential in play.
type fakeCaller struct {
	doer testkit.Doer
}

func (f fakeCaller) Do(ctx context.Context, principal string, req *http.Request) (*http.Response, error) {
	if principal == "" {
		return nil, errors.New("fakeCaller: no principal")
	}
	return f.doer.Do(req.WithContext(ctx))
}

// fakeHarness wires a scripted fake Garmin service to a Client and a Session.
type fakeHarness struct {
	server  *testkit.Server
	client  *client.Client
	session client.Session
}

func newFakeHarness(t *testing.T, script testkit.Script, limits client.Limits) fakeHarness {
	t.Helper()

	server := testkit.NewServer(t, script)
	c, err := client.New(client.Config{
		Hosts:   server.Hosts(protocol.DomainGlobal),
		Limits:  limits,
		Sleeper: client.SleeperFunc(func(context.Context, time.Duration) error { return nil }),
		Jitter:  func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	session, err := client.NewSession(fakeCaller{doer: server.Doer()}, "principal-fake-0001")
	if err != nil {
		t.Fatalf("NewSession() = %v", err)
	}
	return fakeHarness{server: server, client: c, session: session}
}

func TestFakeServiceProfileReadDecodesTolerantly(t *testing.T) {
	script := testkit.NewScript().With(client.PathSocialProfile,
		testkit.JSON(http.StatusOK, testkit.SocialProfileJSON("fake-tester")))
	h := newFakeHarness(t, script, client.Limits{})

	var profile struct {
		DisplayName *string `json:"displayName"`
		ProfileID   *int64  `json:"profileId"`
		Missing     *string `json:"thisFieldIsAbsent"`
	}
	payload, err := h.client.GetJSON(t.Context(), h.session, client.Request{
		Op:       client.OpGetSocialProfile,
		Endpoint: client.EndpointSocialProfile,
		Path:     client.PathSocialProfile,
	}, &profile)
	if err != nil {
		t.Fatalf("GetJSON() = %v", err)
	}

	if profile.DisplayName == nil || *profile.DisplayName != fakeDisplayName {
		t.Errorf("DisplayName = %v, want the fake display name", profile.DisplayName)
	}
	if profile.Missing != nil {
		t.Error("an absent field must decode to nil")
	}
	if payload.Status() != http.StatusOK {
		t.Errorf("Status() = %d, want 200", payload.Status())
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if requests[0].Path != client.PathSocialProfile {
		t.Errorf("path = %q, want %q", requests[0].Path, client.PathSocialProfile)
	}
	if got := requests[0].Header.Get("Accept"); got != mediaTypeJSON {
		t.Errorf("Accept = %q, want application/json", got)
	}
}

func TestFakeServiceSendsQueryParametersSeparatelyFromThePath(t *testing.T) {
	script := testkit.NewScript().With(client.PathActivitySearch,
		testkit.JSON(http.StatusOK, `[{"activityId":9001}]`))
	h := newFakeHarness(t, script, client.Limits{})

	req := client.Request{
		Op:       client.OpListActivitiesByDate,
		Endpoint: client.EndpointActivitySearch,
		Path:     client.PathActivitySearch,
		Query: map[string][]string{
			client.QueryStart:     {"0"},
			client.QueryLimit:     {"20"},
			client.QueryStartDate: {testCalendarDate},
		},
	}
	if _, err := h.client.Do(t.Context(), h.session, req); err != nil {
		t.Fatalf("Do() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStartDate); got != testCalendarDate {
		t.Errorf("startDate = %q, want the calendar date", got)
	}
	if got := requests[0].Path; got != client.PathActivitySearch {
		t.Errorf("path = %q, want the query kept out of the path", got)
	}
}

func TestFakeServiceRateLimitKeepsItsRetryAfterAndIsNotRetried(t *testing.T) {
	script := testkit.NewScript().With(client.PathSocialProfile, testkit.RateLimited(3))
	h := newFakeHarness(t, script, client.Limits{})

	_, err := h.client.Do(t.Context(), h.session, client.Request{
		Op:       client.OpGetSocialProfile,
		Endpoint: client.EndpointSocialProfile,
		Path:     client.PathSocialProfile,
	})
	if !errors.Is(err, client.ErrRateLimited) {
		t.Fatalf("Do() = %v, want ErrRateLimited", err)
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("the failure is not an *APIError")
	}
	if apiErr.RetryAfter != 3*time.Second {
		t.Errorf("RetryAfter = %v, want 3s", apiErr.RetryAfter)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1: a 429 is never retried", got)
	}
}

func TestFakeServiceRetriesAServiceUnavailableThenSucceeds(t *testing.T) {
	script := testkit.NewScript().With(client.PathDevices,
		testkit.JSON(http.StatusServiceUnavailable, `{"message":"synthetic outage"}`),
		testkit.JSON(http.StatusOK, `[{"deviceId":4242}]`))
	h := newFakeHarness(t, script, client.Limits{MaxAttempts: 2})

	var devices []struct {
		DeviceID *int64 `json:"deviceId"`
	}
	if _, err := h.client.GetJSON(t.Context(), h.session, client.Request{
		Op:       client.OpListDevices,
		Endpoint: client.EndpointDevices,
		Path:     client.PathDevices,
	}, &devices); err != nil {
		t.Fatalf("GetJSON() = %v, want the retry to succeed", err)
	}

	if len(devices) != 1 || devices[0].DeviceID == nil || *devices[0].DeviceID != 4242 {
		t.Errorf("devices = %+v, want the retried array", devices)
	}
	if got := len(h.server.Requests()); got != 2 {
		t.Errorf("the fake received %d requests, want 2", got)
	}
}

func TestFakeServiceBoundsAGzippedResponse(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(bytes.Repeat([]byte("a"), 1<<20)); err != nil {
		t.Fatalf("gzip write = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close = %v", err)
	}

	header := make(http.Header, 1)
	header.Set("Content-Encoding", "gzip")
	script := testkit.NewScript().With(client.PathSocialProfile, testkit.Behavior{
		Status:      http.StatusOK,
		ContentType: testkit.ContentTypeJSON,
		Header:      header,
		Body:        compressed.String(),
	})
	h := newFakeHarness(t, script, client.Limits{MaxResponseBytes: 1 << 16, MaxDecompressedBytes: 1 << 16})

	_, err := h.client.Do(t.Context(), h.session, client.Request{
		Op:       client.OpGetSocialProfile,
		Endpoint: client.EndpointSocialProfile,
		Path:     client.PathSocialProfile,
	})
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Errorf("Do() = %v, want ErrResponseTooLarge for a decompression bomb", err)
	}
}

func TestFakeServiceRequestTimeoutIsCarriedIntoTheCall(t *testing.T) {
	script := testkit.NewScript().With(client.PathSocialProfile,
		testkit.JSON(http.StatusOK, testkit.SocialProfileJSON("fake-tester")).WithDelay(300*time.Millisecond))
	h := newFakeHarness(t, script, client.Limits{RequestTimeout: 50 * time.Millisecond, MaxAttempts: 1})

	_, err := h.client.Do(t.Context(), h.session, client.Request{
		Op:       client.OpGetSocialProfile,
		Endpoint: client.EndpointSocialProfile,
		Path:     client.PathSocialProfile,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Do() = %v, want the request timeout to surface as a deadline", err)
	}
	if !errors.Is(err, client.ErrTemporaryConnection) {
		t.Errorf("Do() = %v, want a temporary-connection failure", err)
	}
}

func TestFakeServiceUnscriptedPathIsANotFound(t *testing.T) {
	h := newFakeHarness(t, testkit.NewScript(), client.Limits{})

	_, err := h.client.Do(t.Context(), h.session, client.Request{
		Op:       client.OpGetUserSettings,
		Endpoint: client.EndpointUserSettings,
		Path:     client.PathUserSettings,
	})
	if !errors.Is(err, client.ErrNotFound) {
		t.Errorf("Do() = %v, want ErrNotFound for an unscripted path", err)
	}
}
