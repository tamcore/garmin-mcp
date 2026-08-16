//go:build garminlive

package live

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The two tests here are pure: they drive both guards against a stub that answers in
// this process, and they reach neither Garmin nor the shared session. They exist
// because each covers a hole a guard test built with http.NewRequest cannot express —
// one where the request carries two different bodies, and one where the service answers
// a create with an identifier that is not the created object's.

// createdProbeID is the identifier the stub reports for a create. It is a literal and
// no identifier of the account's.
const createdProbeID int64 = 4242

// TestReadGuardJudgesTheBytesItDispatches covers the difference between the body an
// *http.Request will send and the copy it can replay.
//
// Body and GetBody are independent fields. A guard that judged GetBody would inspect
// one document while a different one left the process, and the exploit writes itself:
// the benign query goes in the replay copy and the mutation goes in the body. This
// sends exactly that request, and additionally proves the admitted case still arrives
// intact — a guard that consumed the body it judged would break every calendar read.
func TestReadGuardJudgesTheBytesItDispatches(t *testing.T) {
	t.Parallel()

	const mutation = `{"query":"mutation{deleteWorkout(workoutId:\"1\")}"}`
	query := knownQueryBody(t)

	refused := &recordingCaller{}
	hidden := graphQLProbe(t, mutation)
	hidden.GetBody = replayOf(query)
	if _, err := (&readOnlyCaller{inner: refused}).Do(
		t.Context(), livePrincipal, hidden); err == nil {
		t.Error("the read-only guard admitted a mutation whose replay copy was a query")
	}
	if refused.calls != 0 {
		t.Error("a mutation reached the transport because the guard judged the replay copy")
	}

	admitted := &recordingCaller{}
	shown := graphQLProbe(t, query)
	shown.GetBody = replayOf(mutation)
	if _, err := (&readOnlyCaller{inner: admitted}).Do(
		t.Context(), livePrincipal, shown); err != nil {
		t.Fatalf("the read-only guard refused the calendar query: %v", err)
	}
	if len(admitted.bodies) != 1 || string(admitted.bodies[0]) != query {
		t.Error("the query the guard judged is not the one that reached the transport")
	}
}

// TestWriteGuardOwnsOnlyACreateItReadsBack covers what a create response is evidence
// of, which is less than it looks.
//
// The identifier is a number the service chose. Deduplication, a cache or plain drift
// can make it name an object this suite never created, and the guard would then permit
// mutating and deleting it — inside a single call, for a tool that creates and then
// writes to its own creation. Ownership therefore requires the object at that identifier
// to report that identifier *and* to carry the name the create sent, and this drives
// every answer: a foreign name, a foreign identifier under the right name, a read-back
// naming nothing, and the one that must still be admitted.
//
// The foreign-identifier case is not a duplicate of the foreign-name one. A generated
// name carries a one-second run stamp and a per-run counter, so two runs of this suite
// starting inside the same second render byte-identical names: the object a drifted
// identifier names can carry exactly the name that was sent while being the other run's.
// Only the identifier comparison separates those two, which is why it is not enough for
// the read-back to look right.
func TestWriteGuardOwnsOnlyACreateItReadsBack(t *testing.T) {
	t.Parallel()

	sent := objectPrefix + string(labelNameWorkout) + "-" +
		strconv.FormatInt(nameStampFloor().Unix(), 10) + "-1"
	item := client.PathWorkoutPrefix + "/" + strconv.FormatInt(createdProbeID, 10)

	refused := map[string]*recordingCaller{
		"an object carrying somebody else's name": {
			created: createdProbeID, stored: "someone else's workout", storedID: createdProbeID,
		},
		"an object reporting a different identifier under the name that was sent": {
			created: createdProbeID, stored: sent, storedID: createdProbeID + 1,
		},
		"an object reporting no identifier at all": {
			created: createdProbeID, stored: sent,
		},
	}
	for label, caller := range refused {
		owned := newOwnedObjects()
		createWorkoutProbe(t, writeCaller{inner: caller, owned: owned}, sent)

		if owned.owns(kindWorkout, createdProbeID) {
			t.Errorf("the guard owned an identifier whose read-back answered with %s", label)
		}
		if err := writeProbe(t, writeCaller{inner: caller, owned: owned},
			http.MethodDelete, item); err == nil {
			t.Errorf("the guard then admitted a delete of an object read back as %s", label)
		}
	}

	matching := &recordingCaller{
		created: createdProbeID, stored: sent, storedID: createdProbeID,
	}
	proven := newOwnedObjects()
	createWorkoutProbe(t, writeCaller{inner: matching, owned: proven}, sent)

	if !proven.owns(kindWorkout, createdProbeID) {
		t.Fatal("the guard refused a create whose object reports the identifier it addressed " +
			"and the name it sent, so every write test would be refused")
	}
	if !matching.sawPath(item) {
		t.Error("the guard recorded ownership without reading the created workout back")
	}
}

// createWorkoutProbe pushes one workout create at the write guard.
func createWorkoutProbe(t *testing.T, guard writeCaller, name string) {
	t.Helper()

	body := `{"workoutName":"` + name + `"}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"https://connectapi.garmin.com"+client.PathWorkoutPrefix, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the create probe: %v", err)
	}
	if _, err := guard.Do(t.Context(), livePrincipal, req); err != nil {
		t.Fatalf("the write guard refused a create: %v", err)
	}
}

// graphQLProbe builds one POST to the GraphQL path carrying body.
func graphQLProbe(t *testing.T, body string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"https://connectapi.garmin.com"+client.PathGraphQL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the GraphQL probe: %v", err)
	}
	return req
}

// replayOf returns a GetBody that hands out a different document than the request's
// own body, which is the shape neither guard may be fooled by.
func replayOf(body string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(body)), nil
	}
}

// recordingCaller answers a create with an identifier and a read-back with the object
// that identifier names, and records what reached it. It contacts nothing.
//
// The read-back answer carries an identifier as well as a name, because the real one
// does and because a fixture that omitted it would make every identifier comparison
// vacuous: an absent field decodes to no identifier, which the ledger refuses, so a
// missing-id fixture cannot distinguish a guard that compares identifiers from one that
// does not. storedID of zero is the deliberate exception, and it renders an answer with
// no identifier field at all.
type recordingCaller struct {
	mu       sync.Mutex
	created  int64
	stored   string
	storedID int64

	calls  int
	paths  []string
	bodies [][]byte
}

func (c *recordingCaller) Do(
	_ context.Context, _ string, req *http.Request,
) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++
	c.paths = append(c.paths, req.URL.Path)
	body := []byte(nil)
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	c.bodies = append(c.bodies, body)

	answer := `{"workoutName":"` + c.stored + `"}`
	if c.storedID != 0 {
		answer = `{"workoutId":` + strconv.FormatInt(c.storedID, 10) +
			`,"workoutName":"` + c.stored + `"}`
	}
	if req.Method == http.MethodPost {
		answer = `{"workoutId":` + strconv.FormatInt(c.created, 10) + `}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(answer)),
	}, nil
}

// sawPath reports whether one path reached the caller.
func (c *recordingCaller) sawPath(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, seen := range c.paths {
		if seen == path {
			return true
		}
	}
	return false
}
