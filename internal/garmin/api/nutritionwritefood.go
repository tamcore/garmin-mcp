package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// maxServingUnitLen bounds the serving-unit code a caller supplies, matching
// the short catalog codes CustomFoodServingUnits reads (nutrition.py:256).
const maxServingUnitLen = 16

// numString formats a value as string, dropping ".0" for a whole number, the
// same wire form _num_to_str builds: Garmin's nutrition write fields expect
// integer strings like "160", never "160.0".
// Source: nutrition.py:14-19.
func numString(value float64) string {
	if value == math.Trunc(value) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// CustomFoodFacts is the strict request model for a custom food's identity and
// nutrition facts. It is shared by create and update, matching upstream:
// create_custom_food and update_custom_food PUT the identical shape, differing
// only in whether a foodId and servingId are already known
// (nutrition.py:319-359, :469-501).
type CustomFoodFacts struct {
	// FoodName names the food. Required.
	FoodName string
	// BrandName is optional: nil omits the field entirely, matching upstream's
	// "if brand_name is not None" (nutrition.py:352-354). A non-nil pointer to
	// an empty or all-whitespace string is refused rather than sent as
	// `"brandName": ""` the way upstream's own caller could: upstream places no
	// validation of its own on brand_name, but this package treats every
	// free-text write field the same defensive way, and an empty brand name
	// carries no information a caller could not have conveyed by passing nil
	// instead. A tool layer that wants to mirror upstream's own permissiveness
	// must map its own caller's empty string to nil before it reaches here,
	// not the other way around.
	BrandName *string
	// ServingUnit is the serving-size unit, for example "G", "ML" or "OZ".
	// Required.
	ServingUnit string
	// NumberOfUnits is the serving size in ServingUnit. Required, must be
	// positive.
	NumberOfUnits float64
	// Calories is the calorie count per serving. Required, must not be
	// negative.
	Calories float64

	// The remaining fields are every optional nutrient create_custom_food
	// accepts (nutrition.py:274-287). A nil field is omitted from the request,
	// matching upstream's "only include optional fields that have values"
	// (nutrition.py:326-344). Every non-nil value must not be negative.
	Carbs        *float64
	Protein      *float64
	Fat          *float64
	Fiber        *float64
	Sugar        *float64
	SaturatedFat *float64
	Sodium       *float64
	Cholesterol  *float64
	Potassium    *float64
	TransFat     *float64
	Calcium      *float64
	Iron         *float64
	VitaminD     *float64
}

// optionalNutrients lists the pointer, JSON key pairs for CustomFoodFacts'
// optional fields, in the order nutrition.py builds them.
func (f CustomFoodFacts) optionalNutrients() []struct {
	value *float64
	key   string
} {
	return []struct {
		value *float64
		key   string
	}{
		{f.Carbs, "carbs"}, {f.Protein, "protein"}, {f.Fat, "fat"},
		{f.Fiber, "fiber"}, {f.Sugar, "sugar"}, {f.SaturatedFat, "saturatedFat"},
		{f.Sodium, "sodium"}, {f.Cholesterol, "cholesterol"}, {f.Potassium, "potassium"},
		{f.TransFat, "transFat"}, {f.Calcium, "calcium"}, {f.Iron, "iron"},
		{f.VitaminD, "vitaminD"},
	}
}

// maxNutritionMagnitude bounds every numeric write field this package sends
// to Garmin — a calorie count, a gram figure, a serving multiplier. It is
// headroom far above any real nutrition value, chosen only to keep a
// malformed or hostile magnitude like 1e300 from ever reaching numString,
// where it renders as an unbounded digit string.
const maxNutritionMagnitude = 1e9

// requireFiniteNutrient refuses NaN, +/-Inf and a magnitude beyond
// maxNutritionMagnitude.
//
// A "< 0" check alone lets both through: NaN compares false to every
// ordering operator including "< 0", so it is never caught that way, and
// int64(+Inf) truncates to the platform's most negative int64 (a huge
// negative number) only after a numeric field has already been written as a
// string via numString — by which point the sign-based check has long since
// passed on the original, still-positive float.
func requireFiniteNutrient(req client.Request, value float64, field string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > maxNutritionMagnitude {
		return invalid(req, fmt.Errorf("%w: %s must be a finite number within range",
			client.ErrValidation, field))
	}
	return nil
}

// requireBoundedText validates a free-text field the same way write.go's
// requireText does — present once trimmed, free of control characters — but
// against a caller-supplied bound instead of requireText's general
// MaxTextLen, matching ServingUnit's short, closed catalog (nutrition.py:256,
// "e.g. G, ML, OZ"). It returns the trimmed value, which is what must reach
// the wire: validating a trimmed form and then sending the untrimmed
// original would let through exactly what was just rejected.
func requireBoundedText(req client.Request, value, field string, maxLen int) (string, error) {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		return "", invalid(req, fmt.Errorf("%w: %s must not be empty", client.ErrValidation, field))
	case len(trimmed) > maxLen:
		return "", invalid(req, fmt.Errorf("%w: %s is too long", client.ErrValidation, field))
	case hasControlRune(trimmed):
		return "", invalid(req, fmt.Errorf("%w: %s must not contain control characters",
			client.ErrValidation, field))
	}
	return trimmed, nil
}

