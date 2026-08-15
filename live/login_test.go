//go:build garminlive

package live

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// errProbeReachedTransport is returned by the probe caller that must never be
// reached.
var errProbeReachedTransport = errors.New("live: a write probe reached the transport")

// TestLiveLoginReachesAnAuthenticatedSessionAndAValidatedAccount is the drift
// detector AGENTS.md names as the purpose of this layer.
//
// It asserts four things about the one real login the suite performs: a strategy in
// the declared fallback chain succeeded, the DI ticket exchange produced a token set
// the request layer can use, the API tier accepted that session, and the profile the
// session reads names an account. A Garmin-side change to the SSO flow, the DI
// exchange or the profile endpoint breaks one of those, which is exactly the signal
// this layer exists to raise.
//
// It names no value: the strategy is one of three compiled-in constants, and the
// display name is asserted for presence and never rendered.
func TestLiveLoginReachesAnAuthenticatedSessionAndAValidatedAccount(t *testing.T) {
	e := liveEnv(t)

	strategies := auth.Strategies()
	names := make([]string, 0, len(strategies))
	for _, strategy := range strategies {
		names = append(names, strategy.String())
	}
	if !slices.Contains(names, e.strategy) {
		t.Fatalf("the login succeeded through %q, which is not in the declared chain %v",
			e.strategy, names)
	}
	if e.strategy != auth.StrategyMobileIOS.String() {
		t.Logf("drift signal: the login needed the %q fallback rather than %q",
			e.strategy, auth.StrategyMobileIOS)
	}

	name, err := e.profile.DisplayName(t.Context(), e.session)
	if err != nil {
		t.Fatalf("the authenticated session could not read the social profile: %v", err)
	}
	if name.IsZero() {
		t.Fatal("the social profile named no display name, so no per-account read can address the account")
	}
}

// TestLiveSessionSurvivesASecondRequest proves the stored token set is reusable
// rather than single-use: a second read on the same session must not need a fresh
// login. A Garmin change that invalidated a DI token after one use would fail here
// and nowhere else, because every fake serves an unlimited token.
func TestLiveSessionSurvivesASecondRequest(t *testing.T) {
	e := liveEnv(t)

	first, err := e.profile.DisplayName(t.Context(), e.session)
	if err != nil {
		t.Fatalf("the first read failed: %v", err)
	}
	second, err := e.profile.DisplayName(t.Context(), e.session)
	if err != nil {
		t.Fatalf("the second read on the same session failed: %v", err)
	}
	if first.Value() != second.Value() {
		t.Error("two reads on one session named two different accounts")
	}
}

// TestLiveCallerRefusesAnythingButARead pins the read-only construction of this
// suite.
//
// The guard is what makes "no test mutates the account" a property of the wiring
// rather than a promise about the tests, so the guard itself is tested: one that
// silently stopped refusing would leave every later slice free to write. The request
// below is never dispatched — it is refused before the inner caller is reached — so
// this test performs no live traffic of its own.
func TestLiveCallerRefusesAnythingButARead(t *testing.T) {
	e := liveEnv(t)

	inner := &countingCaller{}
	guard := readOnlyCaller{inner: inner}

	writePath := client.PathActivityPrefix + "/1"
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		err := probe(t, guard, method, writePath, "")
		if err == nil {
			t.Errorf("the read-only caller accepted a %s request to a write path", method)
		} else if !strings.Contains(err.Error(), "read-only") {
			t.Errorf("the %s refusal does not name the read-only rule", method)
		}
	}
	if inner.reached != 0 {
		t.Errorf("%d write probes passed the guard and reached the transport", inner.reached)
	}

	// The one allowed POST is a GraphQL query the workout calendar needs. It must
	// still pass, or the calendar reads in this suite would be proving nothing.
	if err := probe(t, guard, http.MethodPost, client.PathGraphQL,
		knownQueryBody(t)); err != errProbeReachedTransport {
		t.Errorf("the guard refused the GraphQL read: %v", err)
	}

	// The live session must be usable, or the whole suite would be asserting nothing.
	if e.session.IsZero() {
		t.Fatal("the live session is unusable, so no read reached Garmin through the guard")
	}
}

// TestLiveCallerRefusesAGraphQLMutation is the other half of the read-only guard.
//
// The whole GraphQL surface sits behind one path, so a guard that judged method and
// path would admit every mutation the gateway exposes, now and after any drift. Each
// body below reaches the same path with the same method as the calendar read and must
// still be refused. Nothing is dispatched.
func TestLiveCallerRefusesAGraphQLMutation(t *testing.T) {
	liveEnv(t)

	inner := &countingCaller{}
	guard := readOnlyCaller{inner: inner}

	for name, body := range map[string]string{
		"a mutation document":         `{"query":"mutation{deleteWorkout(workoutId:\"1\")}"}`,
		"an unknown query field":      `{"query":"query{deleteEverythingScalar(id:\"1\")}"}`,
		"a query with a second field": `{"query":"query{workoutScheduleSummariesScalar}"}`,
		"an operation name beside it": `{"query":"query{workoutScheduleSummariesScalar(a:\"b\")}","operationName":"x"}`,
		"a body that is not JSON":     `not json`,
		"an empty body":               "",
	} {
		if err := probe(t, guard, http.MethodPost, client.PathGraphQL, body); err == nil {
			t.Errorf("the read-only caller accepted %s on the GraphQL path", name)
		} else if !strings.Contains(err.Error(), "read-only") {
			t.Errorf("the refusal of %s does not name the read-only rule", name)
		}
	}
	if inner.reached != 0 {
		t.Errorf("%d GraphQL probes passed the guard and reached the transport", inner.reached)
	}
}

// knownQueryBody renders the calendar query the request layer itself produces, so the
// admitted case is the real document rather than one this test authored.
func knownQueryBody(t *testing.T) string {
	t.Helper()

	document, err := client.GraphQLRequest{
		Op:       client.OpGetScheduledWorkouts,
		Endpoint: client.EndpointGraphQL,
		Field:    client.GraphQLFieldWorkoutScheduleSummaries,
		Arguments: []client.GraphQLArgument{
			{Name: client.GraphQLArgStartDate, Value: "2026-01-01"},
			{Name: client.GraphQLArgEndDate, Value: "2026-01-07"},
		},
	}.Document()
	if err != nil {
		t.Fatalf("rendering the calendar query document: %v", err)
	}

	body, err := json.Marshal(map[string]string{"query": document})
	if err != nil {
		t.Fatalf("encoding the calendar query body: %v", err)
	}
	return string(body)
}

// probe pushes one request at the guard and reports what came back. Nothing is
// dispatched: the inner caller never performs a request.
//
// A body is supplied through http.NewRequest with a *strings.Reader, which is what
// sets GetBody — the same seam internal/garmin/client relies on, so the guard here
// reads a request shaped exactly like a real one.
func probe(t *testing.T, guard readOnlyCaller, method, path, body string) error {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(
		t.Context(), method, "https://connectapi.garmin.com"+path, reader)
	if err != nil {
		t.Fatalf("building the %s probe: %v", method, err)
	}
	_, err = guard.Do(t.Context(), livePrincipal, req)
	return err
}

// countingCaller stands behind the guard in TestLiveCallerRefusesAnythingButARead
// and counts what reached it, so a guard that let a write past is caught here rather
// than at Garmin. It dispatches nothing.
type countingCaller struct {
	reached int
}

func (c *countingCaller) Do(_ context.Context, _ string, _ *http.Request) (*http.Response, error) {
	c.reached++
	return nil, errProbeReachedTransport
}
