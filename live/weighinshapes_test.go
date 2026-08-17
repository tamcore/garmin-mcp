//go:build garminlive

package live

import "github.com/tamcore/garmin-mcp/internal/tools"

// The weigh-in read half of the shape table. Split from shapes_test.go only to stay
// inside the package's 400-line limit; the contract is the one stated there.

// weighInReadShapes names, per weigh-in read tool, the result keys its answer
// always carries. Both average-weight fields are omitempty
// (internal/tools/weighinreads.go's WeighInRangeResult/DailyWeighInsResult) and are
// never required here, because upstream treats an average of exactly zero the same
// as absent.
func weighInReadShapes() map[string][]string {
	window := []string{argStartDate, argEndDate}

	return map[string][]string{
		tools.ToolGetWeighIns: append(
			[]string{"measurement_count", keyDaysWithData, "measurements", "measurements_truncated"},
			window...,
		),
		tools.ToolGetDailyWeighIns: {
			argDate, "measurement_count", "measurements", "measurements_truncated",
		},
	}
}
