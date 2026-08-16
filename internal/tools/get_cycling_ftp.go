package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetCyclingFTP is the upstream compatibility name of the cycling FTP read.
const ToolGetCyclingFTP = "get_cycling_ftp"

// CyclingFTP is the account's latest cycling functional threshold power. It is health
// data — never log it, never cache it.
type CyclingFTP struct {
	Sport               *string  `json:"sport,omitempty" jsonschema:"the Garmin sport key the record belongs to"`
	FTPWatts            *float64 `json:"functional_threshold_power_watts,omitempty" jsonschema:"the power in watts"`
	CalendarDate        *string  `json:"calendar_date,omitempty" jsonschema:"the day the record is dated, YYYY-MM-DD"`
	IsStale             *bool    `json:"is_stale,omitempty" jsonschema:"whether the estimate is stale"`
	BiometricSourceType *string  `json:"biometric_source_type,omitempty" jsonschema:"where Garmin got the value from"`

	Reported     bool `json:"reported" jsonschema:"whether Garmin held a cycling threshold power at all"`
	RecordsFound int  `json:"records_found" jsonschema:"how many records answered; the newest is reported"`
}

// LogValue reports the shape of the answer, never a reading.
func (c CyclingFTP) LogValue() slog.Value {
	return shape("cyclingFTP",
		slog.Bool("reported", c.Reported),
		slog.Int("records", c.RecordsFound),
	)
}

func getCyclingFTPContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetCyclingFTP,
			Title: "Get cycling FTP",
			Description: "read the account's latest cycling functional threshold power as " +
				"Garmin holds it: the value in watts, the day it is dated, where it came " +
				"from, and whether Garmin considers it out of date. An account Garmin " +
				"holds no cycling threshold for answers with reported false. This is " +
				"Garmin's own record; get_power_duration_curve estimates one from " +
				"recorded efforts instead",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetCyclingFTP registers the tool.
func registerGetCyclingFTP(registry *mcpserver.Registry, svc *service) error {
	scores, err := trainingScoresClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, CyclingFTP, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, CyclingFTP{}, err
		}
		records, err := scores.CyclingFTP(ctx, session)
		if err != nil {
			return nil, CyclingFTP{}, fail(err)
		}
		return nil, newCyclingFTP(records), nil
	}
	return mcpserver.AddTool(registry, getCyclingFTPContract().Registration(), handler)
}

// newCyclingFTP maps the latest record onto the result.
//
// Garmin answers with one object or with a list of them, and the list is not ordered
// by contract, so the most recently dated record wins rather than the first one.
func newCyclingFTP(records []api.ThresholdPower) CyclingFTP {
	out := CyclingFTP{RecordsFound: len(records)}

	latest, ok := latestThresholdPower(records)
	if !ok {
		return out
	}
	out.Reported = true
	out.Sport = optionalText(latest.Sport)
	out.FTPWatts = optionalFloat(latest.FunctionalThresholdPower)
	out.CalendarDate = latest.CalendarDate
	out.IsStale = latest.IsStale
	out.BiometricSourceType = optionalText(latest.BiometricSourceType)
	return out
}

// latestThresholdPower picks the most recently dated record, keeping the first of two
// equally dated ones so the answer does not depend on the order Garmin listed them in.
func latestThresholdPower(records []api.ThresholdPower) (api.ThresholdPower, bool) {
	chosen, found := api.ThresholdPower{}, false
	for _, record := range records {
		if !found || laterCalendarDate(record.CalendarDate, chosen.CalendarDate) {
			chosen, found = record, true
		}
	}
	return chosen, found
}

// laterCalendarDate reports whether candidate names a later day than current. A day
// Garmin did not name never wins.
func laterCalendarDate(candidate, current *string) bool {
	if candidate == nil {
		return false
	}
	return current == nil || *candidate > *current
}
