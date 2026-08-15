package tools

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

const cardioRestingHeartRateBody = `{"statisticsStartDate":"` + cardioDate + `",` +
	`"statisticsEndDate":"` + cardioDate + `","allMetrics":{"metricsMap":{` +
	`"WELLNESS_RESTING_HEART_RATE":[{"calendarDate":"` + cardioDate + `","value":52.0}]}}}`

func cardioRestingHeartRatePath() string {
	return client.PathRestingHeartRatePrefix + "/" + cardioDisplayName
}

func TestReadRestingHeartRateDayReturnsTheReading(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript(
		cardioJSON(cardioRestingHeartRatePath(), cardioRestingHeartRateBody)))

	got, err := svc.readRestingHeartRateDay(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readRestingHeartRateDay() = %v", err)
	}

	if got.Date != cardioDate {
		t.Errorf("Date = %q, want %q", got.Date, cardioDate)
	}
	if !got.HasData || got.RestingBPM == nil || *got.RestingBPM != 52 {
		t.Fatalf("HasData/RestingBPM = %t/%v, want true/52", got.HasData, got.RestingBPM)
	}

	for _, request := range fake.Requests() {
		if request.Path != cardioRestingHeartRatePath() {
			continue
		}
		if value := request.Query.Get(client.QueryMetricID); value != "60" {
			t.Errorf("metricId query = %q, want %q", value, "60")
		}
	}
}

func TestReadRestingHeartRateDayReportsADayWithNoReading(t *testing.T) {
	t.Parallel()

	body := `{"allMetrics":{"metricsMap":{"WELLNESS_RESTING_HEART_RATE":` +
		`[{"calendarDate":"` + cardioDate + `","value":null}]}}}`
	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioRestingHeartRatePath(), body)))

	got, err := svc.readRestingHeartRateDay(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readRestingHeartRateDay() = %v", err)
	}
	if got.HasData || got.RestingBPM != nil {
		t.Errorf("HasData/RestingBPM = %t/%v, want false/nil", got.HasData, got.RestingBPM)
	}
	if got.Date != cardioDate {
		t.Errorf("Date = %q, want the requested day even with no reading", got.Date)
	}
}

func TestRestingHeartRateDayLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	reading := 52.0
	rendered := RestingHeartRateDay{HasData: true, RestingBPM: &reading}.LogValue().String()
	if strings.Contains(rendered, "52") {
		t.Errorf("the log value %q carries the reading, want shape only", rendered)
	}
}

func TestReadRestingHeartRateDayReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioRestingHeartRatePath())))

	_, err := svc.readRestingHeartRateDay(cardioContext(t), cardioDate)
	assertSanitizedGarminFailure(t, err)
}
