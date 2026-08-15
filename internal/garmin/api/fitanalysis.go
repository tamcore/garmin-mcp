package api

import (
	"math"
	"sort"
	"time"
)

// Analysis bounds and thresholds.
//
// These are this server's own, not upstream's: upstream's helpers are not published
// as a specification, so the thresholds are stated here rather than claimed to match.
const (
	// maxSeriesSeconds bounds the per-second series one activity is rendered into.
	maxSeriesSeconds = 24 * 60 * 60

	// normalizedWindow is the rolling window of the normalized-power definition.
	normalizedWindow = 30

	// normalizedExponent is the fourth-power weighting of that definition.
	normalizedExponent = 4.0

	// maxSampleGapSeconds caps the time one sample is credited with, so a paused
	// recorder cannot inflate a time-in-band total.
	maxSampleGapSeconds = 10
)

// curveDurations are the standard power-duration points, in seconds.
var curveDurations = [...]int{5, 30, 60, 300, 600, 1200, 3600}

// CurveDurations returns the durations the power duration curve reports.
func CurveDurations() []int {
	out := make([]int, 0, len(curveDurations))
	out = append(out, curveDurations[:]...)
	return out
}

// A FITPowerBest is the best mean maximal power over one duration.
type FITPowerBest struct {
	Seconds     int
	Watts       float64
	StartOffset int
}

// FITDynamics are the cycling-dynamics averages of one segment.
type FITDynamics struct {
	RightBalance FITNumber
	LeftTorque   FITNumber
	RightTorque  FITNumber
	LeftSmooth   FITNumber
	RightSmooth  FITNumber
	LeftPCO      FITNumber
	RightPCO     FITNumber
}

// Present reports whether any dynamics reading was recorded.
func (d FITDynamics) Present() bool {
	return d.RightBalance.OK || d.LeftTorque.OK || d.RightTorque.OK ||
		d.LeftSmooth.OK || d.RightSmooth.OK || d.LeftPCO.OK || d.RightPCO.OK
}

// A FITSegment is the computed summary of one session or lap.
//
// Every figure is computed from the record stream rather than read out of the
// session or lap message. The container is public; the field numbering of those
// summary messages is not, so deriving the figures keeps a profile guess from
// becoming a reported measurement.
type FITSegment struct {
	Start        time.Time
	End          time.Time
	Sport        string
	Seconds      float64
	Distance     FITNumber
	Ascent       FITNumber
	AvgPower     FITNumber
	MaxPower     FITNumber
	NormalizedPw FITNumber
	Variability  FITNumber
	AvgCadence   FITNumber
	AvgHeartRate FITNumber
	MaxHeartRate FITNumber
	Samples      int
	Dynamics     FITDynamics
}

// A FITSummary is the whole analysis of one decoded activity file.
type FITSummary struct {
	Sport       string
	Start       time.Time
	End         time.Time
	Sessions    []FITSegment
	Laps        []FITSegment
	Overall     FITSegment
	Curve       []FITPowerBest
	Climbs      []FITClimb
	GradeBands  []FITGradeBand
	Temperature FITTemperature
	Drift       FITDrift
	Shifts      FITShiftSummary
}

// AnalyzeFIT computes the whole summary of one decoded activity.
func AnalyzeFIT(activity FITActivity) FITSummary {
	records := activity.Records
	summary := FITSummary{
		Overall:     analyzeSegment(records, FITSpan{}),
		Curve:       PowerDurationCurve(records),
		Climbs:      detectClimbs(records),
		GradeBands:  gradeBands(records),
		Temperature: temperatureSplit(records),
		Drift:       heartRateDrift(records),
		Shifts:      summarizeShifts(activity.Shifts, records),
	}
	summary.Sessions = analyzeSpans(records, activity.Sessions)
	summary.Laps = analyzeSpans(records, activity.Laps)

	if len(records) > 0 {
		summary.Start, summary.End = records[0].Time, records[len(records)-1].Time
	}
	if len(activity.Sessions) > 0 {
		summary.Sport = activity.Sessions[0].Sport
	}
	return summary
}

