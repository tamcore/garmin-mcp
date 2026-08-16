package api

import "time"

// This file folds the per-session profile summaries into the whole-activity one.

// overallSpan folds every session's profile summary into one span, leaving the window
// zero so the derived half still covers the whole record stream.
//
// A figure folds only when every session carried it; otherwise it is absent and
// analyzeSegment's record-derived value stands. Calories is the one field with no
// such fallback, so there absence is final. See docs/parity.md.
func overallSpan(sessions []FITSpan) FITSpan {
	if len(sessions) == 0 {
		return FITSpan{}
	}
	if len(sessions) == 1 {
		// Taken exactly: a weighted mean would round a whole-number heart rate into
		// a fraction of a beat.
		only := sessions[0]
		only.Start, only.End = time.Time{}, time.Time{}
		return only
	}

	var fold sessionFold
	for _, session := range sessions {
		fold.add(session)
	}
	return fold.span(sessions[0].Sport)
}

// A sessionFold accumulates every whole-activity figure across the sessions.
type sessionFold struct {
	elapsed, distance, ascent, descent, calories fitTotal
	maxPower, maxCadence, maxHeart               fitPeak
	power, cadence, heart, normalized            fitWeighted
}

// add folds one session in.
func (f *sessionFold) add(session FITSpan) {
	f.elapsed.add(session.Elapsed)
	f.distance.add(session.Distance)
	f.ascent.add(session.Ascent)
	f.descent.add(session.Descent)
	f.calories.add(session.Calories)
	f.maxPower.add(session.MaxPower)
	f.maxCadence.add(session.MaxCadence)
	f.maxHeart.add(session.MaxHeartRate)

	weight := sessionWeight(session)
	f.power.add(session.AvgPower, weight)
	f.cadence.add(session.AvgCadence, weight)
	f.heart.add(session.AvgHeartRate, weight)
	f.normalized.add(session.NormalizedPw, weight)
}

// span reports the folded summary.
func (f sessionFold) span(sport string) FITSpan {
	return FITSpan{
		Sport:        sport,
		Elapsed:      f.elapsed.value(),
		Distance:     f.distance.value(),
		Ascent:       f.ascent.value(),
		Descent:      f.descent.value(),
		Calories:     f.calories.value(),
		MaxPower:     f.maxPower.value(),
		MaxCadence:   f.maxCadence.value(),
		MaxHeartRate: f.maxHeart.value(),
		AvgPower:     f.power.mean(),
		AvgCadence:   f.cadence.mean(),
		AvgHeartRate: f.heart.mean(),
		NormalizedPw: f.normalized.mean(),
	}
}

// A fitTotal adds one figure across the sessions, absent unless every session
// carried it.
type fitTotal struct {
	sum     float64
	carried bool
	missing bool
}

func (t *fitTotal) add(value FITNumber) {
	if !value.OK {
		t.missing = true
		return
	}
	t.sum, t.carried = t.sum+value.Value, true
}

func (t fitTotal) value() FITNumber {
	if t.missing || !t.carried {
		return FITNumber{}
	}
	return fitNumber(t.sum)
}

// A fitPeak keeps the greatest reading across the sessions, under the same rule: the
// greatest of a subset is a lower bound, not a maximum.
type fitPeak struct {
	peak    FITNumber
	missing bool
}

func (p *fitPeak) add(value FITNumber) {
	if !value.OK {
		p.missing = true
		return
	}
	if !p.peak.OK || value.Value > p.peak.Value {
		p.peak = value
	}
}

func (p fitPeak) value() FITNumber {
	if p.missing {
		return FITNumber{}
	}
	return p.peak
}

// sessionWeight is how much one session counts toward a weighted average. A session
// whose elapsed time is missing still counts, as one unit rather than as nothing.
func sessionWeight(session FITSpan) float64 {
	if session.Elapsed.OK && session.Elapsed.Value > 0 {
		return session.Elapsed.Value
	}
	return 1
}

// A fitWeighted is a weighted mean of optional readings, under the same
// every-session rule.
type fitWeighted struct {
	sum     float64
	weight  float64
	missing bool
}

// add folds one reading in at its weight.
func (w *fitWeighted) add(value FITNumber, weight float64) {
	if !value.OK {
		w.missing = true
		return
	}
	if weight <= 0 {
		return
	}
	w.sum += value.Value * weight
	w.weight += weight
}

// mean reports the weighted mean, absent when a session carried none.
func (w fitWeighted) mean() FITNumber {
	if w.missing || w.weight == 0 {
		return FITNumber{}
	}
	return fitNumber(w.sum / w.weight)
}
