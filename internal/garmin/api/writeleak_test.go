package api_test

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// A type conversion strips the method set, so LogValue and the redacting
// renderers no longer apply and fmt reflects over the fields instead. The
// material each of these models retains — the raw Garmin response — sits behind
// client.Payload, which seals it one pointer deeper than fmt's badVerb path can
// dereference. These aliases exist to prove that still holds for the write
// models this slice adds.
type (
	strippedWriteResult  api.WriteResult
	strippedSavedWorkout api.SavedWorkout
	strippedWorkout      api.Workout
	strippedCreated      api.CreatedActivity
	strippedSettings     api.ProfileSettings
)

const (
	leakWorkoutName = "SENTINEL-WORKOUT-NAME-9a41"
	leakGearName    = "SENTINEL-GEAR-6f2b"
)

// TestMethodStrippingAliasCannotRevealARetainedWritePayload is the leak test for
// the models this slice adds.
func TestMethodStrippingAliasCannotRevealARetainedWritePayload(t *testing.T) {
	t.Parallel()

	saved, workout, created, settings := collectWriteModels(t)
	values := map[string]any{
		"WriteResult":     strippedWriteResult(api.WriteResult{}),
		"SavedWorkout":    strippedSavedWorkout(saved),
		"Workout":         strippedWorkout(workout),
		"CreatedActivity": strippedCreated(created),
		"ProfileSettings": strippedSettings(settings),
		"in a slice":      []strippedSavedWorkout{strippedSavedWorkout(saved)},
		"in a map":        map[string]strippedWorkout{"w": strippedWorkout(workout)},
	}

	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"}
	for name, value := range values {
		for _, verb := range verbs {
			rendered := fmt.Sprintf(verb, value)
			if strings.Contains(rendered, leakWorkoutName) {
				t.Errorf("%s rendered with %s leaks the retained body: %s", name, verb, rendered)
			}
		}
	}
}

// TestWriteModelsReportShapeToASlogSink covers the ordinary logging path, where
// the methods are still in place.
func TestWriteModelsReportShapeToASlogSink(t *testing.T) {
	t.Parallel()

	saved, workout, created, settings := collectWriteModels(t)
	models := map[string]any{
		"SavedWorkout":    saved,
		"Workout":         workout,
		"CreatedActivity": created,
		"ProfileSettings": settings,
		"StrengthSet":     sampleSet(t),
		"GearItem":        api.GearItem{DisplayName: new(leakGearName)},
	}

	for name, value := range models {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, nil)).Info("garmin write", "model", value)

		rendered := logged.String()
		for _, needle := range []string{leakWorkoutName, leakGearName, exerciseBackSquat, "20000"} {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}

// TestWriteFailuresRenderLabelsOnly proves an error carries no body and no value
// a caller supplied.
func TestWriteFailuresRenderLabelsOnly(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(workoutItemPath(),
		testkit.JSON(http.StatusBadRequest, `{"message":"`+leakWorkoutName+`"}`))
	h := newHarness(t, script, client.Limits{})

	document, err := api.ParseWorkoutDocument([]byte(`{"workoutName":"` + leakWorkoutName + `"}`))
	if err != nil {
		t.Fatalf("ParseWorkoutDocument() = %v", err)
	}
	_, err = newWorkouts(t, h).Update(t.Context(), h.session, mustID(t), document)
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("Update() = %v, want the 400 classified as ErrValidation", err)
	}
	if strings.Contains(err.Error(), leakWorkoutName) {
		t.Errorf("the error renders the response body: %s", err)
	}
}

// collectWriteModels fetches one of every write-side model that retains a
// payload, from a body carrying the sentinel.
func collectWriteModels(t *testing.T) (
	api.SavedWorkout, api.Workout, api.CreatedActivity, api.ProfileSettings,
) {
	t.Helper()

	body := `{"workoutId":18446744,"workoutName":"` + leakWorkoutName + `"}`
	script := testkit.NewScript().
		With(client.PathWorkoutPrefix, testkit.JSON(http.StatusOK, body)).
		With(workoutItemPath(), testkit.JSON(http.StatusOK, body)).
		With(client.PathActivityPrefix, testkit.JSON(http.StatusOK,
			`{"activityId":18446744,"activityName":"`+leakWorkoutName+`"}`)).
		With(client.PathUserProfileSettings, testkit.JSON(http.StatusOK,
			`{"id":1,"fullName":"`+leakWorkoutName+`"}`))
	h := newHarness(t, script, client.Limits{})

	saved, err := newWorkouts(t, h).Upload(t.Context(), h.session, mustWorkoutDocument(t))
	if err != nil {
		t.Fatalf("Upload() = %v", err)
	}
	workout, err := newWorkouts(t, h).Get(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	created, err := newActivityWrites(t, h).CreateManual(t.Context(), h.session,
		api.NewManualActivity("running", "2026-01-31T09:00:00.000", "UTC", "", 0, 30))
	if err != nil {
		t.Fatalf("CreateManual() = %v", err)
	}
	settings, err := newProfile(t, h).ProfileSettings(t.Context(), h.session)
	if err != nil {
		t.Fatalf("ProfileSettings() = %v", err)
	}
	return saved, workout, created, settings
}
