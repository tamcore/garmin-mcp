package tools

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetDeviceAlarms is the upstream compatibility name of the account-wide
// device-alarm read.
const ToolGetDeviceAlarms = "get_device_alarms"

// DeviceAlarmEntry is one configured alarm.
//
// Source: the curation in devices.py's get_device_alarms tool (devices.py:290-311):
// alarm.get("alarmId"), the HH:MM time from _format_alarm_time(alarm.get("alarmTime"))
// (devices.py:270-276, minutes-from-midnight), alarm.get("alarmMode") == "ON",
// alarm.get("alarmDays", []), alarm.get("alarmSound"), alarm.get("backlight") and
// alarm.get("alarmMessage"). Days and Backlight are sanitized structured documents
// rather than typed fields, matching api.DeviceAlarm's own doc comment: no pinned
// source documents what an individual day or backlight value looks like on the wire,
// so guessing a Go shape for them would be an unevidenced tag.
type DeviceAlarmEntry struct {
	AlarmID     *int64  `json:"alarm_id,omitempty" jsonschema:"the alarm's identifier"`
	Time        *string `json:"time,omitempty" jsonschema:"the alarm time, HH:MM"`
	TimeMinutes *int64  `json:"time_minutes,omitempty" jsonschema:"minutes from midnight"`
	Enabled     bool    `json:"enabled" jsonschema:"whether the alarm mode is ON"`
	Days        any     `json:"days,omitempty" jsonschema:"the alarm's configured days, sanitized"`
	Sound       *string `json:"sound,omitempty" jsonschema:"the alarm sound"`
	Backlight   any     `json:"backlight,omitempty" jsonschema:"the backlight setting, sanitized"`
	Message     *string `json:"message,omitempty" jsonschema:"the alarm message, when set"`
}

// DeviceAlarmList is every alarm configured across the account's registered devices.
//
// It is device material: an alarm schedule describes a person's routine, so it is
// returned to the authorized caller and never logged.
type DeviceAlarmList struct {
	TotalAlarms   int                `json:"total_alarms" jsonschema:"how many alarms this result carries"`
	EnabledAlarms int                `json:"enabled_alarms" jsonschema:"how many of them are enabled"`
	Alarms        []DeviceAlarmEntry `json:"alarms" jsonschema:"every configured alarm, ordered by time"`

	// Truncated reports that a device or alarm-count bound cut the account-wide
	// walk, matching api.DeviceAlarmResult.Truncated.
	Truncated bool `json:"truncated" jsonschema:"whether a server bound cut the device or alarm walk"`

	// DroppedFields is a count, never a list of names: see sanitizeUntyped's own
	// doc comment for why naming a removed key would itself be a disclosure.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed across every alarm's day/backlight data"`
}

// LogValue reports counts only, never an alarm's schedule.
func (l DeviceAlarmList) LogValue() slog.Value {
	return shape("deviceAlarmList",
		slog.Int("total", l.TotalAlarms),
		slog.Int("enabled", l.EnabledAlarms),
		slog.Bool("truncated", l.Truncated),
	)
}

func getDeviceAlarmsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDeviceAlarms,
			Title: "Get device alarms",
			Description: "read every alarm configured across the account's registered " +
				"devices: time, enabled state, configured days, sound and message. " +
				"Takes no arguments. An account with no configured alarm returns an " +
				"empty list, which is a normal state",
			Tier:        policy.TierReadOnly,
			Category:    categoryDevice,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetDeviceAlarms registers the tool.
func registerGetDeviceAlarms(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, DeviceAlarmList, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DeviceAlarmList{}, err
		}
		result, err := svc.devices.Alarms(ctx, session)
		if err != nil {
			return nil, DeviceAlarmList{}, fail(err)
		}
		return nil, newDeviceAlarmList(result), nil
	}
	return mcpserver.AddTool(registry, getDeviceAlarmsContract().Registration(), handler)
}

// alarmMinutes reads the sortable time key, treating an unset value as midnight —
// matching get_device_alarms's own sort key, `x.get("time_minutes") or 0`
// (devices.py:316).
func alarmMinutes(alarm api.DeviceAlarm) int64 {
	value, ok := alarm.AlarmTime.Int64Exact()
	if !ok {
		return 0
	}
	return value
}

// newDeviceAlarmList maps and sorts every alarm, matching the account-wide sort
// get_device_alarms performs after concatenating every device's alarms
// (devices.py:279-316).
func newDeviceAlarmList(result api.DeviceAlarmResult) DeviceAlarmList {
	alarms := slices.Clone(result.Alarms)
	sort.SliceStable(alarms, func(i, j int) bool {
		return alarmMinutes(alarms[i]) < alarmMinutes(alarms[j])
	})

	out := make([]DeviceAlarmEntry, 0, len(alarms))
	enabled := 0
	dropped := 0
	truncated := result.Truncated
	for _, alarm := range alarms {
		entry, entryDropped, entryTruncated := newDeviceAlarmEntry(alarm)
		dropped += entryDropped
		truncated = truncated || entryTruncated
		if alarm.Enabled() {
			enabled++
		}
		out = append(out, entry)
	}
	return DeviceAlarmList{
		TotalAlarms: len(out), EnabledAlarms: enabled, Alarms: out,
		Truncated: truncated, DroppedFields: dropped,
	}
}

// newDeviceAlarmEntry maps one alarm, sanitizing its day and backlight data through
// the same untyped-document path every other uncurated Garmin value in this package
// takes (see sanitize.go's sanitizeUntyped doc comment).
func newDeviceAlarmEntry(alarm api.DeviceAlarm) (DeviceAlarmEntry, int, bool) {
	days := sanitizeRaw(alarm.AlarmDays)
	backlight := sanitizeRaw(alarm.Backlight)

	entry := DeviceAlarmEntry{
		AlarmID:     optionalInt64(alarm.AlarmID),
		TimeMinutes: optionalInt64(alarm.AlarmTime),
		Time:        formatAlarmTime(alarm.AlarmTime),
		Enabled:     alarm.Enabled(),
		Days:        days.Value,
		Sound:       alarm.AlarmSound,
		Backlight:   backlight.Value,
		Message:     alarm.AlarmMessage,
	}
	return entry, days.Dropped + backlight.Dropped, days.Truncated || backlight.Truncated
}

// formatAlarmTime converts minutes-from-midnight into HH:MM, matching
// _format_alarm_time (devices.py:270-276).
func formatAlarmTime(minutes client.Number) *string {
	value, ok := minutes.Int64Exact()
	if !ok {
		return nil
	}
	text := fmt.Sprintf("%02d:%02d", value/60, value%60)
	return &text
}
