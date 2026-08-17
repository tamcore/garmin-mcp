package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetDeviceSolarData is the upstream compatibility name of the device
// solar-charging read.
const ToolGetDeviceSolarData = "get_device_solar_data"

// DeviceSolarDayEntry is one day of solar-charging data.
//
// Source: the curation in devices.py's get_device_solar_data tool (devices.py:250-262):
// day_data.get("calendarDate"), day_data.get("solarIntensityAvg"),
// day_data.get("solarIntensityMax"), day_data.get("batteryCharged"),
// day_data.get("batteryUsed") and day_data.get("batteryNet").
type DeviceSolarDayEntry struct {
	Date                  *string  `json:"date,omitempty" jsonschema:"the calendar day, YYYY-MM-DD"`
	SolarIntensityAvg     *float64 `json:"solar_intensity_avg,omitempty" jsonschema:"the average solar intensity"`
	SolarIntensityMax     *float64 `json:"solar_intensity_max,omitempty" jsonschema:"the peak solar intensity"`
	BatteryChargedPercent *float64 `json:"battery_charged_percent,omitempty" jsonschema:"battery percent charged by solar"`
	BatteryUsedPercent    *float64 `json:"battery_used_percent,omitempty" jsonschema:"battery percent used"`
	BatteryNetPercent     *float64 `json:"battery_net_percent,omitempty" jsonschema:"the net battery percent change"`
}

// DeviceSolarResult is one device's solar-charging data for one day.
//
// Source: devices.py's get_device_solar_data tool (devices.py:264-266), which wraps
// the curated days under {"device_id": device_id, "solar_data": curated_days}. A
// device with no solar capability, or a device the account does not own, both answer
// with an empty list, which is a normal state and not distinguished further: neither
// pinned source classifies the two cases apart.
//
// It is device material: the identifier names a person's registered hardware, so it
// is returned to the authorized caller and never logged.
type DeviceSolarResult struct {
	DeviceID  string                `json:"device_id" jsonschema:"the device this solar data belongs to"`
	SolarData []DeviceSolarDayEntry `json:"solar_data" jsonschema:"the solar-charging days"`
	Count     int                   `json:"count" jsonschema:"how many days this result carries"`

	// Truncated reports that the response carried more days than this server's
	// bound retains. The tool contract exposes only a single-day read, so this
	// is generous headroom rather than an expected event.
	Truncated bool `json:"truncated" jsonschema:"whether the days were cut at this server's bound"`
}

// LogValue reports counts only, never a device identifier.
func (d DeviceSolarResult) LogValue() slog.Value {
	return shape("deviceSolarResult",
		slog.Int("days", d.Count),
		slog.Bool("truncated", d.Truncated),
	)
}

// deviceSolarInput is the argument set: a device identifier and one calendar day.
type deviceSolarInput struct {
	DeviceID string `json:"device_id" jsonschema:"the Garmin device identifier"`
	Date     string `json:"date" jsonschema:"the day to read, YYYY-MM-DD"`
}

// deviceIDStringProperty declares the required, plain-string device identifier
// get_device_solar_data takes. Unlike get_device_settings's device_id, the manifest
// declares this one as a single string type, with no numeric alternative and no
// default.
func deviceIDStringProperty() Property {
	return Property{
		Name:        argDeviceID,
		Types:       []string{typeString},
		Description: "the Garmin device identifier, from get_devices",
		MaxLength:   new(maxIdentifierArgumentLen),
		Required:    true,
	}
}

func getDeviceSolarDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDeviceSolarData,
			Title: "Get device solar data",
			Description: "read one day of solar-charging data for a solar-capable " +
				"device: average and peak solar intensity, and the battery percent " +
				"charged, used and net. A device with no solar capability returns an " +
				"empty list, which is a normal state",
			Tier:        policy.TierReadOnly,
			Category:    categoryDevice,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(deviceIDStringProperty(), dateProperty("date", "the day to read")),
	}
}

// registerGetDeviceSolarData registers the tool.
func registerGetDeviceSolarData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deviceSolarInput) (
		*mcp.CallToolResult, DeviceSolarResult, error,
	) {
		id, err := identifierFromText(argDeviceID, in.DeviceID)
		if err != nil {
			return nil, DeviceSolarResult{}, err
		}
		day, err := parseCalendarDate("date", in.Date)
		if err != nil {
			return nil, DeviceSolarResult{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DeviceSolarResult{}, err
		}
		days, truncated, err := svc.devices.SolarData(ctx, session, id, day)
		if err != nil {
			return nil, DeviceSolarResult{}, fail(err)
		}
		return nil, newDeviceSolarResult(in.DeviceID, days, truncated), nil
	}
	return mcpserver.AddTool(registry, getDeviceSolarDataContract().Registration(), handler)
}

// newDeviceSolarResult maps the domain days onto the result. The device identifier
// is echoed back verbatim as the caller sent it, matching upstream's own
// {"device_id": device_id, ...} wrapper (devices.py:264-266).
func newDeviceSolarResult(deviceID string, days []api.DeviceSolarDay, truncated bool) DeviceSolarResult {
	out := make([]DeviceSolarDayEntry, 0, len(days))
	for _, day := range days {
		out = append(out, DeviceSolarDayEntry{
			Date:                  day.CalendarDate,
			SolarIntensityAvg:     optionalFloat(day.SolarIntensityAvg),
			SolarIntensityMax:     optionalFloat(day.SolarIntensityMax),
			BatteryChargedPercent: optionalFloat(day.BatteryCharged),
			BatteryUsedPercent:    optionalFloat(day.BatteryUsed),
			BatteryNetPercent:     optionalFloat(day.BatteryNet),
		})
	}
	return DeviceSolarResult{DeviceID: deviceID, SolarData: out, Count: len(out), Truncated: truncated}
}
