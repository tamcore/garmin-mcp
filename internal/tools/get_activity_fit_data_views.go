package tools

import (
	"math"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// Rounding places. A FIT reading is a sensor value, not a measurement to fifteen
// decimal places, and a rounded result is a smaller result.
const (
	placesWhole = 0
	placesOne   = 1
	placesTwo   = 2
)

// A FITDynamicsView is the cycling-dynamics average set of one segment.
type FITDynamicsView struct {
	RightBalance   *float64 `json:"right_balance_percent,omitempty" jsonschema:"the right-side share of the power split"`
	LeftTorque     *float64 `json:"left_torque_effectiveness,omitempty" jsonschema:"left torque effectiveness in percent"`
	RightTorque    *float64 `json:"right_torque_effectiveness,omitempty" jsonschema:"right torque effectiveness, percent"`
	LeftSmoothness *float64 `json:"left_pedal_smoothness,omitempty" jsonschema:"left pedal smoothness in percent"`
	RightSmooth    *float64 `json:"right_pedal_smoothness,omitempty" jsonschema:"right pedal smoothness in percent"`
	LeftOffset     *float64 `json:"left_platform_center_offset_mm,omitempty" jsonschema:"left platform offset, mm"`
	RightOffset    *float64 `json:"right_platform_center_offset_mm,omitempty" jsonschema:"right platform offset, mm"`
}

// A FITSegmentView is one computed session, lap or whole-activity summary.
type FITSegmentView struct {
	Sport            *string          `json:"sport,omitempty" jsonschema:"the recorded sport, when the file names one"`
	Start            *string          `json:"start_time,omitempty" jsonschema:"the segment start in UTC"`
	End              *string          `json:"end_time,omitempty" jsonschema:"the segment end in UTC"`
	DurationSecs     float64          `json:"duration_seconds" jsonschema:"the elapsed seconds of the segment"`
	Samples          int              `json:"samples" jsonschema:"how many records the segment covers"`
	DistanceMeters   *float64         `json:"distance_meters,omitempty" jsonschema:"the distance covered in meters"`
	AscentMeters     *float64         `json:"ascent_meters,omitempty" jsonschema:"the elevation gained in meters"`
	Calories         *float64         `json:"calories_kcal,omitempty" jsonschema:"the energy the device computed, in kcal"`
	AveragePower     *float64         `json:"average_power_w,omitempty" jsonschema:"the average power in watts"`
	MaxPower         *float64         `json:"max_power_w,omitempty" jsonschema:"the peak power in watts"`
	NormalizedPower  *float64         `json:"normalized_power_w,omitempty" jsonschema:"the normalized power in watts"`
	VariabilityIndex *float64         `json:"variability_index,omitempty" jsonschema:"normalized power over average power"`
	AverageCadence   *float64         `json:"average_cadence_rpm,omitempty" jsonschema:"the average cadence in rpm"`
	AverageHeartRate *float64         `json:"average_heart_rate,omitempty" jsonschema:"the average heart rate in bpm"`
	MaxHeartRate     *float64         `json:"max_heart_rate,omitempty" jsonschema:"the peak heart rate in bpm"`
	Dynamics         *FITDynamicsView `json:"cycling_dynamics,omitempty" jsonschema:"the pedal metrics, when recorded"`
}

// A FITClimbView is one detected sustained ascent.
type FITClimbView struct {
	Start            string   `json:"start_time" jsonschema:"the climb start in UTC"`
	End              string   `json:"end_time" jsonschema:"the climb end in UTC"`
	DurationSecs     float64  `json:"duration_seconds" jsonschema:"how long the climb lasted"`
	GainMeters       float64  `json:"elevation_gain_meters" jsonschema:"the height gained in meters"`
	VAM              float64  `json:"vam_meters_per_hour" jsonschema:"the vertical ascent rate in meters per hour"`
	DistanceMeters   *float64 `json:"distance_meters,omitempty" jsonschema:"the distance climbed in meters"`
	AverageGrade     *float64 `json:"average_grade_percent,omitempty" jsonschema:"the average gradient in percent"`
	AveragePower     *float64 `json:"average_power_w,omitempty" jsonschema:"the average power in watts"`
	AverageCadence   *float64 `json:"average_cadence_rpm,omitempty" jsonschema:"the average cadence in rpm"`
	AverageHeartRate *float64 `json:"average_heart_rate,omitempty" jsonschema:"the average heart rate in bpm"`
}

// A FITGradeBandView is the time spent in one terrain steepness band.
type FITGradeBandView struct {
	Band             string   `json:"band" jsonschema:"the terrain band, from steep_descent to very_steep_climb"`
	Seconds          float64  `json:"seconds" jsonschema:"the seconds spent in the band"`
	Samples          int      `json:"samples" jsonschema:"how many records fell in the band"`
	AveragePower     *float64 `json:"average_power_w,omitempty" jsonschema:"the average power in watts"`
	AverageCadence   *float64 `json:"average_cadence_rpm,omitempty" jsonschema:"the average cadence in rpm"`
	AverageHeartRate *float64 `json:"average_heart_rate,omitempty" jsonschema:"the average heart rate in bpm"`
}

// A FITTemperatureView compares the warmest quarter of a ride with the coolest.
type FITTemperatureView struct {
	Samples          int      `json:"samples" jsonschema:"how many records carried a temperature"`
	HottestAverageC  float64  `json:"hottest_average_c" jsonschema:"the average temperature of the warmest quarter"`
	CoolestAverageC  float64  `json:"coolest_average_c" jsonschema:"the average temperature of the coolest quarter"`
	HotHeartRate     *float64 `json:"hot_average_heart_rate,omitempty" jsonschema:"the average heart rate when warmest"`
	HotPower         *float64 `json:"hot_average_power_w,omitempty" jsonschema:"the average power when warmest"`
	CoolHeartRate    *float64 `json:"cool_average_heart_rate,omitempty" jsonschema:"the average heart rate when coolest"`
	CoolAveragePower *float64 `json:"cool_average_power_w,omitempty" jsonschema:"the average power when coolest"`
}

// A FITDriftView is the aerobic decoupling of one ride.
type FITDriftView struct {
	Seconds     float64 `json:"paired_seconds" jsonschema:"the seconds with both power and heart rate"`
	FirstRatio  float64 `json:"first_half_power_per_beat" jsonschema:"the first half's power over heart rate"`
	SecondRatio float64 `json:"second_half_power_per_beat" jsonschema:"the second half's power over heart rate"`
	Percent     float64 `json:"decoupling_percent" jsonschema:"how far the ratio fell, in percent"`
}

// A FITShiftEventView is one electronic gear change.
type FITShiftEventView struct {
	Time      string   `json:"time" jsonschema:"when the shift happened, in UTC"`
	Position  string   `json:"position" jsonschema:"front or rear derailleur"`
	FrontGear *float64 `json:"front_gear,omitempty" jsonschema:"the front gear after the shift"`
	RearGear  *float64 `json:"rear_gear,omitempty" jsonschema:"the rear gear after the shift"`
	Cadence   *float64 `json:"cadence_rpm,omitempty" jsonschema:"the cadence at the shift"`
	Grade     *float64 `json:"grade_percent,omitempty" jsonschema:"the gradient at the shift"`
	Quality   string   `json:"quality,omitempty" jsonschema:"proactive, reactive, coasting or spun_out"`
}

// A FITShiftView counts and lists the gear changes of one ride.
type FITShiftView struct {
	Total      int                 `json:"total" jsonschema:"how many gear changes the file records"`
	Front      int                 `json:"front" jsonschema:"how many were front changes"`
	Rear       int                 `json:"rear" jsonschema:"how many were rear changes"`
	Proactive  int                 `json:"proactive" jsonschema:"shifts at 70 to 100 rpm"`
	Reactive   int                 `json:"reactive" jsonschema:"shifts below 70 rpm"`
	Coasting   int                 `json:"coasting" jsonschema:"shifts at zero cadence"`
	SpunOut    int                 `json:"spun_out" jsonschema:"shifts above 100 rpm"`
	Classified int                 `json:"classified" jsonschema:"how many shifts had a cadence to classify"`
	Truncated  bool                `json:"truncated" jsonschema:"whether the event list was cut at this server's bound"`
	Events     []FITShiftEventView `json:"events" jsonschema:"the gear changes, in order"`
}

// A FITPowerBestView is the best mean maximal power over one duration.
type FITPowerBestView struct {
	Seconds     int     `json:"duration_seconds" jsonschema:"the window length in seconds"`
	Watts       float64 `json:"watts" jsonschema:"the best mean power over that window"`
	StartOffset int     `json:"start_offset_seconds" jsonschema:"how far into the activity the window starts"`
}

// A FITRecordView is one sample of the per-second series.
//
// It carries no coordinates. The file has them and this server never decodes them:
// a per-second track is the most sensitive thing in an activity file, and no figure
// in this result needs it.
type FITRecordView struct {
	OffsetSecs  int      `json:"offset_seconds" jsonschema:"seconds since the first record"`
	Power       *float64 `json:"power_w,omitempty" jsonschema:"the power in watts"`
	HeartRate   *float64 `json:"heart_rate,omitempty" jsonschema:"the heart rate in bpm"`
	Cadence     *float64 `json:"cadence_rpm,omitempty" jsonschema:"the cadence in rpm"`
	Speed       *float64 `json:"speed_mps,omitempty" jsonschema:"the speed in meters per second"`
	Altitude    *float64 `json:"altitude_meters,omitempty" jsonschema:"the altitude in meters"`
	Grade       *float64 `json:"grade_percent,omitempty" jsonschema:"the gradient in percent"`
	Temperature *float64 `json:"temperature_c,omitempty" jsonschema:"the temperature in degrees Celsius"`
}

// newFITSegmentView renders one computed segment.
func newFITSegmentView(segment api.FITSegment) FITSegmentView {
	view := FITSegmentView{
		Start:            fitInstant(segment.Start),
		End:              fitInstant(segment.End),
		DurationSecs:     fitRound(segment.Seconds, placesWhole),
		Samples:          segment.Samples,
		DistanceMeters:   fitOptional(segment.Distance, placesOne),
		AscentMeters:     fitOptional(segment.Ascent, placesOne),
		Calories:         fitOptional(segment.Calories, placesWhole),
		AveragePower:     fitOptional(segment.AvgPower, placesOne),
		MaxPower:         fitOptional(segment.MaxPower, placesWhole),
		NormalizedPower:  fitOptional(segment.NormalizedPw, placesOne),
		VariabilityIndex: fitOptional(segment.Variability, placesTwo),
		AverageCadence:   fitOptional(segment.AvgCadence, placesOne),
		AverageHeartRate: fitOptional(segment.AvgHeartRate, placesOne),
		MaxHeartRate:     fitOptional(segment.MaxHeartRate, placesWhole),
	}
	if segment.Sport != "" {
		sport := segment.Sport
		view.Sport = &sport
	}
	if segment.Dynamics.Present() {
		dynamics := newFITDynamicsView(segment.Dynamics)
		view.Dynamics = &dynamics
	}
	return view
}

// newFITDynamicsView renders the pedal metrics of one segment.
func newFITDynamicsView(dynamics api.FITDynamics) FITDynamicsView {
	return FITDynamicsView{
		RightBalance:   fitOptional(dynamics.RightBalance, placesOne),
		LeftTorque:     fitOptional(dynamics.LeftTorque, placesOne),
		RightTorque:    fitOptional(dynamics.RightTorque, placesOne),
		LeftSmoothness: fitOptional(dynamics.LeftSmooth, placesOne),
		RightSmooth:    fitOptional(dynamics.RightSmooth, placesOne),
		LeftOffset:     fitOptional(dynamics.LeftPCO, placesOne),
		RightOffset:    fitOptional(dynamics.RightPCO, placesOne),
	}
}

// newFITClimbViews renders every detected climb.
func newFITClimbViews(climbs []api.FITClimb) []FITClimbView {
	out := make([]FITClimbView, 0, len(climbs))
	for _, climb := range climbs {
		out = append(out, FITClimbView{
			Start:            climb.Start.UTC().Format(time.RFC3339),
			End:              climb.End.UTC().Format(time.RFC3339),
			DurationSecs:     fitRound(climb.Seconds, placesWhole),
			GainMeters:       fitRound(climb.GainMeters, placesOne),
			VAM:              fitRound(climb.VAM, placesWhole),
			DistanceMeters:   fitOptional(climb.Distance, placesOne),
			AverageGrade:     fitOptional(climb.AvgGrade, placesTwo),
			AveragePower:     fitOptional(climb.AvgPower, placesOne),
			AverageCadence:   fitOptional(climb.AvgCadence, placesOne),
			AverageHeartRate: fitOptional(climb.AvgHeartRate, placesOne),
		})
	}
	return out
}

// newFITGradeBandViews renders the terrain bands the ride touched.
func newFITGradeBandViews(bands []api.FITGradeBand) []FITGradeBandView {
	out := make([]FITGradeBandView, 0, len(bands))
	for _, band := range bands {
		out = append(out, FITGradeBandView{
			Band:             band.Label,
			Seconds:          fitRound(band.Seconds, placesWhole),
			Samples:          band.Samples,
			AveragePower:     fitOptional(band.AvgPower, placesOne),
			AverageCadence:   fitOptional(band.AvgCadence, placesOne),
			AverageHeartRate: fitOptional(band.AvgHeartRate, placesOne),
		})
	}
	return out
}

// newFITTemperatureView renders the hot-cool comparison, when there was one.
func newFITTemperatureView(split api.FITTemperature) *FITTemperatureView {
	if !split.OK {
		return nil
	}
	return &FITTemperatureView{
		Samples:          split.Samples,
		HottestAverageC:  fitRound(split.HotAvgC, placesOne),
		CoolestAverageC:  fitRound(split.CoolAvgC, placesOne),
		HotHeartRate:     fitOptional(split.HotHeartRate, placesOne),
		HotPower:         fitOptional(split.HotPower, placesOne),
		CoolHeartRate:    fitOptional(split.CoolHeartRate, placesOne),
		CoolAveragePower: fitOptional(split.CoolPower, placesOne),
	}
}

// newFITDriftView renders the decoupling, when the ride was long enough for one.
func newFITDriftView(drift api.FITDrift) *FITDriftView {
	if !drift.OK {
		return nil
	}
	return &FITDriftView{
		Seconds:     fitRound(drift.Seconds, placesWhole),
		FirstRatio:  fitRound(drift.FirstRatio, placesTwo),
		SecondRatio: fitRound(drift.SecondRatio, placesTwo),
		Percent:     fitRound(drift.Percent, placesTwo),
	}
}

// newFITShiftView renders the gear changes under the result bound.
func newFITShiftView(summary api.FITShiftSummary, limit int) FITShiftView {
	events := summary.Events
	truncated := len(events) > limit
	if truncated {
		events = events[:limit]
	}

	out := FITShiftView{
		Total:      summary.Total,
		Front:      summary.Front,
		Rear:       summary.Rear,
		Proactive:  summary.Proactive,
		Reactive:   summary.Reactive,
		Coasting:   summary.Coasting,
		SpunOut:    summary.SpunOut,
		Classified: summary.Classified,
		Truncated:  truncated,
		Events:     make([]FITShiftEventView, 0, len(events)),
	}
	for _, event := range events {
		out.Events = append(out.Events, newFITShiftEventView(event))
	}
	return out
}

// The two derailleur labels.
const (
	shiftPositionFront = "front"
	shiftPositionRear  = "rear"
)

// newFITShiftEventView renders one gear change.
func newFITShiftEventView(event api.FITShiftEvent) FITShiftEventView {
	position := shiftPositionRear
	if event.Front {
		position = shiftPositionFront
	}
	return FITShiftEventView{
		Time:      event.Time.UTC().Format(time.RFC3339),
		Position:  position,
		FrontGear: fitOptional(event.FrontGear, placesWhole),
		RearGear:  fitOptional(event.RearGear, placesWhole),
		Cadence:   fitOptional(event.Cadence, placesWhole),
		Grade:     fitOptional(event.Grade, placesTwo),
		Quality:   event.Quality,
	}
}

// newFITPowerBestViews renders a power duration curve.
func newFITPowerBestViews(curve []api.FITPowerBest) []FITPowerBestView {
	out := make([]FITPowerBestView, 0, len(curve))
	for _, best := range curve {
		out = append(out, FITPowerBestView{
			Seconds:     best.Seconds,
			Watts:       fitRound(best.Watts, placesOne),
			StartOffset: best.StartOffset,
		})
	}
	return out
}

// newFITRecordViews renders the per-second series under the result bound.
func newFITRecordViews(records []api.FITRecord, limit int) ([]FITRecordView, bool) {
	truncated := len(records) > limit
	if truncated {
		records = records[:limit]
	}
	if len(records) == 0 {
		return []FITRecordView{}, truncated
	}

	start := records[0].Time
	out := make([]FITRecordView, 0, len(records))
	for _, record := range records {
		out = append(out, FITRecordView{
			OffsetSecs:  int(record.Time.Sub(start).Seconds()),
			Power:       fitOptional(record.Power, placesWhole),
			HeartRate:   fitOptional(record.HeartRate, placesWhole),
			Cadence:     fitOptional(record.Cadence, placesWhole),
			Speed:       fitOptional(record.Speed, placesTwo),
			Altitude:    fitOptional(record.Altitude, placesOne),
			Grade:       fitOptional(record.Grade, placesTwo),
			Temperature: fitOptional(record.Temperature, placesOne),
		})
	}
	return out, truncated
}

// fitOptional renders an optional reading, rounded.
func fitOptional(value api.FITNumber, places int) *float64 {
	if !value.OK {
		return nil
	}
	rounded := fitRound(value.Value, places)
	return &rounded
}

// fitRound rounds a reading to the given number of decimal places.
func fitRound(value float64, places int) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	scale := math.Pow(10, float64(places))
	return math.Round(value*scale) / scale
}

// fitInstant renders an instant in UTC, or nothing when there is none.
func fitInstant(at time.Time) *string {
	if at.IsZero() {
		return nil
	}
	rendered := at.UTC().Format(time.RFC3339)
	return &rendered
}
