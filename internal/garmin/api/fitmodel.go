package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The FIT global message numbers this server reads. Everything else is skipped.
const (
	mesgSession = 18
	mesgLap     = 19
	mesgRecord  = 20
	mesgEvent   = 21
)

// The record message field numbers this server reads, with the scale and offset the
// format applies. A raw value is divided by the scale and then reduced by the offset.
const (
	fieldRecordAltitude    = 2
	fieldRecordHeartRate   = 3
	fieldRecordCadence     = 4
	fieldRecordDistance    = 5
	fieldRecordSpeed       = 6
	fieldRecordPower       = 7
	fieldRecordGrade       = 9
	fieldRecordTemperature = 13
	fieldRecordBalance     = 30
	fieldRecordLeftTE      = 43
	fieldRecordRightTE     = 44
	fieldRecordLeftPS      = 45
	fieldRecordRightPS     = 46
	fieldRecordLeftPCO     = 67
	fieldRecordRightPCO    = 68
	fieldRecordEnhSpeed    = 73
	fieldRecordEnhAltitude = 78
)

// Scales and offsets of the record fields above.
const (
	scaleAltitude    = 5.0
	offsetAltitude   = 500.0
	scaleDistance    = 100.0
	scaleSpeed       = 1000.0
	scaleGrade       = 100.0
	scalePedalMetric = 2.0
)

// The session, lap and event field numbers this server reads.
const (
	fieldStartTime  = 2
	fieldSport      = 5
	fieldEventKind  = 0
	fieldEventData  = 3
	eventFrontShift = 42
	eventRearShift  = 43
)

// balanceRightFlag marks a left_right_balance reading as the right-side share.
const (
	balanceRightFlag = 0x80
	balanceMask      = 0x7F
	balanceWhole     = 100.0
)

// A FITNumber is one optional reading. It is a value rather than a pointer because
// an activity carries tens of thousands of records and a pointer per field would be
// an allocation per field.
type FITNumber struct {
	Value float64
	OK    bool
}

// fitNumber returns a present reading.
func fitNumber(value float64) FITNumber { return FITNumber{Value: value, OK: true} }

// A FITRecord is one sample of the record stream.
//
// Coordinates are deliberately absent. The FIT file carries them, and this server
// decodes activity files without ever building a track: a per-second position series
// is the most sensitive thing in the file and no summary here needs it.
type FITRecord struct {
	Time         time.Time
	HeartRate    FITNumber
	Cadence      FITNumber
	Power        FITNumber
	Speed        FITNumber
	Altitude     FITNumber
	Distance     FITNumber
	Grade        FITNumber
	Temperature  FITNumber
	RightBalance FITNumber
	LeftTorque   FITNumber
	RightTorque  FITNumber
	LeftSmooth   FITNumber
	RightSmooth  FITNumber
	LeftPCO      FITNumber
	RightPCO     FITNumber
}

// A FITSpan is the time window of one session or lap.
type FITSpan struct {
	Start time.Time
	End   time.Time
	Sport string
}

// A FITShift is one electronic gear change.
type FITShift struct {
	Time      time.Time
	Front     bool
	FrontGear FITNumber
	RearGear  FITNumber
}

// A FITActivity is the decoded content of one activity file.
type FITActivity struct {
	Sessions []FITSpan
	Laps     []FITSpan
	Records  []FITRecord
	Shifts   []FITShift

	// RecordsTruncated reports that the record stream hit the configured bound and
	// the decode stopped collecting rather than growing without limit.
	RecordsTruncated bool
}

// ParseFITActivity decodes one activity file into the model above.
//
// data is what Garmin served for the original format: a zip archive holding the
// device FIT file, or the bare FIT file. Both are accepted, and both are bounded
// before anything is decoded, so a small archive that expands into a large file is
// refused rather than decoded.
func ParseFITActivity(data []byte, limits FITLimits) (FITActivity, error) {
	resolved := limits.withDefaults()
	raw, err := extractFIT(data, resolved)
	if err != nil {
		return FITActivity{}, err
	}

	builder := &fitBuilder{limits: resolved}
	if err := decodeFIT(raw, resolved, builder.visit); err != nil {
		return FITActivity{}, err
	}
	return builder.activity(), nil
}

