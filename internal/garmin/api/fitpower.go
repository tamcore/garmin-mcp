package api

import "math"

// This file is the power analysis the FIT profile does not carry: the power duration
// curve over arbitrary windows, and normalized power over a segment the device never
// summarized. A device writes its own normalized power for a session and a lap, and
// that figure is preferred where it exists; these functions are what answers the
// question for every other window.

// The rolling-window definitions the normalized-power figure is computed from.
const (
	// maxSeriesSeconds bounds the per-second series one activity is rendered into.
	maxSeriesSeconds = 24 * 60 * 60

	// normalizedWindow is the rolling window of the normalized-power definition.
	normalizedWindow = 30

	// normalizedExponent is the fourth-power weighting of that definition.
	normalizedExponent = 4.0
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
