package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetDeviceSettings is the upstream compatibility name of the device-settings
// read.
const ToolGetDeviceSettings = "get_device_settings"

// argDeviceID is the manifest's device identifier argument name, shared by
// get_device_settings and get_device_solar_data.
const argDeviceID = "device_id"

// TrackingSettings is the nested activity-tracking-alert settings of one device.
//
// Source: the activity_tracking curation in devices.py's get_device_settings tool
// (devices.py:140-160): activity_tracking.get("moveAlertEnabled"),
// activity_tracking.get("pulseOxSleepTrackingEnabled"),
// activity_tracking.get("highHrAlertEnabled") and
// activity_tracking.get("lowHrAlertEnabled").
type TrackingSettings struct {
	MoveAlertEnabled     *bool `json:"move_alert_enabled,omitempty" jsonschema:"move alert on/off"`
	PulseOxSleepTracking *bool `json:"pulse_ox_sleep_tracking,omitempty" jsonschema:"pulse ox sleep tracking on/off"`
	HighHRAlertEnabled   *bool `json:"high_hr_alert_enabled,omitempty" jsonschema:"high heart-rate alert on/off"`
	LowHRAlertEnabled    *bool `json:"low_hr_alert_enabled,omitempty" jsonschema:"low heart-rate alert on/off"`
}

// DeviceSettings is one device's configuration document.
//
// Source: the curation in devices.py's get_device_settings tool (devices.py:119-172):
// settings.get("deviceId"), settings.get("timeFormat"), settings.get("dateFormat"),
// settings.get("measurementUnits"), settings.get("keyTonesEnabled"),
// settings.get("keyVibrationEnabled"), settings.get("alertTonesEnabled"),
// settings.get("activityTracking", {}), and the alarm count and enabled-alarm count
// derived from settings.get("alarms", []) (devices.py:163-167). The full alarm array
// is deliberately not repeated here: get_device_alarms already carries it curated the
// same way, and reporting only the two counts here matches upstream's own choice not
// to duplicate the list in this tool.
//
// It is device material: a device identifier and an alarm schedule describe a
// person's hardware and routine, so it is returned to the authorized caller and never
// logged.
type DeviceSettings struct {
	DeviceID            *int64            `json:"device_id,omitempty" jsonschema:"the device this belongs to"`
	TimeFormat          *string           `json:"time_format,omitempty" jsonschema:"the configured time format"`
	DateFormat          *string           `json:"date_format,omitempty" jsonschema:"the configured date format"`
	MeasurementUnits    *string           `json:"measurement_units,omitempty" jsonschema:"the configured units"`
	KeyTonesEnabled     *bool             `json:"key_tones_enabled,omitempty" jsonschema:"key tones on/off"`
	KeyVibrationEnabled *bool             `json:"key_vibration_enabled,omitempty" jsonschema:"key vibration on/off"`
	AlertTonesEnabled   *bool             `json:"alert_tones_enabled,omitempty" jsonschema:"alert tones on/off"`
	ActivityTracking    *TrackingSettings `json:"activity_tracking,omitempty" jsonschema:"activity-tracking alerts"`
	AlarmCount          int               `json:"alarm_count" jsonschema:"how many alarms this device carries"`
	EnabledAlarmCount   int               `json:"enabled_alarm_count" jsonschema:"how many of them are enabled"`
}

// LogValue reports presence and counts only, never a device identifier or an alarm
// schedule.
func (d DeviceSettings) LogValue() slog.Value {
	return shape("deviceSettings",
		slog.String("deviceId", presence(d.DeviceID != nil)),
		slog.Int("alarms", d.AlarmCount),
	)
}

// deviceSettingsInput is the argument set: an optional device identifier.
//
// DeviceID is typed any so a caller may omit it, send JSON null, a number, or a
// decimal string — the manifest's nullable anyOf. A nil value resolves to the
// account's most recently used device, matching devices.py's own fallback
// (devices.py:107-117).
type deviceSettingsInput struct {
	DeviceID any `json:"device_id,omitempty" jsonschema:"the device id; defaults to the last-used device"`
}

