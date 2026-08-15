package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The body-battery and training-readiness halves of the stress client are covered here;
// the three views of the daily stress read and the weekly aggregate are covered in
// wellnessstress_test.go, which also holds the helpers both files share.

// Synthetic body-battery and readiness fixtures. Every value is invented.
const (
	bodyBatteryBody = `[{"date":"` + testCalendarDate + `","charged":56,"drained":"48",` +
		`"bodyBatteryActivityEvent":[{"eventType":"sleep","eventStartTimeGmt":"2026-01-30T22:05:00.0",` +
		`"durationInMilliseconds":28800000,"bodyBatteryImpact":41,"shortFeedback":"GOOD"}],` +
		`"bodyBatteryDynamicFeedbackEvent":{"feedbackShortType":"STEADY","bodyBatteryLevel":62}}]`

	readinessBody = `[{"calendarDate":"` + testCalendarDate + `","timestampLocal":"2026-01-31T06:40:00.0",` +
		`"inputContext":"MANUAL_RESET","level":"MODERATE","score":58,"feedbackShort":"OK",` +
		`"sleepScore":71,"recoveryTime":180,"acwrFactorPercent":12,"hrvFactorPercent":"9",` +
		`"hrvWeeklyAverage":74,"acuteLoad":330},` +
		`{"calendarDate":"` + testCalendarDate + `","inputContext":"AFTER_WAKEUP_RESET",` +
		`"level":"HIGH","score":83,"sleepScore":88}]`
)

func readinessPath() string { return client.PathTrainingReadinessPrefix + "/" + testCalendarDate }

func TestWellnessStressBodyBatteryFiltersByTheWindow(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathBodyBatteryDaily,
		testkit.JSON(http.StatusOK, bodyBatteryBody))
	h := newHarness(t, script, client.Limits{MaxDateRangeDays: 31})

	span, err := client.NewDateRange(mustDate(t, "2026-01-30"), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}

	got, err := newStressClient(t, h).BodyBattery(t.Context(), h.session, span)
	if err != nil {
		t.Fatalf("BodyBattery() = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("%d days decoded, want 1", len(got))
	}
	if drained, ok := got[0].Drained.Int64(); !ok || drained != 48 {
		t.Errorf("Drained = %v/%v, want 48 from the string form", drained, ok)
	}
	if got[0].ActivityEvents.Len() != 1 {
		t.Fatalf("%d events decoded, want 1", got[0].ActivityEvents.Len())
	}
	if got[0].DynamicFeedback == nil {
		t.Fatal("DynamicFeedback = nil, want the decoded block")
	}
	if level, ok := got[0].DynamicFeedback.BodyBatteryLevel.Int64(); !ok || level != 62 {
		t.Errorf("BodyBatteryLevel = %v/%v, want 62", level, ok)
	}

	query := h.server.Requests()[0].Query
	if query.Get(client.QueryStartDate) != "2026-01-30" ||
		query.Get(client.QueryEndDate) != testCalendarDate {
		t.Errorf("window query = %v, want the inclusive window", query)
	}
}

// TestWellnessStressRetainsUncuratedEventsWhole covers both event reads: upstream
// establishes no element shape, so the array, single-object and null forms all
// normalize without inventing field names.
func TestWellnessStressRetainsUncuratedEventsWhole(t *testing.T) {
	t.Parallel()

	eventsPath := client.PathBodyBatteryEventsPrefix + "/" + testCalendarDate
	cases := map[string]struct {
		body string
		want int
	}{
		"array":         {`[{"a":1},{"b":2}]`, 2},
		"single object": {`{"a":1}`, 1},
		nullBody:        {nullBody, 0},
		emptyBody:       {`[]`, 0},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().
				With(eventsPath, testkit.JSON(http.StatusOK, testCase.body)).
				With(client.PathDailyEvents, testkit.JSON(http.StatusOK, testCase.body))
			h := newHarness(t, script, client.Limits{})
			stress := newStressClient(t, h)
			date := mustDate(t, testCalendarDate)

			battery, err := stress.BodyBatteryEvents(t.Context(), h.session, date)
			if err != nil {
				t.Fatalf("BodyBatteryEvents() = %v", err)
			}
			if len(battery) != testCase.want {
				t.Errorf("%d body-battery events, want %d", len(battery), testCase.want)
			}

			daily, err := stress.AllDayEvents(t.Context(), h.session, date)
			if err != nil {
				t.Fatalf("AllDayEvents() = %v", err)
			}
			if len(daily) != testCase.want {
				t.Errorf("%d daily events, want %d", len(daily), testCase.want)
			}
			// The daily events endpoint takes the day as a parameter rather
			// than as a path segment.
			for _, request := range h.server.Requests() {
				if request.Path != client.PathDailyEvents {
					continue
				}
				if got := request.Query.Get(client.QueryCalendarDate); got != testCalendarDate {
					t.Errorf("calendarDate = %q, want %q", got, testCalendarDate)
				}
			}
		})
	}
}

func TestWellnessStressTrainingReadinessDecodesEverySnapshot(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(readinessPath(), testkit.JSON(http.StatusOK, readinessBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newStressClient(t, h).TrainingReadiness(t.Context(), h.session,
		mustDate(t, testCalendarDate), api.ReadinessViewAll)
	if err != nil {
		t.Fatalf("TrainingReadiness() = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("%d snapshots decoded, want 2", len(got))
	}
	if score, ok := got[0].Score.Int64(); !ok || score != 58 {
		t.Errorf("Score = %v/%v, want 58", score, ok)
	}
	if percent, ok := got[0].HRVFactorPercent.Int64(); !ok || percent != 9 {
		t.Errorf("HRVFactorPercent = %v/%v, want 9 from the string form", percent, ok)
	}
	if got[1].RecoveryTime.IsSet() {
		t.Error("an absent recovery time must stay absent, not become a zero")
	}
}

// TestWellnessStressMorningReadinessFollowsUpstreamSelection pins the documented
// behaviour: the AFTER_WAKEUP_RESET entry wins, and the first entry is the fallback.
func TestWellnessStressMorningReadinessFollowsUpstreamSelection(t *testing.T) {
	t.Parallel()

	entries := decodeReadiness(t, readinessBody)
	entry, matched, ok := api.MorningReadiness(entries)
	if !ok || !matched {
		t.Fatalf("MorningReadiness() = matched %v, ok %v, want the wake-up entry", matched, ok)
	}
	if score, _ := entry.Score.Int64(); score != 83 {
		t.Errorf("selected score = %v, want the AFTER_WAKEUP_RESET snapshot", score)
	}

	fallback := decodeReadiness(t, `[{"score":41},{"score":42}]`)
	entry, matched, ok = api.MorningReadiness(fallback)
	if !ok || matched {
		t.Fatalf("MorningReadiness() = matched %v, ok %v, want the fallback", matched, ok)
	}
	if score, _ := entry.Score.Int64(); score != 41 {
		t.Errorf("fallback score = %v, want the first entry", score)
	}

	if _, _, ok := api.MorningReadiness(nil); ok {
		t.Error("MorningReadiness(nil) reported an entry, want none")
	}
}

func decodeReadiness(t *testing.T, body string) []api.Readiness {
	t.Helper()

	var entries []api.Readiness
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		t.Fatalf("decoding the readiness fixture: %v", err)
	}
	return entries
}
