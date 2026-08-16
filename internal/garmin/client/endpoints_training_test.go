package client_test

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// TestTrainingConstantsHaveTheirPinnedValues pins every training constant to its
// literal value: a shape test passes a path that points at the wrong service, and 15
// tools are built on these. The values come from python-garminconnect at the
// commit docs/upstream-pins.md names, so changing one here without changing the
// pin is the mistake this test exists to make loud.
func TestTrainingConstantsHaveTheirPinnedValues(t *testing.T) {
	t.Parallel()

	paths := []struct {
		got  string
		want string
	}{
		{client.PathFitnessStatsActivity, "/fitnessstats-service/activity"},
		{client.PathHillScore, "/metrics-service/metrics/hillscore"},
		{client.PathHillScoreStats, "/metrics-service/metrics/hillscore/stats"},
		{client.PathEnduranceScore, "/metrics-service/metrics/endurancescore"},
		{client.PathEnduranceScoreStats, "/metrics-service/metrics/endurancescore/stats"},
		{client.PathTrainingStatusPrefix, "/metrics-service/metrics/trainingstatus/aggregated"},
		{client.PathMaxMetricsPrefix, "/metrics-service/metrics/maxmet/daily"},
		{client.PathHRVPrefix, "/hrv-service/hrv"},
		{client.PathFitnessAgePrefix, "/fitnessage-service/fitnessage"},
		{
			client.PathLatestFunctionalThresholdPowerPrefix,
			"/biometric-service/biometric/latestFunctionalThresholdPower",
		},
		{client.PathLatestLactateThreshold, "/biometric-service/biometric/latestLactateThreshold"},
		{client.PathPowerToWeightLatestPrefix, "/biometric-service/biometric/powerToWeight/latest"},
		{
			client.PathLactateThresholdSpeedRangePrefix,
			"/biometric-service/stats/lactateThresholdSpeed/range",
		},
		{
			client.PathLactateThresholdHeartRateRangePrefix,
			"/biometric-service/stats/lactateThresholdHeartRate/range",
		},
		{
			client.PathFunctionalThresholdPowerRangePrefix,
			"/biometric-service/stats/functionalThresholdPower/range",
		},
		{client.PathEpochReloadRequestPrefix, "/wellness-service/wellness/epoch/request"},
	}
	for _, tc := range paths {
		if tc.got != tc.want {
			t.Errorf("path = %q, want %q", tc.got, tc.want)
		}
	}

	labels := []struct {
		got  client.Endpoint
		want string
	}{
		{client.EndpointFitnessStats, "connectapi.fitnessstats.activity"},
		{client.EndpointHillScore, "connectapi.metrics.hill_score"},
		{client.EndpointHillScoreStats, "connectapi.metrics.hill_score_stats"},
		{client.EndpointEnduranceScore, "connectapi.metrics.endurance_score"},
		{client.EndpointEnduranceScoreStats, "connectapi.metrics.endurance_score_stats"},
		{client.EndpointTrainingStatus, "connectapi.metrics.training_status"},
		{client.EndpointMaxMetrics, "connectapi.metrics.max_metrics"},
		{client.EndpointHRV, "connectapi.hrv.daily"},
		{client.EndpointFitnessAge, "connectapi.fitnessage.daily"},
		{client.EndpointLatestFunctionalThresholdPower, "connectapi.biometric.latest_ftp"},
		{client.EndpointLatestLactateThreshold, "connectapi.biometric.latest_lactate_threshold"},
		{client.EndpointPowerToWeightLatest, "connectapi.biometric.power_to_weight_latest"},
		{
			client.EndpointLactateThresholdSpeedRange,
			"connectapi.biometric.lactate_threshold_speed_range",
		},
		{
			client.EndpointLactateThresholdHeartRateRange,
			"connectapi.biometric.lactate_threshold_heart_rate_range",
		},
		{client.EndpointFunctionalThresholdPowerRange, "connectapi.biometric.ftp_range"},
		{client.EndpointEpochReloadRequest, "connectapi.wellness.epoch_reload_request"},
	}
	for _, tc := range labels {
		if string(tc.got) != tc.want {
			t.Errorf("endpoint label = %q, want %q", tc.got, tc.want)
		}
	}

	operations := []struct {
		got  client.Op
		want string
	}{
		{client.OpGetProgressSummaryBetweenDates, "get_progress_summary_between_dates"},
		{client.OpGetHillScore, "get_hill_score"},
		{client.OpGetEnduranceScore, "get_endurance_score"},
		{client.OpGetTrainingEffect, "get_training_effect"},
		{client.OpGetHRVData, "get_hrv_data"},
		{client.OpGetFitnessAgeData, "get_fitnessage_data"},
		{client.OpGetTrainingStatus, "get_training_status"},
		{client.OpGetCyclingFTP, "get_cycling_ftp"},
		{client.OpGetLactateThreshold, "get_lactate_threshold"},
		{client.OpRequestReload, "request_reload"},
		{client.OpGetTrainingLoadTrend, "get_training_load_trend"},
		{client.OpGetTrainingLoadBalance, "get_training_load_balance"},
		{client.OpGetHRVTrend, "get_hrv_trend"},
		{client.OpGetVO2MaxTrend, "get_vo2max_trend"},
		{client.OpGetRespirationTrend, "get_respiration_trend"},
	}
	for _, tc := range operations {
		if string(tc.got) != tc.want {
			t.Errorf("op = %q, want %q", tc.got, tc.want)
		}
	}

	wireValues := []struct {
		got  string
		want string
	}{
		{client.QueryAggregation, "aggregation"},
		{client.QueryAggregationStrategy, "aggregationStrategy"},
		{client.QueryMetric, "metric"},
		{client.QueryGroupByParentActivityType, "groupByParentActivityType"},
		{client.QuerySport, "sport"},
		{client.AggregationLifetime, "lifetime"},
		{client.AggregationDaily, "daily"},
		{client.AggregationWeekly, "weekly"},
		{client.AggregationStrategyLatest, "LATEST"},
		{client.SportRunning, "RUNNING"},
		{client.SportCycling, "CYCLING"},
		// Upstream sends this one mixed-case to the power-to-weight endpoint. The
		// difference from SportRunning is deliberate, so it is pinned separately.
		{client.SportRunningMixedCase, "Running"},
	}
	for _, tc := range wireValues {
		if tc.got != tc.want {
			t.Errorf("wire value = %q, want %q", tc.got, tc.want)
		}
	}
}

