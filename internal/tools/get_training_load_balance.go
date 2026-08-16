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

// ToolGetTrainingLoadBalance is the upstream compatibility name.
const ToolGetTrainingLoadBalance = "get_training_load_balance"

// Band statuses, computed from the load against Garmin's own target range.
const (
	bandBelow  = "below"
	bandWithin = "within"
	bandAbove  = "above"
)

// LoadBand is one intensity band of the trailing-month load focus.
//
// The status is derived here, exactly as upstream derives it: below when the load is
// under the target minimum, above when it is over the maximum, within otherwise. It is
// reported only when the load and both edges arrived, because a comparison against a
// missing edge would be a guess.
type LoadBand struct {
	Load      *float64 `json:"load,omitempty" jsonschema:"the trailing-month load in this band"`
	TargetMin *float64 `json:"target_min,omitempty" jsonschema:"the lower edge of Garmin's target range"`
	TargetMax *float64 `json:"target_max,omitempty" jsonschema:"the upper edge of Garmin's target range"`
	Status    string   `json:"status,omitempty" jsonschema:"below, within or above the target range"`
}

// LogValue reports that a band was present, never its load.
func (b LoadBand) LogValue() slog.Value {
	return shape("loadBand", slog.String("load", presence(b.Load != nil)))
}

// TrainingLoadBalance is Garmin's load focus for one day.
//
// It is training data derived from health readings: never log it.
type TrainingLoadBalance struct {
	Date    string `json:"date" jsonschema:"the calendar day the balance is dated, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held a load balance for the day"`

	Feedback string `json:"feedback,omitempty" jsonschema:"Garmin's feedback phrase, for example AEROBIC_HIGH_SHORTAGE"`

	AerobicLow  *LoadBand `json:"aerobic_low,omitempty" jsonschema:"the low-aerobic band"`
	AerobicHigh *LoadBand `json:"aerobic_high,omitempty" jsonschema:"the high-aerobic band"`
	Anaerobic   *LoadBand `json:"anaerobic,omitempty" jsonschema:"the anaerobic band"`
}

// LogValue reports the shape of the balance, never a load.
func (b TrainingLoadBalance) LogValue() slog.Value {
	return shape("trainingLoadBalance",
		slog.Bool("hasData", b.HasData),
		slog.String("feedback", presence(b.Feedback != "")),
	)
}

// getTrainingLoadBalanceInput is the strict argument set: one calendar day.
type getTrainingLoadBalanceInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getTrainingLoadBalanceContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetTrainingLoadBalance,
			Title: "Get the training load balance",
			Description: "read Garmin's load focus for one day: how the trailing month's " +
				"training load is spread across the low-aerobic, high-aerobic and " +
				"anaerobic bands, each against Garmin's own target range, plus the " +
				"feedback phrase",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetTrainingLoadBalance registers the tool.
func registerGetTrainingLoadBalance(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getTrainingLoadBalanceInput) (
		*mcp.CallToolResult, TrainingLoadBalance, error,
	) {
		out, err := svc.readTrainingLoadBalance(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getTrainingLoadBalanceContract().Registration(), handler)
}

// readTrainingLoadBalance performs the read behind the tool.
func (s *service) readTrainingLoadBalance(
	ctx context.Context, date string,
) (TrainingLoadBalance, error) {
	day, err := parseCalendarDate("date", date)
	if err != nil {
		return TrainingLoadBalance{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return TrainingLoadBalance{}, err
	}

	document, err := s.trends().TrainingLoadBalance(ctx, session, day)
	if err != nil {
		return TrainingLoadBalance{}, fail(err)
	}
	return newTrainingLoadBalance(day.String(), document), nil
}

// newTrainingLoadBalance maps the domain model onto the result.
func newTrainingLoadBalance(date string, document api.TrainingStatus) TrainingLoadBalance {
	out := TrainingLoadBalance{Date: date}
	balance, ok := document.PrimaryLoadBalance()
	if !ok {
		return out
	}

	if balance.CalendarDate != nil && *balance.CalendarDate != "" {
		out.Date = *balance.CalendarDate
	}
	out.Feedback = textOrEmpty(balance.FeedbackPhrase)
	out.AerobicLow = newLoadBand(balance.MonthlyLoadAerobicLow,
		balance.MonthlyLoadAerobicLowTargetMin, balance.MonthlyLoadAerobicLowTargetMax)
	out.AerobicHigh = newLoadBand(balance.MonthlyLoadAerobicHigh,
		balance.MonthlyLoadAerobicHighTargetMin, balance.MonthlyLoadAerobicHighTargetMax)
	out.Anaerobic = newLoadBand(balance.MonthlyLoadAnaerobic,
		balance.MonthlyLoadAnaerobicTargetMin, balance.MonthlyLoadAnaerobicTargetMax)
	out.HasData = out.AerobicLow != nil || out.AerobicHigh != nil || out.Anaerobic != nil ||
		out.Feedback != ""
	return out
}

// newLoadBand renders one band, or nothing when Garmin carried none of its three
// figures.
func newLoadBand(load, minimum, maximum client.Number) *LoadBand {
	band := LoadBand{
		Load:      optionalFloat(load),
		TargetMin: optionalFloat(minimum),
		TargetMax: optionalFloat(maximum),
	}
	if band.Load == nil && band.TargetMin == nil && band.TargetMax == nil {
		return nil
	}
	if band.Load != nil && band.TargetMin != nil && band.TargetMax != nil {
		band.Status = bandStatus(*band.Load, *band.TargetMin, *band.TargetMax)
	}
	return &band
}

// bandStatus places a load against its target range.
func bandStatus(load, minimum, maximum float64) string {
	switch {
	case load < minimum:
		return bandBelow
	case load > maximum:
		return bandAbove
	default:
		return bandWithin
	}
}
