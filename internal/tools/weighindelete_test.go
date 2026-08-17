package tools_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

const deleteWeighInsDate = "2026-01-31"

func deleteWeighInsDayviewPath() string {
	return client.PathWeightDayviewPrefix + "/" + deleteWeighInsDate
}

func deleteWeighInsDeletePath(t *testing.T, pk int64) string {
	t.Helper()

	id, err := client.NewID(pk)
	if err != nil {
		t.Fatalf("client.NewID(%d) = %v", pk, err)
	}
	return client.PathWeightDeletePrefix + "/" + deleteWeighInsDate + "/" +
		client.PathWeightByVersionSegment + "/" + id.String()
}

// deleteWeighInsDailyBody renders a dateWeightList of n synthetic entries, each
// with a distinct samplePk starting at 900001.
func deleteWeighInsDailyBody(n int) string {
	var body strings.Builder
	body.WriteString(`{"dateWeightList":[`)
	for i := range n {
		if i > 0 {
			body.WriteString(",")
		}
		body.WriteString(`{"weight":72500,"sourceType":"MANUAL","samplePk":` +
			strconv.FormatInt(int64(900001+i), 10) + `}`)
	}
	return body.String() + `]}`
}

func TestDeleteWeighInsIsRefusedWhenConfirmationIsUnavailable(t *testing.T) {
	script := testkit.NewScript().
		With(deleteWeighInsDayviewPath(), okJSON(deleteWeighInsDailyBody(1))).
		With(deleteWeighInsDeletePath(t, 900001), okJSON("{}"))
	opts := enabledWrites()
	opts.confirmer = refusingConfirmer{}
	h := newWriteHarness(t, script, opts)

	message := h.callError(t, tools.ToolDeleteWeighIns, map[string]any{argDate: deleteWeighInsDate})

	if message == "" {
		t.Error("the refusal carried no message")
	}
	if len(h.fake.Requests()) != 0 {
		t.Errorf("an unconfirmed delete still reached Garmin: %v", h.recordedMethods())
	}
}

func TestDeleteWeighInsRunsOnceConfirmedRemovingEveryEntryByDefault(t *testing.T) {
	script := testkit.NewScript().
		With(deleteWeighInsDayviewPath(), okJSON(deleteWeighInsDailyBody(2))).
		With(deleteWeighInsDeletePath(t, 900001), okJSON("{}")).
		With(deleteWeighInsDeletePath(t, 900002), okJSON("{}"))
	h := newWriteHarness(t, script, enabledWrites())

	out := h.call(t, tools.ToolDeleteWeighIns, map[string]any{argDate: deleteWeighInsDate})

	if got := out["deleted_count"]; got != 2.0 {
		t.Errorf("deleted_count = %v, want 2 (delete_all defaults to true)", got)
	}
	if got := out[argDate]; got != deleteWeighInsDate {
		t.Errorf("date = %v, want %q", got, deleteWeighInsDate)
	}

	methods := h.recordedMethods()
	deletes := 0
	for _, m := range methods {
		if len(m) >= len(http.MethodDelete) && m[:len(http.MethodDelete)] == http.MethodDelete {
			deletes++
		}
	}
	if deletes != 2 {
		t.Errorf("recorded %d DELETE requests, want 2: %v", deletes, methods)
	}
}

func TestDeleteWeighInsRefusesMultipleEntriesWhenDeleteAllIsFalse(t *testing.T) {
	script := testkit.NewScript().With(deleteWeighInsDayviewPath(), okJSON(deleteWeighInsDailyBody(2)))
	h := newWriteHarness(t, script, enabledWrites())

	message := h.callError(t, tools.ToolDeleteWeighIns, map[string]any{
		argDate: deleteWeighInsDate, "delete_all": false,
	})

	if message == "" {
		t.Error("the refusal carried no message")
	}
	for _, m := range h.recordedMethods() {
		if len(m) >= len(http.MethodDelete) && m[:len(http.MethodDelete)] == http.MethodDelete {
			t.Errorf("a refused ambiguous delete still dispatched a DELETE: %v", h.recordedMethods())
		}
	}
}

func TestDeleteWeighInsWithNoEntriesReportsZeroAndIsNotAnError(t *testing.T) {
	script := testkit.NewScript().With(deleteWeighInsDayviewPath(), okJSON(`{"dateWeightList":[]}`))
	h := newWriteHarness(t, script, enabledWrites())

	out := h.call(t, tools.ToolDeleteWeighIns, map[string]any{argDate: deleteWeighInsDate})

	if got := out["deleted_count"]; got != 0.0 {
		t.Errorf("deleted_count = %v, want 0", got)
	}
}

func TestDeleteWeighInsRefusedUnderTheCurrentPolicy(t *testing.T) {
	h := newHarness(t, testkit.NewScript())

	message := h.callError(t, tools.ToolDeleteWeighIns, map[string]any{argDate: deleteWeighInsDate})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if len(h.fake.Requests()) != 0 {
		t.Errorf("the refusal still reached Garmin: %v", h.recordedMethods())
	}
}
