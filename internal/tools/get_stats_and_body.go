package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetStatsAndBody is the upstream compatibility name of the composed daily
// stats and body-composition tool.
const ToolGetStatsAndBody = "get_stats_and_body"

// StatsAndBody is one day of the account's totals together with the same day's
// body-composition average.
//
// Upstream merges the two into one flat object, spreading the body average's Garmin
// wire keys next to get_stats's curated keys. This result keeps them apart on
// purpose: the curated half has sourced names and the body half does not, and a flat
// object would make the two indistinguishable to a caller.
//
// It is health data — never log it, never cache it.
type StatsAndBody struct {
	// Date is the calendar day that was requested.
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	// Stats is the day's curated totals, identical to the get_stats result.
	Stats DailyActivityStats `json:"stats" jsonschema:"the day's curated totals"`

	// BodyCompositionAverage is the day's averaged body composition, as
	// get_body_composition returns it. It is absent
	// only when Garmin sent no such object: an account that records no weight still
	// gets the object, with every metric omitted.
	BodyCompositionAverage *BodyCompositionAverage `json:"body_composition_average,omitempty"`

	// DroppedFields is how many identifying keys were removed from the body half,
	// whose metrics accept whatever JSON Garmin sends. It is a count and never a list
	// of names: see sanitizeUntyped.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from the body half"`
}

// LogValue reports the shape of the day, never a reading from either half.
func (b StatsAndBody) LogValue() slog.Value {
	return shape("statsAndBody",
		slog.Any("stats", b.Stats),
		slog.String("bodyAverage", presence(b.BodyCompositionAverage != nil)),
	)
}

// getStatsAndBodyInput is the strict argument set: one calendar day.
type getStatsAndBodyInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getStatsAndBodyContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetStatsAndBody,
			Title: "Get daily stats and body composition",
			Description: "read one calendar day of the account's curated totals together " +
				"with the same day's averaged body composition. The two halves are returned " +
				"separately: stats carries the curated metrics and body_composition_average " +
				"carries Garmin's own averaged document",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetStatsAndBody registers the tool.
func registerGetStatsAndBody(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getStatsAndBodyInput) (
		*mcp.CallToolResult, StatsAndBody, error,
	) {
		combined, err := svc.statsAndBody(ctx, in)
		return nil, combined, err
	}
	return mcpserver.AddTool(registry, getStatsAndBodyContract().Registration(), handler)
}

// statsAndBody reads both halves for one day.
func (s *service) statsAndBody(ctx context.Context, in getStatsAndBodyInput) (StatsAndBody, error) {
	read, err := s.resolveDailyRead(ctx, in.Date)
	if err != nil {
		return StatsAndBody{}, err
	}
	stats, body, err := s.wellness.Daily().StatsAndBody(ctx, read.session, read.name, read.date)
	if err != nil {
		return StatsAndBody{}, fail(err)
	}
	average, tally := newBodyCompositionAverage(body.TotalAverage)
	return StatsAndBody{
		Date:                   read.date.String(),
		Stats:                  newDailyActivityStats(read.date.String(), stats),
		BodyCompositionAverage: average,
		DroppedFields:          tally.Dropped,
	}, nil
}
