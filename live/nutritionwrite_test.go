//go:build garminlive

package live

import (
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Argument and field names the nutrition writes share, spelled once so a rename
// shows up in one place.
const (
	argFoodID    = "food_id"
	argServingID = "serving_id"
	argMealDate  = "meal_date"
	argMealTime  = "meal_time"
	argSearch    = "search"
	argCalories  = "calories"
	argFoodName  = "food_name"
	keyLogged    = "logged"
)

// The custom food this lifecycle creates. The figures are arbitrary, bounded and
// reversible, and none of them describes the account.
//
// customFoodCreateProteinG is set on create and never resent by the update below,
// so the update's read-back can pin update_custom_food's own documented
// replace-not-merge contract: a field the update omits is cleared, not preserved.
const (
	customFoodCalories        = 150.0
	customFoodCreateProteinG  = 12.5
	customFoodUpdatedCalories = 220.0
	logMealTime               = "09:00:00"
)

// TestLiveCustomFoodLifecycle drives one custom food from creation to removal,
// exercising every custom-food and food-log write tool on the way: create, update,
// log, quick-add log, find-or-create-then-log, and the two destructive removals.
//
// The subject is a custom food this suite created, and the write guard refuses every
// update, log and delete against a food it did not create — see foodguard_test.go.
func TestLiveCustomFoodLifecycle(t *testing.T) {
	w := liveWriteEnv(t)

	name := w.names.name(labelNameCustomFood)
	created := w.call(t, tools.ToolCreateCustomFood, map[string]any{
		argFoodName: name, argCalories: customFoodCalories, "protein": customFoodCreateProteinG,
	})
	foodID := stringField(t, created, tools.ToolCreateCustomFood, argFoodID)
	servingID := stringField(t, created, tools.ToolCreateCustomFood, argServingID)
	w.keepCleanFood(t, foodID)

	if !w.foods.ownsFood(foodID) {
		t.Fatal("the write guard did not learn the created custom food from Garmin's own " +
			"response, so every later write against it would be refused")
	}
	assertSuiteValue(t, tools.ToolCreateCustomFood, argFoodName, name, created)

	w.updateCreatedCustomFood(t, foodID, servingID, name)
	date := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)

	loggedID := w.logCreatedCustomFood(t, foodID, servingID, date)
	w.deleteFoodLogViaTool(t, loggedID, date)

	upsertedID := w.upsertAndLogExistingFood(t, name, date)
	w.deleteFoodLogViaTool(t, upsertedID, date)

	quickAddID := w.quickAddFoodLog(t, date)
	w.deleteFoodLogViaTool(t, quickAddID, date)

	w.deleteCustomFoodViaTool(t, foodID, name)
}

// updateCreatedCustomFood replaces the custom food's nutrition facts in place and
// confirms both halves of update_custom_food's own documented contract: every field
// this call resends is persisted, and the one field it deliberately omits —
// protein, set at create and never sent again here — is cleared rather than
// carried over, matching the tool's "replaces the whole record ... every field
// this call omits is cleared, not preserved" description
// (internal/tools/customfoodwrites.go's updateCustomFoodContract).
func (w *writeEnv) updateCreatedCustomFood(t *testing.T, foodID, servingID, name string) {
	t.Helper()

	updated := w.call(t, tools.ToolUpdateCustomFood, map[string]any{
		argFoodID: foodID, argServingID: servingID,
		argFoodName: name, argCalories: customFoodUpdatedCalories,
	})
	if got := stringField(t, updated, tools.ToolUpdateCustomFood, argFoodID); got != foodID {
		t.Fatalf("%s saved the food under a different identifier", tools.ToolUpdateCustomFood)
	}

	item, serving := w.findCustomFoodServing(t, foodID, name)
	if got, _ := item["name"].(string); got != name {
		t.Errorf("%s did not persist the food name it was given", tools.ToolUpdateCustomFood)
	}
	if calories, ok := serving[argCalories].(float64); !ok || calories != customFoodUpdatedCalories {
		t.Errorf("%s did not persist the calorie figure it was given", tools.ToolUpdateCustomFood)
	}
	if _, present := serving["protein_g"]; present {
		t.Errorf("%s did not clear a field this update omitted, contradicting its own "+
			"documented replace-not-merge contract", tools.ToolUpdateCustomFood)
	}
}

