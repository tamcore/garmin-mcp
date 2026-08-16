package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// garminTimestampLayout is the literal log-timestamp form every food-log write
// sends. Source: nutrition.py:599-601,
// datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.000Z") — the
// milliseconds are always literally ".000", never the real sub-second value.
const garminTimestampLayout = "2006-01-02T15:04:05.000Z"

// renderLogTimestamp renders instant in the fixed millisecond form upstream
// sends, in UTC regardless of the instant's own location.
func renderLogTimestamp(instant time.Time) string {
	return instant.UTC().Format(garminTimestampLayout)
}

// MealTime is a validated time-of-day in Garmin's "HH:MM:SS" form, matched
// lexicographically against a meal's startTime/endTime window.
// Source: nutrition.py's meal_time parameter, "Time in HH:MM:SS format"
// (nutrition.py:573).
type MealTime struct {
	value string
}

// mealTimeLayout is the wall-clock form a meal time is validated against.
const mealTimeLayout = "15:04:05"

// ParseMealTime validates a time-of-day in HH:MM:SS form.
func ParseMealTime(value string) (MealTime, error) {
	trimmed := strings.TrimSpace(value)
	if _, err := time.Parse(mealTimeLayout, trimmed); err != nil {
		return MealTime{}, fmt.Errorf("%w: meal time must be in HH:MM:SS form", client.ErrValidation)
	}
	return MealTime{value: trimmed}, nil
}

// IsZero reports whether the meal time is unset.
func (m MealTime) IsZero() bool { return m.value == "" }

// String is the validated HH:MM:SS value, or "".
func (m MealTime) String() string { return m.value }

// FoodSource is the food namespace a logged item belongs to: the user's own
// Garmin custom-food library, or a FatSecret catalog entry.
// Source: nutrition.py:552-565.
type FoodSource string

// The two source namespaces log_custom_food accepts.
const (
	SourceGarmin    FoodSource = client.FoodSourceGarmin
	SourceFatSecret FoodSource = client.FoodSourceFatSecret
)

// ParseFoodSource validates a food source against the two Garmin recognizes.
func ParseFoodSource(value string) (FoodSource, error) {
	switch FoodSource(value) {
	case SourceGarmin, SourceFatSecret:
		return FoodSource(value), nil
	default:
		return "", fmt.Errorf("%w: food source must be %q or %q",
			client.ErrValidation, SourceGarmin, SourceFatSecret)
	}
}

// LogCustomFoodEntry is the strict request model for logging a food item from
// either food namespace to a meal.
//
// Source: log_custom_food (nutrition.py:546-634), whose payload carries a
// caller-supplied food, serving and meal identifier plus a server-timestamped
// log entry.
type LogCustomFoodEntry struct {
	MealDate   client.Date
	MealTime   MealTime
	MealID     int64
	FoodID     FoodID
	ServingID  ServingID
	ServingQty float64
	Source     FoodSource
	// LoggedAt is the instant the entry is logged at, rendered in Garmin's
	// fixed millisecond form. The caller supplies it so this package needs no
	// clock of its own.
	LoggedAt time.Time
}

// validate reports whether the entry may be dispatched.
func (e LogCustomFoodEntry) validate(req client.Request) error {
	switch {
	case e.MealDate.IsZero():
		return invalid(req, fmt.Errorf("%w: a meal date is required", client.ErrValidation))
	case e.MealTime.IsZero():
		return invalid(req, fmt.Errorf("%w: a meal time is required", client.ErrValidation))
	case e.MealID <= 0:
		return invalid(req, fmt.Errorf("%w: a positive meal id is required", client.ErrValidation))
	case e.FoodID.IsZero():
		return invalid(req, fmt.Errorf("%w: a food id is required", client.ErrValidation))
	case e.ServingID.IsZero():
		return invalid(req, fmt.Errorf("%w: a serving id is required", client.ErrValidation))
	case e.ServingQty <= 0:
		return invalid(req, fmt.Errorf("%w: serving quantity must be positive", client.ErrValidation))
	}
	if err := requireFiniteNutrient(req, e.ServingQty, "serving quantity"); err != nil {
		return err
	}
	switch {
	case e.Source != SourceGarmin && e.Source != SourceFatSecret:
		return invalid(req, fmt.Errorf("%w: food source must be %q or %q",
			client.ErrValidation, SourceGarmin, SourceFatSecret))
	case e.LoggedAt.IsZero():
		return invalid(req, fmt.Errorf("%w: a log instant is required", client.ErrValidation))
	}
	return nil
}

