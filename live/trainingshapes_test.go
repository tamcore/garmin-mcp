//go:build garminlive

package live

import "github.com/tamcore/garmin-mcp/internal/tools"

// The training half of the shape table. Split from shapes_test.go only to stay
// inside the package's 400-line limit; the contract is the one stated there.

// Result keys the training shapes repeat.
const (
	keyReported     = "reported"
	keyTrend        = "trend"
	keyCoverage     = "coverage"
	keyDaysWithData = "days_with_data"
)

// trainingShapes names, per training tool, the result keys its answer always
// carries. A key that is also an argument must repeat the value that was sent, so
// the window tools list their own start and end dates back.
func trainingShapes() map[string][]string {
	window := []string{argStartDate, argEndDate}
	trend := append([]string{keyDaysWithData, keyTrend, keyCoverage}, window...)

	return map[string][]string{
		tools.ToolGetHillScore:      append([]string{keyCount, keyTruncated, "daily_scores"}, window...),
		tools.ToolGetEnduranceScore: append([]string{"weekly_breakdown", keyTruncated}, window...),
		tools.ToolGetTrainingEffect: {argActivityID, keyReported},
		tools.ToolGetFitnessAgeData: {argDate, keyReported, "components_truncated"},
		tools.ToolGetTrainingStatus: {
			argDate, keyReported,
			"status_devices_reported", "balance_devices_reported",
		},
		tools.ToolGetCyclingFTP:          {keyReported, "records_found"},
		tools.ToolGetLactateThreshold:    append([]string{"mode", "parts", keyTruncated}, window...),
		tools.ToolGetHRVData:             {argDate, keyHasData},
		tools.ToolGetTrainingLoadBalance: {argDate, keyHasData},
		tools.ToolGetHRVTrend:            trend,
		tools.ToolGetVO2MaxTrend:         append([]string{"data_points"}, trend...),
		tools.ToolGetRespirationTrend:    trend,
		tools.ToolGetTrainingLoadTrend:   trend,
		tools.ToolGetProgressSummaryBetweenDates: append(
			[]string{argMetric, keyHasData, "stats_by_activity_type", keyTruncated}, window...,
		),
	}
}