// validate reports whether the facts may be dispatched, returning the facts
// with every free-text field normalized to what was actually validated — the
// trimmed serving unit and brand name — so the caller's untrimmed original
// never reaches the wire.
func (f CustomFoodFacts) validate(req client.Request) (CustomFoodFacts, error) {
	// Trimmed before requireText the same way BrandName already is: requireText
	// itself neither trims nor rejects a leading/trailing space or a bare "\n\n",
	// so an untrimmed food name would otherwise reach Garmin verbatim while an
	// all-whitespace BrandName is refused.
	trimmedName := strings.TrimSpace(f.FoodName)
	name, err := requireText(req, trimmedName, "food name")
	if err != nil {
		return CustomFoodFacts{}, err
	}
	f.FoodName = name

	if f.BrandName != nil {
		trimmedBrand := strings.TrimSpace(*f.BrandName)
		brand, err := requireText(req, trimmedBrand, "brand name")
		if err != nil {
			return CustomFoodFacts{}, err
		}
		f.BrandName = &brand
	}

	unit, err := requireBoundedText(req, f.ServingUnit, "serving unit", maxServingUnitLen)
	if err != nil {
		return CustomFoodFacts{}, err
	}
	f.ServingUnit = unit

	if err := requireFiniteNutrient(req, f.NumberOfUnits, "number of units"); err != nil {
		return CustomFoodFacts{}, err
	}
	if f.NumberOfUnits <= 0 {
		return CustomFoodFacts{}, invalid(req, fmt.Errorf("%w: number of units must be positive",
			client.ErrValidation))
	}
	if err := requireFiniteNutrient(req, f.Calories, "calories"); err != nil {
		return CustomFoodFacts{}, err
	}
	if f.Calories < 0 {
		return CustomFoodFacts{}, invalid(req, fmt.Errorf("%w: calories must not be negative",
			client.ErrValidation))
	}
	for _, nutrient := range f.optionalNutrients() {
		if nutrient.value == nil {
			continue
		}
		if err := requireFiniteNutrient(req, *nutrient.value, nutrient.key); err != nil {
			return CustomFoodFacts{}, err
		}
		if *nutrient.value < 0 {
			return CustomFoodFacts{}, invalid(req, fmt.Errorf("%w: %s must not be negative",
				client.ErrValidation, nutrient.key))
		}
	}
	return f, nil
}

// nutritionContentDTO is the wire shape of one nutritionContents entry. Every
// numeric field is a string, matching _num_to_str's wire form.
type nutritionContentDTO struct {
	ServingID     string `json:"servingId,omitempty"`
	ServingUnit   string `json:"servingUnit"`
	NumberOfUnits string `json:"numberOfUnits"`
	Calories      string `json:"calories"`
	Carbs         string `json:"carbs,omitempty"`
	Protein       string `json:"protein,omitempty"`
	Fat           string `json:"fat,omitempty"`
	Fiber         string `json:"fiber,omitempty"`
	Sugar         string `json:"sugar,omitempty"`
	SaturatedFat  string `json:"saturatedFat,omitempty"`
	Sodium        string `json:"sodium,omitempty"`
	Cholesterol   string `json:"cholesterol,omitempty"`
	Potassium     string `json:"potassium,omitempty"`
	TransFat      string `json:"transFat,omitempty"`
	Calcium       string `json:"calcium,omitempty"`
	Iron          string `json:"iron,omitempty"`
	VitaminD      string `json:"vitaminD,omitempty"`
}

// foodMetaDataDTO is the wire shape of the foodMetaData object a create or
// update PUT carries.
type foodMetaDataDTO struct {
	FoodID       string `json:"foodId,omitempty"`
	FoodName     string `json:"foodName"`
	FoodType     string `json:"foodType"`
	Source       string `json:"source"`
	RegionCode   string `json:"regionCode"`
	LanguageCode string `json:"languageCode"`
	BrandName    string `json:"brandName,omitempty"`
}

// customFoodDTO is the full PUT body create and update share.
type customFoodDTO struct {
	FoodMetaData      foodMetaDataDTO       `json:"foodMetaData"`
	NutritionContents []nutritionContentDTO `json:"nutritionContents"`
}

