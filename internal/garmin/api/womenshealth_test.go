package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The women's-health reads. Every fixture here is synthetic and none is a
// recording of a real account.

func newWomensHealth(t *testing.T, h harness) *api.WomensHealth {
	t.Helper()

	w, err := api.NewWomensHealth(h.rc)
	if err != nil {
		t.Fatalf("NewWomensHealth() = %v", err)
	}
	return w
}

func menstrualDayviewPath() string {
	return client.PathMenstrualDayviewPrefix + "/" + testCalendarDate
}

func menstrualCalendarPath() string {
	return client.PathMenstrualCalendarPrefix + "/" + testCalendarDate + "/" + testCalendarDate
}

func TestNewWomensHealthRefusesAMissingRequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewWomensHealth(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Errorf("NewWomensHealth(nil) = %v, want ErrNotConfigured", err)
	}
}

func TestWomensHealthDayViewCarriesTheDocumentVerbatim(t *testing.T) {
	t.Parallel()

	body := `{"unknownKey":[{"nested":true}]}`
	script := testkit.NewScript().With(menstrualDayviewPath(), testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	got, err := newWomensHealth(t, h).DayView(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("DayView() = %v", err)
	}
	if !got.HasDocument() {
		t.Fatal("HasDocument() = false, want true")
	}
	if !json.Valid(got.Document) {
		t.Error("the carried document is not valid JSON")
	}
}

func TestWomensHealthDayViewReportsAnEmptyBody(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(menstrualDayviewPath(), testkit.Behavior{Status: http.StatusNoContent})
	h := newHarness(t, script, client.Limits{})

	got, err := newWomensHealth(t, h).DayView(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("DayView() = %v", err)
	}
	if got.HasDocument() {
		t.Error("HasDocument() = true for an empty body, want false")
	}
}

func TestWomensHealthDayViewRefusesAZeroDate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	if _, err := newWomensHealth(t, h).DayView(
		t.Context(), h.session, client.Date{},
	); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("DayView() with a zero date = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0: a refused date costs no Garmin call", got)
	}
}

func TestWomensHealthCalendarCarriesTheDocumentVerbatim(t *testing.T) {
	t.Parallel()

	body := `{"cycles":[{"unknownKey":true}]}`
	script := testkit.NewScript().With(menstrualCalendarPath(), testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	got, err := newWomensHealth(t, h).Calendar(t.Context(), h.session, mustWindow(t))
	if err != nil {
		t.Fatalf("Calendar() = %v", err)
	}
	if !got.HasDocument() {
		t.Fatal("HasDocument() = false, want true")
	}
	if !json.Valid(got.Document) {
		t.Error("the carried document is not valid JSON")
	}
}

func TestWomensHealthCalendarRefusesAWindowOverTheBound(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 1})

	span, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, "2026-03-01"))
	if err != nil {
		t.Fatalf("client.NewDateRange() = %v", err)
	}
	if _, err := newWomensHealth(t, h).Calendar(
		t.Context(), h.session, span,
	); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("Calendar() over the bound = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0: a refused window costs no Garmin call", got)
	}
}

func TestWomensHealthCalendarRefusesAZeroWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	if _, err := newWomensHealth(t, h).Calendar(
		t.Context(), h.session, client.DateRange{},
	); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("Calendar() with a zero window = %v, want ErrValidation", err)
	}
}

func TestWomensHealthPregnancySummaryCarriesTheDocumentVerbatim(t *testing.T) {
	t.Parallel()

	body := `{"unknownKey":42}`
	script := testkit.NewScript().With(client.PathPregnancySnapshot, testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	got, err := newWomensHealth(t, h).PregnancySummary(t.Context(), h.session)
	if err != nil {
		t.Fatalf("PregnancySummary() = %v", err)
	}
	if !got.HasDocument() {
		t.Fatal("HasDocument() = false, want true")
	}
	if !json.Valid(got.Document) {
		t.Error("the carried document is not valid JSON")
	}
}

func TestWomensHealthPregnancySummaryReportsAnEmptyBody(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathPregnancySnapshot, testkit.Behavior{Status: http.StatusNoContent})
	h := newHarness(t, script, client.Limits{})

	got, err := newWomensHealth(t, h).PregnancySummary(t.Context(), h.session)
	if err != nil {
		t.Fatalf("PregnancySummary() = %v", err)
	}
	if got.HasDocument() {
		t.Error("HasDocument() = true for an empty body, want false")
	}
}

func TestWomensHealthMethodsRefuseAnUnusableSession(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	w := newWomensHealth(t, h)

	if _, err := w.PregnancySummary(t.Context(), client.Session{}); !errors.Is(err, client.ErrMissingPrincipal) {
		t.Errorf("PregnancySummary() with a zero session = %v, want ErrMissingPrincipal", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
