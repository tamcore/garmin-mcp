package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetStepsData is the upstream compatibility name of the intraday step tool.
const ToolGetStepsData = "get_steps_data"

// maxStepIntervals bounds the intraday step series one call returns.
//
// A day of fifteen-minute intervals is 96 entries and a day of five-minute intervals
// is 288, so this bound cuts nothing a real day produces; it exists so a drifted or
// hostile response cannot become an unbounded result.
const maxStepIntervals = 512

// StepsData is the intraday step series for one day.
//
// It is health data — never log it, never cache it. A step count for a fifteen-minute
// bucket is a reading, and so is the activity level that labels the bucket.
type StepsData struct {
	// Date is the calendar day that was requested.
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	// Intervals are the day's buckets, oldest first.
	Intervals []StepInterval `json:"intervals" jsonschema:"the day's step buckets, oldest first"`

	// Count is how many intervals this result carries.
	Count int `json:"count" jsonschema:"how many intervals this result carries"`

	// Truncated reports that the series was cut at this server's bound.
	Truncated bool `json:"truncated" jsonschema:"whether the series was cut at this server's bound"`
}

// StepInterval is one bucket of the day, fifteen minutes wide on the observed shape.
//
// Every field is optional and omitted when Garmin sent nothing, so an account that
// records less differs from one that recorded a zero: pushes in particular arrives
// present and zero on a walking account, and absent on a device that has no such
// concept. PrimaryActivityLevel is an open enum, so it is carried as a free string
// and an unrecognized value is passed on rather than refused.
type StepInterval struct {
	// StartGMT and EndGMT are Garmin's own timestamps for the bucket, in UTC with
	// no zone suffix. There is no local pair on this endpoint.
	StartGMT *string `json:"start_gmt,omitempty" jsonschema:"bucket start, UTC, YYYY-MM-DDTHH:MM:SS.s"`
	EndGMT   *string `json:"end_gmt,omitempty" jsonschema:"bucket end, UTC, YYYY-MM-DDTHH:MM:SS.s"`

	Steps  *int `json:"steps,omitempty" jsonschema:"steps taken in the bucket"`
	Pushes *int `json:"pushes,omitempty" jsonschema:"wheelchair pushes in the bucket"`

	// PrimaryActivityLevel is an open set: an unrecognized label is passed on.
	PrimaryActivityLevel *string `json:"primary_activity_level,omitempty" jsonschema:"Garmin's activity label"`
	// ActivityLevelConstant reports that the level held for the whole bucket.
	ActivityLevelConstant *bool `json:"activity_level_constant,omitempty" jsonschema:"level held all bucket"`
}

// LogValue reports the shape of the series, never a step count.
func (d StepsData) LogValue() slog.Value {
	return shape("stepsData",
		slog.Int("intervals", d.Count),
		slog.Bool("truncated", d.Truncated),
	)
}

// getStepsDataInput is the strict argument set: one calendar day.
type getStepsDataInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getStepsDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetStepsData,
			Title: "Get intraday steps",
			Description: "read one calendar day of the account's intraday step series, one " +
				"record per interval, oldest first. This is a large result; for the day's " +
				"total use get_stats instead. The series is bounded by this server and the " +
				"result says whether it was cut",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetStepsData registers the tool.
func registerGetStepsData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getStepsDataInput) (
		*mcp.CallToolResult, StepsData, error,
	) {
		steps, err := svc.stepsData(ctx, in)
		return nil, steps, err
	}
	return mcpserver.AddTool(registry, getStepsDataContract().Registration(), handler)
}

// stepsData validates the day, reads the series and bounds it.
func (s *service) stepsData(ctx context.Context, in getStepsDataInput) (StepsData, error) {
	read, err := s.resolveDailyRead(ctx, in.Date)
	if err != nil {
		return StepsData{}, err
	}
	entries, err := s.wellness.Daily().StepsIntervals(ctx, read.session, read.name, read.date)
	if err != nil {
		return StepsData{}, fail(err)
	}

	truncated := len(entries) > maxStepIntervals
	if truncated {
		entries = entries[:maxStepIntervals]
	}
	intervals := make([]StepInterval, 0, len(entries))
	for _, entry := range entries {
		intervals = append(intervals, newStepInterval(entry))
	}
	return StepsData{
		Date:      read.date.String(),
		Intervals: intervals,
		Count:     len(intervals),
		Truncated: truncated,
	}, nil
}

// newStepInterval maps one decoded bucket onto the curated result.
func newStepInterval(entry api.StepInterval) StepInterval {
	return StepInterval{
		StartGMT:              optionalText(entry.StartGMT),
		EndGMT:                optionalText(entry.EndGMT),
		Steps:                 optionalInt(entry.Steps),
		Pushes:                optionalInt(entry.Pushes),
		PrimaryActivityLevel:  optionalText(entry.PrimaryActivityLevel),
		ActivityLevelConstant: entry.ActivityLevelConstant,
	}
}
