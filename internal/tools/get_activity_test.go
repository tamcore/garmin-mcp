package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// activityRecord is the synthetic answer of the per-activity endpoint. duration
// arrives as a string on purpose: Garmin sends the same field both ways. The start
// coordinates are present because the tool must drop them.
const activityRecord = `{"activityId":987654321,"activityName":"Synthetic run",` +
	`"description":"a synthetic session",` +
	`"activityTypeDTO":{"typeId":1,"typeKey":"running","parentTypeId":17},` +
	`"eventTypeDTO":{"typeId":9,"typeKey":"race"},` +
	`"summaryDTO":{"startTimeLocal":"2026-01-31T06:12:00.0",` +
	`"startTimeGMT":"2026-01-31T05:12:00.0","duration":"3000.0","movingDuration":2980.0,` +
	`"elapsedDuration":3060.0,"distance":10000.0,"averageSpeed":3.33,"maxSpeed":4.1,` +
	`"averageHR":148,"maxHR":172,"minHR":94,"calories":640,"bmrCalories":90,` +
	`"averageRunCadence":172.5,"maxRunCadence":186.0,"strideLength":118.4,` +
	`"groundContactTime":244.0,"verticalOscillation":8.1,"steps":8800,` +
	`"averagePower":240,"maxPower":410,"normalizedPower":255,` +
	`"trainingEffect":3.4,"anaerobicTrainingEffect":1.2,"trainingEffectLabel":"TEMPO",` +
	`"activityTrainingLoad":128.5,"moderateIntensityMinutes":12,` +
	`"vigorousIntensityMinutes":34,"elevationGain":220.0,"elevationLoss":214.0,` +
	`"maxElevation":610.0,"minElevation":505.0,"recoveryHeartRate":118,` +
	`"differenceBodyBattery":-21,"directWorkoutFeel":75,"directWorkoutRpe":60,` +
	`"startLatitude":48.1,"startLongitude":11.5},` +
	`"metadataDTO":{"lapCount":4,"hasSplits":true,"manufacturer":"GARMIN",` +
	`"unknownField":true}}`

func activityIDArgs() map[string]any { return map[string]any{argActivityID: parityActivityID} }

// TestGetActivityReturnsTheCuratedRecord pins the mapping of every group.
func TestGetActivityReturnsTheCuratedRecord(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(parityActivityPath(),
		testkit.JSON(http.StatusOK, activityRecord))
	h := newParityHarness(t, script)

	result := h.call(t, ToolGetActivity, activityIDArgs())

	if got := number(t, result, "activity_id"); got != 987654321 {
		t.Errorf("activity_id = %v, want the requested identifier", got)
	}
	if got, _ := result["activity_type"].(string); got != typeKeyRunning {
		t.Errorf("activity_type = %q, want running", got)
	}
	if got, _ := result["event_type"].(string); got != "race" {
		t.Errorf("event_type = %q, want race", got)
	}
	if got := number(t, result, "parent_type_id"); got != 17 {
		t.Errorf("parent_type_id = %v, want 17", got)
	}
	if got := number(t, result, "lap_count"); got != 4 {
		t.Errorf("lap_count = %v, want 4", got)
	}
	if has, _ := result["has_splits"].(bool); !has {
		t.Error("has_splits = false, want true")
	}
	assertActivityGroups(t, result)
}

