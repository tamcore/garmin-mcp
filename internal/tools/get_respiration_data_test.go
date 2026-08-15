package tools

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// cardioRespirationBody follows the sampled document: negative sentinels rather than
// nulls in the per-sample series, a second hourly series whose descriptor list uses
// Garmin's other key spelling, a sentinel bucket whose bounds are null, and both sleep
// windows.
const cardioRespirationBody = `{"userProfilePK":900001,"calendarDate":"` + cardioDate + `",` +
	`"lowestRespirationValue":11.0,"highestRespirationValue":19.0,` +
	`"avgWakingRespirationValue":14.0,"avgSleepRespirationValue":13.0,` +
	`"avgTomorrowSleepRespirationValue":12.0,"respirationVersion":1,` +
	`"sleepStartTimestampGMT":"` + cardioDate + `T22:10:00.0",` +
	`"sleepEndTimestampGMT":"` + cardioDate + `T06:10:00.0",` +
	`"sleepStartTimestampLocal":"` + cardioDate + `T23:10:00.0",` +
	`"sleepEndTimestampLocal":"` + cardioDate + `T07:10:00.0",` +
	`"tomorrowSleepStartTimestampGMT":"` + cardioDate + `T22:40:00.0",` +
	`"tomorrowSleepEndTimestampGMT":"` + cardioDate + `T06:40:00.0",` +
	`"tomorrowSleepStartTimestampLocal":"` + cardioDate + `T23:40:00.0",` +
	`"tomorrowSleepEndTimestampLocal":"` + cardioDate + `T07:40:00.0",` +
	`"respirationValueDescriptorsDTOList":[{"index":0,"key":"timestamp"},` +
	`{"index":1,"key":"respiration"}],` +
	`"respirationValuesArray":[[1786689600000,14.0],[1786689660000,-1.0],` +
	`[1786689720000,-2.0]],` +
	`"respirationAveragesValueDescriptorDTOList":[` +
	`{"respirationAveragesValueDescriptorIndex":0,` +
	`"respirationAveragesValueDescriptionKey":"timestamp"},` +
	`{"respirationAveragesValueDescriptorIndex":1,` +
	`"respirationAveragesValueDescriptionKey":"averageRespirationValue"},` +
	`{"respirationAveragesValueDescriptorIndex":2,` +
	`"respirationAveragesValueDescriptionKey":"highRespirationValue"},` +
	`{"respirationAveragesValueDescriptorIndex":3,` +
	`"respirationAveragesValueDescriptionKey":"lowRespirationValue"}],` +
	`"respirationAveragesValuesArray":[[1786689600000,14.0,19.0,11.0],` +
	`[1786693200000,-2.0,null,null]]}`

func cardioRespirationPath() string {
	return client.PathDailyRespirationPrefix + "/" + cardioDate
}

func TestReadRespirationReturnsTheDayAndItsSeries(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript(
		cardioJSON(cardioRespirationPath(), cardioRespirationBody)))

	got, err := svc.readRespiration(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readRespiration() = %v", err)
	}

	if !got.HasData {
		t.Error("HasData = false, want true")
	}
	if got.LowestBreathsPerMin == nil || *got.LowestBreathsPerMin != 11 {
		t.Errorf("LowestBreathsPerMin = %v, want 11", got.LowestBreathsPerMin)
	}
	if got.SampleCount != 3 {
		t.Fatalf("SampleCount = %d, want 3", got.SampleCount)
	}
	if calls := len(fake.Requests()); calls != 1 {
		t.Errorf("requests = %d, want 1: the day is in the path, so no profile read is needed",
			calls)
	}
}

func TestReadRespirationNeverPresentsASentinelAsABreathRate(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(
		cardioJSON(cardioRespirationPath(), cardioRespirationBody)))

	got, err := svc.readRespiration(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readRespiration() = %v", err)
	}

	// Garmin marks "no reading" with -1.0 and -2.0 rather than with null. Taken at
	// face value they are a rate of minus one breath a minute.
	for i, want := range map[int]float64{1: -1, 2: -2} {
		sample := got.Samples[i]
		if sample.BreathsPerMinute != nil {
			t.Errorf("sample %d reports %v breaths per minute, want no reading",
				i, *sample.BreathsPerMinute)
		}
		if sample.NoReadingCode == nil || *sample.NoReadingCode != want {
			t.Errorf("sample %d no_reading_code = %v, want %v: the two markers mean "+
				"different things", i, sample.NoReadingCode, want)
		}
		if sample.TimeGMTMillis == nil {
			t.Errorf("sample %d lost its timestamp", i)
		}
	}
	if got.Samples[0].NoReadingCode != nil {
		t.Error("a real reading carries a no-reading code, want none")
	}
}

