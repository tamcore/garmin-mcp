package api

import "time"

// This file folds the per-session profile summaries into the whole-activity one.
//
// The whole-activity figures used to be derived from the record stream, and the
// derived ascent came out at roughly twice what the device reported. A device writes
// a summary per session, so the honest whole-activity figure is the fold of those
// summaries rather than a second, worse computation over the samples.

// overallSpan folds every session's profile summary into one span.
//
// The window stays zero on purpose, so the derived half of the whole-activity
// segment still covers the entire record stream and its sample count still counts
// every sample. What the fold contributes is the summary: totals add, peaks take the
// maximum, and averages are weighted by the elapsed time of the session they came
// from. One session — which is every ordinary activity — reproduces that session's
// figures exactly.
func overallSpan(sessions []FITSpan) FITSpan {
	if len(sessions) == 0 {
		return FITSpan{}
	}
	if len(sessions) == 1 {
		// The ordinary case, taken exactly rather than through a weighted mean that
		// would round a whole-number heart rate into a fraction of a beat.
		only := sessions[0]
		only.Start, only.End = time.Time{}, time.Time{}
		return only
	}

	span := FITSpan{Sport: sessions[0].Sport}
	var power, cadence, heart, normalized fitWeighted
	for _, session := range sessions {
		span.Elapsed = sumOf(span.Elapsed, session.Elapsed)
		span.Distance = sumOf(span.Distance, session.Distance)
		span.Ascent = sumOf(span.Ascent, session.Ascent)
		span.Calories = sumOf(span.Calories, session.Calories)
		span.MaxPower = largerOf(span.MaxPower, session.MaxPower)
		span.MaxHeartRate = largerOf(span.MaxHeartRate, session.MaxHeartRate)

		weight := sessionWeight(session)
		power.add(session.AvgPower, weight)
		cadence.add(session.AvgCadence, weight)
		heart.add(session.AvgHeartRate, weight)
		normalized.add(session.NormalizedPw, weight)
	}

	span.AvgPower, span.AvgCadence = power.mean(), cadence.mean()
	span.AvgHeartRate, span.NormalizedPw = heart.mean(), normalized.mean()
	return span
}

// sessionWeight is how much one session counts toward a weighted average. A session
// whose elapsed time is missing still counts, as one unit rather than as nothing.
func sessionWeight(session FITSpan) float64 {
	if session.Elapsed.OK && session.Elapsed.Value > 0 {
		return session.Elapsed.Value
	}
	return 1
}

// sumOf adds two optional readings, and is absent only when both are.
func sumOf(total, next FITNumber) FITNumber {
	if !next.OK {
		return total
	}
	if !total.OK {
		return next
	}
	return fitNumber(total.Value + next.Value)
}

// largerOf keeps the greater of two optional readings.
func largerOf(peak, next FITNumber) FITNumber {
	if !next.OK {
		return peak
	}
	if !peak.OK || next.Value > peak.Value {
		return next
	}
	return peak
}

// A fitWeighted is a weighted mean of optional readings.
type fitWeighted struct {
	sum    float64
	weight float64
}

// add folds one reading in at its weight.
func (w *fitWeighted) add(value FITNumber, weight float64) {
	if !value.OK || weight <= 0 {
		return
	}
	w.sum += value.Value * weight
	w.weight += weight
}

// mean reports the weighted mean, or an absent reading when nothing was folded in.
func (w fitWeighted) mean() FITNumber {
	if w.weight == 0 {
		return FITNumber{}
	}
	return fitNumber(w.sum / w.weight)
}
