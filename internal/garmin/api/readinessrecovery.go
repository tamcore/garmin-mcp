package api

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
