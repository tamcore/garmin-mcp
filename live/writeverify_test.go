//go:build garminlive

package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// This file is how the write guard learns that an object Garmin named is an object
// this suite made.
//
// The identifier in a create response is not that evidence. It is a number a service
// chose, and a service that deduplicates, answers from a cache or simply drifts can
// choose one that names an object the suite never created — after which the guard
// would let a test mutate and delete it, which for a create-then-mutate tool happens
// inside a single call. What is required instead is a binding between the create this
// suite sent and the object that identifier now names: the name goes out in the create
// request, and the object is read back and must carry it.

// nameProbeLimit bounds both bodies this file reads: the create request whose name is
// extracted, and the read-back whose name is compared. A create body is a workout
// document at worst, and this is the same ceiling the create response is read under.
const nameProbeLimit = maxSniffBytes

// headerContentType is dropped from the read-back, which carries no body.
const headerContentType = "Content-Type"

// sentName reads the name this suite put in one create request, or "" when the request
// carries none the guard can read.
//
// The bytes come from the request that is about to be dispatched rather than from
// anything a test declared, and takeBody puts them back untouched, so reading the name
// cannot change what is sent.
func sentName(kind ownedKind, req *http.Request) string {
	field := kind.nameField()
	if field == "" {
		return ""
	}
	raw, read := takeBody(req, nameProbeLimit)
	if !read {
		return ""
	}
	return nameIn(raw, "", field)
}

// storedName reads the name Garmin serves for one object, or reports why it could not.
//
// The read is a GET built from the create's own URL, so it addresses the same host and
// the same deployment, and it carries the create's headers so the request layer's own
// negotiation still applies. It goes through the inner caller, which is what attaches
// the credential; the guard is not re-entered, and a GET would pass it anyway.
//
// A failure here is deliberately not fatal to the run and deliberately not ownership
// either. The object stays unowned, so nothing may mutate or delete it — and if it was
// in fact this suite's, it carries this suite's prefix and the next run's sweeper
// removes it. Refusing to guess is the whole point: the alternative is guessing about
// somebody's data.
func (c writeCaller) storedName(
	ctx context.Context, principal string, kind ownedKind, id int64, model *http.Request,
) (string, error) {
	probe, err := c.readBackRequest(ctx, kind, id, model)
	if err != nil {
		return "", err
	}

	resp, err := c.inner.Do(ctx, principal, probe)
	if err != nil {
		return "", fmt.Errorf("reading the created %s back: %w", kind, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("reading the created %s back answered with status %d",
			kind, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, nameProbeLimit+1))
	if err != nil || int64(len(raw)) > nameProbeLimit {
		return "", fmt.Errorf("the read-back of the created %s could not be read", kind)
	}
	return nameIn(raw, resp.Header.Get(headerContentEncoding), kind.nameField()), nil
}

// readBackRequest builds the per-object GET for one created identifier.
func (c writeCaller) readBackRequest(
	ctx context.Context, kind ownedKind, id int64, model *http.Request,
) (*http.Request, error) {
	path := kind.itemPath(id)
	if path == "" || model.URL == nil {
		return nil, fmt.Errorf("no read-back is defined for a %s", kind)
	}

	target := *model.URL
	target.Path = path
	target.RawQuery = ""
	target.Fragment = ""

	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building the read-back of the created %s: %w", kind, err)
	}
	probe.Header = model.Header.Clone()
	probe.Header.Del(headerContentType)
	return probe, nil
}

// nameIn reads one string field out of a JSON object, or "" when the document is not
// one, does not carry the field, or does not carry it as a string.
func nameIn(body []byte, encoding, field string) string {
	document, err := decompressed(body, encoding)
	if err != nil {
		return ""
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return ""
	}
	raw, present := fields[field]
	if !present {
		return ""
	}

	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return ""
	}
	return name
}
