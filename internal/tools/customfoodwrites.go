package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the custom-food write tools.
const (
	ToolCreateCustomFood = "create_custom_food"
	ToolUpdateCustomFood = "update_custom_food"
	ToolDeleteCustomFood = "delete_custom_food"
)

// defaultServingUnit and defaultNumberOfUnits are the manifest defaults every
// custom-food write shares (nutrition.py:271-273, :378-380).
const (
	defaultServingUnit   = "G"
	defaultNumberOfUnits = 100.0
)

// customFoodFactsFields is the nutrition-facts argument set create_custom_food and
// update_custom_food share (nutrition.py:267-287, :373-393): a name and calories,
// plus every optional nutrient create_custom_food accepts.
type customFoodFactsFields struct {
	FoodName      string   `json:"food_name" jsonschema:"the custom food's name"`
	Calories      float64  `json:"calories" jsonschema:"calories per serving"`
	ServingUnit   *string  `json:"serving_unit,omitempty" jsonschema:"the serving-size unit, default G"`
	NumberOfUnits *float64 `json:"number_of_units,omitempty" jsonschema:"the serving size, default 100"`
	BrandName     *string  `json:"brand_name,omitempty" jsonschema:"the brand or vendor name"`
	Carbs         *float64 `json:"carbs,omitempty" jsonschema:"carbohydrates in grams per serving"`
	Protein       *float64 `json:"protein,omitempty" jsonschema:"protein in grams per serving"`
	Fat           *float64 `json:"fat,omitempty" jsonschema:"total fat in grams per serving"`
	Fiber         *float64 `json:"fiber,omitempty" jsonschema:"fiber in grams per serving"`
	Sugar         *float64 `json:"sugar,omitempty" jsonschema:"sugar in grams per serving"`
	SaturatedFat  *float64 `json:"saturated_fat,omitempty" jsonschema:"saturated fat in grams per serving"`
	Sodium        *float64 `json:"sodium,omitempty" jsonschema:"sodium in milligrams per serving"`
	Cholesterol   *float64 `json:"cholesterol,omitempty" jsonschema:"cholesterol in milligrams per serving"`
	Potassium     *float64 `json:"potassium,omitempty" jsonschema:"potassium in milligrams per serving"`
	TransFat      *float64 `json:"trans_fat,omitempty" jsonschema:"trans fat in grams per serving"`
	Calcium       *float64 `json:"calcium,omitempty" jsonschema:"calcium in milligrams per serving, absolute, not %DV"`
	Iron          *float64 `json:"iron,omitempty" jsonschema:"iron in milligrams per serving, absolute, not %DV"`
	VitaminD      *float64 `json:"vitamin_d,omitempty" jsonschema:"vitamin D in micrograms per serving, absolute, not %DV"`
}

// toFacts renders the fields as the domain client's strict request model, applying
// the manifest's default serving unit and size. Every numeric bound (finite, not
// negative) is enforced by api.CustomFoodFacts.validate, not duplicated here.
func (f customFoodFactsFields) toFacts() api.CustomFoodFacts {
	unit := defaultServingUnit
	if f.ServingUnit != nil {
		unit = *f.ServingUnit
	}
	units := defaultNumberOfUnits
	if f.NumberOfUnits != nil {
		units = *f.NumberOfUnits
	}
	return api.CustomFoodFacts{
		FoodName:      f.FoodName,
		BrandName:     f.BrandName,
		ServingUnit:   unit,
		NumberOfUnits: units,
		Calories:      f.Calories,
		Carbs:         f.Carbs,
		Protein:       f.Protein,
		Fat:           f.Fat,
		Fiber:         f.Fiber,
		Sugar:         f.Sugar,
		SaturatedFat:  f.SaturatedFat,
		Sodium:        f.Sodium,
		Cholesterol:   f.Cholesterol,
		Potassium:     f.Potassium,
		TransFat:      f.TransFat,
		Calcium:       f.Calcium,
		Iron:          f.Iron,
		VitaminD:      f.VitaminD,
	}
}

