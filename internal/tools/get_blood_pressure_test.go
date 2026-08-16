package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

const cardioBloodPressureBody = `{"measurementSummaries":[{"measurementSummaryDate":"` +
	cardioDate + `","measurements":[{"systolic":118,"diastolic":76,"pulse":60,` +
	`"measurementTimestampGMT":"` + cardioDate + `T07:00:00.0",` +
	`"measurementTimestampLocal":"` + cardioDate + `T08:00:00.0","sourceType":"MANUAL",` +
	`"notes":"after a walk"}]}]}`

func cardioBloodPressurePath() string {
	return client.PathBloodPressureRangePrefix + "/" + cardioDate + "/" + cardioDate
}

func cardioWindow() getBloodPressureInput {
	return getBloodPressureInput{StartDate: cardioDate, EndDate: cardioDate}
}

func TestReadBloodPressureReturnsTheReadingsInTheWindow(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript(
		cardioJSON(cardioBloodPressurePath(), cardioBloodPressureBody)))

	got, err := svc.readBloodPressure(cardioContext(t), cardioWindow())
	if err != nil {
		t.Fatalf("readBloodPressure() = %v", err)
	}

	if got.StartDate != cardioDate || got.EndDate != cardioDate {
		t.Errorf("window = %q..%q, want the requested one", got.StartDate, got.EndDate)
	}
	if got.Count != 1 {
		t.Fatalf("Count = %d, want 1", got.Count)
	}
	reading := got.Readings[0]
	if reading.SystolicMMHG == nil || *reading.SystolicMMHG != 118 {
		t.Errorf("SystolicMMHG = %v, want 118", reading.SystolicMMHG)
	}
	if reading.Notes == nil || *reading.Notes != "after a walk" {
		t.Errorf("Notes = %v, want the account's own note", reading.Notes)
	}
	if got.Truncated {
		t.Error("Truncated = true for a single reading")
	}
	if calls := len(fake.Requests()); calls != 1 {
		t.Errorf("requests = %d, want 1: the window is in the path", calls)
	}
}

func TestReadBloodPressureReportsAnEmptyWindow(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioBloodPressurePath(), `{}`)))

	got, err := svc.readBloodPressure(cardioContext(t), cardioWindow())
	if err != nil {
		t.Fatalf("readBloodPressure() = %v", err)
	}
	if got.Count != 0 {
		t.Errorf("Count = %d, want 0", got.Count)
	}
	if got.Readings == nil {
		t.Error("Readings = nil, want an empty list so the result renders as an array")
	}
}

func TestReadBloodPressureRefusesAnInvertedWindowBeforeAnyCall(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript())

	in := getBloodPressureInput{StartDate: challengeWindowEnd, EndDate: scoresStartDate}
	if _, err := svc.readBloodPressure(cardioContext(t), in); !errors.Is(
		err, ErrInvalidArgument) {
		t.Fatalf("readBloodPressure() with an inverted window = %v, want ErrInvalidArgument", err)
	}
	if calls := len(fake.Requests()); calls != 0 {
		t.Errorf("requests = %d, want 0", calls)
	}
}

func TestReadBloodPressureRefusesAMalformedEndDate(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript())

	in := getBloodPressureInput{StartDate: cardioDate, EndDate: "not-a-date"}
	if _, err := svc.readBloodPressure(cardioContext(t), in); !errors.Is(
		err, ErrInvalidArgument) {
		t.Fatalf("readBloodPressure() with a malformed end date = %v, want ErrInvalidArgument",
			err)
	}
}

func TestBloodPressureLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	systolic := 118.0
	got := BloodPressure{
		Readings: []BloodPressureReading{{SystolicMMHG: &systolic}},
		Count:    1,
	}

	rendered := got.LogValue().String()
	if strings.Contains(rendered, "118") {
		t.Errorf("the log value %q carries a reading, want shape only", rendered)
	}
	if !strings.Contains(rendered, "readings=1") {
		t.Errorf("the log value %q does not report the reading count", rendered)
	}
}

func TestReadBloodPressureReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioBloodPressurePath())))

	_, err := svc.readBloodPressure(cardioContext(t), cardioWindow())
	assertSanitizedGarminFailure(t, err)
}

func TestReadBloodPressureRefusesAnUnattributedRequest(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript())

	if _, err := svc.readBloodPressure(t.Context(), cardioWindow()); err == nil {
		t.Fatal("readBloodPressure() without a principal = nil, want a refusal")
	}
}
