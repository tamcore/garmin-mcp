package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// activityTypeRows renders count synthetic catalog rows. typeId arrives as a string
// on the second row on purpose: Garmin sends the same field both ways.
func activityTypeRows(count int) string {
	rows := make([]string, 0, count)
	for index := range count {
		id := strconv.Itoa(index + 1)
		typeID := id
		if index == 1 {
			typeID = `"` + id + `"`
		}
		rows = append(rows, `{"typeId":`+typeID+`,"typeKey":"synthetic_type_`+id+`",`+
			`"parentTypeId":17,"isHidden":false,"sortOrder":`+id+`,"unknownField":true}`)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

// TestGetActivityTypesReturnsTheCatalog pins the mapping.
func TestGetActivityTypesReturnsTheCatalog(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivityTypes,
		testkit.JSON(http.StatusOK, activityTypeRows(2)))
	h := newParityHarness(t, script)

	result := h.call(t, ToolGetActivityTypes, nil)

	if got := number(t, result, "count"); got != 2 {
		t.Fatalf("count = %v, want 2", got)
	}
	if truncated, _ := result["truncated"].(bool); truncated {
		t.Error("truncated = true, want false for a short catalog")
	}
	rows := list(t, result, "activity_types")
	first := entry(t, rows, 0)
	if got, _ := first["type_key"].(string); got != "synthetic_type_1" {
		t.Errorf("type_key = %q, want synthetic_type_1", got)
	}
	if got := number(t, first, "type_id"); got != 1 {
		t.Errorf("type_id = %v, want 1", got)
	}
	if got := number(t, first, "parent_type_id"); got != 17 {
		t.Errorf("parent_type_id = %v, want 17", got)
	}
	if got := number(t, first, "sort_order"); got != 1 {
		t.Errorf("sort_order = %v, want 1", got)
	}
	if hidden, present := first["is_hidden"].(bool); !present || hidden {
		t.Errorf("is_hidden = %v, want false and present", first["is_hidden"])
	}
	if got := number(t, entry(t, rows, 1), "type_id"); got != 2 {
		t.Errorf("the numeric-string type_id decoded to %v, want 2", got)
	}
}

// TestGetActivityTypesReadsTheCatalogPath pins the endpoint.
func TestGetActivityTypesReadsTheCatalogPath(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivityTypes,
		testkit.JSON(http.StatusOK, activityTypeRows(1)))
	h := newParityHarness(t, script)

	h.call(t, ToolGetActivityTypes, nil)

	recorded := h.fake.Requests()
	if len(recorded) != 1 || recorded[0].Path != client.PathActivityTypes {
		t.Fatalf("requests = %+v, want one read of %q", recorded, client.PathActivityTypes)
	}
	if recorded[0].Method != http.MethodGet {
		t.Errorf("method = %q, want GET", recorded[0].Method)
	}
}

// TestGetActivityTypesReportsAnEmptyCatalogAsEmpty proves an empty answer is not a
// failure.
func TestGetActivityTypesReportsAnEmptyCatalogAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivityTypes,
		testkit.JSON(http.StatusOK, `[]`))
	h := newParityHarness(t, script)

	if got := number(t, h.call(t, ToolGetActivityTypes, nil), "count"); got != 0 {
		t.Errorf("count = %v, want 0", got)
	}
}

// TestGetActivityTypesTakesNoArguments proves the schema carries no account
// selector, and the tool no arguments at all.
func TestGetActivityTypesTakesNoArguments(t *testing.T) {
	t.Parallel()

	if got := len(getActivityTypesContract().Schema.Properties()); got != 0 {
		t.Errorf("get_activity_types declares %d properties, want 0", got)
	}
}

// TestGetActivityTypesSanitizesAGarminFailure proves an upstream failure comes back
// as authored advice.
func TestGetActivityTypesSanitizesAGarminFailure(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivityTypes,
		testkit.JSON(http.StatusInternalServerError, `{"trace":"internal-detail"}`))
	h := newParityHarness(t, script)

	if advice := h.callError(t, ToolGetActivityTypes, nil); strings.Contains(advice, "internal-detail") {
		t.Errorf("the refusal %q carries the upstream payload", advice)
	}
}
