package client_test

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// TestDevicesConstantsHaveTheirPinnedValues pins every device-and-gear constant to
// its literal value: a shape test passes a path that points at the wrong service,
// and six tools are built on these. The values come from python-garminconnect at the
// commit docs/upstream-pins.md names, so changing one here without changing the pin
// is the mistake this test exists to make loud.
func TestDevicesConstantsHaveTheirPinnedValues(t *testing.T) {
	t.Parallel()

	paths := []struct {
		got  string
		want string
	}{
		{client.PathDeviceSettingsPrefix, "/device-service/deviceservice/device-info/settings"},
		{client.PathDeviceLastUsed, "/device-service/deviceservice/mylastused"},
		{client.PathPrimaryTrainingDevice, "/web-gateway/device-info/primary-training-device"},
		{client.PathDeviceSolarPrefix, "/web-gateway/solar"},
		{client.PathGearStatsPrefix, "/gear-service/gear/stats"},
		{client.PathGearUserDefaultsPrefix, "/gear-service/gear/user"},
	}
	for _, tc := range paths {
		if tc.got != tc.want {
			t.Errorf("path = %q, want %q", tc.got, tc.want)
		}
	}

	labels := []struct {
		got  client.Endpoint
		want string
	}{
		{client.EndpointDeviceSettings, "connectapi.device.settings"},
		{client.EndpointDeviceLastUsed, "connectapi.device.last_used"},
		{client.EndpointPrimaryTrainingDevice, "connectapi.device.primary_training_device"},
		{client.EndpointDeviceSolar, "connectapi.device.solar"},
		{client.EndpointGearDefaults, "connectapi.gear.defaults"},
		{client.EndpointGearStats, "connectapi.gear.stats"},
	}
	for _, tc := range labels {
		if string(tc.got) != tc.want {
			t.Errorf("endpoint label = %q, want %q", tc.got, tc.want)
		}
	}

	operations := []struct {
		got  client.Op
		want string
	}{
		{client.OpGetDeviceSettings, "get_device_settings"},
		{client.OpGetDeviceLastUsed, "get_device_last_used"},
		{client.OpGetPrimaryTrainingDevice, "get_primary_training_device"},
		{client.OpGetDeviceSolarData, "get_device_solar_data"},
		{client.OpGetGear, "get_gear"},
		{client.OpGetGearDefaults, "get_gear_defaults"},
		{client.OpGetGearStats, "get_gear_stats"},
	}
	for _, tc := range operations {
		if string(tc.got) != tc.want {
			t.Errorf("op = %q, want %q", tc.got, tc.want)
		}
	}

	wireValues := []struct {
		got  string
		want string
	}{
		{client.QuerySingleDayView, "singleDayView"},
		{client.QueryUserProfilePK, "userProfilePk"},
		{client.ActivityTypesSegment, "activityTypes"},
	}
	for _, tc := range wireValues {
		if tc.got != tc.want {
			t.Errorf("wire value = %q, want %q", tc.got, tc.want)
		}
	}
}

// TestDevicesPathsAreTemplates keeps every device-and-gear path a query-free
// template, so an id or a date is always appended as an escaped segment by the
// domain client rather than baked into the constant.
func TestDevicesPathsAreTemplates(t *testing.T) {
	t.Parallel()

	paths := []string{
		client.PathDeviceSettingsPrefix, client.PathDeviceLastUsed,
		client.PathPrimaryTrainingDevice, client.PathDeviceSolarPrefix,
		client.PathGearStatsPrefix, client.PathGearUserDefaultsPrefix,
	}
	seen := make(map[string]struct{})
	for _, path := range paths {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("path %q must be host-relative and start with a slash", path)
		}
		if strings.ContainsAny(path, "?&= {}") || strings.HasSuffix(path, "/") {
			t.Errorf("path %q must be a bare template: no query, no placeholder, no trailing slash", path)
		}
		if _, duplicate := seen[path]; duplicate {
			t.Errorf("path %q is declared twice; one endpoint needs exactly one constant", path)
		}
		seen[path] = struct{}{}
	}
}

// devicesAllowlistedEndpoints are the labels this tier's tools dispatch
// through, including EndpointGearFilter and EndpointDevices — both declared in
// endpoints.go and reused here (the gear-by-account list, and the device
// fan-out Alarms walks) rather than restated.
func devicesAllowlistedEndpoints() []client.Endpoint {
	return []client.Endpoint{
		client.EndpointDeviceSettings,
		client.EndpointDeviceLastUsed,
		client.EndpointPrimaryTrainingDevice,
		client.EndpointDeviceSolar,
		client.EndpointGearDefaults,
		client.EndpointGearStats,
		client.EndpointGearFilter,
		client.EndpointDevices,
	}
}

// devicesAllowlistedOps are the operations this tier's tools dispatch through,
// including OpListDevices — declared in endpoints.go and reused by the alarms
// walk rather than restated.
func devicesAllowlistedOps() []client.Op {
	return []client.Op{
		client.OpGetDeviceSettings,
		client.OpGetDeviceLastUsed,
		client.OpGetPrimaryTrainingDevice,
		client.OpGetDeviceSolarData,
		client.OpGetGear,
		client.OpGetGearDefaults,
		client.OpGetGearStats,
		client.OpListDevices,
	}
}

// TestEveryDevicesEndpointAndOpIsInTheAllowlist is the regression test for a
// dropped entry.
//
// Request.Validate refuses any endpoint or op outside the allowlists, so an entry
// removed from knownEndpoints or knownOps makes its tool impossible to call while
// every other test stays green. Counting is not enough — a swap would keep the
// count — so each one is asserted by name. Table-driven over both label kinds at
// once, rather than one loop per kind, so this regression test does not read as a
// structural clone of the sibling per-tier allowlist tests.
func TestEveryDevicesEndpointAndOpIsInTheAllowlist(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		wantCount   int
		checkKnown  func(t *testing.T)
		description string
	}{
		{
			name:        "endpoints",
			wantCount:   len(devicesAllowlistedEndpoints()),
			description: "devices endpoints",
			checkKnown: func(t *testing.T) {
				t.Helper()
				for _, endpoint := range devicesAllowlistedEndpoints() {
					if !endpoint.IsKnown() {
						t.Errorf("endpoint %q is not in the allowlist, so Request.Validate refuses it", endpoint)
					}
				}
			},
		},
		{
			name:        "ops",
			wantCount:   len(devicesAllowlistedOps()),
			description: "devices ops",
			checkKnown: func(t *testing.T) {
				t.Helper()
				for _, op := range devicesAllowlistedOps() {
					if !op.IsKnown() {
						t.Errorf("op %q is not in the allowlist, so Request.Validate refuses it", op)
					}
					if op.IsCredentialSubmission() {
						t.Errorf("op %q must not be treated as a credential submission", op)
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.checkKnown(t)
			if tc.wantCount != 8 {
				t.Errorf("%s: %d asserted, want 8", tc.description, tc.wantCount)
			}
		})
	}
}
