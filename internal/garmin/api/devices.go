package api

import (
	"context"
	"encoding/json"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Devices reads the account's registered devices.
//
// Source: get_devices over garmin_connect_devices_url
// ("/device-service/deviceregistration/devices"), which answers with a plain array.
type Devices struct {
	req requester
}

// NewDevices returns a device client over the request layer.
func NewDevices(rc *client.Client) (*Devices, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Devices{req: req}, nil
}

// Device is one registered device.
//
// It is sensitive: a serial number and a unit identifier identify a person's
// hardware, so it must not be logged. The identifier is a union decoder because
// Garmin sends it as a number and as a string depending on the endpoint that produced
// the record.
type Device struct {
	DeviceID           client.Number   `json:"deviceId"`
	UnitID             client.Number   `json:"unitId"`
	SerialNumber       *string         `json:"serialNumber"`
	ProductDisplayName *string         `json:"productDisplayName"`
	ProductSKU         *string         `json:"productSku"`
	ApplicationKey     *string         `json:"applicationKey"`
	ImageURL           *string         `json:"imageUrl"`
	DeviceCategories   json.RawMessage `json:"deviceCategories"`
	CurrentFirmware    json.RawMessage `json:"currentFirmwareVersion"`
}

// List reads every registered device.
//
// An empty array and a 204 both yield no devices rather than an error: an account
// with no paired device is a normal state, not a failure.
func (d *Devices) List(ctx context.Context, session client.Session) ([]Device, error) {
	req := readRequest(client.OpListDevices, client.EndpointDevices, client.PathDevices, nil)

	var devices []Device
	if _, err := d.req.read(ctx, session, req, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

// ActivityTracking is the nested activity-tracking-alert settings of one device.
//
// Source: the activity_tracking curation in devices.py's get_device_settings tool
// (devices.py:140-160), which reads exactly these four keys off
// settings.get("activityTracking", {}).
type ActivityTracking struct {
	MoveAlertEnabled            *bool `json:"moveAlertEnabled"`
	PulseOxSleepTrackingEnabled *bool `json:"pulseOxSleepTrackingEnabled"`
	HighHrAlertEnabled          *bool `json:"highHrAlertEnabled"`
	LowHrAlertEnabled           *bool `json:"lowHrAlertEnabled"`
}

// DeviceSettings is one device's configuration document.
//
// Source: the curation in devices.py's get_device_settings tool
// (devices.py:119-172): settings.get("deviceId"), settings.get("timeFormat"),
// settings.get("dateFormat"), settings.get("measurementUnits"),
// settings.get("keyTonesEnabled"), settings.get("keyVibrationEnabled"),
// settings.get("alertTonesEnabled"), settings.get("activityTracking", {}) and
// settings.get("alarms", []).
//
// It is sensitive the same way Device is: a time format is not, but the alarm
// schedule and device identity it carries describe a person's routine.
type DeviceSettings struct {
	DeviceID            client.Number            `json:"deviceId"`
	TimeFormat          *string                  `json:"timeFormat"`
	DateFormat          *string                  `json:"dateFormat"`
	MeasurementUnits    *string                  `json:"measurementUnits"`
	KeyTonesEnabled     *bool                    `json:"keyTonesEnabled"`
	KeyVibrationEnabled *bool                    `json:"keyVibrationEnabled"`
	AlertTonesEnabled   *bool                    `json:"alertTonesEnabled"`
	ActivityTracking    *ActivityTracking        `json:"activityTracking"`
	Alarms              client.List[DeviceAlarm] `json:"alarms"`
}

// Settings reads one device's settings document.
func (d *Devices) Settings(
	ctx context.Context, session client.Session, id client.ID,
) (DeviceSettings, error) {
	req := readRequest(client.OpGetDeviceSettings, client.EndpointDeviceSettings,
		devicePath(client.PathDeviceSettingsPrefix, id), nil)
	if err := requireID(req, id); err != nil {
		return DeviceSettings{}, err
	}

	var settings DeviceSettings
	if _, err := d.req.read(ctx, session, req, &settings); err != nil {
		return DeviceSettings{}, err
	}
	return settings, nil
}

// devicePath appends a validated device identifier as one path segment.
func devicePath(prefix string, id client.ID) string {
	return prefix + "/" + id.String()
}

// DeviceLastUsed is the most-recently-used device document, which also carries
// the account's own numeric profile id — the identifier get_gear needs and no
// gear tool argument may carry directly (AGENTS.md: never accept a user_id
// argument; the profile id is looked up through this read instead).
//
// Source: the curation in devices.py's get_device_last_used tool
// (devices.py:70-88): device.get("userDeviceId"),
// device.get("lastUsedDeviceName"), device.get("lastUsedDeviceApplicationKey"),
// device.get("userProfileNumber"), device.get("lastUsedDeviceUploadTime") and
// device.get("imageUrl").
type DeviceLastUsed struct {
	UserDeviceID                 client.Number `json:"userDeviceId"`
	LastUsedDeviceName           *string       `json:"lastUsedDeviceName"`
	LastUsedDeviceApplicationKey *string       `json:"lastUsedDeviceApplicationKey"`
	UserProfileNumber            client.Number `json:"userProfileNumber"`
	LastUsedDeviceUploadTime     client.Number `json:"lastUsedDeviceUploadTime"`
	ImageURL                     *string       `json:"imageUrl"`
}

// LastUsed reads the most-recently-used device document.
func (d *Devices) LastUsed(ctx context.Context, session client.Session) (DeviceLastUsed, error) {
	req := readRequest(client.OpGetDeviceLastUsed, client.EndpointDeviceLastUsed,
		client.PathDeviceLastUsed, nil)

	var last DeviceLastUsed
	if _, err := d.req.read(ctx, session, req, &last); err != nil {
		return DeviceLastUsed{}, err
	}
	return last, nil
}
