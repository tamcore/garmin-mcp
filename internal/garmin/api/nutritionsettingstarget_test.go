package api_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestSetSettingsRefusesAPassedWeightGoalTargetDate refuses, before the PUT, a
// write Garmin answers with 400 because the stored weight-goal targetDate is not
// after the day written (nutrition.py:23-66, :246-250).
func TestSetSettingsRefusesAPassedWeightGoalTargetDate(t *testing.T) {
	t.Parallel()

	for name, target := range map[string]string{
		"same day":        testCalendarDate,
		"earlier":         "2025-12-01",
		"same day, timed": testCalendarDate + "T00:00:00.0",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			script := testkit.NewScript().With(nutritionSettingsPath(),
				testkit.JSON(http.StatusOK, `{"calorieGoal":2000,"targetDate":"`+target+`"}`))
			h := newHarness(t, script, client.Limits{})

			_, err := newNutrition(t, h).SetSettings(t.Context(), h.session, mustDate(t, testCalendarDate),
				api.NutritionSettingsUpdate{CalorieGoal: new(int64(2100))})
			passed, ok := errors.AsType[*api.TargetDatePassedError](err)
			if !ok || !errors.Is(err, client.ErrValidation) {
				t.Fatalf("SetSettings() = %v, want a TargetDatePassedError wrapping ErrValidation", err)
			}
			for _, want := range []string{"targetDate", target[:10], testCalendarDate} {
				if !strings.Contains(passed.Error(), want) {
					t.Errorf("error %q does not name %q", passed.Error(), want)
				}
			}
			if got := len(h.server.Requests()); got != 1 {
				t.Errorf("the fake received %d requests, want 1 (the read only)", got)
			}
		})
	}
}

// TestSetSettingsWritesWhenTheWeightGoalTargetDateIsLater keeps a valid stored
// targetDate untouched in the PUT.
func TestSetSettingsWritesWhenTheWeightGoalTargetDateIsLater(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionSettingsPath(),
		testkit.JSON(http.StatusOK, `{"calorieGoal":2000,"targetDate":"2026-02-01"}`),
		testkit.JSON(http.StatusNoContent, ""))
	h := newHarness(t, script, client.Limits{})

	if _, err := newNutrition(t, h).SetSettings(t.Context(), h.session, mustDate(t, testCalendarDate),
		api.NutritionSettingsUpdate{CalorieGoal: new(int64(2100))}); err != nil {
		t.Fatalf("SetSettings() = %v", err)
	}
	if body := decodeBody(t, h.server.Requests()[1].Body); body["targetDate"] != "2026-02-01" {
		t.Errorf("targetDate = %v, want the stored 2026-02-01", body["targetDate"])
	}
}
