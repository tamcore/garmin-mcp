package api

import (
	"testing"
	"time"
)

// TestOverallSpanFoldsEverySession proves the whole-activity summary of a multisport
// file adds the totals, keeps the peaks and weights the averages by elapsed time,
// rather than reporting the first session as if it were the whole activity.
func TestOverallSpanFoldsEverySession(t *testing.T) {
	t.Parallel()

	span := overallSpan([]FITSpan{
		{
			Sport: sportCyclingName, Elapsed: fitNumber(3000), Distance: fitNumber(30000),
			Ascent: fitNumber(200), Calories: fitNumber(700),
			AvgPower: fitNumber(200), MaxPower: fitNumber(600),
			AvgHeartRate: fitNumber(140), MaxHeartRate: fitNumber(180),
			AvgCadence: fitNumber(85), NormalizedPw: fitNumber(210),
		},
		{
			Sport: sportRunningName, Elapsed: fitNumber(1000), Distance: fitNumber(5000),
			Ascent: fitNumber(50), Calories: fitNumber(300),
			AvgPower: fitNumber(400), MaxPower: fitNumber(500),
			AvgHeartRate: fitNumber(160), MaxHeartRate: fitNumber(190),
			AvgCadence: fitNumber(90), NormalizedPw: fitNumber(410),
		},
	})

	for name, got := range map[string]struct {
		reading FITNumber
		want    float64
	}{
		"elapsed":         {span.Elapsed, 4000},
		"distance":        {span.Distance, 35000},
		nameAscent:        {span.Ascent, 250},
		"calories":        {span.Calories, 1000},
		namePeakPow:       {span.MaxPower, 600},
		"peak heart":      {span.MaxHeartRate, 190},
		"average power":   {span.AvgPower, (200*3000 + 400*1000) / 4000.0},
		nameAvgHeart:      {span.AvgHeartRate, (140*3000 + 160*1000) / 4000.0},
		"average cadence": {span.AvgCadence, (85*3000 + 90*1000) / 4000.0},
		"normalized":      {span.NormalizedPw, (210*3000 + 410*1000) / 4000.0},
	} {
		if !got.reading.OK || got.reading.Value != got.want {
			t.Errorf("%s = %+v, want %v", name, got.reading, got.want)
		}
	}
	if span.Sport != "cycling" {
		t.Errorf("sport = %q, want the first session's", span.Sport)
	}
	if !span.Start.IsZero() || !span.End.IsZero() {
		t.Error("the folded span carries a window, want it left to the record stream")
	}
}

// TestOverallSpanOfOneSessionIsThatSession keeps the ordinary case exact: an
// activity with a single session reports that session's figures unchanged.
func TestOverallSpanOfOneSessionIsThatSession(t *testing.T) {
	t.Parallel()

	only := FITSpan{
		Sport: sportRunningName, Elapsed: fitNumber(3473.61), Distance: fitNumber(11839.05),
		Ascent: fitNumber(63), AvgHeartRate: fitNumber(169), MaxHeartRate: fitNumber(191),
	}
	span := overallSpan([]FITSpan{only})

	if span.Elapsed != only.Elapsed || span.Distance != only.Distance ||
		span.Ascent != only.Ascent || span.AvgHeartRate != only.AvgHeartRate ||
		span.MaxHeartRate != only.MaxHeartRate || span.Sport != only.Sport {
		t.Errorf("overallSpan() = %+v, want the single session unchanged", span)
	}
}

// TestOverallSpanWithoutSessionsIsEmpty proves a file that carries no session
// message leaves every figure to the record-derived fallback.
func TestOverallSpanWithoutSessionsIsEmpty(t *testing.T) {
	t.Parallel()

	if span := overallSpan(nil); span != (FITSpan{}) {
		t.Errorf("overallSpan(nil) = %+v, want the zero span", span)
	}
}

