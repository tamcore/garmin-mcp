package api_test

import (
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func newNutrition(t *testing.T, h harness) *api.Nutrition {
	t.Helper()

	n, err := api.NewNutrition(h.rc)
	if err != nil {
		t.Fatalf("NewNutrition() = %v", err)
	}
	return n
}

// TestNutritionIdentifiersRejectHostileInput pins the charset every food,
// serving and log identifier is validated against: it must reach a request
// body or a URL path segment safely.
func TestNutritionIdentifiersRejectHostileInput(t *testing.T) {
	t.Parallel()

	rejected := []string{"", "has space", "../../etc/passwd", "a/b", "bad\x00value",
		string(make([]byte, 65))}

	for _, value := range rejected {
		if _, err := api.ParseFoodID(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseFoodID(%q) = %v, want ErrValidation", value, err)
		}
		if _, err := api.ParseServingID(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseServingID(%q) = %v, want ErrValidation", value, err)
		}
		if _, err := api.ParseLogID(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseLogID(%q) = %v, want ErrValidation", value, err)
		}
	}

	const hexUUID = "1f3c9d2a00004000800000abcdef0123"
	food, err := api.ParseFoodID(hexUUID)
	if err != nil {
		t.Fatalf("ParseFoodID(%q) = %v", hexUUID, err)
	}
	if food.String() != hexUUID || food.IsZero() {
		t.Errorf("ParseFoodID(%q) = %+v, want the validated identifier", hexUUID, food)
	}

	const numericID = "4132350"
	serving, err := api.ParseServingID(numericID)
	if err != nil {
		t.Fatalf("ParseServingID(%q) = %v", numericID, err)
	}
	if serving.String() != numericID {
		t.Errorf("ParseServingID(%q) = %q, want %q", numericID, serving.String(), numericID)
	}

	if (api.FoodID{}).IsZero() != true {
		t.Error("the zero FoodID must report IsZero")
	}
}

// TestNutritionIdentifiersRejectShapesThatAreNeitherDecimalNorHex pins the
// tightened charset: FoodID, ServingID and LogID are all decimal or exactly
// 32-char hex. FoodID and LogID have that shape evidenced directly
// (nutrition.py:567, :728); ServingID's wire format is not evidenced upstream
// at all (nutrition.py:186, :470 and :613 only ever pass it through as an
// opaque string), so it is validated the same permissive way rather than
// refusing a hex value no source rules out. A hyphenated token that the old,
// looser alphanumeric-hyphen charset used to accept must still be refused for
// all three.
func TestNutritionIdentifiersRejectShapesThatAreNeitherDecimalNorHex(t *testing.T) {
	t.Parallel()

	// 31 hex characters: one short of the exact 32-char UUID shape.
	const shortHex = "1f3c9d2a00004000800000abcdef012"
	// 33 hex characters: one over.
	const longHex = "1f3c9d2a00004000800000abcdef01234"
	// A hyphenated token the old charset accepted but no identifier is ever
	// evidenced to look like.
	const hyphenated = "abc-123-def"

	for _, value := range []string{shortHex, longHex, hyphenated} {
		if _, err := api.ParseFoodID(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseFoodID(%q) = %v, want ErrValidation", value, err)
		}
		if _, err := api.ParseServingID(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseServingID(%q) = %v, want ErrValidation", value, err)
		}
		if _, err := api.ParseLogID(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseLogID(%q) = %v, want ErrValidation", value, err)
		}
	}
}

// TestParseServingIDAcceptsAHexShape proves a Garmin-minted custom-food
// serving id — a 32-char hex UUID, the same shape FoodID accepts for a
// Garmin custom food (nutrition.py:567) — is not refused before the request
// is even sent. Nothing in nutrition.py rules out this shape for a serving
// id; it only ever passes servingId through opaquely (nutrition.py:186,
// :470, :613).
func TestParseServingIDAcceptsAHexShape(t *testing.T) {
	t.Parallel()

	const hexUUID = "1f3c9d2a00004000800000abcdef0123"
	serving, err := api.ParseServingID(hexUUID)
	if err != nil {
		t.Fatalf("ParseServingID(%q) = %v, want no error", hexUUID, err)
	}
	if serving.String() != hexUUID || serving.IsZero() {
		t.Errorf("ParseServingID(%q) = %+v, want the validated identifier", hexUUID, serving)
	}
}

// TestParseMealTimeValidatesTheGarminForm pins the HH:MM:SS layout meal_time is
// documented in (nutrition.py:573).
func TestParseMealTimeValidatesTheGarminForm(t *testing.T) {
	t.Parallel()

	valid, err := api.ParseMealTime("12:30:00")
	if err != nil {
		t.Fatalf("ParseMealTime(12:30:00) = %v", err)
	}
	if valid.String() != "12:30:00" {
		t.Errorf("String() = %q, want 12:30:00", valid.String())
	}

	for _, value := range []string{"", "12:30", "25:00:00", "noon", "12:30:00Z"} {
		if _, err := api.ParseMealTime(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseMealTime(%q) = %v, want ErrValidation", value, err)
		}
	}
}

// TestParseFoodSourceAcceptsOnlyTheTwoNamespaces pins the closed set
// log_custom_food accepts (nutrition.py:552-565).
func TestParseFoodSourceAcceptsOnlyTheTwoNamespaces(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"GARMIN", "FATSECRET"} {
		source, err := api.ParseFoodSource(value)
		if err != nil {
			t.Fatalf("ParseFoodSource(%q) = %v", value, err)
		}
		if string(source) != value {
			t.Errorf("ParseFoodSource(%q) = %q, want %q", value, source, value)
		}
	}
	if _, err := api.ParseFoodSource("garmin"); !errors.Is(err, client.ErrValidation) {
		t.Errorf("ParseFoodSource(lowercase) = %v, want ErrValidation", err)
	}
	if _, err := api.ParseFoodSource("MYFITNESSPAL"); !errors.Is(err, client.ErrValidation) {
		t.Errorf("ParseFoodSource(unknown) = %v, want ErrValidation", err)
	}
}

// TestNewNutritionRefusesANilRequestLayer keeps the constructor consistent with
// every other domain client.
func TestNewNutritionRefusesANilRequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewNutrition(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Errorf("NewNutrition(nil) = %v, want ErrNotConfigured", err)
	}
}
