package tools

import (
	"net/http"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func statsRangeScript(calories, steps string) testkit.Script {
	return testkit.NewScript().With(client.PathDailyStatsPrefix+"/2026-01-30/2026-02-01",
		testkit.JSON(http.StatusOK, calories), testkit.JSON(http.StatusOK, steps))
}

func TestGetStatsRangeJoinsBothSeriesIntoOneRowPerDay(t *testing.T) {
	t.Parallel()

	now := func() time.Time { return time.Date(2026, 2, 1, 15, 0, 0, 0, time.Local) }
	h := newToolHarnessAt(t, statsRangeScript(
		`{"values":[{"calendarDate":"2026-01-30","values":`+
			`{"totalCalories":2500,"activeCalories":600,"restingCalories":1900}},`+
			`{"calendarDate":"2026-02-01","values":{"totalCalories":1200}}]}`,
		`{"values":[{"calendarDate":"2026-01-30","values":{"totalSteps":9000}}]}`), now)

	got := h.call(t, ToolGetStatsRange, map[string]any{
		argStartDate: bodyBatteryWindowStart, argEndDate: challengeWindowEnd,
	})
	days, _ := got["days"].([]any)
	if len(days) != 3 || got["count"] != float64(3) {
		t.Fatalf("result = %v, want one row for each of the three days", got)
	}
	first, _ := days[0].(map[string]any)
	want := map[string]any{
		argDate: bodyBatteryWindowStart, "total_calories": float64(2500), "active_calories": float64(600),
		"resting_calories": float64(1900), keySteps: float64(9000),
		"has_data": true, "is_partial": false,
	}
	for key, value := range want {
		if first[key] != value {
			t.Errorf("first day %s = %v, want %v", key, first[key], value)
		}
	}
	missing, _ := days[1].(map[string]any)
	if missing["has_data"] != false || missing["total_calories"] != nil || missing[keySteps] != nil {
		t.Errorf("a day Garmin holds nothing for = %v, want has_data false and no values", missing)
	}
	today, _ := days[2].(map[string]any)
	if today["is_partial"] != true || today["has_data"] != true {
		t.Errorf("today = %v, want a partial day with data", today)
	}

	requests := h.fake.Requests()
	if len(requests) != 2 ||
		requests[0].Query.Get(client.QueryStatsType) != string(client.StatsTypeCalories) ||
		requests[1].Query.Get(client.QueryStatsType) != string(client.StatsTypeSteps) {
		t.Errorf("requests = %v, want one CALORIES and one STEPS read", requests)
	}
}

func TestGetStatsRangeRefusesAWindowWiderThanGarminAnswers(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript())
	h.callError(t, ToolGetStatsRange, map[string]any{
		argStartDate: scoresStartDate, argEndDate: "2026-01-29",
	})
	if requests := h.fake.Requests(); len(requests) != 0 {
		t.Errorf("the fake received %d requests, want none", len(requests))
	}
}

func TestGetStatsRangeFailsWhenOneSeriesFails(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript().With(client.PathDailyStatsPrefix+"/2026-01-30/2026-02-01",
		testkit.JSON(http.StatusOK, `{"values":[]}`), testkit.JSON(http.StatusInternalServerError, `{}`)))
	h.callError(t, ToolGetStatsRange, map[string]any{
		argStartDate: bodyBatteryWindowStart, argEndDate: challengeWindowEnd,
	})
}
