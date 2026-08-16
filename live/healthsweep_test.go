//go:build garminlive

package live

import (
	"slices"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The health-and-wellness half of the read-only sweep. It obeys the same contract as
// surface_test.go and is split off only to stay inside the package's 400-line limit.

// sweepWeeks is how many weeks the week-scoped tools are asked for.
const sweepWeeks = 1

// healthCalls are the health-and-wellness tools, with the argument shape each one's
// own contract declares: a single calendar day, an inclusive date range, or an end
// date plus a week count.
func healthCalls(now time.Time) []sweepCall {
	window := sweepWindow(now)
	weeks := map[string]any{
		argEndDate: now.Format(time.DateOnly),
		argWeeks:   sweepWeeks,
	}

	return slices.Concat(dayScopedHealthCalls(now), []sweepCall{
		{tools.ToolGetBodyComposition, window},
		{tools.ToolGetDailySteps, window},
		{tools.ToolGetBodyBattery, window},
		{tools.ToolGetBloodPressure, window},
		{tools.ToolGetWeeklySteps, weeks},
		{tools.ToolGetWeeklyIntensityMinutes, weeks},
		{tools.ToolGetWeeklyStress, weeks},
	})
}

// dayScopedHealthCalls are the health tools whose only argument is one calendar day.
func dayScopedHealthCalls(now time.Time) []sweepCall {
	day := map[string]any{argDate: sweepDay(now)}
	return []sweepCall{
		{tools.ToolGetStats, day},
		{tools.ToolGetStatsAndBody, day},
		{tools.ToolGetStepsData, day},
		{tools.ToolGetFloors, day},
		{tools.ToolGetStressData, day},
		{tools.ToolGetStressSummary, day},
		{tools.ToolGetAllDayStress, day},
		{tools.ToolGetBodyBatteryEvents, day},
		{tools.ToolGetTrainingReadiness, day},
		{tools.ToolGetMorningTrainingReadiness, day},
		{tools.ToolGetAllDayEvents, day},
		{tools.ToolGetHeartRates, day},
		{tools.ToolGetHeartRatesSummary, day},
		{tools.ToolGetRestingHeartRateDay, day},
		{tools.ToolGetRespirationData, day},
		{tools.ToolGetRespirationSummary, day},
		{tools.ToolGetSpO2Data, day},
		{tools.ToolGetSleepSummary, day},
		{tools.ToolGetHydrationData, day},
		{tools.ToolGetLifestyleLoggingData, day},
	}
}

// TestHealthAndWellnessToolsAnswerOverTheLiveAccount drives every health-and-wellness
// read-only tool against the real account. An empty day is a pass: the account records
// almost no wellness data, and the assertions are that a request was dispatched, the
// answer carries this tool's shape and bounds, and nothing leaked.
func TestHealthAndWellnessToolsAnswerOverTheLiveAccount(t *testing.T) {
	e := liveEnv(t)

	for _, call := range healthCalls(e.now) {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}
