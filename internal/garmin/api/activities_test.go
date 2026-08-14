package api_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// activityJSON is one synthetic activity. duration arrives as a string on purpose:
// Garmin sends the same field both ways, which is what the union decoder exists for.
func activityJSON(id int64) string {
	return `{"activityId":` + strconv.FormatInt(id, 10) +
		`,"activityName":"Morning Run","startTimeLocal":"2026-01-31 06:12:00",` +
		`"distance":10000.0,"duration":"3600","activityType":{"typeId":1,"typeKey":"running"},` +
		`"startLatitude":48.1,"startLongitude":11.5,"unknownField":true}`
}

func activityArray(ids ...int64) string {
	var body strings.Builder
	body.WriteString("[")
	for index, id := range ids {
		if index > 0 {
			body.WriteString(",")
		}
		body.WriteString(activityJSON(id))
	}
	return body.String() + "]"
}

func newActivities(t *testing.T, h harness) *api.Activities {
	t.Helper()

	activities, err := api.NewActivities(h.rc)
	if err != nil {
		t.Fatalf("NewActivities() = %v", err)
	}
	return activities
}

func TestActivitiesListDecodesAPaginatedArray(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivitySearch,
		testkit.JSON(http.StatusOK, activityArray(9001, 9002)))
	h := newHarness(t, script, client.Limits{})

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	activityType, err := api.ParseActivityType("running")
	if err != nil {
		t.Fatalf("ParseActivityType() = %v", err)
	}

	got, err := newActivities(t, h).List(t.Context(), h.session, api.ListQuery{Page: page, Type: activityType})
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	if len(got.Activities) != 2 {
		t.Fatalf("%d activities decoded, want 2", len(got.Activities))
	}
	assertFirstActivity(t, got.Activities[0])

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	assertListQuery(t, requests[0].Query)
}

// assertFirstActivity checks the tolerant decoding of one activity record.
func assertFirstActivity(t *testing.T, first api.Activity) {
	t.Helper()

	if first.ActivityID == nil || *first.ActivityID != 9001 {
		t.Errorf("ActivityID = %v, want 9001", first.ActivityID)
	}
	if duration, ok := first.Duration.Float64(); !ok || duration != 3600 {
		t.Errorf("Duration = %v/%v, want 3600 from the string form", duration, ok)
	}
	if distance, ok := first.Distance.Float64(); !ok || distance != 10000 {
		t.Errorf("Distance = %v/%v, want 10000", distance, ok)
	}
	if string(first.ActivityType) == "" {
		t.Error("ActivityType raw shape was not retained")
	}
}

// assertListQuery checks that pagination and the type filter reached the query
// string rather than the path.
func assertListQuery(t *testing.T, query url.Values) {
	t.Helper()

	if query.Get(client.QueryStart) != "0" || query.Get(client.QueryLimit) != "20" {
		t.Errorf("pagination = %q/%q, want 0/20", query.Get(client.QueryStart), query.Get(client.QueryLimit))
	}
	if query.Get(client.QueryActivityType) != "running" {
		t.Errorf("activityType = %q, want running", query.Get(client.QueryActivityType))
	}
}

