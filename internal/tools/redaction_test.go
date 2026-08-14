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
	latitude := 51.4779
	serial := "SYNTH-0001"

	cases := map[string]struct {
		value   slog.LogValuer
		secrets []string
	}{
		"sleep data": {
			value: tools.SleepData{
				Date:             "2026-01-31",
				SleepTimeSeconds: float64Ptr(27000),
			},
			secrets: []string{"27000"},
		},
		"daily summary": {
			value: tools.DailySummary{
				Date:             "2026-01-31",
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

	_ = latitude
}

func logValue(value slog.LogValuer) string {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
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
