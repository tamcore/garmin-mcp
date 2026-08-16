package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolSearchFoods is the upstream compatibility name.
const ToolSearchFoods = "search_foods"

// Pagination bounds shared by search_foods and get_custom_foods: both take the same
// start/limit pair with the same manifest defaults (nutrition.py:145-147, :222-224).
const (
	defaultFoodPageStart      = 0
	defaultFoodPageLimit      = 20
	DefaultMaxFoodResultsPage = 100
	DefaultMaxServingsPerFood = 50
)

// FoodServing is one serving's worth of nutrition facts.
//
// Source: search_foods' curated serving fields (nutrition.py:182-195) — servingId,
// servingUnit, numberOfUnits, calories, carbs, protein, fat, fiber, sodium. Every
// other nutrient create_custom_food can carry is not part of this curated read.
type FoodServing struct {
	ServingID     *string  `json:"serving_id,omitempty" jsonschema:"the serving identifier, for log_custom_food"`
	ServingUnit   *string  `json:"serving_unit,omitempty" jsonschema:"the serving-size unit, for example G or ML"`
	NumberOfUnits *float64 `json:"number_of_units,omitempty" jsonschema:"the serving size in serving_unit"`
	Calories      *float64 `json:"calories,omitempty" jsonschema:"calories per serving"`
	CarbsG        *float64 `json:"carbs_g,omitempty" jsonschema:"carbohydrates in grams per serving"`
	ProteinG      *float64 `json:"protein_g,omitempty" jsonschema:"protein in grams per serving"`
	FatG          *float64 `json:"fat_g,omitempty" jsonschema:"fat in grams per serving"`
	FiberG        *float64 `json:"fiber_g,omitempty" jsonschema:"fiber in grams per serving"`
	SodiumMg      *float64 `json:"sodium_mg,omitempty" jsonschema:"sodium in milligrams per serving"`
}

// LogValue reports which facts arrived, never a figure.
func (s FoodServing) LogValue() slog.Value {
	return shape("foodServing",
		slog.String("servingId", presence(s.ServingID != nil)),
		slog.String("calories", presence(s.Calories != nil)),
	)
}

// newFoodServing maps one nutrition-content entry onto the curated serving.
func newFoodServing(content api.NutritionContent) FoodServing {
	return FoodServing{
		ServingID:     optionalText(content.ServingID),
		ServingUnit:   optionalText(content.ServingUnit),
		NumberOfUnits: optionalFloat(content.NumberOfUnits),
		Calories:      optionalFloat(content.Calories),
		CarbsG:        optionalFloat(content.Carbs),
		ProteinG:      optionalFloat(content.Protein),
		FatG:          optionalFloat(content.Fat),
		FiberG:        optionalFloat(content.Fiber),
		SodiumMg:      optionalFloat(content.Sodium),
	}
}

// FoodCatalogItem is one food entry from the general catalog or the user's own
// custom-food library.
//
// Source: search_foods' curated identity fields (nutrition.py:196-208): food_id,
// name, food_type, source, region, language and brand.
type FoodCatalogItem struct {
	FoodID   *string       `json:"food_id,omitempty" jsonschema:"the food identifier, for log_custom_food"`
	Name     *string       `json:"name,omitempty" jsonschema:"the food's display name"`
	FoodType *string       `json:"food_type,omitempty" jsonschema:"Garmin's food-type classification"`
	Source   *string       `json:"source,omitempty" jsonschema:"GARMIN or FATSECRET, for log_custom_food"`
	Region   *string       `json:"region,omitempty" jsonschema:"the region code the entry was catalogued under"`
	Language *string       `json:"language,omitempty" jsonschema:"the language code the entry was catalogued under"`
	Brand    *string       `json:"brand,omitempty" jsonschema:"the brand or vendor name, when the food carries one"`
	Servings []FoodServing `json:"servings" jsonschema:"the servings available for this food, bounded"`

	// ServingsTruncated reports that Garmin sent more servings for this food than
	// DefaultMaxServingsPerFood, so Servings is not the whole list.
	ServingsTruncated bool `json:"servings_truncated" jsonschema:"whether servings was cut at this server's bound"`
}

// LogValue reports the serving count, never the name, brand or a figure.
func (i FoodCatalogItem) LogValue() slog.Value {
	return shape("foodCatalogItem",
		slog.String("foodId", presence(i.FoodID != nil)),
		slog.Int("servings", len(i.Servings)),
		slog.Bool("servingsTruncated", i.ServingsTruncated),
	)
}

// newFoodCatalogItem maps one catalog food onto the curated item, bounding its
// serving list and reporting when that bound cut it.
func newFoodCatalogItem(item api.FoodItem) FoodCatalogItem {
	out := FoodCatalogItem{}
	if meta := item.Meta; meta != nil {
		out.FoodID = optionalText(meta.FoodID)
		out.Name = meta.FoodName
		out.FoodType = optionalText(meta.FoodType)
		out.Source = optionalText(meta.Source)
		out.Region = optionalText(meta.RegionCode)
		out.Language = optionalText(meta.LanguageCode)
		out.Brand = meta.BrandName
	}

	contents := item.Contents.Items()
	if len(contents) > DefaultMaxServingsPerFood {
		contents = contents[:DefaultMaxServingsPerFood]
		out.ServingsTruncated = true
	}
	out.Servings = make([]FoodServing, 0, len(contents))
	for _, content := range contents {
		out.Servings = append(out.Servings, newFoodServing(content))
	}
	return out
}

