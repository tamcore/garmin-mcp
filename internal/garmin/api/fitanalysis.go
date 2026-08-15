package api

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// Analysis bounds and thresholds.
//
// These are this server's own, not upstream's: upstream's helpers are not published
// as a specification, so the thresholds are stated here rather than claimed to match.
const (
	// maxSampleGapSeconds caps the time one sample is credited with, so a paused
	// recorder cannot inflate a time-in-band total.
	maxSampleGapSeconds = 10

	// ascentThreshold is the rise a run of samples must accumulate before it counts
	// as a climb rather than as barometric noise. Summing every positive delta of a
	// one-second altitude series roughly doubles the figure a device reports, because
	// the sensor jitters by a few decimeters between consecutive samples. This
	// threshold is only reached by the record-derived fallback: a file that carries
	// total_ascent is read, not resummed.
	ascentThreshold = 3.0
)

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

// A FITSegment is the computed summary of one session, lap or whole activity.
//
// A figure the FIT profile carries is read out of the session or lap message and is
// preferred over the same figure derived from the record stream. Only what the
// profile does not carry — normalized power over an arbitrary window, the
// variability index and the cycling-dynamics averages — is derived here.
type FITSegment struct {
	Start        time.Time
	End          time.Time
	Sport        string
	Seconds      float64
	Distance     FITNumber
	Ascent       FITNumber
	Calories     FITNumber
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
//
// ctx bounds the analysis, and it is not decoration. This is the one stage of the FIT
// path whose cost is set by the file rather than by the request: the bounds cap a decode
// at DefaultMaxFITSessions sessions, DefaultMaxFITLaps laps and DefaultMaxFITRecords
// records, and a file is free to make every span cover the whole record stream, so the
// worst case is the product of all three, several walks deep. The arithmetic is pinned
// in fitcollector_test.go. Every stage below and every span is separated by a context
// check, so a caller who has given up stops paying at the next one rather than at the
// end of the file, and a cancelled caller is reported as itself.
func AnalyzeFIT(ctx context.Context, activity FITActivity) (FITSummary, error) {
	records := activity.Records
	var summary FITSummary

	// The whole-activity stages, each one walk over the record stream. They are a list
	// rather than one struct literal so a context check can sit between them.
	stages := []func(){
		func() { summary.Overall = analyzeSegment(records, overallSpan(activity.Sessions)) },
		func() { summary.Curve = PowerDurationCurve(records) },
		func() { summary.Climbs = detectClimbs(records) },
		func() { summary.GradeBands = gradeBands(records) },
		func() { summary.Temperature = temperatureSplit(records) },
		func() { summary.Drift = heartRateDrift(records) },
		func() { summary.Shifts = summarizeShifts(activity.Shifts, records) },
	}
	for _, stage := range stages {
		if err := analysisContext(ctx); err != nil {
			return FITSummary{}, err
		}
		stage()
	}

	var err error
	if summary.Sessions, err = analyzeSpans(ctx, records, activity.Sessions); err != nil {
		return FITSummary{}, err
	}
	if summary.Laps, err = analyzeSpans(ctx, records, activity.Laps); err != nil {
		return FITSummary{}, err
	}

	if len(records) > 0 {
		summary.Start, summary.End = records[0].Time, records[len(records)-1].Time
	}
	if len(activity.Sessions) > 0 {
		summary.Sport = activity.Sessions[0].Sport
	}
	return summary, nil
}

// analysisContext reports the caller's cancellation as itself, wrapped with what was
// being done. A cancelled caller is never reported as a malformed file.
func analysisContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("analysing the activity file: %w", err)
	}
	return nil
}

// analyzeSpans summarizes every session or lap window.
//
// The context is checked per span rather than per list, because the span count is the
// multiplier: one span is a bounded amount of work and a list of them is not.
func analyzeSpans(
	ctx context.Context, records []FITRecord, spans []FITSpan,
) ([]FITSegment, error) {
	out := make([]FITSegment, 0, len(spans))
	for _, span := range spans {
		if err := analysisContext(ctx); err != nil {
			return nil, err
		}
		out = append(out, analyzeSegment(records, span))
	}
	return out, nil
}

// analyzeSegment summarizes the records inside one window and then lets the profile
// figures of that window override what the records implied. A zero window means the
// whole stream.
func analyzeSegment(records []FITRecord, span FITSpan) FITSegment {
	return withProfileFigures(deriveSegment(records, span), span)
}

// deriveSegment computes every figure of one window from the record stream alone.
func deriveSegment(records []FITRecord, span FITSpan) FITSegment {
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

// withProfileFigures replaces each derived figure with the one the device wrote into
// the session or lap message, where the file carries it. A figure the profile does
// not carry keeps its derived value.
func withProfileFigures(segment FITSegment, span FITSpan) FITSegment {
	if span.Elapsed.OK {
		segment.Seconds = span.Elapsed.Value
	}
	segment.Distance = preferred(span.Distance, segment.Distance)
	segment.Ascent = preferred(span.Ascent, segment.Ascent)
	segment.Calories = span.Calories
	segment.AvgPower = preferred(span.AvgPower, segment.AvgPower)
	segment.MaxPower = preferred(span.MaxPower, segment.MaxPower)
	segment.NormalizedPw = preferred(span.NormalizedPw, segment.NormalizedPw)
	segment.AvgCadence = preferred(span.AvgCadence, segment.AvgCadence)
	segment.AvgHeartRate = preferred(span.AvgHeartRate, segment.AvgHeartRate)
	segment.MaxHeartRate = preferred(span.MaxHeartRate, segment.MaxHeartRate)
	segment.Variability = variabilityIndex(segment.NormalizedPw, segment.AvgPower)
	return segment
}

// preferred returns the profile reading when the file carries one, and the derived
// reading otherwise.
func preferred(profile, derived FITNumber) FITNumber {
	if profile.OK {
		return profile
	}
	return derived
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

// ascentOf sums the climbs of a segment, ignoring any rise that never accumulates
// past the noise threshold.
//
// The anchor is the lowest altitude seen since the last banked climb. A rise is
// credited only once it clears the threshold above that anchor, and a descent moves
// the anchor down, so sensor jitter cancels instead of accumulating.
func ascentOf(records []FITRecord) float64 {
	var total float64
	var anchor FITNumber
	for _, record := range records {
		altitude := record.Altitude
		if !altitude.OK {
			continue
		}
		switch {
		case !anchor.OK, altitude.Value < anchor.Value:
			anchor = altitude
		case altitude.Value-anchor.Value >= ascentThreshold:
			total += altitude.Value - anchor.Value
			anchor = altitude
		}
	}
	return total
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
