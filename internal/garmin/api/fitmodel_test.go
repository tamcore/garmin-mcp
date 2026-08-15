package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The steady readings the synthetic ride is built from.
const (
	ridePower       = 200
	rideHeartRate   = 140
	rideCadence     = 90
	rideGrade       = 10.0
	rideTemperature = 20
	rideBalance     = 0x80 | 52 // the right-side form of a 52 percent share
	rideTorqueEff   = 75.0
	rideMetersPerS  = 10.0
	rideClimbPerS   = 1.0
	rideBaseAlt     = 100.0
)

// The reading names the scale tests index their expectations by.
const (
	nameHeartRate   = "heart rate"
	namePower       = "power"
	nameCadence     = "cadence"
	nameDistance    = "distance"
	nameGrade       = "grade"
	nameTemperature = "temperature"
	nameAltitude    = "altitude"
	nameBalance     = "balance"
	nameTorque      = "torque"
)

// rideSamples builds a steady climbing ride: constant power, cadence and heart rate,
// one meter of ascent and ten meters of distance per second.
func rideSamples(count int) []testkit.FITSample {
	out := make([]testkit.FITSample, 0, count)
	for second := range count {
		out = append(out, testkit.FITSample{
			Second:      second,
			Power:       new(ridePower),
			HeartRate:   new(rideHeartRate),
			Cadence:     new(rideCadence),
			Altitude:    new(rideBaseAlt + rideClimbPerS*float64(second)),
			Distance:    new(rideMetersPerS * float64(second)),
			Grade:       new(rideGrade),
			Temperature: new(rideTemperature),
			Balance:     new(rideBalance),
			TorqueEff:   new(rideTorqueEff),
		})
	}
	return out
}

// rideFile is the synthetic cycling file the analysis tests decode.
func rideFile(seconds int) testkit.FITFile {
	return testkit.FITFile{Sport: 2, Session: true, Samples: rideSamples(seconds)}
}

// TestParseFITReadsEveryScaledReading pins the scale and offset of each record field
// this server decodes. A wrong scale would be a plausible-looking wrong measurement,
// which is worse than no measurement at all.
func TestParseFITReadsEveryScaledReading(t *testing.T) {
	t.Parallel()

	activity, err := api.ParseFITActivity(rideFile(10).Bytes(), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Records) != 10 {
		t.Fatalf("%d records, want 10", len(activity.Records))
	}

	record := activity.Records[5]
	cases := map[string]struct {
		got  api.FITNumber
		want float64
	}{
		namePower:       {record.Power, ridePower},
		nameHeartRate:   {record.HeartRate, rideHeartRate},
		nameCadence:     {record.Cadence, rideCadence},
		nameAltitude:    {record.Altitude, rideBaseAlt + 5},
		nameDistance:    {record.Distance, rideMetersPerS * 5},
		nameGrade:       {record.Grade, rideGrade},
		nameTemperature: {record.Temperature, rideTemperature},
		nameBalance:     {record.RightBalance, 52},
		nameTorque:      {record.LeftTorque, rideTorqueEff},
	}
	for name, want := range cases {
		if !want.got.OK || want.got.Value != want.want {
			t.Errorf("%s = %+v, want %v", name, want.got, want.want)
		}
	}
}

// TestParseFITReportsAMissingSensorAsAbsent proves an invalid sentinel becomes an
// absent reading, never a zero one. A zero heart rate would be read as a fact.
func TestParseFITReportsAMissingSensorAsAbsent(t *testing.T) {
	t.Parallel()

	file := testkit.FITFile{Samples: []testkit.FITSample{{Second: 0}}}
	activity, err := api.ParseFITActivity(file.Bytes(), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Records) != 1 {
		t.Fatalf("%d records, want 1", len(activity.Records))
	}

	record := activity.Records[0]
	for name, value := range map[string]api.FITNumber{
		namePower:       record.Power,
		nameHeartRate:   record.HeartRate,
		nameCadence:     record.Cadence,
		nameAltitude:    record.Altitude,
		nameDistance:    record.Distance,
		nameGrade:       record.Grade,
		nameTemperature: record.Temperature,
		nameBalance:     record.RightBalance,
		nameTorque:      record.LeftTorque,
	} {
		if value.OK {
			t.Errorf("%s = %+v, want an absent reading", name, value)
		}
	}
}

// TestParseFITReadsTheSessionSportAndLaps proves the session and lap windows survive
// the decode, since every computed segment is cut from them.
func TestParseFITReadsTheSessionSportAndLaps(t *testing.T) {
	t.Parallel()

	file := rideFile(60)
	file.Laps = []testkit.FITLapFixture{
		{StartSecond: 0, EndSecond: 29},
		{StartSecond: 30, EndSecond: 59},
	}

	activity, err := api.ParseFITActivity(file.Bytes(), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Sessions) != 1 || activity.Sessions[0].Sport != "cycling" {
		t.Fatalf("sessions = %+v, want one cycling session", activity.Sessions)
	}
	if len(activity.Laps) != 2 {
		t.Fatalf("%d laps, want 2", len(activity.Laps))
	}
	if got := activity.Laps[1].Start.Sub(activity.Laps[0].Start).Seconds(); got != 30 {
		t.Errorf("the second lap starts %v seconds later, want 30", got)
	}
}

