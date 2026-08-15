package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Descriptor keys of the floors series, read from a sample rather than derived.
//
// Deriving one Garmin field name from a sibling does not work, and this document is
// the proof: the descriptor list is floorsValueDescriptorDTOList — "floorsValue" —
// while the rows are floorValuesArray — "floorValues". The plural moves between the
// two words inside one response. Every name here was read, none was inferred.
const (
	FloorsKeyStartTimeGMT    = "startTimeGMT"
	FloorsKeyEndTimeGMT      = "endTimeGMT"
	FloorsKeyFloorsAscended  = "floorsAscended"
	FloorsKeyFloorsDescended = "floorsDescended"
)

// Floors is the daily floors-climbed chart. It is health data.
//
// The envelope timestamps come in a GMT and a local pair, both in Garmin's own
// 2006-01-02T15:04:05.0 layout, which is not reparsed here.
type Floors struct {
	StartTimestampGMT   client.Text `json:"startTimestampGMT"`
	EndTimestampGMT     client.Text `json:"endTimestampGMT"`
	StartTimestampLocal client.Text `json:"startTimestampLocal"`
	EndTimestampLocal   client.Text `json:"endTimestampLocal"`

	// Descriptors name the columns of Rows. The wire key is the first spelling.
	Descriptors []FloorsDescriptor `json:"floorsValueDescriptorDTOList"`

	// Rows are the raw tuples, one per bucket. The wire key is the second
	// spelling, and the elements are deliberately json.RawMessage: a floors tuple
	// is **mixed-type**, two timestamp strings followed by two numbers, so the
	// all-numeric row parser this package uses for the cardio series cannot decode
	// it. client.Number tolerates a numeric string and a timestamp is not one, so
	// reusing that parser would fail the whole read rather than one column.
	Rows [][]json.RawMessage `json:"floorValuesArray"`
}

// FloorsDescriptor names one column of the floors series.
type FloorsDescriptor struct {
	Index client.Number `json:"index"`
	Key   client.Text   `json:"key"`
}

// FloorsBucket is one bucket of the day, fifteen minutes wide on the observed shape.
//
// Every field is optional. Ascent and descent are independent: a bucket may carry
// one, both or neither, and none of that is a failure.
type FloorsBucket struct {
	StartTimeGMT    client.Text
	EndTimeGMT      client.Text
	FloorsAscended  client.Number
	FloorsDescended client.Number
}

// Buckets decodes the rows column by column, taking each column's kind from the
// descriptor rather than assuming one kind for the whole row.
//
// A column whose value does not decode as the kind its key implies is left absent
// instead of failing the read: one drifted column should cost that column, never the
// day. When the descriptor list is missing the documented order is used as a
// fallback, which is the only information left at that point.
func (f Floors) Buckets() []FloorsBucket {
	columns := f.columns()
	out := make([]FloorsBucket, 0, len(f.Rows))
	for _, row := range f.Rows {
		out = append(out, buildFloorsBucket(row, columns))
	}
	return out
}

// columns maps a descriptor key onto its column position.
func (f Floors) columns() map[string]int {
	out := make(map[string]int, len(f.Descriptors))
	for _, descriptor := range f.Descriptors {
		key, ok := descriptor.Key.Value()
		if !ok || key == "" {
			continue
		}
		if index, ok := descriptor.Index.Int64(); ok && index >= 0 {
			out[key] = int(index)
		}
	}
	if len(out) > 0 {
		return out
	}
	return map[string]int{
		FloorsKeyStartTimeGMT: 0, FloorsKeyEndTimeGMT: 1,
		FloorsKeyFloorsAscended: 2, FloorsKeyFloorsDescended: 3,
	}
}

// buildFloorsBucket reads one row through the column map.
func buildFloorsBucket(row []json.RawMessage, columns map[string]int) FloorsBucket {
	return FloorsBucket{
		StartTimeGMT:    floorsText(row, columns, FloorsKeyStartTimeGMT),
		EndTimeGMT:      floorsText(row, columns, FloorsKeyEndTimeGMT),
		FloorsAscended:  floorsNumber(row, columns, FloorsKeyFloorsAscended),
		FloorsDescended: floorsNumber(row, columns, FloorsKeyFloorsDescended),
	}
}

// floorsText reads a string column, reporting absent for anything it cannot decode.
func floorsText(row []json.RawMessage, columns map[string]int, key string) client.Text {
	raw, ok := floorsCell(row, columns, key)
	if !ok {
		return client.Text{}
	}
	var text client.Text
	if err := json.Unmarshal(raw, &text); err != nil {
		return client.Text{}
	}
	return text
}

// floorsNumber reads a numeric column, reporting absent for anything it cannot
// decode. A timestamp landing in this column is absent, not an error.
func floorsNumber(row []json.RawMessage, columns map[string]int, key string) client.Number {
	raw, ok := floorsCell(row, columns, key)
	if !ok {
		return client.Number{}
	}
	var number client.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return client.Number{}
	}
	return number
}

// floorsCell returns the raw cell for key, or reports that the row does not carry it.
func floorsCell(
	row []json.RawMessage, columns map[string]int, key string,
) (json.RawMessage, bool) {
	index, named := columns[key]
	if !named || index < 0 || index >= len(row) {
		return nil, false
	}
	return row[index], true
}

// Floors reads the daily floors chart for one day.
func (w *WellnessDaily) Floors(
	ctx context.Context, session client.Session, date client.Date,
) (Floors, error) {
	req := readRequest(client.OpGetFloors, client.EndpointFloorsChartDaily,
		client.PathFloorsChartDailyPrefix+"/"+date.String(), nil)
	if err := requireDate(req, date); err != nil {
		return Floors{}, err
	}

	var floors Floors
	payload, err := w.req.read(ctx, session, req, &floors)
	if err != nil {
		return Floors{}, err
	}
	if payload.NoContent() {
		return Floors{}, unexpected(req, fmt.Errorf(
			"%w: the floors response carried no data", client.ErrUnexpectedResponse))
	}
	return floors, nil
}
