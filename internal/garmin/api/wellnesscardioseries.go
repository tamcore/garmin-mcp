package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Descriptor keys Garmin declares for the intraday series this package reads.
//
// Source: the descriptor arrays of real daily documents. The heart-rate document
// declares {"index":0,"key":"timestamp"} and {"index":1,"key":"heartrate"}; the
// respiration document declares timestamp/respiration for its per-sample series and
// timestamp/averageRespirationValue/highRespirationValue/lowRespirationValue for its
// hourly one. Garmin ships the descriptors precisely because the order is not
// guaranteed.
const (
	SeriesKeyTimestamp          = "timestamp"
	SeriesKeyHeartRate          = "heartrate"
	SeriesKeyRespiration        = "respiration"
	SeriesKeyAverageRespiration = "averageRespirationValue"
	SeriesKeyHighRespiration    = "highRespirationValue"
	SeriesKeyLowRespiration     = "lowRespirationValue"
)

// A SeriesDescriptor is one entry of a Garmin value-descriptor list: it names which
// tuple position carries which quantity.
//
// It decodes two spellings, because Garmin uses two. The per-sample lists say index
// and key; the respiration averages list says respirationAveragesValueDescriptorIndex
// and respirationAveragesValueDescriptionKey — "Description", not "Descriptor", in the
// second. A reader written for one spelling finds nothing in the other and silently
// falls back to positional order, which is the exact failure the descriptors exist to
// prevent, so both are declared here rather than inferred.
type SeriesDescriptor struct {
	Index client.Number `json:"index"`
	Key   client.Text   `json:"key"`

	AveragesIndex client.Number `json:"respirationAveragesValueDescriptorIndex"`
	AveragesKey   client.Text   `json:"respirationAveragesValueDescriptionKey"`
}

// position returns the declared tuple position, in either spelling.
func (d SeriesDescriptor) position() (int, bool) {
	number := d.Index
	if !number.IsSet() {
		number = d.AveragesIndex
	}
	value, ok := number.Int64()
	if !ok || value < 0 {
		return 0, false
	}
	return int(value), true
}

// name returns the declared quantity, in either spelling.
func (d SeriesDescriptor) name() (string, bool) {
	text := d.Key
	if !text.IsSet() {
		text = d.AveragesKey
	}
	return text.Value()
}

// A Sample is one point of an intraday series.
//
// A point can carry no reading in two different ways, and both are normal. The value
// can be null, and it can be a negative sentinel: a real respiration day carries -1.0
// and -2.0 in different stretches, which are two distinct reasons for "no reading" and
// not measurements. Either way Value is absent and the timestamp is kept, so nothing
// downstream can average a minus-one breath per minute into a daily figure. Sentinel
// keeps the marker Garmin sent, so the two reasons stay distinguishable.
//
// Nothing may assume the series is contiguous, that the cadence is fixed, or that the
// sample count implies elapsed time — the pair after a gap can jump by an hour.
//
// A sample is a health reading. Never log it.
type Sample struct {
	// TimeMillis is the sample instant as a UTC epoch in milliseconds.
	TimeMillis client.Number
	// Value is the reading. It is absent for a null and for a sentinel.
	Value client.Number
	// Sentinel is the negative marker Garmin sent in place of a reading, when it
	// sent one. It is absent for a real reading and for a null.
	Sentinel client.Number
}

// An AverageSample is one bucket of an hourly aggregate series.
//
// Garmin sends the whole bucket as a sentinel too, and when it does the high and low
// of that bucket are null: one tuple mixes a sentinel with two nulls, so the tuple
// length says nothing about whether every element is a number.
type AverageSample struct {
	TimeMillis client.Number
	Average    client.Number
	Sentinel   client.Number
	High       client.Number
	Low        client.Number
}

// ParseSeries maps Garmin's two-element tuple rows onto samples, reading the tuple
// layout from the declared descriptors rather than from a hardcoded position.
//
// An absent or null series is no samples and no error: a day with no wearable data is
// a normal state. A series whose declaration and tuples disagree is reported as an
// unexpected response rather than silently misread, because a misread series would
// present a timestamp as a heart rate.
func ParseSeries(
	rows json.RawMessage, descriptors []SeriesDescriptor, valueKey string,
) ([]Sample, error) {
	tuples, ok, err := seriesTuples(rows)
	if err != nil || !ok {
		return nil, err
	}

	positions, err := seriesPositions(descriptors, []string{SeriesKeyTimestamp, valueKey})
	if err != nil {
		return nil, err
	}

	out := make([]Sample, 0, len(tuples))
	for _, tuple := range tuples {
		if err := requireWidth(tuple, positions); err != nil {
			return nil, err
		}
		value, sentinel := splitReading(tuple[positions[1]])
		out = append(out, Sample{
			TimeMillis: tuple[positions[0]],
			Value:      value,
			Sentinel:   sentinel,
		})
	}
	return out, nil
}