// analyzeSpans summarizes every session or lap window.
func analyzeSpans(records []FITRecord, spans []FITSpan) []FITSegment {
	out := make([]FITSegment, 0, len(spans))
	for _, span := range spans {
		out = append(out, analyzeSegment(records, span))
	}
	return out
}

// analyzeSegment summarizes the records inside one window. A zero window means the
// whole stream.
func analyzeSegment(records []FITRecord, span FITSpan) FITSegment {
	inside := recordsIn(records, span)
	segment := FITSegment{Start: span.Start, End: span.End, Sport: span.Sport, Samples: len(inside)}
	if len(inside) == 0 {
		return segment
	}
	if segment.Start.IsZero() {
		segment.Start = inside[0].Time
	}
	if segment.End.IsZero() {
		segment.End = inside[len(inside)-1].Time
	}

	segment.Seconds = segment.End.Sub(segment.Start).Seconds()
	segment.Distance = distanceOf(inside)
	segment.Ascent = fitNumber(ascentOf(inside))
	return withPowerMetrics(segment, inside)
}

// withPowerMetrics adds every averaged and peak reading of one segment.
func withPowerMetrics(segment FITSegment, records []FITRecord) FITSegment {
	var power, cadence, heart fitAccumulator
	for _, record := range records {
		power.add(record.Power)
		cadence.add(record.Cadence)
		heart.add(record.HeartRate)
	}

	segment.AvgPower, segment.MaxPower = power.mean(), power.peak()
	segment.AvgCadence = cadence.mean()
	segment.AvgHeartRate, segment.MaxHeartRate = heart.mean(), heart.peak()
	segment.NormalizedPw = normalizedPower(powerSeries(records))
	segment.Variability = variabilityIndex(segment.NormalizedPw, segment.AvgPower)
	segment.Dynamics = dynamicsOf(records)
	return segment
}

// variabilityIndex is normalized power over average power.
func variabilityIndex(normalized, average FITNumber) FITNumber {
	if !normalized.OK || !average.OK || average.Value <= 0 {
		return FITNumber{}
	}
	return fitNumber(normalized.Value / average.Value)
}

// dynamicsOf averages the cycling-dynamics readings of one segment.
func dynamicsOf(records []FITRecord) FITDynamics {
	var balance, leftTE, rightTE, leftPS, rightPS, leftPCO, rightPCO fitAccumulator
	for _, record := range records {
		balance.add(record.RightBalance)
		leftTE.add(record.LeftTorque)
		rightTE.add(record.RightTorque)
		leftPS.add(record.LeftSmooth)
		rightPS.add(record.RightSmooth)
		leftPCO.add(record.LeftPCO)
		rightPCO.add(record.RightPCO)
	}
	return FITDynamics{
		RightBalance: balance.mean(),
		LeftTorque:   leftTE.mean(),
		RightTorque:  rightTE.mean(),
		LeftSmooth:   leftPS.mean(),
		RightSmooth:  rightPS.mean(),
		LeftPCO:      leftPCO.mean(),
		RightPCO:     rightPCO.mean(),
	}
}

// recordsIn returns the records inside one window, or all of them for a zero window.
func recordsIn(records []FITRecord, span FITSpan) []FITRecord {
	if span.Start.IsZero() && span.End.IsZero() {
		return records
	}

	first := sort.Search(len(records), func(index int) bool {
		return !records[index].Time.Before(span.Start)
	})
	last := len(records)
	if !span.End.IsZero() {
		last = sort.Search(len(records), func(index int) bool {
			return records[index].Time.After(span.End)
		})
	}
	if first >= last {
		return nil
	}
	return records[first:last]
}

// distanceOf reports the distance covered inside a segment, from the odometer the
// record stream carries.
func distanceOf(records []FITRecord) FITNumber {
	var first, last FITNumber
	for _, record := range records {
		if !record.Distance.OK {
			continue
		}
		if !first.OK {
			first = record.Distance
		}
		last = record.Distance
	}
	if !first.OK || !last.OK || last.Value < first.Value {
		return FITNumber{}
	}
	return fitNumber(last.Value - first.Value)
}

