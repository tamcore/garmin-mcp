package api_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// respirationBody follows the sampled document: two series with two different
// descriptor key spellings, negative sentinels rather than nulls in the per-sample
// series, a nulls-bearing sentinel bucket in the hourly one, and both sleep windows.
const respirationBody = `{"userProfilePK":900001,"calendarDate":"` + testCalendarDate + `",` +
	`"lowestRespirationValue":11.0,"highestRespirationValue":19.0,` +
	`"avgWakingRespirationValue":14.0,"avgSleepRespirationValue":13.0,` +
	`"avgTomorrowSleepRespirationValue":12.0,"respirationVersion":1,` +
	`"sleepStartTimestampGMT":"` + testCalendarDate + `T22:10:00.0",` +
	`"sleepEndTimestampGMT":"` + testCalendarDate + `T06:10:00.0",` +
	`"sleepStartTimestampLocal":"` + testCalendarDate + `T23:10:00.0",` +
	`"sleepEndTimestampLocal":"` + testCalendarDate + `T07:10:00.0",` +
	`"tomorrowSleepStartTimestampGMT":"` + testCalendarDate + `T22:40:00.0",` +
	`"tomorrowSleepEndTimestampGMT":"` + testCalendarDate + `T06:40:00.0",` +
	`"tomorrowSleepStartTimestampLocal":"` + testCalendarDate + `T23:40:00.0",` +
	`"tomorrowSleepEndTimestampLocal":"` + testCalendarDate + `T07:40:00.0",` +
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

func respirationPath() string {
	return client.PathDailyRespirationPrefix + "/" + testCalendarDate
}

func TestWellnessCardioRespirationDecodesTheDayAndItsSeries(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(respirationPath(), testkit.JSON(http.StatusOK, respirationBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).Respiration(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("Respiration() = %v", err)
	}
	if value, ok := got.LowestRespirationValue.Float64(); !ok || value != 11 {
		t.Errorf("LowestRespirationValue = %v/%t, want 11", value, ok)
	}

	if value, ok := got.AvgTomorrowSleepRespirationValue.Float64(); !ok || value != 12 {
		t.Errorf("AvgTomorrowSleepRespirationValue = %v/%t, want 12", value, ok)
	}
	if _, ok := got.SleepStartTimestampGMT.Value(); !ok {
		t.Error("SleepStartTimestampGMT is absent, want last night's window")
	}
	if _, ok := got.TomorrowSleepEndTimestampLocal.Value(); !ok {
		t.Error("TomorrowSleepEndTimestampLocal is absent, want the coming night's window")
	}

	samples, err := got.Samples()
	if err != nil {
		t.Fatalf("Samples() = %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("len(Samples()) = %d, want 3", len(samples))
	}
	for i := 1; i < 3; i++ {
		if samples[i].Value.IsSet() {
			t.Errorf("sample %d presents a negative sentinel as a reading", i)
		}
		if !samples[i].Sentinel.IsSet() {
			t.Errorf("sample %d dropped the sentinel Garmin sent", i)
		}
	}
}

func TestWellnessCardioRespirationDecodesTheHourlyAverages(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(respirationPath(), testkit.JSON(http.StatusOK, respirationBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).Respiration(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("Respiration() = %v", err)
	}

	hourly, err := got.HourlyAverages()
	if err != nil {
		t.Fatalf("HourlyAverages() = %v", err)
	}
	if len(hourly) != 2 {
		t.Fatalf("len(HourlyAverages()) = %d, want 2", len(hourly))
	}
	if value, ok := hourly[0].Average.Float64(); !ok || value != 14 {
		t.Errorf("the first bucket's average = %v/%t, want 14", value, ok)
	}
	if instant, ok := hourly[0].TimeMillis.Int64(); !ok || instant != 1786689600000 {
		t.Errorf("the first bucket's instant = %v/%t, want the declared position", instant, ok)
	}
	if hourly[1].Average.IsSet() || hourly[1].High.IsSet() || hourly[1].Low.IsSet() {
		t.Error("the sentinel bucket carries a figure, want the sentinel and the nulls absent")
	}
	if code, ok := hourly[1].Sentinel.Float64(); !ok || code != -2 {
		t.Errorf("the sentinel bucket's marker = %v/%t, want -2", code, ok)
	}
}

func TestWellnessCardioRespirationSummaryReadsTheSameDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(respirationPath(), testkit.JSON(http.StatusOK, respirationBody))
	h := newHarness(t, script, client.Limits{})

	if _, err := newCardio(t, h).RespirationSummary(
		t.Context(), h.session, mustDate(t, testCalendarDate),
	); err != nil {
		t.Fatalf("RespirationSummary() = %v", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestWellnessCardioRespirationToleratesAMissingSeries(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(respirationPath(), testkit.JSON(http.StatusOK,
			`{"calendarDate":"`+testCalendarDate+`","lowestRespirationValue":11}`))
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).Respiration(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("Respiration() = %v", err)
	}
	samples, err := got.Samples()
	if err != nil {
		t.Fatalf("Samples() with no series = %v, want no error", err)
	}
	if len(samples) != 0 {
		t.Errorf("len(Samples()) = %d, want 0", len(samples))
	}
}
