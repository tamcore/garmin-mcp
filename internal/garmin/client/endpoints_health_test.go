package client_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// TestHealthConstantsHaveTheirPinnedValues pins every health constant to the
// literal it must carry.
//
// The shape tests below cannot catch a wrong value: a path that is host-relative
// and free of query text passes them while pointing at the wrong Garmin service,
// and 27 tools are built on these. The values come from python-garminconnect at
// the commit docs/upstream-pins.md names, so changing one here without changing
// the pin is the mistake this test exists to make loud.
func TestHealthConstantsHaveTheirPinnedValues(t *testing.T) {
	t.Parallel()

	paths := []struct {
		got  string
		want string
	}{
		{client.PathDailySummaryChartPrefix, "/wellness-service/wellness/dailySummaryChart"},
		{client.PathFloorsChartDailyPrefix, "/wellness-service/wellness/floorsChartData/daily"},
		{client.PathDailyHeartRatePrefix, "/wellness-service/wellness/dailyHeartRate"},
		{client.PathDailyStressPrefix, "/wellness-service/wellness/dailyStress"},
		{client.PathDailyRespirationPrefix, "/wellness-service/wellness/daily/respiration"},
		{client.PathDailySpO2Prefix, "/wellness-service/wellness/daily/spo2"},
		{client.PathDailyEvents, "/wellness-service/wellness/dailyEvents"},
		{client.PathBodyBatteryDaily, "/wellness-service/wellness/bodyBattery/reports/daily"},
		{client.PathBodyBatteryEventsPrefix, "/wellness-service/wellness/bodyBattery/events"},
		{client.PathDailyHydrationPrefix, "/usersummary-service/usersummary/hydration/daily"},
		{client.PathDailyStepsStatsPrefix, "/usersummary-service/stats/steps/daily"},
		{client.PathWeeklyStepsStatsPrefix, "/usersummary-service/stats/steps/weekly"},
		{client.PathWeeklyStressStatsPrefix, "/usersummary-service/stats/stress/weekly"},
		{client.PathWeeklyIntensityMinutesStatsPrefix, "/usersummary-service/stats/im/weekly"},
		{client.PathBodyComposition, "/weight-service/weight/dateRange"},
		{client.PathBloodPressureRangePrefix, "/bloodpressure-service/bloodpressure/range"},
		{client.PathRestingHeartRatePrefix, "/userstats-service/wellness/daily"},
		{client.PathTrainingReadinessPrefix, "/metrics-service/metrics/trainingreadiness"},
		{client.PathLifestyleLoggingPrefix, "/lifestylelogging-service/dailyLog"},
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
		{client.EndpointDailySummaryChart, "connectapi.wellness.daily_summary_chart"},
		{client.EndpointFloorsChartDaily, "connectapi.wellness.floors_chart_daily"},
		{client.EndpointDailyHeartRate, "connectapi.wellness.daily_heart_rate"},
		{client.EndpointDailyStress, "connectapi.wellness.daily_stress"},
		{client.EndpointDailyRespiration, "connectapi.wellness.daily_respiration"},
		{client.EndpointDailySpO2, "connectapi.wellness.daily_spo2"},
		{client.EndpointDailyEvents, "connectapi.wellness.daily_events"},
		{client.EndpointBodyBatteryDaily, "connectapi.wellness.body_battery_daily"},
		{client.EndpointBodyBatteryEvents, "connectapi.wellness.body_battery_events"},
		{client.EndpointDailyHydration, "connectapi.usersummary.hydration_daily"},
		{client.EndpointDailyStepsStats, "connectapi.usersummary.steps_daily"},
		{client.EndpointWeeklyStepsStats, "connectapi.usersummary.steps_weekly"},
		{client.EndpointWeeklyStressStats, "connectapi.usersummary.stress_weekly"},
		{client.EndpointWeeklyIntensityMinutesStats, "connectapi.usersummary.intensity_minutes_weekly"},
		{client.EndpointBodyComposition, "connectapi.weight.date_range"},
		{client.EndpointBloodPressure, "connectapi.bloodpressure.range"},
		{client.EndpointRestingHeartRate, "connectapi.userstats.resting_heart_rate"},
		{client.EndpointTrainingReadiness, "connectapi.metrics.training_readiness"},
		{client.EndpointLifestyleLogging, "connectapi.lifestylelogging.daily_log"},
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
		{client.OpGetStats, "get_stats"},
		{client.OpGetStatsAndBody, "get_stats_and_body"},
		{client.OpGetBodyComposition, "get_body_composition"},
		{client.OpGetStepsData, "get_steps_data"},
		{client.OpGetDailySteps, "get_daily_steps"},
		{client.OpGetWeeklySteps, "get_weekly_steps"},
		{client.OpGetWeeklyStress, "get_weekly_stress"},
		{client.OpGetWeeklyIntensityMinutes, "get_weekly_intensity_minutes"},
		{client.OpGetTrainingReadiness, "get_training_readiness"},
		{client.OpGetMorningTrainingReadiness, "get_morning_training_readiness"},
		{client.OpGetBodyBattery, "get_body_battery"},
		{client.OpGetBodyBatteryEvents, "get_body_battery_events"},
		{client.OpGetBloodPressure, "get_blood_pressure"},
		{client.OpGetFloors, "get_floors"},
		{client.OpGetRestingHeartRateDay, "get_rhr_day"},
		{client.OpGetHeartRates, "get_heart_rates"},
		{client.OpGetHeartRatesSummary, "get_heart_rates_summary"},
		{client.OpGetHydrationData, "get_hydration_data"},
		{client.OpGetSleepSummary, "get_sleep_summary"},
		{client.OpGetStressData, "get_stress_data"},
		{client.OpGetStressSummary, "get_stress_summary"},
		{client.OpGetAllDayStress, "get_all_day_stress"},
		{client.OpGetAllDayEvents, "get_all_day_events"},
		{client.OpGetRespirationData, "get_respiration_data"},
		{client.OpGetRespirationSummary, "get_respiration_summary"},
		{client.OpGetSpO2Data, "get_spo2_data"},
		{client.OpGetLifestyleLoggingData, "get_lifestyle_logging_data"},
	}
	for _, tc := range operations {
		if string(tc.got) != tc.want {
			t.Errorf("op = %q, want %q", tc.got, tc.want)
		}
	}

	queries := []struct {
		got  string
		want string
	}{
		{client.QueryFromDate, "fromDate"},
		{client.QueryUntilDate, "untilDate"},
		{client.QueryMetricID, "metricId"},
		{client.QueryIncludeAll, "includeAll"},
	}
	for _, tc := range queries {
		if tc.got != tc.want {
			t.Errorf("query name = %q, want %q", tc.got, tc.want)
		}
	}

	if got, want := client.MetricIDRestingHeartRate, 60; got != want {
		t.Errorf("MetricIDRestingHeartRate = %d, want %d", got, want)
	}
}

