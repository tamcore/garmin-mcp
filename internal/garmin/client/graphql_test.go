package client_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The synthetic window every calendar fixture in this file uses.
const (
	windowStart = "2026-01-05"
	windowEnd   = "2026-01-11"
)

// scheduleSummariesRequest is the calendar read upstream's get_scheduled_workouts
// and its schedule pre-check both send. The end of the window is fixed; only the
// start varies, because it is the value the injection cases probe.
func scheduleSummariesRequest(start string) client.GraphQLRequest {
	return client.GraphQLRequest{
		Op:       client.OpGetScheduledWorkouts,
		Endpoint: client.EndpointGraphQL,
		Field:    client.GraphQLFieldWorkoutScheduleSummaries,
		Arguments: []client.GraphQLArgument{
			{Name: client.GraphQLArgStartDate, Value: start},
			{Name: client.GraphQLArgEndDate, Value: windowEnd},
		},
	}
}

// trainingPlanRequest is the Garmin Coach read upstream's
// _get_garmin_coach_workouts sends.
func trainingPlanRequest(date string) client.GraphQLRequest {
	return client.GraphQLRequest{
		Op:       client.OpGetTrainingPlanWorkouts,
		Endpoint: client.EndpointGraphQL,
		Field:    client.GraphQLFieldTrainingPlan,
		Arguments: []client.GraphQLArgument{
			{Name: client.GraphQLArgCalendarDate, Value: date},
			{Name: client.GraphQLArgLang, Value: client.GraphQLLangDefault},
			{Name: client.GraphQLArgFirstDayOfWeek, Value: client.GraphQLFirstDayOfWeekDefault},
		},
	}
}

// readRequestBody replays an outbound body through GetBody, which is the same seam
// the token layer uses to retry a request after a refresh.
func readRequestBody(t *testing.T, req *http.Request) string {
	t.Helper()

	if req.GetBody == nil {
		t.Fatal("the request carries no replayable body")
	}
	body, err := req.GetBody()
	if err != nil {
		t.Fatalf("GetBody() = %v", err)
	}
	defer func() { _ = body.Close() }()

	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("reading the request body: %v", err)
	}
	return string(raw)
}

