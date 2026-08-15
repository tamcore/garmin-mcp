package testkit

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"time"
)

// This file builds synthetic FIT activity files for tests.
//
// Nothing here is a recording. Every byte is generated from the values a test
// declares, so a fixture can never carry a credential, a coordinate or a real
// person's health data. The layout is the public FIT container, which
// FITContainer renders: header, a stream of definition and data records, and the
// checksum a conforming reader verifies.

// The local message slots and global message numbers the builder emits.
const (
	fitDefinitionBit = 0x40

	fitLocalRecord  = 0
	fitLocalSession = 1
	fitLocalLap     = 2
	fitLocalEvent   = 3

	fitGlobalSession = 18
	fitGlobalLap     = 19
	fitGlobalRecord  = 20
	fitGlobalEvent   = 21

	fitFrontShift = 42
	fitRearShift  = 43
)

// FIT base type numbers the builder emits.
const (
	fitEnum   = 0x00
	fitSint8  = 0x01
	fitUint8  = 0x02
	fitSint16 = 0x83
	fitUint16 = 0x84
	fitSint32 = 0x85
	fitUint32 = 0x86
)

// The scales the FIT profile gives the summary fields the builder writes.
const (
	scaleDistance = 100.0
	scaleSeconds  = 1000.0
)

// fitFixtureEpoch is the FIT date_time epoch.
var fitFixtureEpoch = time.Date(1989, time.December, 31, 0, 0, 0, 0, time.UTC)

// fitFixtureStart is the synthetic instant a file starts at when a test names none.
var fitFixtureStart = time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC)

// A FITSample is one synthetic record message. A nil field is written as the base
// type's invalid sentinel, which is how a test declares a missing sensor.
type FITSample struct {
	Second int
	// Latitude and Longitude are synthetic degrees. They exist so a test can prove
	// that a file carrying a position decodes to a model that carries none; nothing
	// here is a recorded location.
	Latitude    *float64
	Longitude   *float64
	Power       *int
	HeartRate   *int
	Cadence     *int
	Altitude    *float64
	Distance    *float64
	Grade       *float64
	Temperature *int
	Balance     *int
	TorqueEff   *float64
}

// A FITShiftFixture is one synthetic electronic gear change.
type FITShiftFixture struct {
	Second    int
	Front     bool
	FrontGear int
	RearGear  int
}

// A FITLapFixture is one synthetic lap window.
type FITLapFixture struct {
	StartSecond int
	EndSecond   int
}

// A FITSummaryFixture is the profile-carried summary of a session or lap: the
// figures a device computes itself and writes into the summary message, rather
// than the ones a reader would have to derive from the record stream. A nil field
// is written as its invalid sentinel.
type FITSummaryFixture struct {
	ElapsedSeconds  *float64
	DistanceMeters  *float64
	AscentMeters    *int
	Calories        *int
	AvgHeartRate    *int
	MaxHeartRate    *int
	AvgCadence      *int
	AvgPower        *int
	MaxPower        *int
	NormalizedPower *int
}

// A FITFile is a synthetic activity file to build.
type FITFile struct {
	// Start is the instant the first record carries.
	Start time.Time
	// Sport is the FIT sport code of the session, for example 2 for cycling.
	Sport int
	// Session reports whether a session message is written.
	Session bool
	// Summary is the session's profile-carried summary, when a test declares one.
	Summary *FITSummaryFixture
	// LapSummary is written into every lap message, when a test declares one.
	LapSummary *FITSummaryFixture
	// Samples are the record messages, in order.
	Samples []FITSample
	// Shifts are the gear-change event messages.
	Shifts []FITShiftFixture
	// Laps are the lap messages.
	Laps []FITLapFixture
}

// Bytes renders the file.
func (f FITFile) Bytes() []byte { return FITContainer(f.records()) }