// TestEveryHealthEndpointAndOpIsInTheAllowlist is the regression test for a
// dropped entry.
//
// Request.Validate refuses any endpoint or op outside the allowlists, so an
// entry removed from healthEndpoints or healthOps makes its tool impossible to
// call while every other test stays green. Counting is not enough — a swap
// would keep the count — so each one is asserted by name.
func TestEveryHealthEndpointAndOpIsInTheAllowlist(t *testing.T) {
	t.Parallel()

	endpoints := []client.Endpoint{
		client.EndpointDailySummaryChart,
		client.EndpointFloorsChartDaily,
		client.EndpointDailyHeartRate,
		client.EndpointDailyStress,
		client.EndpointDailyRespiration,
		client.EndpointDailySpO2,
		client.EndpointDailyEvents,
		client.EndpointBodyBatteryDaily,
		client.EndpointBodyBatteryEvents,
		client.EndpointDailyHydration,
		client.EndpointDailyStepsStats,
		client.EndpointWeeklyStepsStats,
		client.EndpointWeeklyStressStats,
		client.EndpointWeeklyIntensityMinutesStats,
		client.EndpointBodyComposition,
		client.EndpointBloodPressure,
		client.EndpointRestingHeartRate,
		client.EndpointTrainingReadiness,
		client.EndpointLifestyleLogging,
	}
	for _, endpoint := range endpoints {
		if !endpoint.IsKnown() {
			t.Errorf("endpoint %q is not in the allowlist, so Request.Validate refuses it", endpoint)
		}
	}
	if got, want := len(endpoints), 19; got != want {
		t.Errorf("%d health endpoints asserted, want %d", got, want)
	}

	operations := []client.Op{
		client.OpGetStats,
		client.OpGetStatsAndBody,
		client.OpGetBodyComposition,
		client.OpGetStepsData,
		client.OpGetDailySteps,
		client.OpGetWeeklySteps,
		client.OpGetWeeklyStress,
		client.OpGetWeeklyIntensityMinutes,
		client.OpGetTrainingReadiness,
		client.OpGetMorningTrainingReadiness,
		client.OpGetBodyBattery,
		client.OpGetBodyBatteryEvents,
		client.OpGetBloodPressure,
		client.OpGetFloors,
		client.OpGetRestingHeartRateDay,
		client.OpGetHeartRates,
		client.OpGetHeartRatesSummary,
		client.OpGetHydrationData,
		client.OpGetSleepSummary,
		client.OpGetStressData,
		client.OpGetStressSummary,
		client.OpGetAllDayStress,
		client.OpGetAllDayEvents,
		client.OpGetRespirationData,
		client.OpGetRespirationSummary,
		client.OpGetSpO2Data,
		client.OpGetLifestyleLoggingData,
	}
	for _, op := range operations {
		if !op.IsKnown() {
			t.Errorf("op %q is not in the allowlist, so Request.Validate refuses it", op)
		}
	}
	if got, want := len(operations), 27; got != want {
		t.Errorf("%d health ops asserted, want %d", got, want)
	}
}
