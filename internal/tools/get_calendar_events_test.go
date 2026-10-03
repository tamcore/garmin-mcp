package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every calendar fixture here is synthetic: the races, locations, links and
// identifiers are invented and none is a recording of a real account.
const (
	eventsWindowStart = "2026-03-01"
	eventsWindowEnd   = "2026-04-30"

	eventsSeamTitle = "Synthetic Half"
)

// marchCalendarBody is one month of the feed. It carries a race, a workout that must
// not be reported as an event, and an event dated outside the window because Garmin
// serves the whole month.
const marchCalendarBody = `{"calendarItems":[` +
	`{"itemType":"event","title":"` + eventsSeamTitle + `","date":"2026-03-14",` +
	`"isRace":true,"primaryEvent":true,"subscribed":false,"location":"  Somewhere  ",` +
	`"url":"https://example.invalid/race","shareableEventUuid":"aaaa-bbbb",` +
	`"completionTarget":{"unitType":"distance","unit":"kilometer","value":21.0975},` +
	`"eventTimeLocal":{"startTimeHhMm":"09:30","timeZoneId":"Europe/Berlin"}},` +
	`{"itemType":"workout","title":"Easy Run","date":"2026-03-15"},` +
	`{"itemType":"event","title":"Out Of Window","date":"2026-02-20"}]}`

// aprilCalendarBody repeats the March race — a multi-month read can see an event twice
// at the seams — and adds one whose goal is a duration rather than a distance.
const aprilCalendarBody = `{"calendarItems":[` +
	`{"itemType":"event","title":"` + eventsSeamTitle + `","date":"2026-03-14",` +
	`"isRace":true,"shareableEventUuid":"aaaa-bbbb"},` +
	`{"itemType":"event","title":"Synthetic Fun Run","date":"2026-04-05",` +
	`"completionTarget":{"unitType":"duration","value":3600},"location":"   "}]}`

// eventsYear is the year every event fixture is dated in.
const eventsYear = "2026"

// calendarMonthPathFor is the path of one month, with Garmin's zero-based month.
func calendarMonthPathFor(zeroBasedMonth string) string {
	return client.PathCalendarService + "/year/" + eventsYear + "/month/" + zeroBasedMonth
}

// eventsScript scripts the two months the test window touches.
func eventsScript() testkit.Script {
	return testkit.NewScript().
		With(calendarMonthPathFor("2"), testkit.JSON(http.StatusOK, marchCalendarBody)).
		With(calendarMonthPathFor("3"), testkit.JSON(http.StatusOK, aprilCalendarBody))
}

// eventsWindowArgs is the window every event test asks for.
func eventsWindowArgs() map[string]any {
	return map[string]any{argStartDate: eventsWindowStart, argEndDate: eventsWindowEnd}
}

func TestGetCalendarEventsReturnsOnlyEventsInsideTheWindow(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, eventsScript())

	result := h.call(t, ToolGetCalendarEvents, eventsWindowArgs())

	events := list(t, result, "events")
	if len(events) != 2 {
		t.Fatalf("events holds %d entries, want the race and the fun run: %v",
			len(events), events)
	}
	if got := number(t, result, "count"); got != 2 {
		t.Errorf("count = %v, want 2", got)
	}

	first := entry(t, events, 0)
	if got, _ := first["title"].(string); got != eventsSeamTitle {
		t.Errorf("events[0].title = %q, want the earlier event first", got)
	}
	if race, _ := first["is_race"].(bool); !race {
		t.Error("events[0].is_race = false, want Garmin's own flag carried through")
	}
	if primary, _ := first["primary_event"].(bool); !primary {
		t.Error("events[0].primary_event = false, want the plan's goal race marked")
	}
	if got := number(t, first, "distance_meters"); got != 21097.5 {
		t.Errorf("events[0].distance_meters = %v, want the distance goal", got)
	}
	if got, _ := first["start_time_local"].(string); got != "09:30" {
		t.Errorf("events[0].start_time_local = %q, want the published local time", got)
	}
	if got, _ := first["time_zone"].(string); got != "Europe/Berlin" {
		t.Errorf("events[0].time_zone = %q, want the event's zone", got)
	}
	// The location arrives padded and is reported trimmed.
	if got, _ := first["location"].(string); got != "Somewhere" {
		t.Errorf("events[0].location = %q, want the trimmed location", got)
	}

	second := entry(t, events, 1)
	if got, _ := second["title"].(string); got != "Synthetic Fun Run" {
		t.Errorf("events[1].title = %q, want the later event second", got)
	}
	// A goal that is not a distance produces no distance at all.
	if _, present := second["distance_meters"]; present {
		t.Error("a duration goal was reported as a distance")
	}
	// A location of nothing but space is no location.
	if _, present := second["location"]; present {
		t.Error("a blank location reached the caller")
	}
}

