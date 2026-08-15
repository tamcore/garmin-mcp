package api

import "testing"

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
		"ascent":          {span.Ascent, 250},
		"calories":        {span.Calories, 1000},
		"peak power":      {span.MaxPower, 600},
		"peak heart":      {span.MaxHeartRate, 190},
		"average power":   {span.AvgPower, (200*3000 + 400*1000) / 4000.0},
		"average heart":   {span.AvgHeartRate, (140*3000 + 160*1000) / 4000.0},
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
