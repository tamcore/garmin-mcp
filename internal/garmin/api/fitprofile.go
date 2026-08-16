package api

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
)

// This file maps the FIT profile's typed messages onto this server's model.
//
// Every field number, scale and offset below comes from the profile the SDK ships
// as generated Go, not from a hand-read specification. That is the whole point of
// the dependency: the session and lap messages number the same quantity
// differently — average heart rate is field 16 on a session and field 15 on a lap —
// and no amount of care makes that safe to maintain by hand.

// unmappedSportPrefix is what the SDK's Sport.String returns for a code the profile
// does not name. Such a code is reported by number rather than under a label the
// reader would take as fact.
const unmappedSportPrefix = "SportInvalid("

// readRecord maps one record message onto the sample model.
//
// Coordinates are absent from the result by construction: the SDK decodes
// PositionLat and PositionLong into its own message struct, and this function never
// reads either of them, so no returned structure can carry a track.
func readRecord(m *mesgdef.Record) FITRecord {
	record := FITRecord{
		Time:        m.Timestamp,
		HeartRate:   fitUint8(m.HeartRate),
		Cadence:     fitUint8(m.Cadence),
		Power:       fitUint16(m.Power),
		Distance:    fitScaled(m.DistanceScaled()),
		Grade:       fitScaled(m.GradeScaled()),
		Temperature: fitSint8(m.Temperature),
		Speed:       firstOf(fitScaled(m.EnhancedSpeedScaled()), fitScaled(m.SpeedScaled())),
		Altitude:    firstOf(fitScaled(m.EnhancedAltitudeScaled()), fitScaled(m.AltitudeScaled())),
	}
	return readDynamics(record, m)
}

// readDynamics adds the cycling-dynamics readings, which only a compatible power
// meter records.
func readDynamics(record FITRecord, m *mesgdef.Record) FITRecord {
	record.LeftTorque = fitScaled(m.LeftTorqueEffectivenessScaled())
	record.RightTorque = fitScaled(m.RightTorqueEffectivenessScaled())
	record.LeftSmooth = fitScaled(m.LeftPedalSmoothnessScaled())
	record.RightSmooth = fitScaled(m.RightPedalSmoothnessScaled())
	record.LeftPCO = fitSint8(m.LeftPco)
	record.RightPCO = fitSint8(m.RightPco)
	record.RightBalance = readBalance(m.LeftRightBalance)
	return record
}

// balanceWhole is the full share a left-right split adds up to.
const balanceWhole = 100.0

// readBalance reports the right-side share of the power split. The high bit says
// which pedal the stored percentage describes, so the left-side form is converted.
func readBalance(packed typedef.LeftRightBalance) FITNumber {
	if packed == typedef.LeftRightBalanceInvalid {
		return FITNumber{}
	}
	share := float64(packed & typedef.LeftRightBalanceMask)
	if packed&typedef.LeftRightBalanceRight != 0 {
		return fitNumber(share)
	}
	return fitNumber(balanceWhole - share)
}

// spanEnd is the instant a session or lap window closes.
//
// The profile says a summary message's timestamp is the end of the segment, and on
// real Garmin running files it is not: the device writes the same instant into
// timestamp and into start_time, which collapses the window to a single point and
// leaves every derived per-session figure reading zero. The elapsed time is the
// authority, so the window runs start_time plus total_elapsed_time, and the recorded
// timestamp is used only when it is later or when there is no elapsed time to add.
func spanEnd(start, timestamp time.Time, elapsed FITNumber) time.Time {
	if start.IsZero() || !elapsed.OK || elapsed.Value <= 0 {
		return timestamp
	}
	end := start.Add(time.Duration(elapsed.Value * float64(time.Second)))
	if timestamp.After(end) {
		return timestamp
	}
	return end
}