// TestGetCalendarEventsReadsOneMonthPerMonthSpanned pins the month-at-a-time shape of
// the feed, including Garmin's zero-based month.
func TestGetCalendarEventsReadsOneMonthPerMonthSpanned(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, eventsScript())

	h.call(t, ToolGetCalendarEvents, eventsWindowArgs())

	paths := make([]string, 0, 2)
	for _, request := range h.fake.Requests() {
		paths = append(paths, request.Path)
	}
	want := []string{calendarMonthPathFor("2"), calendarMonthPathFor("3")}
	if len(paths) != len(want) {
		t.Fatalf("dispatched %v, want one read per month: %v", paths, want)
	}
	for index, path := range want {
		if paths[index] != path {
			t.Errorf("request %d asked %q, want %q", index, paths[index], path)
		}
	}
}

// TestGetCalendarEventsDropsASeamDuplicate proves an event two months both report is
// returned once.
func TestGetCalendarEventsDropsASeamDuplicate(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, eventsScript())

	result := h.call(t, ToolGetCalendarEvents, eventsWindowArgs())

	seen := 0
	events := list(t, result, "events")
	for index := range events {
		if title, _ := entry(t, events, index)["title"].(string); title == eventsSeamTitle {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the seam event appears %d times, want once", seen)
	}
}

// TestGetCalendarEventsReportsAnEmptyCalendar proves an account with no events answers
// with an empty list rather than a failure.
func TestGetCalendarEventsReportsAnEmptyCalendar(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(calendarMonthPathFor("2"),
		testkit.JSON(http.StatusOK, `{"calendarItems":[]}`))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetCalendarEvents, map[string]any{
		argStartDate: eventsWindowStart, argEndDate: "2026-03-31",
	})

	if got := len(list(t, result, "events")); got != 0 {
		t.Errorf("events holds %d entries, want none", got)
	}
	if got := number(t, result, "count"); got != 0 {
		t.Errorf("count = %v, want 0", got)
	}
}

// TestGetCalendarEventsRefusesAReversedWindow keeps validation at the boundary.
func TestGetCalendarEventsRefusesAReversedWindow(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, eventsScript())

	if err := h.callError(t, ToolGetCalendarEvents, map[string]any{
		argStartDate: eventsWindowEnd, argEndDate: eventsWindowStart,
	}); err == "" {
		t.Error("a reversed window was accepted")
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("dispatched %d requests for a refused window, want none", got)
	}
}

// TestCalendarEventListLogValueReportsShapeOnly keeps a race name and a location out
// of the logs.
func TestCalendarEventListLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	title, location := eventsSeamTitle, "Somewhere"
	out := CalendarEventList{
		Count:  1,
		Events: []CalendarEvent{{Title: &title, Location: &location}},
	}

	rendered := out.LogValue().String()
	for _, forbidden := range []string{title, location} {
		if contains(rendered, forbidden) {
			t.Errorf("the log value %q carries %q", rendered, forbidden)
		}
	}
}
