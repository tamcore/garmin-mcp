package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestGetAllDayEventsPassesEventsThroughWhole proves the tool renames nothing: upstream
// curates no field of this document, so inventing names here would be a fabricated
// schema.
func TestGetAllDayEventsPassesEventsThroughWhole(t *testing.T) {
	t.Parallel()

	body := `[{"eventType":"AUTO_DETECTED","unknownField":true}]`
	script := testkit.NewScript().With(client.PathDailyEvents, testkit.JSON(http.StatusOK, body))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetAllDayEvents, stressArgs())

	if got, _ := result[argDate].(string); got != stressDate {
		t.Errorf("date = %q, want %q", got, stressDate)
	}
	if got := number(t, result, "count"); got != 1 {
		t.Fatalf("count = %v, want 1", got)
	}
	first := entry(t, list(t, result, "events"), 0)
	if got, _ := first["eventType"].(string); got != "AUTO_DETECTED" {
		t.Errorf("events[0].eventType = %v, want the key Garmin sent", first["eventType"])
	}
	if _, present := first["unknownField"]; !present {
		t.Error("an unrecognized key was dropped, want the event kept whole")
	}
}

// TestGetAllDayEventsSendsTheDayAsAQueryParameter pins the keyed read: this endpoint
// takes calendarDate as a parameter rather than as a path segment.
func TestGetAllDayEventsSendsTheDayAsAQueryParameter(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDailyEvents, testkit.JSON(http.StatusOK, `[]`))
	h := newToolHarness(t, script)

	h.call(t, ToolGetAllDayEvents, stressArgs())

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want exactly one", len(requests))
	}
	if requests[0].Path != client.PathDailyEvents {
		t.Errorf("path = %q, want %q", requests[0].Path, client.PathDailyEvents)
	}
	if got := requests[0].Query.Get(client.QueryCalendarDate); got != stressDate {
		t.Errorf("calendarDate = %q, want %q", got, stressDate)
	}
}

// TestGetAllDayEventsNormalizesTheAnswerShape proves the array, single-object and null
// forms all yield a list.
func TestGetAllDayEventsNormalizesTheAnswerShape(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body string
		want float64
	}{
		"array":         {`[{"eventType":"AUTO_DETECTED"}]`, 1},
		"single object": {`{"eventType":"AUTO_DETECTED"}`, 1},
		jsonNull:        {jsonNull, 0},
		caseEmptyArray:  {`[]`, 0},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(client.PathDailyEvents,
				testkit.JSON(http.StatusOK, testCase.body))
			h := newToolHarness(t, script)

			result := h.call(t, ToolGetAllDayEvents, stressArgs())
			if got := number(t, result, "count"); got != testCase.want {
				t.Errorf("count = %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestGetAllDayEventsBoundsTheList proves a long day is cut and says so.
func TestGetAllDayEventsBoundsTheList(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, maxWellnessEvents+5)
	for range maxWellnessEvents + 5 {
		events = append(events, `{"eventType":"AUTO_DETECTED"}`)
	}
	script := testkit.NewScript().With(client.PathDailyEvents,
		testkit.JSON(http.StatusOK, "["+strings.Join(events, ",")+"]"))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetAllDayEvents, stressArgs())

	if got := number(t, result, "count"); got != maxWellnessEvents {
		t.Errorf("count = %v, want the bound %d", got, maxWellnessEvents)
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true for a cut event list")
	}
}

// TestGetAllDayEventsSanitizesAGarminFailure proves no raw body reaches the caller.
func TestGetAllDayEventsSanitizesAGarminFailure(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDailyEvents,
		testkit.JSON(http.StatusTooManyRequests, `{"error":"synthetic","retry":"soon"}`))
	h := newToolHarness(t, script)

	advice := h.callError(t, ToolGetAllDayEvents, stressArgs())
	assertNoRawPayload(t, advice)
	if !strings.Contains(strings.ToLower(advice), "rate") {
		t.Errorf("advice = %q, want it to name the rate limit", advice)
	}
}

// TestGetAllDayEventsDropsIdentifyingFields is the same passthrough-egress
// regression as the body-battery one: an unsourced event shape must still not carry
// an account identifier or a coordinate out of the server.
func TestGetAllDayEventsDropsIdentifyingFields(t *testing.T) {
	t.Parallel()

	body := `[{"eventType":"AUTO_DETECTED","userProfilePK":900001,` +
		`"nested":{"userId":4242,"lat":1.0,"lon":2.0,"steps":11}}]`
	script := testkit.NewScript().With(client.PathDailyEvents, testkit.JSON(http.StatusOK, body))
	h := newToolHarness(t, script)

	rendered := h.text(t, ToolGetAllDayEvents, stressArgs())
	for _, forbidden := range []string{
		keyUserProfilePK, fixtureProfilePK, keyCamelUserID, fixtureIdentifier,
		`"lat"`, `"lon"`,
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the result carries %q, which identifies an account or a place", forbidden)
		}
	}

	result := h.call(t, ToolGetAllDayEvents, stressArgs())
	if got := number(t, result, "dropped_fields"); got != 4 {
		t.Errorf("dropped_fields = %v, want 4", got)
	}
	if !strings.Contains(rendered, "steps") {
		t.Error("the sanitiser dropped a reading key, want only the identifiers removed")
	}
}
