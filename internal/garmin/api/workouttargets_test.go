package api_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// TestRangeTargetsAreOrderedTheWayGarminShowsThem pins the order Garmin Connect
// displays a range in, which is the stored order: heart rate and speed low first,
// pace fastest first (the higher m/s in targetValueOne). A reversed pair is shown
// reversed, so it is swapped before upload.
func TestRangeTargetsAreOrderedTheWayGarminShowsThem(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body string
		want string
	}{
		"reversed pace pair is swapped": {
			body: `{"workoutId":18446744073709551,"workoutSegments":[{"workoutSteps":[` +
				`{"targetType":{"workoutTargetTypeId":6,"workoutTargetTypeKey":"pace.zone"},` +
				`"targetValueOne":3.115,"targetValueTwo":3.425}]}]}`,
			want: `{"workoutId":18446744073709551,"workoutSegments":[{"workoutSteps":[` +
				`{"targetType":{"workoutTargetTypeId":6,"workoutTargetTypeKey":"pace.zone"},` +
				`"targetValueOne":3.425,"targetValueTwo":3.115}]}]}`,
		},
		"reversed speed pair is swapped": {
			body: `{"targetType":{"workoutTargetTypeKey":"speed.zone"},"targetValueOne":8.3,"targetValueTwo":6.9}`,
			want: `{"targetType":{"workoutTargetTypeKey":"speed.zone"},"targetValueOne":6.9,"targetValueTwo":8.3}`,
		},
		"reversed heart rate pair is swapped": {
			body: `{"targetType":{"workoutTargetTypeKey":"heart.rate.zone"},"targetValueOne":140,"targetValueTwo":120}`,
			want: `{"targetType":{"workoutTargetTypeKey":"heart.rate.zone"},"targetValueOne":120,"targetValueTwo":140}`,
		},
		"pair inside a repeat group is swapped": {
			body: `[{"workoutSteps":[{"workoutSteps":[` +
				`{"targetType":{"workoutTargetTypeKey":"pace.zone"},"targetValueOne":3,"targetValueTwo":4}]}]}]`,
			want: `[{"workoutSteps":[{"workoutSteps":[` +
				`{"targetType":{"workoutTargetTypeKey":"pace.zone"},"targetValueOne":4,"targetValueTwo":3}]}]}]`,
		},
		"reversed secondary pair is swapped": {
			body: `{"secondaryTargetType":{"workoutTargetTypeKey":"pace.zone"},` +
				`"secondaryTargetValueOne":3,"secondaryTargetValueTwo":4}`,
			want: `{"secondaryTargetType":{"workoutTargetTypeKey":"pace.zone"},` +
				`"secondaryTargetValueOne":4,"secondaryTargetValueTwo":3}`,
		},
		"ordered pair is kept byte for byte": {
			body: `{"targetType":{"workoutTargetTypeKey":"pace.zone"}, "targetValueOne":3.4,"targetValueTwo":3.1}`,
			want: `{"targetType":{"workoutTargetTypeKey":"pace.zone"}, "targetValueOne":3.4,"targetValueTwo":3.1}`,
		},
		"unverified target type is not touched": {
			body: `{"targetType":{"workoutTargetTypeKey":"power.zone"},"targetValueOne":250,"targetValueTwo":200}`,
			want: `{"targetType":{"workoutTargetTypeKey":"power.zone"},"targetValueOne":250,"targetValueTwo":200}`,
		},
		"half a pair is not touched": {
			body: `{"targetType":{"workoutTargetTypeKey":"pace.zone"},"targetValueOne":3}`,
			want: `{"targetType":{"workoutTargetTypeKey":"pace.zone"},"targetValueOne":3}`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			document, err := api.ParseWorkoutDocument([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseWorkoutDocument() = %v", err)
			}
			if got := string(document.Bytes()); got != tc.want {
				t.Errorf("Bytes() =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
