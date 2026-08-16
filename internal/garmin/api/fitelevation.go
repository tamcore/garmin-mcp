package api

// The elevation walk. Ascent and descent share it so the two can never be derived
// by disagreeing routes.

const (
	// elevationThreshold is the height a move must accumulate before it counts as
	// one. Summing every delta of a one-second series roughly doubles the device's
	// own figure, because the sensor jitters by decimeters between samples.
	elevationThreshold = 3.0
)

// The two directions elevationOf sums.
const (
	directionUp   = 1.0
	directionDown = -1.0
)

func ascentOf(records []FITRecord) FITNumber { return elevationOf(records, directionUp) }

func descentOf(records []FITRecord) FITNumber { return elevationOf(records, directionDown) }

// elevationOf sums one direction's moves, anchored on the furthest altitude seen
// against that direction so jitter cancels instead of accumulating.
//
// A stream with no altitude at all reports absence; one that carried altitude and did
// not move reports a measured zero.
func elevationOf(records []FITRecord, direction float64) FITNumber {
	var total float64
	var anchor FITNumber
	var measured bool
	for _, record := range records {
		altitude := record.Altitude
		if !altitude.OK {
			continue
		}
		measured = true
		moved := direction * (altitude.Value - anchor.Value)
		switch {
		case !anchor.OK, moved < 0:
			anchor = altitude
		case moved >= elevationThreshold:
			total += moved
			anchor = altitude
		}
	}
	if !measured {
		return FITNumber{}
	}
	return fitNumber(total)
}
