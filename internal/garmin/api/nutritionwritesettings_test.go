package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestSetSettingsMergesOntoTheCurrentDocument proves the read-modify-write
// dance: the PUT carries the caller's overrides plus every field the current
// document held that the caller did not name.
func TestSetSettingsMergesOntoTheCurrentDocument(t *testing.T) {
	t.Parallel()

	current := `{"calorieGoal":2000,"macroGoals":{"carbs":220,"fat":65,"protein":120,"fiber":30},` +
		`"planId":"p-1"}`
	script := testkit.NewScript().With(nutritionSettingsPath(),
		testkit.JSON(http.StatusOK, current),
		testkit.JSON(http.StatusNoContent, ""))
	h := newHarness(t, script, client.Limits{})

	calorieGoal := int64(2200)
	proteinGrams := int64(150)
	settings, err := newNutrition(t, h).SetSettings(t.Context(), h.session, mustDate(t, testCalendarDate),
		api.NutritionSettingsUpdate{CalorieGoal: &calorieGoal, ProteinGrams: &proteinGrams})
	if err != nil {
		t.Fatalf("SetSettings() = %v", err)
	}
	if got, ok := settings.CalorieGoal.Int64(); !ok || got != 2200 {
		t.Errorf("CalorieGoal = %v/%v, want 2200", got, ok)
	}
	if got, ok := settings.MacroGoals.Protein.Int64(); !ok || got != 150 {
		t.Errorf("MacroGoals.Protein = %v/%v, want 150", got, ok)
	}

	// Payload() is the retained raw RESPONSE, and Garmin's acknowledgement has no
	// body. Manufacturing one from the request would make it claim Garmin sent
	// content it never sent, under Garmin's own status code. The decoded fields
	// above are what carry the written document.
	if settings.Payload().Len() != 0 {
		t.Error("SetSettings() reported a response payload for a 204 acknowledgement " +
			"that carried no body")
	}

	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("the fake received %d requests, want 2 (read then write)", len(requests))
	}
	if requests[0].Method != http.MethodGet {
		t.Errorf("first request method = %q, want GET", requests[0].Method)
	}
	if requests[1].Method != http.MethodPut {
		t.Errorf("second request method = %q, want PUT", requests[1].Method)
	}

	body := decodeBody(t, requests[1].Body)
	if body["calorieGoal"] != float64(2200) {
		t.Errorf("calorieGoal = %v, want 2200", body["calorieGoal"])
	}
	if body["planId"] != "p-1" {
		t.Errorf("planId = %v, want p-1 (preserved, unmodeled field)", body["planId"])
	}
	macros, ok := body["macroGoals"].(map[string]any)
	if !ok {
		t.Fatalf("macroGoals = %v, want an object", body["macroGoals"])
	}
	want := map[string]any{"carbs": float64(220), "fat": float64(65), "protein": float64(150), "fiber": float64(30)}
	for key, value := range want {
		if macros[key] != value {
			t.Errorf("macroGoals.%s = %v, want %v", key, macros[key], value)
		}
	}
}

