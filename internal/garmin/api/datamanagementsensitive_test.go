package api_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// dataManagementLogNeedles reports fixture values a data-management log line
// must never contain, each at least 5 characters.
func dataManagementLogNeedles() []string {
	return []string{"88812", "77714", "99913", "distinctive notes text", "2026-04-05"}
}

func collectDataManagementModels(t *testing.T) map[string]any {
	t.Helper()

	at := time.Date(2026, 4, 5, 8, 0, 0, 0, time.UTC)
	percent := 88.812
	return map[string]any{
		"BodyCompositionEntry": api.BodyCompositionEntry{
			At: at, Weight: 77.714, PercentFat: &percent,
		},
		"BloodPressureEntry": api.BloodPressureEntry{
			Systolic: 128, Diastolic: 88, Pulse: 74, // no single field spells a needle
			Notes: "distinctive notes text", At: at,
		},
		"HydrationEntry": api.HydrationEntry{
			ValueInML: 999.13, Date: mustDate(t, "2026-04-05"), At: at,
		},
	}
}

// TestDataManagementModelsAreNotLoggable proves that handing a
// data-management model to slog reports its shape only, never a weight, a
// percentage, a blood-pressure reading, notes, a hydration volume or a
// timestamp.
func TestDataManagementModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	for name, value := range collectDataManagementModels(t) {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin write", "model", value)

		rendered := logged.String()
		for _, needle := range dataManagementLogNeedles() {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}