func TestReadRespirationReturnsTheHourlyAverages(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(
		cardioJSON(cardioRespirationPath(), cardioRespirationBody)))

	got, err := svc.readRespiration(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readRespiration() = %v", err)
	}

	if got.HourlyAverageCount != 2 {
		t.Fatalf("HourlyAverageCount = %d, want 2", got.HourlyAverageCount)
	}
	first := got.HourlyAverages[0]
	if first.AvgBreathsPerMin == nil || *first.AvgBreathsPerMin != 14 {
		t.Errorf("the first bucket's average = %v, want 14", first.AvgBreathsPerMin)
	}
	if first.TimeGMTMillis == nil || *first.TimeGMTMillis != 1786689600000 {
		t.Errorf("the first bucket's instant = %v, want the position the second "+
			"descriptor spelling declares", first.TimeGMTMillis)
	}
	if first.HighBreathsPerMin == nil || first.LowBreathsPerMin == nil {
		t.Error("the first bucket lost its bounds")
	}

	sentinel := got.HourlyAverages[1]
	if sentinel.AvgBreathsPerMin != nil || sentinel.HighBreathsPerMin != nil ||
		sentinel.LowBreathsPerMin != nil {
		t.Error("the sentinel bucket reports a rate, want the sentinel and the nulls absent")
	}
	if sentinel.NoReadingCode == nil || *sentinel.NoReadingCode != -2 {
		t.Errorf("the sentinel bucket's code = %v, want -2", sentinel.NoReadingCode)
	}
}

func TestReadRespirationCarriesBothSleepWindowsAndTheVersion(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(
		cardioJSON(cardioRespirationPath(), cardioRespirationBody)))

	got, err := svc.readRespiration(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readRespiration() = %v", err)
	}

	for name, value := range map[string]*string{
		"sleep_start_gmt":            got.SleepStartGMT,
		"sleep_end_gmt":              got.SleepEndGMT,
		"sleep_start_local":          got.SleepStartLocal,
		"sleep_end_local":            got.SleepEndLocal,
		"tomorrow_sleep_start_gmt":   got.TomorrowSleepStartGMT,
		"tomorrow_sleep_end_gmt":     got.TomorrowSleepEndGMT,
		"tomorrow_sleep_start_local": got.TomorrowSleepStartLocal,
		"tomorrow_sleep_end_local":   got.TomorrowSleepEndLocal,
	} {
		if value == nil {
			t.Errorf("%s is absent, want the window Garmin reported", name)
		}
	}
	if got.AvgNextNightBPM == nil || *got.AvgNextNightBPM != 12 {
		t.Errorf("AvgNextNightBPM = %v, want 12", got.AvgNextNightBPM)
	}
	if got.RespirationVersion == nil || *got.RespirationVersion != 1 {
		t.Errorf("RespirationVersion = %v, want 1", got.RespirationVersion)
	}
}

func TestReadRespirationReportsADayWithNoData(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioRespirationPath(), `{}`)))

	got, err := svc.readRespiration(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readRespiration() = %v", err)
	}
	if got.HasData || got.SampleCount != 0 || got.HourlyAverageCount != 0 {
		t.Errorf("HasData/SampleCount/HourlyAverageCount = %t/%d/%d, want false/0/0",
			got.HasData, got.SampleCount, got.HourlyAverageCount)
	}
	if got.Samples == nil || got.HourlyAverages == nil {
		t.Error("a series is nil, want empty lists so the result renders as arrays")
	}
}

func TestReadRespirationRefusesAnImpossibleDate(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript())

	if _, err := svc.readRespiration(cardioContext(t), "2026-13-45"); err == nil {
		t.Fatal("readRespiration() with an impossible date = nil, want a refusal")
	}
	if calls := len(fake.Requests()); calls != 0 {
		t.Errorf("requests = %d, want 0", calls)
	}
}

func TestRespirationLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	value := 14.0
	got := Respiration{
		HasData:             true,
		LowestBreathsPerMin: &value,
		Samples:             []RespirationSample{{BreathsPerMinute: &value}},
		SampleCount:         1,
	}

	rendered := got.LogValue().String()
	if strings.Contains(rendered, "14") {
		t.Errorf("the log value %q carries a reading, want shape only", rendered)
	}
	if !strings.Contains(rendered, "samples=1") {
		t.Errorf("the log value %q does not report the sample count", rendered)
	}
}

func TestReadRespirationReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioRespirationPath())))

	_, err := svc.readRespiration(cardioContext(t), cardioDate)
	assertSanitizedGarminFailure(t, err)
}
