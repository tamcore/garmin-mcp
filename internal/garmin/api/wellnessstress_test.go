package api_test

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Synthetic stress fixtures. Every value is invented. The series deliberately mixes
// the shapes Garmin sends: a plain pair, a numeric string, the negative marker for a
// gap, a null element and a malformed one.
const (
	stressBody = `{"calendarDate":"` + testCalendarDate + `","maxStressLevel":81,` +
		`"avgStressLevel":"34","stressValuesArray":[[1738296000000,12],[1738296180000,"44"],` +
		`[1738296360000,64],[1738296540000,88],[1738296720000,-1],null,[1738296900000],` +
		`{"unexpected":true}],"unknownBlock":{"x":1}}`

	weeklyStressBody = `[{"calendarDate":"2026-01-05","value":31},` +
		`{"calendarDate":"2026-01-12","value":"37"},{"calendarDate":null,"value":null}]`
)

// emptyBody names the empty-document case shared by the tolerance tests in this file
// and in wellnessstressreadiness_test.go.
const emptyBody = "empty document"

func stressPath() string { return client.PathDailyStressPrefix + "/" + testCalendarDate }

func newStressClient(t *testing.T, h harness) *api.WellnessStress {
	t.Helper()

	stress, err := api.NewWellnessStress(h.rc)
	if err != nil {
		t.Fatalf("NewWellnessStress() = %v", err)
	}
	return stress
}

func TestWellnessStressConstructorsRefuseAnUnusableDependency(t *testing.T) {
	t.Parallel()

	if _, err := api.NewWellnessStress(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Errorf("NewWellnessStress(nil) = %v, want ErrNotConfigured", err)
	}
	if _, err := api.NewWellnessStressFrom(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Errorf("NewWellnessStressFrom(nil) = %v, want ErrNotConfigured", err)
	}
}

// TestWellnessStressSharesTheRequestLayerOfAWellnessClient proves the second
// constructor reaches the same fake service as the first.
func TestWellnessStressSharesTheRequestLayerOfAWellnessClient(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressPath(), testkit.JSON(http.StatusOK, stressBody))
	h := newHarness(t, script, client.Limits{})

	stress, err := api.NewWellnessStressFrom(newWellness(t, h))
	if err != nil {
		t.Fatalf("NewWellnessStressFrom() = %v", err)
	}
	if _, err := stress.DailyStress(t.Context(), h.session, mustDate(t, testCalendarDate),
		api.StressViewFull); err != nil {
		t.Fatalf("DailyStress() = %v", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1", got)
	}
}

func TestWellnessStressDailyStressDecodesTolerantly(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressPath(), testkit.JSON(http.StatusOK, stressBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newStressClient(t, h).DailyStress(t.Context(), h.session,
		mustDate(t, testCalendarDate), api.StressViewFull)
	if err != nil {
		t.Fatalf("DailyStress() = %v", err)
	}

	if got.CalendarDate == nil || *got.CalendarDate != testCalendarDate {
		t.Errorf("CalendarDate = %v, want %q", got.CalendarDate, testCalendarDate)
	}
	if level, ok := got.MaxStressLevel.Int64(); !ok || level != 81 {
		t.Errorf("MaxStressLevel = %v/%v, want 81", level, ok)
	}
	if level, ok := got.AvgStressLevel.Int64(); !ok || level != 34 {
		t.Errorf("AvgStressLevel = %v/%v, want 34 from the string form", level, ok)
	}
	if got.Values.Len() != 8 {
		t.Fatalf("%d samples decoded, want every element of the array", got.Values.Len())
	}
	samples := got.Values.Items()
	if level, ok := samples[1].Level.Int64(); !ok || level != 44 {
		t.Errorf("samples[1].Level = %v/%v, want 44 from the string form", level, ok)
	}
	for _, index := range []int{5, 6, 7} {
		if samples[index].Level.IsSet() {
			t.Errorf("samples[%d] must decode to an absent sample, not a reading", index)
		}
	}

	// The distribution applies the thresholds get_stress_summary applies: 12 rests,
	// 44 is low, 64 is medium, 88 is high, and the negative, null and malformed
	// elements are not readings.
	want := api.StressDistribution{Rest: 1, Low: 1, Medium: 1, High: 1, Valid: 4, Samples: 8}
	if got := got.Distribution(); got != want {
		t.Errorf("Distribution() = %+v, want %+v", got, want)
	}
}

// TestWellnessStressViewsShareOneURLAndDifferOnlyInTheOperation is the shape of the
// three-tools-one-read design: one path, three operation labels.
func TestWellnessStressViewsShareOneURLAndDifferOnlyInTheOperation(t *testing.T) {
	t.Parallel()

	views := map[api.StressView]client.Op{
		api.StressViewFull:    client.OpGetStressData,
		api.StressViewSummary: client.OpGetStressSummary,
		api.StressViewAllDay:  client.OpGetAllDayStress,
	}
	for view, wantOp := range views {
		script := testkit.NewScript().With(stressPath(),
			testkit.JSON(http.StatusInternalServerError, `{"error":"synthetic"}`))
		h := newHarness(t, script, client.Limits{MaxAttempts: 1})

		_, err := newStressClient(t, h).DailyStress(t.Context(), h.session,
			mustDate(t, testCalendarDate), view)

		var apiErr *client.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("view %v failed with %v, want an *APIError", view, err)
		}
		if apiErr.Op != wantOp {
			t.Errorf("view %v carried op %q, want %q", view, apiErr.Op, wantOp)
		}
		if apiErr.Endpoint != client.EndpointDailyStress {
			t.Errorf("view %v carried endpoint %q, want %q", view, apiErr.Endpoint,
				client.EndpointDailyStress)
		}
		requests := h.server.Requests()
		if len(requests) != 1 || requests[0].Path != stressPath() {
			t.Errorf("view %v requested %+v, want exactly %q", view, requests, stressPath())
		}
	}
}

