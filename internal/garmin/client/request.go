package client

import (
	"net/http"
	"net/url"
	"strings"
)

// Effect declares what a request does, which is what the retry predicate needs to
// know before it may repeat one.
type Effect int

const (
	// EffectRead is a safe read. It carries no body and may be retried.
	EffectRead Effect = iota
	// EffectIdempotentWrite is a write Garmin applies at most once for the same
	// payload, so repeating it cannot apply a change twice. PUT is the usual verb.
	EffectIdempotentWrite
	// EffectUnsafeWrite is a write that may be applied more than once if repeated.
	// It is never retried automatically.
	EffectUnsafeWrite
	// EffectDelete removes a resource. It is never retried automatically, so a
	// caller sees the real outcome of the one attempt that was made.
	EffectDelete
	// EffectQueryRead is a read that carries a body, which is what Garmin's GraphQL
	// tier requires: the query document travels in the request, so the call is a
	// POST even though it changes nothing.
	//
	// It is repeatable for the same reason EffectRead is, and the reason is
	// structural rather than a promise: the only bodies this package sends with this
	// effect are rendered by GraphQLRequest, whose root field must be one of the
	// GraphQLField constants, and every one of those constants is a query field. No
	// mutation can be expressed, so repeating the request cannot apply anything
	// twice.
	EffectQueryRead
)

// String returns a stable label for logs and metrics.
func (e Effect) String() string {
	switch e {
	case EffectRead:
		return "read"
	case EffectIdempotentWrite:
		return "idempotent_write"
	case EffectUnsafeWrite:
		return "unsafe_write"
	case EffectDelete:
		return "delete"
	case EffectQueryRead:
		return "query_read"
	default:
		return labelUnknown
	}
}

// repeatable reports whether a request with this effect may be sent twice.
func (e Effect) repeatable() bool {
	return e == EffectRead || e == EffectIdempotentWrite || e == EffectQueryRead
}

// allowedMethods are the HTTP methods this package will dispatch. Everything else
// — CONNECT, TRACE, an arbitrary verb — is refused before dispatch.
var allowedMethods = [...]string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete,
}

// Media types this package sends and accepts.
const (
	mediaTypeJSON = "application/json"
	mediaTypeAny  = "*/*"
)

// Request is one API-tier call, fully specified and validated before anything is
// dispatched.
//
// The path is a clean path with no query string and no fragment: query parameters
// go in Query, so neither a date nor an identifier nor a credential can be
// concatenated into a path. Source: the defense-in-depth path check in
// Client._run_request, which rejects a traversal segment, a "?", a "#" and a
// backslash, and validates the percent-decoded form because the HTTP client
// decodes unreserved escapes afterwards.
type Request struct {
	// Op is the sanitized operation label. It must be a recognized label.
	Op Op
	// Endpoint is the sanitized endpoint label. It must be a recognized label.
	Endpoint Endpoint
	// Method is the HTTP method. Empty means GET.
	Method string
	// Path is the absolute API-tier path, starting with "/".
	Path string
	// Query holds the query parameters, encoded by this package.
	Query url.Values
	// Body is the request body for a write. It must be empty for a read.
	Body []byte
	// ContentType is the body media type. Empty with a body means JSON.
	ContentType string
	// Effect declares what the request does; see Effect.
	Effect Effect
	// FileTransfer marks a request that uploads or downloads a file, so a
	// rejected payload is reported as ErrInvalidFile rather than as a generic
	// validation failure.
	FileTransfer bool
}

// method is the effective HTTP method.
func (r Request) method() string {
	if r.Method == "" {
		return http.MethodGet
	}
	return r.Method
}

// Validate reports whether the request may be dispatched. Every failure matches
// ErrValidation and names the rule, never the rejected value.
func (r Request) Validate() error {
	if !r.Op.IsKnown() {
		return validationError("request op is not a recognized label")
	}
	if !r.Endpoint.IsKnown() {
		return validationError("request endpoint is not a recognized label")
	}
	if err := validatePath(r.Path); err != nil {
		return err
	}
	if err := validateMethod(r.method()); err != nil {
		return err
	}
	if len(r.Body) > 0 && r.Effect == EffectRead {
		return validationError("a read request must not carry a body")
	}
	if r.Effect != EffectRead && r.method() == http.MethodGet {
		return validationError("a write request must not use GET")
	}
	if r.Effect == EffectQueryRead && (len(r.Body) == 0 || r.method() != http.MethodPost) {
		return validationError("a query read must be a POST carrying its query document")
	}
	return validateQuery(r.Query)
}

// validatePath enforces the clean-path rule on both the literal and the
// percent-decoded form.
func validatePath(path string) error {
	if path == "" || !strings.HasPrefix(path, "/") {
		return validationError("request path must be absolute and start with /")
	}

	decoded, err := url.PathUnescape(path)
	if err != nil {
		return validationError("request path is not valid percent-encoding")
	}
	for _, candidate := range [2]string{path, decoded} {
		switch {
		case strings.ContainsAny(candidate, "?#\\"):
			return validationError("request path must carry no query, fragment or backslash")
		case hasTraversalSegment(candidate):
			return validationError("request path must carry no traversal segment")
		case strings.ContainsAny(candidate, "\x00\n\r"):
			return validationError("request path must carry no control characters")
		}
	}
	return nil
}

// hasTraversalSegment reports whether any path segment is exactly "..", including
// the "..;matrix-param" filter-bypass form upstream also rejects.
func hasTraversalSegment(path string) bool {
	for segment := range strings.SplitSeq(path, "/") {
		if head, _, _ := strings.Cut(segment, ";"); head == ".." {
			return true
		}
	}
	return false
}

func validateMethod(method string) error {
	for _, allowed := range allowedMethods {
		if method == allowed {
			return nil
		}
	}
	return validationError("request method is not one this package dispatches")
}

// validateQuery refuses a parameter name or value with a control character, which
// is the only way a query could corrupt a log line or a header.
func validateQuery(query url.Values) error {
	for key, values := range query {
		if key == "" || strings.ContainsAny(key, "\x00\n\r") {
			return validationError("query parameter name is empty or contains control characters")
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\x00\n\r") {
				return validationError("query parameter value contains control characters")
			}
		}
	}
	return nil
}

// requestURL renders the absolute request URL against base. The query is encoded
// by net/url, so a value cannot break out of its parameter.
func (r Request) requestURL(base string) string {
	full := strings.TrimRight(base, "/") + r.Path
	if len(r.Query) == 0 {
		return full
	}
	return full + "?" + r.Query.Encode()
}

// contentType is the effective body media type.
func (r Request) contentType() string {
	if r.ContentType != "" {
		return r.ContentType
	}
	if len(r.Body) > 0 {
		return mediaTypeJSON
	}
	return ""
}

// accept is the Accept header value. Source: the Accept "*/*" upstream sets for a
// download, versus JSON for an ordinary API read.
func (r Request) accept() string {
	if r.FileTransfer {
		return mediaTypeAny
	}
	return mediaTypeJSON
}
