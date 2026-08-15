package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func bodyBatteryEventsPath() string {
	return client.PathBodyBatteryEventsPrefix + "/" + stressDate
}

// TestGetBodyBatteryEventsPassesEventsThroughWhole proves the tool renames nothing:
// upstream curates no field of this document, so inventing names here would be a
// fabricated schema.
func TestGetBodyBatteryEventsPassesEventsThroughWhole(t *testing.T) {
	t.Parallel()

	body := `[{"eventType":"sleep","unknownField":1},{"eventType":"nap"}]`
	script := testkit.NewScript().With(bodyBatteryEventsPath(),
		testkit.JSON(http.StatusOK, body))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetBodyBatteryEvents, stressArgs())

	if got, _ := result[argDate].(string); got != stressDate {
		t.Errorf("date = %q, want %q", got, stressDate)
	}
	if got := number(t, result, "count"); got != 2 {
		t.Fatalf("count = %v, want 2", got)
	}
	first := entry(t, list(t, result, "events"), 0)
	if got, _ := first["eventType"].(string); got != eventTypeSleep {
		t.Errorf("events[0].eventType = %v, want the key Garmin sent", first["eventType"])
	}
	if _, present := first["unknownField"]; !present {
		t.Error("an unrecognized key was dropped, want the event kept whole")
	}
}

// TestGetBodyBatteryEventsNormalizesTheAnswerShape proves the array, single-object and
// null forms all yield a list, which is what a caller can rely on.
func TestGetBodyBatteryEventsNormalizesTheAnswerShape(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body string
		want float64
	}{
		"array":         {`[{"eventType":"sleep"}]`, 1},
		"single object": {`{"eventType":"sleep"}`, 1},
		jsonNull:        {jsonNull, 0},
		caseEmptyArray:  {`[]`, 0},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(bodyBatteryEventsPath(),
				testkit.JSON(http.StatusOK, testCase.body))
			h := newToolHarness(t, script)

			result := h.call(t, ToolGetBodyBatteryEvents, stressArgs())
			if got := number(t, result, "count"); got != testCase.want {
				t.Errorf("count = %v, want %v", got, testCase.want)
			}
			if truncated, _ := result["truncated"].(bool); truncated {
				t.Error("truncated = true, want false for a short list")
			}
		})
	}
}

// TestGetBodyBatteryEventsBoundsTheList proves an implausibly long day is cut and says
// so rather than being handed on whole.
func TestGetBodyBatteryEventsBoundsTheList(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, maxWellnessEvents+5)
	for range maxWellnessEvents + 5 {
		events = append(events, `{"eventType":"nap"}`)
	}
	script := testkit.NewScript().With(bodyBatteryEventsPath(),
		testkit.JSON(http.StatusOK, "["+strings.Join(events, ",")+"]"))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetBodyBatteryEvents, stressArgs())

	if got := number(t, result, "count"); got != maxWellnessEvents {
		t.Errorf("count = %v, want the bound %d", got, maxWellnessEvents)
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true for a cut event list")
	}
}

// TestGetBodyBatteryEventsReadsTheDateKeyedPath proves the day is a path segment, and
// that the read needs no profile call.
func TestGetBodyBatteryEventsReadsTheDateKeyedPath(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(bodyBatteryEventsPath(), testkit.JSON(http.StatusOK, `[]`))
	h := newToolHarness(t, script)

	h.call(t, ToolGetBodyBatteryEvents, stressArgs())

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want exactly one", len(requests))
	}
	if requests[0].Path != bodyBatteryEventsPath() {
		t.Errorf("path = %q, want %q", requests[0].Path, bodyBatteryEventsPath())
	}
}

// TestGetBodyBatteryEventsSanitizesAGarminFailure proves no raw body reaches the
// caller, which matters most for a passthrough tool.
func TestGetBodyBatteryEventsSanitizesAGarminFailure(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(bodyBatteryEventsPath(),
		testkit.JSON(http.StatusUnauthorized, `{"error":"synthetic","cookie":"GARMIN-SSO=abc"}`))
	h := newToolHarness(t, script)

	advice := h.callError(t, ToolGetBodyBatteryEvents, stressArgs())
	assertNoRawPayload(t, advice)
	if strings.Contains(advice, "GARMIN-SSO") {
		t.Error("the refusal carries the scripted cookie")
	}
}

// TestGetBodyBatteryEventsDropsIdentifyingFields is the passthrough-egress
// regression. The event shape is unsourced, so the tool keeps Garmin's own names —
// which means an account identifier or a coordinate would leave this server unread
// unless the shared sanitiser removes it first.
func TestGetBodyBatteryEventsDropsIdentifyingFields(t *testing.T) {
	t.Parallel()

	body := `[{"eventType":"sleep","userProfilePK":900001,` +
		`"startLatitude":1.5,"endLongitude":-2.5,` +
		`"detail":{"ownerDisplayName":"fake-tester","level":3}}]`
	script := testkit.NewScript().With(bodyBatteryEventsPath(),
		testkit.JSON(http.StatusOK, body))
	h := newToolHarness(t, script)

	rendered := h.text(t, ToolGetBodyBatteryEvents, stressArgs())
	for _, forbidden := range []string{
		keyUserProfilePK, fixtureProfilePK, keyStartLatitude, keyEndLongitude,
		keyOwnerDisplay, cardioDisplayName,
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the result carries %q, which identifies an account or a place", forbidden)
		}
	}

	result := h.call(t, ToolGetBodyBatteryEvents, stressArgs())
	if got := number(t, result, "dropped_fields"); got != 4 {
		t.Errorf("dropped_fields = %v, want 4", got)
	}
	first := entry(t, list(t, result, "events"), 0)
	if got, _ := first["eventType"].(string); got != eventTypeSleep {
		t.Errorf("events[0].eventType = %v, want the key Garmin sent", first["eventType"])
	}
}
