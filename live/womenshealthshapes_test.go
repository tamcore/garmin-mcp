//go:build garminlive

package live

import "github.com/tamcore/garmin-mcp/internal/tools"

// The women's-health half of the shape table. Split from shapes_test.go only to stay
// inside the package's 400-line limit; the contract is the one stated there.
//
// Every shape here names only has_data and dropped_fields (plus the echoed date
// argument): the sanitized document itself is omitempty
// (internal/tools/get_menstrual_calendar_data.go, get_menstrual_data_for_date.go,
// get_pregnancy_summary.go) and is never required, because an account with no
// menstrual-cycle or pregnancy data answers with has_data false and no document at
// all — an empty answer is a pass for this category, not a failure.
func womensHealthShapes() map[string][]string {
	window := []string{argStartDate, argEndDate}

	return map[string][]string{
		tools.ToolGetMenstrualCalendarData: append([]string{keyHasData, keyDroppedFields}, window...),
		tools.ToolGetMenstrualDataForDate:  {argDate, keyHasData, keyDroppedFields},
		tools.ToolGetPregnancySummary:      {keyHasData, keyDroppedFields},
	}
}