// foodLogItemDTO is the wire shape of one foodLogItems entry.
// Source: nutrition.py:605-618.
type foodLogItemDTO struct {
	LogTimestamp string  `json:"logTimestamp"`
	LogSource    string  `json:"logSource"`
	LogCategory  string  `json:"logCategory"`
	MealTime     string  `json:"mealTime"`
	Action       string  `json:"action"`
	MealID       int64   `json:"mealId"`
	FoodID       string  `json:"foodId"`
	ServingID    string  `json:"servingId"`
	Source       string  `json:"source"`
	RegionCode   string  `json:"regionCode"`
	LanguageCode string  `json:"languageCode"`
	ServingQty   float64 `json:"servingQty"`
}

// foodLogRequestDTO is the PUT body log_custom_food sends.
// Source: nutrition.py:602-620.
type foodLogRequestDTO struct {
	MealDate     string           `json:"mealDate"`
	FoodLogItems []foodLogItemDTO `json:"foodLogItems"`
}

// LogCustomFood logs a food item from the user's custom library or the
// FatSecret catalog to a meal.
//
// Source: log_custom_food, PUT "/nutrition-service/food/logs"
// (nutrition.py:621-624).
func (n *Nutrition) LogCustomFood(
	ctx context.Context, session client.Session, entry LogCustomFoodEntry,
) (WriteResult, error) {
	req := writeRequest(client.OpLogCustomFood, client.EndpointNutritionFoodLog,
		http.MethodPut, client.PathNutritionFoodLogs, client.EffectUnsafeWrite)
	if err := entry.validate(req); err != nil {
		return WriteResult{}, err
	}

	body, err := jsonBody(req, foodLogRequestDTO{
		MealDate: entry.MealDate.String(),
		FoodLogItems: []foodLogItemDTO{{
			LogTimestamp: renderLogTimestamp(entry.LoggedAt),
			LogSource:    client.LogSourceGCW,
			LogCategory:  client.LogCategoryRegular,
			MealTime:     entry.MealTime.String(),
			Action:       client.FoodLogActionAdd,
			MealID:       entry.MealID,
			FoodID:       entry.FoodID.String(),
			ServingID:    entry.ServingID.String(),
			Source:       string(entry.Source),
			RegionCode:   client.RegionCodeUS,
			LanguageCode: client.LanguageCodeEN,
			ServingQty:   entry.ServingQty,
		}},
	})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body

	payload, err := n.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}

// LogFoodEntry is the strict request model for a quick-add food-log entry:
// name and macros, with no food or serving identifier.
//
// Source: log_food (nutrition.py:637-717).
type LogFoodEntry struct {
	MealDate client.Date
	MealTime MealTime
	MealID   int64
	Name     string
	Calories float64
	Carbs    float64
	Protein  float64
	Fat      float64
	LoggedAt time.Time
}

// validate reports whether the entry may be dispatched, returning the entry
// with Name normalized to its trimmed form — matching CustomFoodFacts'
// BrandName and FoodName — so the caller's untrimmed original never reaches
// the wire.
func (e LogFoodEntry) validate(req client.Request) (LogFoodEntry, error) {
	switch {
	case e.MealDate.IsZero():
		return LogFoodEntry{}, invalid(req, fmt.Errorf("%w: a meal date is required", client.ErrValidation))
	case e.MealTime.IsZero():
		return LogFoodEntry{}, invalid(req, fmt.Errorf("%w: a meal time is required", client.ErrValidation))
	case e.MealID <= 0:
		return LogFoodEntry{}, invalid(req, fmt.Errorf("%w: a positive meal id is required", client.ErrValidation))
	case e.LoggedAt.IsZero():
		return LogFoodEntry{}, invalid(req, fmt.Errorf("%w: a log instant is required", client.ErrValidation))
	}
	// Trimmed before requireText: requireText itself neither trims nor rejects
	// a leading/trailing space or a bare "\n\n", so an untrimmed name would
	// otherwise reach Garmin verbatim.
	name, err := requireText(req, strings.TrimSpace(e.Name), "food name")
	if err != nil {
		return LogFoodEntry{}, err
	}
	e.Name = name
	for _, macro := range [...]struct {
		value float64
		field string
	}{{e.Calories, "calories"}, {e.Carbs, "carbs"}, {e.Protein, "protein"}, {e.Fat, "fat"}} {
		if err := requireFiniteNutrient(req, macro.value, macro.field); err != nil {
			return LogFoodEntry{}, err
		}
		if macro.value < 0 {
			return LogFoodEntry{}, invalid(req, fmt.Errorf("%w: %s must not be negative",
				client.ErrValidation, macro.field))
		}
	}
	return e, nil
}