// zipMagic is the local file header signature of a zip archive.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// fitExtension is the archive entry this server unpacks.
const fitExtension = ".fit"

// extractFIT returns the FIT bytes of an archive, or the input when it already is a
// FIT file.
func extractFIT(data []byte, limits FITLimits) ([]byte, error) {
	if int64(len(data)) > limits.MaxBytes {
		return nil, fmt.Errorf("%w: the downloaded activity file is larger than this server decodes",
			client.ErrResponseTooLarge)
	}
	if !bytes.HasPrefix(data, zipMagic) {
		return data, nil
	}

	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: the activity archive could not be opened",
			client.ErrMalformedPayload)
	}
	for _, entry := range archive.File {
		if strings.HasSuffix(strings.ToLower(entry.Name), fitExtension) {
			return readArchiveEntry(entry, limits)
		}
	}
	return nil, fmt.Errorf("%w: the activity archive holds no FIT file",
		client.ErrMalformedPayload)
}

// readArchiveEntry expands one archive entry under the byte bound, so a compression
// bomb is refused at the bound rather than at the allocator.
func readArchiveEntry(entry *zip.File, limits FITLimits) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: the activity archive entry could not be opened",
			client.ErrMalformedPayload)
	}
	defer func() { _ = reader.Close() }()

	raw, err := io.ReadAll(io.LimitReader(reader, limits.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: the activity archive entry could not be read",
			client.ErrMalformedPayload)
	}
	if int64(len(raw)) > limits.MaxBytes {
		return nil, fmt.Errorf("%w: the activity file expands past this server's bound",
			client.ErrResponseTooLarge)
	}
	return raw, nil
}

// fitBuilder collects the messages this server reads out of the decode.
type fitBuilder struct {
	limits    FITLimits
	sessions  []FITSpan
	laps      []FITSpan
	records   []FITRecord
	shifts    []FITShift
	truncated bool
}

// maxFITShifts bounds the collected gear changes. A long ride shifts often, and the
// shift list is a summary input, not a transcript.
const maxFITShifts = 5000

// visit dispatches one decoded message.
func (b *fitBuilder) visit(global uint16, fields fitFields) error {
	switch global {
	case mesgRecord:
		b.addRecord(fields)
	case mesgSession:
		b.sessions = append(b.sessions, readSpan(fields, true))
	case mesgLap:
		b.laps = append(b.laps, readSpan(fields, false))
	case mesgEvent:
		b.addShift(fields)
	}
	return nil
}

// activity returns the collected model.
func (b *fitBuilder) activity() FITActivity {
	return FITActivity{
		Sessions:         b.sessions,
		Laps:             b.laps,
		Records:          b.records,
		Shifts:           b.shifts,
		RecordsTruncated: b.truncated,
	}
}

// addRecord collects one sample, up to the record bound.
func (b *fitBuilder) addRecord(fields fitFields) {
	stamp, ok := fields.timestamp(fieldTimestamp)
	if !ok {
		return
	}
	if len(b.records) >= b.limits.MaxRecords {
		b.truncated = true
		return
	}
	b.records = append(b.records, readRecord(stamp, fields))
}

// addShift collects one electronic gear change, up to the shift bound.
func (b *fitBuilder) addShift(fields fitFields) {
	kind, ok := fields.unsigned(fieldEventKind)
	if !ok || (kind != eventFrontShift && kind != eventRearShift) {
		return
	}
	stamp, ok := fields.timestamp(fieldTimestamp)
	if !ok || len(b.shifts) >= maxFITShifts {
		return
	}
	packed, ok := fields.unsigned(fieldEventData)
	if !ok {
		return
	}
	b.shifts = append(b.shifts, decodeGearChange(stamp, kind == eventFrontShift, packed))
}

// decodeGearChange unpacks the gear_change_data bit field: the rear gear number and
// gear in the low two bytes, the front gear number and gear in the high two.
func decodeGearChange(stamp time.Time, front bool, packed uint64) FITShift {
	const (
		byteMask  = 0xFF
		rearShift = 8
		frontMove = 24
	)
	return FITShift{
		Time:      stamp,
		Front:     front,
		RearGear:  fitNumber(float64((packed >> rearShift) & byteMask)),
		FrontGear: fitNumber(float64((packed >> frontMove) & byteMask)),
	}
}

