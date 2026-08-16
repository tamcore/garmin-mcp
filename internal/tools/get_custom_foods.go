package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetCustomFoods is the upstream compatibility name.
const ToolGetCustomFoods = "get_custom_foods"

// getCustomFoodsInput is the strict argument set. search is optional and defaults to
// "", which lists every custom food, matching upstream's default (nutrition.py:221).
type getCustomFoodsInput struct {
	Search *string `json:"search,omitempty" jsonschema:"a name filter, default lists everything"`
	Start  *int    `json:"start,omitempty" jsonschema:"zero-based record offset, default 0"`
	Limit  *int    `json:"limit,omitempty" jsonschema:"results per page, default 20"`
}

// CustomFoodPageResult is one page of the account's own custom-food library.
//
// It reuses FoodCatalogItem: get_custom_foods, search_foods, create_custom_food and
// update_custom_food all read and write the same foodMetaData/nutritionContents
// shape (internal/garmin/api/nutritionread.go's CustomFoodPage doc comment), so the
// curation search_foods evidences (nutrition.py:182-208) applies here too, even
// though upstream's own get_custom_foods passes its response through uncurated
// (nutrition.py:239-249).
type CustomFoodPageResult struct {
	Results []FoodCatalogItem `json:"results" jsonschema:"the account's custom foods, bounded"`
	Count   int               `json:"count" jsonschema:"how many results this page carries"`
	Start   int               `json:"start" jsonschema:"the record offset this page started at"`
	Limit   int               `json:"limit" jsonschema:"the effective page size, after this server's bound"`

	// Truncated reports that Garmin returned more items than Limit, so Results was
	// cut down to it rather than returned in full.
	Truncated bool `json:"truncated" jsonschema:"whether results was cut down to the requested limit"`
}

// LogValue reports the result count, never a food's name or brand.
func (r CustomFoodPageResult) LogValue() slog.Value {
	return shape("customFoodPage", slog.Int("results", r.Count), slog.Bool("truncated", r.Truncated))
}

func getCustomFoodsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetCustomFoods,
			Title: "Get custom foods",
			Description: "search or list the account's own custom-food library. Use the " +
				"search filter to find an existing food by name before creating a duplicate " +
				"— the response carries the foodId and servingId create_custom_food and " +
				"log_custom_food need. For the general branded catalog, use search_foods",
			Tier:        policy.TierReadOnly,
			Category:    categoryNutrition,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(append([]Property{
			{
				Name:        "search",
				Types:       []string{typeString},
				Description: "a name filter; empty lists every custom food",
				MaxLength:   new(api.MaxSearchQueryLen),
				Default:     "",
			},
		}, foodPageProperties()...)...),
	}
}

// registerGetCustomFoods registers the tool.
func registerGetCustomFoods(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getCustomFoodsInput) (
		*mcp.CallToolResult, CustomFoodPageResult, error,
	) {
		out, err := svc.getCustomFoods(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getCustomFoodsContract().Registration(), handler)
}

// getCustomFoods performs the read behind the tool.
func (s *service) getCustomFoods(ctx context.Context, in getCustomFoodsInput) (CustomFoodPageResult, error) {
	page, err := resolveFoodPage(in.Start, in.Limit, s.limits)
	if err != nil {
		return CustomFoodPageResult{}, err
	}
	search := ""
	if in.Search != nil {
		search = *in.Search
	}

	session, err := s.session(ctx)
	if err != nil {
		return CustomFoodPageResult{}, err
	}
	result, err := s.nutrition.CustomFoods(ctx, session, search, page)
	if err != nil {
		return CustomFoodPageResult{}, fail(err)
	}

	items := result.CustomFoods.Items()
	out := CustomFoodPageResult{
		Start: page.Start(),
		Limit: page.Limit(),
	}
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
	return out, nil
}
