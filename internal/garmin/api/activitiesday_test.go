package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// This file covers the two single-day reads: the account's activity count and the
// day document, whose surrounding heart-rate series the decoder drops.

const dayDocumentJSON = `{"userProfilePK":900001,"restingHeartRate":52,` +
	`"heartRateValues":[[1738296000000,61]],` +
	`"ActivitiesForDay":{"payload":[` + `{"activityId":9001,"activityName":"Morning Run",` +
	`"steps":8800,"lapCount":4,"moderateIntensityMinutes":12,` +
	`"vigorousIntensityMinutes":34,"duration":"3600"}]}}`

func TestActivitiesCountReadsTheTotal(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivitiesCount,
		testkit.JSON(http.StatusOK, `{"totalCount":"1234","unknownField":true}`))
	h := newHarness(t, script, client.Limits{})

	total, err := newActivities(t, h).Count(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Count() = %v", err)
	}
	if total != 1234 {
		t.Errorf("Count() = %d, want 1234", total)
	}
	recorded := h.server.Requests()
	if len(recorded) != 1 || recorded[0].Path != client.PathActivitiesCount {
		t.Fatalf("requests = %+v, want one read of the count path", recorded)
	}
}

// TestActivitiesCountRefusesAnAnswerWithoutATotal keeps the upstream distinction: a
// response without a count is drift, not an account with no activities.
func TestActivitiesCountRefusesAnAnswerWithoutATotal(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"no total":       `{"other":1}`,
		"negative total": `{"totalCount":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(client.PathActivitiesCount,
				testkit.JSON(http.StatusOK, body))
			h := newHarness(t, script, client.Limits{})

			_, err := newActivities(t, h).Count(t.Context(), h.session)
			if !errors.Is(err, client.ErrUnexpectedResponse) {
				t.Errorf("Count() = %v, want ErrUnexpectedResponse", err)
			}
		})
	}
}

func TestActivitiesForDateKeepsOnlyTheActivities(t *testing.T) {
	t.Parallel()

	date := mustDate(t, testCalendarDate)
	path := client.PathActivitiesForDatePrefix + "/" + testCalendarDate
	script := testkit.NewScript().With(path, testkit.JSON(http.StatusOK, dayDocumentJSON))
	h := newHarness(t, script, client.Limits{})

	activities, err := newActivities(t, h).ForDate(t.Context(), h.session, date)
	if err != nil {
		t.Fatalf("ForDate() = %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("ForDate() returned %d activities, want 1", len(activities))
	}
	if steps, ok := activities[0].Steps.Float64(); !ok || steps != 8800 {
		t.Errorf("steps = %v, want 8800", activities[0].Steps)
	}
	if laps, ok := activities[0].LapCount.Int64(); !ok || laps != 4 {
		t.Errorf("lapCount = %v, want 4", activities[0].LapCount)
	}
	if minutes, ok := activities[0].ModerateIntensityMinutes.Float64(); !ok || minutes != 12 {
		t.Errorf("moderateIntensityMinutes = %v, want 12", activities[0].ModerateIntensityMinutes)
	}
	if minutes, ok := activities[0].VigorousIntensityMinutes.Float64(); !ok || minutes != 34 {
		t.Errorf("vigorousIntensityMinutes = %v, want 34", activities[0].VigorousIntensityMinutes)
	}
}

// TestActivitiesForDateToleratesTheShapesGarminSends covers the wrapper, the
// unwrapped document, a bare array and null.
func TestActivitiesForDateToleratesTheShapesGarminSends(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body string
		want int
	}{
		"wrapped":    {`{"ActivitiesForDay":{"payload":[` + activityJSON(9001) + `]}}`, 1},
		"unwrapped":  {`{"payload":[` + activityJSON(9001) + `]}`, 1},
		"bare array": {activityArray(9001, 9002), 2},
		nullBody:     {nullBody, 0},
		"no payload": {`{"restingHeartRate":52}`, 0},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			date := mustDate(t, testCalendarDate)
			path := client.PathActivitiesForDatePrefix + "/" + testCalendarDate
			script := testkit.NewScript().With(path, testkit.JSON(http.StatusOK, testCase.body))
			h := newHarness(t, script, client.Limits{})

			activities, err := newActivities(t, h).ForDate(t.Context(), h.session, date)
			if err != nil {
				t.Fatalf("ForDate() = %v", err)
			}
			if len(activities) != testCase.want {
				t.Errorf("ForDate() returned %d activities, want %d", len(activities), testCase.want)
			}
		})
	}
}

// TestActivitiesForDateRefusesAnUnsetDate proves no empty segment reaches a URL.
func TestActivitiesForDateRefusesAnUnsetDate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	_, err := newActivities(t, h).ForDate(t.Context(), h.session, client.Date{})
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("ForDate() with a zero date = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}
