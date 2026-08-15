package tools

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

const cardioSpO2Body = `{"calendarDate":"` + cardioDate + `","averageSpO2":95,"lowestSpO2":90,` +
	`"latestSpO2":96,"latestSpO2TimestampGMT":"` + cardioDate + `T07:00:00.0",` +
	`"lastSevenDaysAvgSpO2":"95","avgSleepSpO2":94,` +
	`"spO2HourlyAverages":[[1786689600000,95],[1786693200000,null]]}`

func cardioSpO2Path() string {
	return client.PathDailySpO2Prefix + "/" + cardioDate
}

func TestReadSpO2ReturnsTheDayAndItsHourlyAverages(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript(cardioJSON(cardioSpO2Path(), cardioSpO2Body)))

	got, err := svc.readSpO2(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readSpO2() = %v", err)
	}

	if !got.HasData {
		t.Error("HasData = false, want true")
	}
	if got.AveragePercent == nil || *got.AveragePercent != 95 {
		t.Errorf("AveragePercent = %v, want 95", got.AveragePercent)
	}
	if got.Last7DaysAvg == nil || *got.Last7DaysAvg != 95 {
		t.Errorf("Last7DaysAvg = %v, want 95 decoded from a numeric string", got.Last7DaysAvg)
	}
	if got.LatestReadingGMT == nil || *got.LatestReadingGMT != cardioDate+"T07:00:00.0" {
		t.Errorf("LatestReadingGMT = %v, want Garmin's own rendering", got.LatestReadingGMT)
	}
	if got.HourlyAverageCount != 2 {
		t.Fatalf("HourlyAverageCount = %d, want 2", got.HourlyAverageCount)
	}
	if got.HourlyAverages[1].SpO2Percent != nil {
		t.Error("the second hour carries a reading, want the gap preserved as absent")
	}
	if calls := len(fake.Requests()); calls != 1 {
		t.Errorf("requests = %d, want 1: the day is in the path", calls)
	}
}

func TestReadSpO2ReportsADayWithNoData(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioSpO2Path(), `{}`)))

	got, err := svc.readSpO2(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readSpO2() = %v", err)
	}
	if got.HasData || got.HourlyAverageCount != 0 {
		t.Errorf("HasData/HourlyAverageCount = %t/%d, want false/0",
			got.HasData, got.HourlyAverageCount)
	}
	if got.HourlyAverages == nil {
		t.Error("HourlyAverages = nil, want an empty list so the result renders as an array")
	}
}

func TestSpO2LogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	value := 95.0
	got := SpO2{
		HasData:            true,
		AveragePercent:     &value,
		HourlyAverages:     []SpO2HourlyAverage{{SpO2Percent: &value}},
		HourlyAverageCount: 1,
	}

	rendered := got.LogValue().String()
	if strings.Contains(rendered, "95") {
		t.Errorf("the log value %q carries a reading, want shape only", rendered)
	}
	if !strings.Contains(rendered, "hourlyAverages=1") {
		t.Errorf("the log value %q does not report the hourly count", rendered)
	}
}

func TestReadSpO2ReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioSpO2Path())))

	_, err := svc.readSpO2(cardioContext(t), cardioDate)
	assertSanitizedGarminFailure(t, err)
}

func TestReadSpO2ReportsADriftedHourlySeries(t *testing.T) {
	t.Parallel()

	body := `{"calendarDate":"` + cardioDate + `","spO2HourlyAverages":{"unexpected":true}}`
	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioSpO2Path(), body)))

	if _, err := svc.readSpO2(cardioContext(t), cardioDate); err == nil {
		t.Fatal("readSpO2() with a drifted series = nil, want a refusal")
	}
}
