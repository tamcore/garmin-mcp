package api

import (
	"cmp"
	"slices"
)

// recoveryReachedZero is the recoveryTimeChangePhrase Garmin sends once the
// recovery clock has drained. Source: health_wellness.py:29, :46.
const recoveryReachedZero = "REACHED_ZERO"

// RemainingRecoveryMinutes reports the recovery time still to run. Garmin keeps the
// last assigned recoveryTime after the clock drains, so REACHED_ZERO reports zero.
func (r Readiness) RemainingRecoveryMinutes() (float64, bool) {
	if phrase, ok := r.RecoveryTimeChangePhrase.Value(); ok && phrase == recoveryReachedZero {
		return 0, true
	}
	return r.RecoveryTime.Float64()
}

// LatestReadiness selects the newest snapshot by timestampLocal, else timestamp.
// Source: health_wellness.py:58-61.
func LatestReadiness(entries []Readiness) (Readiness, bool) {
	if len(entries) == 0 {
		return Readiness{}, false
	}
	return slices.MaxFunc(entries, func(a, b Readiness) int {
		return cmp.Compare(a.stamp(), b.stamp())
	}), true
}

// stamp is the snapshot's ordering key.
func (r Readiness) stamp() string {
	value, _ := cmp.Or(r.TimestampLocal, r.Timestamp).Value()
	return value
}
