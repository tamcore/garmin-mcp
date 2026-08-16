package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The threshold reads: the latest cycling FTP and the five endpoints
// get_lactate_threshold spans. Every fixture here is synthetic and no value is a
// recording of a real account.

const cyclingFTPBody = `{"sport":"CYCLING","functionalThresholdPower":244,` +
	`"calendarDate":"` + testCalendarDate + `","isStale":false,` +
	`"biometricSourceType":"DEVICE_MEASURED"}`

const latestLactateBody = `[{"userProfilePK":900001,"calendarDate":"` + testCalendarDate +
	`","sequence":1,"version":2,"speed":3.42,"heartRate":null},` +
	`{"userProfilePK":900001,"calendarDate":"` + testCalendarDate + `","hearRate":168}]`

// seriesRunning is the one series value this project has sampled. The value set is
// open, so it is a fixture value and never a rule.
const seriesRunning = "running"

const powerToWeightBody = `[{"sport":"Running","functionalThresholdPower":301,` +
	`"weight":72000,"powerToWeight":4.18,"calendarDate":"` + testCalendarDate + `",` +
	`"isStale":true}]`

// thresholdRangeBody is two samples: one naming its series, one omitting it. The
// second value arrives as a numeric string, which a real document does.
const thresholdRangeBody = `[{"from":"` + testCalendarDate + `","value":3.41,` +
	`"series":"running"},{"from":"2026-01-30","value":"3.38"}]`

func TestTrainingScoresCyclingFTPAcceptsASingleObject(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(
		client.PathLatestFunctionalThresholdPowerPrefix+"/"+client.SportCycling,
		testkit.JSON(http.StatusOK, cyclingFTPBody))
	h := newHarness(t, script, client.Limits{})

	records, err := newTrainingScores(t, h).CyclingFTP(t.Context(), h.session)
	if err != nil {
		t.Fatalf("CyclingFTP() = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("the endpoint answered with %d records, want one", len(records))
	}
	if got, _ := records[0].FunctionalThresholdPower.Float64(); got != 244 {
		t.Errorf("functionalThresholdPower = %v, want 244", got)
	}
}

func TestTrainingScoresLatestLactateThresholdKeepsBothSpellings(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathLatestLactateThreshold,
		testkit.JSON(http.StatusOK, latestLactateBody))
	h := newHarness(t, script, client.Limits{})

	entries, err := newTrainingScores(t, h).LatestLactateThreshold(t.Context(), h.session)
	if err != nil {
		t.Fatalf("LatestLactateThreshold() = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("the endpoint answered with %d entries, want two", len(entries))
	}
	if got, _ := entries[0].Speed.Float64(); got != 3.42 {
		t.Errorf("speed = %v, want 3.42", got)
	}
	if got, ok := entries[1].HeartRateTypo.Float64(); !ok || got != 168 {
		t.Errorf("hearRate = %v (%t), want 168 from Garmin's misspelled key", got, ok)
	}
}

// TestTrainingScoresPowerToWeightSendsTheMixedCaseSport pins the one place upstream
// spells the sport "Running" rather than "RUNNING". The casing is deliberate.
func TestTrainingScoresPowerToWeightSendsTheMixedCaseSport(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(
		client.PathPowerToWeightLatestPrefix+"/"+testCalendarDate,
		testkit.JSON(http.StatusOK, powerToWeightBody))
	h := newHarness(t, script, client.Limits{})

	records, err := newTrainingScores(t, h).LatestPowerToWeight(
		t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("LatestPowerToWeight() = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("the endpoint answered with %d records, want one", len(records))
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	if got := requests[0].Query.Get(client.QuerySport); got != client.SportRunningMixedCase {
		t.Errorf("sport = %q, want the mixed-case %q", got, client.SportRunningMixedCase)
	}
}

// TestTrainingScoresRangeReadsSendTheUpperCaseSport pins the other casing: the three
// biometric statistics ranges send "RUNNING".
func TestTrainingScoresRangeReadsSendTheUpperCaseSport(t *testing.T) {
	t.Parallel()

	reads := map[string]struct {
		prefix string
		call   func(*api.TrainingScores, harness, client.DateRange) ([]api.ThresholdSample, error)
	}{
		"speed": {client.PathLactateThresholdSpeedRangePrefix,
			func(s *api.TrainingScores, h harness, span client.DateRange) ([]api.ThresholdSample, error) {
				return s.LactateThresholdSpeedRange(context.Background(), h.session, span)
			}},
		"heart rate": {client.PathLactateThresholdHeartRateRangePrefix,
			func(s *api.TrainingScores, h harness, span client.DateRange) ([]api.ThresholdSample, error) {
				return s.LactateThresholdHeartRateRange(context.Background(), h.session, span)
			}},
		"power": {client.PathFunctionalThresholdPowerRangePrefix,
			func(s *api.TrainingScores, h harness, span client.DateRange) ([]api.ThresholdSample, error) {
				return s.FunctionalThresholdPowerRange(context.Background(), h.session, span)
			}},
	}

	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := read.prefix + "/" + scoresWindowStart + "/" + scoresWindowEnd
			script := testkit.NewScript().With(path, testkit.JSON(http.StatusOK, thresholdRangeBody))
			h := newHarness(t, script, client.Limits{})

			samples, err := read.call(newTrainingScores(t, h), h, scoresWindow(t))
			if err != nil {
				t.Fatalf("range read = %v", err)
			}
			if len(samples) != 2 {
				t.Fatalf("the range answered with %d samples, want two", len(samples))
			}
			if got, ok := samples[1].Value.Float64(); !ok || got != 3.38 {
				t.Errorf("value = %v (%t), want 3.38 from the string form", got, ok)
			}
			if got, ok := samples[0].Series.Value(); !ok || got != seriesRunning {
				t.Errorf("series = %q (%t), want the string %q", got, ok, seriesRunning)
			}
			if samples[1].Series.IsSet() {
				t.Error("a sample that declared no series decoded as carrying one")
			}

			requests := h.server.Requests()
			if len(requests) != 1 {
				t.Fatalf("the fake received %d requests, want one", len(requests))
			}
			query := requests[0].Query
			if got := query.Get(client.QuerySport); got != client.SportRunning {
				t.Errorf("sport = %q, want the upper-case %q", got, client.SportRunning)
			}
			if got := query.Get(client.QueryAggregationStrategy); got != client.AggregationStrategyLatest {
				t.Errorf("aggregationStrategy = %q, want %q", got, client.AggregationStrategyLatest)
			}
		})
	}
}

// TestTrainingScoresReportsAnEmptyDocumentAsEmpty proves a quiet account is a normal
// answer rather than a failure.
