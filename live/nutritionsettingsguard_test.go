//go:build garminlive

package live

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// This file is the one exception foodguard_test.go's doNutrition admits without
// ownership: set_nutrition_daily_settings mutates a real, permanent, account-wide
// document nothing here can create, so there is no owned object to bind the write
// to. What stands in for ownership is the exact date the running test declares
// before the write leaves this process. nutritionsettingswrite_test.go is the only
// caller of allowSettingsDate, and it also carries the fifth acknowledgement gate
// this endpoint needs beyond the four AGENTS.md names — see its own doc comment for
// why a value restored is not a shape restored.

// allowSettingsDate declares the one date the running test is about to write
// through set_nutrition_daily_settings. It is not a proof of ownership — nothing
// can own a document Garmin never lets this suite create — it is the caller's half
// of the binding settingsPut checks below: the guard trusts the test to have named
// the date before the write leaves the process, not after.
func (f *foodLedger) allowSettingsDate(date string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settingsDate = date
}

// settingsDateAllowed reports whether date is the one the running test declared.
func (f *foodLedger) settingsDateAllowed(date string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return date != "" && f.settingsDate == date
}

// settingsPut admits a nutrition-settings write only for the exact date the
// running test declared through allowSettingsDate, refusing every other date
// before the request reaches Garmin. A tool under test that computed the wrong
// date is caught here rather than overwriting a day the suite never intended.
func (c writeCaller) settingsPut(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	date, found := strings.CutPrefix(req.URL.Path, client.PathNutritionSettingsPrefix+"/")
	if !found || date == "" || !c.foods.settingsDateAllowed(date) {
		return nil, fmt.Errorf(
			"live: refusing a nutrition-settings write for a date this test did not declare")
	}
	return c.inner.Do(ctx, principal, req)
}
