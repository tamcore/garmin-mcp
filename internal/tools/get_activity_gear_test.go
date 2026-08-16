package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// gearItemJSON renders one synthetic gear item.
func gearItemJSON(index int) string {
	suffix := strconv.Itoa(index)
	return `{"uuid":"1f3c9d2a-0000-4000-8000-abcdef01234` + strconv.Itoa(index%10) + `",` +
		`"gearPk":` + strconv.Itoa(4400+index) + `,"displayName":"Synthetic Shoes ` + suffix + `",` +
		`"customMakeModel":"Synth Runner","gearMakeName":"Synth","gearModelName":"Runner",` +
		`"gearTypeName":"Shoes","gearStatusName":"active",` +
		`"dateBegin":"2025-01-01T00:00:00.0","dateEnd":null,"gearStatus":{"id":1},` +
		`"unknownField":true}`
}

func gearArray(count int) string {
	items := make([]string, 0, count)
	for index := range count {
		items = append(items, gearItemJSON(index))
	}
	return "[" + strings.Join(items, ",") + "]"
}

// TestGetActivityGearReturnsTheLinkedGear pins the mapping.
func TestGetActivityGearReturnsTheLinkedGear(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGearFilter,
		testkit.JSON(http.StatusOK, gearArray(1)))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetActivityGear, activityIDArgs())

	if got := number(t, result, "activity_id"); got != 987654321 {
		t.Errorf("activity_id = %v, want the requested identifier", got)
	}
	if got := number(t, result, "count"); got != 1 {
		t.Fatalf("count = %v, want 1", got)
	}
	if truncated, _ := result["truncated"].(bool); truncated {
		t.Error("truncated = true, want false for a single item")
	}
	item := entry(t, list(t, result, "gear"), 0)
	assertGearItem(t, item)
}

func assertGearItem(t *testing.T, item map[string]any) {
	t.Helper()

	texts := map[string]string{
		"gear_uuid":         "1f3c9d2a-0000-4000-8000-abcdef012340",
		keyDisplayName:      "Synthetic Shoes 0",
		"custom_make_model": "Synth Runner",
		"make_name":         "Synth",
		"model_name":        "Runner",
		"gear_type_name":    "Shoes",
		"gear_status_name":  valueActive,
		"date_begin":        "2025-01-01T00:00:00.0",
	}
	for key, want := range texts {
		if got, _ := item[key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := number(t, item, "gear_pk"); got != 4400 {
		t.Errorf("gear_pk = %v, want 4400", got)
	}
	if _, present := item["date_end"]; present {
		t.Error("date_end is present, want it omitted while the gear is in service")
	}
}

// TestGetActivityGearSendsTheActivityAsAQueryParameter pins the request shape: the
// identifier is a query parameter, never a path segment.
func TestGetActivityGearSendsTheActivityAsAQueryParameter(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGearFilter,
		testkit.JSON(http.StatusOK, gearArray(1)))
	h := newToolHarness(t, script)

	h.call(t, ToolGetActivityGear, activityIDArgs())

	recorded := h.fake.Requests()
	if len(recorded) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(recorded))
	}
	if recorded[0].Path != client.PathGearFilter {
		t.Errorf("path = %q, want %q", recorded[0].Path, client.PathGearFilter)
	}
	if got := recorded[0].Query.Get(client.QueryActivityID); got != parityActivityID {
		t.Errorf("activityId = %q, want %q", got, parityActivityID)
	}
}

// TestGetActivityGearReportsNoGearAsEmpty proves an activity without gear is a
// normal state.
func TestGetActivityGearReportsNoGearAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGearFilter,
		testkit.JSON(http.StatusOK, `[]`))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetActivityGear, activityIDArgs())
	if got := number(t, result, "count"); got != 0 {
		t.Errorf("count = %v, want 0", got)
	}
}

// TestGetActivityGearTruncatesAnOversizedList proves the bound is applied and
// reported rather than hidden.
func TestGetActivityGearTruncatesAnOversizedList(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGearFilter,
		testkit.JSON(http.StatusOK, gearArray(defaultMaxActivityGear+5)))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetActivityGear, activityIDArgs())

	if got := number(t, result, "count"); got != float64(defaultMaxActivityGear) {
		t.Errorf("count = %v, want the bound %d", got, defaultMaxActivityGear)
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true when the bound was applied")
	}
}

// TestGetActivityGearRefusesAnIdentifierThatIsNotOne proves the argument is
// validated before Garmin is reached.
func TestGetActivityGearRefusesAnIdentifierThatIsNotOne(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript())

	if advice := h.callError(t, ToolGetActivityGear,
		map[string]any{argActivityID: "not-an-id"}); advice == "" {
		t.Error("the refusal carries no advice")
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}
