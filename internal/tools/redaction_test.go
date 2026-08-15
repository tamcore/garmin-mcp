package tools_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// forbiddenInErrors is what a caller-facing failure must never carry: a cookie, a
// token, a raw payload fragment, a coordinate, or a stack trace.
var forbiddenInErrors = []string{
	"secret-cookie-value", "GARMIN-SSO", "eyJ", "Set-Cookie", "Authorization",
	"startLatitude", "startLongitude", "sleepTimeSeconds", "restingHeartRate",
	"serialNumber", "goroutine", ".go:", "0x",
}

func assertSanitized(t *testing.T, text string) {
	t.Helper()

	for _, forbidden := range forbiddenInErrors {
		if strings.Contains(text, forbidden) {
			t.Errorf("the caller-facing text carries %q: %s", forbidden, text)
		}
	}
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// TestSensitiveResultsReportShapeToALogSink pins the redaction convention on the
// result models themselves: a tool result that reaches a log sink by accident must
// report its shape, never its health, location or device content.
func TestSensitiveResultsReportShapeToALogSink(t *testing.T) {
	t.Parallel()

	steps, heartRate := 9123, 52
	serial := "SYNTH-0001"

	cases := map[string]struct {
		value   slog.LogValuer
		secrets []string
	}{
		"sleep data": {
			value: tools.SleepData{
				Date:             testCalendarDate,
				SleepTimeSeconds: float64Ptr(27000),
			},
			secrets: []string{"27000"},
		},
		"daily summary": {
			value: tools.DailySummary{
				Date:             testCalendarDate,
				TotalSteps:       &steps,
				RestingHeartRate: &heartRate,
			},
			secrets: []string{"9123", "52"},
		},
		"activity list": {
			value: tools.ActivityList{Activities: []tools.ActivitySummary{{
				ActivityID: int64Ptr(9001),
				Name:       new("Morning run past home"),
			}}},
			secrets: []string{"9001", "Morning run past home"},
		},
		"device list": {
			value:   tools.DeviceList{Devices: []tools.DeviceSummary{{SerialNumber: &serial}}},
			secrets: []string{serial},
		},
		"typed splits": {
			value: tools.TypedSplitList{Splits: []tools.TypedSplit{{
				AverageHeartRate: float64Ptr(151),
			}}},
			secrets: []string{"151"},
		},
		"exercise sets": {
			value: tools.ExerciseSetList{Sets: []tools.ExerciseSet{{
				Weight: float64Ptr(20000),
			}}},
			secrets: []string{"20000"},
		},
		"user profile": {
			value: tools.UserProfile{
				FullName: new(testFullName),
				Location: new("Nowhere"),
			},
			secrets: []string{testFullName, "Nowhere"},
		},
		"profile settings": {
			value: tools.ProfileSettingsResult{
				FullName:  new(testFullName),
				Location:  new("Nowhere"),
				BirthDate: new("1990-01-01"),
			},
			secrets: []string{testFullName, "Nowhere", "1990-01-01"},
		},
		"personal records": {
			value: tools.PersonalRecordList{Records: []tools.PersonalRecord{{
				ActivityName: new("Synthetic 5k"),
				Value:        float64Ptr(1500),
			}}},
			secrets: []string{"Synthetic 5k", "1500"},
		},
		"split summaries": {
			value: tools.SplitSummaryList{Summaries: []tools.SplitSummary{{
				Calories: float64Ptr(320),
			}}},
			secrets: []string{"320"},
		},
		"time in zones": {
			value: tools.ZoneList{Zones: []tools.ZoneBucket{{
				SecondsIn: float64Ptr(600), HighBoundary: float64Ptr(117),
			}}},
			secrets: []string{"600", "117"},
		},
		"activity weather": {
			value: tools.ActivityWeather{
				Temperature: float64Ptr(10), WindSpeed: float64Ptr(11),
				TemperatureUnit: "C",
			},
			secrets: []string{"11"},
		},
		"workout list": {
			value: tools.WorkoutList{Workouts: []tools.WorkoutEntry{{
				WorkoutID: 550001, Name: new("Easy run"),
			}}},
			secrets: []string{"550001", "Easy run"},
		},
		"workout detail": {
			value: tools.WorkoutDetail{
				WorkoutID: 550001, Name: new("Easy run"), Segments: []any{"step"},
			},
			secrets: []string{"550001", "Easy run", "step"},
		},
		"saved workout": {
			value:   tools.SavedWorkoutResult{WorkoutID: 550009, Name: savedWorkoutName},
			secrets: []string{"550009", savedWorkoutName},
		},
		"activity update": {
			value:   tools.ActivityUpdate{ActivityID: 987654321, Updated: "feel", Status: 200},
			secrets: []string{"987654321"},
		},
		"created activity": {
			value:   tools.CreatedActivityResult{ActivityID: 987654321},
			secrets: []string{"987654321"},
		},
		"deletion": {
			value:   tools.DeletionResult{ID: 987654321, Deleted: true, Status: 204},
			secrets: []string{"987654321"},
		},
		"schedule": {
			value: tools.ScheduleResult{
				WorkoutID: 550001, CalendarDate: testCalendarDate, Status: 200,
			},
			secrets: []string{"550001", testCalendarDate},
		},
		"batch": {
			value: tools.BatchResult{Outcomes: []tools.BatchOutcome{{
				ID: 550001, Applied: true,
			}}, Requested: 1, Applied: 1},
			secrets: []string{"550001"},
		},
		"downloaded file": {
			value: tools.DownloadedFile{
				ID: 987654321, Format: "fit", Bytes: 8, URI: "garmin://activity/987654321.fit",
			},
			secrets: []string{"987654321"},
		},
		"created strength activity": {
			value: tools.CreatedStrengthActivityResult{
				ActivityID: 987654321,
				Sets: tools.ExerciseSetList{Sets: []tools.ExerciseSet{{
					Weight: float64Ptr(40000),
				}}, Count: 1},
			},
			secrets: []string{"987654321", "40000"},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			logged := logValue(testCase.value)
			for _, secret := range testCase.secrets {
				if strings.Contains(logged, secret) {
					t.Errorf("the log record carries %q: %s", secret, logged)
				}
			}
			if !strings.Contains(logged, "model=") {
				t.Errorf("the log record names no model shape: %s", logged)
			}
		})
	}
}

