//go:build garminlive

package live

import (
	"strconv"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The device half of the read-only sweep. It obeys the same contract as
// surface_test.go and is split off only to stay inside the package's 400-line limit.

// argDeviceID is the device-identifier argument get_device_solar_data requires and
// get_device_settings accepts optionally.
const argDeviceID = "device_id"

// deviceCalls are the device tools that need no device identifier: each takes no
// argument, or get_device_settings, which defaults to the account's most recently
// used device when device_id is omitted.
func deviceCalls() []sweepCall {
	return []sweepCall{
		{tools.ToolGetDeviceLastUsed, map[string]any{}},
		{tools.ToolGetDeviceSettings, map[string]any{}},
		{tools.ToolGetPrimaryTrainingDevice, map[string]any{}},
		{tools.ToolGetDeviceAlarms, map[string]any{}},
		{tools.ToolGetGear, map[string]any{}},
	}
}

// deviceScopedCalls are the device tools that need a device identifier a prior read
// produced.
func deviceScopedCalls(deviceID, day string) []sweepCall {
	return []sweepCall{
		{tools.ToolGetDeviceSolarData, map[string]any{argDeviceID: deviceID, argDate: day}},
	}
}

// TestDeviceToolsAnswerOverTheLiveAccount drives the device tools that need no
// derived argument.
func TestDeviceToolsAnswerOverTheLiveAccount(t *testing.T) {
	e := liveEnv(t)

	for _, call := range deviceCalls() {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}

// TestDeviceScopedToolsAnswerForOneDevice drives the tool that needs a device
// identifier. It is separate from the account sweep so an account with no
// registered device still proves the rest of the surface instead of skipping all of
// it.
func TestDeviceScopedToolsAnswerForOneDevice(t *testing.T) {
	e := liveEnv(t)

	id, found := firstDeviceID(t, e)
	if !found {
		t.Skip("not run — the account has no registered device, so no device id can be derived")
	}

	for _, call := range deviceScopedCalls(id, sweepDay(e.now)) {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}

// firstDeviceID reads the account's first registered device identifier, rendered as
// the decimal string get_device_solar_data's manifest requires.
func firstDeviceID(t *testing.T, e *env) (string, bool) {
	t.Helper()

	devices := e.call(t, tools.ToolGetDevices, nil)
	items, _ := devices["devices"].([]any)
	for _, item := range items {
		device, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%s returned a device that is not an object", tools.ToolGetDevices)
		}
		id, ok := device[argDeviceID].(float64)
		if !ok || id <= 0 {
			continue
		}
		return strconv.FormatFloat(id, 'f', -1, 64), true
	}
	return "", false
}