// customFoodFactsProperties declares the nutrition-facts schema properties create
// and update share.
func customFoodFactsProperties() []Property {
	numeric := func(name, description string) Property {
		return Property{Name: name, Types: []string{typeNumber}, Description: description, Nullable: true}
	}
	return []Property{
		{
			Name: "food_name", Types: []string{typeString},
			Description: "the custom food's name", Required: true,
		},
		{
			Name: argCalories, Types: []string{typeNumber},
			Description: "calories per serving", Required: true,
		},
		{
			Name: "serving_unit", Types: []string{typeString},
			Description: "the serving-size unit, for example G, ML or OZ",
			Default:     defaultServingUnit,
		},
		{
			Name: "number_of_units", Types: []string{typeNumber},
			Description: "the serving size in serving_unit", Default: defaultNumberOfUnits,
		},
		{
			Name: "brand_name", Types: []string{typeString},
			Description: "the brand or vendor name", Nullable: true,
		},
		numeric("carbs", "carbohydrates in grams per serving"),
		numeric("protein", "protein in grams per serving"),
		numeric("fat", "total fat in grams per serving"),
		numeric("fiber", "fiber in grams per serving"),
		numeric("sugar", "sugar in grams per serving"),
		numeric("saturated_fat", "saturated fat in grams per serving"),
		numeric("sodium", "sodium in milligrams per serving"),
		numeric("cholesterol", "cholesterol in milligrams per serving"),
		numeric("potassium", "potassium in milligrams per serving"),
		numeric("trans_fat", "trans fat in grams per serving"),
		numeric("calcium", "calcium in milligrams per serving, absolute amount, not %DV"),
		numeric("iron", "iron in milligrams per serving, absolute amount, not %DV"),
		numeric("vitamin_d", "vitamin D in micrograms per serving, absolute amount, not %DV"),
	}
}

// CustomFoodWriteResult is what create_custom_food and update_custom_food report.
//
// Source: "On success the response includes foodId and servingId needed for
// log_custom_food. If the API returns no data (204), use get_custom_foods(...)"
// (nutrition.py:291-293). A 204 leaves every field here absent, matching that
// documented gap rather than inventing values Garmin did not send. The domain
// client's CreateCustomFood/UpdateCustomFood return only the decoded FoodItem, with
// no retained status of their own, so none is reported here either.
type CustomFoodWriteResult struct {
	FoodID    *string `json:"food_id,omitempty" jsonschema:"the food identifier, for log_custom_food"`
	ServingID *string `json:"serving_id,omitempty" jsonschema:"the serving identifier, for log_custom_food"`
	FoodName  *string `json:"food_name,omitempty" jsonschema:"the food's name, echoed back"`
	BrandName *string `json:"brand_name,omitempty" jsonschema:"the brand or vendor name, when present"`
}

// LogValue reports whether the identifiers arrived, never the name, brand or a
// nutrition figure.
func (r CustomFoodWriteResult) LogValue() slog.Value {
	return shape("customFoodWrite",
		slog.String("foodId", presence(r.FoodID != nil)),
		slog.String("servingId", presence(r.ServingID != nil)),
	)
}

// newCustomFoodWriteResult maps the domain model onto the result.
func newCustomFoodWriteResult(item api.FoodItem) CustomFoodWriteResult {
	out := CustomFoodWriteResult{}
	if meta := item.Meta; meta != nil {
		out.FoodID = optionalText(meta.FoodID)
		out.FoodName = meta.FoodName
		out.BrandName = meta.BrandName
	}
	for _, content := range item.Contents.Items() {
		if id := optionalText(content.ServingID); id != nil {
			out.ServingID = id
			break
		}
	}
	return out
}

// createCustomFoodInput is the strict argument set.
type createCustomFoodInput struct {
	customFoodFactsFields
}

func createCustomFoodContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateCustomFood,
			Title: "Create a custom food",
			Description: "create a new food item in the account's custom-food library, with " +
				"nutrition facts per serving. Every nutrient amount is an absolute value, not " +
				"a percent daily value — convert a nutrition label's %DV for calcium, iron or " +
				"vitamin D before calling. Creates a new record every time it is called: read " +
				"get_custom_foods first to avoid a duplicate",
			Tier:        policy.TierWrite,
			Category:    categoryNutrition,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(customFoodFactsProperties()...),
	}
}

// registerCreateCustomFood registers the tool.
func registerCreateCustomFood(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in createCustomFoodInput) (
		*mcp.CallToolResult, CustomFoodWriteResult, error,
	) {
		out, err := svc.createCustomFood(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, createCustomFoodContract().Registration(), handler)
}

// createCustomFood performs the write behind the tool.
func (s *service) createCustomFood(ctx context.Context, in createCustomFoodInput) (CustomFoodWriteResult, error) {
	session, err := s.session(ctx)
	if err != nil {
		return CustomFoodWriteResult{}, err
	}
	item, err := s.nutrition.CreateCustomFood(ctx, session, in.toFacts())
	if err != nil {
		return CustomFoodWriteResult{}, fail(err)
	}
	return newCustomFoodWriteResult(item), nil
}

// updateCustomFoodInput is the strict argument set.
type updateCustomFoodInput struct {
	FoodID    string `json:"food_id" jsonschema:"the custom food to update, from get_custom_foods"`
	ServingID string `json:"serving_id" jsonschema:"the serving to update, from get_custom_foods"`
	customFoodFactsFields
}

func updateCustomFoodContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUpdateCustomFood,
			Title: "Update a custom food",
			Description: "update an existing custom food's nutrition facts. Garmin's write " +
				"replaces the whole record, so this tool first reads the current record — by " +
				"searching food_name for a match on food_id — and overlays only the fields " +
				"this call supplies onto it before writing the merged document back; an " +
				"omitted optional nutrient keeps its existing value rather than being " +
				"cleared. When the current record cannot be found this way, an omitted " +
				"field is cleared, the same as create_custom_food. Every nutrient amount " +
				"is an absolute value, not a percent daily value",
			Tier:        policy.TierWrite,
			Category:    categoryNutrition,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(append([]Property{
			{
				Name: argFoodID, Types: []string{typeString},
				Description: "the custom food to update, from get_custom_foods", Required: true,
			},
			{
				Name: "serving_id", Types: []string{typeString},
				Description: "the serving to update, from get_custom_foods", Required: true,
			},
		}, customFoodFactsProperties()...)...),
	}
}

// registerUpdateCustomFood registers the tool.
func registerUpdateCustomFood(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in updateCustomFoodInput) (
		*mcp.CallToolResult, CustomFoodWriteResult, error,
	) {
		out, err := svc.updateCustomFood(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, updateCustomFoodContract().Registration(), handler)
}

// updateCustomFoodLookupLimit matches upstream's own existing-record lookup page
// size for update_custom_food's merge (nutrition.py: "start=0&limit=20").
const updateCustomFoodLookupLimit = 20

// updateCustomFood performs the write behind the tool.
func (s *service) updateCustomFood(ctx context.Context, in updateCustomFoodInput) (CustomFoodWriteResult, error) {
	id, err := api.ParseFoodID(in.FoodID)
	if err != nil {
		return CustomFoodWriteResult{}, invalidArgument("food_id must be a valid identifier")
	}
	serving, err := api.ParseServingID(in.ServingID)
	if err != nil {
		return CustomFoodWriteResult{}, invalidArgument("serving_id must be a valid identifier")
	}
	session, err := s.session(ctx)
	if err != nil {
		return CustomFoodWriteResult{}, err
	}
	facts, err := s.mergeCustomFoodFacts(ctx, session, id, in.customFoodFactsFields)
	if err != nil {
		return CustomFoodWriteResult{}, err
	}
	item, err := s.nutrition.UpdateCustomFood(ctx, session, id, serving, facts)
	if err != nil {
		return CustomFoodWriteResult{}, fail(err)
	}
	return newCustomFoodWriteResult(item), nil
}

// deleteCustomFoodInput is the strict argument set.
type deleteCustomFoodInput struct {
	FoodID string `json:"food_id" jsonschema:"the custom food to delete, from get_custom_foods"`
}

// FoodDeletionResult reports one custom-food removal, matching the manifest's
// staticTopLevelKeys (food_id, message, status).
type FoodDeletionResult struct {
	FoodID  string `json:"food_id" jsonschema:"the food identifier that was removed"`
	Message string `json:"message" jsonschema:"a human-readable confirmation"`
	Status  int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
}

// LogValue reports that a removal happened, never which food it named.
func (r FoodDeletionResult) LogValue() slog.Value {
	return shape("foodDeletion", slog.Int("status", r.Status))
}

func deleteCustomFoodContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDeleteCustomFood,
			Title: "Delete a custom food",
			Description: "permanently remove a custom food from the account's library. The " +
				"food must not be referenced by a logged meal. It cannot be undone and it " +
				"requires confirmation",
			Tier:        policy.TierDestructive,
			Category:    categoryNutrition,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(Property{
			Name: argFoodID, Types: []string{typeString},
			Description: "the custom food to delete, from get_custom_foods", Required: true,
		}),
	}
}

// registerDeleteCustomFood registers the tool.
func registerDeleteCustomFood(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deleteCustomFoodInput) (
		*mcp.CallToolResult, FoodDeletionResult, error,
	) {
		out, err := svc.deleteCustomFood(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, deleteCustomFoodContract().Registration(), handler)
}

// deleteCustomFood performs the removal behind the tool.
func (s *service) deleteCustomFood(ctx context.Context, in deleteCustomFoodInput) (FoodDeletionResult, error) {
	id, err := api.ParseFoodID(in.FoodID)
	if err != nil {
		return FoodDeletionResult{}, invalidArgument("food_id must be a valid identifier")
	}
	session, err := s.session(ctx)
	if err != nil {
		return FoodDeletionResult{}, err
	}
	result, err := s.nutrition.DeleteCustomFood(ctx, session, id)
	if err != nil {
		return FoodDeletionResult{}, fail(err)
	}
	return FoodDeletionResult{
		FoodID:  id.String(),
		Message: "Custom food deleted successfully.",
		Status:  result.Status,
	}, nil
}