// records renders every definition and data record of the file.
func (f FITFile) records() []byte {
	var out []byte
	out = append(out, recordDefinition()...)
	for _, sample := range f.Samples {
		out = append(out, f.recordData(sample)...)
	}
	if f.Session {
		out = append(out, f.sessionDefinition()...)
		out = append(out, f.sessionData()...)
	}
	if len(f.Laps) > 0 {
		out = append(out, f.lapDefinition()...)
		for _, lap := range f.Laps {
			out = append(out, f.lapData(lap)...)
		}
	}
	if len(f.Shifts) > 0 {
		out = append(out, eventDefinition()...)
		for _, shift := range f.Shifts {
			out = append(out, f.shiftData(shift)...)
		}
	}
	return out
}

// stamp renders one offset as a FIT date_time.
func (f FITFile) stamp(second int) uint32 {
	base := f.Start
	if base.IsZero() {
		base = fitFixtureStart
	}
	return uint32(base.Add(time.Duration(second) * time.Second).Sub(fitFixtureEpoch).Seconds())
}

// definition renders one definition message.
func definition(local byte, global uint16, fields [][3]byte) []byte {
	out := []byte{fitDefinitionBit | local, 0, 0}
	out = binary.LittleEndian.AppendUint16(out, global)
	out = append(out, byte(len(fields)))
	for _, field := range fields {
		out = append(out, field[0], field[1], field[2])
	}
	return out
}

// recordDefinition declares the record layout the builder writes.
func recordDefinition() []byte {
	return definition(fitLocalRecord, fitGlobalRecord, [][3]byte{
		{253, 4, fitUint32},
		{0, 4, fitSint32},
		{1, 4, fitSint32},
		{7, 2, fitUint16},
		{3, 1, fitUint8},
		{4, 1, fitUint8},
		{2, 2, fitUint16},
		{5, 4, fitUint32},
		{9, 2, fitSint16},
		{13, 1, fitSint8},
		{30, 1, fitUint8},
		{43, 1, fitUint8},
	})
}

// recordData renders one record message.
func (f FITFile) recordData(sample FITSample) []byte {
	out := []byte{fitLocalRecord}
	out = binary.LittleEndian.AppendUint32(out, f.stamp(sample.Second))
	out = appendSemicircles(out, sample.Latitude)
	out = appendSemicircles(out, sample.Longitude)
	out = appendUint16(out, sample.Power)
	out = appendUint8(out, sample.HeartRate)
	out = appendUint8(out, sample.Cadence)
	out = appendScaledUint16(out, sample.Altitude, 5, 500)
	out = appendScaledUint32(out, sample.Distance, 100)
	out = appendScaledSint16(out, sample.Grade, 100)
	out = appendSint8(out, sample.Temperature)
	out = appendUint8(out, sample.Balance)
	out = appendScaledUint8(out, sample.TorqueEff, 2)
	return out
}

// summaryFields are the session summary field definitions, in the order
// summaryData writes them. Source: the FIT profile's session message.
var summaryFields = [][3]byte{
	{7, 4, fitUint32},  // total_elapsed_time, scale 1000
	{9, 4, fitUint32},  // total_distance, scale 100
	{22, 2, fitUint16}, // total_ascent
	{11, 2, fitUint16}, // total_calories
	{20, 2, fitUint16}, // avg_power
	{21, 2, fitUint16}, // max_power
	{34, 2, fitUint16}, // normalized_power
	{16, 1, fitUint8},  // avg_heart_rate
	{17, 1, fitUint8},  // max_heart_rate
	{18, 1, fitUint8},  // avg_cadence
}

// lapSummaryFields are the same quantities on the lap message, which numbers them
// differently. Source: the FIT profile's lap message.
var lapSummaryFields = [][3]byte{
	{7, 4, fitUint32},  // total_elapsed_time, scale 1000
	{9, 4, fitUint32},  // total_distance, scale 100
	{21, 2, fitUint16}, // total_ascent
	{11, 2, fitUint16}, // total_calories
	{19, 2, fitUint16}, // avg_power
	{20, 2, fitUint16}, // max_power
	{33, 2, fitUint16}, // normalized_power
	{15, 1, fitUint8},  // avg_heart_rate
	{16, 1, fitUint8},  // max_heart_rate
	{17, 1, fitUint8},  // avg_cadence
}

