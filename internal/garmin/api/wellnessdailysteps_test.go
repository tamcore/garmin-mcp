package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The step reads — the intraday chart and the daily aggregate — split out of
// wellnessdaily_test.go to keep both files inside the file-length rule. Every
// fixture is synthetic.
const (
	// The second bucket exercises the tolerated forms: a numeric string, an
	// explicit null and a missing field.
	stepsChartBody = `[{"startGMT":"2026-01-31T00:00:00.0",` +
		`"endGMT":"2026-01-31T00:15:00.0","steps":12,"pushes":0,` +
		`"primaryActivityLevel":"sedentary","activityLevelConstant":true},` +
		`{"startGMT":"2026-01-31T00:15:00.0","steps":"0",` +
		`"primaryActivityLevel":null,"activityLevelConstant":null}]`
)

func stepsChartPath() string {
	return client.PathDailySummaryChartPrefix + "/" + fakeDisplayName
}

func TestWellnessDailyStepsIntervalsDecodesTheBucketShape(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(stepsChartPath(),
			testkit.JSON(http.StatusOK, stepsChartBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).StepsIntervals(t.Context(), h.session,
		mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("StepsIntervals() = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("%d intervals returned, want 2", len(got))
	}

	// Garmin's own layout: one fractional digit, no zone.
	for name, pair := range map[string][2]string{
		"StartGMT": {mustText(t, got[0].StartGMT), testCalendarDate + "T00:00:00.0"},
		"EndGMT":   {mustText(t, got[0].EndGMT), testCalendarDate + "T00:15:00.0"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
	}
	if steps, ok := got[0].Steps.Int64(); !ok || steps != 12 {
		t.Errorf("Steps = %v/%v, want 12", steps, ok)
	}
	// A measured zero is not an absent field.
	if pushes, ok := got[0].Pushes.Int64(); !ok || pushes != 0 {
		t.Errorf("Pushes = %v/%v, want a measured zero", pushes, ok)
	}
	if level, ok := got[0].PrimaryActivityLevel.Value(); !ok || level != "sedentary" {
		t.Errorf("PrimaryActivityLevel = %q/%v, want sedentary", level, ok)
	}
	if got[0].ActivityLevelConstant == nil || !*got[0].ActivityLevelConstant {
		t.Error("ActivityLevelConstant lost the boolean")
	}

	if date := h.server.Requests()[0].Query.Get(client.QueryDate); date != testCalendarDate {
		t.Errorf("date = %q, want %q", date, testCalendarDate)
	}
}

// TestWellnessDailyStepsIntervalsToleratesTheAbsentForms covers the second bucket:
// none of the absent forms may fail the read, and none may become a zero.
func TestWellnessDailyStepsIntervalsToleratesTheAbsentForms(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(stepsChartPath(),
			testkit.JSON(http.StatusOK, stepsChartBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).StepsIntervals(t.Context(), h.session,
		mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("StepsIntervals() = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("%d intervals returned, want 2", len(got))
	}

	if steps, ok := got[1].Steps.Int64(); !ok || steps != 0 {
		t.Errorf("Steps = %v/%v, want 0 from the string form", steps, ok)
	}
	if got[1].Pushes.IsSet() {
		t.Error("Pushes must report absent for a bucket that carried no such field")
	}
	if got[1].EndGMT.IsSet() {
		t.Error("EndGMT must report absent for a bucket that carried no such field")
	}
	if got[1].PrimaryActivityLevel.IsSet() {
		t.Error("PrimaryActivityLevel must report absent for an explicit null")
	}
	if got[1].ActivityLevelConstant != nil {
		t.Error("ActivityLevelConstant must report absent for an explicit null")
	}
}

// TestWellnessDailyStepsIntervalsKeepsAnUnknownActivityLevel pins the open enum:
// four labels on one day of one account is not the full range.
func TestWellnessDailyStepsIntervalsKeepsAnUnknownActivityLevel(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(stepsChartPath(),
			testkit.JSON(http.StatusOK, `[{"primaryActivityLevel":"aLevelNobodyHasSeen"}]`))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).StepsIntervals(t.Context(), h.session,
		mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("StepsIntervals() = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("%d intervals returned, want 1", len(got))
	}
	if level, ok := got[0].PrimaryActivityLevel.Value(); !ok || level != "aLevelNobodyHasSeen" {
		t.Errorf("PrimaryActivityLevel = %q/%v, want the unknown label kept", level, ok)
	}
}

func TestWellnessDailyStepsIntervalsTreatsNullAsNoData(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(stepsChartPath(),
			testkit.JSON(http.StatusOK, "null"))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).StepsIntervals(t.Context(), h.session,
		mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("StepsIntervals() = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("%d intervals returned, want none", len(got))
	}
}

func TestWellnessDailyStepsIntervalsRequiresADisplayNameAndADate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	daily := newWellnessDaily(t, h)

	if _, err := daily.StepsIntervals(t.Context(), h.session, client.DisplayName{},
		mustDate(t, testCalendarDate)); !errors.Is(err, client.ErrValidation) {
		t.Errorf("StepsIntervals() with no display name = %v, want a validation error", err)
	}
	if _, err := daily.StepsIntervals(t.Context(), h.session, mustDisplayName(t),
		client.Date{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("StepsIntervals() with no date = %v, want a validation error", err)
	}
}

// mustText reads a union-decoded string that the fixture guarantees is present.
func mustText(t *testing.T, text client.Text) string {
	t.Helper()

	value, ok := text.Value()
	if !ok {
		t.Error("a field the fixture carries decoded as absent")
	}
	return value
}

// The per-day aggregate is a flat four-field record, unrelated to the interval
// document above. The second day exercises the tolerated absent forms.
const dailyStepsBody = `[{"calendarDate":"2026-01-01","totalSteps":9123,` +
	`"totalDistance":7345,"stepGoal":8000},` +
	`{"calendarDate":"2026-01-03","totalSteps":"0","totalDistance":null}]`

func dailyStepsPath(start, end string) string {
	return client.PathDailyStepsStatsPrefix + "/" + start + "/" + end
}

// TestWellnessDailyDailyStepsChunksAWideWindow pins the 28-day chunking: a single
// wider request is the one shape Garmin refuses outright.
func TestWellnessDailyDailyStepsChunksAWideWindow(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(dailyStepsPath(testWindowStart, "2026-01-28"),
			testkit.JSON(http.StatusOK, dailyStepsBody)).
		With(dailyStepsPath("2026-01-29", "2026-02-05"),
			testkit.JSON(http.StatusOK, `[{"calendarDate":"2026-01-29","totalSteps":11}]`))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).DailySteps(t.Context(), h.session,
		mustRange(t, testWindowStart, "2026-02-05"))
	if err != nil {
		t.Fatalf("DailySteps() = %v", err)
	}
	// Two records from the first chunk and one from the second, concatenated in
	// chunk order and never padded to the length of the window.
	if len(got) != 3 {
		t.Fatalf("%d days returned, want every record both chunks held", len(got))
	}
	if got[2].CalendarDate == nil || *got[2].CalendarDate != "2026-01-29" {
		t.Errorf("the last day = %v, want the second chunk's record", got[2].CalendarDate)
	}
	if got := len(h.server.Requests()); got != 2 {
		t.Errorf("the fake received %d requests, want 2 chunks", got)
	}
}

func TestWellnessDailyDailyStepsBoundsTheWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 7})
	_, err := newWellnessDaily(t, h).DailySteps(t.Context(), h.session,
		mustRange(t, testWindowStart, "2026-02-05"))
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("DailySteps() over the bound = %v, want a validation error", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestWellnessDailyDailyStepsDecodesTheFlatDayRecord pins the four-field shape and
// the absent forms. It is deliberately a different shape from the interval record:
// the two tools are one word apart and share nothing.
func TestWellnessDailyDailyStepsDecodesTheFlatDayRecord(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(dailyStepsPath(testWindowStart, "2026-01-03"),
			testkit.JSON(http.StatusOK, dailyStepsBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).DailySteps(t.Context(), h.session,
		mustRange(t, testWindowStart, "2026-01-03"))
	if err != nil {
		t.Fatalf("DailySteps() = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("%d days returned, want the two Garmin held", len(got))
	}

	if steps, ok := got[0].TotalSteps.Int64(); !ok || steps != 9123 {
		t.Errorf("TotalSteps = %v/%v, want 9123", steps, ok)
	}
	if distance, ok := got[0].TotalDistance.Float64(); !ok || distance != 7345 {
		t.Errorf("TotalDistance = %v/%v, want 7345", distance, ok)
	}
	if goal, ok := got[0].StepGoal.Int64(); !ok || goal != 8000 {
		t.Errorf("StepGoal = %v/%v, want 8000", goal, ok)
	}

	// 2026-01-02 is missing from the answer: a day the watch was not worn is
	// simply absent, so the second record is the third day and not the second.
	if got[1].CalendarDate == nil || *got[1].CalendarDate != "2026-01-03" {
		t.Errorf("the second record = %v, want the day Garmin actually sent",
			got[1].CalendarDate)
	}
	if steps, ok := got[1].TotalSteps.Int64(); !ok || steps != 0 {
		t.Errorf("TotalSteps = %v/%v, want 0 from the string form", steps, ok)
	}
	if got[1].TotalDistance.IsSet() {
		t.Error("TotalDistance must report absent for an explicit null")
	}
	if got[1].StepGoal.IsSet() {
		t.Error("StepGoal must report absent for a record that carried no such field")
	}
}
