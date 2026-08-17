package client_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// TestWomensHealthConstantsHaveTheirPinnedValues pins every women's-health constant
// to the literal it must carry. A path that is host-relative and free of query
// text passes a shape test while pointing at the wrong Garmin service, and three
// of the most sensitive tools on this surface are built on these.
func TestWomensHealthConstantsHaveTheirPinnedValues(t *testing.T) {
	t.Parallel()

	paths := []struct {
		got  string
		want string
	}{
		{client.PathMenstrualCalendarPrefix, "/periodichealth-service/menstrualcycle/calendar"},
		{client.PathMenstrualDayviewPrefix, "/periodichealth-service/menstrualcycle/dayview"},
		{client.PathPregnancySnapshot, "/periodichealth-service/menstrualcycle/pregnancysnapshot"},
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
		{client.EndpointMenstrualCalendar, "connectapi.womenshealth.menstrual_calendar"},
		{client.EndpointMenstrualDayview, "connectapi.womenshealth.menstrual_dayview"},
		{client.EndpointPregnancySnapshot, "connectapi.womenshealth.pregnancy_snapshot"},
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
		{client.OpGetMenstrualCalendarData, "get_menstrual_calendar_data"},
		{client.OpGetMenstrualDataForDate, "get_menstrual_data_for_date"},
		{client.OpGetPregnancySummary, "get_pregnancy_summary"},
	}
	for _, tc := range operations {
		if string(tc.got) != tc.want {
			t.Errorf("op = %q, want %q", tc.got, tc.want)
		}
	}
}

// TestEveryWomensHealthEndpointAndOpIsInTheAllowlist is the regression test for a
// dropped entry.
//
// Request.Validate refuses any endpoint or op outside the allowlists, so an entry
// removed from knownEndpoints or knownOps makes its tool impossible to call while
// every other test stays green. Counting is not enough — a swap would keep the
// count — so each one is asserted by name.
func TestEveryWomensHealthEndpointAndOpIsInTheAllowlist(t *testing.T) {
	t.Parallel()

	endpoints := []client.Endpoint{
		client.EndpointMenstrualCalendar,
		client.EndpointMenstrualDayview,
		client.EndpointPregnancySnapshot,
	}
	for _, endpoint := range endpoints {
		if !endpoint.IsKnown() {
			t.Errorf("endpoint %q is not in the allowlist, so Request.Validate refuses it", endpoint)
		}
	}

	operations := []client.Op{
		client.OpGetMenstrualCalendarData,
		client.OpGetMenstrualDataForDate,
		client.OpGetPregnancySummary,
	}
	for _, op := range operations {
		if !op.IsKnown() {
			t.Errorf("op %q is not in the allowlist, so Request.Validate refuses it", op)
		}
	}
}
