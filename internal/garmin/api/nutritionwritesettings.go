package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// NutritionSettingsUpdate is the strict request model for a nutrition-goal
// change. A nil field leaves Garmin's existing value in place; at least one
// field must be set.
//
// Source: set_nutrition_daily_settings, which reads the current document,
// applies only the overrides the caller supplied, and writes the merged
// result back (nutrition.py:117-139) — Garmin's settings PUT takes the whole
// document, so an omitted field would otherwise be cleared rather than left
// alone.
type NutritionSettingsUpdate struct {
	// CalorieGoal is the daily calorie target in kcal.
	CalorieGoal *int64
	// CarbsGrams is the daily carbohydrate target in grams.
	CarbsGrams *int64
	// FatGrams is the daily fat target in grams.
	FatGrams *int64
	// ProteinGrams is the daily protein target in grams.
	ProteinGrams *int64
}

// isEmpty reports whether the update carries no field to apply.
func (u NutritionSettingsUpdate) isEmpty() bool {
	return u.CalorieGoal == nil && u.CarbsGrams == nil && u.FatGrams == nil && u.ProteinGrams == nil
}

// settingsFieldKeys names the four known keys of the settings document, in the
// order NutritionSettingsUpdate's fields correspond to them. A function, not a
// var: AGENTS.md allows no package-level mutable state, and a constant that
// cannot be a const is a function, never a var.
func settingsFieldKeys() [4]string {
	return [4]string{
		"activeDailyCalories", "activeDailyCarbohydrateGrams",
		"activeDailyFatGrams", "activeDailyProteinGrams",
	}
}

// SetSettings applies a nutrition-goal update for one day.
//
// It reads the current document, overlays only the supplied fields onto the
// unmodeled ones the document also carries, and writes the merged result
// back — the read-modify-write dance nutrition.py's set_nutrition_daily_settings
// performs, required because Garmin's settings PUT replaces the whole
// document rather than patching it.
func (n *Nutrition) SetSettings(
	ctx context.Context, session client.Session, date client.Date, update NutritionSettingsUpdate,
) (NutritionSettings, error) {
	req := writeRequest(client.OpSetNutritionDailySettings, client.EndpointNutritionSettings,
		http.MethodPut, "", client.EffectIdempotentWrite)
	path, err := datedNutritionPath(req, client.PathNutritionSettingsPrefix, date)
	if err != nil {
		return NutritionSettings{}, err
	}
	req.Path = path

	if update.isEmpty() {
		return NutritionSettings{}, invalid(req, fmt.Errorf(
			"%w: at least one of calorie goal, carbs, fat or protein must be set",
			client.ErrValidation))
	}

	current, err := n.Settings(ctx, session, date)
	if err != nil {
		return NutritionSettings{}, err
	}
	if current.Payload().Len() == 0 {
		return NutritionSettings{}, unexpected(req, fmt.Errorf(
			"%w: no current nutrition settings to update", client.ErrMalformedPayload))
	}

	body, err := mergeSettingsFields(req, current.Payload().Bytes(), update)
	if err != nil {
		return NutritionSettings{}, err
	}
	req.Body = body

	var settings NutritionSettings
	payload, err := n.req.write(ctx, session, req, &settings)
	if err != nil {
		return NutritionSettings{}, err
	}
	if payload.NoContent() {
		// Garmin acknowledges this write with no body, so the decoded fields are
		// filled from the document this call sent — a caller that just wrote a
		// setting should be able to read it back from the result.
		//
		// Payload() is deliberately left as the real, empty response. It is
		// documented as the retained raw response, and manufacturing one from the
		// request body would make it claim Garmin sent content it never sent,
		// under Garmin's own status code. An empty payload after a 204 is the
		// truth; the fields carry what was written.
		if err := json.Unmarshal(body, &settings); err != nil {
			return NutritionSettings{}, unexpected(req, fmt.Errorf(
				"%w: the merged settings document could not be re-decoded",
				client.ErrMalformedPayload))
		}
	}
	settings.raw = payload
	return settings, nil
}

// mergeSettingsFields overlays update's set fields onto base, preserving every
// other field base carries, the same way workouts.go's withWorkoutID merges a
// known field into an opaque document.
func mergeSettingsFields(
	req client.Request, base []byte, update NutritionSettingsUpdate,
) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(base, &fields); err != nil {
		return nil, unexpected(req, fmt.Errorf(
			"%w: the current settings document is not a JSON object", client.ErrMalformedPayload))
	}
	// A literal `null` unmarshals to a nil map with no error, and an empty `{}`
	// object holds no other field to preserve; both are rejected here rather
	// than merged into, which would otherwise panic on the nil map or silently
	// truncate the document to only the caller's overrides.
	if len(fields) == 0 {
		return nil, unexpected(req, fmt.Errorf(
			"%w: the current settings document carries no fields to merge onto",
			client.ErrMalformedPayload))
	}

	keys := settingsFieldKeys()
	overrides := [...]*int64{update.CalorieGoal, update.CarbsGrams, update.FatGrams, update.ProteinGrams}
	for index, value := range overrides {
		if value == nil {
			continue
		}
		encoded, err := json.Marshal(*value)
		if err != nil {
			return nil, invalid(req, fmt.Errorf("%w: nutrition goal could not be encoded",
				client.ErrValidation))
		}
		fields[keys[index]] = encoded
	}

	body, err := json.Marshal(fields)
	if err != nil {
		return nil, invalid(req, fmt.Errorf("%w: merged settings document could not be encoded",
			client.ErrValidation))
	}
	if len(body) > MaxRequestBodyBytes {
		return nil, invalid(req, fmt.Errorf("%w: request body exceeds its bound", client.ErrValidation))
	}
	return body, nil
}
