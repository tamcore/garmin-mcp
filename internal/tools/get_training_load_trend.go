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

// ToolGetTrainingLoadTrend is the upstream compatibility name.
const ToolGetTrainingLoadTrend = "get_training_load_trend"

// TrainingLoadPoint is one day of the performance-management chart.
//
// The balance is computed here rather than read: Garmin sends the acute and the
// chronic load, and the balance is their difference. It is reported only when both
// arrived.
type TrainingLoadPoint struct {
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	ATL *float64 `json:"atl,omitempty" jsonschema:"acute load, the 7-day fatigue figure"`
	CTL *float64 `json:"ctl,omitempty" jsonschema:"chronic load, the 42-day fitness figure"`
	TSB *float64 `json:"tsb,omitempty" jsonschema:"training stress balance, chronic minus acute"`

	ACWR        *float64 `json:"acwr,omitempty" jsonschema:"the acute-to-chronic workload ratio"`
	ACWRStatus  string   `json:"acwr_status,omitempty" jsonschema:"Garmin's label for that ratio"`
	ACWRPercent *float64 `json:"acwr_percent,omitempty" jsonschema:"the ratio as a percentage"`

	OptimalChronicLoadMin *float64 `json:"optimal_chronic_load_min,omitempty" jsonschema:"the optimal load floor"`
	OptimalChronicLoadMax *float64 `json:"optimal_chronic_load_max,omitempty" jsonschema:"the optimal load ceiling"`

	TrainingStatus     string `json:"training_status,omitempty" jsonschema:"Garmin's training status phrase"`
	TrainingStatusCode string `json:"training_status_code,omitempty" jsonschema:"the status code, as sent"`
	FitnessTrend       string `json:"fitness_trend,omitempty" jsonschema:"Garmin's fitness trend indicator, as sent"`

	VO2Max *float64 `json:"vo2_max,omitempty" jsonschema:"the VO2 max the same document carried"`
}

// LogValue reports that a day carried a load, never the load.
func (p TrainingLoadPoint) LogValue() slog.Value {
	return shape("trainingLoadPoint", slog.String("ctl", presence(p.CTL != nil)))
}

// TrainingLoadTrend is the performance-management chart over a bounded window.
//
// It is training and fitness data derived from health readings: never log it.
type TrainingLoadTrend struct {
	StartDate string `json:"start_date" jsonschema:"the first calendar day of the window"`
	EndDate   string `json:"end_date" jsonschema:"the last calendar day of the window"`

	DaysWithData int `json:"days_with_data" jsonschema:"how many days yielded a reading"`

	Trend    []TrainingLoadPoint `json:"trend" jsonschema:"one entry per day that yielded a reading, oldest first"`
	Coverage TrendCoverage       `json:"coverage" jsonschema:"how complete this trend is"`
}

// LogValue reports the shape of the trend and never a load.
func (t TrainingLoadTrend) LogValue() slog.Value {
	return shape("trainingLoadTrend",
		slog.Int("points", len(t.Trend)),
		slog.Any("coverage", t.Coverage),
	)
}

func getTrainingLoadTrendContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetTrainingLoadTrend,
			Title: "Get the training load trend",
			Description: "read the performance-management chart over a date window, one " +
				"Garmin request per day: chronic load (fitness), acute load (fatigue), " +
				"their balance and the acute-to-chronic ratio. The result reports how many " +
				"days were read, how many held nothing and which days failed. The window " +
				"is at most 90 days; four to eight weeks is the useful range",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(trendWindowProperties(MaxTrainingLoadTrendDays)...),
	}
}

// registerGetTrainingLoadTrend registers the tool.
func registerGetTrainingLoadTrend(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in trendWindowInput) (
		*mcp.CallToolResult, TrainingLoadTrend, error,
	) {
		out, err := svc.readTrainingLoadTrend(ctx, in.StartDate, in.EndDate)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getTrainingLoadTrendContract().Registration(), handler)
}

// readTrainingLoadTrend walks the window one day at a time.
func (s *service) readTrainingLoadTrend(
	ctx context.Context, start, end string,
) (TrainingLoadTrend, error) {
	window, err := s.resolveTrendWindow(ctx, start, end, MaxTrainingLoadTrendDays)
	if err != nil {
		return TrainingLoadTrend{}, err
	}

	trend := make([]TrainingLoadPoint, 0, window.span.Days())
	read := func(ctx context.Context, day client.Date) (bool, error) {
		document, err := s.trends().TrainingLoadDay(ctx, window.session, day)
		if err != nil {
			return false, err
		}
		point, ok := newTrainingLoadPoint(day.String(), document)
		if !ok {
			return false, nil
		}
		trend = append(trend, point)
		return true, nil
	}

	coverage, err := walkTrendDays(ctx, window.span, read)
	if err != nil {
		return TrainingLoadTrend{}, err
	}
	return TrainingLoadTrend{
		StartDate:    window.span.Start().String(),
		EndDate:      window.span.End().String(),
		DaysWithData: coverage.DaysWithData,
		Trend:        trend,
		Coverage:     coverage,
	}, nil
}

// newTrainingLoadPoint maps one day onto a trend point, reporting whether it carried
// anything at all.
func newTrainingLoadPoint(date string, document api.TrainingStatus) (TrainingLoadPoint, bool) {
	point := TrainingLoadPoint{Date: date}
	if status, ok := document.PrimaryStatus(); ok {
		point.TrainingStatus = textOrEmpty(status.FeedbackPhrase)
		point.TrainingStatusCode = textOrEmpty(status.TrainingStatus)
		point.FitnessTrend = textOrEmpty(status.FitnessTrend)
		point.applyLoad(status.AcuteTrainingLoad)
	}
	if vo2 := document.MostRecentVO2Max; vo2 != nil && vo2.Generic != nil {
		point.VO2Max = optionalFloat(vo2.Generic.Value())
	}

	// Every field the point can carry counts. Leaving FitnessTrend out dropped a day
	// whose only reading was that trend, and then counted it as a day with no data.
	empty := point.ATL == nil && point.CTL == nil && point.ACWR == nil &&
		point.VO2Max == nil && point.TrainingStatus == "" &&
		point.TrainingStatusCode == "" && point.FitnessTrend == ""
	return point, !empty
}

// applyLoad copies the performance-management figures, computing the balance only when
// both of its inputs arrived.
func (p *TrainingLoadPoint) applyLoad(load *api.AcuteTrainingLoad) {
	if load == nil {
		return
	}
	p.ATL = optionalFloat(load.DailyTrainingLoadAcute)
	p.CTL = optionalFloat(load.DailyTrainingLoadChronic)
	p.ACWR = optionalFloat(load.DailyAcuteChronicWorkloadRatio)
	p.ACWRStatus = textOrEmpty(load.ACWRStatus)
	p.ACWRPercent = optionalFloat(load.ACWRPercent)
	p.OptimalChronicLoadMin = optionalFloat(load.MinTrainingLoadChronic)
	p.OptimalChronicLoadMax = optionalFloat(load.MaxTrainingLoadChronic)
	if p.ATL != nil && p.CTL != nil {
		balance := *p.CTL - *p.ATL
		p.TSB = &balance
	}
}
