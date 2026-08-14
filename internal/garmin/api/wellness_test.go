package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func sleepBody() string {
	return `{"dailySleepDTO":{"id":1,"calendarDate":"` + testCalendarDate + `","sleepTimeSeconds":27000,` +
		`"deepSleepSeconds":"5400","remSleepSeconds":null,"sleepScores":{"overall":{"value":78}}},` +
		`"sleepLevels":[{"startGMT":"` + testCalendarDate + `T22:10:00.0"}],"unknownBlock":{"x":1}}`
}

func summaryBody(privacyProtected bool) string {
	protected := "false"
	if privacyProtected {
		protected = "true"
	}
	return `{"userProfileId":900001,"calendarDate":"` + testCalendarDate + `","totalSteps":8342,` +
		`"restingHeartRate":"58","totalKilocalories":2410.0,"privacyProtected":` + protected + `}`
}

func sleepPath() string {
	return client.PathDailySleepPrefix + "/" + fakeDisplayName
}

func summaryPath() string {
	return client.PathUserSummaryPrefix + "/" + fakeDisplayName
}

func newWellness(t *testing.T, h harness) *api.Wellness {
	t.Helper()

	wellness, err := api.NewWellness(h.rc)
	if err != nil {
		t.Fatalf("NewWellness() = %v", err)
	}
	return wellness
}

func TestWellnessDailySleepDecodesADateKeyedObject(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(sleepPath(), testkit.JSON(http.StatusOK, sleepBody()))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellness(t, h).DailySleep(t.Context(), h.session, mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("DailySleep() = %v", err)
	}

	if got.Summary == nil {
		t.Fatal("Summary = nil, want the nested dailySleepDTO")
	}
	if got.Summary.CalendarDate == nil || *got.Summary.CalendarDate != testCalendarDate {
		t.Errorf("CalendarDate = %v, want %q", got.Summary.CalendarDate, testCalendarDate)
	}
	if seconds, ok := got.Summary.SleepTimeSeconds.Float64(); !ok || seconds != 27000 {
		t.Errorf("SleepTimeSeconds = %v/%v, want 27000", seconds, ok)
	}
	if deep, ok := got.Summary.DeepSleepSeconds.Float64(); !ok || deep != 5400 {
		t.Errorf("DeepSleepSeconds = %v/%v, want 5400 from the string form", deep, ok)
	}
	if got.Summary.RemSleepSeconds.IsSet() {
		t.Error("RemSleepSeconds must report absent for an explicit null")
	}
	if len(got.SleepLevels) == 0 {
		t.Error("SleepLevels raw shape was not retained")
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryDate); got != testCalendarDate {
		t.Errorf("date = %q, want %q", got, testCalendarDate)
	}
	// Source: the nonSleepBufferMinutes=60 parameter get_sleep_data always sends.
	if got := requests[0].Query.Get(client.QueryNonSleepBufferMinutes); got != "60" {
		t.Errorf("nonSleepBufferMinutes = %q, want 60", got)
	}
}

func TestWellnessDailySleepRequiresADisplayNameAndADate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	wellness := newWellness(t, h)

	if _, err := wellness.DailySleep(t.Context(), h.session, client.DisplayName{},
		mustDate(t, testCalendarDate)); !errors.Is(err, client.ErrValidation) {
		t.Errorf("DailySleep() with no display name = %v, want a validation error", err)
	}
	if _, err := wellness.DailySleep(t.Context(), h.session, mustDisplayName(t),
		client.Date{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("DailySleep() with no date = %v, want a validation error", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestWellnessDailySleepRangeFansOutUnderItsBounds(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(sleepPath(), testkit.JSON(http.StatusOK, sleepBody()))
	h := newHarness(t, script, client.Limits{MaxConcurrency: 2, MaxDateRangeDays: 7})

	span, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, "2026-01-05"))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}

	got, err := newWellness(t, h).DailySleepRange(t.Context(), h.session, mustDisplayName(t), span)
	if err != nil {
		t.Fatalf("DailySleepRange() = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("%d days returned, want one per day of the inclusive range", len(got))
	}

	requests := h.server.Requests()
	if len(requests) != 5 {
		t.Fatalf("the fake received %d requests, want 5", len(requests))
	}
	dates := make(map[string]bool, len(requests))
	for _, request := range requests {
		dates[request.Query.Get(client.QueryDate)] = true
	}
	for _, want := range []string{"2026-01-01", "2026-01-02", "2026-01-03", "2026-01-04", "2026-01-05"} {
		if !dates[want] {
			t.Errorf("no request carried date %q", want)
		}
	}
}

func TestWellnessDailySleepRangeBoundsTheWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 3})
	span, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, "2026-01-10"))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}

	_, err = newWellness(t, h).DailySleepRange(t.Context(), h.session, mustDisplayName(t), span)
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("DailySleepRange() = %v, want a validation error", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestWellnessUserSummaryDecodesADateKeyedObject(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(summaryPath(),
		testkit.JSON(http.StatusOK, summaryBody(false)))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellness(t, h).UserSummary(t.Context(), h.session, mustDisplayName(t),
		mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("UserSummary() = %v", err)
	}
	if got.CalendarDate == nil || *got.CalendarDate != testCalendarDate {
		t.Errorf("CalendarDate = %v, want %q", got.CalendarDate, testCalendarDate)
	}
	if steps, ok := got.TotalSteps.Int64(); !ok || steps != 8342 {
		t.Errorf("TotalSteps = %v/%v, want 8342", steps, ok)
	}
	if rhr, ok := got.RestingHeartRate.Int64(); !ok || rhr != 58 {
		t.Errorf("RestingHeartRate = %v/%v, want 58 from the string form", rhr, ok)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryCalendarDate); got != testCalendarDate {
		t.Errorf("calendarDate = %q, want %q", got, testCalendarDate)
	}
}

func TestWellnessUserSummaryTreatsPrivacyProtectedAsAnAuthenticationFailure(t *testing.T) {
	t.Parallel()

	// Source: get_user_summary, which raises GarminConnectAuthenticationError when
	// the payload reports privacyProtected.
	script := testkit.NewScript().With(summaryPath(),
		testkit.JSON(http.StatusOK, summaryBody(true)))
	h := newHarness(t, script, client.Limits{})

	_, err := newWellness(t, h).UserSummary(t.Context(), h.session, mustDisplayName(t),
		mustDate(t, testCalendarDate))
	if !errors.Is(err, client.ErrAuthentication) {
		t.Fatalf("UserSummary() = %v, want ErrAuthentication", err)
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("the failure is not an *APIError")
	}
	if apiErr.Op != client.OpGetUserSummary {
		t.Errorf("Op = %q, want %q", apiErr.Op, client.OpGetUserSummary)
	}
}

func TestWellnessUserSummaryReportsAnEmptyResponse(t *testing.T) {
	t.Parallel()

	// Source: get_user_summary, which raises rather than returning an empty summary
	// when the server sends nothing.
	script := testkit.NewScript().With(summaryPath(), testkit.Behavior{Status: http.StatusNoContent})
	h := newHarness(t, script, client.Limits{})

	_, err := newWellness(t, h).UserSummary(t.Context(), h.session, mustDisplayName(t),
		mustDate(t, testCalendarDate))
	if !errors.Is(err, client.ErrUnexpectedResponse) {
		t.Errorf("UserSummary() = %v, want ErrUnexpectedResponse for an empty body", err)
	}
}