// TestTrainingPathsAreTemplates keeps every training path a query-free template,
// so a date or a sport key is always appended as an escaped segment by the
// domain client rather than baked into the constant.
func TestTrainingPathsAreTemplates(t *testing.T) {
	t.Parallel()

	paths := []string{
		client.PathFitnessStatsActivity, client.PathHillScore, client.PathHillScoreStats,
		client.PathEnduranceScore, client.PathEnduranceScoreStats,
		client.PathTrainingStatusPrefix, client.PathMaxMetricsPrefix,
		client.PathHRVPrefix, client.PathFitnessAgePrefix,
		client.PathLatestFunctionalThresholdPowerPrefix, client.PathLatestLactateThreshold,
		client.PathPowerToWeightLatestPrefix, client.PathLactateThresholdSpeedRangePrefix,
		client.PathLactateThresholdHeartRateRangePrefix,
		client.PathFunctionalThresholdPowerRangePrefix, client.PathEpochReloadRequestPrefix,
	}
	seen := make(map[string]struct{})
	for _, path := range paths {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("path %q must be host-relative and start with a slash", path)
		}
		if strings.ContainsAny(path, "?&= {}") || strings.HasSuffix(path, "/") {
			t.Errorf("path %q must be a bare template: no query, no placeholder, no trailing slash", path)
		}
		if _, duplicate := seen[path]; duplicate {
			t.Errorf("path %q is declared twice; one endpoint needs exactly one constant", path)
		}
		seen[path] = struct{}{}
	}
}

// TestEveryTrainingEndpointAndOpIsInTheAllowlist is the regression test for a
// dropped entry.
//
// Request.Validate refuses any endpoint or op outside the allowlists, so an
// entry removed from trainingEndpoints or trainingOps makes its tool impossible
// to call while every other test stays green. Counting is not enough — a swap
// would keep the count — so each one is asserted by name.
func TestEveryTrainingEndpointAndOpIsInTheAllowlist(t *testing.T) {
	t.Parallel()

	endpoints := []client.Endpoint{
		client.EndpointFitnessStats,
		client.EndpointHillScore,
		client.EndpointHillScoreStats,
		client.EndpointEnduranceScore,
		client.EndpointEnduranceScoreStats,
		client.EndpointTrainingStatus,
		client.EndpointMaxMetrics,
		client.EndpointHRV,
		client.EndpointFitnessAge,
		client.EndpointLatestFunctionalThresholdPower,
		client.EndpointLatestLactateThreshold,
		client.EndpointPowerToWeightLatest,
		client.EndpointLactateThresholdSpeedRange,
		client.EndpointLactateThresholdHeartRateRange,
		client.EndpointFunctionalThresholdPowerRange,
		client.EndpointEpochReloadRequest,
	}
	for _, endpoint := range endpoints {
		if !endpoint.IsKnown() {
			t.Errorf("endpoint %q is not in the allowlist, so Request.Validate refuses it", endpoint)
		}
	}
	if got, want := len(endpoints), 16; got != want {
		t.Errorf("%d training endpoints asserted, want %d", got, want)
	}

	operations := []client.Op{
		client.OpGetProgressSummaryBetweenDates,
		client.OpGetHillScore,
		client.OpGetEnduranceScore,
		client.OpGetTrainingEffect,
		client.OpGetHRVData,
		client.OpGetFitnessAgeData,
		client.OpGetTrainingStatus,
		client.OpGetCyclingFTP,
		client.OpGetLactateThreshold,
		client.OpRequestReload,
		client.OpGetTrainingLoadTrend,
		client.OpGetTrainingLoadBalance,
		client.OpGetHRVTrend,
		client.OpGetVO2MaxTrend,
		client.OpGetRespirationTrend,
	}
	for _, op := range operations {
		if !op.IsKnown() {
			t.Errorf("op %q is not in the allowlist, so Request.Validate refuses it", op)
		}
		if op.IsCredentialSubmission() {
			t.Errorf("op %q must not be treated as a credential submission", op)
		}
	}
	if got, want := len(operations), 15; got != want {
		t.Errorf("%d training ops asserted, want %d", got, want)
	}
}
