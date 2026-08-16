package api_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const solarBody = `{"deviceSolarInput":{"solarDailyDataDTOs":[` +
	`{"calendarDate":"2026-01-31","solarIntensityAvg":42.5,"solarIntensityMax":88,` +
	`"batteryCharged":3,"batteryUsed":2,"batteryNet":1}` +
	`]},"futureField":{"x":1}}`

func solarPathFor(t *testing.T, id client.ID, date client.Date) string {
	t.Helper()
	return client.PathDeviceSolarPrefix + "/" + id.String() + "/" + date.String() + "/" + date.String()
}

func TestDevicesSolarDataDecodesTheEnvelope(t *testing.T) {
	t.Parallel()

	id := mustDeviceID(t, 4242)
	date := mustDate(t, testCalendarDate)
	script := testkit.NewScript().With(solarPathFor(t, id, date), testkit.JSON(http.StatusOK, solarBody))
	h := newHarness(t, script, client.Limits{})

	days, truncated, err := newDevices(t, h).SolarData(t.Context(), h.session, id, date)
	if err != nil {
		t.Fatalf("SolarData() = %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false for one day")
	}
	if len(days) != 1 {
		t.Fatalf("%d days decoded, want 1", len(days))
	}
	if days[0].CalendarDate == nil || *days[0].CalendarDate != testCalendarDate {
		t.Errorf("CalendarDate = %v, want %q", days[0].CalendarDate, testCalendarDate)
	}
	if avg, ok := days[0].SolarIntensityAvg.Float64(); !ok || avg != 42.5 {
		t.Errorf("SolarIntensityAvg = %v/%v, want 42.5", avg, ok)
	}

	// IncludeContentTrue is the shared "true" wire value other reads already use
	// for a boolean query parameter; reused here rather than a second inline
	// literal to keep goconst quiet.
	if got := h.server.Requests()[0].Query.Get(client.QuerySingleDayView); got != client.IncludeContentTrue {
		t.Errorf("singleDayView = %q, want %q", got, client.IncludeContentTrue)
	}
}

func TestDevicesSolarDataToleratesAnAbsentEnvelope(t *testing.T) {
	t.Parallel()

	id := mustDeviceID(t, 4242)
	date := mustDate(t, testCalendarDate)
	script := testkit.NewScript().With(solarPathFor(t, id, date), testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	days, truncated, err := newDevices(t, h).SolarData(t.Context(), h.session, id, date)
	if err != nil {
		t.Fatalf("SolarData() = %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
	if days != nil {
		t.Errorf("days = %v, want nil for an absent envelope", days)
	}
}

func TestDevicesSolarDataRefusesUnsetArguments(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	devices := newDevices(t, h)
	date := mustDate(t, testCalendarDate)

	if _, _, err := devices.SolarData(t.Context(), h.session, client.ID{}, date); err == nil {
		t.Error("SolarData() without an id = nil, want an error")
	}
	if _, _, err := devices.SolarData(t.Context(), h.session, mustDeviceID(t, 4242), client.Date{}); err == nil {
		t.Error("SolarData() without a date = nil, want an error")
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
