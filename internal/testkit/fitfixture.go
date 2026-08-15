package testkit

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"math"
	"time"
)

// This file builds synthetic FIT activity files for tests.
//
// Nothing here is a recording. Every byte is generated from the values a test
// declares, so a fixture can never carry a credential, a coordinate or a real
// person's health data. The layout is the public FIT container: a twelve-byte
// header carrying the ".FIT" signature, a stream of definition and data records,
// then a two-byte CRC, which a reader that does not verify it may ignore.

// FIT container and message constants the builder emits.
const (
	fitHeaderSize    = 12
	fitProtocol      = 0x20
	fitProfile       = 2140
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
	fitUint32 = 0x86
)

// The invalid sentinels of those base types.
const (
	invalidUint8  = 0xFF
	invalidSint8  = 0x7F
	invalidUint16 = 0xFFFF
	invalidSint16 = 0x7FFF
	invalidUint32 = 0xFFFFFFFF
)

// fitFixtureEpoch is the FIT date_time epoch.
var fitFixtureEpoch = time.Date(1989, time.December, 31, 0, 0, 0, 0, time.UTC)

// fitFixtureStart is the synthetic instant a file starts at when a test names none.
var fitFixtureStart = time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC)

// A FITSample is one synthetic record message. A nil field is written as the base
// type's invalid sentinel, which is how a test declares a missing sensor.
type FITSample struct {
	Second      int
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

// A FITFile is a synthetic activity file to build.
type FITFile struct {
	// Start is the instant the first record carries.
	Start time.Time
	// Sport is the FIT sport code of the session, for example 2 for cycling.
	Sport int
	// Session reports whether a session message is written.
	Session bool
	// Samples are the record messages, in order.
	Samples []FITSample
	// Shifts are the gear-change event messages.
	Shifts []FITShiftFixture
	// Laps are the lap messages.
	Laps []FITLapFixture
}

// Bytes renders the file.
func (f FITFile) Bytes() []byte {
	body := f.records()
	out := make([]byte, 0, fitHeaderSize+len(body)+2)
	out = append(out, fitHeaderSize, fitProtocol)
	out = binary.LittleEndian.AppendUint16(out, fitProfile)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	out = append(out, ".FIT"...)
	out = append(out, body...)
	return append(out, 0, 0)
}

// records renders every definition and data record of the file.
func (f FITFile) records() []byte {
	var out []byte
	out = append(out, recordDefinition()...)
	for _, sample := range f.Samples {
		out = append(out, f.recordData(sample)...)
	}
	if f.Session {
		out = append(out, sessionDefinition()...)
		out = append(out, f.sessionData()...)
	}
	if len(f.Laps) > 0 {
		out = append(out, lapDefinition()...)
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

// sessionDefinition declares the session layout.
func sessionDefinition() []byte {
	return definition(fitLocalSession, fitGlobalSession, [][3]byte{
		{253, 4, fitUint32},
		{2, 4, fitUint32},
		{5, 1, fitEnum},
	})
}

// sessionData renders the session message spanning every sample.
func (f FITFile) sessionData() []byte {
	out := []byte{fitLocalSession}
	out = binary.LittleEndian.AppendUint32(out, f.stamp(f.lastSecond()))
	out = binary.LittleEndian.AppendUint32(out, f.stamp(f.firstSecond()))
	return append(out, byte(f.Sport))
}

// lapDefinition declares the lap layout.
func lapDefinition() []byte {
	return definition(fitLocalLap, fitGlobalLap, [][3]byte{
		{253, 4, fitUint32},
		{2, 4, fitUint32},
	})
}

// lapData renders one lap message.
func (f FITFile) lapData(lap FITLapFixture) []byte {
	out := []byte{fitLocalLap}
	out = binary.LittleEndian.AppendUint32(out, f.stamp(lap.EndSecond))
	return binary.LittleEndian.AppendUint32(out, f.stamp(lap.StartSecond))
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

// shiftData renders one gear-change event message.
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

// appendUint8 writes an optional byte reading, or its invalid sentinel.
func appendUint8(out []byte, value *int) []byte {
	if value == nil {
		return append(out, invalidUint8)
	}
	return append(out, byte(*value))
}

// appendSint8 writes an optional signed byte reading.
func appendSint8(out []byte, value *int) []byte {
	if value == nil {
		return append(out, invalidSint8)
	}
	return append(out, byte(int8(*value)))
}

// appendScaledUint8 writes an optional scaled byte reading.
func appendScaledUint8(out []byte, value *float64, scale float64) []byte {
	if value == nil {
		return append(out, invalidUint8)
	}
	return append(out, byte(math.Round(*value*scale)))
}

// appendUint16 writes an optional two-byte reading.
func appendUint16(out []byte, value *int) []byte {
	if value == nil {
		return binary.LittleEndian.AppendUint16(out, invalidUint16)
	}
	return binary.LittleEndian.AppendUint16(out, uint16(*value))
}

// appendScaledUint16 writes an optional scaled and offset two-byte reading.
func appendScaledUint16(out []byte, value *float64, scale, offset float64) []byte {
	if value == nil {
		return binary.LittleEndian.AppendUint16(out, invalidUint16)
	}
	return binary.LittleEndian.AppendUint16(out, uint16(math.Round((*value+offset)*scale)))
}

// appendScaledSint16 writes an optional scaled signed two-byte reading.
func appendScaledSint16(out []byte, value *float64, scale float64) []byte {
	if value == nil {
		return binary.LittleEndian.AppendUint16(out, invalidSint16)
	}
	return binary.LittleEndian.AppendUint16(out, uint16(int16(math.Round(*value*scale))))
}

// appendScaledUint32 writes an optional scaled four-byte reading.
func appendScaledUint32(out []byte, value *float64, scale float64) []byte {
	if value == nil {
		return binary.LittleEndian.AppendUint32(out, invalidUint32)
	}
	return binary.LittleEndian.AppendUint32(out, uint32(math.Round(*value*scale)))
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
