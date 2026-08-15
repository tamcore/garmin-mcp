package api_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// declaredLayout is the descriptor list a real intraday document ships.
func declaredLayout(timeIndex, valueIndex int, valueKey string) []api.SeriesDescriptor {
	return []api.SeriesDescriptor{
		{Index: client.NewNumber(float64(timeIndex)), Key: client.NewText(api.SeriesKeyTimestamp)},
		{Index: client.NewNumber(float64(valueIndex)), Key: client.NewText(valueKey)},
	}
}

func TestParseSeriesReadsTheDeclaredPositions(t *testing.T) {
	t.Parallel()

	rows := json.RawMessage(`[[61,1786689600000],[66,1786689720000]]`)
	samples, err := api.ParseSeries(rows, declaredLayout(1, 0, api.SeriesKeyHeartRate),
		api.SeriesKeyHeartRate)
	if err != nil {
		t.Fatalf("ParseSeries() = %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}
	if value, _ := samples[0].Value.Float64(); value != 61 {
		t.Errorf("the first value = %v, want 61 from the declared position", value)
	}
	if instant, _ := samples[0].TimeMillis.Int64(); instant != 1786689600000 {
		t.Errorf("the first instant = %v, want the declared timestamp position", instant)
	}
}

func TestParseSeriesFallsBackToTheObservedOrderWithoutDescriptors(t *testing.T) {
	t.Parallel()

	rows := json.RawMessage(`[[1786689600000,61]]`)
	samples, err := api.ParseSeries(rows, nil, api.SeriesKeyHeartRate)
	if err != nil {
		t.Fatalf("ParseSeries() = %v", err)
	}
	if instant, _ := samples[0].TimeMillis.Int64(); instant != 1786689600000 {
		t.Errorf("the instant = %v, want the leading tuple position", instant)
	}
}

func TestParseSeriesKeepsANullReadingAsAnAbsentValue(t *testing.T) {
	t.Parallel()

	rows := json.RawMessage(`[[1786689720000,null]]`)
	samples, err := api.ParseSeries(rows, nil, api.SeriesKeyHeartRate)
	if err != nil {
		t.Fatalf("ParseSeries() = %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("len(samples) = %d, want 1: the gap keeps its point", len(samples))
	}
	if samples[0].Value.IsSet() {
		t.Error("the null reading decoded as a value, want it reported as absent")
	}
}

func TestParseSeriesReportsAnAbsentSeriesAsNoSamples(t *testing.T) {
	t.Parallel()

	for name, rows := range map[string]json.RawMessage{
		"missing": nil,
		nullBody:  json.RawMessage(nullBody),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			samples, err := api.ParseSeries(rows, nil, api.SeriesKeyHeartRate)
			if err != nil {
				t.Fatalf("ParseSeries() = %v, want no error", err)
			}
			if len(samples) != 0 {
				t.Errorf("len(samples) = %d, want 0", len(samples))
			}
		})
	}
}

func TestParseSeriesRefusesADeclarationTheTuplesCannotSatisfy(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		rows        json.RawMessage
		descriptors []api.SeriesDescriptor
	}{
		"tuple shorter than the declaration": {
			rows:        json.RawMessage(`[[1786689600000]]`),
			descriptors: declaredLayout(0, 1, api.SeriesKeyHeartRate),
		},
		"declaration names no value position": {
			rows:        json.RawMessage(`[[1786689600000,61]]`),
			descriptors: declaredLayout(0, 1, api.SeriesKeyRespiration),
		},
		"declaration collapses both positions": {
			rows:        json.RawMessage(`[[1786689600000,61]]`),
			descriptors: declaredLayout(0, 0, api.SeriesKeyHeartRate),
		},
		"series is not a list of tuples": {
			rows:        json.RawMessage(`{"values":[]}`),
			descriptors: nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := api.ParseSeries(tc.rows, tc.descriptors, api.SeriesKeyHeartRate)
			if !errors.Is(err, client.ErrUnexpectedResponse) {
				t.Fatalf("ParseSeries() = %v, want ErrUnexpectedResponse", err)
			}
		})
	}
}

