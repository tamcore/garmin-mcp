package api

import (
	"math"
	"sort"
	"time"
)

// Climb detection thresholds. They are this server's own, and they are stated here
// rather than presented as upstream's, because upstream publishes no specification
// for its climb detection.
const (
	climbGradeThreshold = 3.0
	climbGapSeconds     = 15.0
	climbMinSeconds     = 60.0
	climbMinGainMeters  = 10.0
	maxClimbs           = 100

	// secondsPerHour converts a rate per second into a rate per hour, which is the
	// unit vertical ascent is reported in.
	secondsPerHour = 3600.0

	// percent renders a ratio as a percentage.
	percent = 100.0
)

// A FITClimb is one detected sustained ascent.
type FITClimb struct {
	Start        time.Time
	End          time.Time
	Seconds      float64
	GainMeters   float64
	Distance     FITNumber
	AvgGrade     FITNumber
	VAM          float64
	AvgPower     FITNumber
	AvgCadence   FITNumber
	AvgHeartRate FITNumber
}

// detectClimbs finds every run of samples at or above the climb grade that lasts
// long enough and gains enough height, allowing a short dip below the grade so one
// flat corner does not split a climb in two.
func detectClimbs(records []FITRecord) []FITClimb {
	out := make([]FITClimb, 0)
	start, lastAbove := -1, -1

	for index := range records {
		grade, ok := gradeAt(records, index)
		above := ok && grade >= climbGradeThreshold
		switch {
		case above && start < 0:
			start, lastAbove = index, index
		case above:
			lastAbove = index
		case start >= 0 && sinceSample(records, lastAbove, index) > climbGapSeconds:
			out = appendClimb(out, records[start:lastAbove+1])
			start, lastAbove = -1, -1
		}
		if len(out) >= maxClimbs {
			return out
		}
	}
	if start >= 0 {
		out = appendClimb(out, records[start:lastAbove+1])
	}
	return out
}

// netGain is the height a detected run actually gained: its last altitude less its
// first. A climb is one sustained rise, so its gain is the difference across it, not
// a sum of per-sample deltas — summing would re-add every metre of sensor jitter
// inside the run and report a climb steeper than the one that was ridden.
func netGain(run []FITRecord) float64 {
	var first, last FITNumber
	for _, record := range run {
		if !record.Altitude.OK {
			continue
		}
		if !first.OK {
			first = record.Altitude
		}
		last = record.Altitude
	}
	if !first.OK || last.Value <= first.Value {
		return 0
	}
	return last.Value - first.Value
}

// sinceSample reports the seconds between two samples.
func sinceSample(records []FITRecord, from, to int) float64 {
	if from < 0 || to < 0 {
		return 0
	}
	return records[to].Time.Sub(records[from].Time).Seconds()
}

// appendClimb adds one candidate run when it is long enough and steep enough.
func appendClimb(out []FITClimb, run []FITRecord) []FITClimb {
	if len(run) < 2 {
		return out
	}
	seconds := run[len(run)-1].Time.Sub(run[0].Time).Seconds()
	gain := netGain(run)
	if seconds < climbMinSeconds || gain < climbMinGainMeters {
		return out
	}

	var power, cadence, heart fitAccumulator
	for _, record := range run {
		power.add(record.Power)
		cadence.add(record.Cadence)
		heart.add(record.HeartRate)
	}
	climb := FITClimb{
		Start:        run[0].Time,
		End:          run[len(run)-1].Time,
		Seconds:      seconds,
		GainMeters:   gain,
		Distance:     distanceOf(run),
		VAM:          gain / seconds * secondsPerHour,
		AvgPower:     power.mean(),
		AvgCadence:   cadence.mean(),
		AvgHeartRate: heart.mean(),
	}
	if climb.Distance.OK && climb.Distance.Value > 0 {
		climb.AvgGrade = fitNumber(gain / climb.Distance.Value * percent)
	}
	return append(out, climb)
}

// gradeAt reports the grade of one sample: the recorded one where the device wrote
// it, and otherwise the one the altitude and distance deltas imply.
func gradeAt(records []FITRecord, index int) (float64, bool) {
	record := records[index]
	if record.Grade.OK {
		return record.Grade.Value, true
	}
	if index == 0 {
		return 0, false
	}

	previous := records[index-1]
	if !record.Altitude.OK || !previous.Altitude.OK ||
		!record.Distance.OK || !previous.Distance.OK {
		return 0, false
	}
	run := record.Distance.Value - previous.Distance.Value
	if run <= 0 {
		return 0, false
	}
	return (record.Altitude.Value - previous.Altitude.Value) / run * percent, true
}

// A FITGradeBand is the time and the averages inside one terrain steepness band.
type FITGradeBand struct {
	Label        string
	Seconds      float64
	Samples      int
	AvgPower     FITNumber
	AvgCadence   FITNumber
	AvgHeartRate FITNumber
}

// gradeBandEdges is the closed band set, each named by its upper bound in percent.
var gradeBandEdges = [...]struct {
	label string
	upper float64
}{
	{"steep_descent", -6},
	{"descent", -3},
	{"gentle_descent", -1},
	{"flat", 1},
	{"gentle_climb", 3},
	{"climb", 6},
	{"steep_climb", 10},
	{"very_steep_climb", math.Inf(1)},
}

