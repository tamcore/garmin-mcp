package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetFloors is the upstream compatibility name of the floors tool.
const ToolGetFloors = "get_floors"

// maxFloorBuckets bounds the bucket series one call returns. A day of fifteen-minute
// buckets is 96 entries, the same cadence as the intraday step chart, so this bound
// cuts nothing a real day produces.
const maxFloorBuckets = 512

// FloorsData is the account's floors-climbed chart for one day.
//
// It is health data — never log it, never cache it. A floor count is a reading.
type FloorsData struct {
	// Date is the calendar day that was requested.
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	// The envelope window, as Garmin reported it, in both encodings it sends.
	StartGMT   *string `json:"start_gmt,omitempty" jsonschema:"chart start, UTC"`
	EndGMT     *string `json:"end_gmt,omitempty" jsonschema:"chart end, UTC"`
	StartLocal *string `json:"start_local,omitempty" jsonschema:"chart start, device local time"`
	EndLocal   *string `json:"end_local,omitempty" jsonschema:"chart end, device local time"`

	// Buckets are the day's intervals, in the order Garmin sent them.
	Buckets []FloorsBucket `json:"buckets" jsonschema:"the day's floor buckets"`

	// Count is how many buckets this result carries.
	Count int `json:"count" jsonschema:"how many buckets this result carries"`

	// Truncated reports that the series was cut at this server's bound.
	Truncated bool `json:"truncated" jsonschema:"whether the series was cut at the bound"`
}

// FloorsBucket is one interval of the chart.
//
// Ascent and descent are independent measurements: a bucket may report one, both or
// neither, and a bucket with neither is a normal quarter-hour rather than a gap. Each
// is omitted when Garmin measured nothing, so no bucket claims a zero it never sent.
type FloorsBucket struct {
	StartGMT *string `json:"start_gmt,omitempty" jsonschema:"bucket start, UTC, YYYY-MM-DDTHH:MM:SS.s"`
	EndGMT   *string `json:"end_gmt,omitempty" jsonschema:"bucket end, UTC, YYYY-MM-DDTHH:MM:SS.s"`

	FloorsAscended  *float64 `json:"floors_ascended,omitempty" jsonschema:"floors climbed in the bucket"`
	FloorsDescended *float64 `json:"floors_descended,omitempty" jsonschema:"floors descended in the bucket"`
}

// LogValue reports the shape of the series, never a floor count.
func (f FloorsData) LogValue() slog.Value {
	return shape("floorsData",
		slog.Int("buckets", f.Count),
		slog.Bool("truncated", f.Truncated),
	)
}

// getFloorsInput is the strict argument set: one calendar day.
type getFloorsInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getFloorsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetFloors,
			Title: "Get floors climbed",
			Description: "read one calendar day of the account's floors-climbed chart, " +
				"one record per interval. Ascent and descent are measured separately, so " +
				"a bucket may report either, both or neither. The series is bounded by " +
				"this server and the result says whether it was cut",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetFloors registers the tool.
func registerGetFloors(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getFloorsInput) (
		*mcp.CallToolResult, FloorsData, error,
	) {
		floors, err := svc.floors(ctx, in)
		return nil, floors, err
	}
	return mcpserver.AddTool(registry, getFloorsContract().Registration(), handler)
}

// floors validates the day, reads the chart and bounds the document.
//
// This endpoint is keyed by the date alone, so it needs no display name and the
// profile is never read for it.
func (s *service) floors(ctx context.Context, in getFloorsInput) (FloorsData, error) {
	date, err := parseCalendarDate("date", in.Date)
	if err != nil {
		return FloorsData{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return FloorsData{}, err
	}

	floors, err := s.wellness.Daily().Floors(ctx, session, date)
	if err != nil {
		return FloorsData{}, fail(err)
	}
	return newFloorsData(date.String(), floors), nil
}

// newFloorsData maps the chart onto the bounded result.
func newFloorsData(date string, floors api.Floors) FloorsData {
	out := FloorsData{
		Date:       date,
		StartGMT:   optionalText(floors.StartTimestampGMT),
		EndGMT:     optionalText(floors.EndTimestampGMT),
		StartLocal: optionalText(floors.StartTimestampLocal),
		EndLocal:   optionalText(floors.EndTimestampLocal),
	}

	decoded := floors.Buckets()
	out.Truncated = len(decoded) > maxFloorBuckets
	if out.Truncated {
		decoded = decoded[:maxFloorBuckets]
	}
	out.Buckets = make([]FloorsBucket, 0, len(decoded))
	for _, bucket := range decoded {
		out.Buckets = append(out.Buckets, FloorsBucket{
			StartGMT:        optionalText(bucket.StartTimeGMT),
			EndGMT:          optionalText(bucket.EndTimeGMT),
			FloorsAscended:  optionalFloat(bucket.FloorsAscended),
			FloorsDescended: optionalFloat(bucket.FloorsDescended),
		})
	}
	out.Count = len(out.Buckets)
	return out
}