// TestOverallSpanRefusesAFoldOverASubsetOfSessions proves the every-session rule:
// when the sessions disagree in provenance the folded figure is absent, so the
// whole-activity result falls back to the complete record-derived value instead of
// serving a total or a peak that silently covers only part of the activity.
//
// A partial sum is the defect this pins. Folding a 200 metre ascent from one session
// while the other carried none reports 200 for the whole activity, and nothing in
// the result says a session is missing from it. A peak fails the same way for its
// own reason: the greatest of a subset is a lower bound, not a maximum.
func TestOverallSpanRefusesAFoldOverASubsetOfSessions(t *testing.T) {
	t.Parallel()

	span := overallSpan([]FITSpan{
		{
			Sport: sportCyclingName, Elapsed: fitNumber(3000),
			Ascent: fitNumber(200), Descent: fitNumber(180),
			MaxCadence: fitNumber(96), MaxPower: fitNumber(600),
			AvgHeartRate: fitNumber(140),
		},
		// The same activity's second session, written by a device that recorded no
		// summary of its own.
		{Sport: sportRunningName, Elapsed: fitNumber(1000)},
	})

	for name, reading := range map[string]FITNumber{
		nameAscent:     span.Ascent,
		nameDescent:    span.Descent,
		"peak cadence": span.MaxCadence,
		namePeakPow:    span.MaxPower,
		nameAvgHeart:   span.AvgHeartRate,
	} {
		if reading.OK {
			t.Errorf("%s = %+v, want absence rather than a fold over one session of two",
				name, reading)
		}
	}
	// Elapsed is carried by both sessions, so it still folds. The rule is about the
	// sessions disagreeing, not about refusing to fold at all.
	if !span.Elapsed.OK || span.Elapsed.Value != 4000 {
		t.Errorf("elapsed = %+v, want the 4000 both sessions carried", span.Elapsed)
	}
}

// TestOverallSpanCountsASessionWithoutAnElapsedTime proves a session that reports no
// elapsed time still contributes to the averages instead of vanishing from them.
func TestOverallSpanCountsASessionWithoutAnElapsedTime(t *testing.T) {
	t.Parallel()

	span := overallSpan([]FITSpan{
		{AvgPower: fitNumber(100)},
		{AvgPower: fitNumber(300)},
	})
	if !span.AvgPower.OK || span.AvgPower.Value != 200 {
		t.Errorf("average power = %+v, want the unweighted mean of 200", span.AvgPower)
	}
	if span.Elapsed.OK {
		t.Errorf("elapsed = %+v, want none", span.Elapsed)
	}
}

// TestMixedSessionCaloriesStayVisiblePerSession pins the one folded field with no
// derived route: absence is final at the whole-activity level, and each session keeps
// its own figure. See docs/parity.md.
func TestMixedSessionCaloriesStayVisiblePerSession(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC)
	records := make([]FITRecord, 0, 120)
	for second := range 120 {
		records = append(records, FITRecord{
			Time:     base.Add(time.Duration(second) * time.Second),
			Power:    fitNumber(200),
			Altitude: fitNumber(100 + float64(second)),
		})
	}
	activity := FITActivity{
		Records: records,
		Sessions: []FITSpan{
			{
				Start: base, End: base.Add(60 * time.Second), Elapsed: fitNumber(60),
				Calories: fitNumber(700), Ascent: fitNumber(40),
			},
			// The second session's device wrote no summary figures of its own.
			{
				Start:   base.Add(60 * time.Second),
				End:     base.Add(119 * time.Second),
				Elapsed: fitNumber(59),
			},
		},
	}

	summary, err := AnalyzeFIT(t.Context(), activity)
	if err != nil {
		t.Fatalf("AnalyzeFIT() = %v", err)
	}

	if summary.Overall.Calories.OK {
		t.Errorf("overall calories = %+v, want absence: a partial sum would be a total "+
			"that silently covers one session of two", summary.Overall.Calories)
	}
	// The gap is visible rather than lost: the session that reported calories still
	// reports them, and the one that did not still says so.
	if len(summary.Sessions) != 2 {
		t.Fatalf("%d session summaries, want 2", len(summary.Sessions))
	}
	if !summary.Sessions[0].Calories.OK || summary.Sessions[0].Calories.Value != 700 {
		t.Errorf("first session calories = %+v, want the 700 it carried",
			summary.Sessions[0].Calories)
	}
	if summary.Sessions[1].Calories.OK {
		t.Errorf("second session calories = %+v, want absence", summary.Sessions[1].Calories)
	}
	// The contrast that makes calories the exception: ascent disagreed in exactly the
	// same way and still reports, because the record stream can answer for it.
	if !summary.Overall.Ascent.OK {
		t.Error("overall ascent = absent, want the record-derived fallback")
	}
}
