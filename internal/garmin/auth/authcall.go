package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
)

// errNilRequest reports a missing request.
var errNilRequest = errors.New("garmin auth: request is nil")

// Do performs req as principal, attaching the principal's DI bearer token.
//
// The token is refreshed before the call when it expires inside the safety
// window. After a 401 the call is retried at most once, and only when both hold:
// the refresh succeeded, and the request is safe or idempotent. A POST or PATCH
// is never replayed — Garmin gives no guarantee that a rejected mutation was not
// applied — so its 401 is handed back to the caller unchanged.
//
// The caller owns the returned response body. Do sets Authorization and leaves
// every other header alone.
func (r *Refresher) Do(ctx context.Context, principal string, req *http.Request) (*http.Response, error) {
	if principal == "" {
		return nil, ErrMissingPrincipal
	}
	if req == nil {
		return nil, errNilRequest
	}

	set, err := r.Fresh(ctx, principal)
	if err != nil {
		return nil, err
	}

	resp, err := r.send(ctx, req, set)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized || !isReplayable(req) {
		return resp, nil
	}

	// The response is being replaced, so its body must not be leaked.
	drain(resp)

	refreshed, err := r.Refresh(ctx, principal)
	if err != nil {
		return nil, err
	}
	return r.send(ctx, req, refreshed)
}

// send performs one attempt with set's bearer token, on a clone of req so the
// caller's request stays reusable.
func (r *Refresher) send(ctx context.Context, req *http.Request, set TokenSet) (*http.Response, error) {
	attempt, err := cloneRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	attempt.Header.Set("Authorization", "Bearer "+set.Token())
	return r.tokens.doer.Do(attempt)
}

// cloneRequest copies req, restoring its body from GetBody so the request can be
// sent twice.
func cloneRequest(ctx context.Context, req *http.Request) (*http.Request, error) {
	clone := req.Clone(ctx)
	if req.Body == nil || req.Body == http.NoBody {
		return clone, nil
	}
	if req.GetBody == nil {
		// Without GetBody the body is consumed by the first attempt, so a replay
		// would silently send nothing.
		return nil, ErrNotIdempotent
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, ErrNotIdempotent
	}
	clone.Body = body
	return clone, nil
}

// isReplayable reports whether a 401 on req may be retried. Safe methods carry no
// side effect, and PUT and DELETE are idempotent by definition, so repeating one
// cannot apply a change twice. A body with no GetBody is refused because it
// cannot be resent.
func isReplayable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace,
		http.MethodPut, http.MethodDelete:
	default:
		return false
	}
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// drain closes a response body that is being discarded, so the connection can be
// reused and no token-bearing payload is left dangling.
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	_ = resp.Body.Close()
}
