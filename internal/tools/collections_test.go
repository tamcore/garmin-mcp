// Behaviour tests for the collection-shaped tools: the plain device array and the
// two union-typed activity-detail endpoints.
package tools_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

func TestGetDevicesReturnsThePlainArray(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetDevices, nil)

	devices, ok := got["devices"].([]any)
	if !ok {
		t.Fatalf("devices = %T, want an array", got["devices"])
	}
	if len(devices) != 2 {
		t.Fatalf("%d devices, want 2", len(devices))
	}
	if got["count"] != float64(2) {
		t.Errorf("count = %v, want 2", got["count"])
	}

	first, _ := devices[0].(map[string]any)
	if first["product_display_name"] != "Fake Forerunner" {
		t.Errorf("product_display_name = %v, want %q",
			first["product_display_name"], "Fake Forerunner")
	}
	if first["device_id"] != float64(3001) {
		t.Errorf("device_id = %v, want 3001", first["device_id"])
	}
}

func TestGetDevicesReportsAnEmptyAccountAsAnEmptyList(t *testing.T) {
	script := testkit.NewScript().With(client.PathDevices, testkit.JSON(http.StatusOK, `[]`))
	h := newHarness(t, script)

	got := h.call(t, tools.ToolGetDevices, nil)

	if got["count"] != float64(0) {
		t.Errorf("count = %v, want 0: an account with no paired device is normal", got["count"])
	}
}

func TestGetDevicesTruncatesAtItsBound(t *testing.T) {
	h := newHarnessWith(t, readScript(), tools.Bounds{MaxDevices: 1}, client.Limits{})

	got := h.call(t, tools.ToolGetDevices, nil)

	devices, _ := got["devices"].([]any)
	if len(devices) != 1 {
		t.Fatalf("%d devices, want the bound of 1", len(devices))
	}
	if got["truncated"] != true {
		t.Errorf("truncated = %v, want true", got["truncated"])
	}
}

func TestGetActivityTypedSplitsNormalizesTheUnionShape(t *testing.T) {
	shapes := map[string]string{
		"lapDTOs object": `{"lapDTOs":[` + splitEntry + `]}`,
		"splits object":  `{"splits":[` + splitEntry + `]}`,
		"bare array":     `[` + splitEntry + `]`,
		"single object":  splitEntry,
	}

	for name, body := range shapes {
		t.Run(name, func(t *testing.T) {
			script := testkit.NewScript().
				With(activityDetailPath(client.SegmentTypedSplits),
					testkit.JSON(http.StatusOK, body))
			h := newHarness(t, script)

			got := h.call(t, tools.ToolGetActivityTypedSplits,
				map[string]any{argActivityID: testActivityID})

			splits, ok := got["splits"].([]any)
			if !ok {
				t.Fatalf("splits = %T, want an array", got["splits"])
			}
			if len(splits) != 1 {
				t.Fatalf("%d splits, want 1", len(splits))
			}
			first, _ := splits[0].(map[string]any)
			if first["type"] != "INTERVAL_ACTIVE" {
				t.Errorf("type = %v, want %q", first["type"], "INTERVAL_ACTIVE")
			}
		})
	}
}

func TestGetActivityTypedSplitsAcceptsBothIdentifierForms(t *testing.T) {
	for name, id := range map[string]any{
		"string": testActivityID,
		"number": float64(987654321),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, readScript())

			got := h.call(t, tools.ToolGetActivityTypedSplits, map[string]any{argActivityID: id})

			if got["activity_id"] != float64(987654321) {
				t.Errorf("activity_id = %v, want 987654321", got["activity_id"])
			}
		})
	}
}

func TestGetActivityExerciseSetsReturnsTheStrengthSets(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetActivityExerciseSets, map[string]any{argActivityID: testActivityID})

	sets, ok := got["sets"].([]any)
	if !ok {
		t.Fatalf("sets = %T, want an array", got["sets"])
	}
	if len(sets) != 1 {
		t.Fatalf("%d sets, want 1", len(sets))
	}
	first, _ := sets[0].(map[string]any)
	if first["set_type"] != setKindActive {
		t.Errorf("set_type = %v, want %q", first["set_type"], setKindActive)
	}
	if exercises, _ := first["exercises"].([]any); len(exercises) != 1 {
		t.Errorf("%d exercises, want 1", len(exercises))
	}
}

func TestActivityDetailToolsRefuseAnUnusableIdentifier(t *testing.T) {
	h := newHarness(t, readScript())

	cases := map[string]map[string]any{
		"missing":        nil,
		"zero":           {argActivityID: 0},
		"negative":       {argActivityID: -5},
		"fractional":     {argActivityID: 1.5},
		"non numeric":    {argActivityID: "not-an-id"},
		"path traversal": {argActivityID: "../../secrets"},
		"boolean":        {argActivityID: true},
		"oversized text": {argActivityID: strings.Repeat("9", 64)},
	}
	for _, tool := range []string{tools.ToolGetActivityTypedSplits, tools.ToolGetActivityExerciseSets} {
		for name, args := range cases {
			t.Run(tool+"/"+name, func(t *testing.T) {
				assertSanitized(t, h.callError(t, tool, args))
			})
		}
	}
}

func TestActivityDetailToolsDoNotReachGarminForABadIdentifier(t *testing.T) {
	h := newHarness(t, readScript())

	h.callError(t, tools.ToolGetActivityTypedSplits, map[string]any{argActivityID: "../../secrets"})

	if paths := h.requests(); len(paths) != 0 {
		t.Errorf("the fake received %v, want nothing: a bad identifier never reaches a URL", paths)
	}
}
