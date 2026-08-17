//go:build garminlive

package live

import (
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The weigh-in read half of the read-only sweep. It obeys the same contract as
// surface_test.go and is split off only to stay inside the package's 400-line limit.
// The write half — add_weigh_in, add_weigh_in_with_timestamps and delete_weigh_ins —
// lives in weighinwrite_test.go and weighinguard_test.go.

// weighInReadCalls are the weigh-in read tools, one window-scoped and one
// day-scoped.
func weighInReadCalls(now time.Time) []sweepCall {
	window := sweepWindow(now)
	day := map[string]any{argDate: sweepDay(now)}

	return []sweepCall{
		{tools.ToolGetWeighIns, window},
		{tools.ToolGetDailyWeighIns, day},
	}
}

// TestWeighInReadToolsAnswerOverTheLiveAccount drives every weigh-in read-only tool
// against the real account. An empty answer is a pass: the account may carry no
// weigh-in for the window or the day.
func TestWeighInReadToolsAnswerOverTheLiveAccount(t *testing.T) {
	e := liveEnv(t)

	for _, call := range weighInReadCalls(e.now) {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}