// TestGraphQLDocumentReproducesTheUpstreamShape pins the rendered query against the
// literal python-garminconnect 0.3.10 callers, character for character.
func TestGraphQLDocumentReproducesTheUpstreamShape(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		req  client.GraphQLRequest
		want string
	}{
		"scheduled workouts": {
			req: scheduleSummariesRequest(windowStart),
			want: `query{workoutScheduleSummariesScalar(startDate:"2026-01-05", ` +
				`endDate:"2026-01-11")}`,
		},
		"training plan": {
			req: trainingPlanRequest(windowStart),
			want: `query{trainingPlanScalar(calendarDate:"2026-01-05", lang:"en-US", ` +
				`firstDayOfWeek:"monday")}`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.req.Document()
			if err != nil {
				t.Fatalf("Document() = %v", err)
			}
			if got != tc.want {
				t.Errorf("Document() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGraphQLRequestValidationRefusesAnythingItCannotRender is the injection guard:
// an argument value is interpolated into the query text, exactly as upstream does,
// so every character that could close the literal or open a second field is refused
// rather than escaped.
func TestGraphQLRequestValidationRefusesAnythingItCannotRender(t *testing.T) {
	t.Parallel()

	valid := scheduleSummariesRequest(windowStart)
	cases := map[string]struct {
		mutate  func(client.GraphQLRequest) client.GraphQLRequest
		wantErr bool
	}{
		"valid": {mutate: func(r client.GraphQLRequest) client.GraphQLRequest { return r }},
		"unknown field": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Field = client.GraphQLField("mutation")
				return r
			},
			wantErr: true,
		},
		"unknown op": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Op = client.Op("not_a_label")
				return r
			},
			wantErr: true,
		},
		"unknown endpoint": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Endpoint = client.Endpoint("https://connect.garmin.com/graphql")
				return r
			},
			wantErr: true,
		},
		"no arguments": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Arguments = nil
				return r
			},
			wantErr: true,
		},
		"argument name is not an identifier": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Arguments = []client.GraphQLArgument{{Name: "start-date", Value: windowStart}}
				return r
			},
			wantErr: true,
		},
		"argument value closes the literal": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Arguments = []client.GraphQLArgument{
					{Name: client.GraphQLArgStartDate, Value: `2026-01-05") { x } q{y(a:"`},
				}
				return r
			},
			wantErr: true,
		},
		"argument value carries a backslash": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Arguments = []client.GraphQLArgument{
					{Name: client.GraphQLArgStartDate, Value: "2026-01\\05"},
				}
				return r
			},
			wantErr: true,
		},
		"argument value carries a newline": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Arguments = []client.GraphQLArgument{
					{Name: client.GraphQLArgStartDate, Value: "2026-01-05\nquery{x}"},
				}
				return r
			},
			wantErr: true,
		},
		"argument value is empty": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Arguments = []client.GraphQLArgument{
					{Name: client.GraphQLArgStartDate, Value: ""},
				}
				return r
			},
			wantErr: true,
		},
		"argument value is too long": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				r.Arguments = []client.GraphQLArgument{
					{Name: client.GraphQLArgStartDate,
						Value: strings.Repeat("a", client.MaxGraphQLArgumentValueLen+1)},
				}
				return r
			},
			wantErr: true,
		},
		"too many arguments": {
			mutate: func(r client.GraphQLRequest) client.GraphQLRequest {
				arguments := make([]client.GraphQLArgument, 0, client.MaxGraphQLArguments+1)
				for range client.MaxGraphQLArguments + 1 {
					arguments = append(arguments,
						client.GraphQLArgument{Name: client.GraphQLArgStartDate, Value: windowStart})
				}
				r.Arguments = arguments
				return r
			},
			wantErr: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.mutate(valid).Validate()
			if tc.wantErr {
				if !errors.Is(err, client.ErrValidation) {
					t.Fatalf("Validate() = %v, want ErrValidation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestGraphQLLabelsRenderSanitized keeps a field built from an arbitrary string out
// of a log line.
func TestGraphQLLabelsRenderSanitized(t *testing.T) {
	t.Parallel()

	if got := client.GraphQLFieldTrainingPlan.String(); got != "trainingPlanScalar" {
		t.Errorf("known field String() = %q", got)
	}
	if got := client.GraphQLField("https://connect.garmin.com?token=x").String(); got != labelUnknown {
		t.Errorf("unknown field String() = %q, want %q", got, labelUnknown)
	}
	if len(client.KnownGraphQLFields()) == 0 {
		t.Error("KnownGraphQLFields() is empty")
	}
}

// TestGraphQLPostsTheUpstreamBodyToTheGateway pins endpoint, method and body.
func TestGraphQLPostsTheUpstreamBodyToTheGateway(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{
		header: jsonHeader(),
		body:   []byte(`{"data":{"workoutScheduleSummariesScalar":[]}}`),
	}}}
	c := newTestClient(t, client.Limits{})

	var out []struct{}
	if _, err := c.GraphQL(t.Context(), mustSession(t, caller),
		scheduleSummariesRequest(windowStart), &out); err != nil {
		t.Fatalf("GraphQL() = %v", err)
	}

	req := caller.lastRequest(t)
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if req.URL.Path != client.PathGraphQL {
		t.Errorf("path = %q, want %q", req.URL.Path, client.PathGraphQL)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	body := readRequestBody(t, req)
	want := `{"query":"query{workoutScheduleSummariesScalar(startDate:\"2026-01-05\", ` +
		`endDate:\"2026-01-11\")}"}`
	if body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
}

// TestGraphQLDecodesTheNamedFieldOutOfData covers the tolerant decode: the caller
// receives data[field], an unknown sibling is ignored, and a missing field is not a
// failure.
func TestGraphQLDecodesTheNamedFieldOutOfData(t *testing.T) {
	t.Parallel()

	type entry struct {
		WorkoutID    int64  `json:"workoutId"`
		ScheduleDate string `json:"scheduleDate"`
	}
	cases := map[string]struct {
		body  string
		want  int
		first entry
	}{
		"one entry": {
			body: `{"data":{"workoutScheduleSummariesScalar":` +
				`[{"workoutId":42,"scheduleDate":"2026-01-05","unknownBlock":7}],"other":1}}`,
			want:  1,
			first: entry{WorkoutID: 42, ScheduleDate: "2026-01-05"},
		},
		"missing field":   {body: `{"data":{}}`},
		"null data":       {body: `{"data":null}`},
		"no data at all":  {body: `{}`},
		"empty body":      {body: ``},
		"null collection": {body: `{"data":{"workoutScheduleSummariesScalar":null}}`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			caller := &stubCaller{outcomes: []stubOutcome{{header: jsonHeader(), body: []byte(tc.body)}}}
			c := newTestClient(t, client.Limits{})

			var out []entry
			if _, err := c.GraphQL(t.Context(), mustSession(t, caller),
				scheduleSummariesRequest(windowStart), &out); err != nil {
				t.Fatalf("GraphQL() = %v", err)
			}
			if len(out) != tc.want {
				t.Fatalf("decoded %d entries, want %d", len(out), tc.want)
			}
			if tc.want > 0 && out[0] != tc.first {
				t.Errorf("first entry = %+v, want %+v", out[0], tc.first)
			}
		})
	}
}

// TestGraphQLResponseCarryingErrorsIsNotSuccess is the whole point of the envelope:
// Garmin answers 200 with an errors array, and reporting that as success would hand
// a caller an empty calendar as if it were the truth.
func TestGraphQLResponseCarryingErrorsIsNotSuccess(t *testing.T) {
	t.Parallel()

	const synthetic = "no plan for tester@example.com"
	caller := &stubCaller{outcomes: []stubOutcome{{
		header: jsonHeader(),
		body: []byte(`{"data":{"workoutScheduleSummariesScalar":[]},"errors":` +
			`[{"message":"` + synthetic + `","path":["workoutScheduleSummariesScalar"]}]}`),
	}}}
	c := newTestClient(t, client.Limits{})

	var out []struct{}
	_, err := c.GraphQL(t.Context(), mustSession(t, caller),
		scheduleSummariesRequest(windowStart), &out)
	if !errors.Is(err, client.ErrGraphQLErrors) {
		t.Fatalf("GraphQL() = %v, want ErrGraphQLErrors", err)
	}
	if strings.Contains(err.Error(), synthetic) || strings.Contains(err.Error(), "example.com") {
		t.Errorf("the rendered failure quotes the upstream message: %q", err.Error())
	}
	if caller.calls() != 1 {
		t.Errorf("dispatched %d times, want 1: a reported GraphQL error is deterministic",
			caller.calls())
	}
}

// TestGraphQLReadIsRepeatedLikeARead pins the retry classification. A GraphQL query
// is a POST only because the document travels in the body; it names a query root
// field from a closed set and can change nothing, so a 503 is retried exactly as a
// GET read would be.
func TestGraphQLReadIsRepeatedLikeARead(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{
		{status: http.StatusServiceUnavailable, header: jsonHeader(), body: []byte(`{}`)},
		{header: jsonHeader(), body: []byte(`{"data":{"workoutScheduleSummariesScalar":[]}}`)},
	}}
	c := newTestClient(t, client.Limits{MaxAttempts: 3})

	var out []struct{}
	if _, err := c.GraphQL(t.Context(), mustSession(t, caller),
		scheduleSummariesRequest(windowStart), &out); err != nil {
		t.Fatalf("GraphQL() = %v", err)
	}
	if caller.calls() != 2 {
		t.Errorf("dispatched %d times, want 2", caller.calls())
	}
}

// TestGraphQLRefusesAnUnusableRequestBeforeDispatch keeps a rejected document off
// the wire entirely.
func TestGraphQLRefusesAnUnusableRequestBeforeDispatch(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{header: jsonHeader(), body: []byte(`{}`)}}}
	c := newTestClient(t, client.Limits{})

	var out []struct{}
	_, err := c.GraphQL(t.Context(), mustSession(t, caller),
		scheduleSummariesRequest(`2026") { x } q{y(a:"`), &out)
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("GraphQL() = %v, want ErrValidation", err)
	}
	if caller.calls() != 0 {
		t.Errorf("dispatched %d times, want 0", caller.calls())
	}
}

// TestGraphQLRefusesAnUnusableSession keeps an unprincipled call off the wire.
func TestGraphQLRefusesAnUnusableSession(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, client.Limits{})
	var out []struct{}
	_, err := c.GraphQL(t.Context(), client.Session{},
		scheduleSummariesRequest(windowStart), &out)
	if !errors.Is(err, client.ErrMissingPrincipal) {
		t.Fatalf("GraphQL() = %v, want ErrMissingPrincipal", err)
	}
}

// TestGraphQLReportsAMalformedEnvelope covers a body that is not JSON at all.
func TestGraphQLReportsAMalformedEnvelope(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{
		header: jsonHeader(),
		body:   []byte(`<html>gateway</html>`),
	}}}
	c := newTestClient(t, client.Limits{})

	var out []struct{}
	_, err := c.GraphQL(t.Context(), mustSession(t, caller),
		scheduleSummariesRequest(windowStart), &out)
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Fatalf("GraphQL() = %v, want ErrMalformedPayload", err)
	}
}
