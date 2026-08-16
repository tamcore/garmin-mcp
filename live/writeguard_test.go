//go:build garminlive

package live

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// maxSniffBytes bounds how much of a create response the guard reads in order to
// learn the identifier Garmin assigned. A create response is a small JSON object;
// anything larger is refused rather than truncated, because a truncated body handed
// back to the request layer would be a corrupt response.
const maxSniffBytes = 1 << 20

// headerContentEncoding is the response header that says whether the body arrives
// compressed. The request layer asks for gzip explicitly, so the guard has to be
// able to read a compressed create response.
const headerContentEncoding = "Content-Encoding"

// encodingGzip is the one content encoding this suite has to decode.
const encodingGzip = "gzip"

// A mutation is what one non-read request would do, expressed in terms of the
// ownership ledger.
//
// needs names the object that must already be owned. creates names the class of
// object the request brings into existence, whose identifier the guard learns from
// Garmin's own answer. A schedule POST does both: it needs an owned workout and it
// creates a calendar entry.
type mutation struct {
	needs   bool
	kind    ownedKind
	id      int64
	creates ownedKind
}

// writeCaller is what makes the write half of this suite safe by construction.
//
// It is the write analogue of readOnlyCaller. A read passes through unchanged. A
// mutation passes only when the guard recognises the path *and* the object it
// targets is one this suite created; anything else is refused before the inner
// caller is reached, so no such request leaves this process. The recognised set is
// an allowlist, so a mutating endpoint this guard has never heard of is refused
// rather than waved through.
//
// Ownership is learned from Garmin rather than declared by a test. When a create
// succeeds the guard reads the identifier out of the response body and then reads that
// object back, admitting it only when the object reports that same identifier and
// carries the name the create sent — which is why a tool that creates and then
// immediately mutates its own creation, create_strength_training_activity does exactly
// that, passes without any test being trusted to register anything, and why an
// identifier Garmin named for some other object does not.
type writeCaller struct {
	inner client.Caller
	owned *ownedObjects
	foods *foodLedger
}

// Do applies the guard and then dispatches.
func (c writeCaller) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	if isReadRequest(req) {
		return c.inner.Do(ctx, principal, req)
	}
	if resp, err, handled := c.doNutrition(ctx, principal, req); handled {
		return resp, err
	}

	target, recognised := classifyMutation(req)
	if !recognised {
		return nil, fmt.Errorf(
			"live: refusing a %s request to %s: this suite mutates only the endpoints its "+
				"write guard recognises", req.Method, req.URL.Path)
	}
	if target.needs && !c.owned.owns(target.kind, target.id) {
		return nil, fmt.Errorf(
			"live: refusing a %s request against a %s this suite did not create: a write test "+
				"may only ever mutate an object it created itself", req.Method, target.kind)
	}

	// The name is taken from the request before it is dispatched, because it is the
	// half of the binding this suite controls: what comes back is only an identifier.
	sent := ""
	if target.creates != 0 {
		sent = sentName(target.creates, req)
	}

	resp, err := c.inner.Do(ctx, principal, req)
	if err != nil || target.creates == 0 {
		return resp, err
	}
	return c.adopt(ctx, principal, target.creates, sent, req, resp)
}

// adopt records the object a successful create produced, once Garmin's own answer for
// that object proves it is the one this suite just asked for.
//
// The response body is read here and handed back verbatim, compressed or not, so the
// request layer sees exactly what Garmin sent. The identifier in it is then read back:
// the object at that identifier must report that same identifier and must carry the name
// this create sent. Nothing else is evidence, and an identifier alone certainly is not —
// see ownCreated.
//
// Not owning is not an error on its own. A calendar create reports no identifier at
// all, and a read-back that fails or disagrees leaves an object this suite may not
// touch, which is the safe direction: every later mutation of it is refused, and a
// leftover of this suite's own is removed by the next run's sweeper.
func (c writeCaller) adopt(
	ctx context.Context, principal string, kind ownedKind, sent string,
	req *http.Request, resp *http.Response,
) (*http.Response, error) {
	if resp == nil || resp.Body == nil || resp.StatusCode >= http.StatusMultipleChoices {
		return resp, nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSniffBytes+1))
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("live: reading the create response so the created %s can be "+
			"owned and removed: %w", kind, err)
	}
	if len(raw) > maxSniffBytes {
		return nil, fmt.Errorf("live: the create response for a %s is larger than this suite "+
			"reads, so the created object could not be owned", kind)
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))

	id, found := createdID(kind, raw, resp.Header.Get(headerContentEncoding))
	if !found || sent == "" {
		return resp, nil
	}
	stored, storedID, err := c.storedObject(ctx, principal, kind, id, req)
	if err != nil {
		suiteLogger().Warn(
			"live: a created object could not be read back, so it is not owned and will not "+
				"be touched",
			slog.String("kind", kind.String()), slog.String("reason", safeError(err)))
		return resp, nil
	}

	c.owned.ownCreated(kind, createdObject{
		id: id, sent: sent, stored: stored, storedID: storedID,
	})
	return resp, nil
}

