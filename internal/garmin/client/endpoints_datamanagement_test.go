package client_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func TestDataManagementConstantsHaveTheirPinnedValues(t *testing.T) {
	t.Parallel()

	checks := []struct {
		got  string
		want string
	}{
		{client.PathUpload, "/upload-service/upload"},
		{client.PathBloodPressureSet, "/bloodpressure-service/bloodpressure"},
		{client.PathHydrationSet, "/usersummary-service/usersummary/hydration/log"},
		{string(client.EndpointUpload), "connectapi.upload"},
		{string(client.EndpointBloodPressureSet), "connectapi.bloodpressure.set"},
		{string(client.EndpointHydrationSet), "connectapi.hydration.set"},
		{string(client.OpAddBodyComposition), "add_body_composition"},
		{string(client.OpSetBloodPressure), "set_blood_pressure"},
		{string(client.OpAddHydrationData), "add_hydration_data"},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("got = %q, want %q", tc.got, tc.want)
		}
	}
}
