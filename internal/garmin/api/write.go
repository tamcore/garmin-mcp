package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// MaxRequestBodyBytes bounds a request body this package will send. A workout
// document and a strength set list are both caller-supplied, so they need a
// ceiling of their own: the response bounds of the request layer say nothing
// about what a caller may push upstream.
const MaxRequestBodyBytes = 1 << 20

// MaxTextLen bounds a free-text write field — an activity name, a description.
// Garmin truncates silently past its own limit, and an unbounded string is a log
// and memory hazard before it ever reaches Garmin.
const MaxTextLen = 1024

// writeRequest builds a write request. Every write names its effect explicitly,
// because that is what the request layer's retry predicate reads before it may
// repeat anything: EffectIdempotentWrite may be repeated, EffectUnsafeWrite and
// EffectDelete never are.
func writeRequest(
	op client.Op, endpoint client.Endpoint, method, path string, effect client.Effect,
) client.Request {
	return client.Request{
		Op:       op,
		Endpoint: endpoint,
		Method:   method,
		Path:     path,
		Effect:   effect,
	}
}

// write dispatches a write and decodes whatever useful document came back.
//
// A Garmin write answers with the updated object, with an empty body, or with a
// 204. All three are success; only a body that claims to be JSON is decoded, so
// a plain-text acknowledgement cannot turn an applied write into a decode
// failure.
func (r requester) write(
	ctx context.Context, session client.Session, req client.Request, out any,
) (client.Payload, error) {
	if session.IsZero() {
		return client.Payload{}, invalid(req, client.ErrMissingPrincipal)
	}
	if len(req.Body) > MaxRequestBodyBytes {
		return client.Payload{}, invalid(req, fmt.Errorf(
			"%w: request body exceeds its bound", client.ErrValidation))
	}

	payload, err := r.rc.Do(ctx, session, req)
	if err != nil {
		return payload, err
	}
	if out == nil {
		return payload, nil
	}
	return payload, decodeIfJSON(payload, out)
}

// WriteResult is what an ordinary write reports: the HTTP status Garmin
// answered with and the retained raw payload, which is bounded, sealed and
// unloggable. The body of a Garmin write is the updated object, so the result is
// sensitive for the same reason the read models are.
type WriteResult struct {
	Status int `json:"status"`

	raw client.Payload
}

// newWriteResult captures the payload of an applied write.
func newWriteResult(payload client.Payload) WriteResult {
	return WriteResult{Status: payload.Status(), raw: payload}
}

// Payload is the retained raw response.
func (w WriteResult) Payload() client.Payload { return w.raw }

// download streams a file response into dst. No path is ever taken from a
// caller: the sink is supplied by the caller and this package never opens one.
func (r requester) download(
	ctx context.Context, session client.Session, req client.Request, dst io.Writer,
) (client.Download, error) {
	if session.IsZero() {
		return client.Download{}, invalid(req, client.ErrMissingPrincipal)
	}
	return r.rc.Download(ctx, session, req, dst)
}

// decodeIfJSON decodes a payload only when it is an empty body or a JSON
// document. Anything else is left alone, because a write that succeeded must not
// be reported as a failure over its acknowledgement format.
func decodeIfJSON(payload client.Payload, out any) error {
	if payload.NoContent() {
		return nil
	}
	if !strings.Contains(payload.ContentType(), "json") {
		return nil
	}
	return client.DecodeJSON(payload, out)
}

// jsonBody marshals a strict request model. A model that cannot be marshaled is
// a programming error, and it is reported as a validation failure rather than
// dispatched.
func jsonBody(req client.Request, value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, invalid(req, fmt.Errorf("%w: request body could not be encoded",
			client.ErrValidation))
	}
	if len(body) > MaxRequestBodyBytes {
		return nil, invalid(req, fmt.Errorf("%w: request body exceeds its bound",
			client.ErrValidation))
	}
	return body, nil
}

// requireText validates a free-text write field: present, bounded, and free of
// the control characters that could corrupt a log line downstream.
func requireText(req client.Request, value, field string) (string, error) {
	switch {
	case value == "":
		return "", invalid(req, fmt.Errorf("%w: %s must not be empty",
			client.ErrValidation, field))
	case len(value) > MaxTextLen:
		return "", invalid(req, fmt.Errorf("%w: %s is too long",
			client.ErrValidation, field))
	case hasControlRune(value):
		return "", invalid(req, fmt.Errorf("%w: %s must not contain control characters",
			client.ErrValidation, field))
	}
	return value, nil
}

// hasControlRune reports whether value carries a control character other than a
// tab or a newline, which a description may legitimately contain.
func hasControlRune(value string) bool {
	for _, r := range value {
		if r == '\t' || r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// mismatch labels a verify-after-write check that found the saved state is not
// the state that was written. It is a failure on purpose: reporting success for
// a write Garmin did not keep is the defect the check exists to prevent.
func mismatch(req client.Request, cause error) error {
	return &client.APIError{
		Op:       req.Op,
		Endpoint: req.Endpoint,
		Kind:     client.KindUnknown,
		Err:      cause,
	}
}
