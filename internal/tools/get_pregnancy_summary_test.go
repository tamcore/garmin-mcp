package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture in this file is synthetic; see the header comment in
// get_menstrual_data_for_date_test.go.

func TestGetPregnancySummarySanitizesTheDocument(t *testing.T) {
	t.Parallel()

	fixture := `{"status":"PREGNANT","userProfilePk":900001}`
	script := testkit.NewScript().With(client.PathPregnancySnapshot, testkit.JSON(http.StatusOK, fixture))
	h := newWomensHealthHarness(t, script)

	result := h.call(t, ToolGetPregnancySummary, nil)
	if got, ok := result["has_data"].(bool); !ok || !got {
		t.Errorf("has_data = %v, want true", result["has_data"])
	}

	document, ok := result["document"].(map[string]any)
	if !ok {
		t.Fatalf("document = %#v, want a structured object", result["document"])
	}
	if _, present := document["userProfilePk"]; present {
		t.Errorf("document %v still carries the identifying key userProfilePk", document)
	}
	if document["status"] != "PREGNANT" {
		t.Errorf("document = %v, want status PREGNANT preserved", document)
	}
	if got := number(t, result, "dropped_fields"); got != 1 {
		t.Errorf("dropped_fields = %v, want 1", got)
	}
}

func TestGetPregnancySummaryReportsNoDataForAnEmptyBody(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathPregnancySnapshot, testkit.Behavior{Status: http.StatusNoContent})
	h := newWomensHealthHarness(t, script)

	result := h.call(t, ToolGetPregnancySummary, nil)
	if got, ok := result["has_data"].(bool); ok && got {
		t.Errorf("has_data = true for an empty body, want false")
	}
	if _, present := result["document"]; present {
		t.Errorf("document = %v, want absent for an empty body", result["document"])
	}
}

func TestGetPregnancySummaryTakesNoArguments(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathPregnancySnapshot, testkit.JSON(http.StatusOK, `{"status":"PREGNANT"}`))
	h := newWomensHealthHarness(t, script)

	// A conformant client sends an empty object, not an omitted field; the tool
	// must not require anything more.
	h.call(t, ToolGetPregnancySummary, map[string]any{})

	contract := getPregnancySummaryContract()
	if got := contract.Schema.Required(); len(got) != 0 {
		t.Errorf("required = %v, want none", got)
	}
}

func TestGetPregnancySummaryResultIsNotLoggable(t *testing.T) {
	t.Parallel()

	result := PregnancySummary{
		HasData: true,
		Document: map[string]any{
			"status":  "PREGNANT_STATUS_MARKER",
			"dueDate": "2026-03-14",
		},
	}
	assertToolResultNotLoggable(t, result, "PREGNANT_STATUS_MARKER", "2026-03-14")
}