// resolveFoodPage applies the shared manifest defaults for a food-catalog page and
// refuses an out-of-range window before any Garmin call.
func resolveFoodPage(start, limit *int, limits client.Limits) (client.Page, error) {
	startValue, limitValue := defaultFoodPageStart, defaultFoodPageLimit
	if start != nil {
		startValue = *start
	}
	if limit != nil {
		limitValue = *limit
	}

	switch {
	case startValue < 0:
		return client.Page{}, invalidArgument("start must not be negative")
	case startValue > client.MaxPageStartCap:
		return client.Page{}, invalidArgument("start is too large")
	case limitValue < 1:
		return client.Page{}, invalidArgument("limit must be at least 1")
	case limitValue > DefaultMaxFoodResultsPage:
		return client.Page{}, invalidArgument("limit must not exceed the server's bound")
	}
	if limitValue > limits.MaxPageSize {
		limitValue = limits.MaxPageSize
	}

	page, err := client.NewPage(startValue, limitValue)
	if err != nil {
		return client.Page{}, fail(err)
	}
	return page, nil
}

// foodPageProperties declares the start/limit pair search_foods and get_custom_foods
// share.
func foodPageProperties() []Property {
	return []Property{
		{
			Name:        argStart,
			Types:       []string{typeInteger},
			Description: "zero-based record offset",
			Minimum:     bound(0),
			Default:     defaultFoodPageStart,
		},
		{
			Name:        argLimit,
			Types:       []string{typeInteger},
			Description: "results per page, 1 to 100",
			Minimum:     bound(1),
			Maximum:     bound(DefaultMaxFoodResultsPage),
			Default:     defaultFoodPageLimit,
		},
	}
}

// searchFoodsInput is the strict argument set.
type searchFoodsInput struct {
	Query string `json:"query" jsonschema:"the food name or brand to search for"`
	Start *int   `json:"start,omitempty" jsonschema:"zero-based record offset, default 0"`
	Limit *int   `json:"limit,omitempty" jsonschema:"results per page, default 20"`
}

// FoodSearchResultOut is one page of the general food-catalog search.
//
// Source: search_foods' returned envelope (nutrition.py:205-209): count, has_more,
// results.
type FoodSearchResultOut struct {
	Count   int               `json:"count" jsonschema:"how many results this page carries"`
	HasMore bool              `json:"has_more" jsonschema:"whether Garmin holds more results beyond this page"`
	Results []FoodCatalogItem `json:"results" jsonschema:"the matching foods, bounded"`

	// Truncated reports that Garmin returned more items than the requested limit,
	// so Results was cut down to it rather than returned in full.
	Truncated bool `json:"truncated" jsonschema:"whether results was cut down to the requested limit"`
}

// LogValue reports the result count, never a food's name or brand.
func (r FoodSearchResultOut) LogValue() slog.Value {
	return shape("foodSearchResult", slog.Int("results", r.Count), slog.Bool("hasMore", r.HasMore),
		slog.Bool("truncated", r.Truncated))
}

func searchFoodsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSearchFoods,
			Title: "Search the food catalog",
			Description: "search Garmin's general food catalog — FatSecret-sourced branded " +
				"and generic foods, plus Garmin custom foods — by name or brand. Returns each " +
				"food's identifier, source and available servings with macros, for use with " +
				"log_custom_food. For the account's own custom foods only, use get_custom_foods",
			Tier:        policy.TierReadOnly,
			Category:    categoryNutrition,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(append([]Property{
			{
				Name:        "query",
				Types:       []string{typeString},
				Description: "the food name or brand to search for",
				MaxLength:   new(api.MaxSearchQueryLen),
				Required:    true,
			},
		}, foodPageProperties()...)...),
	}
}

// registerSearchFoods registers the tool.
func registerSearchFoods(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in searchFoodsInput) (
		*mcp.CallToolResult, FoodSearchResultOut, error,
	) {
		out, err := svc.searchFoods(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, searchFoodsContract().Registration(), handler)
}

// searchFoods performs the read behind the tool.
func (s *service) searchFoods(ctx context.Context, in searchFoodsInput) (FoodSearchResultOut, error) {
	page, err := resolveFoodPage(in.Start, in.Limit, s.limits)
	if err != nil {
		return FoodSearchResultOut{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return FoodSearchResultOut{}, err
	}
	result, err := s.nutrition.SearchFoods(ctx, session, in.Query, page)
	if err != nil {
		return FoodSearchResultOut{}, fail(err)
	}

	items := result.Results.Items()
	out := FoodSearchResultOut{}
	if len(items) > page.Limit() {
		// Garmin may ignore the requested limit and send more than was asked for;
		// capping here keeps the result's cardinality what the caller actually
		// requested regardless of what Garmin chose to send.
		items = items[:page.Limit()]
		out.Truncated = true
	}
	out.Results = make([]FoodCatalogItem, 0, len(items))
	for _, item := range items {
		out.Results = append(out.Results, newFoodCatalogItem(item))
	}
	out.Count = len(out.Results)
	if result.MoreDataAvailable != nil {
		out.HasMore = *result.MoreDataAvailable
	}
	return out, nil
}
