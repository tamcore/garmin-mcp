package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetTrainingStatus is the upstream compatibility name of the training-status read.
const ToolGetTrainingStatus = "get_training_status"

// TrainingStatus is the aggregated training status of one day. It is health data —
// never log it, never cache it.
//
// Garmin keys the status and the load balance by device. This result carries one
// device's view and says how many devices reported, but never which: a device
// identifier is an account fact and no tool here returns one it was not asked for.
type TrainingStatus struct {
	Date string `json:"date" jsonschema:"the day asked for, YYYY-MM-DD"`

	ReportedDate   *string `json:"reported_date,omitempty" jsonschema:"the day the device recorded"`
	TrainingStatus *string `json:"training_status,omitempty" jsonschema:"the training status, as Garmin spells it"`
	Feedback       *string `json:"training_status_feedback,omitempty" jsonschema:"the feedback phrase for the status"`
	Sport          *string `json:"sport,omitempty" jsonschema:"the sport the status was computed for"`
	FitnessTrend   *string `json:"fitness_trend,omitempty" jsonschema:"Garmin's fitness trend indicator"`

	AcuteLoad             *float64 `json:"acute_load,omitempty" jsonschema:"the short-term training load"`
	ChronicLoad           *float64 `json:"chronic_load,omitempty" jsonschema:"the long-term training load"`
	LoadRatio             *float64 `json:"load_ratio,omitempty" jsonschema:"the acute-to-chronic ratio"`
	ACWRStatus            *string  `json:"acwr_status,omitempty" jsonschema:"Garmin's label for that ratio"`
	ACWRPercent           *float64 `json:"acwr_percent,omitempty" jsonschema:"the ratio as a percentage"`
	OptimalChronicLoadMin *float64 `json:"optimal_chronic_load_min,omitempty" jsonschema:"the optimal chronic floor"`
	OptimalChronicLoadMax *float64 `json:"optimal_chronic_load_max,omitempty" jsonschema:"the optimal chronic top"`

	VO2Max               *float64 `json:"vo2_max,omitempty" jsonschema:"the running VO2 max"`
	VO2MaxPrecise        *float64 `json:"vo2_max_precise,omitempty" jsonschema:"the running VO2 max, precise"`
	CyclingVO2Max        *float64 `json:"cycling_vo2_max,omitempty" jsonschema:"the cycling VO2 max"`
	CyclingVO2MaxPrecise *float64 `json:"cycling_vo2_max_precise,omitempty" jsonschema:"the cycling VO2 max, precise"`

	MonthlyLoadAerobicLow   *float64 `json:"monthly_load_aerobic_low,omitempty" jsonschema:"the month's low aerobic"`
	MonthlyLoadAerobicHigh  *float64 `json:"monthly_load_aerobic_high,omitempty" jsonschema:"the month's high aerobic"`
	MonthlyLoadAnaerobic    *float64 `json:"monthly_load_anaerobic,omitempty" jsonschema:"the month's anaerobic load"`
	TrainingBalanceFeedback *string  `json:"training_balance_feedback,omitempty" jsonschema:"Garmin's feedback phrase"`

	StatusDevicesReported  int  `json:"status_devices_reported" jsonschema:"how many devices reported a status"`
	BalanceDevicesReported int  `json:"balance_devices_reported" jsonschema:"how many devices reported a load balance"`
	Reported               bool `json:"reported" jsonschema:"whether any device reported a training status for this day"`
}

// LogValue reports the shape of the answer, never a reading.
func (t TrainingStatus) LogValue() slog.Value {
	return shape("trainingStatus",
		slog.Bool("reported", t.Reported),
		slog.Int("statusDevices", t.StatusDevicesReported),
		slog.Int("balanceDevices", t.BalanceDevicesReported),
		slog.String("vo2max", presence(t.VO2Max != nil)),
	)
}

// trainingStatusInput is the strict argument set: one day.
type trainingStatusInput struct {
	Date string `json:"date" jsonschema:"the day to read, YYYY-MM-DD"`
}

func getTrainingStatusContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetTrainingStatus,
			Title: "Get training status",
			Description: "read the account's training status for one day: Garmin's status " +
				"and its feedback phrase, the acute and chronic training load with the " +
				"ratio between them, the VO2 max for running and cycling, and the " +
				"month's load balance. Where several devices reported, one device's view " +
				"is returned — the most recently dated — and the number of reporting " +
				"devices is stated beside it",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the day to read")),
	}
}

