package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const deviceSerialSentinel = "SENTINEL-SERIAL-0001"

const devicesBody = `[{"deviceId":4242,"productDisplayName":"Fake Watch 9","serialNumber":"` +
	deviceSerialSentinel + `","applicationKey":"fake","imageUrl":null,"futureField":{"x":1}},` +
	`{"deviceId":"4243","productDisplayName":"Fake Strap"}]`

func newDevices(t *testing.T, h harness) *api.Devices {
	t.Helper()

	devices, err := api.NewDevices(h.rc)
	if err != nil {
		t.Fatalf("NewDevices() = %v", err)
	}
	return devices
}

func TestDevicesListDecodesAPlainArray(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDevices, testkit.JSON(http.StatusOK, devicesBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).List(t.Context(), h.session)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("%d devices decoded, want 2", len(got))
	}
	if id, ok := got[0].DeviceID.Int64(); !ok || id != 4242 {
		t.Errorf("DeviceID = %v/%v, want 4242", id, ok)
	}
	// The second device sends its id as a string, which the union decoder tolerates.
	if id, ok := got[1].DeviceID.Int64(); !ok || id != 4243 {
		t.Errorf("second DeviceID = %v/%v, want 4243 from the string form", id, ok)
	}
	if got[0].ProductDisplayName == nil || *got[0].ProductDisplayName != "Fake Watch 9" {
		t.Errorf("ProductDisplayName = %v, want the decoded value", got[0].ProductDisplayName)
	}
	if got[0].SerialNumber == nil || *got[0].SerialNumber != deviceSerialSentinel {
		t.Errorf("SerialNumber = %v, want the decoded value", got[0].SerialNumber)
	}
	if got[0].ImageURL != nil {
		t.Error("ImageURL must be nil for an explicit null")
	}
}

func TestDevicesListNormalizesAnEmptyResponse(t *testing.T) {
	t.Parallel()

	cases := map[string]testkit.Behavior{
		"empty array": testkit.JSON(http.StatusOK, `[]`),
		"no content":  {Status: http.StatusNoContent},
	}

	for name, behavior := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(client.PathDevices, behavior)
			h := newHarness(t, script, client.Limits{})

			got, err := newDevices(t, h).List(t.Context(), h.session)
			if err != nil {
				t.Fatalf("List() = %v, want an empty result rather than an error", err)
			}
			if len(got) != 0 {
				t.Errorf("%d devices decoded, want 0", len(got))
			}
		})
	}
}

func TestDevicesListMapsAServerErrorOntoAPIError(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDevices,
		testkit.JSON(http.StatusInternalServerError, `{"message":"synthetic"}`))
	h := newHarness(t, script, client.Limits{MaxAttempts: 1})

	_, err := newDevices(t, h).List(t.Context(), h.session)
	if !errors.Is(err, client.ErrServer) {
		t.Fatalf("List() = %v, want ErrServer", err)
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("the failure is not an *APIError")
	}
	if apiErr.Endpoint != client.EndpointDevices {
		t.Errorf("Endpoint = %q, want the devices label", apiErr.Endpoint)
	}
}
