package protocol

import (
	"net/http"
	"time"
)

// Response is the classifier input: one HTTP response, already read into memory.
// Build one with NewResponse or NewResponseFromParts.
//
// It is secret-bearing: the body and the headers hold whatever Garmin sent,
// including Set-Cookie and Authorization values. None of that material is
// reachable from outside this package. The fields are unexported and the
// secret-bearing ones sit behind a pointer, so a reflective logger, a direct
// field print and a method-stripping alias (type Raw protocol.Response) all see
// an address rather than the material. String, GoString, MarshalJSON and LogValue
// report only the shape of the response.
//
// The zero value is inert: every accessor reports its zero result.
type Response struct {
	// parts is a pointer on purpose. fmt follows a pointer only at the top level,
	// so a nested unexported pointer renders as an address, whereas a nested
	// unexported struct renders its field values. It is never mutated after
	// construction.
	parts *responseParts
}

// responseParts holds the response material. It is copied, never mutated.
type responseParts struct {
	status    int
	header    http.Header
	mediaType string
	body      []byte
	now       time.Time
}

// NewResponse builds a Response from an HTTP response and its already-read body.
// The header is copied; body is retained by reference and must not be modified
// afterwards. A nil resp yields the zero Response, which classifies as if the
// request never completed.
func NewResponse(resp *http.Response, body []byte) Response {
	if resp == nil {
		return Response{}
	}
	return Response{parts: &responseParts{
		status:    resp.StatusCode,
		header:    resp.Header.Clone(),
		mediaType: resp.Header.Get("Content-Type"),
		body:      body,
	}}
}

// NewResponseFromParts builds a Response from its parts, for a caller that has no
// *http.Response: a synthesized transport failure, or a fixture. An empty
// contentType is taken from header. The header is copied; body is retained by
// reference and must not be modified afterwards.
func NewResponseFromParts(status int, contentType string, header http.Header, body []byte) Response {
	mediaType := contentType
	if mediaType == "" {
		mediaType = header.Get("Content-Type")
	}
	return Response{parts: &responseParts{
		status:    status,
		header:    header.Clone(),
		mediaType: mediaType,
		body:      body,
	}}
}

// WithNow returns a copy of r whose reference instant for a Retry-After HTTP-date
// is now. The receiver is not modified. The zero instant means time.Now().
func (r Response) WithNow(now time.Time) Response {
	parts := r.p()
	parts.now = now
	return Response{parts: &parts}
}

// Status is the HTTP status code, or 0 when the request never completed.
func (r Response) Status() int { return r.p().status }

// ContentType is the sanitized media type, without parameters. It is safe to
// render: it is reduced to a bounded token from the character set of a media type.
func (r Response) ContentType() string { return sanitizeMediaType(r.contentType()) }

// BodyLen is the body size in bytes. The body itself is deliberately not exposed:
// it carries credential material and is only ever read by this package.
func (r Response) BodyLen() int { return len(r.p().body) }

// HeaderLen is the number of distinct header keys. The header values are
// deliberately not exposed: they carry Set-Cookie and Authorization material.
func (r Response) HeaderLen() int { return len(r.p().header) }

// p returns a copy of the response parts, or the zero parts for a zero Response.
func (r Response) p() responseParts {
	if r.parts == nil {
		return responseParts{}
	}
	return *r.parts
}

// contentType is the raw, unsanitized media type, for content sniffing inside
// this package.
func (r Response) contentType() string { return r.p().mediaType }

// body is the raw response body, for the classifiers inside this package.
func (r Response) body() []byte { return r.p().body }

func (r Response) retryAfter() time.Duration {
	parts := r.p()
	if parts.header == nil {
		return 0
	}
	delay, _ := ParseRetryAfter(parts.header.Get(HeaderRetryAfter), r.refTime())
	return delay
}

func (r Response) refTime() time.Time {
	if parts := r.p(); !parts.now.IsZero() {
		return parts.now
	}
	return time.Now()
}
