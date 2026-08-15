package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the activity analysis reads.
const (
	ToolGetActivitySplits         = "get_activity_splits"
	ToolGetActivitySplitSummaries = "get_activity_split_summaries"
	ToolGetActivityHRInZones      = "get_activity_hr_in_timezones"
	ToolGetActivityPowerInZones   = "get_activity_power_in_timezones"
)

func getActivitySplitsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivitySplits,
			Title: "Get activity splits",
			Description: "read one activity's untyped splits, normalized to a single array " +
				"whatever shape Garmin answers with",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

func registerGetActivitySplits(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, TypedSplitList, error,
	) {
		id, session, err := svc.resolveActivityRead(ctx, in.ActivityID)
		if err != nil {
			return nil, TypedSplitList{}, err
		}

		splits, err := svc.details.Splits(ctx, session, id)
		if err != nil {
			return nil, TypedSplitList{}, fail(err)
		}
		return nil, newTypedSplitList(id.Int64(), splits.Splits(), svc.bounds.MaxSplits), nil
	}
	return mcpserver.AddTool(registry, getActivitySplitsContract().Registration(), handler)
}

// A SplitSummary is one aggregated split group of an activity.
type SplitSummary struct {
	SplitType      *string  `json:"split_type,omitempty" jsonschema:"the split group, for example RWD_RUN"`
	Splits         *int     `json:"splits,omitempty" jsonschema:"how many splits the group aggregates"`
	DistanceMeters *float64 `json:"distance_meters,omitempty" jsonschema:"the aggregated distance in meters"`
	DurationSecs   *float64 `json:"duration_seconds,omitempty" jsonschema:"the aggregated duration in seconds"`
	TotalAscent    *float64 `json:"total_ascent,omitempty" jsonschema:"the aggregated ascent in meters"`
	MaxDistance    *float64 `json:"max_distance,omitempty" jsonschema:"the longest split in meters"`
	MaxElevation   *float64 `json:"max_elevation_gain,omitempty" jsonschema:"the largest elevation gain in meters"`
	AverageSpeed   *float64 `json:"average_speed,omitempty" jsonschema:"the average speed in meters per second"`
	MaxSpeed       *float64 `json:"max_speed,omitempty" jsonschema:"the top speed in meters per second"`
	Calories       *float64 `json:"calories,omitempty" jsonschema:"the kilocalories burned"`
}

// A SplitSummaryList is the bounded split-summary collection.
type SplitSummaryList struct {
	ActivityID int64          `json:"activity_id" jsonschema:"the activity these summaries belong to"`
	Summaries  []SplitSummary `json:"summaries" jsonschema:"the split summaries, in order"`
	Count      int            `json:"count" jsonschema:"how many summaries this result carries"`
	Truncated  bool           `json:"truncated" jsonschema:"whether the list was cut at this server's bound"`
}

