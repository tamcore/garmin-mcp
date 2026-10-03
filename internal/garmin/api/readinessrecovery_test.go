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

// TestLatestReadinessPicksTheNewestSnapshot pins the ordering key: timestampLocal,
// else timestamp (health_wellness.py:58-61).
func TestLatestReadinessPicksTheNewestSnapshot(t *testing.T) {
	t.Parallel()

	var entries []api.Readiness
	body := `[{"timestampLocal":"2026-01-31T07:00:00.0","recoveryTime":1},` +
		`{"timestamp":"2026-01-31T09:00:00.0","recoveryTime":2},` +
		`{"timestampLocal":"2026-01-31T08:00:00.0","recoveryTime":3}]`
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	latest, ok := api.LatestReadiness(entries)
	if minutes, _ := latest.RecoveryTime.Float64(); !ok || minutes != 2 {
		t.Errorf("LatestReadiness() = %v/%v, want the 09:00 snapshot", minutes, ok)
	}
	if _, ok := api.LatestReadiness(nil); ok {
		t.Error("LatestReadiness(nil) reported a snapshot")
	}
}
