package tools

import (
	"strings"
	"testing"
)

func TestReadRespirationSummaryKeepsOnlyTheScalars(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript(
		cardioJSON(cardioRespirationPath(), cardioRespirationBody)))

	got, err := svc.readRespirationSummary(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readRespirationSummary() = %v", err)
	}

	if !got.HasData {
		t.Error("HasData = false, want true")
	}
	if got.HighestBreathsPerMin == nil || *got.HighestBreathsPerMin != 19 {
		t.Errorf("HighestBreathsPerMin = %v, want 19", got.HighestBreathsPerMin)
	}
	if got.AvgSleepBreathsPerMin == nil || *got.AvgSleepBreathsPerMin != 13 {
		t.Errorf("AvgSleepBreathsPerMin = %v, want 13", got.AvgSleepBreathsPerMin)
	}
	if got.AvgNextNightBPM == nil || *got.AvgNextNightBPM != 12 {
		t.Errorf("AvgNextNightBPM = %v, want 12", got.AvgNextNightBPM)
	}
	if got.LowestBreathsPerMin == nil || *got.LowestBreathsPerMin <= 0 {
		t.Errorf("LowestBreathsPerMin = %v, want Garmin's own positive figure: the "+
			"sentinels in the series must not reach a daily minimum",
			got.LowestBreathsPerMin)
	}
	if calls := len(fake.Requests()); calls != 1 {
		t.Errorf("requests = %d, want 1: the summary is a view of the one read", calls)
	}
}

func TestReadRespirationSummaryReportsADayWithNoData(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioRespirationPath(), `{}`)))

	got, err := svc.readRespirationSummary(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readRespirationSummary() = %v", err)
	}
	if got.HasData {
		t.Error("HasData = true for an empty document, want false")
	}
	if got.Date != cardioDate {
		t.Errorf("Date = %q, want the requested day", got.Date)
	}
}

func TestRespirationSummaryLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	value := 19.0
	rendered := RespirationSummary{HasData: true, HighestBreathsPerMin: &value}.
		LogValue().String()
	if strings.Contains(rendered, "19") {
		t.Errorf("the log value %q carries a reading, want shape only", rendered)
	}
}

func TestReadRespirationSummaryReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioRespirationPath())))

	_, err := svc.readRespirationSummary(cardioContext(t), cardioDate)
	assertSanitizedGarminFailure(t, err)
}

func TestReadRespirationSummaryRefusesAnUnattributedRequest(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript())

	if _, err := svc.readRespirationSummary(t.Context(), cardioDate); err == nil {
		t.Fatal("readRespirationSummary() without a principal = nil, want a refusal")
	}
}
