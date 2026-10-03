package api_test

import (
	"encoding/json"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// TestReadinessRemainingRecoveryMinutes pins the drained-clock rule: Garmin keeps
// the last assigned recoveryTime after the clock reaches zero and says so through
// recoveryTimeChangePhrase (health_wellness.py:40-52).
func TestReadinessRemainingRecoveryMinutes(t *testing.T) {
	t.Parallel()

	type result struct {
		minutes float64
		ok      bool
	}
	for body, want := range map[string]result{
		`{"recoveryTime":720,"recoveryTimeChangePhrase":"REACHED_ZERO"}`: {0, true},
		`{"recoveryTimeChangePhrase":"REACHED_ZERO"}`:                    {0, true},
		`{"recoveryTime":720,"recoveryTimeChangePhrase":"DECREASED"}`:    {720, true},
		`{"recoveryTime":90}`: {90, true},
		`{}`:                  {},
	} {
		var readiness api.Readiness
		if err := json.Unmarshal([]byte(body), &readiness); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if got, ok := readiness.RemainingRecoveryMinutes(); got != want.minutes || ok != want.ok {
			t.Errorf("%s: RemainingRecoveryMinutes() = %v/%v, want %v/%v",
				body, got, ok, want.minutes, want.ok)
		}
	}
}
