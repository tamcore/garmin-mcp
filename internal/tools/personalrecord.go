package tools

import (
	"fmt"
	"strconv"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

const (
	unitSeconds       = "seconds"
	unitMeters        = "meters"
	unitCount         = "count"
	unitDays          = "days"
	recordTypeUnknown = "unknown"
	metersPerKm       = 1000
)

type personalRecordType struct {
	name, unit string
}

// personalRecordTypes is upstream's PR_TYPE_MAPPING (challenges.py:71 at the pin),
// which upstream itself calls estimated from real data.
var personalRecordTypes = map[int64]personalRecordType{
	1:  {"Fastest 1K", unitSeconds},
	2:  {"Fastest Mile", unitSeconds},
	3:  {"Fastest 5K", unitSeconds},
	4:  {"Fastest 10K", unitSeconds},
	5:  {"Fastest Half Marathon", unitSeconds},
	6:  {"Fastest Marathon", unitSeconds},
	7:  {"Longest Run", unitMeters},
	8:  {"Longest Ride", unitMeters},
	9:  {"Most Elevation Gain Cycling", unitMeters},
	10: {"Fastest 100K Cycling", unitSeconds},
	11: {"Fastest 40K Cycling", unitSeconds},
	12: {"Most Steps Day", unitCount},
	13: {"Most Steps Week", unitCount},
	14: {"Most Steps Month", unitCount},
	15: {"Longest Daily Goal Streak", unitDays},
	16: {"Longest Weekly Goal Streak", unitDays},
	17: {"Longest Pool Swim", unitMeters},
	18: {"Fastest 100m Pool Swim", unitSeconds},
	19: {"Fastest 400m Pool Swim", unitSeconds},
	20: {"Fastest 500m Pool Swim", unitSeconds},
	21: {"Fastest 800m Pool Swim", unitSeconds},
	22: {"Fastest 1500m Pool Swim", unitSeconds},
	23: {"Fastest 1 Mile Pool Swim", unitSeconds},
}

func lookupPersonalRecordType(typeID client.Number) (personalRecordType, bool) {
	id, ok := typeID.Int64Exact()
	if !ok {
		return personalRecordType{name: recordTypeUnknown}, false
	}
	recordType, ok := personalRecordTypes[id]
	if !ok {
		return personalRecordType{name: recordTypeUnknown}, false
	}
	return recordType, true
}

func displayPersonalRecord(unit string, value float64) string {
	switch unit {
	case unitSeconds:
		return formatDuration(int64(value))
	case unitMeters:
		if value >= metersPerKm {
			return fmt.Sprintf("%.2f km", value/metersPerKm)
		}
		return fmt.Sprintf("%.0f m", value)
	case unitDays:
		return fmt.Sprintf("%d days", int64(value))
	default:
		return strconv.FormatInt(int64(value), 10)
	}
}

func formatDuration(total int64) string {
	hours, minutes, seconds := total/3600, total%3600/60, total%60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// personalRecordStart renders Garmin's epoch-milliseconds start time as RFC 3339 and
// passes any other spelling through unchanged.
func personalRecordStart(text client.Text) *string {
	value := optionalText(text)
	if value == nil {
		return nil
	}
	millis, err := strconv.ParseInt(*value, 10, 64)
	if err != nil {
		return value
	}
	return new(time.UnixMilli(millis).UTC().Format(time.RFC3339))
}
