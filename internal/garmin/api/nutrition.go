package api

import (
	"fmt"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Nutrition reads and writes the food-log, meal, settings and custom-food
// surface.
//
// Source: get_nutrition_daily_food_log, get_nutrition_daily_meals and
// get_nutrition_daily_settings in python-garminconnect 0.3.10
// garminconnect/__init__.py, plus set_nutrition_daily_settings, search_foods,
// get_custom_foods, get_custom_food_serving_units, create_custom_food,
// update_custom_food, delete_custom_food, log_custom_food, log_food and
// delete_food_log in the Taxuspt pinned curation at src/garmin_mcp/nutrition.py
// — upstream 0.3.10 carries only the three date-keyed reads, and every other
// method here belongs to the pinned tool surface.
//
// Every document this client reads or writes is health and identity data: a
// calorie figure, a macro gram count and a food name are all readings tied to a
// person, so no model here is ever logged with its content, only its shape.
type Nutrition struct {
	req requester
}

// NewNutrition returns a nutrition client over the request layer.
func NewNutrition(rc *client.Client) (*Nutrition, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Nutrition{req: req}, nil
}

// maxTokenLength bounds a caller-supplied food, serving or log identifier. Every
// identifier this package has observed is far shorter — a 32-character hex UUID
// or a short decimal string — so the bound is generous headroom, not a real
// limit.
const maxTokenLength = 64

// FoodID is a validated food identifier: a 32-character hex UUID for a Garmin
// custom food, or a decimal string for a FatSecret catalog food. Source: the
// "Garmin custom food IDs are 32-char hex UUIDs; FatSecret IDs are numeric
// strings" note on log_custom_food (nutrition.py:567).
//
// Only a parsed value reaches a URL path — delete_custom_food appends it as one
// segment — so it can carry no path separator and no traversal segment.
type FoodID struct {
	value string
}

// ParseFoodID validates a food identifier for use as a URL path segment or a
// request-body value.
func ParseFoodID(value string) (FoodID, error) {
	token, err := parseDecimalOrHexToken(value, "food id")
	if err != nil {
		return FoodID{}, err
	}
	return FoodID{value: token}, nil
}

// IsZero reports whether the identifier is unset.
func (f FoodID) IsZero() bool { return f.value == "" }

// String is the validated identifier, or "".
func (f FoodID) String() string { return f.value }

// ServingID is a validated serving identifier, carried in a food-create,
// food-update or food-log request body.
//
// Its wire format is not evidenced upstream: nutrition.py:186, :470 and :613
// only ever pass servingId through as an opaque string
// ("serving_id": s.get("servingId"), "servingId": serving_id), never
// constraining its shape. Garmin's own custom-food ids are documented as
// 32-char hex UUIDs (nutrition.py:567), and a Garmin-minted serving id is at
// least as plausible as a FatSecret decimal one, so ServingID accepts the same
// two shapes FoodID does rather than refusing a hex value before any request
// is even sent.
type ServingID struct {
	value string
}

// ParseServingID validates a serving identifier: a decimal string or a
// hexIdentifierLength-character hex id, matching FoodID's accepted shapes.
func ParseServingID(value string) (ServingID, error) {
	token, err := parseDecimalOrHexToken(value, "serving id")
	if err != nil {
		return ServingID{}, err
	}
	return ServingID{value: token}, nil
}

// IsZero reports whether the identifier is unset.
func (s ServingID) IsZero() bool { return s.value == "" }

// String is the validated identifier, or "".
func (s ServingID) String() string { return s.value }

// LogID is a validated food-log entry identifier, a 32-character hex UUID.
// Source: "log_id: ... a 32-char hex UUID" on delete_food_log
// (nutrition.py:728).
type LogID struct {
	value string
}

// ParseLogID validates a food-log entry identifier.
func ParseLogID(value string) (LogID, error) {
	token, err := parseDecimalOrHexToken(value, "log id")
	if err != nil {
		return LogID{}, err
	}
	return LogID{value: token}, nil
}

// IsZero reports whether the identifier is unset.
func (l LogID) IsZero() bool { return l.value == "" }

// String is the validated identifier, or "".
func (l LogID) String() string { return l.value }

// hexIdentifierLength is the exact length of a Garmin custom-food or food-log
// identifier when it is not a plain decimal string.
// Source: "Garmin custom food IDs are 32-char hex UUIDs; FatSecret IDs are
// numeric strings" (nutrition.py:567) for FoodID, and "a 32-char hex UUID"
// (nutrition.py:728) for LogID.
const hexIdentifierLength = 32

// parseDecimalOrHexToken validates a FoodID or LogID: either a run of decimal
// digits (a FatSecret catalog id, or any other Garmin-issued decimal id), or
// exactly hexIdentifierLength hex characters (a Garmin UUID). Neither shape
// can carry a path separator, a traversal segment or a control character, so
// no separate check for those is needed once the shape is this narrow.
func parseDecimalOrHexToken(value, field string) (string, error) {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		return "", fmt.Errorf("%w: %s must not be empty", client.ErrValidation, field)
	case len(trimmed) > maxTokenLength:
		return "", fmt.Errorf("%w: %s is too long", client.ErrValidation, field)
	case isDecimalToken(trimmed):
		return trimmed, nil
	case len(trimmed) == hexIdentifierLength && isHexToken(trimmed):
		return trimmed, nil
	default:
		return "", fmt.Errorf("%w: %s must be a decimal id or a %d-character hex id",
			client.ErrValidation, field, hexIdentifierLength)
	}
}

