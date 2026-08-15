package api_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// logNeedles are the values a log line must never contain: identity, location and
// health measurements from the synthetic fixtures.
var logNeedles = []string{
	fakeDisplayName, fakeFullName, deviceSerialSentinel,
	"48.1", "11.5", "8342", "27000", "5400", "BENCH_PRESS", "Morning Run",
}

// TestSensitiveModelsAreNotLoggable proves that handing a domain model to slog
// reports its shape and never its content. The models stay JSON-marshalable on
// purpose, because the tool layer returns them to an authorized caller; a log sink is
// the boundary they must not cross.
func TestSensitiveModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	for name, value := range collectSensitiveModels(t) {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, nil)).Info("garmin read", "model", value)

		rendered := logged.String()
		for _, needle := range logNeedles {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}

// TestNestedSensitiveModelsAreNotLoggable covers the nested models a caller can log
// on their own, without the enclosing document.
func TestNestedSensitiveModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	models := collectSensitiveModels(t)
	settings, ok := models["UserSettings"].(api.UserSettings)
	if !ok || settings.UserData == nil {
		t.Fatal("the settings fixture carries no userData")
	}
	sleep, ok := models["DailySleep"].(api.DailySleep)
	if !ok || sleep.Summary == nil {
		t.Fatal("the sleep fixture carries no summary")
	}
	splits, ok := models["TypedSplits"].(api.TypedSplits)
	if !ok || splits.Len() != 1 {
		t.Fatal("the splits fixture carries no split")
	}
	sets, ok := models["ExerciseSets"].(api.ExerciseSets)
	if !ok || sets.Sets.Len() != 1 {
		t.Fatal("the exercise-set fixture carries no set")
	}

	nested := map[string]any{
		"UserData":     *settings.UserData,
		"SleepSummary": *sleep.Summary,
		"TypedSplit":   splits.Splits()[0],
		"ExerciseSet":  sets.Sets.Items()[0],
		"Exercise":     sets.Sets.Items()[0].Exercises.Items()[0],
	}
	for name, value := range nested {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, nil)).Info("garmin read", "model", value)

		for _, needle := range logNeedles {
			if strings.Contains(logged.String(), needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, logged.String())
			}
		}
	}
}

// TestRetainedPayloadsAndNormalizedShapes covers the diagnostic payload every model
// keeps and the array normalization the union-decoded model performs.
func TestRetainedPayloadsAndNormalizedShapes(t *testing.T) {
	t.Parallel()

	models := collectSensitiveModels(t)
	payloads := map[string]client.Payload{
		"SocialProfile": models["SocialProfile"].(api.SocialProfile).Payload(),
		"UserSettings":  models["UserSettings"].(api.UserSettings).Payload(),
		"ActivityPage":  models["ActivityPage"].(api.ActivityPage).Payload(),
		"DailySleep":    models["DailySleep"].(api.DailySleep).Payload(),
		"UserSummary":   models["UserSummary"].(api.UserSummary).Payload(),
		"TypedSplits":   models["TypedSplits"].(api.TypedSplits).Payload(),
		"ExerciseSets":  models["ExerciseSets"].(api.ExerciseSets).Payload(),
	}
	for name, payload := range payloads {
		if payload.Len() == 0 {
			t.Errorf("%s retained no raw payload", name)
		}
		if strings.Contains(payload.String(), fakeDisplayName) {
			t.Errorf("%s payload renders its body", name)
		}
	}

	splits := models["TypedSplits"].(api.TypedSplits)
	encoded, err := json.Marshal(splits)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	if !strings.HasPrefix(string(encoded), "[") {
		t.Errorf("TypedSplits marshals as %s, want the shape normalized to an array", encoded)
	}
	if empty, err := json.Marshal(api.TypedSplits{}); err != nil || string(empty) != "[]" {
		t.Errorf("the zero TypedSplits marshals as %s/%v, want []", empty, err)
	}
}

// collectSensitiveModels fetches one of every model this package returns.
func collectSensitiveModels(t *testing.T) map[string]any {
	t.Helper()

	script := testkit.NewScript().
		With(client.PathSocialProfile, testkit.JSON(http.StatusOK, socialProfileBody)).
		With(client.PathUserSettings, testkit.JSON(http.StatusOK, userSettingsBody)).
		With(client.PathActivitySearch, testkit.JSON(http.StatusOK, activityArray(9001))).
		With(sleepPath(), testkit.JSON(http.StatusOK, sleepBody())).
		With(summaryPath(), testkit.JSON(http.StatusOK, summaryBody(false))).
		With(client.PathDevices, testkit.JSON(http.StatusOK, devicesBody)).
		With(splitsPath(), testkit.JSON(http.StatusOK, `{"splits":[`+splitEntry+`]}`)).
		With(exerciseSetsPath(), testkit.JSON(http.StatusOK, exerciseSetsBody))
	h := newHarness(t, script, client.Limits{})

	profile := newProfile(t, h)
	social, err := profile.Social(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Social() = %v", err)
	}
	settings, err := profile.Settings(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Settings() = %v", err)
	}

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	activities, err := newActivities(t, h).List(t.Context(), h.session, api.ListQuery{Page: page})
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	wellness := newWellness(t, h)
	sleep, err := wellness.DailySleep(t.Context(), h.session, mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("DailySleep() = %v", err)
	}
	summary, err := wellness.UserSummary(t.Context(), h.session, mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("UserSummary() = %v", err)
	}

	devices, err := newDevices(t, h).List(t.Context(), h.session)
	if err != nil {
		t.Fatalf("devices List() = %v", err)
	}

	details := newActivityDetails(t, h)
	splits, err := details.TypedSplits(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("TypedSplits() = %v", err)
	}
	sets, err := details.ExerciseSets(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("ExerciseSets() = %v", err)
	}

	return map[string]any{
		"SocialProfile": social,
		"UserSettings":  settings,
		"ActivityPage":  activities,
		"Activity":      activities.Activities[0],
		"DailySleep":    sleep,
		"UserSummary":   summary,
		"Device":        devices[0],
		"TypedSplits":   splits,
		"ExerciseSets":  sets,
	}
}
