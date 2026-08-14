package api

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// requester is the request layer every domain client is built on. It is a value
// wrapper rather than an interface because there is exactly one implementation and
// no seam is needed: the injectable seams — the caller, the clock, the sleeper and
// the bounds — all live inside *client.Client.
type requester struct {
	rc *client.Client
}

// newRequester validates the request layer a domain client was handed.
func newRequester(rc *client.Client) (requester, error) {
	if rc == nil {
		return requester{}, fmt.Errorf("garmin api: domain client needs a request layer: %w",
			client.ErrNotConfigured)
	}
	return requester{rc: rc}, nil
}

// limits reports the bounds the request layer enforces, so a domain client can
// validate a page or a date range before it dispatches anything.
func (r requester) limits() client.Limits { return r.rc.Limits() }

// read performs the request and decodes it into out, returning the retained
// payload. An unusable session is refused before anything is dispatched.
func (r requester) read(
	ctx context.Context, session client.Session, req client.Request, out any,
) (client.Payload, error) {
	if session.IsZero() {
		return client.Payload{}, invalid(req, client.ErrMissingPrincipal)
	}
	return r.rc.GetJSON(ctx, session, req, out)
}

// readRequest builds a read request for op, endpoint and path.
func readRequest(op client.Op, endpoint client.Endpoint, path string, query url.Values) client.Request {
	return client.Request{
		Op:       op,
		Endpoint: endpoint,
		Path:     path,
		Query:    query,
		Effect:   client.EffectRead,
	}
}

// activitySegmentPath builds a per-activity path. The identifier is a validated
// client.ID, so it is decimal digits only and can carry no path separator.
func activitySegmentPath(id client.ID, segment string) string {
	return client.PathActivityPrefix + "/" + id.String() + "/" + segment
}

// displayNamePath appends a validated display name as one escaped path segment.
// Source: _require_display_name, which percent-encodes the name with safe="" so a
// hostile profile response cannot inject a separator, a query or a fragment.
func displayNamePath(prefix string, name client.DisplayName) string {
	return prefix + "/" + url.PathEscape(name.Value())
}

// invalid labels a caller-side validation failure as an *APIError, so a domain
// method never returns a bare error and errors.Is keeps working.
func invalid(req client.Request, cause error) error {
	return &client.APIError{
		Op:       req.Op,
		Endpoint: req.Endpoint,
		Kind:     client.KindValidation,
		Err:      cause,
	}
}

// unexpected labels a response that decoded but cannot be used, for example an
// empty user summary. Source: get_user_summary, which raises rather than returning
// an empty document.
func unexpected(req client.Request, cause error) error {
	return &client.APIError{
		Op:       req.Op,
		Endpoint: req.Endpoint,
		Kind:     client.KindUnknown,
		Err:      cause,
	}
}

// rejected labels a payload that reports the data is withheld from this session.
// Source: get_user_summary's privacyProtected check, which upstream raises as an
// authentication error.
func rejected(req client.Request, cause error) error {
	return &client.APIError{
		Op:       req.Op,
		Endpoint: req.Endpoint,
		Kind:     client.KindAuthentication,
		Err:      cause,
	}
}

// requireDisplayName refuses an unset display name before it can be interpolated
// into a URL path.
func requireDisplayName(req client.Request, name client.DisplayName) error {
	if name.IsZero() {
		return invalid(req, fmt.Errorf("%w: a display name is required for this endpoint",
			client.ErrValidation))
	}
	return nil
}

// requireDate refuses an unset calendar date.
func requireDate(req client.Request, date client.Date) error {
	if date.IsZero() {
		return invalid(req, fmt.Errorf("%w: a calendar date is required for this endpoint",
			client.ErrValidation))
	}
	return nil
}

// requireID refuses an unset identifier.
func requireID(req client.Request, id client.ID) error {
	if id.IsZero() {
		return invalid(req, fmt.Errorf("%w: a positive identifier is required for this endpoint",
			client.ErrValidation))
	}
	return nil
}

// maxTokenLen bounds a caller-supplied enumeration token such as an activity type.
const maxTokenLen = 32

// parseLowerToken validates a lowercase Garmin enumeration token: letters, digits
// and underscores only, bounded in length. An empty value is reported as absent.
func parseLowerToken(value, field string) (string, bool, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false, nil
	}
	if len(trimmed) > maxTokenLen {
		return "", false, fmt.Errorf("%w: %s is too long", client.ErrValidation, field)
	}
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return "", false, fmt.Errorf("%w: %s must be lowercase letters, digits or underscores",
				client.ErrValidation, field)
		}
	}
	return trimmed, true, nil
}
