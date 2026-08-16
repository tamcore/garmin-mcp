package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture here is synthetic. The scores, thresholds and ages are invented, and
// no fixture is a recording of a real account.

const (
	scoresWindowStart = "2026-01-01"
	scoresWindowEnd   = testCalendarDate
)

// hillScoreBody carries a numeric string where a number is expected and a null score,
// because both are shapes a tolerant decoder has to survive.
const hillScoreBody = `{"periodAvgScore":{"7":63.5},"maxScore":"71",` +
	`"hillScoreDTOList":[{"calendarDate":"` + testCalendarDate + `","overallScore":68,` +
	`"strengthScore":"51","enduranceScore":null,"hillScoreClassificationId":3}]}`

const enduranceScoreBody = `{"avg":7100,"max":7400,"enduranceScoreDTO":{` +
	`"calendarDate":"` + testCalendarDate + `","overallScore":7250,"classification":4,` +
	`"classificationLowerLimitTrained":6000,"classificationLowerLimitElite":9500,` +
	`"contributors":[{"activityTypeId":1,"contribution":72.456}]},` +
	`"groupMap":{"2026-01-26":{"groupAverage":7200,"groupMax":7300,` +
	`"enduranceContributorDTOList":[{"group":8,"contribution":27.544}]}}}`

const fitnessAgeBody = `{"chronologicalAge":41,"fitnessAge":36.44,"achievableFitnessAge":34.2,` +
	`"previousFitnessAge":37.1,"lastUpdated":"` + testCalendarDate + `T06:12:00.0",` +
	`"components":{"bmi":{"value":23.4,"targetValue":22.0,"improvementValue":1.4,` +
	`"potentialAge":35.44,"priority":1,"stale":false,"lastMeasurementDate":"2026-01-30"}}}`