// buildCustomFoodBody renders facts as the PUT body, optionally carrying an
// existing food and serving identifier for an update.
func buildCustomFoodBody(facts CustomFoodFacts, id FoodID, serving ServingID) customFoodDTO {
	brand := ""
	if facts.BrandName != nil {
		brand = *facts.BrandName
	}
	content := nutritionContentDTO{
		ServingID:     serving.String(),
		ServingUnit:   facts.ServingUnit,
		NumberOfUnits: numString(facts.NumberOfUnits),
		Calories:      numString(facts.Calories),
	}
	assignments := []struct {
		dst   *string
		value *float64
	}{
		{&content.Carbs, facts.Carbs}, {&content.Protein, facts.Protein}, {&content.Fat, facts.Fat},
		{&content.Fiber, facts.Fiber}, {&content.Sugar, facts.Sugar},
		{&content.SaturatedFat, facts.SaturatedFat}, {&content.Sodium, facts.Sodium},
		{&content.Cholesterol, facts.Cholesterol}, {&content.Potassium, facts.Potassium},
		{&content.TransFat, facts.TransFat}, {&content.Calcium, facts.Calcium},
		{&content.Iron, facts.Iron}, {&content.VitaminD, facts.VitaminD},
	}
	for _, assignment := range assignments {
		if assignment.value != nil {
			*assignment.dst = numString(*assignment.value)
		}
	}

	return customFoodDTO{
		FoodMetaData: foodMetaDataDTO{
			FoodID:       id.String(),
			FoodName:     facts.FoodName,
			FoodType:     client.FoodTypeGeneric,
			Source:       client.FoodSourceGarmin,
			RegionCode:   client.RegionCodeUS,
			LanguageCode: client.LanguageCodeEN,
			BrandName:    brand,
		},
		NutritionContents: []nutritionContentDTO{content},
	}
}

// CreateCustomFood creates a custom food in the user's Garmin nutrition
// library.
//
// Its effect is EffectUnsafeWrite, not EffectIdempotentWrite: compat/tools.json
// classifies create_custom_food as non-idempotent ("repeats create
// duplicates"), because each PUT with no target identifier creates a new
// record rather than replacing one, so the retry layer must never replay a
// lost response the way it may for UpdateCustomFood.
//
// Source: create_custom_food, PUT "/nutrition-service/customFood"
// (nutrition.py:360-363).
func (n *Nutrition) CreateCustomFood(
	ctx context.Context, session client.Session, facts CustomFoodFacts,
) (FoodItem, error) {
	return n.saveCustomFood(ctx, session, client.OpCreateCustomFood, client.EffectUnsafeWrite,
		facts, FoodID{}, ServingID{})
}

// UpdateCustomFood updates an existing custom food. The caller supplies the
// full nutrition facts: this package does not read the existing record and
// merge omitted fields the way nutrition.py's update_custom_food does, because
// that lookup-by-name-and-merge behavior is a caller-facing convenience, not a
// requirement of the wire format. Garmin's PUT replaces the whole record, so
// any optional nutrient the caller omits here is cleared on Garmin's side,
// not preserved — the caller must resend every field it wants to keep.
//
// Source: update_custom_food, PUT "/nutrition-service/customFood"
// (nutrition.py:502-505).
func (n *Nutrition) UpdateCustomFood(
	ctx context.Context, session client.Session, id FoodID, serving ServingID, facts CustomFoodFacts,
) (FoodItem, error) {
	if id.IsZero() {
		req := writeRequest(client.OpUpdateCustomFood, client.EndpointNutritionCustomFood,
			http.MethodPut, client.PathNutritionCustomFood, client.EffectIdempotentWrite)
		return FoodItem{}, invalid(req, fmt.Errorf("%w: a food id is required to update a custom food",
			client.ErrValidation))
	}
	if serving.IsZero() {
		req := writeRequest(client.OpUpdateCustomFood, client.EndpointNutritionCustomFood,
			http.MethodPut, client.PathNutritionCustomFood, client.EffectIdempotentWrite)
		return FoodItem{}, invalid(req, fmt.Errorf(
			"%w: a serving id is required to update a custom food", client.ErrValidation))
	}
	return n.saveCustomFood(ctx, session, client.OpUpdateCustomFood, client.EffectIdempotentWrite,
		facts, id, serving)
}

// saveCustomFood performs the PUT both CreateCustomFood and UpdateCustomFood
// share. effect is chosen by the caller because the two operations do not
// share one: a create may not be repeated, an update may.
func (n *Nutrition) saveCustomFood(
	ctx context.Context, session client.Session, op client.Op, effect client.Effect,
	facts CustomFoodFacts, id FoodID, serving ServingID,
) (FoodItem, error) {
	req := writeRequest(op, client.EndpointNutritionCustomFood,
		http.MethodPut, client.PathNutritionCustomFood, effect)
	validated, err := facts.validate(req)
	if err != nil {
		return FoodItem{}, err
	}

	body, err := jsonBody(req, buildCustomFoodBody(validated, id, serving))
	if err != nil {
		return FoodItem{}, err
	}
	req.Body = body

	var item FoodItem
	if _, err := n.req.write(ctx, session, req, &item); err != nil {
		return FoodItem{}, err
	}
	return item, nil
}

// DeleteCustomFood removes a custom food from the user's Garmin nutrition
// library.
//
// Source: delete_custom_food, DELETE
// "/nutrition-service/customFood/{food_id}" (nutrition.py:530-531).
func (n *Nutrition) DeleteCustomFood(
	ctx context.Context, session client.Session, id FoodID,
) (WriteResult, error) {
	req := writeRequest(client.OpDeleteCustomFood, client.EndpointNutritionCustomFood,
		http.MethodDelete, client.PathNutritionCustomFood+"/"+id.String(), client.EffectDelete)
	if id.IsZero() {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: a food id is required",
			client.ErrValidation))
	}

	payload, err := n.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}
