package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// loadDay is one synthetic day of the aggregated training status: two devices, the
// primary one carrying the loads, plus the VO2 max the same document holds.
const loadDay = `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
	`"1001":{"primaryTrainingDevice":false,"trainingStatusFeedbackPhrase":"OTHER"},` +
	`"3001":{"primaryTrainingDevice":true,"trainingStatus":3,` +
	`"trainingStatusFeedbackPhrase":"PRODUCTIVE_1","fitnessTrend":1,` +
	`"acuteTrainingLoadDTO":{"dailyTrainingLoadAcute":320.5,` +
	`"dailyTrainingLoadChronic":300.5,"dailyAcuteChronicWorkloadRatio":1.07,` +
	`"acwrStatus":"OPTIMAL","acwrPercent":63,"minTrainingLoadChronic":250.0,` +
	`"maxTrainingLoadChronic":420.0}}}},` +
	`"mostRecentVO2Max":{"generic":{"vo2MaxValue":52.5}}}`

func loadTrendScript() testkit.Script {
	return trendDailyScript(client.PathTrainingStatusPrefix, allTrendDays(), loadDay)
}

func TestTrainingLoadTrendReportsTheChartAndComputesTheBalance(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, loadTrendScript())
	out, err := h.svc.readTrainingLoadTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadTrend() = %v", err)
	}

	if len(out.Trend) != 3 || out.DaysWithData != 3 {
		t.Fatalf("trend = %+v, want three days", out.Trend)
	}
	assertLoadPoint(t, out.Trend[0])
}

// assertLoadPoint checks one rendered day against the fixture above.
func assertLoadPoint(t *testing.T, point TrainingLoadPoint) {
	t.Helper()

	if point.ATL == nil || *point.ATL != 320.5 || point.CTL == nil || *point.CTL != 300.5 {
		t.Fatalf("point = %+v, want the primary device's loads", point)
	}
	if point.TSB == nil || *point.TSB != -20 {
		t.Errorf("tsb = %v, want chronic minus acute", point.TSB)
	}
	if point.ACWRStatus != "OPTIMAL" || point.ACWR == nil {
		t.Errorf("acwr = %v/%q, want Garmin's ratio and label", point.ACWR, point.ACWRStatus)
	}
	if point.OptimalChronicLoadMin == nil || point.OptimalChronicLoadMax == nil {
		t.Error("the optimal chronic range did not reach the result")
	}
	assertLoadPointLabels(t, point)
}

// assertLoadPointLabels checks the phrases and the VO2 max the same document carried.
func assertLoadPointLabels(t *testing.T, point TrainingLoadPoint) {
	t.Helper()

	if point.TrainingStatus != "PRODUCTIVE_1" || point.TrainingStatusCode != "3" {
		t.Errorf("status = %q/%q, want the primary device's",
			point.TrainingStatus, point.TrainingStatusCode)
	}
	if point.FitnessTrend != "1" {
		t.Errorf("fitness_trend = %q, want the value as Garmin sent it", point.FitnessTrend)
	}
	if point.VO2Max == nil || *point.VO2Max != 52.5 {
		t.Errorf("vo2_max = %v, want 52.5", point.VO2Max)
	}
}

// TestTrainingLoadTrendOmitsTheBalanceWhenAnInputIsMissing keeps a derived figure from
// being invented out of half its inputs.
func TestTrainingLoadTrendOmitsTheBalanceWhenAnInputIsMissing(t *testing.T) {
	t.Parallel()

	partial := `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{"3001":{` +
		`"primaryTrainingDevice":true,"acuteTrainingLoadDTO":{` +
		`"dailyTrainingLoadAcute":320.5}}}}}`
	h := newTrendHarness(t, trendDailyScript(client.PathTrainingStatusPrefix,
		allTrendDays(), partial))

	out, err := h.svc.readTrainingLoadTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadTrend() = %v", err)
	}
	if len(out.Trend) == 0 {
		t.Fatal("the trend is empty, want the acute load to count as data")
	}
	if out.Trend[0].TSB != nil {
		t.Errorf("tsb = %v, want none without a chronic load", *out.Trend[0].TSB)
	}
}

func TestTrainingLoadTrendMarksAPartialWindow(t *testing.T) {
	t.Parallel()

	script := loadTrendScript().With(client.PathTrainingStatusPrefix+"/"+trendEnd,
		testkit.JSON(http.StatusInternalServerError, `{"error":"synthetic"}`))
	h := newTrendHarness(t, script)

	out, err := h.svc.readTrainingLoadTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadTrend() = %v", err)
	}
	if len(out.Trend) != 2 || out.Coverage.Complete || out.Coverage.DaysFailed != 1 {
		t.Errorf("result = %+v, want two days and a named failure", out.Coverage)
	}
}

func TestTrainingLoadTrendCountsADayWithNoStatus(t *testing.T) {
	t.Parallel()

	script := loadTrendScript().With(client.PathTrainingStatusPrefix+"/"+trendMid,
		testkit.JSON(http.StatusOK, `{"mostRecentTrainingStatus":null}`))
	h := newTrendHarness(t, script)

	out, err := h.svc.readTrainingLoadTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadTrend() = %v", err)
	}
	if out.Coverage.DaysWithoutData != 1 || !out.Coverage.Complete {
		t.Errorf("coverage = %+v, want one empty day and a complete window", out.Coverage)
	}
}

func TestTrainingLoadTrendRefusesAnOversizedWindowAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, loadTrendScript())
	if _, err := h.svc.readTrainingLoadTrend(h.ctx, "2026-01-01",
		"2026-05-01"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("a 121-day window = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.readTrainingLoadTrend(t.Context(), trendStart, trendEnd); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestTrainingLoadTrendResultNeverLogsALoad(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, loadTrendScript())
	out, err := h.svc.readTrainingLoadTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadTrend() = %v", err)
	}
	assertShapeOnly(t, "TrainingLoadTrend", out, "320.5", "300.5", "52.5", "PRODUCTIVE_1")
}

// TestTrainingLoadTrendKeepsADayCarryingOnlyTheFitnessTrend is the regression for a
// day dropped despite carrying a reading.
//
// The presence test named every field except FitnessTrend, so a day whose only
// reading was that trend was rendered, then discarded, then counted as a day with no
// data — while the field it did carry was already populated.
func TestTrainingLoadTrendKeepsADayCarryingOnlyTheFitnessTrend(t *testing.T) {
	t.Parallel()

	trendOnly := `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
		`"3002":{"calendarDate":"` + trendEnd + `","fitnessTrend":1}}}}`
	script := trendDailyScript(client.PathTrainingStatusPrefix, allTrendDays(), trendOnly)

	h := newTrendHarness(t, script)
	out, err := h.svc.readTrainingLoadTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadTrend() = %v", err)
	}

	if out.DaysWithData == 0 {
		t.Fatal("every day carried a fitness trend and all were counted as empty")
	}
	if len(out.Trend) == 0 {
		t.Fatal("the trend is empty although each day carried a reading")
	}
	if out.Trend[0].FitnessTrend == "" {
		t.Error("the day was kept but its only reading was not rendered")
	}
}
