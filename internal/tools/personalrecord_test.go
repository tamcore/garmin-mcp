package tools

import (
	"encoding/json"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

func decodeRecords(t *testing.T, body string) []api.PersonalRecord {
	t.Helper()
	var records []api.PersonalRecord
	if err := json.Unmarshal([]byte(body), &records); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return records
}

func TestPersonalRecordsCarryTheirTypeUnitAndDisplayValue(t *testing.T) {
	cases := map[string]struct {
		body, recordType, unit, display string
	}{
		"5K time":         {`[{"typeId":3,"value":1133.6}]`, "Fastest 5K", unitSeconds, "18:53"},
		"marathon time":   {`[{"typeId":6,"value":10804.6}]`, "Fastest Marathon", unitSeconds, "3:00:04"},
		"long distance":   {`[{"typeId":7,"value":63863.3}]`, "Longest Run", unitMeters, "63.86 km"},
		"short distance":  {`[{"typeId":17,"value":950}]`, "Longest Pool Swim", unitMeters, "950 m"},
		"elevation":       {`[{"typeId":9,"value":258.58}]`, "Most Elevation Gain Cycling", unitMeters, "259 m"},
		"step count":      {`[{"typeId":13,"value":203935}]`, "Most Steps Week", unitCount, "203935"},
		"goal streak":     {`[{"typeId":15,"value":241}]`, "Longest Daily Goal Streak", unitDays, "241 days"},
		"unknown type id": {`[{"typeId":45,"value":29000}]`, recordTypeUnknown, "", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := newPersonalRecordList(decodeRecords(t, tc.body), DefaultMaxPersonalRecords).Records[0]

			if got.RecordType == nil || *got.RecordType != tc.recordType {
				t.Errorf("record_type = %v, want %q", got.RecordType, tc.recordType)
			}
			if tc.unit == "" && (got.Unit != nil || got.Display != nil) {
				t.Errorf("unknown type got unit %v, display %v, want neither", got.Unit, got.Display)
			}
			if tc.unit != "" && (got.Unit == nil || *got.Unit != tc.unit) {
				t.Errorf("unit = %v, want %q", got.Unit, tc.unit)
			}
			if tc.display != "" && (got.Display == nil || *got.Display != tc.display) {
				t.Errorf("display = %v, want %q", got.Display, tc.display)
			}
		})
	}
}

func TestPersonalRecordStartTimeIsRFC3339(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"epoch milliseconds": {`[{"typeId":3,"prStartTimeGmt":1617875688000}]`, "2021-04-08T09:54:48Z"},
		"already a string":   {`[{"typeId":3,"prStartTimeGmt":"2026-01-10T06:00:00.0"}]`, "2026-01-10T06:00:00.0"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := newPersonalRecordList(decodeRecords(t, tc.body), DefaultMaxPersonalRecords).Records[0]

			if got.StartGMT == nil || *got.StartGMT != tc.want {
				t.Errorf("start_time_gmt = %v, want %q", got.StartGMT, tc.want)
			}
		})
	}
}