const trainingStatusBody = `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
	`"3001":{"calendarDate":"2026-01-29","trainingStatus":3,"sport":"RUNNING"},` +
	`"3002":{"calendarDate":"` + testCalendarDate + `","trainingStatus":"PRODUCTIVE",` +
	`"trainingStatusFeedbackPhrase":"PRODUCTIVE_1","sport":"RUNNING","fitnessTrend":1,` +
	`"acuteTrainingLoadDTO":{"dailyTrainingLoadAcute":412,"dailyTrainingLoadChronic":388,` +
	`"dailyAcuteChronicWorkloadRatio":1.06,"acwrStatus":"OPTIMAL","acwrPercent":106,` +
	`"minTrainingLoadChronic":300,"maxTrainingLoadChronic":460}}}},` +
	`"mostRecentVO2Max":{"generic":{"vo2MaxValue":52,"vo2MaxPreciseValue":52.4},` +
	`"cycling":null},` +
	`"mostRecentTrainingLoadBalance":{"metricsTrainingLoadBalanceDTOMap":{` +
	`"3002":{"monthlyLoadAerobicLow":1200,"monthlyLoadAerobicHigh":540,` +
	`"monthlyLoadAnaerobic":210,"trainingBalanceFeedbackPhrase":"BALANCED"}}}}`

func newTrainingScores(t *testing.T, h harness) *api.TrainingScores {
	t.Helper()

	scores, err := api.NewTrainingScores(h.rc)
	if err != nil {
		t.Fatalf("NewTrainingScores() = %v", err)
	}
	return scores
}

func scoresWindow(t *testing.T) client.DateRange {
	t.Helper()

	span, err := client.NewDateRange(mustDate(t, scoresWindowStart), mustDate(t, scoresWindowEnd))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}
	return span
}

func TestNewTrainingScoresRefusesAMissingRequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewTrainingScores(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Fatalf("NewTrainingScores(nil) = %v, want ErrNotConfigured", err)
	}
	if _, err := api.NewTrainingScoresFrom(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Fatalf("NewTrainingScoresFrom(nil) = %v, want ErrNotConfigured", err)
	}
}

func TestTrainingScoresSharesTheRequestLayerOfWellness(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathHillScoreStats,
		testkit.JSON(http.StatusOK, hillScoreBody))
	h := newHarness(t, script, client.Limits{})

	wellness, err := api.NewWellness(h.rc)
	if err != nil {
		t.Fatalf("NewWellness() = %v", err)
	}
	scores, err := api.NewTrainingScoresFrom(wellness)
	if err != nil {
		t.Fatalf("NewTrainingScoresFrom() = %v", err)
	}
	if _, err := scores.HillScore(t.Context(), h.session, scoresWindow(t)); err != nil {
		t.Fatalf("HillScore() through the shared layer = %v", err)
	}
}

func TestTrainingScoresHillScoreDecodesTolerantly(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathHillScoreStats,
		testkit.JSON(http.StatusOK, hillScoreBody))
	h := newHarness(t, script, client.Limits{})

	window, err := newTrainingScores(t, h).HillScore(t.Context(), h.session, scoresWindow(t))
	if err != nil {
		t.Fatalf("HillScore() = %v", err)
	}

	if got, ok := window.MaxScore.Float64(); !ok || got != 71 {
		t.Errorf("maxScore = %v (%t), want 71 from the string form", got, ok)
	}
	if got := window.Days.Len(); got != 1 {
		t.Fatalf("the window carries %d days, want one", got)
	}
	day := window.Days.Items()[0]
	if got, ok := day.StrengthScore.Float64(); !ok || got != 51 {
		t.Errorf("strengthScore = %v (%t), want 51 from the string form", got, ok)
	}
	if day.EnduranceScore.IsSet() {
		t.Error("a null enduranceScore decoded as present")
	}
	if got := window.PeriodAvgScore["7"]; !got.IsSet() {
		t.Error("periodAvgScore lost its keyed value")
	}
}

func TestTrainingScoresHillScoreSendsTheWindowAndAggregation(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathHillScoreStats,
		testkit.JSON(http.StatusOK, hillScoreBody))
	h := newHarness(t, script, client.Limits{})

	if _, err := newTrainingScores(t, h).HillScore(
		t.Context(), h.session, scoresWindow(t)); err != nil {
		t.Fatalf("HillScore() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	query := requests[0].Query
	if got := query.Get(client.QueryStartDate); got != scoresWindowStart {
		t.Errorf("startDate = %q, want %q", got, scoresWindowStart)
	}
	if got := query.Get(client.QueryEndDate); got != scoresWindowEnd {
		t.Errorf("endDate = %q, want %q", got, scoresWindowEnd)
	}
	if got := query.Get(client.QueryAggregation); got != client.AggregationDaily {
		t.Errorf("aggregation = %q, want %q", got, client.AggregationDaily)
	}
}

func TestTrainingScoresRefusesAnUnsetWindowBeforeDispatch(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	scores := newTrainingScores(t, h)

	if _, err := scores.HillScore(
		t.Context(), h.session, client.DateRange{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("HillScore(zero window) = %v, want ErrValidation", err)
	}
	if _, err := scores.EnduranceScore(
		t.Context(), h.session, client.DateRange{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("EnduranceScore(zero window) = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

func TestTrainingScoresRefusesAnOversizedWindowBeforeDispatch(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 3})

	_, err := newTrainingScores(t, h).LactateThresholdSpeedRange(
		t.Context(), h.session, scoresWindow(t))
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("LactateThresholdSpeedRange(wide window) = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

func TestTrainingScoresEnduranceScoreDecodesTheWindow(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEnduranceScoreStats,
		testkit.JSON(http.StatusOK, enduranceScoreBody))
	h := newHarness(t, script, client.Limits{})

	window, err := newTrainingScores(t, h).EnduranceScore(
		t.Context(), h.session, scoresWindow(t))
	if err != nil {
		t.Fatalf("EnduranceScore() = %v", err)
	}

	if window.Score == nil {
		t.Fatal("the window carries no endurance score")
	}
	if got, _ := window.Score.OverallScore.Float64(); got != 7250 {
		t.Errorf("overallScore = %v, want 7250", got)
	}
	if got := window.Score.Contributors.Len(); got != 1 {
		t.Errorf("the score carries %d contributors, want one", got)
	}
	group, ok := window.GroupMap["2026-01-26"]
	if !ok {
		t.Fatal("the window carries no group for the scripted week")
	}
	if got, _ := group.GroupMax.Float64(); got != 7300 {
		t.Errorf("groupMax = %v, want 7300", got)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryAggregation); got != client.AggregationWeekly {
		t.Errorf("aggregation = %q, want %q", got, client.AggregationWeekly)
	}
}

func TestTrainingScoresTrainingEffectReadsTheActivitySummary(t *testing.T) {
	t.Parallel()

	id := mustID(t)
	body := `{"activityId":` + id.String() + `,"summaryDTO":{"trainingEffect":3.4,` +
		`"anaerobicTrainingEffect":1.2,"trainingEffectLabel":"TEMPO","recoveryTime":1560,` +
		`"activityTrainingLoad":168.4,"performanceCondition":2}}`
	script := testkit.NewScript().With(
		client.PathActivityPrefix+"/"+id.String(), testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	effect, err := newTrainingScores(t, h).TrainingEffect(t.Context(), h.session, id)
	if err != nil {
		t.Fatalf("TrainingEffect() = %v", err)
	}
	if effect.Summary == nil {
		t.Fatal("the activity carries no training summary")
	}
	if got, _ := effect.Summary.TrainingEffect.Float64(); got != 3.4 {
		t.Errorf("trainingEffect = %v, want 3.4", got)
	}
	if got, _ := effect.Summary.TrainingEffectLabel.Value(); got != "TEMPO" {
		t.Errorf("trainingEffectLabel = %q, want TEMPO", got)
	}
}

func TestTrainingScoresTrainingEffectRefusesAnUnsetIdentifier(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	_, err := newTrainingScores(t, h).TrainingEffect(t.Context(), h.session, client.ID{})
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("TrainingEffect(zero id) = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

func TestTrainingScoresFitnessAgeDecodesTheDay(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(
		client.PathFitnessAgePrefix+"/"+testCalendarDate,
		testkit.JSON(http.StatusOK, fitnessAgeBody))
	h := newHarness(t, script, client.Limits{})

	day, err := newTrainingScores(t, h).FitnessAgeData(
		t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("FitnessAgeData() = %v", err)
	}
	if got, _ := day.FitnessAge.Float64(); got != 36.44 {
		t.Errorf("fitnessAge = %v, want 36.44", got)
	}
	component, ok := day.Components["bmi"]
	if !ok {
		t.Fatal("the document carries no bmi component")
	}
	if component.Stale == nil || *component.Stale {
		t.Errorf("stale = %v, want false", component.Stale)
	}
}

func TestTrainingScoresRefusesAnUnsetDateBeforeDispatch(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	scores := newTrainingScores(t, h)

	if _, err := scores.FitnessAgeData(
		t.Context(), h.session, client.Date{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("FitnessAgeData(zero date) = %v, want ErrValidation", err)
	}
	if _, err := scores.TrainingStatusData(
		t.Context(), h.session, client.Date{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("TrainingStatusData(zero date) = %v, want ErrValidation", err)
	}
	if _, err := scores.LatestPowerToWeight(
		t.Context(), h.session, client.Date{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("LatestPowerToWeight(zero date) = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

func TestTrainingScoresTrainingStatusDecodesTheDeviceKeyedBlocks(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(
		client.PathTrainingStatusPrefix+"/"+testCalendarDate,
		testkit.JSON(http.StatusOK, trainingStatusBody))
	h := newHarness(t, script, client.Limits{})

	status, err := newTrainingScores(t, h).TrainingStatusData(
		t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("TrainingStatusData() = %v", err)
	}

	if status.MostRecentTrainingStatus == nil {
		t.Fatal("the document carries no training status block")
	}
	if got := len(status.MostRecentTrainingStatus.LatestData); got != 2 {
		t.Errorf("the status block carries %d devices, want two", got)
	}
	if status.MostRecentVO2Max == nil || status.MostRecentVO2Max.Generic == nil {
		t.Fatal("the document carries no generic VO2 max")
	}
	if status.MostRecentVO2Max.Cycling != nil {
		t.Error("a null cycling block decoded as present")
	}
	if status.MostRecentTrainingLoadBalance == nil ||
		len(status.MostRecentTrainingLoadBalance.Devices) != 1 {
		t.Error("the load balance block did not decode")
	}
}

func TestTrainingScoresReportsAnEmptyDocumentAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathHillScoreStats, testkit.JSON(http.StatusOK, `{}`)).
		With(client.PathLatestLactateThreshold, testkit.JSON(http.StatusOK, `null`)).
		With(client.PathLatestFunctionalThresholdPowerPrefix+"/"+client.SportCycling,
			testkit.JSON(http.StatusOK, `[]`))
	h := newHarness(t, script, client.Limits{})
	scores := newTrainingScores(t, h)

	window, err := scores.HillScore(t.Context(), h.session, scoresWindow(t))
	if err != nil || window.Days.Len() != 0 {
		t.Errorf("HillScore() = %+v, %v; want an empty window and no error", window, err)
	}
	entries, err := scores.LatestLactateThreshold(t.Context(), h.session)
	if err != nil || len(entries) != 0 {
		t.Errorf("LatestLactateThreshold() = %v, %v; want no entries and no error", entries, err)
	}
	records, err := scores.CyclingFTP(t.Context(), h.session)
	if err != nil || len(records) != 0 {
		t.Errorf("CyclingFTP() = %v, %v; want no records and no error", records, err)
	}
}
