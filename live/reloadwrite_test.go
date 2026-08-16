//go:build garminlive

package live

import (
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// reloadDaysBack is how far back the reloaded day sits. Two days is past any
// in-flight device sync while staying inside the window Garmin still holds detail
// for.
const reloadDaysBack = 2

// TestRequestReloadAsksGarminToReprocessOneDay drives the training domain's only
// write against the real account.
//
// It creates nothing and needs no cleanup: the call asks Garmin to recompute the
// figures it already holds for one past day, so there is no record to delete
// afterwards and nothing for writecleanup to track. That is also why the day is a
// past one — asking for today would race the device sync that is still writing it.
//
// An answer that reports the request was accepted is the assertion. Whether Garmin
// then reprocesses anything is its own business and is not observable from here.
func TestRequestReloadAsksGarminToReprocessOneDay(t *testing.T) {
	w := liveWriteEnv(t)

	date := time.Now().UTC().AddDate(0, 0, -reloadDaysBack).Format(time.DateOnly)
	result := w.call(t, tools.ToolRequestReload, map[string]any{argDate: date})

	if got := result[argDate]; got != date {
		t.Errorf("request_reload answered for %v, asked for %s", got, date)
	}
	if _, reported := result["requested"]; !reported {
		t.Error("request_reload answered without saying whether the request was accepted")
	}
}
