package api

import (
	"context"
	"net/url"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// DeviceSolarDay is one day of solar-charging data for a solar-capable device.
//
// Source: the curation in devices.py's get_device_solar_data tool
// (devices.py:250-262): day.get("calendarDate"), day.get("solarIntensityAvg"),
// day.get("solarIntensityMax"), day.get("batteryCharged"),
// day.get("batteryUsed") and day.get("batteryNet").
type DeviceSolarDay struct {
	CalendarDate      *string       `json:"calendarDate"`
	SolarIntensityAvg client.Number `json:"solarIntensityAvg"`
	SolarIntensityMax client.Number `json:"solarIntensityMax"`
	BatteryCharged    client.Number `json:"batteryCharged"`
	BatteryUsed       client.Number `json:"batteryUsed"`
	BatteryNet        client.Number `json:"batteryNet"`
}

// deviceSolarEnvelope is the response envelope: the tool's own
// garmin_client.get_device_solar_data return is resp["deviceSolarInput"]
// (garminconnect __init__.py's get_device_solar_data, the deviceSolarInput
// membership check), and devices.py's tool then reads
// solar_data.get("solarDailyDataDTOs", []) off that same value
// (devices.py:245-247) — so the two pinned sources together evidence a nested
// object, not a bare array.
type deviceSolarEnvelope struct {
	Input *struct {
		Days client.List[DeviceSolarDay] `json:"solarDailyDataDTOs"`
	} `json:"deviceSolarInput"`
}

// maxDeviceSolarDays bounds the days this package will retain from one solar
// read. The tool contract requests a single day, so this is generous headroom
// against a server that answers with more than asked, not a limit any real
// single-day request approaches.
const maxDeviceSolarDays = 64

// SolarData reads one day of solar-charging data for date, requesting the
// single-day view upstream sends when no end date is given — the only mode
// this project's tool contract exposes. It reports whether the response
// carried more days than maxDeviceSolarDays retains.
func (d *Devices) SolarData(
	ctx context.Context, session client.Session, id client.ID, date client.Date,
) ([]DeviceSolarDay, bool, error) {
	req := readRequest(client.OpGetDeviceSolarData, client.EndpointDeviceSolar,
		solarPath(id, date), solarQuery())
	if err := requireID(req, id); err != nil {
		return nil, false, err
	}
	if err := requireDate(req, date); err != nil {
		return nil, false, err
	}

	var envelope deviceSolarEnvelope
	if _, err := d.req.read(ctx, session, req, &envelope); err != nil {
		return nil, false, err
	}
	if envelope.Input == nil {
		return nil, false, nil
	}

	days := envelope.Input.Days.Items()
	if len(days) > maxDeviceSolarDays {
		return days[:maxDeviceSolarDays], true, nil
	}
	return days, false, nil
}

// solarPath builds the device/start-date/end-date solar path. The tool
// contract's single date is sent as both segments, matching upstream's
// enddate-is-None branch.
func solarPath(id client.ID, date client.Date) string {
	return client.PathDeviceSolarPrefix + "/" + id.String() + "/" + date.String() + "/" + date.String()
}

// solarQuery renders the fixed single-day-view parameter.
func solarQuery() url.Values {
	query := url.Values{}
	query.Set(client.QuerySingleDayView, client.IncludeContentTrue)
	return query
}
