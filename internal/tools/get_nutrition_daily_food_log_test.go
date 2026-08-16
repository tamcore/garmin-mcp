package tools

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// These tests drive the handler directly rather than over an MCP session, because
// register.go does not yet carry this tool. Everything below the handler — the
// domain client, the request layer and the fake Garmin service — is the real thing.

const nutritionTestDate = "2026-01-31"
const nutritionTestTime = "07:30:00"

func foodLogScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathNutritionFoodLogPrefix+"/"+nutritionTestDate,
		testkit.JSON(http.StatusOK, body))
}

func TestFoodLogDayDecodesEntryIdentifiers(t *testing.T) {
	t.Parallel()

	body := `{"foodLogEntries":[{"logId":"1f3c9d2a00004000800000abcdef0123","mealId":5001,` +
		`"mealDate":"2026-01-31"},{"id":"2f3c9d2a00004000800000abcdef0456","mealId":5002}]}`
	h := newTrendHarness(t, foodLogScript(body))

	out, err := h.svc.readNutritionDailyFoodLog(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailyFoodLog() = %v", err)
	}

	if out.Date != nutritionTestDate {
		t.Errorf("date = %q, want %q", out.Date, nutritionTestDate)
	}
	if out.Count != 2 || len(out.Entries) != 2 {
		t.Fatalf("entries = %+v, want 2", out.Entries)
	}
	if out.Entries[0].LogID == nil || *out.Entries[0].LogID != "1f3c9d2a00004000800000abcdef0123" {
		t.Errorf("first log id = %v, want the logId value", out.Entries[0].LogID)
	}
	if out.Entries[1].LogID == nil || *out.Entries[1].LogID != "2f3c9d2a00004000800000abcdef0456" {
		t.Errorf("second log id = %v, want the fallback id value", out.Entries[1].LogID)
	}
	if out.Truncated {
		t.Error("two entries must not report truncation")
	}
}

// TestFoodLogDayResultCarriesTheSanitizedDocument pins the fix: before it, only the
// log id and meal association reached the caller, even though the manifest promises
// calories, macronutrients and meal associations.
func TestFoodLogDayResultCarriesTheSanitizedDocument(t *testing.T) {
	t.Parallel()

	body := `{"foodLogEntries":[{"logId":"1f3c9d2a00004000800000abcdef0123","mealId":5001,` +
		`"calories":320,"userId":42}]}`
	h := newTrendHarness(t, foodLogScript(body))

	out, err := h.svc.readNutritionDailyFoodLog(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailyFoodLog() = %v", err)
	}
	if out.Document == nil {
		t.Fatal("document = nil, want the sanitized food log")
	}
	encoded, err := json.Marshal(out.Document)
	if err != nil {
		t.Fatalf("marshaling document: %v", err)
	}
	if !strings.Contains(string(encoded), `"calories":320`) {
		t.Errorf("document = %s, want it to carry the calories field the manifest promises", encoded)
	}
	if strings.Contains(string(encoded), "userId") {
		t.Errorf("document = %s, want the identifying userId key dropped", encoded)
	}
	if out.DroppedFields == 0 {
		t.Error("dropped_fields = 0, want at least the removed userId key")
	}
}

func TestFoodLogDayReportsNoEntriesForAnEmptyDay(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, foodLogScript(`{}`))
	out, err := h.svc.readNutritionDailyFoodLog(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailyFoodLog() = %v", err)
	}
	if out.Count != 0 || len(out.Entries) != 0 {
		t.Errorf("entries = %+v, want none", out.Entries)
	}
}

func TestGetNutritionDailyFoodLogRefusesABadDateAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, foodLogScript(`{}`))
	if _, err := h.svc.readNutritionDailyFoodLog(h.ctx, "31-01-2026"); !errors.Is(
		err, ErrInvalidArgument) {
		t.Errorf("a malformed date = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.readNutritionDailyFoodLog(t.Context(), nutritionTestDate); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestFoodLogDayResultNeverLogsAnEntry is the redaction rule: a log or meal
// identifier is account-linked material, and the result reports its shape only.
func TestFoodLogDayResultNeverLogsAnEntry(t *testing.T) {
	t.Parallel()

	body := `{"foodLogEntries":[{"logId":"1f3c9d2a00004000800000abcdef0123","mealId":5001}]}`
	h := newTrendHarness(t, foodLogScript(body))
	out, err := h.svc.readNutritionDailyFoodLog(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailyFoodLog() = %v", err)
	}
	assertShapeOnly(t, "FoodLogDayResult", out, "1f3c9d2a00004000800000abcdef0123")
}
