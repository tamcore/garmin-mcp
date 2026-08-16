package api_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestFoodLogDayExposesEachEntrysLogID proves the tolerant decoded view
// exposes the identifier delete_food_log needs, without which no LogID could
// ever be obtained from this layer. The exact wire shape has no evidence
// anywhere in python-garminconnect or the Taxuspt curation, so the fixture
// uses the plausible "logId" spelling and a bare top-level array, the
// simplest shape a day's log entries could take.
func TestFoodLogDayExposesEachEntrysLogID(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionFoodLogPath(), testkit.JSON(http.StatusOK,
		`[{"logId":"`+testLogIDValue+`","mealId":504,"mealDate":"2026-01-31"},`+
			`{"logId":"11112222333344445555666677778888","mealId":501}]`))
	h := newHarness(t, script, client.Limits{})

	day, err := newNutrition(t, h).FoodLog(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("FoodLog() = %v", err)
	}
	entries := day.Entries()
	if len(entries) != 2 {
		t.Fatalf("Entries() = %d, want 2", len(entries))
	}
	if id, ok := entries[0].LogID.Value(); !ok || id != testLogIDValue {
		t.Errorf("entries[0].LogID = %v/%v, want the first logId", id, ok)
	}
	if mealID, ok := entries[0].MealID.Int64(); !ok || mealID != 504 {
		t.Errorf("entries[0].MealID = %v/%v, want 504", mealID, ok)
	}
	if date, ok := entries[0].MealDate.Value(); !ok || date != "2026-01-31" {
		t.Errorf("entries[0].MealDate = %v/%v, want 2026-01-31", date, ok)
	}
	if id, ok := entries[1].LogID.Value(); !ok || id != "11112222333344445555666677778888" {
		t.Errorf("entries[1].LogID = %v/%v, want the second logId", id, ok)
	}
	if day.EntriesTruncated() {
		t.Error("EntriesTruncated() = true for a two-entry day, want false")
	}
}

// TestFoodLogDayEntriesToleratesAWrapperObjectAndUnknownShapes proves the
// tolerant decoder also accepts a wrapper-keyed object, and reports no
// entries (rather than an error) for a shape it does not recognize, since no
// spelling is evidenced and a read must never fail the whole day over an
// undecodable per-entry shape.
func TestFoodLogDayEntriesToleratesAWrapperObjectAndUnknownShapes(t *testing.T) {
	t.Parallel()

	wrapped := testkit.NewScript().With(nutritionFoodLogPath(), testkit.JSON(http.StatusOK,
		`{"foodLogEntries":[{"logId":"`+testLogIDValue+`"}]}`))
	h := newHarness(t, wrapped, client.Limits{})
	day, err := newNutrition(t, h).FoodLog(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("FoodLog() = %v", err)
	}
	entries := day.Entries()
	if len(entries) != 1 {
		t.Fatalf("Entries() over a wrapper object = %d, want 1", len(entries))
	}
	if id, ok := entries[0].LogID.Value(); !ok || id != testLogIDValue {
		t.Errorf("entries[0].LogID = %v/%v, want the wrapped logId", id, ok)
	}

	unknown := testkit.NewScript().With(nutritionFoodLogPath(), testkit.JSON(http.StatusOK,
		`{"somethingElse":"goes here"}`))
	h2 := newHarness(t, unknown, client.Limits{})
	day2, err := newNutrition(t, h2).FoodLog(t.Context(), h2.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("FoodLog() = %v", err)
	}
	if got := day2.Entries(); got != nil {
		t.Errorf("Entries() over an unrecognized shape = %v, want nil", got)
	}
}

// buildFoodLogEntries builds count synthetic food-log entries as a bare JSON
// array, each carrying a distinct logId so a truncated read is visibly
// missing the entries past the bound.
func buildFoodLogEntries(count int) string {
	entries := make([]string, count)
	for i := range entries {
		entries[i] = `{"logId":"entry-` + strconv.Itoa(i) + `"}`
	}
	return "[" + strings.Join(entries, ",") + "]"
}

// TestFoodLogDayEntriesReportsTruncation proves a day whose entry count
// exceeds maxFoodLogEntries reports the truncation through EntriesTruncated,
// matching every other bounded result in this package: a silently truncated
// food log could hide an entry delete_food_log can never reach.
func TestFoodLogDayEntriesReportsTruncation(t *testing.T) {
	t.Parallel()

	const overBound = 1001 // maxFoodLogEntries + 1
	script := testkit.NewScript().With(nutritionFoodLogPath(),
		testkit.JSON(http.StatusOK, buildFoodLogEntries(overBound)))
	h := newHarness(t, script, client.Limits{})

	day, err := newNutrition(t, h).FoodLog(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("FoodLog() = %v", err)
	}
	entries := day.Entries()
	if len(entries) != 1000 {
		t.Fatalf("Entries() = %d, want 1000 (bounded)", len(entries))
	}
	if !day.EntriesTruncated() {
		t.Error("EntriesTruncated() = false for a day over the bound, want true")
	}
}

// TestFoodLogDayEntriesUnderBoundIsNotTruncated is the counter-test: a day at
// exactly the bound is not reported as truncated.
func TestFoodLogDayEntriesUnderBoundIsNotTruncated(t *testing.T) {
	t.Parallel()

	const atBound = 1000 // maxFoodLogEntries
	script := testkit.NewScript().With(nutritionFoodLogPath(),
		testkit.JSON(http.StatusOK, buildFoodLogEntries(atBound)))
	h := newHarness(t, script, client.Limits{})

	day, err := newNutrition(t, h).FoodLog(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("FoodLog() = %v", err)
	}
	if len(day.Entries()) != atBound {
		t.Fatalf("Entries() = %d, want %d", len(day.Entries()), atBound)
	}
	if day.EntriesTruncated() {
		t.Error("EntriesTruncated() = true at exactly the bound, want false")
	}
}
