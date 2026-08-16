//go:build garminlive

package live

import (
	"slices"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The training half of the read-only sweep. It obeys the same contract as
// surface_test.go and is split off only to stay inside the package's 400-line limit.

// sweepProgressMetric is the metric the progress summary is asked for. Distance is
// the one figure every activity type carries, so an empty answer means an empty
// window rather than an unsupported metric.
const sweepProgressMetric = "distance"

// trainingCalls are the training tools whose arguments are a calendar day or a date
// window. The activity-scoped one lives in activityCalls, and the one that takes no
// argument at all lives in noArgTrainingCalls.
func trainingCalls(now time.Time) []sweepCall {
	window := sweepWindow(now)
	day := map[string]any{argDate: sweepDay(now)}

	return slices.Concat(noArgTrainingCalls(), []sweepCall{
		{tools.ToolGetHillScore, window},
		{tools.ToolGetEnduranceScore, window},
		{tools.ToolGetFitnessAgeData, day},
		{tools.ToolGetTrainingStatus, day},
		{tools.ToolGetLactateThreshold, window},
		{tools.ToolGetHRVData, day},
		{tools.ToolGetTrainingLoadBalance, day},
		{tools.ToolGetHRVTrend, window},
		{tools.ToolGetVO2MaxTrend, window},
		{tools.ToolGetRespirationTrend, window},
		{tools.ToolGetTrainingLoadTrend, window},
		{tools.ToolGetProgressSummaryBetweenDates, progressArgs(window)},
	})
}

// noArgTrainingCalls are the training tools that take no argument: each reads the
// account's current value rather than a day or a window.
func noArgTrainingCalls() []sweepCall {
	return []sweepCall{
		{tools.ToolGetCyclingFTP, map[string]any{}},
	}
}

// progressArgs adds the metric to a date window. The window keys are copied rather
// than mutated, because sweepWindow's map is shared by every caller above.
func progressArgs(window map[string]any) map[string]any {
	args := map[string]any{argMetric: sweepProgressMetric}
	for key, value := range window {
		args[key] = value
	}
	return args
}

// TestTrainingToolsAnswerOverTheLiveAccount drives every day- and window-scoped
// training read-only tool against the real account. An empty answer is a pass: the
// account may carry no reading for the window, and the assertions are that a request
// was dispatched, the answer carries this tool's shape and bounds, and nothing leaked.
func TestTrainingToolsAnswerOverTheLiveAccount(t *testing.T) {
	e := liveEnv(t)

	for _, call := range trainingCalls(e.now) {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}
