package tools

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
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
	if got, _ := result["source"].(string); got != trainingEffectSourceDetails {
		t.Errorf("source = %q, want %q", got, trainingEffectSourceDetails)
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
	if got := number(t, result, trainingEffectRecoveryKey); got != 26 {
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

const trainingEffectRecoveryKey = "recovery_time_hours"

// trainingEffectListItem is the list-search shape of activity 9001, which carries the
// training fields at the top level rather than under summaryDTO.
const trainingEffectListItem = `{"activityId":9001,"aerobicTrainingEffect":2.8,` +
	`"anaerobicTrainingEffect":0.4,"aerobicTrainingEffectMessage":"IMPROVING_AEROBIC_BASE_8",` +
	`"activityTrainingLoad":77.5,"recoveryTime":600,"performanceCondition":-1}`

// forbiddenDetailsScript refuses the details and answers the filtered list read with
// filtered, then the recent-page read with recent.
func forbiddenDetailsScript(filtered, recent string) testkit.Script {
	return testkit.NewScript().
		With(trainingEffectActivityPath, testkit.JSON(http.StatusForbidden, `{}`)).
		With(client.PathActivitySearch,
			testkit.JSON(http.StatusOK, filtered), testkit.JSON(http.StatusOK, recent))
}

// listSearches returns the query of every activity-list read the fake received.
func listSearches(h toolHarness) []url.Values {
	var queries []url.Values
	for _, request := range h.fake.Requests() {
		if request.Path == client.PathActivitySearch {
			queries = append(queries, request.Query)
		}
	}
	return queries
}

func TestGetTrainingEffectAcceptsTheAerobicSpellingInTheSummary(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(trainingEffectActivityPath,
		testkit.JSON(http.StatusOK, `{"activityId":9001,"summaryDTO":{"aerobicTrainingEffect":3.1}}`))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetTrainingEffect, trainingEffectArgs())
	if got := number(t, result, "aerobic_training_effect"); got != 3.1 {
		t.Errorf("aerobic_training_effect = %v, want 3.1", got)
	}
}

// TestGetTrainingEffectFallsBackToTheListOnForbiddenDetails proves a 403 on the
// details read is answered from the recent page when the filtered read finds nothing.
func TestGetTrainingEffectFallsBackToTheListOnForbiddenDetails(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, forbiddenDetailsScript(`[]`,
		`[{"activityId":9002,"aerobicTrainingEffect":1.0},`+trainingEffectListItem+`]`))

	result := h.call(t, ToolGetTrainingEffect, trainingEffectArgs())

	if got, _ := result["source"].(string); got != trainingEffectSourceList {
		t.Errorf("source = %q, want %q", got, trainingEffectSourceList)
	}
	if reported, _ := result["reported"].(bool); !reported {
		t.Error("reported = false, want true for a list item with training fields")
	}
	for key, want := range map[string]float64{
		"aerobic_training_effect":   2.8,
		"anaerobic_training_effect": 0.4,
		"training_load":             77.5,
		trainingEffectRecoveryKey:   10,
		"performance_condition":     -1,
	} {
		if got := number(t, result, key); got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if got, _ := result["training_effect_label"].(string); got != "IMPROVING_AEROBIC_BASE_8" {
		t.Errorf("training_effect_label = %q, want the aerobic message", got)
	}
}

// TestGetTrainingEffectFindsAnOlderActivityByItsIdentifier proves the filtered read
// comes first and ends the lookup.
func TestGetTrainingEffectFindsAnOlderActivityByItsIdentifier(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, forbiddenDetailsScript(`[`+trainingEffectListItem+`]`, `[]`))

	result := h.call(t, ToolGetTrainingEffect, trainingEffectArgs())
	if got, _ := result["source"].(string); got != trainingEffectSourceList {
		t.Errorf("source = %q, want %q", got, trainingEffectSourceList)
	}
	searches := listSearches(h)
	if len(searches) != 1 {
		t.Fatalf("the list was read %d times, want once", len(searches))
	}
	want := url.Values{client.QueryStart: {"0"}, client.QueryLimit: {"1"}, client.QueryActivityIDs: {"9001"}}
	if !maps.EqualFunc(searches[0], want, slices.Equal) {
		t.Errorf("filtered query = %v, want %v", searches[0], want)
	}
}

func TestGetTrainingEffectScansOneBoundedRecentPage(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, forbiddenDetailsScript(`[]`, `[`+trainingEffectListItem+`]`))
	h.call(t, ToolGetTrainingEffect, trainingEffectArgs())

	searches := listSearches(h)
	if len(searches) != 2 {
		t.Fatalf("the list was read %d times, want twice", len(searches))
	}
	want := url.Values{client.QueryStart: {"0"}, client.QueryLimit: {"20"}}
	if !maps.EqualFunc(searches[1], want, slices.Equal) {
		t.Errorf("recent-page query = %v, want %v", searches[1], want)
	}
}

// TestGetTrainingEffectExplainsAForbiddenActivityMissingFromTheList proves the miss
// is not reported as a lost login.
func TestGetTrainingEffectExplainsAForbiddenActivityMissingFromTheList(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, forbiddenDetailsScript(`[]`, `[{"activityId":9002}]`))

	advice := h.callError(t, ToolGetTrainingEffect, trainingEffectArgs())
	assertNoRawPayload(t, advice)
	if advice != adviceTrainingEffectNotListed {
		t.Errorf("advice = %q, want %q", advice, adviceTrainingEffectNotListed)
	}
}

// TestGetTrainingEffectPropagatesAnotherFailureWithoutTheList proves only a 403
// triggers the fallback, not the 401 that shares its failure class.
func TestGetTrainingEffectPropagatesAnotherFailureWithoutTheList(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(trainingEffectActivityPath, testkit.JSON(http.StatusUnauthorized, `{}`)).
		With(client.PathActivitySearch, testkit.JSON(http.StatusOK, `[`+trainingEffectListItem+`]`))
	h := newScoresHarness(t, script)

	advice := h.callError(t, ToolGetTrainingEffect, trainingEffectArgs())
	if want := advise(&client.APIError{Kind: client.KindAuthentication}); advice != want {
		t.Errorf("advice = %q, want the unchanged %q", advice, want)
	}
	for _, request := range h.fake.Requests() {
		if request.Path == client.PathActivitySearch {
			t.Fatal("a non-403 failure read the activity list")
		}
	}
}