// findCustomFoodServing searches the account's own custom-food library by name and
// returns the matching item and its first serving, failing the test if the food or
// a serving of it cannot be found.
func (w *writeEnv) findCustomFoodServing(
	t *testing.T, foodID, name string,
) (map[string]any, map[string]any) {
	t.Helper()

	page := w.call(t, tools.ToolGetCustomFoods, map[string]any{argSearch: name})
	results, _ := page["results"].([]any)
	for _, entry := range results {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := item[argFoodID].(string); got != foodID {
			continue
		}
		servings, _ := item["servings"].([]any)
		if len(servings) == 0 {
			t.Fatalf("%s stored a food with no servings", tools.ToolUpdateCustomFood)
		}
		serving, ok := servings[0].(map[string]any)
		if !ok {
			t.Fatalf("%s stored a serving with an unexpected shape", tools.ToolUpdateCustomFood)
		}
		return item, serving
	}
	t.Fatalf("%s: the updated food could not be found by name afterward", tools.ToolUpdateCustomFood)
	return nil, nil
}

// logCreatedCustomFood logs the created food to a meal and adopts the resulting
// food-log entry, the same way workoutwrite_test.go's adoptScheduledEntry adopts a
// calendar entry: neither identifier is ever declared by the test, both are read back
// from what Garmin itself now reports.
func (w *writeEnv) logCreatedCustomFood(t *testing.T, foodID, servingID, date string) string {
	t.Helper()

	before := w.currentLogIDs(t, date)
	result := w.call(t, tools.ToolLogCustomFood, map[string]any{
		argMealDate: date, argMealTime: logMealTime,
		argFoodID: foodID, argServingID: servingID, "source": "GARMIN",
	})
	if logged, _ := result[keyLogged].(bool); !logged {
		t.Fatalf("%s reported the entry as not logged", tools.ToolLogCustomFood)
	}
	return w.adoptNewLogEntry(t, date, before, tools.ToolLogCustomFood)
}

// upsertAndLogExistingFood drives upsert_and_log against the food name this test
// already created, which upsert_and_log's own find-or-create step must find rather
// than duplicate — the property its name promises and the one asserted here, not
// merely that logging succeeded.
func (w *writeEnv) upsertAndLogExistingFood(t *testing.T, name, date string) string {
	t.Helper()

	before := w.currentLogIDs(t, date)
	beforeCount := w.customFoodCount(t, name)
	result := w.call(t, tools.ToolUpsertAndLog, map[string]any{
		argMealDate: date, argMealTime: logMealTime,
		argFoodName: name, argCalories: customFoodUpdatedCalories,
	})
	if logged, _ := result[keyLogged].(bool); !logged {
		t.Fatalf("%s reported the entry as not logged", tools.ToolUpsertAndLog)
	}
	if got := w.customFoodCount(t, name); got != beforeCount {
		t.Errorf("%s created a second custom food named %q instead of finding the existing "+
			"one: the library held %d before and %d after", tools.ToolUpsertAndLog, name,
			beforeCount, got)
	}
	return w.adoptNewLogEntry(t, date, before, tools.ToolUpsertAndLog)
}

// customFoodCount reports how many custom foods in the account's library carry
// exactly name.
func (w *writeEnv) customFoodCount(t *testing.T, name string) int {
	t.Helper()

	page := w.call(t, tools.ToolGetCustomFoods, map[string]any{argSearch: name})
	results, _ := page["results"].([]any)
	count := 0
	for _, entry := range results {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := item["name"].(string); got == name {
			count++
		}
	}
	return count
}

// quickAddFoodLog drives log_food, which needs no food or serving identifier at all.
func (w *writeEnv) quickAddFoodLog(t *testing.T, date string) string {
	t.Helper()

	before := w.currentLogIDs(t, date)
	name := w.names.name(labelNameDescription)
	result := w.call(t, tools.ToolLogFood, map[string]any{
		argMealDate: date, argMealTime: logMealTime, "name": name,
		argCalories: 80, "carbs": 10, "protein": 2, "fat": 1,
	})
	if logged, _ := result[keyLogged].(bool); !logged {
		t.Fatalf("%s reported the entry as not logged", tools.ToolLogFood)
	}
	return w.adoptNewLogEntry(t, date, before, tools.ToolLogFood)
}

