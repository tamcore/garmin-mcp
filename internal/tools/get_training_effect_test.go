package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// trainingEffectActivityID is the synthetic activity every training-effect test asks
// about.
const trainingEffectActivityID = 9001

// trainingEffectActivityPath is the summary path of that activity.
const trainingEffectActivityPath = client.PathActivityPrefix + "/9001"

// trainingEffectDocument carries the training fields of an activity summary, plus the
// coordinates and owner key a real one carries, so the test proves they stay behind.
const trainingEffectDocument = `{"activityId":9001,"summaryDTO":{"trainingEffect":3.4,` +
	`"anaerobicTrainingEffect":1.2,"trainingEffectLabel":"TEMPO","recoveryTime":1560,` +
	`"activityTrainingLoad":168.4,"performanceCondition":2,"startLatitude":0.0,` +
	`"startLongitude":0.0},"userProfilePK":900001}`

func trainingEffectArgs() map[string]any {
	return map[string]any{argActivityID: trainingEffectActivityID}
}

func TestGetTrainingEffectReturnsTheActivitysTrainingFields(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(trainingEffectActivityPath,
		testkit.JSON(http.StatusOK, trainingEffectDocument))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetTrainingEffect, trainingEffectArgs())

	if got := number(t, result, "activity_id"); got != trainingEffectActivityID {
		t.Errorf("activity_id = %v, want %d", got, trainingEffectActivityID)
	}
	if reported, _ := result["reported"].(bool); !reported {
		t.Error("reported = false, want true for an activity with a training summary")
	}
	if got := number(t, result, "aerobic_training_effect"); got != 3.4 {
		t.Errorf("aerobic_training_effect = %v, want 3.4", got)
	}
	if got := number(t, result, "anaerobic_training_effect"); got != 1.2 {
		t.Errorf("anaerobic_training_effect = %v, want 1.2", got)
	}
	if got, _ := result["training_effect_label"].(string); got != "TEMPO" {
		t.Errorf("training_effect_label = %q, want TEMPO", got)
	}
	// Garmin reports the recovery in minutes; 1560 of them are 26 hours.
	if got := number(t, result, "recovery_time_hours"); got != 26 {
		t.Errorf("recovery_time_hours = %v, want 26", got)
	}
	if got := number(t, result, "training_load"); got != 168.4 {
		t.Errorf("training_load = %v, want 168.4", got)
	}
}

// TestGetTrainingEffectReturnsNoCoordinateOrAccountKey proves the tool keeps only the
// training fields of a document that carries much more.
func TestGetTrainingEffectReturnsNoCoordinateOrAccountKey(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(trainingEffectActivityPath,
		testkit.JSON(http.StatusOK, trainingEffectDocument))
	h := newScoresHarness(t, script)

	rendered := h.text(t, ToolGetTrainingEffect, trainingEffectArgs())
	for _, forbidden := range []string{"Latitude", "Longitude", "900001", "userProfilePK"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the result carries %q", forbidden)
		}
	}
}

// TestGetTrainingEffectReportsAnActivityWithoutASummary proves a manual activity is a
// normal answer rather than a failure.
func TestGetTrainingEffectReportsAnActivityWithoutASummary(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(trainingEffectActivityPath,
		testkit.JSON(http.StatusOK, `{"activityId":9001}`))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetTrainingEffect, trainingEffectArgs())
	if reported, _ := result["reported"].(bool); reported {
		t.Error("reported = true, want false for an activity with no training summary")
	}
	if _, present := result["aerobic_training_effect"]; present {
		t.Error("an activity with no summary produced a reading")
	}
}

// TestGetTrainingEffectKeepsTheRequestedIdentifier proves a payload that names another
// activity cannot make the result claim it answered about that one.
func TestGetTrainingEffectKeepsTheRequestedIdentifier(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(trainingEffectActivityPath,
		testkit.JSON(http.StatusOK, `{"activityId":9009,"summaryDTO":{"trainingEffect":2.0}}`))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetTrainingEffect, trainingEffectArgs())
	if got := number(t, result, "activity_id"); got != trainingEffectActivityID {
		t.Errorf("activity_id = %v, want the requested %d", got, trainingEffectActivityID)
	}
}

func TestGetTrainingEffectRefusesABadIdentifierBeforeDispatch(t *testing.T) {
	t.Parallel()

	for name, args := range map[string]map[string]any{
		"zero identifier": {argActivityID: 0},
		caseNegative:      {argActivityID: -1},
		"missing":         {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newScoresHarness(t, testkit.NewScript())

			advice := h.callError(t, ToolGetTrainingEffect, args)
			assertNoRawPayload(t, advice)
			if got := len(h.fake.Requests()); got != 0 {
				t.Errorf("the fake received %d requests, want none", got)
			}
		})
	}
}

// TestGetTrainingEffectSanitizesAGarminFailure proves a Garmin refusal comes back as
// authored advice and no payload.
func TestGetTrainingEffectSanitizesAGarminFailure(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(trainingEffectActivityPath,
		testkit.JSON(http.StatusNotFound, `{"error":{"message":"no such activity 9001"}}`))
	h := newScoresHarness(t, script)

	advice := h.callError(t, ToolGetTrainingEffect, trainingEffectArgs())
	assertNoRawPayload(t, advice)
	if advice != AdviceNoSuchRecord {
		t.Errorf("advice = %q, want the authored %q", advice, AdviceNoSuchRecord)
	}
}

// TestTrainingEffectLogValueReportsShapeOnly proves the log record carries no reading.
func TestTrainingEffectLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	effect := 3.4
	value := TrainingEffect{
		ActivityID:      trainingEffectActivityID,
		Reported:        true,
		AerobicEffect:   &effect,
		AnaerobicEffect: &effect,
	}.LogValue().String()

	if strings.Contains(value, "3.4") {
		t.Errorf("the log value %q carries a reading", value)
	}
	if !strings.Contains(value, "trainingEffect") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
