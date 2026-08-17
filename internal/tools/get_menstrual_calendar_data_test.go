package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture in this file is synthetic; see the header comment in
// get_menstrual_data_for_date_test.go.

func menstrualCalendarToolPath() string {
	return client.PathMenstrualCalendarPrefix + "/" + womensHealthTestDate + "/" + womensHealthTestDate
}

func TestGetMenstrualCalendarDataSanitizesTheDocument(t *testing.T) {
	t.Parallel()

	fixture := `{"cycles":[{"phase":"LUTEAL","ownerId":900001}]}`
	script := testkit.NewScript().With(menstrualCalendarToolPath(), testkit.JSON(http.StatusOK, fixture))
	h := newWomensHealthHarness(t, script)

	args := map[string]any{argStartDate: womensHealthTestDate, argEndDate: womensHealthTestDate}
	result := h.call(t, ToolGetMenstrualCalendarData, args)

	if got, want := result["start_date"], womensHealthTestDate; got != want {
		t.Errorf("start_date = %v, want %v", got, want)
	}
	if got, want := result["end_date"], womensHealthTestDate; got != want {
		t.Errorf("end_date = %v, want %v", got, want)
	}
	if got, ok := result["has_data"].(bool); !ok || !got {
		t.Errorf("has_data = %v, want true", result["has_data"])
	}

	document, ok := result["document"].(map[string]any)
	if !ok {
		t.Fatalf("document = %#v, want a structured object", result["document"])
	}
	cycles := list(t, document, "cycles")
	if len(cycles) != 1 {
		t.Fatalf("cycles holds %d entries, want 1", len(cycles))
	}
	cycle := entry(t, cycles, 0)
	if _, present := cycle["ownerId"]; present {
		t.Errorf("cycle %v still carries the identifying key ownerId", cycle)
	}
	if cycle["phase"] != "LUTEAL" {
		t.Errorf("cycle = %v, want phase LUTEAL preserved", cycle)
	}
	if got := number(t, result, "dropped_fields"); got != 1 {
		t.Errorf("dropped_fields = %v, want 1", got)
	}
}

func TestGetMenstrualCalendarDataReportsNoDataForAnEmptyBody(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(menstrualCalendarToolPath(), testkit.Behavior{Status: http.StatusNoContent})
	h := newWomensHealthHarness(t, script)

	args := map[string]any{argStartDate: womensHealthTestDate, argEndDate: womensHealthTestDate}
	result := h.call(t, ToolGetMenstrualCalendarData, args)
	if got, ok := result["has_data"].(bool); ok && got {
		t.Errorf("has_data = true for an empty body, want false")
	}
	if _, present := result["document"]; present {
		t.Errorf("document = %v, want absent for an empty body", result["document"])
	}
}

func TestGetMenstrualCalendarDataRefusesAnInvertedWindow(t *testing.T) {
	t.Parallel()

	h := newWomensHealthHarness(t, testkit.NewScript())

	args := map[string]any{argStartDate: "2026-02-01", argEndDate: "2026-01-01"}
	advice := h.callError(t, ToolGetMenstrualCalendarData, args)
	assertNoRawPayload(t, advice)
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0: a refused window costs no Garmin call", got)
	}
}

func TestGetMenstrualCalendarDataRefusesAWindowOverTheConfiguredBound(t *testing.T) {
	t.Parallel()

	h := newWomensHealthHarnessWith(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 1})

	args := map[string]any{argStartDate: "2026-01-01", argEndDate: "2026-03-01"}
	advice := h.callError(t, ToolGetMenstrualCalendarData, args)
	assertNoRawPayload(t, advice)
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0: a window over the bound costs no Garmin call", got)
	}
}

func TestGetMenstrualCalendarDataResultIsNotLoggable(t *testing.T) {
	t.Parallel()

	result := MenstrualCalendar{
		StartDate: womensHealthTestDate,
		EndDate:   womensHealthTestDate,
		HasData:   true,
		Document: map[string]any{
			"phase": "LUTEAL_PHASE_MARKER",
			"note":  "cramping and fatigue",
		},
	}
	assertToolResultNotLoggable(t, result, "LUTEAL_PHASE_MARKER", "cramping and fatigue")
}
