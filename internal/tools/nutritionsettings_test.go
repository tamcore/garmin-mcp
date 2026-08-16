package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const settingsBodyFixture = `{"activeDailyCalories":2200,"activeDailyCarbohydrateGrams":250,` +
	`"activeDailyFatGrams":70,"activeDailyProteinGrams":140}`

func nutritionSettingsScript(behaviors ...testkit.Behavior) testkit.Script {
	return testkit.NewScript().With(client.PathNutritionSettingsPrefix+"/"+nutritionTestDate, behaviors...)
}

func TestReadNutritionDailySettingsDecodesTheFourGoals(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, nutritionSettingsScript(testkit.JSON(http.StatusOK, settingsBodyFixture)))
	out, err := h.svc.readNutritionDailySettings(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailySettings() = %v", err)
	}
	if out.CalorieGoal == nil || *out.CalorieGoal != 2200 {
		t.Errorf("calorie_goal = %v, want 2200", out.CalorieGoal)
	}
	if out.CarbsGrams == nil || *out.CarbsGrams != 250 {
		t.Errorf("carbs_grams = %v, want 250", out.CarbsGrams)
	}
	if out.FatGrams == nil || *out.FatGrams != 70 {
		t.Errorf("fat_grams = %v, want 70", out.FatGrams)
	}
	if out.ProteinGrams == nil || *out.ProteinGrams != 140 {
		t.Errorf("protein_grams = %v, want 140", out.ProteinGrams)
	}
}

func TestGetNutritionDailySettingsRefusesABadDateAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, nutritionSettingsScript(testkit.JSON(http.StatusOK, settingsBodyFixture)))
	if _, err := h.svc.readNutritionDailySettings(h.ctx, "bad-date"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("a malformed date = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.readNutritionDailySettings(t.Context(), nutritionTestDate); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestSetNutritionDailySettingsAppliesOnlyTheSuppliedOverride(t *testing.T) {
	t.Parallel()

	// The write reads the current document, then writes the merged result back, so
	// the same path serves a GET and a PUT in sequence.
	h := newTrendHarness(t, nutritionSettingsScript(
		testkit.JSON(http.StatusOK, settingsBodyFixture),
		testkit.JSON(http.StatusOK, `{"activeDailyCalories":2400,"activeDailyCarbohydrateGrams":250,`+
			`"activeDailyFatGrams":70,"activeDailyProteinGrams":140}`),
	))

	newGoal := int64(2400)
	out, err := h.svc.setNutritionDailySettings(h.ctx, setNutritionDailySettingsInput{
		Date: nutritionTestDate, CalorieGoal: &newGoal,
	})
	if err != nil {
		t.Fatalf("setNutritionDailySettings() = %v", err)
	}
	if out.Status != "updated" {
		t.Errorf("status = %q, want updated", out.Status)
	}
	if out.CalorieGoal == nil || *out.CalorieGoal != 2400 {
		t.Errorf("calorie_goal = %v, want 2400", out.CalorieGoal)
	}
	if out.CarbsGrams == nil || *out.CarbsGrams != 250 {
		t.Errorf("carbs_grams = %v, want the untouched 250", out.CarbsGrams)
	}

	requests := h.fake.Requests()
	if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodPut {
		t.Fatalf("requests = %+v, want a GET then a PUT", requests)
	}
}

// TestSetNutritionDailySettingsRefusesAnOutOfRangeGoal pins the bound: before it,
// calorie_goal and the macro goals carried no Minimum or Maximum, so a value like
// -9000000000 reached a real account.
func TestSetNutritionDailySettingsRefusesAnOutOfRangeGoal(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, nutritionSettingsScript(testkit.JSON(http.StatusOK, settingsBodyFixture)))

	hostile := int64(-9000000000)
	if _, err := h.svc.setNutritionDailySettings(h.ctx, setNutritionDailySettingsInput{
		Date: nutritionTestDate, CalorieGoal: &hostile,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("a hostile calorie goal = %v, want ErrInvalidArgument", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0 before any Garmin call", got)
	}

	tooHighCarbs := int64(maxMacroGrams + 1)
	if _, err := h.svc.setNutritionDailySettings(h.ctx, setNutritionDailySettingsInput{
		Date: nutritionTestDate, CarbsGrams: &tooHighCarbs,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("an over-bound carbs goal = %v, want ErrInvalidArgument", err)
	}
}

func TestSetNutritionDailySettingsRefusesWhenNoFieldIsSupplied(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, nutritionSettingsScript(testkit.JSON(http.StatusOK, settingsBodyFixture)))
	if _, err := h.svc.setNutritionDailySettings(h.ctx, setNutritionDailySettingsInput{
		Date: nutritionTestDate,
	}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("an empty update = %v, want ErrValidation", err)
	}
}

// TestNutritionSettingsResultsNeverLogATargetFigure is the redaction rule: a calorie
// or macro target is a health reading.
func TestNutritionSettingsResultsNeverLogATargetFigure(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, nutritionSettingsScript(testkit.JSON(http.StatusOK, settingsBodyFixture)))
	out, err := h.svc.readNutritionDailySettings(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailySettings() = %v", err)
	}
	assertShapeOnly(t, "NutritionSettingsResult", out, "2200", "250", "70", "140")
}
