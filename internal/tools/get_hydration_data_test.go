package tools

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

const cardioHydrationBody = `{"calendarDate":"` + cardioDate + `","valueInML":750.0,` +
	`"goalInML":2000.0,"dailyAverageinML":null,"sweatLossInML":null,` +
	`"lastEntryTimestampLocal":"` + cardioDate + `T09:15:00.0"}`

func cardioHydrationPath() string {
	return client.PathDailyHydrationPrefix + "/" + cardioDate
}

func TestReadHydrationReturnsTheDay(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript(
		cardioJSON(cardioHydrationPath(), cardioHydrationBody)))

	got, err := svc.readHydration(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readHydration() = %v", err)
	}

	if !got.HasData {
		t.Error("HasData = false, want true")
	}
	if got.ValueML == nil || *got.ValueML != 750 {
		t.Errorf("ValueML = %v, want 750", got.ValueML)
	}
	if got.GoalML == nil || *got.GoalML != 2000 {
		t.Errorf("GoalML = %v, want 2000", got.GoalML)
	}
	if got.DailyAverageML != nil {
		t.Errorf("DailyAverageML = %v, want nil for a null field", got.DailyAverageML)
	}
	if got.LastEntryLocal == nil {
		t.Error("LastEntryLocal = nil, want Garmin's own rendering of the last entry")
	}
	if calls := len(fake.Requests()); calls != 1 {
		t.Errorf("requests = %d, want 1: the day is in the path", calls)
	}
}

func TestReadHydrationReportsADayWithNoData(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioHydrationPath(), `{}`)))

	got, err := svc.readHydration(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readHydration() = %v", err)
	}
	if got.HasData {
		t.Error("HasData = true for an empty document, want false")
	}
	if got.Date != cardioDate {
		t.Errorf("Date = %q, want the requested day", got.Date)
	}
}

func TestHydrationLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	value := 750.0
	goal := 2000.0
	rendered := Hydration{HasData: true, ValueML: &value, GoalML: &goal}.LogValue().String()
	for _, forbidden := range []string{"750", "2000"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the log value %q carries a figure, want shape only", rendered)
		}
	}
	if !strings.Contains(rendered, "goal=set") {
		t.Errorf("the log value %q does not report the goal's presence", rendered)
	}
}

func TestReadHydrationReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioHydrationPath())))

	_, err := svc.readHydration(cardioContext(t), cardioDate)
	assertSanitizedGarminFailure(t, err)
}

func TestReadHydrationRefusesAMalformedDate(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript())

	if _, err := svc.readHydration(cardioContext(t), "2026-1-1"); err == nil {
		t.Fatal("readHydration() with a malformed date = nil, want a refusal")
	}
	if calls := len(fake.Requests()); calls != 0 {
		t.Errorf("requests = %d, want 0", calls)
	}
}