func assertActivityGroups(t *testing.T, result map[string]any) {
	t.Helper()

	groups := map[string]map[string]float64{
		"timing":     {"duration_seconds": 3000, "moving_seconds": 2980, "elapsed_seconds": 3060},
		"distance":   {"distance_meters": 10000, "average_speed_mps": 3.33, "max_speed_mps": 4.1},
		"heart_rate": {"average_bpm": 148, "max_bpm": 172, "min_bpm": 94},
		"energy":     {"calories": 640, "bmr_calories": 90},
		"run_metrics": {"average_cadence": 172.5, "max_cadence": 186, "stride_length_cm": 118.4,
			"ground_contact_time_ms": 244, "vertical_oscillation_cm": 8.1, "steps": 8800},
		"power": {"average_watts": 240, "max_watts": 410, "normalized_watts": 255},
		"training": {"aerobic_training_effect": 3.4, "anaerobic_training_effect": 1.2,
			"training_load": 128.5, "moderate_intensity_minutes": 12,
			"vigorous_intensity_minutes": 34},
		"elevation": {"gain_meters": 220, "loss_meters": 214, "max_meters": 610,
			"min_meters": 505},
		"feedback": {"recovery_heart_rate_bpm": 118, "body_battery_impact": -21,
			"workout_feel": 75, "workout_rpe": 60},
	}
	for group, fields := range groups {
		values := object(t, result, group)
		for key, want := range fields {
			if got := number(t, values, key); got != want {
				t.Errorf("%s.%s = %v, want %v", group, key, got, want)
			}
		}
	}
	if got, _ := object(t, result, "training")["training_effect_label"].(string); got != "TEMPO" {
		t.Errorf("training.training_effect_label = %q, want TEMPO", got)
	}
	if got, _ := object(t, result, "feedback")["device_manufacturer"].(string); got != "GARMIN" {
		t.Errorf("feedback.device_manufacturer = %q, want GARMIN", got)
	}
}

// TestGetActivityDropsTheStartCoordinates is the disclosure test.
func TestGetActivityDropsTheStartCoordinates(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(parityActivityPath(),
		testkit.JSON(http.StatusOK, activityRecord))
	h := newParityHarness(t, script)

	rendered := h.text(t, ToolGetActivity, activityIDArgs())
	for _, forbidden := range []string{"48.1", "11.5", "startLatitude", "startLongitude"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the result carries %q, which must never leave this server", forbidden)
		}
	}
}

// TestGetActivityToleratesAThinRecord proves an activity whose sub-documents Garmin
// changed or omitted is still returned, with the fields that did decode.
func TestGetActivityToleratesAThinRecord(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"no sub-documents":  `{"activityId":987654321,"activityName":"Bare"}`,
		"null summary":      `{"activityId":987654321,"summaryDTO":null,"metadataDTO":null}`,
		"summary is a list": `{"activityId":987654321,"summaryDTO":[1,2,3]}`,
		"bare type key":     `{"activityId":987654321,"activityTypeDTO":"running"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(parityActivityPath(),
				testkit.JSON(http.StatusOK, body))
			h := newParityHarness(t, script)

			result := h.call(t, ToolGetActivity, activityIDArgs())
			if got := number(t, result, "activity_id"); got != 987654321 {
				t.Errorf("activity_id = %v, want the requested identifier", got)
			}
			if _, present := result["event_type"]; present {
				t.Error("event_type is present, want it omitted when Garmin sends none")
			}
		})
	}
}

// TestGetActivityRefusesAnIdentifierThatIsNotOne proves no argument can reach a URL
// path unvalidated.
func TestGetActivityRefusesAnIdentifierThatIsNotOne(t *testing.T) {
	t.Parallel()

	h := newParityHarness(t, testkit.NewScript())

	for _, raw := range []any{"../../etc/passwd", "0", "-4", "12.5", true} {
		advice := h.callError(t, ToolGetActivity, map[string]any{argActivityID: raw})
		if !strings.Contains(advice, argActivityID) && !strings.Contains(advice, "arguments") {
			t.Errorf("the refusal of %#v does not explain itself: %q", raw, advice)
		}
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestGetActivitySanitizesAGarminFailure proves an upstream failure comes back as
// authored advice, never as a rendered payload.
func TestGetActivitySanitizesAGarminFailure(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(parityActivityPath(),
		testkit.JSON(http.StatusNotFound, `{"message":"secret-detail","token":"abc.def"}`))
	h := newParityHarness(t, script)

	advice := h.callError(t, ToolGetActivity, activityIDArgs())
	for _, forbidden := range []string{"secret-detail", "abc.def"} {
		if strings.Contains(advice, forbidden) {
			t.Errorf("the refusal %q carries the upstream payload", advice)
		}
	}
}
