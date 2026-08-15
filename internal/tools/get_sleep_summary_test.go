package tools

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

const cardioSleepBody = `{"dailySleepDTO":{"calendarDate":"` + cardioDate + `",` +
	`"sleepTimeSeconds":27000,"napTimeSeconds":0,"deepSleepSeconds":5400,` +
	`"lightSleepSeconds":16200,"remSleepSeconds":5400,"awakeSleepSeconds":600,` +
	`"awakeCount":1,"restlessMomentsCount":9,"avgSleepStress":17.0,"restingHeartRate":52,` +
	`"sleepStartTimestampGMT":1786689600000,"sleepEndTimestampGMT":1786716600000,` +
	`"sleepScores":{"overall":{"value":81,"qualifierKey":"GOOD"}}},` +
	`"wellnessSpO2SleepSummaryDTO":{"averageSpo2":96,"lowestSpo2":92},"avgOvernightHrv":48.0}`

func cardioSleepPath() string {
	return client.PathDailySleepPrefix + "/" + cardioDisplayName
}

func TestReadSleepSummaryReturnsBothViewsOfTheOneDocument(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript(cardioJSON(cardioSleepPath(), cardioSleepBody)))

	got, err := svc.readSleepSummary(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readSleepSummary() = %v", err)
	}

	if !got.HasData {
		t.Error("HasData = false, want true")
	}
	if got.SleepScoreQualifier == nil || *got.SleepScoreQualifier != "GOOD" {
		t.Errorf("SleepScoreQualifier = %v, want GOOD", got.SleepScoreQualifier)
	}
	for _, field := range []struct {
		name string
		got  *float64
		want float64
	}{
		{"SleepSeconds", got.SleepSeconds, 27000},
		{"SleepScore", got.SleepScore, 81},
		{"RestlessMomentsCount", got.RestlessMomentsCount, 9},
		{"AvgSpO2Percent", got.AvgSpO2Percent, 96},
		{"AvgOvernightHRV", got.AvgOvernightHRV, 48},
	} {
		if field.got == nil || *field.got != field.want {
			t.Errorf("%s = %v, want %v", field.name, field.got, field.want)
		}
	}

	if calls := len(fake.Requests()); calls != 2 {
		t.Errorf("requests = %d, want 2: one profile read and one sleep read, "+
			"since the digest re-reads the retained payload", calls)
	}
}

func TestReadSleepSummaryReportsANightWithNoData(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(
		cardioJSON(cardioSleepPath(), `{"dailySleepDTO":null}`)))

	got, err := svc.readSleepSummary(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readSleepSummary() = %v", err)
	}
	if got.HasData {
		t.Error("HasData = true for a night with no summary, want false")
	}
	if got.Date != cardioDate {
		t.Errorf("Date = %q, want the requested day", got.Date)
	}
}

func TestSleepSummaryLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	score := 81.0
	spo2 := 96.0
	got := SleepSummary{HasData: true, SleepScore: &score, AvgSpO2Percent: &spo2}

	rendered := got.LogValue().String()
	for _, forbidden := range []string{"81", "96"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the log value %q carries a reading, want shape only", rendered)
		}
	}
	if !strings.Contains(rendered, "score=set") {
		t.Errorf("the log value %q does not report the score's presence", rendered)
	}
}

func TestReadSleepSummaryReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioSleepPath())))

	_, err := svc.readSleepSummary(cardioContext(t), cardioDate)
	assertSanitizedGarminFailure(t, err)
}