// classifyMutation maps one non-read request onto what it would do, and reports
// whether the guard recognises it at all.
func classifyMutation(req *http.Request) (mutation, bool) {
	if req.URL == nil {
		return mutation{}, false
	}
	switch req.Method {
	case http.MethodPost:
		return classifyCreate(req.URL.Path)
	case http.MethodPut:
		return classifyUpdate(req.URL.Path)
	case http.MethodDelete:
		return classifyDelete(req.URL.Path)
	default:
		return mutation{}, false
	}
}

// classifyCreate recognises the three POSTs this suite performs: an activity
// create, a workout create, and a calendar entry for an already-owned workout.
func classifyCreate(path string) (mutation, bool) {
	switch path {
	case client.PathActivityPrefix:
		return mutation{creates: kindActivity}, true
	case client.PathWorkoutPrefix:
		return mutation{creates: kindWorkout}, true
	}
	if id, ok := idAfter(path, client.PathWorkoutSchedule); ok {
		return mutation{needs: true, kind: kindWorkout, id: id, creates: kindSchedule}, true
	}
	return mutation{}, false
}

// classifyUpdate recognises the per-activity PUTs — every metadata write and the
// strength-set replacement share the activity path — the in-place workout PUT, and
// the two gear links.
//
// A gear link is guarded on the *activity*, not on the gear: linking changes the
// activity's association and leaves the gear item itself untouched, so an activity
// this suite created is the whole permission the write needs.
func classifyUpdate(path string) (mutation, bool) {
	// The strength-set replacement is the activity path with one extra segment, and
	// trimming it folds both shapes into the same lookup.
	base := strings.TrimSuffix(path, "/"+client.SegmentExerciseSets)
	if id, ok := idAfter(base, client.PathActivityPrefix); ok {
		return mutation{needs: true, kind: kindActivity, id: id}, true
	}
	if id, ok := idAfter(path, client.PathWorkoutPrefix); ok {
		return mutation{needs: true, kind: kindWorkout, id: id}, true
	}
	if id, ok := gearLinkActivity(path); ok {
		return mutation{needs: true, kind: kindActivity, id: id}, true
	}
	return mutation{}, false
}

// gearLinkActivity reads the activity out of a gear link or unlink path, which is
// "<gear prefix>/{link|unlink}/{uuid}/activity/{id}".
//
// The shape is matched exactly — four segments, a known action, the literal activity
// segment — so no other path under the gear prefix is admitted by accident.
func gearLinkActivity(path string) (int64, bool) {
	rest, found := strings.CutPrefix(path, client.PathGearPrefix+"/")
	if !found {
		return 0, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || parts[2] != "activity" {
		return 0, false
	}
	if parts[0] != "link" && parts[0] != "unlink" {
		return 0, false
	}
	return positiveID(parts[3])
}

// classifyDelete recognises the three removals: an activity, a workout template,
// and one calendar entry.
func classifyDelete(path string) (mutation, bool) {
	if id, ok := idAfter(path, client.PathActivityPrefix); ok {
		return mutation{needs: true, kind: kindActivity, id: id}, true
	}
	if id, ok := idAfter(path, client.PathWorkoutPrefix); ok {
		return mutation{needs: true, kind: kindWorkout, id: id}, true
	}
	if id, ok := idAfter(path, client.PathWorkoutSchedule); ok {
		return mutation{needs: true, kind: kindSchedule, id: id}, true
	}
	return mutation{}, false
}

// idAfter reads the positive identifier that is the *only* remaining segment after
// prefix.
//
// Requiring a single segment is what keeps the catalog and download paths out: a PUT
// to the activity-type catalog leaves a non-numeric segment, and a delete under the
// workout FIT prefix leaves two segments. Both fail here and are therefore refused.
func idAfter(path, prefix string) (int64, bool) {
	rest, found := strings.CutPrefix(path, prefix+"/")
	if !found || rest == "" || strings.Contains(rest, "/") {
		return 0, false
	}
	return positiveID(rest)
}

// positiveID parses one path segment as a positive identifier.
func positiveID(segment string) (int64, bool) {
	id, err := strconv.ParseInt(segment, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// createdID reads the identifier Garmin assigned out of a create response.
//
// The value is accepted as a JSON number and as a quoted number, because Garmin
// sends both depending on the endpoint and the deployment, which is why the domain
// models decode it through a union too.
func createdID(kind ownedKind, body []byte, encoding string) (int64, bool) {
	field := kind.createdField()
	if field == "" {
		return 0, false
	}
	document, err := decompressed(body, encoding)
	if err != nil {
		return 0, false
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return 0, false
	}
	raw, present := fields[field]
	if !present {
		return 0, false
	}

	id, err := strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// decompressed returns the body as JSON, decoding the one content encoding the
// request layer asks Garmin for.
func decompressed(body []byte, encoding string) ([]byte, error) {
	if !strings.EqualFold(strings.TrimSpace(encoding), encodingGzip) {
		return body, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("live: the create response declared gzip and is not: %w", err)
	}
	defer func() { _ = reader.Close() }()

	return io.ReadAll(io.LimitReader(reader, maxSniffBytes))
}
