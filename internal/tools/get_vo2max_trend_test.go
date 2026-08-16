package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// maxMetricsPath is the range path for the whole test window.
func maxMetricsPath() string {
	return client.PathMaxMetricsPrefix + "/" + trendStart + "/" + trendEnd
}

// vo2RangeBody covers the window in one response: two days that differ and one that
// repeats the previous value.
const vo2RangeBody = `[{"generic":{"calendarDate":"2026-01-29","vo2MaxValue":51.5}},` +
	`{"generic":{"calendarDate":"2026-01-30","vo2MaxValue":51.5}},` +
	`{"generic":{"calendarDate":"2026-01-31","vo2MaxValue":52.5}}]`

func vo2Script(rangeBody string) testkit.Script {
	return testkit.NewScript().With(maxMetricsPath(), testkit.JSON(http.StatusOK, rangeBody))
}

func TestVO2MaxTrendReadsTheWholeWindowInOneRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, vo2Script(vo2RangeBody))
	out, err := h.svc.readVO2MaxTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readVO2MaxTrend() = %v", err)
	}

	if got := len(h.fake.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want one covering the window", got)
	}
	if out.DaysWithData != 3 {
		t.Errorf("days_with_data = %d, want 3", out.DaysWithData)
	}
	// The repeated day is dropped from the trend but still counted above, so the
	// shorter list loses no information.
	if out.DataPoints != 2 || len(out.Trend) != 2 {
		t.Fatalf("trend = %+v, want the two change points", out.Trend)
	}
	if out.Sport != sportRunning {
		t.Errorf("sport = %q, want running", out.Sport)
	}
	if out.Trend[0].Source != sourceMaxMetrics {
		t.Errorf("source = %q, want the range read", out.Trend[0].Source)
	}
	if out.Change == nil || *out.Change != 1 {
		t.Errorf("change = %v, want 1", out.Change)
	}
	if out.Current != nil {
		t.Error("the profile estimate was reported even though the window held history")
	}
}

// TestVO2MaxTrendFallsBackToTheDailyStatusForUncoveredDays proves the per-day read
// only runs for days the one-request path did not answer.
func TestVO2MaxTrendFallsBackToTheDailyStatusForUncoveredDays(t *testing.T) {
	t.Parallel()

	partialRange := `[{"generic":{"calendarDate":"2026-01-29","vo2MaxValue":51.5}}]`
	script := vo2Script(partialRange)
	for _, day := range []string{trendMid, trendEnd} {
		script = script.With(client.PathTrainingStatusPrefix+"/"+day,
			testkit.JSON(http.StatusOK,
				`{"mostRecentVO2Max":{"generic":{"vo2MaxValue":53.5}}}`))
	}
	h := newTrendHarness(t, script)

	out, err := h.svc.readVO2MaxTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readVO2MaxTrend() = %v", err)
	}
	if got := len(h.fake.Requests()); got != 3 {
		t.Errorf("the fake received %d requests, want the range plus the two uncovered days", got)
	}
	if out.DaysWithData != 3 {
		t.Errorf("days_with_data = %d, want 3", out.DaysWithData)
	}
	if len(out.Trend) != 2 || out.Trend[1].Source != sourceTrainingStatus {
		t.Errorf("trend = %+v, want the fallback source named", out.Trend)
	}
}

// TestVO2MaxTrendSurvivesAFailedRangeRead keeps the cheap path from being fatal.
func TestVO2MaxTrendSurvivesAFailedRangeRead(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(maxMetricsPath(),
		testkit.JSON(http.StatusInternalServerError, `{"error":"synthetic"}`))
	for _, day := range allTrendDays() {
		script = script.With(client.PathTrainingStatusPrefix+"/"+day,
			testkit.JSON(http.StatusOK,
				`{"mostRecentVO2Max":{"generic":{"vo2MaxValue":50.5}}}`))
	}
	h := newTrendHarness(t, script)

	out, err := h.svc.readVO2MaxTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readVO2MaxTrend() = %v", err)
	}
	if out.DaysWithData != 3 || len(out.Trend) != 1 {
		t.Errorf("result = %+v, want three days collapsing to one change point", out)
	}
}