func TestParseSeriesReportsANegativeReadingAsASentinelAndNotAsAValue(t *testing.T) {
	t.Parallel()

	// A real respiration day carries -1.0 and -2.0 where there is no reading. Taken
	// at face value they are a rate of minus one breath a minute, and any minimum or
	// mean computed over the series is then silently wrong.
	rows := json.RawMessage(`[[1786689600000,14.0],[1786689660000,-1.0],[1786689720000,-2.0]]`)
	samples, err := api.ParseSeries(rows, nil, api.SeriesKeyRespiration)
	if err != nil {
		t.Fatalf("ParseSeries() = %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("len(samples) = %d, want 3: a sentinel keeps its point", len(samples))
	}

	if value, ok := samples[0].Value.Float64(); !ok || value != 14 {
		t.Errorf("the first reading = %v/%t, want 14", value, ok)
	}
	if samples[0].Sentinel.IsSet() {
		t.Error("a real reading carries a sentinel, want none")
	}

	for i, want := range map[int]float64{1: -1, 2: -2} {
		if samples[i].Value.IsSet() {
			t.Errorf("sample %d presents the sentinel as a reading, want no reading", i)
		}
		if code, ok := samples[i].Sentinel.Float64(); !ok || code != want {
			t.Errorf("sample %d sentinel = %v/%t, want %v: the two markers are not "+
				"interchangeable", i, code, ok, want)
		}
		if !samples[i].TimeMillis.IsSet() {
			t.Errorf("sample %d lost its timestamp", i)
		}
	}
}

func TestParseAverageSeriesReadsTheSecondDescriptorSpelling(t *testing.T) {
	t.Parallel()

	// The averages list declares its positions under respirationAveragesValueDescriptorIndex
	// and respirationAveragesValueDescriptionKey — "Description", not "Descriptor".
	// A reader written for index/key finds nothing here and silently falls back to
	// positional order, so this declaration is deliberately reversed: a positional
	// fallback would read the low as the timestamp and pass a laxer assertion.
	descriptors := []api.SeriesDescriptor{
		{AveragesIndex: client.NewNumber(3), AveragesKey: client.NewText(api.SeriesKeyTimestamp)},
		{AveragesIndex: client.NewNumber(2), AveragesKey: client.NewText(api.SeriesKeyAverageRespiration)},
		{AveragesIndex: client.NewNumber(1), AveragesKey: client.NewText(api.SeriesKeyHighRespiration)},
		{AveragesIndex: client.NewNumber(0), AveragesKey: client.NewText(api.SeriesKeyLowRespiration)},
	}
	rows := json.RawMessage(`[[11.0,19.0,14.0,1786689600000]]`)

	samples, err := api.ParseAverageSeries(rows, descriptors)
	if err != nil {
		t.Fatalf("ParseAverageSeries() = %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("len(samples) = %d, want 1", len(samples))
	}
	got := samples[0]
	if instant, _ := got.TimeMillis.Int64(); instant != 1786689600000 {
		t.Errorf("timestamp = %v, want the declared position, not position 0", instant)
	}
	if value, _ := got.Average.Float64(); value != 14 {
		t.Errorf("average = %v, want 14", value)
	}
	if value, _ := got.High.Float64(); value != 19 {
		t.Errorf("high = %v, want 19", value)
	}
	if value, _ := got.Low.Float64(); value != 11 {
		t.Errorf("low = %v, want 11", value)
	}
}

func TestParseAverageSeriesToleratesASentinelBucketWithNullBounds(t *testing.T) {
	t.Parallel()

	// Where the hourly average is the -2.0 sentinel, the high and the low are null:
	// one tuple mixes a sentinel with two nulls, so tuple length says nothing about
	// whether every element is a number.
	rows := json.RawMessage(`[[1786689600000,14.0,19.0,11.0],[1786693200000,-2.0,null,null]]`)

	samples, err := api.ParseAverageSeries(rows, nil)
	if err != nil {
		t.Fatalf("ParseAverageSeries() = %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}

	sentinel := samples[1]
	if sentinel.Average.IsSet() {
		t.Error("the sentinel bucket presents an average, want no reading")
	}
	if code, ok := sentinel.Sentinel.Float64(); !ok || code != -2 {
		t.Errorf("the sentinel bucket's marker = %v/%t, want -2", code, ok)
	}
	if sentinel.High.IsSet() || sentinel.Low.IsSet() {
		t.Error("the sentinel bucket carries bounds, want the nulls reported as absent")
	}
	if !sentinel.TimeMillis.IsSet() {
		t.Error("the sentinel bucket lost its timestamp")
	}
}

func TestParseAverageSeriesReportsAnAbsentSeriesAsNoSamples(t *testing.T) {
	t.Parallel()

	samples, err := api.ParseAverageSeries(nil, nil)
	if err != nil {
		t.Fatalf("ParseAverageSeries() = %v, want no error", err)
	}
	if len(samples) != 0 {
		t.Errorf("len(samples) = %d, want 0", len(samples))
	}
}

func TestParseAverageSeriesRefusesADeclarationTheTuplesCannotSatisfy(t *testing.T) {
	t.Parallel()

	descriptors := []api.SeriesDescriptor{
		{AveragesIndex: client.NewNumber(0), AveragesKey: client.NewText(api.SeriesKeyTimestamp)},
		{AveragesIndex: client.NewNumber(1), AveragesKey: client.NewText(api.SeriesKeyAverageRespiration)},
	}
	_, err := api.ParseAverageSeries(json.RawMessage(`[[1,2,3,4]]`), descriptors)
	if !errors.Is(err, client.ErrUnexpectedResponse) {
		t.Fatalf("ParseAverageSeries() with an incomplete declaration = %v, "+
			"want ErrUnexpectedResponse", err)
	}
}

func TestParseSeriesRefusesANegativeDeclaredIndex(t *testing.T) {
	t.Parallel()

	descriptors := []api.SeriesDescriptor{
		{Index: client.NewNumber(-1), Key: client.NewText(api.SeriesKeyTimestamp)},
		{Index: client.NewNumber(1), Key: client.NewText(api.SeriesKeyHeartRate)},
	}
	_, err := api.ParseSeries(json.RawMessage(`[[1,2]]`), descriptors, api.SeriesKeyHeartRate)
	if !errors.Is(err, client.ErrUnexpectedResponse) {
		t.Fatalf("ParseSeries() with a negative index = %v, want ErrUnexpectedResponse", err)
	}
}

func TestBoundSamplesCutsAtTheLimitAndReportsIt(t *testing.T) {
	t.Parallel()

	samples := make([]api.Sample, 5)

	kept, truncated := api.BoundSamples(samples, 3)
	if len(kept) != 3 || !truncated {
		t.Fatalf("BoundSamples(5, 3) = %d/%t, want 3/true", len(kept), truncated)
	}

	kept, truncated = api.BoundSamples(samples, 5)
	if len(kept) != 5 || truncated {
		t.Fatalf("BoundSamples(5, 5) = %d/%t, want 5/false", len(kept), truncated)
	}

	kept, truncated = api.BoundSamples(samples, 0)
	if len(kept) != 5 || truncated {
		t.Fatalf("BoundSamples(5, 0) = %d/%t, want 5/false: a non-positive limit is no bound",
			len(kept), truncated)
	}
}
