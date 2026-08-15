package testkit_test

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// fullSample is a sample with every reading present.
func fullSample(second int) testkit.FITSample {
	return testkit.FITSample{
		Second:      second,
		Power:       new(210),
		HeartRate:   new(150),
		Cadence:     new(85),
		Altitude:    new(120.4),
		Distance:    new(1000.5),
		Grade:       new(-4.25),
		Temperature: new(-3),
		Balance:     new(0x80 | 49),
		TorqueEff:   new(72.5),
	}
}

// TestFITFileCarriesTheContainerHeader pins the header the builder writes: a reader
// that trusts the signature and the announced size must find both.
func TestFITFileCarriesTheContainerHeader(t *testing.T) {
	t.Parallel()

	file := testkit.FITFile{
		Sport:   2,
		Session: true,
		Samples: []testkit.FITSample{fullSample(0), fullSample(1)},
		Laps:    []testkit.FITLapFixture{{StartSecond: 0, EndSecond: 1}},
		Shifts:  []testkit.FITShiftFixture{{Second: 1, FrontGear: 2, RearGear: 7}},
	}
	data := file.Bytes()

	if data[0] != 12 {
		t.Errorf("header size = %d, want 12", data[0])
	}
	if string(data[8:12]) != ".FIT" {
		t.Errorf("signature = %q, want .FIT", data[8:12])
	}
	announced := int(binary.LittleEndian.Uint32(data[4:]))
	if got := len(data) - 12 - 2; announced != got {
		t.Errorf("announced size = %d, want the %d record bytes", announced, got)
	}
}

// TestFITFileWritesInvalidSentinels proves a missing reading is written as the base
// type's invalid sentinel, which is what lets a decoder tell absent from zero.
func TestFITFileWritesInvalidSentinels(t *testing.T) {
	t.Parallel()

	data := testkit.FITFile{Samples: []testkit.FITSample{{Second: 0}}}.Bytes()
	// The last record is the only data message: fifteen field bytes sit between the
	// timestamp and the trailing CRC.
	fields := data[len(data)-2-15 : len(data)-2]

	want := []byte{
		0xFF, 0xFF, // power
		0xFF,       // heart rate
		0xFF,       // cadence
		0xFF, 0xFF, // altitude
		0xFF, 0xFF, 0xFF, 0xFF, // distance
		0xFF, 0x7F, // grade
		0x7F, // temperature
		0xFF, // balance
		0xFF, // torque effectiveness
	}
	if !bytes.Equal(fields, want) {
		t.Errorf("field bytes = % #x, want the invalid sentinels % #x", fields, want)
	}
}

// TestFITFileUsesTheDeclaredStart proves a test can pin the instant a file starts at.
func TestFITFileUsesTheDeclaredStart(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.March, 1, 6, 30, 0, 0, time.UTC)
	data := testkit.FITFile{Start: start, Samples: []testkit.FITSample{{Second: 0}}}.Bytes()

	epoch := time.Date(1989, time.December, 31, 0, 0, 0, 0, time.UTC)
	want := uint32(start.Sub(epoch).Seconds())
	if !bytes.Contains(data, binary.LittleEndian.AppendUint32(nil, want)) {
		t.Error("the file carries no record at the declared start")
	}
}

// TestZipFITRoundTrips proves the archive form carries the file back unchanged.
func TestZipFITRoundTrips(t *testing.T) {
	t.Parallel()

	raw := testkit.FITFile{Samples: []testkit.FITSample{fullSample(0)}}.Bytes()
	archived := testkit.ZipFIT("activity.fit", raw)

	reader, err := zip.NewReader(bytes.NewReader(archived), int64(len(archived)))
	if err != nil {
		t.Fatalf("zip.NewReader() = %v", err)
	}
	if len(reader.File) != 1 || reader.File[0].Name != "activity.fit" {
		t.Fatalf("archive holds %d entries, want one named activity.fit", len(reader.File))
	}

	entry, err := reader.File[0].Open()
	if err != nil {
		t.Fatalf("opening the entry = %v", err)
	}
	defer func() { _ = entry.Close() }()

	got, err := io.ReadAll(entry)
	if err != nil {
		t.Fatalf("reading the entry = %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Error("the archived file does not round-trip")
	}
}