// summaryData writes the summary values in the order the definitions above declare.
func summaryData(out []byte, summary *FITSummaryFixture) []byte {
	if summary == nil {
		return out
	}
	out = appendScaledUint32(out, summary.ElapsedSeconds, scaleSeconds)
	out = appendScaledUint32(out, summary.DistanceMeters, scaleDistance)
	out = appendUint16(out, summary.AscentMeters)
	out = appendUint16(out, summary.Calories)
	out = appendUint16(out, summary.AvgPower)
	out = appendUint16(out, summary.MaxPower)
	out = appendUint16(out, summary.NormalizedPower)
	out = appendUint8(out, summary.AvgHeartRate)
	out = appendUint8(out, summary.MaxHeartRate)
	return appendUint8(out, summary.AvgCadence)
}

// withSummary appends the summary field definitions when a summary is declared.
func withSummary(base [][3]byte, summary *FITSummaryFixture, extra [][3]byte) [][3]byte {
	if summary == nil {
		return base
	}
	return append(base, extra...)
}

// sessionDefinition declares the session layout.
func (f FITFile) sessionDefinition() []byte {
	fields := withSummary([][3]byte{
		{253, 4, fitUint32},
		{2, 4, fitUint32},
		{5, 1, fitEnum},
	}, f.Summary, summaryFields)
	return definition(fitLocalSession, fitGlobalSession, fields)
}

// sessionData renders the session message spanning every sample.
func (f FITFile) sessionData() []byte {
	out := []byte{fitLocalSession}
	out = binary.LittleEndian.AppendUint32(out, f.stamp(f.lastSecond()))
	out = binary.LittleEndian.AppendUint32(out, f.stamp(f.firstSecond()))
	out = append(out, byte(f.Sport))
	return summaryData(out, f.Summary)
}

// lapDefinition declares the lap layout.
func (f FITFile) lapDefinition() []byte {
	fields := withSummary([][3]byte{
		{253, 4, fitUint32},
		{2, 4, fitUint32},
	}, f.LapSummary, lapSummaryFields)
	return definition(fitLocalLap, fitGlobalLap, fields)
}

// lapData renders one lap message.
func (f FITFile) lapData(lap FITLapFixture) []byte {
	out := []byte{fitLocalLap}
	out = binary.LittleEndian.AppendUint32(out, f.stamp(lap.EndSecond))
	out = binary.LittleEndian.AppendUint32(out, f.stamp(lap.StartSecond))
	return summaryData(out, f.LapSummary)
}

// eventDefinition declares the event layout.
func eventDefinition() []byte {
	return definition(fitLocalEvent, fitGlobalEvent, [][3]byte{
		{253, 4, fitUint32},
		{0, 1, fitEnum},
		{1, 1, fitEnum},
		{3, 4, fitUint32},
	})
}

// shiftData renders one gear-change event message. The gear_change_data payload
// packs the rear gear number and gear into the low two bytes and the front gear
// number and gear into the high two.
func (f FITFile) shiftData(shift FITShiftFixture) []byte {
	kind := byte(fitRearShift)
	if shift.Front {
		kind = fitFrontShift
	}
	packed := uint32(shift.RearGear)<<8 | uint32(shift.FrontGear)<<24

	out := []byte{fitLocalEvent}
	out = binary.LittleEndian.AppendUint32(out, f.stamp(shift.Second))
	out = append(out, kind, 3)
	return binary.LittleEndian.AppendUint32(out, packed)
}

// firstSecond is the offset the session starts at.
func (f FITFile) firstSecond() int {
	if len(f.Samples) == 0 {
		return 0
	}
	return f.Samples[0].Second
}

// lastSecond is the offset the session ends at.
func (f FITFile) lastSecond() int {
	if len(f.Samples) == 0 {
		return 0
	}
	return f.Samples[len(f.Samples)-1].Second
}

// ZipFIT wraps a FIT file in the zip archive Garmin serves the original format as.
func ZipFIT(name string, data []byte) []byte {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create(name)
	if err != nil {
		panic("testkit: building the FIT archive: " + err.Error())
	}
	if _, err := entry.Write(data); err != nil {
		panic("testkit: writing the FIT archive entry: " + err.Error())
	}
	if err := archive.Close(); err != nil {
		panic("testkit: closing the FIT archive: " + err.Error())
	}
	return buffer.Bytes()
}
