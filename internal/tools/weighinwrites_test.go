package tools_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Argument keys and synthetic values this file's tests share, named once so a
// rename shows up in one place.
const (
	argWeight          = "weight"
	argDateTimestamp   = "date_timestamp"
	argGMTTimestamp    = "gmt_timestamp"
	valWeighInUnitLbs  = "lbs"
	testLocalTimestamp = "2026-01-31T06:12:00"
	testGMTTimestamp   = "2026-01-31T05:12:00"
)

// weighInWritePath is the single POST target both add tools share.
func weighInWritePath() string { return client.PathWeightUserWeight }

func weighInWriteScript() testkit.Script {
	return testkit.NewScript().With(weighInWritePath(), repeat(okJSON("{}"), 6)...)
}

func TestAddWeighInWritesTheDefaultUnitAndReportsTheRecord(t *testing.T) {
	h := newWriteHarness(t, weighInWriteScript(), enabledWrites())

	out := h.call(t, tools.ToolAddWeighIn, map[string]any{argWeight: 70.5})

	if got := out[argWeight]; got != 70.5 {
		t.Errorf("weight = %v, want 70.5", got)
	}
	if got := out["unit"]; got != "kg" {
		t.Errorf("unit = %v, want kg (the default)", got)
	}
	if _, ok := out["message"].(string); !ok {
		t.Error("message is missing or not a string")
	}

	body := h.bodyFor(t, http.MethodPost, weighInWritePath())
	if got := body["unitKey"]; got != "kg" {
		t.Errorf("the write sent unitKey = %v, want kg", got)
	}
	if got := body["value"]; got != 70.5 {
		t.Errorf("the write sent value = %v, want 70.5", got)
	}
	if got := body["sourceType"]; got != "MANUAL" {
		t.Errorf("the write sent sourceType = %v, want MANUAL", got)
	}
	if got, _ := body["dateTimestamp"].(string); got == "" {
		t.Error("the write sent no dateTimestamp")
	}
	if got, _ := body["gmtTimestamp"].(string); got == "" {
		t.Error("the write sent no gmtTimestamp")
	}
}

func TestAddWeighInAcceptsLbsAsTheUnit(t *testing.T) {
	h := newWriteHarness(t, weighInWriteScript(), enabledWrites())

	out := h.call(t, tools.ToolAddWeighIn, map[string]any{argWeight: 155.0, "unit_key": valWeighInUnitLbs})

	if got := out["unit"]; got != valWeighInUnitLbs {
		t.Errorf("unit = %v, want lbs", got)
	}
	body := h.bodyFor(t, http.MethodPost, weighInWritePath())
	if got := body["unitKey"]; got != valWeighInUnitLbs {
		t.Errorf("the write sent unitKey = %v, want lbs", got)
	}
}

func TestAddWeighInRefusesLbAbbreviationWithoutCoercing(t *testing.T) {
	h := newWriteHarness(t, weighInWriteScript(), enabledWrites())

	message := h.callError(t, tools.ToolAddWeighIn, map[string]any{argWeight: 155.0, "unit_key": "lb"})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if len(h.fake.Requests()) != 0 {
		t.Errorf("a rejected unit still reached Garmin: %v", h.recordedMethods())
	}
}

func TestAddWeighInRefusedUnderTheCurrentPolicy(t *testing.T) {
	h := newHarness(t, testkit.NewScript())

	message := h.callError(t, tools.ToolAddWeighIn, map[string]any{argWeight: 70.5})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if len(h.fake.Requests()) != 0 {
		t.Errorf("the refusal still reached Garmin: %v", h.recordedMethods())
	}
}

func TestAddWeighInWithTimestampsUsesTheSuppliedInstants(t *testing.T) {
	h := newWriteHarness(t, weighInWriteScript(), enabledWrites())

	out := h.call(t, tools.ToolAddWeighInWithTimestamps, map[string]any{
		argWeight:        82.0,
		argDateTimestamp: testLocalTimestamp,
		argGMTTimestamp:  testGMTTimestamp,
	})

	if got := out["timestamp_local"]; got != testLocalTimestamp {
		t.Errorf("timestamp_local = %v, want the supplied local instant echoed back", got)
	}
	if got := out["timestamp_gmt"]; got != testGMTTimestamp {
		t.Errorf("timestamp_gmt = %v, want the supplied GMT instant echoed back", got)
	}

	body := h.bodyFor(t, http.MethodPost, weighInWritePath())
	if got, _ := body["dateTimestamp"].(string); got == "" || got[:19] != testLocalTimestamp {
		t.Errorf("the write sent dateTimestamp = %v, want it to start with the supplied local instant", got)
	}
	if got, _ := body["gmtTimestamp"].(string); got == "" || got[:19] != testGMTTimestamp {
		t.Errorf("the write sent gmtTimestamp = %v, want it to start with the supplied GMT instant", got)
	}
}

func TestAddWeighInWithTimestampsRegeneratesBothWhenEitherIsMissing(t *testing.T) {
	h := newWriteHarness(t, weighInWriteScript(), enabledWrites())

	// Only date_timestamp is supplied; gmt_timestamp is omitted. Matching
	// weight_management.py:191-196, both are regenerated from the current
	// instant, and the supplied date_timestamp is discarded rather than paired
	// with a generated GMT counterpart.
	out := h.call(t, tools.ToolAddWeighInWithTimestamps, map[string]any{
		argWeight:        82.0,
		argDateTimestamp: "2020-01-01T00:00:00",
	})

	if got := out["timestamp_local"]; got == "2020-01-01T00:00:00" {
		t.Error("timestamp_local = the stale supplied value, want it regenerated from now")
	}
}

func TestAddWeighInWithTimestampsRefusesAMalformedTimestamp(t *testing.T) {
	h := newWriteHarness(t, weighInWriteScript(), enabledWrites())

	message := h.callError(t, tools.ToolAddWeighInWithTimestamps, map[string]any{
		argWeight:        82.0,
		argDateTimestamp: "not-a-timestamp",
		argGMTTimestamp:  testGMTTimestamp,
	})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if len(h.fake.Requests()) != 0 {
		t.Errorf("a rejected timestamp still reached Garmin: %v", h.recordedMethods())
	}
}
