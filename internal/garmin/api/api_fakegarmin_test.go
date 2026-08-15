//go:build fakegarmin

package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// fullScript scripts every endpoint this slice reads, so one harness can drive the
// whole flow a tool call performs: resolve the display name, then read the domains.
func fullScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, testkit.JSON(http.StatusOK, testkit.SocialProfileJSON(fakeDisplayName))).
		With(client.PathUserSettings, testkit.JSON(http.StatusOK, userSettingsBody)).
		With(client.PathActivitySearch, testkit.JSON(http.StatusOK, activityArray(9001, 9002))).
		With(sleepPath(), testkit.JSON(http.StatusOK, sleepBody())).
		With(summaryPath(), testkit.JSON(http.StatusOK, summaryBody(false))).
		With(client.PathDevices, testkit.JSON(http.StatusOK, devicesBody)).
		With(splitsPath(), testkit.JSON(http.StatusOK, `{"lapDTOs":[`+splitEntry+`]}`)).
		With(exerciseSetsPath(), testkit.JSON(http.StatusOK, exerciseSetsBody))
}

func TestFakeServiceWholeReadSliceAgainstOneAccount(t *testing.T) {
	h := newHarness(t, fullScript(), client.Limits{})

	name := readProfileDomain(t, h)
	readActivityDomain(t, h)
	readWellnessDomain(t, h, name)
	readDeviceDomain(t, h)
	readActivityDetailDomain(t, h)

	requests := h.server.Requests()
	if len(requests) < 8 {
		t.Fatalf("the fake received %d requests, want at least 8", len(requests))
	}
	for _, request := range requests {
		if request.Header.Get("Authorization") != "" {
			t.Error("a request carried an Authorization header: the caller owns the token")
		}
	}
}

// readProfileDomain resolves the display name the wellness reads need.
func readProfileDomain(t *testing.T, h harness) client.DisplayName {
	t.Helper()

	profile := newProfile(t, h)
	name, err := profile.DisplayName(t.Context(), h.session)
	if err != nil {
		t.Fatalf("DisplayName() = %v", err)
	}
	if _, err := profile.Settings(t.Context(), h.session); err != nil {
		t.Fatalf("Settings() = %v", err)
	}
	return name
}

func readActivityDomain(t *testing.T, h harness) {
	t.Helper()

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	activities, err := newActivities(t, h).List(t.Context(), h.session, api.ListQuery{Page: page})
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(activities.Activities) != 2 {
		t.Errorf("%d activities, want 2", len(activities.Activities))
	}
}

func readWellnessDomain(t *testing.T, h harness, name client.DisplayName) {
	t.Helper()

	wellness := newWellness(t, h)
	if _, err := wellness.DailySleep(t.Context(), h.session, name, mustDate(t, testCalendarDate)); err != nil {
		t.Fatalf("DailySleep() = %v", err)
	}
	if _, err := wellness.UserSummary(t.Context(), h.session, name, mustDate(t, testCalendarDate)); err != nil {
		t.Fatalf("UserSummary() = %v", err)
	}
}

func readDeviceDomain(t *testing.T, h harness) {
	t.Helper()

	devices, err := newDevices(t, h).List(t.Context(), h.session)
	if err != nil {
		t.Fatalf("devices List() = %v", err)
	}
	if len(devices) != 2 {
		t.Errorf("%d devices, want 2", len(devices))
	}
}

func readActivityDetailDomain(t *testing.T, h harness) {
	t.Helper()

	details := newActivityDetails(t, h)
	splits, err := details.TypedSplits(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("TypedSplits() = %v", err)
	}
	if splits.Len() != 1 {
		t.Errorf("%d splits, want 1 from the lapDTOs shape", splits.Len())
	}
	if _, err := details.ExerciseSets(t.Context(), h.session, mustID(t)); err != nil {
		t.Fatalf("ExerciseSets() = %v", err)
	}
}

// writeScript scripts the whole write flow one strength session performs: create
// the activity, replace its sets, read the sets back, and read the activity back.
func writeScript() testkit.Script {
	return strengthCreateScript(`{"activityId":18446744}`).
		With(client.PathWorkoutPrefix, testkit.JSON(http.StatusOK,
			`{"workoutId":18446744,"workoutName":"Easy Run"}`)).
		With(client.PathWorkoutSchedule+"/18446744", testkit.JSON(http.StatusOK, `{"id":5150}`))
}

// TestFakeServiceWholeWriteSliceAgainstOneAccount drives the write domains end to
// end against the scripted fake, including the verification reads.
func TestFakeServiceWholeWriteSliceAgainstOneAccount(t *testing.T) {
	h := newHarness(t, writeScript(), client.Limits{})

	created, err := newStrengthWrites(t, h).Create(t.Context(), h.session, strengthActivity())
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if created.Sets.Sets.Len() != 1 {
		t.Errorf("%d verified sets, want 1", created.Sets.Sets.Len())
	}

	workouts := newWorkouts(t, h)
	saved, err := workouts.Upload(t.Context(), h.session, mustWorkoutDocument(t))
	if err != nil {
		t.Fatalf("Upload() = %v", err)
	}
	workoutID, err := saved.ID()
	if err != nil {
		t.Fatalf("ID() = %v", err)
	}
	if _, err := workouts.Schedule(t.Context(), h.session, workoutID,
		mustDate(t, testCalendarDate)); err != nil {
		t.Fatalf("Schedule() = %v", err)
	}

	for _, request := range h.server.Requests() {
		if request.Header.Get("Authorization") != "" {
			t.Error("a request carried an Authorization header: the caller owns the token")
		}
	}
}

func TestFakeServiceRateLimitReachesTheDomainCallerIntact(t *testing.T) {
	script := testkit.NewScript().With(client.PathDevices, testkit.RateLimited(4))
	h := newHarness(t, script, client.Limits{})

	_, err := newDevices(t, h).List(t.Context(), h.session)
	if !errors.Is(err, client.ErrRateLimited) {
		t.Fatalf("List() = %v, want ErrRateLimited", err)
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("the failure is not an *APIError")
	}
	if apiErr.RetryAfter.Seconds() != 4 {
		t.Errorf("RetryAfter = %v, want 4s", apiErr.RetryAfter)
	}
	if apiErr.Op != client.OpListDevices {
		t.Errorf("Op = %q, want %q", apiErr.Op, client.OpListDevices)
	}
}

func TestFakeServicePaginatedDateReadWalksTheFakePages(t *testing.T) {
	script := testkit.NewScript().With(client.PathActivitySearch,
		testkit.JSON(http.StatusOK, activityArray(9001, 9002)),
		testkit.JSON(http.StatusOK, activityArray(9003)),
	)
	h := newHarness(t, script, client.Limits{MaxPageSize: 2, MaxPages: 5})

	span, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}

	got, err := newActivities(t, h).ListByDate(t.Context(), h.session, api.DateQuery{Span: span, PageSize: 2})
	if err != nil {
		t.Fatalf("ListByDate() = %v", err)
	}
	if len(got) != 3 {
		t.Errorf("%d activities, want 3: a short page ends the walk", len(got))
	}
	if requests := h.server.Requests(); len(requests) != 2 {
		t.Errorf("the fake received %d requests, want 2", len(requests))
	}
}