// readSpan reads the time window, and for a session the sport, of one message.
func readSpan(fields fitFields, withSport bool) FITSpan {
	span := FITSpan{}
	if start, ok := fields.timestamp(fieldStartTime); ok {
		span.Start = start
	}
	if end, ok := fields.timestamp(fieldTimestamp); ok {
		span.End = end
	}
	if code, ok := fields.unsigned(fieldSport); withSport && ok {
		span.Sport = sportName(code)
	}
	return span
}

// sportName maps the sport codes this server is sure of. An unmapped code is
// reported by number rather than guessed at, because a wrong sport label would be
// read as fact.
func sportName(code uint64) string {
	switch code {
	case 0:
		return "generic"
	case 1:
		return "running"
	case 2:
		return "cycling"
	case 5:
		return "swimming"
	case 11:
		return "walking"
	case 17:
		return "hiking"
	}
	return "sport_" + strconv.FormatUint(code, 10)
}

// readRecord maps one record message onto the sample model.
func readRecord(stamp time.Time, fields fitFields) FITRecord {
	record := FITRecord{
		Time:        stamp,
		HeartRate:   scaled(fields, fieldRecordHeartRate, 1, 0),
		Cadence:     scaled(fields, fieldRecordCadence, 1, 0),
		Power:       scaled(fields, fieldRecordPower, 1, 0),
		Distance:    scaled(fields, fieldRecordDistance, scaleDistance, 0),
		Grade:       scaled(fields, fieldRecordGrade, scaleGrade, 0),
		Temperature: scaled(fields, fieldRecordTemperature, 1, 0),
	}
	record.Speed = firstOf(
		scaled(fields, fieldRecordEnhSpeed, scaleSpeed, 0),
		scaled(fields, fieldRecordSpeed, scaleSpeed, 0))
	record.Altitude = firstOf(
		scaled(fields, fieldRecordEnhAltitude, scaleAltitude, offsetAltitude),
		scaled(fields, fieldRecordAltitude, scaleAltitude, offsetAltitude))
	return readDynamics(record, fields)
}

// readDynamics adds the cycling-dynamics readings, which only a compatible power
// meter records.
func readDynamics(record FITRecord, fields fitFields) FITRecord {
	record.LeftTorque = scaled(fields, fieldRecordLeftTE, scalePedalMetric, 0)
	record.RightTorque = scaled(fields, fieldRecordRightTE, scalePedalMetric, 0)
	record.LeftSmooth = scaled(fields, fieldRecordLeftPS, scalePedalMetric, 0)
	record.RightSmooth = scaled(fields, fieldRecordRightPS, scalePedalMetric, 0)
	record.LeftPCO = scaled(fields, fieldRecordLeftPCO, 1, 0)
	record.RightPCO = scaled(fields, fieldRecordRightPCO, 1, 0)
	record.RightBalance = readBalance(fields)
	return record
}

// readBalance reports the right-side share of the power split. The high bit says
// which pedal the stored percentage describes, so the left-side form is converted.
func readBalance(fields fitFields) FITNumber {
	packed, ok := fields.unsigned(fieldRecordBalance)
	if !ok {
		return FITNumber{}
	}
	share := float64(packed & balanceMask)
	if packed&balanceRightFlag != 0 {
		return fitNumber(share)
	}
	return fitNumber(balanceWhole - share)
}

// scaled reads one numeric field and applies the format's scale and offset.
func scaled(fields fitFields, num uint8, scale, offset float64) FITNumber {
	raw, ok := fields.number(num)
	if !ok {
		return FITNumber{}
	}
	return fitNumber(raw/scale - offset)
}

// firstOf returns the first present reading, which is how an enhanced field takes
// precedence over the legacy one it replaced.
func firstOf(values ...FITNumber) FITNumber {
	for _, value := range values {
		if value.OK {
			return value
		}
	}
	return FITNumber{}
}