func TestActivitiesListRejectsAPageOverTheConfiguredBound(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxPageSize: 20})
	page, err := client.NewPage(0, 21)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}

	_, err = newActivities(t, h).List(t.Context(), h.session, api.ListQuery{Page: page})
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("List() = %v, want a validation error", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestActivitiesListByDateWalksPagesUntilTheServerRunsOut(t *testing.T) {
	t.Parallel()

	// Source: get_activities_by_date, which pages 20 at a time until Garmin returns
	// an empty page.
	script := testkit.NewScript().With(client.PathActivitySearch,
		testkit.JSON(http.StatusOK, activityArray(9001, 9002)),
		testkit.JSON(http.StatusOK, activityArray(9003, 9004)),
		testkit.JSON(http.StatusOK, `[]`))
	h := newHarness(t, script, client.Limits{MaxPageSize: 2, MaxPages: 10})

	span, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}
	sort, err := api.ParseSortOrder("asc")
	if err != nil {
		t.Fatalf("ParseSortOrder() = %v", err)
	}

	got, err := newActivities(t, h).ListByDate(t.Context(), h.session, api.DateQuery{
		Span:     span,
		Sort:     sort,
		PageSize: 2,
	})
	if err != nil {
		t.Fatalf("ListByDate() = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("%d activities returned, want 4 across two pages", len(got))
	}

	requests := h.server.Requests()
	if len(requests) != 3 {
		t.Fatalf("the fake received %d requests, want 3", len(requests))
	}
	for index, want := range []string{"0", "2", "4"} {
		if got := requests[index].Query.Get(client.QueryStart); got != want {
			t.Errorf("request %d start = %q, want %q", index, got, want)
		}
	}
	if got := requests[0].Query.Get(client.QueryStartDate); got != "2026-01-01" {
		t.Errorf("startDate = %q, want 2026-01-01", got)
	}
	if got := requests[0].Query.Get(client.QueryEndDate); got != testCalendarDate {
		t.Errorf("endDate = %q, want %q", got, testCalendarDate)
	}
	if got := requests[0].Query.Get(client.QuerySortOrder); got != "asc" {
		t.Errorf("sortOrder = %q, want asc", got)
	}
}

func TestActivitiesListByDateFailsLoudlyOnEndlessPagination(t *testing.T) {
	t.Parallel()

	// Source: the MAX_PAGINATED_REQUESTS guard, which aborts rather than truncating
	// when a server never returns an empty page.
	script := testkit.NewScript().With(client.PathActivitySearch,
		testkit.JSON(http.StatusOK, activityArray(9001, 9002)))
	h := newHarness(t, script, client.Limits{MaxPageSize: 2, MaxPages: 3})

	span, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}

	_, err = newActivities(t, h).ListByDate(t.Context(), h.session, api.DateQuery{Span: span, PageSize: 2})
	if !errors.Is(err, client.ErrPaginationExhausted) {
		t.Fatalf("ListByDate() = %v, want ErrPaginationExhausted", err)
	}
	if got := len(h.server.Requests()); got != 3 {
		t.Errorf("the fake received %d requests, want the 3-page bound", got)
	}
}

func TestActivitiesListByDateBoundsTheDateWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 7})
	span, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}

	_, err = newActivities(t, h).ListByDate(t.Context(), h.session, api.DateQuery{Span: span})
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("ListByDate() = %v, want a validation error for a 31-day window", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestParseActivityTypeAndSortOrderValidateTheirInput(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"running", "fitness_equipment", "multi_sport"} {
		if _, err := api.ParseActivityType(value); err != nil {
			t.Errorf("ParseActivityType(%q) = %v, want nil", value, err)
		}
	}
	for _, value := range []string{"Running", "run ning", "run/../x", "run&x=1", "a\nb", string(make([]byte, 64))} {
		if _, err := api.ParseActivityType(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseActivityType(%q) = %v, want a validation error", value, err)
		}
	}
	if got, err := api.ParseActivityType(""); err != nil || !got.IsZero() {
		t.Errorf("ParseActivityType(\"\") = %v/%v, want the unfiltered zero value", got, err)
	}

	for _, value := range []string{"", "asc", "desc"} {
		if _, err := api.ParseSortOrder(value); err != nil {
			t.Errorf("ParseSortOrder(%q) = %v, want nil", value, err)
		}
	}
	for _, value := range []string{"ASC", "ascending", "up"} {
		if _, err := api.ParseSortOrder(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseSortOrder(%q) = %v, want a validation error", value, err)
		}
	}
}

func TestActivitiesToleratesAnObjectWrappedArray(t *testing.T) {
	t.Parallel()

	// Some deployments answer the search endpoint with an object that carries the
	// array, rather than with a bare array.
	body := fmt.Sprintf(`{"activityList":%s}`, activityArray(9001))
	script := testkit.NewScript().With(client.PathActivitySearch, testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	got, err := newActivities(t, h).List(t.Context(), h.session, api.ListQuery{Page: page})
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(got.Activities) != 1 {
		t.Errorf("%d activities decoded, want 1 from the wrapped array", len(got.Activities))
	}
}