// TestSetSettingsBuildsMacroGoalsWhenAbsentOrNull proves a missing or null
// macroGoals is treated as an empty object, as upstream does (nutrition.py:261-263).
func TestSetSettingsBuildsMacroGoalsWhenAbsentOrNull(t *testing.T) {
	t.Parallel()

	for name, current := range map[string]string{
		"absent":        `{"calorieGoal":2000}`,
		"explicit null": `{"calorieGoal":2000,"macroGoals":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			script := testkit.NewScript().With(nutritionSettingsPath(),
				testkit.JSON(http.StatusOK, current), testkit.JSON(http.StatusNoContent, ""))
			h := newHarness(t, script, client.Limits{})

			if _, err := newNutrition(t, h).SetSettings(t.Context(), h.session,
				mustDate(t, testCalendarDate), api.NutritionSettingsUpdate{FatGrams: new(int64(70))}); err != nil {
				t.Fatalf("SetSettings() = %v", err)
			}
			body := decodeBody(t, h.server.Requests()[1].Body)
			macros, ok := body["macroGoals"].(map[string]any)
			if !ok || len(macros) != 1 || macros["fat"] != float64(70) {
				t.Errorf("macroGoals = %v, want exactly {fat: 70}", body["macroGoals"])
			}
		})
	}
}

// TestSetSettingsRejectsANonObjectMacroGoals refuses to overwrite a macroGoals
// value it cannot merge into (nutrition.py:264-265).
func TestSetSettingsRejectsANonObjectMacroGoals(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionSettingsPath(),
		testkit.JSON(http.StatusOK, `{"calorieGoal":2000,"macroGoals":[1]}`))
	h := newHarness(t, script, client.Limits{})

	if _, err := newNutrition(t, h).SetSettings(t.Context(), h.session, mustDate(t, testCalendarDate),
		api.NutritionSettingsUpdate{CarbsGrams: new(int64(200))}); !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("SetSettings() over a non-object macroGoals = %v, want ErrMalformedPayload", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1 (the read only)", got)
	}
}

// TestSetSettingsRefusesAnEmptyUpdate mirrors upstream's "No fields to
// update" refusal (nutrition.py:115-116), without dispatching anything.
func TestSetSettingsRefusesAnEmptyUpdate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newNutrition(t, h).SetSettings(t.Context(), h.session, mustDate(t, testCalendarDate),
		api.NutritionSettingsUpdate{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("SetSettings() with no fields = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestSetSettingsRefusesAnUnsetDate keeps validation ahead of the read.
func TestSetSettingsRefusesAnUnsetDate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	goal := int64(2000)
	if _, err := newNutrition(t, h).SetSettings(t.Context(), h.session, client.Date{},
		api.NutritionSettingsUpdate{CalorieGoal: &goal}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("SetSettings() without a date = %v, want ErrValidation", err)
	}
}

// TestSetSettingsRejectsANonObjectCurrentDocument proves a current document
// that decodes as valid JSON but is not an object cannot be merged into.
func TestSetSettingsRejectsANonObjectCurrentDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionSettingsPath(), testkit.JSON(http.StatusOK, `[1,2,3]`))
	h := newHarness(t, script, client.Limits{})

	goal := int64(2000)
	if _, err := newNutrition(t, h).SetSettings(t.Context(), h.session, mustDate(t, testCalendarDate),
		api.NutritionSettingsUpdate{CalorieGoal: &goal}); !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("SetSettings() over a non-object current document = %v, want ErrMalformedPayload", err)
	}
}

// TestSetSettingsRejectsAnEmptyCurrentDocument proves an empty read is
// refused rather than silently overwritten.
func TestSetSettingsRejectsAnEmptyCurrentDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionSettingsPath(), testkit.JSON(http.StatusNoContent, ""))
	h := newHarness(t, script, client.Limits{})

	goal := int64(2000)
	if _, err := newNutrition(t, h).SetSettings(t.Context(), h.session, mustDate(t, testCalendarDate),
		api.NutritionSettingsUpdate{CalorieGoal: &goal}); !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("SetSettings() over an empty current document = %v, want ErrMalformedPayload", err)
	}
}

// TestSetSettingsRejectsALiteralNullCurrentDocument proves a current document
// of literal JSON `null` is refused rather than merged into: `null` unmarshals
// to a nil map, and merging an override into a nil map panics with
// "assignment to entry in nil map" unless it is rejected first.
func TestSetSettingsRejectsALiteralNullCurrentDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionSettingsPath(), testkit.JSON(http.StatusOK, "null"))
	h := newHarness(t, script, client.Limits{})

	goal := int64(2000)
	if _, err := newNutrition(t, h).SetSettings(t.Context(), h.session, mustDate(t, testCalendarDate),
		api.NutritionSettingsUpdate{CalorieGoal: &goal}); !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("SetSettings() over a literal null current document = %v, want ErrMalformedPayload", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1 (the read only, no write)", got)
	}
}

// TestSetSettingsRejectsAnEmptyObjectCurrentDocument proves a current
// document of `{}` is refused rather than merged into: an empty object holds
// no other fields to preserve, so writing only the caller's overrides would
// silently reset every other setting Garmin held.
func TestSetSettingsRejectsAnEmptyObjectCurrentDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionSettingsPath(), testkit.JSON(http.StatusOK, "{}"))
	h := newHarness(t, script, client.Limits{})

	goal := int64(2000)
	if _, err := newNutrition(t, h).SetSettings(t.Context(), h.session, mustDate(t, testCalendarDate),
		api.NutritionSettingsUpdate{CalorieGoal: &goal}); !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("SetSettings() over an empty object current document = %v, want ErrMalformedPayload", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1 (the read only, no write)", got)
	}
}
