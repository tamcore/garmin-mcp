package client

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// Payload is one Garmin response body, already read and bounded: the status, the
// sanitized media type and the bytes, kept for tolerant decoding and for
// diagnostics.
//
// It is sensitive by construction. A Garmin response carries health data,
// coordinates and identity at once, so the bytes are unreachable by accident: the
// fields are unexported and the material sits two pointers deep, which is the
// depth fmt's badVerb path cannot dereference even through a method-stripping
// alias (see internal/garmin/protocol/alias_leak_test.go for why one level is not
// enough). String, GoString, MarshalJSON and LogValue report the shape of the
// payload. Bytes hands a copy to a caller that asks deliberately, which is how
// DecodeJSON reads it.
//
// The zero value is inert: every accessor reports its zero result.
type Payload struct {
	// sealed is a pointer on purpose; see protocol.Response.
	sealed *sealedPayload
}

// sealedPayload is the deliberate extra level of indirection that keeps the body
// out of reach of fmt.
type sealedPayload struct {
	inner *payloadParts
}

// payloadParts holds the response material. It is copied, never mutated.
type payloadParts struct {
	op          Op
	endpoint    Endpoint
	status      int
	contentType string
	body        []byte
}

// newPayload seals response material into an immutable Payload. body is retained
// by reference and must not be modified afterwards.
func newPayload(op Op, endpoint Endpoint, status int, contentType string, body []byte) Payload {
	return Payload{sealed: &sealedPayload{inner: &payloadParts{
		op:          op,
		endpoint:    endpoint,
		status:      status,
		contentType: sanitizeMediaType(contentType),
		body:        body,
	}}}
}

// p returns a copy of the payload parts, or the zero parts for a zero Payload.
func (p Payload) p() payloadParts {
	if p.sealed == nil || p.sealed.inner == nil {
		return payloadParts{}
	}
	return *p.sealed.inner
}

// Op is the sanitized operation label the payload was fetched for.
func (p Payload) Op() Op { return p.p().op }

// Endpoint is the sanitized endpoint label the payload came from.
func (p Payload) Endpoint() Endpoint { return p.p().endpoint }

// Status is the HTTP status code, or 0 for a zero Payload.
func (p Payload) Status() int { return p.p().status }

// ContentType is the sanitized media type, without parameters.
func (p Payload) ContentType() string { return p.p().contentType }

// Len is the body size in bytes.
func (p Payload) Len() int { return len(p.p().body) }

// NoContent reports whether Garmin answered with 204, or with an empty body.
// Source: the 204 branch of Client._run_request, which normalizes an empty
// response to an empty JSON document.
func (p Payload) NoContent() bool {
	parts := p.p()
	return parts.status == http.StatusNoContent || len(parts.body) == 0
}

// Bytes returns a copy of the body, so no caller can mutate the payload and no
// two callers share storage. It is sensitive material: decode it, never log it.
func (p Payload) Bytes() []byte {
	body := p.p().body
	out := make([]byte, len(body))
	copy(out, body)
	return out
}

// redactedPayload is the only shape a Payload ever renders as: its labels, size
// and media type, never its bytes.
type redactedPayload struct {
	Op          string `json:"op"`
	Endpoint    string `json:"endpoint"`
	ContentType string `json:"contentType"`
	Status      int    `json:"status"`
	Bytes       int    `json:"bytes"`
}

func (p Payload) redacted() redactedPayload {
	parts := p.p()
	return redactedPayload{
		Op:          parts.op.String(),
		Endpoint:    parts.endpoint.String(),
		ContentType: parts.contentType,
		Status:      parts.status,
		Bytes:       len(parts.body),
	}
}

// String reports the shape of the payload. The body is health, location and
// identity material at once, so it is never rendered.
func (p Payload) String() string {
	r := p.redacted()
	return "Payload{op:" + r.Op +
		" endpoint:" + r.Endpoint +
		" contentType:" + quoteLabel(r.ContentType) +
		" status:" + strconv.Itoa(r.Status) +
		" bytes:" + strconv.Itoa(r.Bytes) + "}"
}

// GoString keeps %#v as redacted as %v.
func (p Payload) GoString() string { return p.String() }

// MarshalJSON encodes the shape, never the body, so a payload cannot reach a JSON
// log sink by being embedded in a larger structure.
func (p Payload) MarshalJSON() ([]byte, error) { return json.Marshal(p.redacted()) }

// LogValue keeps slog from reaching the body through reflection.
func (p Payload) LogValue() slog.Value {
	r := p.redacted()
	return slog.GroupValue(
		slog.String("op", r.Op),
		slog.String("endpoint", r.Endpoint),
		slog.String("contentType", r.ContentType),
		slog.Int("status", r.Status),
		slog.Int("bytes", r.Bytes),
	)
}

// MaxMediaTypeLen bounds a rendered media type.
const MaxMediaTypeLen = 64

// sanitizeMediaType keeps the media type of a Content-Type header, dropping
// parameters and anything outside the token charset, so a hostile header cannot
// smuggle markup or a newline into a log line.
func sanitizeMediaType(value string) string {
	mediaType, _, _ := strings.Cut(value, ";")

	var b strings.Builder
	for _, r := range strings.TrimSpace(mediaType) {
		if !isMediaTypeRune(r) || b.Len() >= MaxMediaTypeLen {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isMediaTypeRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.', r == '+', r == '/':
		return true
	default:
		return false
	}
}