// registerGetTrainingStatus registers the tool.
func registerGetTrainingStatus(registry *mcpserver.Registry, svc *service) error {
	scores, err := trainingScoresClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in trainingStatusInput) (
		*mcp.CallToolResult, TrainingStatus, error,
	) {
		day, err := parseCalendarDate("date", in.Date)
		if err != nil {
			return nil, TrainingStatus{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, TrainingStatus{}, err
		}
		read, err := scores.TrainingStatusData(ctx, session, day)
		if err != nil {
			return nil, TrainingStatus{}, fail(err)
		}
		return nil, newTrainingStatus(day.String(), read), nil
	}
	return mcpserver.AddTool(registry, getTrainingStatusContract().Registration(), handler)
}

// newTrainingStatus maps the aggregated document onto the result.
func newTrainingStatus(date string, read api.TrainingStatus) TrainingStatus {
	out := TrainingStatus{Date: date}
	device := applyStatusDevice(&out, read.MostRecentTrainingStatus)
	applyStatusVO2Max(&out, read.MostRecentVO2Max)
	applyStatusLoadBalance(&out, read.MostRecentTrainingLoadBalance, device)
	return out
}

// applyStatusDevice picks one device's status and maps it, returning the key of the
// device it chose so the load balance can describe that same device. An empty key
// means no device reported.
func applyStatusDevice(out *TrainingStatus, block *api.TrainingStatusLatest) string {
	if block == nil {
		return ""
	}
	out.StatusDevicesReported = len(block.LatestData)

	key, device, ok := api.SelectStatusDevice(block.LatestData)
	if !ok {
		return ""
	}
	out.Reported = true
	out.ReportedDate = device.CalendarDate
	out.TrainingStatus = optionalText(device.TrainingStatus)
	out.Feedback = optionalText(device.FeedbackPhrase)
	out.Sport = optionalText(device.Sport)
	out.FitnessTrend = optionalText(device.FitnessTrend)

	if load := device.AcuteTrainingLoad; load != nil {
		out.AcuteLoad = optionalFloat(load.DailyTrainingLoadAcute)
		out.ChronicLoad = optionalFloat(load.DailyTrainingLoadChronic)
		out.LoadRatio = optionalFloat(load.DailyAcuteChronicWorkloadRatio)
		out.ACWRStatus = optionalText(load.ACWRStatus)
		out.ACWRPercent = optionalFloat(load.ACWRPercent)
		out.OptimalChronicLoadMin = optionalFloat(load.MinTrainingLoadChronic)
		out.OptimalChronicLoadMax = optionalFloat(load.MaxTrainingLoadChronic)
	}
	return key
}

// applyStatusVO2Max maps the per-sport VO2 max block.
func applyStatusVO2Max(out *TrainingStatus, block *api.TrainingStatusVO2Max) {
	if block == nil {
		return
	}
	if generic := block.Generic; generic != nil {
		out.VO2Max = optionalFloat(generic.VO2MaxValue)
		out.VO2MaxPrecise = optionalFloat(generic.VO2MaxPreciseValue)
	}
	if cycling := block.Cycling; cycling != nil {
		out.CyclingVO2Max = optionalFloat(cycling.VO2MaxValue)
		out.CyclingVO2MaxPrecise = optionalFloat(cycling.VO2MaxPreciseValue)
	}
}

// applyStatusLoadBalance maps the monthly load balance of the device whose status
// this result already carries.
//
// The device is passed in rather than chosen again. Choosing again is what produced a
// result spliced across two devices: the status block picks the most recently dated
// device, and picking the first key here answered with a different watch's monthly
// load beside it. When the chosen device reports no balance the block is skipped
// entirely, because some device's load is not this device's load.
func applyStatusLoadBalance(
	out *TrainingStatus, block *api.TrainingStatusLoadBalance, device string,
) {
	if block == nil {
		return
	}
	out.BalanceDevicesReported = len(block.Devices)

	balance, ok := block.Devices[device]
	if device == "" || !ok {
		return
	}
	out.MonthlyLoadAerobicLow = optionalFloat(balance.MonthlyLoadAerobicLow)
	out.MonthlyLoadAerobicHigh = optionalFloat(balance.MonthlyLoadAerobicHigh)
	out.MonthlyLoadAnaerobic = optionalFloat(balance.MonthlyLoadAnaerobic)
	out.TrainingBalanceFeedback = optionalText(balance.FeedbackPhrase)
}
