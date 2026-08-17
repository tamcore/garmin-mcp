//go:build garminlive

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// This file is course-service's own share of the write guard, split out for the same
// reason foodguard_test.go and weighinguard_test.go are: it is the one class whose
// ownership evidence does not fit the generic path writeguard_test.go's adopt and
// writeverify_test.go's storedObject give every other class.
//
// A course's create response reports courseId and courseName at the top level exactly
// the way an activity or a workout does, so kindCourse (owned_test.go) is a plain
// ownedKind and reuses ownedObjects, classifyDelete and remove/removeOutstanding without
// its own ledger. What it cannot reuse is storedObject's read-back: Garmin's
// course-service exposes no per-course GET, only the list (courses.go's GetCourses) and
// the delete, so kindCourse.itemPath is "" and a create routed through the generic
// adopt() would always fail its read-back and never be owned. The create is therefore
// intercepted here, before classifyMutation ever sees it, and its read-back is a list
// search for the name this suite sent — the same shape foodguard_test.go's
// adoptCustomFood already uses for a custom food, which has no per-item GET either.

// doCourse handles the course-service surface's two POSTs, and reports whether it
// recognised the request at all. It is consulted before classifyMutation, alongside
// doNutrition and doWeighIn, and it is the only place a course create is adopted.
func (c writeCaller) doCourse(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error, bool) {
	if req.URL == nil {
		return nil, nil, false
	}
	switch {
	case req.Method == http.MethodPost && req.URL.Path == client.PathCourseImport:
		// The import step only parses an uploaded GPX file into geo points; it creates
		// no persistent object and its response names no identifier, so it needs
		// neither an ownership check on the way in nor an adoption on the way out.
		resp, err := c.inner.Do(ctx, principal, req)
		return resp, err, true
	case req.Method == http.MethodPost && req.URL.Path == client.PathCourseBase:
		resp, err := c.courseCreate(ctx, principal, req)
		return resp, err, true
	default:
		return nil, nil, false
	}
}

// courseCreate performs the course-save POST and then adopts the course it created.
func (c writeCaller) courseCreate(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	raw, read := takeBody(req, nameProbeLimit)
	if !read {
		return nil, fmt.Errorf("live: refusing a course create whose body could not be read")
	}
	sent := nameIn(raw, "", kindCourse.nameField())

	resp, err := c.inner.Do(ctx, principal, req)
	if err != nil {
		return resp, err
	}
	return c.adoptCourse(ctx, principal, sent, req, resp)
}

// adoptCourse records a created course, once a search read-back for its own name
// confirms both the identifier Garmin's create response claimed and the name this
// create sent.
//
// A failure to verify leaves the object unowned rather than failing the run, the same
// choice adopt and adoptCustomFood make: it still carries this suite's reserved prefix,
// so the next run's sweeper (writesweep_test.go's sweepCourses) finds it by name even
// though this run cannot remove it.
func (c writeCaller) adoptCourse(
	ctx context.Context, principal, sent string, model *http.Request, resp *http.Response,
) (*http.Response, error) {
	if resp == nil || resp.Body == nil || resp.StatusCode >= http.StatusMultipleChoices || sent == "" {
		return resp, nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSniffBytes+1))
	_ = resp.Body.Close()
	if err != nil || len(raw) > maxSniffBytes {
		return nil, fmt.Errorf(
			"live: reading the course create response so the created course can be owned and removed")
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))

	encoding := resp.Header.Get(headerContentEncoding)
	created, found := createdID(kindCourse, raw, encoding)
	if !found {
		return resp, nil
	}

	storedID, storedName, err := c.storedCourse(ctx, principal, sent, model)
	if err != nil {
		suiteLogger().Warn(
			"live: a created course could not be read back, so it is not owned and will not "+
				"be touched",
			slog.String("reason", safeError(err)))
		return resp, nil
	}
	c.owned.ownCreated(kindCourse, createdObject{
		id: created, sent: sent, stored: storedName, storedID: storedID,
	})
	return resp, nil
}

// storedCourse searches the account's own course listing for name and returns the
// identifier and name Garmin serves for the matching entry.
//
// The list is read whole, the same way storedCustomFood's search and sweepCourses'
// leftover pass both are: course-service's GetCourses (courses.go) takes no filter of
// its own, so there is no narrower read to ask for.
func (c writeCaller) storedCourse(
	ctx context.Context, principal, name string, model *http.Request,
) (id int64, storedName string, err error) {
	if model.URL == nil {
		return 0, "", fmt.Errorf("no read-back request model is available")
	}

	target := *model.URL
	target.Path = client.PathCourseBase
	target.RawQuery = ""
	target.Fragment = ""

	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return 0, "", fmt.Errorf("building the course read-back: %w", err)
	}
	probe.Header = model.Header.Clone()
	probe.Header.Del(headerContentType)

	resp, err := c.inner.Do(ctx, principal, probe)
	if err != nil {
		return 0, "", fmt.Errorf("reading the created course back: %s", safeError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusMultipleChoices {
		return 0, "", fmt.Errorf("reading the created course back answered with status %d",
			resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSniffBytes+1))
	if err != nil || int64(len(raw)) > maxSniffBytes {
		return 0, "", fmt.Errorf("the course read-back could not be read")
	}

	document, err := decompressed(raw, resp.Header.Get(headerContentEncoding))
	if err != nil {
		return 0, "", fmt.Errorf("the course read-back is not readable")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(document, &items); err != nil {
		return 0, "", fmt.Errorf("the course read-back is not a course list")
	}
	for _, item := range items {
		itemName := nameIn(item, "", kindCourse.nameField())
		if itemName == "" || itemName != name {
			continue
		}
		if itemID, ok := createdID(kindCourse, item, ""); ok {
			return itemID, itemName, nil
		}
	}
	return 0, "", fmt.Errorf("no matching course was found in the read-back")
}
