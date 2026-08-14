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