// currentLogIDs reads the day's food-log entries and returns the set of log
// identifiers it carries.
func (w *writeEnv) currentLogIDs(t *testing.T, date string) map[string]bool {
	t.Helper()

	result := w.call(t, tools.ToolGetNutritionDailyFoodLog, map[string]any{argDate: date})
	entries, _ := result[keyEntries].([]any)
	ids := make(map[string]bool, len(entries))
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := object["log_id"].(string); ok && id != "" {
			ids[id] = true
		}
	}
	return ids
}

// adoptNewLogEntry re-reads the day's food log and hands both reads to
// foodLedger.ownLogEntry, which adopts the one entry that was not there before the
// write — the same trust ownScheduled places in a calendar read: the entry's
// identifier is never declared by the test, only ever derived from what Garmin
// itself now reports.
func (w *writeEnv) adoptNewLogEntry(
	t *testing.T, date string, before map[string]bool, tool string,
) string {
	t.Helper()

	after := w.currentLogIDs(t, date)
	found, ok := w.foods.ownLogEntry(before, after, date)
	if !ok {
		t.Fatalf("%s did not produce exactly one new, adoptable food-log entry for %s", tool, date)
	}
	w.keepCleanLog(t, found, date)
	return found
}

// deleteFoodLogViaTool drives delete_food_log against one owned entry and confirms
// both the confirmation gate and the removal.
func (w *writeEnv) deleteFoodLogViaTool(t *testing.T, id, date string) {
	t.Helper()

	asked := w.confirmations.Load()
	result := w.call(t, tools.ToolDeleteFoodLog, map[string]any{"log_id": id, argMealDate: date})
	if got := stringField(t, result, tools.ToolDeleteFoodLog, "log_id"); got != id {
		t.Fatalf("%s did not report the entry it was asked to delete", tools.ToolDeleteFoodLog)
	}
	if w.confirmations.Load() == asked {
		t.Errorf("%s ran without asking for confirmation, so the destructive gate was not reached",
			tools.ToolDeleteFoodLog)
	}
	if !awaitAbsent(w.foodLogEntryGone(t, date, id)) {
		t.Errorf("%s reported the entry as deleted and it is still listed", tools.ToolDeleteFoodLog)
		return
	}
	w.foods.releaseLog(id)
}

// foodLogEntryGone reports whether the day's food log no longer lists one entry.
//
// Like entryGone for a calendar entry, this has no authoritative not-found: the
// food-log read is a list, and an absent identifier is a list without it rather than
// Garmin stating the entry does not exist. calendarAbsence's weaker agreement applies
// for the same reason it applies there.
func (w *writeEnv) foodLogEntryGone(t *testing.T, date, id string) absenceProof {
	t.Helper()

	return calendarAbsence(func() bool {
		return !w.currentLogIDs(t, date)[id]
	})
}

// deleteCustomFoodViaTool drives delete_custom_food against the owned food and
// confirms both the confirmation gate and the removal.
//
// Absence here has no authoritative not-found either: get_custom_foods is a search
// list, so the proof is the same weaker, repeated-agreement shape foodLogEntryGone
// uses rather than workoutGone's authored refusal.
func (w *writeEnv) deleteCustomFoodViaTool(t *testing.T, id, name string) {
	t.Helper()

	asked := w.confirmations.Load()
	result := w.call(t, tools.ToolDeleteCustomFood, map[string]any{argFoodID: id})
	if got := stringField(t, result, tools.ToolDeleteCustomFood, argFoodID); got != id {
		t.Fatalf("%s did not report the food it was asked to delete", tools.ToolDeleteCustomFood)
	}
	if w.confirmations.Load() == asked {
		t.Errorf("%s ran without asking for confirmation, so the destructive gate was not reached",
			tools.ToolDeleteCustomFood)
	}

	gone := calendarAbsence(func() bool {
		page := w.call(t, tools.ToolGetCustomFoods, map[string]any{argSearch: name})
		results, _ := page["results"].([]any)
		for _, entry := range results {
			item, ok := entry.(map[string]any)
			if ok {
				if got, _ := item[argFoodID].(string); got == id {
					return false
				}
			}
		}
		return true
	})
	if !awaitAbsent(gone) {
		t.Errorf("%s reported the food as deleted and it is still listed", tools.ToolDeleteCustomFood)
		return
	}
	w.foods.releaseFood(id)
}

// stringField reads a non-empty string field out of a tool result.
func stringField(t *testing.T, result map[string]any, tool, field string) string {
	t.Helper()

	value, ok := result[field].(string)
	if !ok || value == "" {
		t.Fatalf("%s returned no usable %s", tool, field)
	}
	return value
}
