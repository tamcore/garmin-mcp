package api_test

import (
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// retryLimits allow three attempts, so a request the predicate permits to repeat
// reaches the fake three times and one it refuses reaches it once.
func retryLimits() client.Limits {
	return client.Limits{MaxAttempts: 3}
}

// serverErrors scripts a 503, which is the one failure class the request layer is
// allowed to retry. Whether it actually does is decided by the declared Effect.
func serverErrors(path string) testkit.Script {
	return testkit.NewScript().With(path,
		testkit.JSON(http.StatusServiceUnavailable, `{"error":"synthetic"}`))
}

// TestDeclaredEffectsDecideWhetherAWriteIsRepeated is the retry-safety test.
//
// Garmin gives no guarantee that a rejected create or delete was not applied, so
// repeating one can create a second activity or hide a completed deletion. The
// declared Effect is what stops it, and the proof is the request count the fake
// recorded.
func TestDeclaredEffectsDecideWhetherAWriteIsRepeated(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		path     string
		call     func(*testing.T, harness) error
		attempts int
	}{
		"create a manual activity": {
			path: client.PathActivityPrefix,
			call: func(t *testing.T, h harness) error {
				_, err := newActivityWrites(t, h).CreateManual(t.Context(), h.session,
					api.NewManualActivity("running", "2026-01-31T09:00:00.000", "UTC", "", 0, 30))
				return err
			},
			attempts: 1,
		},
		"delete an activity": {
			path: activityWritePath(),
			call: func(t *testing.T, h harness) error {
				_, err := newActivityWrites(t, h).Delete(t.Context(), h.session, mustID(t))
				return err
			},
			attempts: 1,
		},
		"upload a workout": {
			path: client.PathWorkoutPrefix,
			call: func(t *testing.T, h harness) error {
				_, err := newWorkouts(t, h).Upload(t.Context(), h.session, mustWorkoutDocument(t))
				return err
			},
			attempts: 1,
		},
		"schedule a workout": {
			path: client.PathWorkoutSchedule + "/18446744",
			call: func(t *testing.T, h harness) error {
				_, err := newWorkouts(t, h).Schedule(t.Context(), h.session, mustID(t),
					mustDate(t, testCalendarDate))
				return err
			},
			attempts: 1,
		},
		"delete a workout": {
			path: client.PathWorkoutPrefix + "/18446744",
			call: func(t *testing.T, h harness) error {
				_, err := newWorkouts(t, h).Delete(t.Context(), h.session, mustID(t))
				return err
			},
			attempts: 1,
		},
		"download an activity file": {
			path: client.PathActivityTCXDownload + "/18446744",
			call: func(t *testing.T, h harness) error {
				_, err := newActivityFiles(t, h).Download(t.Context(), h.session, mustID(t),
					api.FormatTCX, io.Discard)
				return err
			},
			attempts: 1,
		},
		"rename an activity": {
			path: activityWritePath(),
			call: func(t *testing.T, h harness) error {
				_, err := newActivityWrites(t, h).SetName(t.Context(), h.session, mustID(t), "x")
				return err
			},
			attempts: 3,
		},
		"update a workout in place": {
			path: client.PathWorkoutPrefix + "/18446744",
			call: func(t *testing.T, h harness) error {
				_, err := newWorkouts(t, h).Update(t.Context(), h.session, mustID(t),
					mustWorkoutDocument(t))
				return err
			},
			attempts: 3,
		},
		"link gear to an activity": {
			path: client.PathGearPrefix + "/link/" + testGearUUID + "/activity/18446744",
			call: func(t *testing.T, h harness) error {
				_, err := newGear(t, h).Add(t.Context(), h.session, mustGearUUID(t), mustID(t))
				return err
			},
			attempts: 3,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, serverErrors(tc.path), retryLimits())
			if err := tc.call(t, h); !errors.Is(err, client.ErrServer) {
				t.Fatalf("call = %v, want ErrServer", err)
			}
			if got := len(h.server.Requests()); got != tc.attempts {
				t.Errorf("the fake received %d requests, want %d", got, tc.attempts)
			}
		})
	}
}

// TestUnscheduleIsNeverRepeated keeps the delete of a calendar entry apart from
// the delete of the template, because they share a path prefix and a mistake
// there would repeat a removal.
func TestUnscheduleIsNeverRepeated(t *testing.T) {
	t.Parallel()

	h := newHarness(t, serverErrors(client.PathWorkoutSchedule+"/18446744"), retryLimits())
	if _, err := newWorkouts(t, h).Unschedule(t.Context(), h.session, mustID(t)); !errors.Is(
		err, client.ErrServer) {
		t.Fatalf("Unschedule() = %v, want ErrServer", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1", got)
	}
}

// TestReplacingStrengthSetsRetriesTheWriteButNotTheVerification proves the two
// halves of the strength write behave as their effects say: the replace-all PUT
// is idempotent and may repeat, and the read that verifies it never runs for a
// write that failed.
func TestReplacingStrengthSetsRetriesTheWriteButNotTheVerification(t *testing.T) {
	t.Parallel()

	h := newHarness(t, serverErrors(exerciseSetsPath()), retryLimits())
	_, err := newStrengthWrites(t, h).ReplaceSets(t.Context(), h.session, mustID(t),
		[]api.StrengthSet{sampleSet(t)})
	if !errors.Is(err, client.ErrServer) {
		t.Fatalf("ReplaceSets() = %v, want ErrServer", err)
	}

	requests := h.server.Requests()
	if len(requests) != 3 {
		t.Fatalf("the fake received %d requests, want 3 attempts of the idempotent PUT",
			len(requests))
	}
	for _, request := range requests {
		if request.Method != http.MethodPut {
			t.Errorf("method = %q, want every attempt to be the PUT: the verifying read "+
				"must not run for a write that failed", request.Method)
		}
	}
}
