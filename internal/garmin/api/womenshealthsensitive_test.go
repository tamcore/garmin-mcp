package api_test

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// womensHealthLogNeedles are fixture values a women's-health log line must never
// contain: an invented cycle phase, symptom, note and date, each at least five
// characters so it cannot collide with a log line's own timestamp digits. Every
// value is invented; none is a recording of a real account.
func womensHealthLogNeedles() []string {
	return []string{"LUTEAL_PHASE", "HEADACHE_SYMPTOM", "cramping and fatigue", "2026-03-14", "PREGNANT_STATUS"}
}

// collectWomensHealthModels builds one fixture of every women's-health model.
// Every document is invented: no field in it is pinned by any source (see
// WomensHealth's doc comment in womenshealth.go), so the fixture only has to
// prove that whatever bytes Garmin might send never reach a log line.
func collectWomensHealthModels(t *testing.T) map[string]any {
	t.Helper()

	dayView := mustDecodeModel[api.MenstrualDay](t,
		`{"cycleDay":14,"phase":"LUTEAL_PHASE","symptoms":["HEADACHE_SYMPTOM"],`+
			`"note":"cramping and fatigue","date":"2026-03-14"}`)

	calendar := mustDecodeModel[api.MenstrualCalendar](t,
		`{"cycles":[{"phase":"LUTEAL_PHASE","startDate":"2026-03-14",`+
			`"note":"cramping and fatigue"}]}`)

	pregnancy := mustDecodeModel[api.PregnancySummary](t,
		`{"status":"PREGNANT_STATUS","dueDate":"2026-03-14","note":"cramping and fatigue"}`)

	return map[string]any{
		"MenstrualDay":      dayView,
		"MenstrualCalendar": calendar,
		"PregnancySummary":  pregnancy,
	}
}

// TestWomensHealthModelsAreNotLoggable proves that handing a women's-health model
// to slog reports its shape only, never a cycle phase, a symptom, a note or a
// date. This is the most sensitive category this project handles, so the test
// enforces it directly rather than relying on TestChallengesModelsAreNotLoggable's
// broader sweep to catch a regression here.
func TestWomensHealthModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	for name, value := range collectWomensHealthModels(t) {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin read", "model", value)

		rendered := logged.String()
		for _, needle := range womensHealthLogNeedles() {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}

// TestWomensHealthDocumentFieldItselfIsNeverLogged proves the raw json.RawMessage
// Document field is not what LogValue reports: only the redacted retained
// payload reaches the log line, which is what keeps a caller who mistakenly logs
// a whole result safe even though Document is exported.
func TestWomensHealthDocumentFieldItselfIsNeverLogged(t *testing.T) {
	t.Parallel()

	document := json.RawMessage(`{"phase":"LUTEAL_PHASE","note":"cramping and fatigue"}`)
	model := api.MenstrualDay{Document: document}

	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin read", "model", model)

	rendered := logged.String()
	if strings.Contains(rendered, "LUTEAL_PHASE") || strings.Contains(rendered, "cramping and fatigue") {
		t.Errorf("logging MenstrualDay leaks the raw document: %s", rendered)
	}
}