// LogValue reports the summary count, never a summary.
func (l SplitSummaryList) LogValue() slog.Value {
	return shape("splitSummaryList",
		slog.Int("summaries", len(l.Summaries)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getActivitySplitSummariesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivitySplitSummaries,
			Title: "Get activity split summaries",
			Description: "read one activity's split summaries, which aggregate the splits of " +
				"each kind, for example every climbing split of a bouldering session",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

func registerGetActivitySplitSummaries(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, SplitSummaryList, error,
	) {
		id, session, err := svc.resolveActivityRead(ctx, in.ActivityID)
		if err != nil {
			return nil, SplitSummaryList{}, err
		}

		summaries, err := svc.details.SplitSummaries(ctx, session, id)
		if err != nil {
			return nil, SplitSummaryList{}, fail(err)
		}
		return nil, newSplitSummaryList(id.Int64(), summaries.Summaries.Items()), nil
	}
	return mcpserver.AddTool(registry, getActivitySplitSummariesContract().Registration(), handler)
}

// newSplitSummaryList maps the domain models onto the bounded result.
func newSplitSummaryList(activityID int64, summaries []api.SplitSummary) SplitSummaryList {
	truncated := len(summaries) > DefaultMaxSplitSummaries
	if truncated {
		summaries = summaries[:DefaultMaxSplitSummaries]
	}

	out := make([]SplitSummary, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, SplitSummary{
			SplitType:      optionalText(summary.SplitType),
			Splits:         optionalInt(summary.NoOfSplits),
			DistanceMeters: optionalFloat(summary.Distance),
			DurationSecs:   optionalFloat(summary.Duration),
			TotalAscent:    optionalFloat(summary.TotalAscent),
			MaxDistance:    optionalFloat(summary.MaxDistance),
			MaxElevation:   optionalFloat(summary.MaxElevationGain),
			AverageSpeed:   optionalFloat(summary.AverageSpeed),
			MaxSpeed:       optionalFloat(summary.MaxSpeed),
			Calories:       optionalFloat(summary.Calories),
		})
	}
	return SplitSummaryList{
		ActivityID: activityID, Summaries: out, Count: len(out), Truncated: truncated,
	}
}

// A ZoneBucket is one heart-rate or power zone with the time spent in it.
type ZoneBucket struct {
	ZoneNumber   *int     `json:"zone_number,omitempty" jsonschema:"the zone index, 1 upwards"`
	SecondsIn    *float64 `json:"seconds_in_zone,omitempty" jsonschema:"the time spent in the zone"`
	LowBoundary  *float64 `json:"low_boundary,omitempty" jsonschema:"the lower bound of the zone"`
	HighBoundary *float64 `json:"high_boundary,omitempty" jsonschema:"the upper bound of the zone"`
}

// A ZoneList is the bounded time-in-zones collection. It is health data.
type ZoneList struct {
	ActivityID int64        `json:"activity_id" jsonschema:"the activity these zones belong to"`
	Zones      []ZoneBucket `json:"zones" jsonschema:"the zones, in order"`
	Count      int          `json:"count" jsonschema:"how many zones this result carries"`
}

// LogValue reports the zone count, never a measurement.
func (l ZoneList) LogValue() slog.Value {
	return shape("zoneList", slog.Int("zones", len(l.Zones)))
}

func getActivityHRInZonesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivityHRInZones,
			Title: "Get activity heart-rate zones",
			Description: "read how long one activity spent in each heart-rate zone, with the " +
				"bpm boundaries of each zone",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

func registerGetActivityHRInZones(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, ZoneList, error,
	) {
		id, session, err := svc.resolveActivityRead(ctx, in.ActivityID)
		if err != nil {
			return nil, ZoneList{}, err
		}

		zones, err := svc.details.HRInZones(ctx, session, id)
		if err != nil {
			return nil, ZoneList{}, fail(err)
		}
		return nil, newZoneList(id.Int64(), zones), nil
	}
	return mcpserver.AddTool(registry, getActivityHRInZonesContract().Registration(), handler)
}

func getActivityPowerInZonesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivityPowerInZones,
			Title: "Get activity power zones",
			Description: "read how long one activity spent in each power zone, with the watt " +
				"boundaries of each zone. An activity recorded without a power meter answers " +
				"with no zones, which is a normal state rather than a failure",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

func registerGetActivityPowerInZones(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, ZoneList, error,
	) {
		id, session, err := svc.resolveActivityRead(ctx, in.ActivityID)
		if err != nil {
			return nil, ZoneList{}, err
		}

		zones, err := svc.details.PowerInZones(ctx, session, id)
		if err != nil {
			return nil, ZoneList{}, fail(err)
		}
		return nil, newZoneList(id.Int64(), zones), nil
	}
	return mcpserver.AddTool(registry, getActivityPowerInZonesContract().Registration(), handler)
}

// newZoneList maps the domain models onto the bounded result.
func newZoneList(activityID int64, zones []api.ZoneBucket) ZoneList {
	if len(zones) > DefaultMaxZones {
		zones = zones[:DefaultMaxZones]
	}
	out := make([]ZoneBucket, 0, len(zones))
	for _, zone := range zones {
		out = append(out, ZoneBucket{
			ZoneNumber:   optionalInt(zone.ZoneNumber),
			SecondsIn:    optionalFloat(zone.SecsInZone),
			LowBoundary:  optionalFloat(zone.ZoneLowBound),
			HighBoundary: optionalFloat(zone.ZoneHighBound),
		})
	}
	return ZoneList{ActivityID: activityID, Zones: out, Count: len(out)}
}