// deviceIDOptionalProperty declares the optional, nullable device identifier
// get_device_settings takes.
func deviceIDOptionalProperty() Property {
	return Property{
		Name:  argDeviceID,
		Types: []string{typeInteger, typeString},
		Description: "the device identifier from get_devices or get_device_last_used; " +
			"defaults to the most recently used device when omitted",
		MaxLength: new(maxIdentifierArgumentLen),
		Nullable:  true,
	}
}

func getDeviceSettingsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDeviceSettings,
			Title: "Get device settings",
			Description: "read one device's configuration: time and date format, " +
				"measurement units, key tone and vibration settings, the " +
				"activity-tracking alert settings, and its alarm count. device_id " +
				"defaults to the account's most recently used device when omitted",
			Tier:        policy.TierReadOnly,
			Category:    categoryDevice,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(deviceIDOptionalProperty()),
	}
}

// registerGetDeviceSettings registers the tool.
func registerGetDeviceSettings(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deviceSettingsInput) (
		*mcp.CallToolResult, DeviceSettings, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DeviceSettings{}, err
		}
		id, err := svc.resolveDeviceID(ctx, session, in.DeviceID)
		if err != nil {
			return nil, DeviceSettings{}, err
		}
		settings, err := svc.devices.Settings(ctx, session, id)
		if err != nil {
			return nil, DeviceSettings{}, fail(err)
		}
		return nil, newDeviceSettings(settings), nil
	}
	return mcpserver.AddTool(registry, getDeviceSettingsContract().Registration(), handler)
}

// resolveDeviceID resolves an optional device_id argument, defaulting to the
// account's most recently used device when raw is nil — matching devices.py's
// get_device_settings tool (devices.py:107-117), which falls back to
// get_device_last_used().get("userDeviceId") when device_id is omitted.
func (s *service) resolveDeviceID(
	ctx context.Context, session client.Session, raw any,
) (client.ID, error) {
	if raw == nil {
		last, err := s.devices.LastUsed(ctx, session)
		if err != nil {
			return client.ID{}, fail(err)
		}
		value, ok := last.UserDeviceID.Int64Exact()
		if !ok || value <= 0 {
			return client.ID{}, invalidArgument(
				"no default device was found; pass device_id explicitly")
		}
		id, err := client.NewID(value)
		if err != nil {
			return client.ID{}, invalidArgument("device_id must be a positive whole number")
		}
		return id, nil
	}
	return parseIdentifier(argDeviceID, raw)
}

// newDeviceSettings maps the domain document onto the result.
func newDeviceSettings(settings api.DeviceSettings) DeviceSettings {
	out := DeviceSettings{
		DeviceID:            optionalInt64(settings.DeviceID),
		TimeFormat:          settings.TimeFormat,
		DateFormat:          settings.DateFormat,
		MeasurementUnits:    settings.MeasurementUnits,
		KeyTonesEnabled:     settings.KeyTonesEnabled,
		KeyVibrationEnabled: settings.KeyVibrationEnabled,
		AlertTonesEnabled:   settings.AlertTonesEnabled,
	}
	if tracking := settings.ActivityTracking; tracking != nil {
		out.ActivityTracking = &TrackingSettings{
			MoveAlertEnabled:     tracking.MoveAlertEnabled,
			PulseOxSleepTracking: tracking.PulseOxSleepTrackingEnabled,
			HighHRAlertEnabled:   tracking.HighHrAlertEnabled,
			LowHRAlertEnabled:    tracking.LowHrAlertEnabled,
		}
	}

	alarms := settings.Alarms.Items()
	out.AlarmCount = len(alarms)
	for _, alarm := range alarms {
		if alarm.Enabled() {
			out.EnabledAlarmCount++
		}
	}
	return out
}
