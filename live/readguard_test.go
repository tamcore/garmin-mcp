//go:build garminlive

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// maxGraphQLProbeBytes bounds how much of an outbound GraphQL body the guard reads
// before deciding. The request layer bounds the rendered document at
// client.MaxGraphQLDocumentBytes, so anything materially larger is not a document
// this server produced and is refused rather than parsed.
const maxGraphQLProbeBytes int64 = 4 * client.MaxGraphQLDocumentBytes

// graphQLQueryField is the only key a GraphQL body this server sends may carry.
// client.graphQLBody marshals exactly one field, so a body of any other shape did not
// come from the request layer.
const graphQLQueryField = "query"

// graphQLQueryStart is the opening of every document the request layer renders.
// client.GraphQLRequest.Document writes "query{" and nothing else, so a document that
// does not start with it is not one of this server's queries — a mutation document
// starts with "mutation".
const graphQLQueryStart = "query{"

// readOnlyCaller is what makes this suite read-only by construction rather than by
// convention. Every domain client and every tool reaches Garmin through it, and it
// refuses anything that is not a read, so a write or destructive tool cannot mutate
// the account even if some future test called one.
//
// One exception is unavoidable and is deliberately narrow: Garmin serves the workout
// calendar from a GraphQL gateway, and a GraphQL query is a POST. A POST is therefore
// admitted to that one path, and only when the body it carries is one of the query
// documents this server can render. Method and path alone are not enough: the whole
// GraphQL surface — every mutation the gateway exposes, now and after any drift — sits
// behind that single path, so a guard that judged the path would be admitting all of
// it. What is judged is the document.
//
// It wraps the caller only. The login and the token refresh use their own transport
// and legitimately POST, which is why the guard sits here and not on the HTTP client.
type readOnlyCaller struct {
	inner client.Caller

	// requests counts what the guard admitted. It is the evidence a tool reached the
	// service: a result on its own proves only that the handler is wired.
	requests atomic.Int64
}

func (c *readOnlyCaller) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	if !isReadRequest(req) {
		return nil, fmt.Errorf("live: refusing a %s request to %s: this suite is read-only",
			req.Method, req.URL.Path)
	}
	// Counted before dispatch: the request has left the guard whatever follows.
	c.requests.Add(1)
	return c.inner.Do(ctx, principal, req)
}

// dispatched reports how many requests this guard has admitted.
func (c *readOnlyCaller) dispatched() int64 { return c.requests.Load() }

// isReadRequest reports whether one request only reads.
func isReadRequest(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead:
		return true
	case http.MethodPost:
		return isKnownGraphQLQuery(req)
	default:
		return false
	}
}

// isKnownGraphQLQuery reports whether one POST is a GraphQL query this server renders.
//
// It judges the bytes that will actually be sent. A request that cannot be read is
// refused rather than trusted: the guard cannot judge what it cannot read.
func isKnownGraphQLQuery(req *http.Request) bool {
	if req.URL == nil || req.URL.Path != client.PathGraphQL {
		return false
	}
	raw, read := takeBody(req, maxGraphQLProbeBytes)
	if !read {
		return false
	}
	return isKnownQueryDocument(documentOf(raw))
}

// takeBody reads the body one request will dispatch and puts exactly those bytes back,
// or reports that it could not.
//
// Reading GetBody instead would be judging the wrong thing. An *http.Request carries
// two representations of its body — Body, which the transport writes, and GetBody,
// which only replays it — and nothing in net/http keeps the two equal. A guard that
// read GetBody would therefore be inspecting one document while a different one left
// the process, which is a hole in exactly the direction that matters: the benign
// document goes in the copy and the mutation goes in the body. Body is consumed here,
// judged, and reinstalled from the bytes that were read; GetBody is replaced with a
// replay of those same bytes so a post-refresh retry cannot resurrect anything else.
//
// A body over the bound is refused rather than truncated: a truncated body handed on
// would be a corrupt request, and one this guard has not seen all of.
func takeBody(req *http.Request, limit int64) ([]byte, bool) {
	if req.Body == nil {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, limit+1))
	_ = req.Body.Close()
	if err != nil || int64(len(raw)) > limit {
		return nil, false
	}

	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(raw)), nil
	}
	req.ContentLength = int64(len(raw))
	return raw, true
}

// documentOf reads the query document out of a GraphQL request body, or "" when the
// body is not the one-field object the request layer sends.
func documentOf(raw []byte) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 1 {
		return ""
	}
	encoded, present := fields[graphQLQueryField]
	if !present {
		return ""
	}

	var document string
	if err := json.Unmarshal(encoded, &document); err != nil {
		return ""
	}
	return document
}

// isKnownQueryDocument reports whether document is an anonymous query naming one of
// the root fields internal/garmin/client is able to query.
//
// The match is anchored at the start of the document and the named field is followed
// by its argument list, so nothing can be prepended to reach a second operation and no
// field outside the allowlist can be named. The allowlist is the request layer's own
// KnownGraphQLFields, which carries query fields only, so it cannot drift away from
// what this suite is allowed to send.
func isKnownQueryDocument(document string) bool {
	rest, found := strings.CutPrefix(document, graphQLQueryStart)
	if !found {
		return false
	}
	for _, field := range client.KnownGraphQLFields() {
		if strings.HasPrefix(rest, string(field)+"(") {
			return true
		}
	}
	return false
}
