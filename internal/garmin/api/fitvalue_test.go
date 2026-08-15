package api_test

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// typedRecord decodes one record message written with the declared field types. It
// is how the base types Garmin's own files do not use are still exercised: the
// container allows every one of them, so every one of them must decode or be
// skipped, never be misread.
func typedRecord(t *testing.T, fields [][3]byte, payload []byte) api.FITRecord {
	t.Helper()

	body := []byte{0x40, 0, 0}
	body = binary.LittleEndian.AppendUint16(body, 20)
	body = append(body, byte(len(fields)))
	for _, field := range fields {
		body = append(body, field[0], field[1], field[2])
	}
	body = append(body, 0x00)
	body = append(body, payload...)

	activity, err := api.ParseFITActivity(fitContainer(body), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Records) != 1 {
		t.Fatalf("%d records, want 1", len(activity.Records))
	}
	return activity.Records[0]
}

// TestParseFITDecodesTheWideBaseTypes covers the float and eight-byte base types,
// which a device may use for any field the profile allows.
func TestParseFITDecodesTheWideBaseTypes(t *testing.T) {
	t.Parallel()

	payload := binary.LittleEndian.AppendUint32(nil, fitStamp)
	payload = binary.LittleEndian.AppendUint32(payload, math.Float32bits(275.5))
	payload = binary.LittleEndian.AppendUint64(payload, math.Float64bits(21.25))
	payload = binary.LittleEndian.AppendUint64(payload, 12345)
	grade := int64(-250)
	payload = binary.LittleEndian.AppendUint64(payload, uint64(grade))

	record := typedRecord(t, [][3]byte{
		{253, 4, 0x86},
		{7, 4, 0x88},  // power as float32
		{13, 8, 0x89}, // temperature as float64
		{5, 8, 0x8F},  // distance as uint64
		{9, 8, 0x8E},  // grade as sint64
	}, payload)

	for name, want := range map[string]struct {
		got  api.FITNumber
		want float64
	}{
		namePower:       {record.Power, 275.5},
		nameTemperature: {record.Temperature, 21.25},
		nameDistance:    {record.Distance, 123.45},
		nameGrade:       {record.Grade, -2.5},
	} {
		if !want.got.OK || want.got.Value != want.want {
			t.Errorf("%s = %+v, want %v", name, want.got, want.want)
		}
	}
}

// TestParseFITDecodesTheZeroInvalidatedTypes covers the z types, whose invalid
// sentinel is zero rather than an all-ones pattern.
func TestParseFITDecodesTheZeroInvalidatedTypes(t *testing.T) {
	t.Parallel()

	payload := binary.LittleEndian.AppendUint32(nil, fitStamp)
	payload = append(payload, 0) // heart rate as uint8z, the invalid value
	payload = binary.LittleEndian.AppendUint16(payload, 95)
	payload = binary.LittleEndian.AppendUint32(payload, 0) // distance as uint32z

	record := typedRecord(t, [][3]byte{
		{253, 4, 0x86},
		{3, 1, 0x8A},
		{4, 2, 0x8B},
		{5, 4, 0x8C},
	}, payload)

	if record.HeartRate.OK {
		t.Errorf("heart rate = %+v, want the zero sentinel read as absent", record.HeartRate)
	}
	if !record.Cadence.OK || record.Cadence.Value != 95 {
		t.Errorf("cadence = %+v, want 95", record.Cadence)
	}
	if record.Distance.OK {
		t.Errorf("distance = %+v, want the zero sentinel read as absent", record.Distance)
	}
}

// TestParseFITSkipsAFieldItCannotRead proves a string-typed or unknown-typed field
// is skipped rather than read as a number.
func TestParseFITSkipsAFieldItCannotRead(t *testing.T) {
	t.Parallel()

	payload := binary.LittleEndian.AppendUint32(nil, fitStamp)
	payload = append(payload, "hello\x00\x00\x00"...) // power declared as a string
	payload = append(payload, 1, 2, 3)                // cadence declared as an unknown type

	record := typedRecord(t, [][3]byte{
		{253, 4, 0x86},
		{7, 8, 0x07},
		{4, 3, 0x1F},
	}, payload)

	if record.Power.OK || record.Cadence.OK {
		t.Errorf("record = %+v, want both unreadable fields absent", record)
	}
}

// TestParseFITReadsAnInvalidFloatAsAbsent proves the float sentinel is honoured, so
// a not-a-number never reaches an average.
func TestParseFITReadsAnInvalidFloatAsAbsent(t *testing.T) {
	t.Parallel()

	payload := binary.LittleEndian.AppendUint32(nil, fitStamp)
	payload = binary.LittleEndian.AppendUint32(payload, math.MaxUint32)

	record := typedRecord(t, [][3]byte{{253, 4, 0x86}, {7, 4, 0x88}}, payload)
	if record.Power.OK {
		t.Errorf("power = %+v, want the invalid float read as absent", record.Power)
	}
}
