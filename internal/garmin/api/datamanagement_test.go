package api_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func newDataManagement(t *testing.T, h harness) *api.DataManagement {
	t.Helper()

	d, err := api.NewDataManagement(h.rc)
	if err != nil {
		t.Fatalf("NewDataManagement() = %v", err)
	}
	return d
}

func TestNewDataManagementRefusesANilRequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewDataManagement(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Fatalf("NewDataManagement(nil) = %v, want %v", err, client.ErrNotConfigured)
	}
}

// TestAddBodyCompositionUploadsAFITFile pins add_body_composition's own
// multipart upload target (garminconnect/__init__.py:1213-1216, POST
// "/upload-service/upload" with a "file" part).
func TestAddBodyCompositionUploadsAFITFile(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathUpload, testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	entry := api.BodyCompositionEntry{At: time.Now(), Weight: 70}
	if _, err := newDataManagement(t, h).AddBodyComposition(t.Context(), h.session, entry); err != nil {
		t.Fatalf("AddBodyComposition() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(requests))
	}
	if requests[0].Method != http.MethodPost {
		t.Errorf("method = %q, want POST", requests[0].Method)
	}
	if !strings.Contains(requests[0].Header.Get("Content-Type"), "multipart/form-data") {
		t.Errorf("content type = %q, want multipart/form-data", requests[0].Header.Get("Content-Type"))
	}
	if len(requests[0].Body) == 0 {
		t.Errorf("the request carried no body")
	}
}

func TestAddBodyCompositionRefusesInvalidWeight(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	entry := api.BodyCompositionEntry{At: time.Now(), Weight: -5}
	_, err := newDataManagement(t, h).AddBodyComposition(t.Context(), h.session, entry)
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("AddBodyComposition() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestSetBloodPressureSendsTheUpstreamPayload pins
// set_blood_pressure's POST body (0310-__init__.py:1388-1396).
func TestSetBloodPressureSendsTheUpstreamPayload(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathBloodPressureSet, testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	at := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	entry := api.BloodPressureEntry{Systolic: 120, Diastolic: 80, Pulse: 65, Notes: "resting", At: at}
	if _, err := newDataManagement(t, h).SetBloodPressure(t.Context(), h.session, entry); err != nil {
		t.Fatalf("SetBloodPressure() = %v", err)
	}

	body := decodeBody(t, h.server.Requests()[0].Body)
	want := map[string]any{
		"systolic": float64(120), "diastolic": float64(80), "pulse": float64(65),
		"sourceType": "MANUAL", "notes": "resting",
		"measurementTimestampLocal": "2026-03-01T08:00:00.000",
		"measurementTimestampGMT":   "2026-03-01T08:00:00.000",
	}
	for key, wantValue := range want {
		if body[key] != wantValue {
			t.Errorf("%s = %v, want %v", key, body[key], wantValue)
		}
	}
}

func TestSetBloodPressureRefusesOutOfRangeReadings(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	at := time.Now()
	cases := []api.BloodPressureEntry{
		{Systolic: 300, Diastolic: 80, Pulse: 65, At: at},
		{Systolic: 120, Diastolic: 200, Pulse: 65, At: at},
		{Systolic: 120, Diastolic: 80, Pulse: 5, At: at},
	}
	for _, entry := range cases {
		_, err := newDataManagement(t, h).SetBloodPressure(t.Context(), h.session, entry)
		if !errors.Is(err, client.ErrValidation) {
			t.Errorf("SetBloodPressure(%+v) = %v, want ErrValidation", entry, err)
		}
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestAddHydrationDataSendsTheUpstreamPayload pins add_hydration_data's PUT
// body (0310-__init__.py:1691-1695).
func TestAddHydrationDataSendsTheUpstreamPayload(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathHydrationSet, testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	date := mustDate(t, "2026-03-01")
	at := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	entry := api.HydrationEntry{ValueInML: 250, Date: date, At: at}
	if _, err := newDataManagement(t, h).AddHydrationData(t.Context(), h.session, entry); err != nil {
		t.Fatalf("AddHydrationData() = %v", err)
	}

	requests := h.server.Requests()
	if requests[0].Method != http.MethodPut {
		t.Errorf("method = %q, want PUT", requests[0].Method)
	}
	body := decodeBody(t, requests[0].Body)
	if body["calendarDate"] != "2026-03-01" || body["valueInML"] != float64(250) {
		t.Errorf("body = %v, want calendarDate=2026-03-01, valueInML=250", body)
	}
	if body["timestampLocal"] != "2026-03-01T09:30:00.000" {
		t.Errorf("timestampLocal = %v, want 2026-03-01T09:30:00.000", body["timestampLocal"])
	}
}

func TestAddHydrationDataRefusesAMismatchedDate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	date := mustDate(t, "2026-03-02")
	at := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	entry := api.HydrationEntry{ValueInML: 250, Date: date, At: at}
	_, err := newDataManagement(t, h).AddHydrationData(t.Context(), h.session, entry)
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("AddHydrationData() = %v, want ErrValidation", err)
	}
}

func TestAddHydrationDataRefusesAnUnreasonableVolume(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	date := mustDate(t, "2026-03-01")
	at := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	entry := api.HydrationEntry{ValueInML: 20000, Date: date, At: at}
	_, err := newDataManagement(t, h).AddHydrationData(t.Context(), h.session, entry)
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("AddHydrationData() = %v, want ErrValidation", err)
	}
}
