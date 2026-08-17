package client_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func TestWeightConstantsHaveTheirPinnedValues(t *testing.T) {
	t.Parallel()

	paths := []struct {
		got  string
		want string
	}{
		{client.PathWeightUserWeight, "/weight-service/user-weight"},
		{client.PathWeightRangePrefix, "/weight-service/weight/range"},
		{client.PathWeightDayviewPrefix, "/weight-service/weight/dayview"},
		{client.PathWeightDeletePrefix, "/weight-service/weight"},
		{client.PathWeightByVersionSegment, "byversion"},
	}
	for _, tc := range paths {
		if tc.got != tc.want {
			t.Errorf("path = %q, want %q", tc.got, tc.want)
		}
	}

	wireValues := []struct {
		got  string
		want string
	}{
		{client.WeightUnitKg, "kg"},
		{client.WeightUnitLbs, "lbs"},
		{client.WeightSourceManual, "MANUAL"},
	}
	for _, tc := range wireValues {
		if tc.got != tc.want {
			t.Errorf("wire value = %q, want %q", tc.got, tc.want)
		}
	}

	labels := []struct {
		got  client.Endpoint
		want string
	}{
		{client.EndpointWeightUserWeight, "connectapi.weight.user_weight"},
		{client.EndpointWeightRange, "connectapi.weight.range"},
		{client.EndpointWeightDayview, "connectapi.weight.dayview"},
		{client.EndpointWeightDelete, "connectapi.weight.delete"},
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
		{client.OpAddWeighIn, "add_weigh_in"},
		{client.OpAddWeighInWithTimestamps, "add_weigh_in_with_timestamps"},
		{client.OpGetWeighIns, "get_weigh_ins"},
		{client.OpGetDailyWeighIns, "get_daily_weigh_ins"},
		{client.OpDeleteWeighIns, "delete_weigh_ins"},
	}
	for _, tc := range operations {
		if string(tc.got) != tc.want {
			t.Errorf("op = %q, want %q", tc.got, tc.want)
		}
	}
}