// gradeBands reports how the ride was spent across the terrain bands.
func gradeBands(records []FITRecord) []FITGradeBand {
	bands := make([]FITGradeBand, len(gradeBandEdges))
	power := make([]fitAccumulator, len(gradeBandEdges))
	cadence := make([]fitAccumulator, len(gradeBandEdges))
	heart := make([]fitAccumulator, len(gradeBandEdges))

	for index := range records {
		grade, ok := gradeAt(records, index)
		if !ok {
			continue
		}
		slot := gradeBandIndex(grade)
		bands[slot].Samples++
		if index > 0 {
			bands[slot].Seconds += sampleSeconds(records[index-1].Time, records[index].Time)
		}
		power[slot].add(records[index].Power)
		cadence[slot].add(records[index].Cadence)
		heart[slot].add(records[index].HeartRate)
	}
	return collectBands(bands, power, cadence, heart)
}

// collectBands names each band and drops the ones the ride never entered.
func collectBands(bands []FITGradeBand, power, cadence, heart []fitAccumulator) []FITGradeBand {
	out := make([]FITGradeBand, 0, len(bands))
	for slot := range bands {
		if bands[slot].Samples == 0 {
			continue
		}
		bands[slot].Label = gradeBandEdges[slot].label
		bands[slot].AvgPower = power[slot].mean()
		bands[slot].AvgCadence = cadence[slot].mean()
		bands[slot].AvgHeartRate = heart[slot].mean()
		out = append(out, bands[slot])
	}
	return out
}

// gradeBandIndex is the band one grade falls into.
func gradeBandIndex(grade float64) int {
	for index, band := range gradeBandEdges {
		if grade < band.upper {
			return index
		}
	}
	return len(gradeBandEdges) - 1
}

// A FITTemperature is the hot-against-cool comparison of one ride.
type FITTemperature struct {
	OK            bool
	Samples       int
	HotAvgC       float64
	CoolAvgC      float64
	HotHeartRate  FITNumber
	HotPower      FITNumber
	CoolHeartRate FITNumber
	CoolPower     FITNumber
}

// minTemperatureSamples is the smallest stream a hot-cool split says anything about.
const minTemperatureSamples = 8

// temperatureSplit compares the warmest quarter of the ride with the coolest.
func temperatureSplit(records []FITRecord) FITTemperature {
	sorted := make([]FITRecord, 0, len(records))
	for _, record := range records {
		if record.Temperature.OK {
			sorted = append(sorted, record)
		}
	}
	if len(sorted) < minTemperatureSamples {
		return FITTemperature{}
	}
	sort.SliceStable(sorted, func(a, b int) bool {
		return sorted[a].Temperature.Value < sorted[b].Temperature.Value
	})

	quarter := max(len(sorted)/4, 1)
	cool := summarizeTemperature(sorted[:quarter])
	hot := summarizeTemperature(sorted[len(sorted)-quarter:])
	return FITTemperature{
		OK:            true,
		Samples:       len(sorted),
		HotAvgC:       hot.degrees,
		CoolAvgC:      cool.degrees,
		HotHeartRate:  hot.heart,
		HotPower:      hot.power,
		CoolHeartRate: cool.heart,
		CoolPower:     cool.power,
	}
}

// A temperatureSlice is the summary of one end of the temperature range.
type temperatureSlice struct {
	degrees float64
	heart   FITNumber
	power   FITNumber
}

// summarizeTemperature averages one end of the temperature range.
func summarizeTemperature(records []FITRecord) temperatureSlice {
	var degrees, heart, power fitAccumulator
	for _, record := range records {
		degrees.add(record.Temperature)
		heart.add(record.HeartRate)
		power.add(record.Power)
	}
	return temperatureSlice{degrees: degrees.mean().Value, heart: heart.mean(), power: power.mean()}
}

// A FITDrift is the aerobic decoupling of one ride. Percent is
// (first - second) / first * 100, positive when the ratio fell; upstream computes the
// inverse under the same name, so do not flip the sign. See docs/parity.md.
type FITDrift struct {
	OK          bool
	Seconds     float64
	FirstRatio  float64
	SecondRatio float64
	Percent     float64
}

// Decoupling bounds: the shortest ride it is reported for, and the smallest paired
// stream it can be computed from.
const (
	minDriftSeconds = 3600.0
	minDriftSamples = 4
)

// heartRateDrift compares the power-to-heart-rate ratio of the two halves.
func heartRateDrift(records []FITRecord) FITDrift {
	paired := make([]FITRecord, 0, len(records))
	for _, record := range records {
		if record.Power.OK && record.HeartRate.OK && record.HeartRate.Value > 0 {
			paired = append(paired, record)
		}
	}
	if len(paired) < minDriftSamples {
		return FITDrift{}
	}
	seconds := paired[len(paired)-1].Time.Sub(paired[0].Time).Seconds()
	if seconds < minDriftSeconds {
		return FITDrift{}
	}

	middle := len(paired) / 2
	first, firstOK := ratioOf(paired[:middle])
	second, secondOK := ratioOf(paired[middle:])
	if !firstOK || !secondOK || first <= 0 {
		return FITDrift{}
	}
	return FITDrift{
		OK:          true,
		Seconds:     seconds,
		FirstRatio:  first,
		SecondRatio: second,
		Percent:     (first - second) / first * percent,
	}
}

// ratioOf is the average power over the average heart rate of one half.
func ratioOf(records []FITRecord) (float64, bool) {
	var power, heart fitAccumulator
	for _, record := range records {
		power.add(record.Power)
		heart.add(record.HeartRate)
	}
	average, beats := power.mean(), heart.mean()
	if !average.OK || !beats.OK || beats.Value <= 0 {
		return 0, false
	}
	return average.Value / beats.Value, true
}