// readSession maps one session message onto a span, summary figures included.
func readSession(m *mesgdef.Session) FITSpan {
	elapsed := fitScaled(m.TotalElapsedTimeScaled())
	return FITSpan{
		Start:        m.StartTime,
		End:          spanEnd(m.StartTime, m.Timestamp, elapsed),
		Sport:        sportName(m.Sport),
		Elapsed:      elapsed,
		Distance:     fitScaled(m.TotalDistanceScaled()),
		Ascent:       fitUint16(m.TotalAscent),
		Descent:      fitUint16(m.TotalDescent),
		Calories:     fitUint16(m.TotalCalories),
		AvgHeartRate: fitUint8(m.AvgHeartRate),
		MaxHeartRate: fitUint8(m.MaxHeartRate),
		AvgCadence:   fitUint8(m.AvgCadence),
		MaxCadence:   fitUint8(m.MaxCadence),
		AvgPower:     fitUint16(m.AvgPower),
		MaxPower:     fitUint16(m.MaxPower),
		NormalizedPw: fitUint16(m.NormalizedPower),
	}
}

// readLap maps one lap message onto a span. The quantities are the session's, and
// the field numbers are not.
func readLap(m *mesgdef.Lap) FITSpan {
	elapsed := fitScaled(m.TotalElapsedTimeScaled())
	return FITSpan{
		Start:        m.StartTime,
		End:          spanEnd(m.StartTime, m.Timestamp, elapsed),
		Sport:        sportName(m.Sport),
		Elapsed:      elapsed,
		Distance:     fitScaled(m.TotalDistanceScaled()),
		Ascent:       fitUint16(m.TotalAscent),
		Descent:      fitUint16(m.TotalDescent),
		Calories:     fitUint16(m.TotalCalories),
		AvgHeartRate: fitUint8(m.AvgHeartRate),
		MaxHeartRate: fitUint8(m.MaxHeartRate),
		AvgCadence:   fitUint8(m.AvgCadence),
		MaxCadence:   fitUint8(m.MaxCadence),
		AvgPower:     fitUint16(m.AvgPower),
		MaxPower:     fitUint16(m.MaxPower),
		NormalizedPw: fitUint16(m.NormalizedPower),
	}
}

// readShift maps one gear-change event onto a shift, when it is one.
//
// The gear numbers come from the decoder's component expansion of the
// gear_change_data payload, which is the profile's own unpacking of that bit field.
func readShift(m *mesgdef.Event) (FITShift, bool) {
	front := m.Event == typedef.EventFrontGearChange
	if !front && m.Event != typedef.EventRearGearChange {
		return FITShift{}, false
	}
	if m.Timestamp.IsZero() {
		return FITShift{}, false
	}
	return FITShift{
		Time:      m.Timestamp,
		Front:     front,
		FrontGear: fitUint8z(m.FrontGear),
		RearGear:  fitUint8z(m.RearGear),
	}, true
}

// sportName names a sport code, and reports an unnamed one by number.
func sportName(sport typedef.Sport) string {
	if sport == typedef.SportInvalid {
		return ""
	}
	name := sport.String()
	if strings.HasPrefix(name, unmappedSportPrefix) {
		return "sport_" + strconv.Itoa(int(sport))
	}
	return name
}

// fitUint8 reads a byte reading unless it is the base type's invalid sentinel.
func fitUint8(value uint8) FITNumber {
	if value == basetype.Uint8Invalid {
		return FITNumber{}
	}
	return fitNumber(float64(value))
}

// fitUint8z reads a byte reading whose invalid sentinel is zero.
func fitUint8z(value uint8) FITNumber {
	if value == basetype.Uint8zInvalid {
		return FITNumber{}
	}
	return fitNumber(float64(value))
}

// fitUint16 reads a two-byte reading unless it is the invalid sentinel.
func fitUint16(value uint16) FITNumber {
	if value == basetype.Uint16Invalid {
		return FITNumber{}
	}
	return fitNumber(float64(value))
}

// fitSint8 reads a signed byte reading unless it is the invalid sentinel.
func fitSint8(value int8) FITNumber {
	if value == basetype.Sint8Invalid {
		return FITNumber{}
	}
	return fitNumber(float64(value))
}

// fitScaled reads one of the SDK's scaled accessors, which report an absent reading
// as the float64 invalid pattern rather than as a number.
func fitScaled(value float64) FITNumber {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return FITNumber{}
	}
	return fitNumber(value)
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
