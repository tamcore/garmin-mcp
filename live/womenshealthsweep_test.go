//go:build garminlive

package live

import (
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The women's-health half of the read-only sweep. It obeys the same contract as
// surface_test.go and is split off only to stay inside the package's 400-line
// limit.
//
// The three tools here answer the most sensitive category this project handles:
// menstrual-cycle and pregnancy data. An empty answer is a pass — the account may
// hold no such data — and assertToolAnswers already checks shape, bounds and the
// absence of a leak only, never the content of a reading, so nothing here needs a
// second, more careful assertion: it is the same contract every other swept tool
// gets, applied to a document this suite never prints on failure either way.
func womensHealthCalls(now time.Time) []sweepCall {
	window := sweepWindow(now)
	day := map[string]any{argDate: sweepDay(now)}

	return []sweepCall{
		{tools.ToolGetMenstrualCalendarData, window},
		{tools.ToolGetMenstrualDataForDate, day},
		{tools.ToolGetPregnancySummary, map[string]any{}},
	}
}

// TestWomensHealthToolsAnswerOverTheLiveAccount drives every women's-health
// read-only tool against the real account.
func TestWomensHealthToolsAnswerOverTheLiveAccount(t *testing.T) {
	e := liveEnv(t)

	for _, call := range womensHealthCalls(e.now) {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}
