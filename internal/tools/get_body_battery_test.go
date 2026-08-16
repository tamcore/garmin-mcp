package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// bodyBatteryWindowStart is the first day of the synthetic window.
const bodyBatteryWindowStart = "2026-01-30"

// bodyBatteryDocument is synthetic: one measured day with one event and Garmin's
// dynamic feedback block. Every value is invented.
const bodyBatteryDocument = `[{"date":"` + stressDate + `","charged":56,"drained":"48",` +
	`"bodyBatteryActivityEvent":[{"eventType":"sleep",` +
	`"eventStartTimeGmt":"2026-01-30T22:05:00.0","durationInMilliseconds":28800000,` +
	`"bodyBatteryImpact":41,"shortFeedback":"GOOD"}],` +
	`"bodyBatteryDynamicFeedbackEvent":{"feedbackShortType":"STEADY","bodyBatteryLevel":62}}]`

func bodyBatteryArgs() map[string]any {
	return map[string]any{argStartDate: bodyBatteryWindowStart, argEndDate: stressDate}
}

// eventTypeSleep is the one event type key the body-battery fixtures assert on.
const eventTypeSleep = "sleep"

func TestGetBodyBatteryReturnsTheWindowsDays(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathBodyBatteryDaily,
		testkit.JSON(http.StatusOK, bodyBatteryDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetBodyBattery, bodyBatteryArgs())

	if got, _ := result["start_date"].(string); got != bodyBatteryWindowStart {
		t.Errorf("start_date = %q, want %q", got, bodyBatteryWindowStart)
	}
	if got := number(t, result, "count"); got != 1 {
		t.Fatalf("count = %v, want 1", got)
	}
	day := entry(t, list(t, result, "days"), 0)
	assertBodyBatteryDay(t, day)
}

// assertBodyBatteryDay pins the mapping of one day and its event.
func assertBodyBatteryDay(t *testing.T, day map[string]any) {
	t.Helper()

	if got := number(t, day, "charged"); got != 56 {
		t.Errorf("charged = %v, want 56", got)
	}
	if got := number(t, day, "drained"); got != 48 {
		t.Errorf("drained = %v, want 48 from the string form", got)
	}
	if got := number(t, day, "body_battery_level"); got != 62 {
		t.Errorf("body_battery_level = %v, want 62", got)
	}
	if got, _ := day["current_feedback"].(string); got != "STEADY" {
		t.Errorf("current_feedback = %q, want STEADY", got)
	}

	event := entry(t, list(t, day, "events"), 0)
	if got, _ := event["type"].(string); got != eventTypeSleep {
		t.Errorf("event type = %q, want sleep", got)
	}
	// Source: the upstream curation, which renders the duration in minutes rounded
	// to one decimal. 28800000 ms is eight hours.
	if got := number(t, event, "duration_minutes"); got != 480 {
		t.Errorf("duration_minutes = %v, want 480", got)
	}
	if got := number(t, event, "body_battery_impact"); got != 41 {
		t.Errorf("body_battery_impact = %v, want 41", got)
	}
}

// TestGetBodyBatterySendsTheWindowAsQueryParameters pins the keyed read: the endpoint
// filters by startDate and endDate rather than by a path segment.
func TestGetBodyBatterySendsTheWindowAsQueryParameters(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathBodyBatteryDaily,
		testkit.JSON(http.StatusOK, bodyBatteryDocument))
	h := newToolHarness(t, script)

	h.call(t, ToolGetBodyBattery, bodyBatteryArgs())

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStartDate); got != bodyBatteryWindowStart {
		t.Errorf("startDate = %q, want %q", got, bodyBatteryWindowStart)
	}
	if got := requests[0].Query.Get(client.QueryEndDate); got != stressDate {
		t.Errorf("endDate = %q, want %q", got, stressDate)
	}
}

// TestGetBodyBatteryReportsAQuietWindowAsEmpty proves a window with no wearable data is
// a normal answer.
func TestGetBodyBatteryReportsAQuietWindowAsEmpty(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{caseEmptyArray: `[]`, jsonNull: jsonNull} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(client.PathBodyBatteryDaily,
				testkit.JSON(http.StatusOK, body))
			h := newToolHarness(t, script)

			result := h.call(t, ToolGetBodyBattery, bodyBatteryArgs())
			if got := number(t, result, "count"); got != 0 {
				t.Errorf("count = %v, want 0", got)
			}
			if got := len(list(t, result, "days")); got != 0 {
				t.Errorf("days holds %d entries, want none", got)
			}
		})
	}
}

// TestGetBodyBatteryBoundsTheWindow proves an over-wide window is refused before
// dispatch rather than answered in part.
func TestGetBodyBatteryBoundsTheWindow(t *testing.T) {
	t.Parallel()

	h := newToolHarnessWith(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 3})

	advice := h.callError(t, ToolGetBodyBattery,
		map[string]any{argStartDate: scoresStartDate, argEndDate: stressDate})
	assertNoRawPayload(t, advice)
	if !strings.Contains(advice, "window") && !strings.Contains(advice, "date") {
		t.Errorf("the refusal %q does not say the window was the problem", advice)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestGetBodyBatteryRefusesAnInvertedWindow proves the two dates are checked against
// each other, not only for shape.
func TestGetBodyBatteryRefusesAnInvertedWindow(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript())

	advice := h.callError(t, ToolGetBodyBattery,
		map[string]any{argStartDate: stressDate, argEndDate: bodyBatteryWindowStart})
	assertNoRawPayload(t, advice)
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestGetBodyBatteryBoundsADaysEvents proves a day with an implausible event list is
// cut and says so.
func TestGetBodyBatteryBoundsADaysEvents(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, maxBodyBatteryDayEvents+5)
	for range maxBodyBatteryDayEvents + 5 {
		events = append(events, `{"eventType":"nap","bodyBatteryImpact":1}`)
	}
	body := `[{"date":"` + stressDate + `","bodyBatteryActivityEvent":[` +
		strings.Join(events, ",") + `]}]`
	script := testkit.NewScript().With(client.PathBodyBatteryDaily,
		testkit.JSON(http.StatusOK, body))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetBodyBattery, bodyBatteryArgs())

	day := entry(t, list(t, result, "days"), 0)
	if got := number(t, day, "event_count"); got != maxBodyBatteryDayEvents {
		t.Errorf("event_count = %v, want the bound %d", got, maxBodyBatteryDayEvents)
	}
	if truncated, _ := day["events_truncated"].(bool); !truncated {
		t.Error("events_truncated = false, want true for a cut event list")
	}
}