// ascentOf sums the positive altitude deltas of a segment.
func ascentOf(records []FITRecord) float64 {
	var total float64
	var previous FITNumber
	for _, record := range records {
		if !record.Altitude.OK {
			continue
		}
		if previous.OK && record.Altitude.Value > previous.Value {
			total += record.Altitude.Value - previous.Value
		}
		previous = record.Altitude
	}
	return total
}

// PowerDurationCurve returns the best mean maximal power at each standard duration.
//
// The stream is rendered onto a one-second grid first, so a gap in the recording
// counts as zero watts rather than shortening the window it falls in.
func PowerDurationCurve(records []FITRecord) []FITPowerBest {
	series := powerSeries(records)
	out := make([]FITPowerBest, 0, len(curveDurations))
	if len(series) == 0 {
		return out
	}

	prefix := make([]float64, len(series)+1)
	for index, watts := range series {
		prefix[index+1] = prefix[index] + watts
	}
	for _, duration := range curveDurations {
		if best, ok := bestWindow(prefix, duration); ok {
			out = append(out, best)
		}
	}
	return out
}

// bestWindow reports the highest mean over one window length.
func bestWindow(prefix []float64, duration int) (FITPowerBest, bool) {
	samples := len(prefix) - 1
	if duration <= 0 || duration > samples {
		return FITPowerBest{}, false
	}

	best, offset := math.Inf(-1), 0
	for start := 0; start+duration <= samples; start++ {
		total := prefix[start+duration] - prefix[start]
		if total > best {
			best, offset = total, start
		}
	}
	if math.IsInf(best, -1) {
		return FITPowerBest{}, false
	}
	return FITPowerBest{Seconds: duration, Watts: best / float64(duration), StartOffset: offset}, true
}

// powerSeries renders the recorded power onto a one-second grid.
func powerSeries(records []FITRecord) []float64 {
	if len(records) == 0 {
		return nil
	}
	start := records[0].Time
	span := int(records[len(records)-1].Time.Sub(start).Seconds()) + 1
	if span < 1 {
		return nil
	}
	if span > maxSeriesSeconds {
		span = maxSeriesSeconds
	}

	series := make([]float64, span)
	seen := false
	for _, record := range records {
		index := int(record.Time.Sub(start).Seconds())
		if index < 0 || index >= span || !record.Power.OK {
			continue
		}
		series[index] = record.Power.Value
		seen = true
	}
	if !seen {
		return nil
	}
	return series
}

// normalizedPower is the fourth-power mean of the thirty-second rolling average.
func normalizedPower(series []float64) FITNumber {
	if len(series) < normalizedWindow {
		return FITNumber{}
	}

	var window, total float64
	for index := range normalizedWindow {
		window += series[index]
	}
	count := 0
	for index := normalizedWindow; ; index++ {
		total += math.Pow(window/normalizedWindow, normalizedExponent)
		count++
		if index >= len(series) {
			break
		}
		window += series[index] - series[index-normalizedWindow]
	}
	return fitNumber(math.Pow(total/float64(count), 1/normalizedExponent))
}

// A fitAccumulator averages and peaks a stream of optional readings.
type fitAccumulator struct {
	sum   float64
	count int
	max   float64
	seen  bool
}

func (a *fitAccumulator) add(value FITNumber) {
	if !value.OK {
		return
	}
	a.sum += value.Value
	a.count++
	if !a.seen || value.Value > a.max {
		a.max, a.seen = value.Value, true
	}
}

func (a fitAccumulator) mean() FITNumber {
	if a.count == 0 {
		return FITNumber{}
	}
	return fitNumber(a.sum / float64(a.count))
}

func (a fitAccumulator) peak() FITNumber {
	if !a.seen {
		return FITNumber{}
	}
	return fitNumber(a.max)
}

// sampleSeconds is how long one sample is credited with, capped so a paused
// recorder cannot inflate a total.
func sampleSeconds(previous, current time.Time) float64 {
	if previous.IsZero() {
		return 0
	}
	gap := current.Sub(previous).Seconds()
	if gap <= 0 {
		return 0
	}
	return math.Min(gap, maxSampleGapSeconds)
}