// ParseAverageSeries maps Garmin's four-element aggregate rows onto bucket samples.
func ParseAverageSeries(
	rows json.RawMessage, descriptors []SeriesDescriptor,
) ([]AverageSample, error) {
	tuples, ok, err := seriesTuples(rows)
	if err != nil || !ok {
		return nil, err
	}

	positions, err := seriesPositions(descriptors, []string{
		SeriesKeyTimestamp,
		SeriesKeyAverageRespiration,
		SeriesKeyHighRespiration,
		SeriesKeyLowRespiration,
	})
	if err != nil {
		return nil, err
	}

	out := make([]AverageSample, 0, len(tuples))
	for _, tuple := range tuples {
		if err := requireWidth(tuple, positions); err != nil {
			return nil, err
		}
		average, sentinel := splitReading(tuple[positions[1]])
		high, _ := splitReading(tuple[positions[2]])
		low, _ := splitReading(tuple[positions[3]])
		out = append(out, AverageSample{
			TimeMillis: tuple[positions[0]],
			Average:    average,
			Sentinel:   sentinel,
			High:       high,
			Low:        low,
		})
	}
	return out, nil
}

// splitReading separates a real reading from a sentinel.
//
// Garmin marks "no reading" with a negative number rather than with null in at least
// the respiration series, and a negative breath rate is not a measurement. Anything
// negative therefore becomes an absent value plus the marker that was sent, so no
// caller can average it into a daily figure. A null is absent in both.
func splitReading(raw client.Number) (value, sentinel client.Number) {
	number, ok := raw.Float64()
	if !ok {
		return client.Number{}, client.Number{}
	}
	if number < 0 {
		return client.Number{}, raw
	}
	return raw, client.Number{}
}

// seriesTuples decodes the rows, reporting an absent series as "no rows, no error".
func seriesTuples(rows json.RawMessage) ([][]client.Number, bool, error) {
	trimmed := strings.TrimSpace(string(rows))
	if trimmed == "" || trimmed == jsonNull {
		return nil, false, nil
	}

	var tuples [][]client.Number
	if err := json.Unmarshal(rows, &tuples); err != nil {
		return nil, false, fmt.Errorf("%w: the intraday series is not a list of numeric tuples",
			client.ErrUnexpectedResponse)
	}
	return tuples, true, nil
}

// requireWidth refuses a tuple the declared layout cannot address.
func requireWidth(tuple []client.Number, positions []int) error {
	for _, position := range positions {
		if position >= len(tuple) {
			return fmt.Errorf(
				"%w: an intraday tuple is shorter than the declared series layout",
				client.ErrUnexpectedResponse)
		}
	}
	return nil
}

// seriesPositions resolves one tuple position per key from the descriptors.
//
// Without descriptors the positions fall back to the observed order, which is the keys
// in the order they are asked for. With descriptors every key must be declared exactly
// once: a missing key and two keys sharing a position are both reported rather than
// guessed around.
func seriesPositions(descriptors []SeriesDescriptor, keys []string) ([]int, error) {
	positions := make([]int, len(keys))
	if len(descriptors) == 0 {
		for i := range positions {
			positions[i] = i
		}
		return positions, nil
	}

	taken := make(map[int]bool, len(keys))
	for i, key := range keys {
		position, ok := descriptorIndex(descriptors, key)
		if !ok {
			return nil, fmt.Errorf("%w: the series descriptors declare no position for %s",
				client.ErrUnexpectedResponse, key)
		}
		if taken[position] {
			return nil, fmt.Errorf(
				"%w: the series descriptors put two quantities in one position",
				client.ErrUnexpectedResponse)
		}
		taken[position] = true
		positions[i] = position
	}
	return positions, nil
}

// descriptorIndex returns the declared position of key, and whether it is usable.
func descriptorIndex(descriptors []SeriesDescriptor, key string) (int, bool) {
	for _, descriptor := range descriptors {
		name, ok := descriptor.name()
		if !ok || !strings.EqualFold(name, key) {
			continue
		}
		return descriptor.position()
	}
	return 0, false
}

// BoundSamples returns at most limit entries and whether the series was cut.
//
// A non-positive limit is no bound. The returned slice shares its backing array with
// the input, which is safe because nothing here appends to either.
func BoundSamples[T Sample | AverageSample](samples []T, limit int) ([]T, bool) {
	if limit <= 0 || len(samples) <= limit {
		return samples, false
	}
	return samples[:limit], true
}
