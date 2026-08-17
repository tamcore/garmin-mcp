package api_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// weightLogNeedles reports the fixture values a weight-management log line
// must never contain: a distinctive weight, body-composition figure, date and
// timestamp. Every needle is at least 5 characters, so it cannot collide with
// a log line's own timestamp attribute. A function, not a package-level var:
// AGENTS.md allows no package-level mutable state.
func weightLogNeedles() []string {
	return []string{
		"81234", "27766", "58123", "45123", "34123",
		"2026-03-15", "998877", "T09:15:00",
	}
}

// collectWeightModels builds one fixture of every weight-management model
// that carries a weight, a body-composition figure, a date or a timestamp.
// The read models are decoded from a literal JSON document quoting the
// weight_management.py/0310-__init__.py lines that evidence their field
// spellings, never built from this package's own struct tags.
func collectWeightModels(t *testing.T) map[string]any {
	t.Helper()

	// w.get("calendarDate"), w.get("weight"), w.get("bmi"), w.get("bodyFat"),
	// w.get("bodyWater"), w.get("boneMass"), w.get("muscleMass"),
	// w.get("sourceType"), w.get("timestampGMT") (weight_management.py:53-65),
	// and samplePk (garminconnect/__init__.py:1343).
	measurement := mustDecodeModel[api.WeighInMeasurement](t,
		`{"calendarDate":"2026-03-15","weight":81234,"bmi":23.4,"bodyFat":18.2,`+
			`"bodyWater":58123,"boneMass":34123,"muscleMass":45123,"sourceType":"MANUAL",`+
			`"timestampGMT":"2026-03-15T09:15:00.0","samplePk":998877}`)

	// total_avg.get("weight") (weight_management.py:76-79).
	average := mustDecodeModel[api.WeighInAverage](t, `{"weight":27766}`)

	rangeResult := api.WeighInRange{TotalAverage: &average}
	daily := api.DailyWeighIns{DateWeightList: []api.WeighInMeasurement{measurement}, TotalAverage: &average}

	entry := api.WeighInEntry{
		Weight: 81234, Unit: api.WeightUnitKg,
		LocalAt: time.Date(2026, 3, 15, 9, 15, 0, 0, time.UTC),
		GMTAt:   time.Date(2026, 3, 15, 9, 15, 0, 0, time.UTC),
	}

	return map[string]any{
		"WeighInMeasurement": measurement,
		"WeighInAverage":     average,
		"WeighInRange":       rangeResult,
		"DailyWeighIns":      daily,
		"WeighInEntry":       entry,
		"DeleteWeighInsResult": api.DeleteWeighInsResult{
			Deleted: []api.WeighInDeletion{{}},
		},
	}
}

// TestWeightModelsAreNotLoggable proves that handing a weight-management model
// to slog reports its shape only, never a weight, a body-composition figure, a
// date or a timestamp.
func TestWeightModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	for name, value := range collectWeightModels(t) {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin read", "model", value)

		rendered := logged.String()
		for _, needle := range weightLogNeedles() {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}