// errOf discards a refused call's first result, so the table below stays terse.
func errOf[T any](_ T, err error) error { return err }

// TestWellnessStressRefusalsCostNoRequest covers every caller-side refusal at once: an
// unset date, an unknown view, an out-of-range week count and an unusable window are all
// validation failures, and not one is dispatched.
func TestWellnessStressRefusalsCostNoRequest(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 3})
	c := newStressClient(t, h)
	day := mustDate(t, testCalendarDate)
	none := client.Date{}
	ctx, s := t.Context(), h.session
	wide, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, "2026-01-31"))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}

	refusals := map[string]func() error{
		"stress without a date": func() error {
			return errOf(c.DailyStress(ctx, s, none, api.StressViewFull))
		},
		"unknown stress view": func() error {
			return errOf(c.DailyStress(ctx, s, day, api.StressView(99)))
		},
		"unknown readiness view": func() error {
			return errOf(c.TrainingReadiness(ctx, s, day, api.ReadinessView(99)))
		},
		"readiness without a date": func() error {
			return errOf(c.TrainingReadiness(ctx, s, none, api.ReadinessViewAll))
		},
		"zero weeks":         func() error { return errOf(c.WeeklyStress(ctx, s, day, 0)) },
		"negative weeks":     func() error { return errOf(c.WeeklyStress(ctx, s, day, -1)) },
		"weeks over the cap": func() error { return errOf(c.WeeklyStress(ctx, s, day, 53)) },
		"weekly stress without an end date": func() error {
			return errOf(c.WeeklyStress(ctx, s, none, 4))
		},
		"body battery without a window": func() error {
			return errOf(c.BodyBattery(ctx, s, client.DateRange{}))
		},
		"body battery over the window bound": func() error {
			return errOf(c.BodyBattery(ctx, s, wide))
		},
		"body-battery events without a date": func() error {
			return errOf(c.BodyBatteryEvents(ctx, s, none))
		},
		"daily events without a date": func() error { return errOf(c.AllDayEvents(ctx, s, none)) },
	}
	for name, refuse := range refusals {
		if err := refuse(); !errors.Is(err, client.ErrValidation) {
			t.Errorf("%s = %v, want a validation error", name, err)
		}
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestWellnessStressReportsAnEmptyDayAsEmpty proves absence is a first-class outcome.
func TestWellnessStressReportsAnEmptyDayAsEmpty(t *testing.T) {
	t.Parallel()

	cases := map[string]testkit.Behavior{
		"no content": {Status: http.StatusNoContent},
		nullBody:     testkit.JSON(http.StatusOK, nullBody),
		emptyBody:    testkit.JSON(http.StatusOK, `{}`),
	}
	for name, behavior := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(stressPath(), behavior)
			h := newHarness(t, script, client.Limits{})

			got, err := newStressClient(t, h).DailyStress(t.Context(), h.session,
				mustDate(t, testCalendarDate), api.StressViewAllDay)
			if err != nil {
				t.Fatalf("DailyStress() = %v", err)
			}
			if !got.IsEmpty() {
				t.Errorf("IsEmpty() = false for %s, want an empty day", name)
			}
		})
	}
}

func TestWellnessStressWeeklyStressReadsTheWeekCountAsAPathSegment(t *testing.T) {
	t.Parallel()

	const weeks = 4
	path := client.PathWeeklyStressStatsPrefix + "/" + testCalendarDate + "/" + strconv.Itoa(weeks)
	script := testkit.NewScript().With(path, testkit.JSON(http.StatusOK, weeklyStressBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newStressClient(t, h).WeeklyStress(t.Context(), h.session,
		mustDate(t, testCalendarDate), weeks)
	if err != nil {
		t.Fatalf("WeeklyStress() = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("%d weeks decoded, want 3", len(got))
	}
	if value, ok := got[1].Value.Int64(); !ok || value != 37 {
		t.Errorf("weeks[1].Value = %v/%v, want 37 from the string form", value, ok)
	}
	if got[2].CalendarDate != nil || got[2].Value.IsSet() {
		t.Error("a null week must decode to absent values, not to zeroes")
	}
}