// quickAddItemDTO is the wire shape of one quickAddItems entry.
// Source: nutrition.py:688-702.
type quickAddItemDTO struct {
	Name         string  `json:"name"`
	LogID        *string `json:"logId"`
	LogTimestamp string  `json:"logTimestamp"`
	LogSource    string  `json:"logSource"`
	LogCategory  string  `json:"logCategory"`
	MealTime     string  `json:"mealTime"`
	MealID       int64   `json:"mealId"`
	Action       string  `json:"action"`
	Calories     string  `json:"calories"`
	Carbs        string  `json:"carbs"`
	Protein      string  `json:"protein"`
	Fat          string  `json:"fat"`
}

// quickAddRequestDTO is the PUT body log_food sends.
// Source: nutrition.py:685-703.
type quickAddRequestDTO struct {
	MealDate      string            `json:"mealDate"`
	QuickAddItems []quickAddItemDTO `json:"quickAddItems"`
}

// LogFood quick-adds a food entry by name and macros, without a food or
// serving identifier.
//
// Source: log_food, PUT "/nutrition-service/food/logs/quickAdd"
// (nutrition.py:704-707).
func (n *Nutrition) LogFood(
	ctx context.Context, session client.Session, entry LogFoodEntry,
) (WriteResult, error) {
	req := writeRequest(client.OpLogFood, client.EndpointNutritionFoodLogQuickAdd,
		http.MethodPut, client.PathNutritionFoodLogQuickAdd, client.EffectUnsafeWrite)
	validated, err := entry.validate(req)
	if err != nil {
		return WriteResult{}, err
	}
	entry = validated

	body, err := jsonBody(req, quickAddRequestDTO{
		MealDate: entry.MealDate.String(),
		QuickAddItems: []quickAddItemDTO{{
			Name:         entry.Name,
			LogID:        nil,
			LogTimestamp: renderLogTimestamp(entry.LoggedAt),
			LogSource:    client.LogSourceGCW,
			LogCategory:  client.LogCategoryQuickAdd,
			MealTime:     entry.MealTime.String(),
			MealID:       entry.MealID,
			Action:       client.FoodLogActionAdd,
			Calories:     numString(entry.Calories),
			Carbs:        numString(entry.Carbs),
			Protein:      numString(entry.Protein),
			Fat:          numString(entry.Fat),
		}},
	})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body

	payload, err := n.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}

// deleteFoodLogBody is the DELETE body delete_food_log sends.
// Source: nutrition.py:734, json={"logIds": [log_id]}.
type deleteFoodLogBody struct {
	LogIDs []string `json:"logIds"`
}

// DeleteFoodLog removes one logged food entry.
//
// Source: delete_food_log, DELETE "/nutrition-service/food/logs/{meal_date}"
// with a logIds body (nutrition.py:733-734).
func (n *Nutrition) DeleteFoodLog(
	ctx context.Context, session client.Session, date client.Date, log LogID,
) (WriteResult, error) {
	req := writeRequest(client.OpDeleteFoodLog, client.EndpointNutritionFoodLog,
		http.MethodDelete, "", client.EffectDelete)
	path, err := datedNutritionPath(req, client.PathNutritionFoodLogPrefix, date)
	if err != nil {
		return WriteResult{}, err
	}
	req.Path = path
	if log.IsZero() {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: a log id is required",
			client.ErrValidation))
	}

	body, err := jsonBody(req, deleteFoodLogBody{LogIDs: []string{log.String()}})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body

	payload, err := n.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}