// TestVO2MaxTrendReportsTheProfileEstimateSeparately is the documented fallback: it is
// reported outside the trend and labelled, never as a historical point.
func TestVO2MaxTrendReportsTheProfileEstimateSeparately(t *testing.T) {
	t.Parallel()

	script := vo2Script(`[]`).With(client.PathUserSettings,
		testkit.JSON(http.StatusOK, `{"userData":{"vo2MaxRunning":49.5}}`))
	for _, day := range allTrendDays() {
		script = script.With(client.PathTrainingStatusPrefix+"/"+day,
			testkit.JSON(http.StatusOK, `{"mostRecentVO2Max":null}`))
	}
	h := newTrendHarness(t, script)

	out, err := h.svc.readVO2MaxTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readVO2MaxTrend() = %v", err)
	}
	if len(out.Trend) != 0 {
		t.Fatalf("trend = %+v, want it empty", out.Trend)
	}
	if out.Current == nil || out.Current.VO2Max != 49.5 {
		t.Fatalf("current estimate = %+v, want the profile value", out.Current)
	}
	if out.Current.Source != sourceUserSettings || out.Note == "" {
		t.Errorf("current = %+v, note %q, want the source and the caveat named",
			out.Current, out.Note)
	}
}

func TestVO2MaxTrendReportsNoEstimateWhenTheProfileHasNone(t *testing.T) {
	t.Parallel()

	script := vo2Script(`[]`).With(client.PathUserSettings,
		testkit.JSON(http.StatusOK, `{"userData":null}`))
	for _, day := range allTrendDays() {
		script = script.With(client.PathTrainingStatusPrefix+"/"+day,
			testkit.JSON(http.StatusOK, `{"mostRecentVO2Max":null}`))
	}
	h := newTrendHarness(t, script)

	out, err := h.svc.readVO2MaxTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readVO2MaxTrend() = %v", err)
	}
	if out.Current != nil || out.Note != "" {
		t.Errorf("result = %+v, want no estimate and no note", out)
	}
	if !out.Coverage.Complete {
		t.Errorf("coverage = %+v, want a complete window of empty days", out.Coverage)
	}
}

func TestVO2MaxTrendPrefersTheSportWithBetterCoverage(t *testing.T) {
	t.Parallel()

	body := `[{"generic":{"calendarDate":"2026-01-29","vo2MaxValue":51.5}},` +
		`{"cycling":{"calendarDate":"2026-01-30","vo2MaxValue":44.5}},` +
		`{"cycling":{"calendarDate":"2026-01-31","vo2MaxValue":45.5}}]`
	h := newTrendHarness(t, vo2Script(body))

	out, err := h.svc.readVO2MaxTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readVO2MaxTrend() = %v", err)
	}
	if out.Sport != sportCycling {
		t.Errorf("sport = %q, want the sport with two days", out.Sport)
	}
}

func TestVO2MaxTrendRefusesAnOversizedWindowAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, vo2Script(vo2RangeBody))
	if _, err := h.svc.readVO2MaxTrend(h.ctx, "2026-01-01", "2026-05-01"); !errors.Is(
		err, ErrInvalidArgument) {
		t.Errorf("a 121-day window = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.readVO2MaxTrend(t.Context(), trendStart, trendEnd); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestVO2MaxTrendResultNeverLogsAnEstimate(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, vo2Script(vo2RangeBody))
	out, err := h.svc.readVO2MaxTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readVO2MaxTrend() = %v", err)
	}
	assertShapeOnly(t, "VO2MaxTrend", out, "51.5", "52.5")
}

// TestVO2MaxTrendDropsRangeDaysOutsideTheWindow is the regression for a range read
// that answered with more than it was asked for.
//
// The one-request path recorded every dated section Garmin returned, so a response
// carrying days outside the requested window put them in the trend and counted them
// in days_with_data. The window is the caller's question and the declared bound; an
// answer may be narrower than it, never wider.
func TestVO2MaxTrendDropsRangeDaysOutsideTheWindow(t *testing.T) {
	t.Parallel()

	// Two days sit inside the window; the other three do not, one before it, one
	// after it, and one a whole year away.
	wideBody := `[{"generic":{"calendarDate":"2025-12-01","vo2MaxValue":40}},` +
		`{"generic":{"calendarDate":"2026-01-29","vo2MaxValue":51.5}},` +
		`{"generic":{"calendarDate":"2026-01-31","vo2MaxValue":52.5}},` +
		`{"generic":{"calendarDate":"2026-03-01","vo2MaxValue":60}},` +
		`{"generic":{"calendarDate":"2027-01-30","vo2MaxValue":70}}]`

	h := newTrendHarness(t, vo2Script(wideBody).With(
		client.PathTrainingStatusPrefix+"/"+trendMid,
		testkit.JSON(http.StatusOK, `{}`),
	))
	out, err := h.svc.readVO2MaxTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readVO2MaxTrend() = %v", err)
	}

	if out.DaysWithData != 2 {
		t.Errorf("days_with_data = %d, want the 2 days inside the window", out.DaysWithData)
	}
	for _, point := range out.Trend {
		if point.Date < trendStart || point.Date > trendEnd {
			t.Errorf("trend carries %s, which is outside %s..%s", point.Date, trendStart, trendEnd)
		}
	}
}
