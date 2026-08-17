//go:build garminlive

package live

import "github.com/tamcore/garmin-mcp/internal/tools"

// The device half of the shape table. Split from shapes_test.go only to stay
// inside the package's 400-line limit; the contract is the one stated there.

// deviceShapes names, per device tool, the result keys its answer always carries.
//
// get_device_last_used declares no required key: every field of DeviceLastUsed is
// omitempty (internal/tools/get_device_last_used.go), because an account with no
// recorded device upload answers with none of them set, and requiring one here
// would pin this suite to that account's own history.
func deviceShapes() map[string][]string {
	return map[string][]string{
		tools.ToolGetDeviceLastUsed:        {},
		tools.ToolGetDeviceSettings:        {"alarm_count", "enabled_alarm_count"},
		tools.ToolGetPrimaryTrainingDevice: {"training_device_count", keyTruncated},
		tools.ToolGetDeviceAlarms: {
			"total_alarms", "enabled_alarms", "alarms", keyTruncated, keyDroppedFields,
		},
		tools.ToolGetGear: {
			"gear_count", "active_count", "retired_count", "defaults", "gear", keyTruncated,
		},
		tools.ToolGetDeviceSolarData: {argDeviceID, "solar_data", keyCount, keyTruncated},
	}
}
