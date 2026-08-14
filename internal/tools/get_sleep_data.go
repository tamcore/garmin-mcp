package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetSleepData is the upstream compatibility name of the daily sleep tool.
const ToolGetSleepData = "get_sleep_data"

// SleepData is one day of sleep, curated.
//
// Upstream returns the raw document, which is roughly 50 KB of per-minute stage,
// movement and restless-moment detail. This result carries the summary instead: the
// bound exists so one tool call cannot fill a model's context with a timeline nobody
// asked for. It is health data — never log it, never cache it.
type SleepData struct {
	// Date is the calendar day that was requested.
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	// HasData reports whether Garmin held a sleep summary for the day. A day with
	// no wearable data is a normal state, not a failure.
	HasData bool `json:"has_data" jsonschema:"whether Garmin held a sleep summary for the day"`

	SleepTimeSeconds  *float64 `json:"sleep_time_seconds,omitempty" jsonschema:"total measured sleep"`
	DeepSleepSeconds  *float64 `json:"deep_sleep_seconds,omitempty" jsonschema:"time in deep sleep"`
	LightSleepSeconds *float64 `json:"light_sleep_seconds,omitempty" jsonschema:"time in light sleep"`
	RemSleepSeconds   *float64 `json:"rem_sleep_seconds,omitempty" jsonschema:"time in REM sleep"`
	AwakeSeconds      *float64 `json:"awake_seconds,omitempty" jsonschema:"time awake in the window"`
	SleepStartGMT     *float64 `json:"sleep_start_gmt,omitempty" jsonschema:"sleep start as a UTC epoch"`
	SleepEndGMT       *float64 `json:"sleep_end_gmt,omitempty" jsonschema:"sleep end as a UTC epoch"`
	AvgRespiration    *float64 `json:"average_respiration,omitempty" jsonschema:"average breaths per minute"`
	AvgSpO2           *float64 `json:"average_spo2,omitempty" jsonschema:"average blood oxygen percentage"`
	SleepQuality      *string  `json:"sleep_quality,omitempty" jsonschema:"Garmin's sleep quality label"`
}

// LogValue reports the shape of the day, never a single health value.
func (s SleepData) LogValue() slog.Value {
	return shape("sleepData",
		slog.Bool("hasData", s.HasData),
		slog.String("sleepTime", presence(s.SleepTimeSeconds != nil)),
		slog.String("spo2", presence(s.AvgSpO2 != nil)),
	)
}

// getSleepDataInput is the strict argument set: one calendar day.
type getSleepDataInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getSleepDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetSleepData,
			Title: "Get sleep data",
			Description: "read one calendar day of the account's sleep: total, deep, light " +
				"and REM time, awake time, the sleep window, average respiration and SpO2, " +
				"and Garmin's quality label. The per-minute stage detail is not returned",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetSleepData registers the tool.
func registerGetSleepData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getSleepDataInput) (
		*mcp.CallToolResult, SleepData, error,
	) {
		read, err := svc.resolveDailyRead(ctx, in.Date)
		if err != nil {
			return nil, SleepData{}, err
		}
		sleep, err := svc.wellness.DailySleep(ctx, read.session, read.name, read.date)
		if err != nil {
			return nil, SleepData{}, fail(err)
		}
		return nil, newSleepData(read.date.String(), sleep), nil
	}
	return mcpserver.AddTool(registry, getSleepDataContract().Registration(), handler)
}

// newSleepData maps the domain model onto the curated result. The date is the day that
// was asked for, not the one the payload echoes, so a caller always knows what it got.
func newSleepData(date string, sleep api.DailySleep) SleepData {
	out := SleepData{Date: date}
	summary := sleep.Summary
	if summary == nil {
		return out
	}

	out.HasData = true
	out.SleepTimeSeconds = optionalFloat(summary.SleepTimeSeconds)
	out.DeepSleepSeconds = optionalFloat(summary.DeepSleepSeconds)
	out.LightSleepSeconds = optionalFloat(summary.LightSleepSeconds)
	out.RemSleepSeconds = optionalFloat(summary.RemSleepSeconds)
	out.AwakeSeconds = optionalFloat(summary.AwakeSleepSeconds)
	out.SleepStartGMT = optionalFloat(summary.SleepStartTimestampGMT)
	out.SleepEndGMT = optionalFloat(summary.SleepEndTimestampGMT)
	out.AvgRespiration = optionalFloat(summary.AverageRespirationValue)
	out.AvgSpO2 = optionalFloat(summary.AverageSpO2Value)
	out.SleepQuality = optionalText(summary.SleepQualityTypeName)
	return out
}