// isDecimalToken reports whether value is one or more decimal digits.
func isDecimalToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isHexToken reports whether value is one or more hex digits, either case.
func isHexToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// FoodMetaData is the identity half of a food entry, shared by Garmin's custom
// library and the FatSecret catalog search_foods also reaches.
//
// Source: the foodMetaData keys search_foods reads (nutrition.py:182-208) and
// create_custom_food/update_custom_food write (nutrition.py:346-354,
// :487-496). FoodID is client.Text rather than a parsed FoodID because a read
// response is not a URL path: a hostile or malformed value here can only be
// echoed back to the caller, never interpolated anywhere.
type FoodMetaData struct {
	FoodID       client.Text `json:"foodId"`
	FoodName     *string     `json:"foodName"`
	FoodType     client.Text `json:"foodType"`
	Source       client.Text `json:"source"`
	RegionCode   client.Text `json:"regionCode"`
	LanguageCode client.Text `json:"languageCode"`
	BrandName    *string     `json:"brandName"`
}

// NutritionContent is one serving's worth of nutrition facts, shared by the
// FatSecret catalog and Garmin's custom-food library.
//
// Source: the nutritionContents keys search_foods reads (nutrition.py:184-195)
// and create_custom_food writes (nutrition.py:321-344, the full optional-field
// set). Every macro and micro is optional: search_foods only ever reads the six
// it re-exports, and a custom food need not carry every micro at all.
type NutritionContent struct {
	ServingID     client.Text   `json:"servingId"`
	ServingUnit   client.Text   `json:"servingUnit"`
	NumberOfUnits client.Number `json:"numberOfUnits"`
	Calories      client.Number `json:"calories"`
	Carbs         client.Number `json:"carbs"`
	Protein       client.Number `json:"protein"`
	Fat           client.Number `json:"fat"`
	Fiber         client.Number `json:"fiber"`
	Sugar         client.Number `json:"sugar"`
	SaturatedFat  client.Number `json:"saturatedFat"`
	Sodium        client.Number `json:"sodium"`
	Cholesterol   client.Number `json:"cholesterol"`
	Potassium     client.Number `json:"potassium"`
	TransFat      client.Number `json:"transFat"`
	Calcium       client.Number `json:"calcium"`
	Iron          client.Number `json:"iron"`
	VitaminD      client.Number `json:"vitaminD"`
}

// FoodItem is one food entry: its identity and every serving Garmin sent for
// it. It is the shape search_foods, get_custom_foods, create_custom_food and
// update_custom_food all share.
type FoodItem struct {
	Meta     *FoodMetaData                 `json:"foodMetaData"`
	Contents client.List[NutritionContent] `json:"nutritionContents"`
}

// datedNutritionPath builds a date-keyed nutrition path and validates the date
// before it can be interpolated.
func datedNutritionPath(
	req client.Request, prefix string, date client.Date,
) (string, error) {
	if err := requireDate(req, date); err != nil {
		return "", err
	}
	return datedPath(prefix, date), nil
}
