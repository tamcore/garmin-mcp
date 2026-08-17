package api_test

import (
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestAddWeighInSendsTheUpstreamPayload pins add_weigh_in's POST body
// (garminconnect/__init__.py:1219-1246): dateTimestamp and gmtTimestamp in
// millisecond wall-clock form, unitKey, a literal "MANUAL" sourceType and the
// weight as value.
func TestAddWeighInSendsTheUpstreamPayload(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathWeightUserWeight, testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	local := time.Date(2026, 1, 31, 8, 30, 0, 123000000, time.FixedZone("CET", 3600))
	gmt := time.Date(2026, 1, 31, 7, 30, 0, 123000000, time.UTC)
	entry := api.WeighInEntry{Weight: 72.5, Unit: api.WeightUnitKg, LocalAt: local, GMTAt: gmt}

	if _, err := newWeight(t, h).AddWeighIn(t.Context(), h.session, entry); err != nil {
		t.Fatalf("AddWeighIn() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if requests[0].Method != http.MethodPost {
		t.Errorf("method = %q, want POST", requests[0].Method)
	}
	body := decodeBody(t, requests[0].Body)
	want := map[string]any{
		"dateTimestamp": "2026-01-31T08:30:00.123",
		"gmtTimestamp":  "2026-01-31T07:30:00.123",
		"unitKey":       "kg",
		"sourceType":    "MANUAL",
		"value":         72.5,
	}
	for key, wantValue := range want {
		if body[key] != wantValue {
			t.Errorf("%s = %v, want %v", key, body[key], wantValue)
		}
	}
}

// TestAddWeighInWithTimestampsSharesTheSameWireShape proves the two write
// tools dispatch the identical payload, differing only in Op label.
func TestAddWeighInWithTimestampsSharesTheSameWireShape(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathWeightUserWeight, testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	at := time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC)
	entry := api.WeighInEntry{Weight: 160, Unit: api.WeightUnitLbs, LocalAt: at, GMTAt: at}

	if _, err := newWeight(t, h).AddWeighInWithTimestamps(t.Context(), h.session, entry); err != nil {
		t.Fatalf("AddWeighInWithTimestamps() = %v", err)
	}

	body := decodeBody(t, h.server.Requests()[0].Body)
	if body["unitKey"] != "lbs" || body["value"] != float64(160) {
		t.Errorf("body = %v, want unitKey=lbs, value=160", body)
	}
}

// TestWeighInEntryRefusesInvalidInput covers every boundary AddWeighIn and
// AddWeighInWithTimestamps share, so neither dispatches a request the wire
// cannot represent safely.
func TestWeighInEntryRefusesInvalidInput(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	w := newWeight(t, h)
	at := time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC)

	nan := math.NaN()
	posInf := math.Inf(1)
	negInf := math.Inf(-1)
	huge := 1e300

	calls := map[string]api.WeighInEntry{
		"zero weight":       {Weight: 0, Unit: api.WeightUnitKg, LocalAt: at, GMTAt: at},
		"negative weight":   {Weight: -5, Unit: api.WeightUnitKg, LocalAt: at, GMTAt: at},
		"NaN weight":        {Weight: nan, Unit: api.WeightUnitKg, LocalAt: at, GMTAt: at},
		"+Inf weight":       {Weight: posInf, Unit: api.WeightUnitKg, LocalAt: at, GMTAt: at},
		"-Inf weight":       {Weight: negInf, Unit: api.WeightUnitKg, LocalAt: at, GMTAt: at},
		"absurd weight":     {Weight: huge, Unit: api.WeightUnitKg, LocalAt: at, GMTAt: at},
		"empty unit":        {Weight: 70, Unit: "", LocalAt: at, GMTAt: at},
		"unrecognized unit": {Weight: 70, Unit: "lb", LocalAt: at, GMTAt: at},
		"zero local time":   {Weight: 70, Unit: api.WeightUnitKg, GMTAt: at},
		"zero gmt time":     {Weight: 70, Unit: api.WeightUnitKg, LocalAt: at},
	}
	for name, entry := range calls {
		t.Run(name, func(t *testing.T) {
			if _, err := w.AddWeighIn(t.Context(), h.session, entry); !errors.Is(err, client.ErrValidation) {
				t.Errorf("AddWeighIn(%s) = %v, want ErrValidation", name, err)
			}
		})
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