// TestParseFITReportsAnUnmappedSportByNumber keeps a sport code this server is not
// sure of from being labelled with a guess.
func TestParseFITReportsAnUnmappedSportByNumber(t *testing.T) {
	t.Parallel()

	file := rideFile(5)
	file.Sport = 96

	activity, err := api.ParseFITActivity(file.Bytes(), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Sessions) != 1 || activity.Sessions[0].Sport != "sport_96" {
		t.Errorf("sport = %+v, want the unmapped code reported by number", activity.Sessions)
	}
}

// TestParseFITDecodesGearChanges pins the gear_change_data unpacking, which is the
// only reason the shift analysis can name a gear at all.
func TestParseFITDecodesGearChanges(t *testing.T) {
	t.Parallel()

	file := rideFile(60)
	file.Shifts = []testkit.FITShiftFixture{
		{Second: 10, Front: false, FrontGear: 2, RearGear: 7},
		{Second: 20, Front: true, FrontGear: 1, RearGear: 7},
	}

	activity, err := api.ParseFITActivity(file.Bytes(), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Shifts) != 2 {
		t.Fatalf("%d shifts, want 2", len(activity.Shifts))
	}
	if got := activity.Shifts[0]; got.Front || got.RearGear.Value != 7 || got.FrontGear.Value != 2 {
		t.Errorf("first shift = %+v, want a rear change into 2x7", got)
	}
	if !activity.Shifts[1].Front {
		t.Errorf("second shift = %+v, want a front change", activity.Shifts[1])
	}
}

// TestParseFITNeverDecodesCoordinates is the position suppression test. The file
// carries a synthetic track in every record, and neither the semicircle value nor
// the degrees it renders as may appear anywhere in the decoded model.
func TestParseFITNeverDecodesCoordinates(t *testing.T) {
	t.Parallel()

	const (
		latitude  = 48.137154
		longitude = 11.576124
	)
	samples := rideSamples(10)
	for index := range samples {
		samples[index].Latitude = new(latitude)
		samples[index].Longitude = new(longitude)
	}

	activity, err := api.ParseFITActivity(
		testkit.FITFile{Sport: 2, Session: true, Samples: samples}.Bytes(), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Records) != 10 {
		t.Fatalf("%d records, want 10", len(activity.Records))
	}

	rendered := fmt.Sprintf("%+v", activity)
	encoded, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}
	for _, needle := range []string{"48.13", "11.57", "574653", "138126", "Lat", "Long", "osition"} {
		if strings.Contains(rendered, needle) || strings.Contains(string(encoded), needle) {
			t.Errorf("the decoded activity carries %q, want no position of any kind", needle)
		}
	}
}

// TestParseFITUnpacksTheArchiveGarminServes proves the zip form Garmin serves the
// original format as is unpacked before it is decoded.
func TestParseFITUnpacksTheArchiveGarminServes(t *testing.T) {
	t.Parallel()

	archived := testkit.ZipFIT("18446744_ACTIVITY.fit", rideFile(10).Bytes())
	activity, err := api.ParseFITActivity(archived, api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() on the archive = %v", err)
	}
	if len(activity.Records) != 10 {
		t.Errorf("%d records from the archive, want 10", len(activity.Records))
	}
}

// TestParseFITRefusesAnArchiveWithoutAFITEntry keeps an archive of something else
// from being decoded as an activity.
func TestParseFITRefusesAnArchiveWithoutAFITEntry(t *testing.T) {
	t.Parallel()

	archived := testkit.ZipFIT("readme.txt", []byte("not an activity"))
	_, err := api.ParseFITActivity(archived, api.FITLimits{})
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("ParseFITActivity() = %v, want ErrMalformedPayload", err)
	}
}

// TestParseFITRefusesAnArchiveThatExpandsPastTheBound is the compression-bomb test
// of this layer: the bound applies to the expanded file, not to the archive.
func TestParseFITRefusesAnArchiveThatExpandsPastTheBound(t *testing.T) {
	t.Parallel()

	archived := testkit.ZipFIT("big.fit", rideFile(600).Bytes())
	if len(archived) >= 8192 {
		t.Fatalf("the compressed fixture is %d bytes, want it well under the bound", len(archived))
	}

	_, err := api.ParseFITActivity(archived, api.FITLimits{MaxBytes: 8192})
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Errorf("ParseFITActivity() = %v, want ErrResponseTooLarge", err)
	}
}
