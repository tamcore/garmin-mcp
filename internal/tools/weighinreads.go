package tools

import (
	"context"
	"log/slog"
	"math"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the weigh-in read tools.
const (
	ToolGetWeighIns      = "get_weigh_ins"
	ToolGetDailyWeighIns = "get_daily_weigh_ins"
)

// weighInGramsToKg renders a gram figure as kilograms rounded to two decimal
// places, matching weight_management.py:57 and :112:
// round(w.get("weight", 0) / 1000, 2).
func weighInGramsToKg(grams float64) float64 {
	return math.Round(grams/1000*100) / 100
}

// WeighInReading is one curated weigh-in measurement.
//
// Every field here is a body-weight or body-composition reading tied to a person, so
// it is health data end to end. Field selection and spelling follow
// weight_management.py's two curation loops verbatim (get_weigh_ins: lines 53-65;
// get_daily_weigh_ins: lines 109-120), except that Date is left empty for
// get_daily_weigh_ins, whose per-item curation carries no date field of its own
// (the day is already the tool's own argument).
type WeighInReading struct {
	Date             string   `json:"date,omitempty" jsonschema:"the calendar day, YYYY-MM-DD"`
	WeightGrams      *float64 `json:"weight_grams,omitempty" jsonschema:"the raw measurement in grams"`
	WeightKg         *float64 `json:"weight_kg,omitempty" jsonschema:"the measurement in kg, rounded to two decimals"`
	BMI              *float64 `json:"bmi,omitempty" jsonschema:"body mass index"`
	BodyFatPercent   *float64 `json:"body_fat_percent,omitempty" jsonschema:"body fat as a percentage"`
	BodyWaterPercent *float64 `json:"body_water_percent,omitempty" jsonschema:"body water as a percentage"`
	BoneMassGrams    *float64 `json:"bone_mass_grams,omitempty" jsonschema:"bone mass in grams"`
	MuscleMassGrams  *float64 `json:"muscle_mass_grams,omitempty" jsonschema:"muscle mass in grams"`
	SourceType       string   `json:"source_type,omitempty" jsonschema:"how the reading was recorded, for example MANUAL"`
	TimestampGMT     string   `json:"timestamp_gmt,omitempty" jsonschema:"the UTC wall-clock timestamp Garmin recorded"`
}

// LogValue reports which fields one reading carries, never a weight, a
// body-composition figure, a date or a timestamp.
func (r WeighInReading) LogValue() slog.Value {
	return shape("weighInReading",
		slog.String("weightGrams", presence(r.WeightGrams != nil)),
		slog.String("bmi", presence(r.BMI != nil)),
	)
}

// newWeighInReading renders one measurement. includeDate controls whether the
// per-item date is populated, matching the upstream curation each caller mirrors: the
// range read includes it (line 55), the daily read does not, because the day is
// already the tool's own argument (lines 109-120 carry no "date" key).
func newWeighInReading(m api.WeighInMeasurement, includeDate bool) WeighInReading {
	out := WeighInReading{
		BMI:              optionalFloat(m.BMI),
		BodyFatPercent:   optionalFloat(m.BodyFat),
		BodyWaterPercent: optionalFloat(m.BodyWater),
		BoneMassGrams:    optionalFloat(m.BoneMass),
		MuscleMassGrams:  optionalFloat(m.MuscleMass),
		SourceType:       textOrEmpty(m.SourceType),
		TimestampGMT:     textOrEmpty(m.TimestampGMT),
	}
	if includeDate {
		out.Date = textOrEmpty(m.CalendarDate)
	}
	if grams, ok := m.Weight.Float64(); ok {
		out.WeightGrams = &grams
		kg := weighInGramsToKg(grams)
		out.WeightKg = &kg
	}
	return out
}

// weighInReadings renders every measurement in list order.
func weighInReadings(measurements []api.WeighInMeasurement, includeDate bool) []WeighInReading {
	out := make([]WeighInReading, 0, len(measurements))
	for _, m := range measurements {
		out = append(out, newWeighInReading(m, includeDate))
	}
	return out
}

// weighInAverage renders an average weight in grams and kilograms, matching the
// upstream truthiness check (weight_management.py:77-79, :127-129): an average of
// exactly zero is treated the same as absent, because upstream's own `if
// total_avg.get("weight")` is falsy for both None and 0.
func weighInAverage(avg *api.WeighInAverage) (gramsOut, kgOut *float64) {
	if avg == nil {
		return nil, nil
	}
	grams, ok := avg.Weight.Float64()
	if !ok || grams == 0 {
		return nil, nil
	}
	kg := weighInGramsToKg(grams)
	return &grams, &kg
}

// compareStrings orders two strings the way cmp.Compare does, kept local so this
// file needs no extra import beyond what it already uses.
func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// WeighInRangeResult is the answer for get_weigh_ins.
type WeighInRangeResult struct {
	StartDate             string           `json:"start_date" jsonschema:"the inclusive first day of the window"`
	EndDate               string           `json:"end_date" jsonschema:"the inclusive last day of the window"`
	MeasurementCount      int              `json:"measurement_count" jsonschema:"how many measurements Garmin held"`
	DaysWithData          int              `json:"days_with_data" jsonschema:"how many days in the window carry data"`
	Measurements          []WeighInReading `json:"measurements" jsonschema:"the measurements, most recent day first"`
	MeasurementsTruncated bool             `json:"measurements_truncated" jsonschema:"whether the list was cut to fit"`
	AverageWeightGrams    *float64         `json:"average_weight_grams,omitempty" jsonschema:"the window average in grams"`
	AverageWeightKg       *float64         `json:"average_weight_kg,omitempty" jsonschema:"the window average in kg"`
}

// LogValue reports the shape of the window, never a reading.
func (r WeighInRangeResult) LogValue() slog.Value {
	return shape("weighInRange",
		slog.Int("measurements", r.MeasurementCount),
		slog.Int("daysWithData", r.DaysWithData),
		slog.Bool("truncated", r.MeasurementsTruncated),
	)
}

// getWeighInsInput is the strict argument set.
type getWeighInsInput struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day of the window, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day of the window, YYYY-MM-DD"`
}

func getWeighInsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetWeighIns,
			Title: "Get weigh-ins over a date window",
			Description: "read every weigh-in Garmin holds between two calendar days, " +
				"inclusive: the raw and converted weight, body composition figures Garmin " +
				"attached, the source and the timestamp of each. The list is bounded and " +
				"reports a truncation flag",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "the inclusive first day of the window"),
			dateProperty("end_date", "the inclusive last day of the window"),
		),
	}
}

// registerGetWeighIns registers the tool.
func registerGetWeighIns(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getWeighInsInput) (
		*mcp.CallToolResult, WeighInRangeResult, error,
	) {
		out, err := svc.getWeighIns(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getWeighInsContract().Registration(), handler)
}

// getWeighIns performs the read behind the tool.
func (s *service) getWeighIns(ctx context.Context, in getWeighInsInput) (WeighInRangeResult, error) {
	span, err := parseWindow(in.StartDate, in.EndDate, s.limits)
	if err != nil {
		return WeighInRangeResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return WeighInRangeResult{}, err
	}

	window, err := s.weight.GetWeighIns(ctx, session, span)
	if err != nil {
		return WeighInRangeResult{}, fail(err)
	}

	readings := weighInReadings(window.Measurements(), true)
	// Sort most-recent-day first, matching weight_management.py:70-73's
	// sort(key=lambda x: x.get("date") or "", reverse=True). YYYY-MM-DD sorts
	// lexicographically the same as chronologically, so a plain string comparison
	// reproduces the same order.
	slices.SortFunc(readings, func(a, b WeighInReading) int {
		return compareStrings(b.Date, a.Date)
	})

	avgGrams, avgKg := weighInAverage(window.TotalAverage)
	return WeighInRangeResult{
		StartDate:             in.StartDate,
		EndDate:               in.EndDate,
		MeasurementCount:      len(readings),
		DaysWithData:          len(window.DailySummaries),
		Measurements:          readings,
		MeasurementsTruncated: window.MeasurementsTruncated(),
		AverageWeightGrams:    avgGrams,
		AverageWeightKg:       avgKg,
	}, nil
}

// DailyWeighInsResult is the answer for get_daily_weigh_ins.
type DailyWeighInsResult struct {
	Date                  string           `json:"date" jsonschema:"the day requested, YYYY-MM-DD"`
	MeasurementCount      int              `json:"measurement_count" jsonschema:"how many measurements Garmin held"`
	Measurements          []WeighInReading `json:"measurements" jsonschema:"the day's measurements, Garmin's own order"`
	MeasurementsTruncated bool             `json:"measurements_truncated" jsonschema:"whether the list was cut to fit"`
	AverageWeightGrams    *float64         `json:"average_weight_grams,omitempty" jsonschema:"the day average in grams"`
	AverageWeightKg       *float64         `json:"average_weight_kg,omitempty" jsonschema:"the day's average weight in kg"`
}

// LogValue reports the shape of the day, never a reading.
func (d DailyWeighInsResult) LogValue() slog.Value {
	return shape("dailyWeighIns",
		slog.Int("measurements", d.MeasurementCount),
		slog.Bool("truncated", d.MeasurementsTruncated),
	)
}

// getDailyWeighInsInput is the strict argument set: one calendar day.
type getDailyWeighInsInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getDailyWeighInsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDailyWeighIns,
			Title: "Get weigh-ins for one day",
			Description: "read every weigh-in Garmin holds for one calendar day: the raw " +
				"and converted weight, the body composition figures Garmin attached, the " +
				"source and the timestamp of each. The list is bounded and reports a " +
				"truncation flag",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetDailyWeighIns registers the tool.
func registerGetDailyWeighIns(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getDailyWeighInsInput) (
		*mcp.CallToolResult, DailyWeighInsResult, error,
	) {
		out, err := svc.getDailyWeighIns(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getDailyWeighInsContract().Registration(), handler)
}

// getDailyWeighIns performs the read behind the tool.
func (s *service) getDailyWeighIns(ctx context.Context, dateValue string) (DailyWeighInsResult, error) {
	day, err := parseCalendarDate("date", dateValue)
	if err != nil {
		return DailyWeighInsResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return DailyWeighInsResult{}, err
	}

	daily, err := s.weight.GetDailyWeighIns(ctx, session, day)
	if err != nil {
		return DailyWeighInsResult{}, fail(err)
	}

	avgGrams, avgKg := weighInAverage(daily.TotalAverage)
	return DailyWeighInsResult{
		Date:                  dateValue,
		MeasurementCount:      len(daily.Measurements()),
		Measurements:          weighInReadings(daily.Measurements(), false),
		MeasurementsTruncated: daily.MeasurementsTruncated(),
		AverageWeightGrams:    avgGrams,
		AverageWeightKg:       avgKg,
	}, nil
}
