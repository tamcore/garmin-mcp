package tools

import (
	"strings"
	"testing"
)

func TestReadHeartRatesSummaryAveragesThePresentReadings(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript(
		cardioJSON(cardioHeartRatePath(), cardioHeartRateBody)))

	got, err := svc.readHeartRatesSummary(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readHeartRatesSummary() = %v", err)
	}

	if !got.HasData {
		t.Error("HasData = false, want true")
	}
	if got.DataPoints != 2 {
		t.Errorf("DataPoints = %d, want 2: the null reading is not a data point", got.DataPoints)
	}
	if got.AverageBPM == nil || *got.AverageBPM != 66 {
		t.Errorf("AverageBPM = %v, want 66, the mean of 61 and 71", got.AverageBPM)
	}
	if got.Last7DaysAvgRestingBPM == nil || *got.Last7DaysAvgRestingBPM != 53 {
		t.Errorf("Last7DaysAvgRestingBPM = %v, want 53", got.Last7DaysAvgRestingBPM)
	}
	if calls := len(fake.Requests()); calls != 2 {
		t.Errorf("requests = %d, want 2: the summary is a view of the one read", calls)
	}
}

func TestReadHeartRatesSummaryReportsADayWithNoReadings(t *testing.T) {
	t.Parallel()

	body := `{"calendarDate":"` + cardioDate + `","heartRateValues":[[1786689600000,null]]}`
	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioHeartRatePath(), body)))

	got, err := svc.readHeartRatesSummary(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readHeartRatesSummary() = %v", err)
	}
	if got.HasData {
		t.Error("HasData = true for a day with nothing but a gap, want false")
	}
	if got.AverageBPM != nil || got.DataPoints != 0 {
		t.Errorf("AverageBPM/DataPoints = %v/%d, want nil/0", got.AverageBPM, got.DataPoints)
	}
}

func TestHeartRateSummaryLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	average := 66.0
	got := HeartRateSummary{HasData: true, AverageBPM: &average, DataPoints: 2}

	rendered := got.LogValue().String()
	if strings.Contains(rendered, "66") {
		t.Errorf("the log value %q carries a reading, want shape only", rendered)
	}
	if !strings.Contains(rendered, "average=set") {
		t.Errorf("the log value %q does not report that an average was computed", rendered)
	}
}

func TestReadHeartRatesSummaryReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioHeartRatePath())))

	_, err := svc.readHeartRatesSummary(cardioContext(t), cardioDate)
	assertSanitizedGarminFailure(t, err)
}

// TestHeartRateSummaryLogValueOmitsTheValueConditionedCount is the same coverage leak
// as the stress one: DataPoints counts the readings that were present and positive,
// not the samples that were retained, so it reports wear coverage rather than shape.
func TestHeartRateSummaryLogValueOmitsTheValueConditionedCount(t *testing.T) {
	t.Parallel()

	average := 61.5
	rendered := HeartRateSummary{
		HasData: true, DataPoints: 137, AverageBPM: &average,
	}.LogValue().String()

	if strings.Contains(rendered, "137") || strings.Contains(rendered, "dataPoints") {
		t.Errorf("LogValue = %s, want no count of readings that passed a value test", rendered)
	}
	if !strings.Contains(rendered, "average=set") {
		t.Errorf("LogValue = %s, want the presence of the average", rendered)
	}
}
