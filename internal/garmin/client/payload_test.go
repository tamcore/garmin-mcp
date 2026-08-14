package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

const (
	sleepSentinel  = "SENTINEL-SLEEP-PAYLOAD-4f10"
	coordSentinel  = "48.137154,11.576124"
	sleepBodyShape = `{"dailySleepDTO":{"calendarDate":"2026-01-31","sleepTimeSeconds":27000,` +
		`"note":"` + sleepSentinel + `","startPoint":"` + coordSentinel + `"}}`
)

func fetchSleepPayload(t *testing.T) client.Payload {
	t.Helper()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK,
		header: jsonHeader(),
		body:   []byte(sleepBodyShape),
	}}}
	payload, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}
	return payload
}

// TestPayloadNeverRendersItsBody covers the redacting renderers and the
// method-stripping alias, the way internal/garmin/protocol/alias_leak_test.go does
// for protocol's own sealed types: a type conversion drops String, GoString,
// MarshalJSON and LogValue, and fmt's badVerb path then re-prints the value at
// depth 0.
func TestPayloadNeverRendersItsBody(t *testing.T) {
	t.Parallel()

	payload := fetchSleepPayload(t)

	marshaled, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("read", "payload", payload)

	type stripped client.Payload
	rendered := map[string]string{
		"String":      payload.String(),
		"GoString":    payload.GoString(),
		"MarshalJSON": string(marshaled),
		"LogValue":    logged.String(),
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d"} {
		rendered["value "+verb] = fmt.Sprintf(verb, payload)
		rendered["stripped "+verb] = fmt.Sprintf(verb, stripped(payload))
		rendered["nested "+verb] = fmt.Sprintf(verb, struct{ P stripped }{stripped(payload)})
		rendered["slice "+verb] = fmt.Sprintf(verb, []stripped{stripped(payload)})
	}

	needles := []string{
		sleepSentinel, coordSentinel, "sleepTimeSeconds", "27000",
		decimalBytesOf(sleepSentinel), decimalBytesOf(coordSentinel),
	}
	for label, value := range rendered {
		for _, needle := range needles {
			if strings.Contains(value, needle) {
				t.Errorf("%s leaks %q: %s", label, needle, value)
			}
		}
	}
}

// TestPayloadStillReportsItsBodyToAuthorizedCallers is the counter-test: the fix
// must not become "hide everything", because decoding needs the bytes.
func TestPayloadStillReportsItsBodyToAuthorizedCallers(t *testing.T) {
	t.Parallel()

	payload := fetchSleepPayload(t)
	if got := string(payload.Bytes()); got != sleepBodyShape {
		t.Errorf("Bytes() = %q, want the body the accessor exists to return", got)
	}
	if payload.ContentType() != mediaTypeJSON {
		t.Errorf("ContentType() = %q, want application/json", payload.ContentType())
	}
	if payload.Op() != client.OpGetSocialProfile || payload.Endpoint() != client.EndpointSocialProfile {
		t.Errorf("labels = %q/%q, want the request's labels", payload.Op(), payload.Endpoint())
	}
}

func TestPayloadBytesReturnsACopy(t *testing.T) {
	t.Parallel()

	payload := fetchSleepPayload(t)
	first := payload.Bytes()
	first[0] = 'X'
	if second := payload.Bytes(); string(second) != sleepBodyShape {
		t.Error("Bytes() shares storage with the payload: a caller mutated it")
	}
}

func TestZeroPayloadIsInert(t *testing.T) {
	t.Parallel()

	var payload client.Payload
	if payload.Status() != 0 || payload.Len() != 0 || payload.ContentType() != "" {
		t.Error("the zero Payload must report zero results")
	}
	if !payload.NoContent() {
		t.Error("the zero Payload must report no content")
	}
	if payload.String() == "" {
		t.Error("the zero Payload must still render a shape")
	}
}

func TestGetJSONDecodesInOneStep(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK,
		header: jsonHeader(),
		body:   []byte(profileBody),
	}}}

	var profile tolerantProfile
	payload, err := newTestClient(t, client.Limits{}).
		GetJSON(t.Context(), mustSession(t, caller), profileRequest(), &profile)
	if err != nil {
		t.Fatalf("GetJSON() = %v", err)
	}
	if profile.DisplayName == nil || *profile.DisplayName != fakeDisplayName {
		t.Errorf("DisplayName = %v, want the decoded value", profile.DisplayName)
	}
	if payload.Len() != len(profileBody) {
		t.Errorf("the raw payload was not retained: Len() = %d", payload.Len())
	}
}

func TestGetJSONPropagatesTheAPIError(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusNotFound, header: jsonHeader()}}}
	var profile tolerantProfile
	_, err := newTestClient(t, client.Limits{MaxAttempts: 1}).
		GetJSON(t.Context(), mustSession(t, caller), profileRequest(), &profile)
	if !errors.Is(err, client.ErrNotFound) {
		t.Errorf("GetJSON() = %v, want ErrNotFound", err)
	}
}

func TestFanOutBoundsConcurrency(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, client.Limits{MaxConcurrency: 2})

	var mu sync.Mutex
	var running, peak int
	var done atomic.Int64
	release := make(chan struct{})
	var once sync.Once

	err := c.FanOut(t.Context(), 6, func(context.Context, int) error {
		mu.Lock()
		running++
		if running > peak {
			peak = running
		}
		reached := running
		mu.Unlock()

		if reached == 2 {
			once.Do(func() { close(release) })
		}
		select {
		case <-release:
		case <-t.Context().Done():
		}

		mu.Lock()
		running--
		mu.Unlock()
		done.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("FanOut() = %v", err)
	}
	if got := done.Load(); got != 6 {
		t.Errorf("%d tasks ran, want 6", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if peak > 2 {
		t.Errorf("peak concurrency = %d, want at most the configured 2", peak)
	}
}

func TestFanOutReportsTheFirstFailureAndStops(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, client.Limits{MaxConcurrency: 1})
	boom := errors.New("synthetic task failure")

	var started atomic.Int64
	err := c.FanOut(t.Context(), 4, func(context.Context, int) error {
		if started.Add(1) == 1 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Errorf("FanOut() = %v, want the task failure", err)
	}
	if got := started.Load(); got > 2 {
		t.Errorf("%d tasks started after a failure, want the fan-out to stop", got)
	}
}

func TestFanOutValidatesItsArguments(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, client.Limits{})
	if err := c.FanOut(t.Context(), -1, func(context.Context, int) error { return nil }); err != nil {
		t.Errorf("FanOut() with no work = %v, want nil", err)
	}
	if err := c.FanOut(t.Context(), 1, nil); !errors.Is(err, client.ErrValidation) {
		t.Errorf("FanOut() with no task = %v, want a validation error", err)
	}
}
