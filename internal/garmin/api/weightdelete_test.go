package api_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func mustWeighInID(t *testing.T, value int64) client.ID {
	t.Helper()

	id, err := client.NewID(value)
	if err != nil {
		t.Fatalf("client.NewID(%d) = %v", value, err)
	}
	return id
}

func weightDeletePath(t *testing.T, pk int64) string {
	t.Helper()

	return client.PathWeightDeletePrefix + "/" + testCalendarDate + "/" +
		client.PathWeightByVersionSegment + "/" + mustWeighInID(t, pk).String()
}

func singleWeighInBody(pk int64) string {
	return `{"dateWeightList":[{"weight":72500,"sourceType":"MANUAL","samplePk":` +
		itoa(pk) + `}]}`
}

// itoa is a tiny local decimal formatter so this file needs no extra import
// beyond what its neighbors already use.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestDeleteWeighInsRemovesTheSingleEntry proves a day with exactly one
// weigh-in is deleted without needing confirmAll.
func TestDeleteWeighInsRemovesTheSingleEntry(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(weightDayviewPath(), testkit.JSON(http.StatusOK, singleWeighInBody(998877))).
		With(weightDeletePath(t, 998877), testkit.JSON(http.StatusOK, "{}"))
	h := newHarness(t, script, client.Limits{})

	result, err := newWeight(t, h).DeleteWeighIns(t.Context(), h.session, mustDate(t, testCalendarDate), false)
	if err != nil {
		t.Fatalf("DeleteWeighIns() = %v", err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0].SamplePK.Int64() != 998877 {
		t.Errorf("Deleted = %+v, want one entry with samplePk 998877", result.Deleted)
	}
	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("the fake received %d requests, want 2 (read + delete)", len(requests))
	}
	if requests[1].Method != http.MethodDelete {
		t.Errorf("second request method = %q, want DELETE", requests[1].Method)
	}
}

// TestDeleteWeighInsWithNoEntriesReportsAnEmptyResultAndNoError matches
// upstream's own "no weigh-ins found" case: not an error.
func TestDeleteWeighInsWithNoEntriesReportsAnEmptyResultAndNoError(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(weightDayviewPath(), testkit.JSON(http.StatusOK, `{"dateWeightList":[]}`))
	h := newHarness(t, script, client.Limits{})

	result, err := newWeight(t, h).DeleteWeighIns(t.Context(), h.session, mustDate(t, testCalendarDate), true)
	if err != nil {
		t.Fatalf("DeleteWeighIns() = %v", err)
	}
	if len(result.Deleted) != 0 {
		t.Errorf("Deleted = %+v, want none", result.Deleted)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1 (the read only)", got)
	}
}

// TestDeleteWeighInsRefusesAmbiguousMultiEntryDayWithoutConfirmAll proves an
// ambiguous day is refused with a hard error, never a silent no-op, and that
// no delete is dispatched.
func TestDeleteWeighInsRefusesAmbiguousMultiEntryDayWithoutConfirmAll(t *testing.T) {
	t.Parallel()

	body := `{"dateWeightList":[{"weight":72000,"samplePk":1001},{"weight":72200,"samplePk":1002}]}`
	script := testkit.NewScript().With(weightDayviewPath(), testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	if _, err := newWeight(t, h).DeleteWeighIns(
		t.Context(), h.session, mustDate(t, testCalendarDate), false); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("DeleteWeighIns() without confirmAll = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1 (the read only, no delete dispatched)", got)
	}
}

// TestDeleteWeighInsWithConfirmAllRemovesEveryEntry proves confirmAll fans the
// delete out to every discovered sample.
func TestDeleteWeighInsWithConfirmAllRemovesEveryEntry(t *testing.T) {
	t.Parallel()

	body := `{"dateWeightList":[{"weight":72000,"samplePk":1001},{"weight":72200,"samplePk":1002}]}`
	script := testkit.NewScript().
		With(weightDayviewPath(), testkit.JSON(http.StatusOK, body)).
		With(weightDeletePath(t, 1001), testkit.JSON(http.StatusOK, "{}")).
		With(weightDeletePath(t, 1002), testkit.JSON(http.StatusOK, "{}"))
	h := newHarness(t, script, client.Limits{})

	result, err := newWeight(t, h).DeleteWeighIns(t.Context(), h.session, mustDate(t, testCalendarDate), true)
	if err != nil {
		t.Fatalf("DeleteWeighIns() = %v", err)
	}
	if len(result.Deleted) != 2 {
		t.Fatalf("Deleted = %+v, want 2 entries", result.Deleted)
	}
	if got := len(h.server.Requests()); got != 3 {
		t.Errorf("the fake received %d requests, want 3 (one read, two deletes)", got)
	}
}

// TestDeleteWeighInsRefusesAnUnparsableSamplePK proves a malformed entry
// aborts the whole call before any delete is dispatched, rather than deleting
// the entries it could parse.
func TestDeleteWeighInsRefusesAnUnparsableSamplePK(t *testing.T) {
	t.Parallel()

	body := `{"dateWeightList":[{"weight":72000,"samplePk":1001},{"weight":72200}]}`
	script := testkit.NewScript().
		With(weightDayviewPath(), testkit.JSON(http.StatusOK, body)).
		With(weightDeletePath(t, 1001), testkit.JSON(http.StatusOK, "{}"))
	h := newHarness(t, script, client.Limits{})

	if _, err := newWeight(t, h).DeleteWeighIns(
		t.Context(), h.session, mustDate(t, testCalendarDate), true); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("DeleteWeighIns() with an unparsable samplePk = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1 (the read only, no delete dispatched)", got)
	}
}

// TestDeleteWeighInsRefusesAZeroDate proves the date is validated before any
// request is dispatched.
func TestDeleteWeighInsRefusesAZeroDate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newWeight(t, h).DeleteWeighIns(
		t.Context(), h.session, client.Date{}, true); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("DeleteWeighIns() with a zero date = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestDeleteWeighInsRefusesAnOversizedDay proves a day reporting more entries
// than maxWeighInDeletionsPerDay is refused outright rather than truncated.
func TestDeleteWeighInsRefusesAnOversizedDay(t *testing.T) {
	t.Parallel()

	var entries strings.Builder
	for i := range 51 {
		if i > 0 {
			entries.WriteString(",")
		}
		entries.WriteString(`{"weight":70000,"samplePk":` + itoa(int64(2000+i)) + `}`)
	}
	body := `{"dateWeightList":[` + entries.String() + `]}`
	script := testkit.NewScript().With(weightDayviewPath(), testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	if _, err := newWeight(t, h).DeleteWeighIns(
		t.Context(), h.session, mustDate(t, testCalendarDate), true); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("DeleteWeighIns() over the per-day bound = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1 (the read only, no delete dispatched)", got)
	}
}
