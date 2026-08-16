package api

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// DeviceAlarm is one configured alarm, from either a device's own settings
// document or the account-wide alarm walk.
//
// Source: the alarm curation in devices.py's get_device_alarms tool
// (devices.py:290-311): alarm.get("alarmId"), alarm.get("alarmTime"),
// alarm.get("alarmMode") == "ON", alarm.get("alarmDays", []),
// alarm.get("alarmSound"), alarm.get("backlight") and alarm.get("alarmMessage").
// AlarmDays and Backlight keep their raw shape: neither pinned source documents
// what an individual day or backlight value looks like on the wire, only that
// each is present, so guessing an element type here would be exactly the
// unevidenced-tag mistake this package avoids elsewhere (see api.Goal).
type DeviceAlarm struct {
	AlarmID      client.Number   `json:"alarmId"`
	AlarmTime    client.Number   `json:"alarmTime"`
	AlarmMode    *string         `json:"alarmMode"`
	AlarmDays    json.RawMessage `json:"alarmDays"`
	AlarmSound   *string         `json:"alarmSound"`
	Backlight    json.RawMessage `json:"backlight"`
	AlarmMessage *string         `json:"alarmMessage"`
}

// Enabled reports whether the alarm mode is "ON", matching the exact-string
// comparison get_device_alarms performs.
func (a DeviceAlarm) Enabled() bool {
	return a.AlarmMode != nil && *a.AlarmMode == "ON"
}

// maxAlarmDevices bounds the devices Alarms fans out to. A real account pairs a
// handful of devices; this is generous headroom against a hostile or malformed
// device list turning one tool call into an unbounded burst of settings reads.
const maxAlarmDevices = 64

// maxDeviceAlarmItems bounds the alarms Alarms accumulates across every device,
// matching the discipline every other bounded walk in this project reports
// through a Truncated flag rather than an error (see api.GoalResult.Truncated).
const maxDeviceAlarmItems = 2000

// DeviceAlarmResult is every alarm configured across the account's devices.
type DeviceAlarmResult struct {
	Alarms []DeviceAlarm
	// Truncated reports that maxAlarmDevices or maxDeviceAlarmItems was reached
	// before every device's alarms were read.
	Truncated bool
}

// Alarms reads every alarm from every registered device, matching
// get_device_alarms: it lists the account's devices, then reads each device's
// settings for its alarms and concatenates them. Source: devices.py's
// get_device_alarms tool (devices.py:279-306), which performs the identical
// two-step read with no endpoint of its own.
func (d *Devices) Alarms(ctx context.Context, session client.Session) (DeviceAlarmResult, error) {
	devices, err := d.List(ctx, session)
	if err != nil {
		return DeviceAlarmResult{}, err
	}

	truncatedDevices := false
	if len(devices) > maxAlarmDevices {
		devices = devices[:maxAlarmDevices]
		truncatedDevices = true
	}

	settings := make([]DeviceSettings, len(devices))
	var mu sync.Mutex
	fanErr := d.req.rc.FanOut(ctx, len(devices), func(taskCtx context.Context, index int) error {
		id, ok := devices[index].DeviceID.Int64Exact()
		if !ok {
			return nil
		}
		deviceID, idErr := client.NewID(id)
		if idErr != nil {
			return nil
		}
		one, settingsErr := d.Settings(taskCtx, session, deviceID)
		if settingsErr != nil {
			return settingsErr
		}
		mu.Lock()
		settings[index] = one
		mu.Unlock()
		return nil
	})
	if fanErr != nil {
		return DeviceAlarmResult{}, fanErr
	}

	var all []DeviceAlarm
	truncatedItems := false
	for _, one := range settings {
		items := one.Alarms.Items()
		if room := maxDeviceAlarmItems - len(all); len(items) > room {
			if room > 0 {
				all = append(all, items[:room]...)
			}
			truncatedItems = true
			break
		}
		all = append(all, items...)
	}
	return DeviceAlarmResult{Alarms: all, Truncated: truncatedDevices || truncatedItems}, nil
}