// logValue renders one record and drops the timestamp.
//
// The timestamp must go, or this assertion is a coin flip: the secrets under test
// include short numbers such as a resting heart rate of 52, and a record stamped
// at 23:52 contains those digits for a whole minute of every hour. The test would
// then fail for a reason that has nothing to do with redaction.
func logValue(value slog.LogValuer) string {
	var buffer bytes.Buffer
	options := &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	}
	logger := slog.New(slog.NewTextHandler(&buffer, options))
	logger.Info("probe", slog.Any("result", value))
	return buffer.String()
}

func float64Ptr(v float64) *float64 { return new(v) }

func int64Ptr(v int64) *int64 { return new(v) }

// TestASecondReadReachesGarminAgain is the no-cache assertion: health and location
// results are not cached, so two identical calls cost two upstream reads.
func TestASecondReadReachesGarminAgain(t *testing.T) {
	h := newHarness(t, readScript())

	args := map[string]any{argDate: testCalendarDate}
	h.call(t, tools.ToolGetSleepData, args)
	h.call(t, tools.ToolGetSleepData, args)

	if got := countPath(h.requests(), sleepPath()); got != 2 {
		t.Errorf("the fake served %d sleep reads, want 2: no health result may be cached", got)
	}
}

func countPath(paths []string, want string) int {
	count := 0
	for _, path := range paths {
		if path == want {
			count++
		}
	}
	return count
}
