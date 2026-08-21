package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

// Redaction markers use square brackets rather than angle brackets. The JSON
// encoder escapes angle brackets, which would leave an ungreppable marker in a
// JSON log line. internal/config uses the same convention. redactedValue
// replaces a query value; redactedURLMarker replaces a URL that cannot be
// parsed, because an unparsable string cannot be split into a safe prefix and a
// secret-bearing query.
const (
	redactedValue     = "[redacted]"
	redactedURLMarker = "[redacted-url]"
)

// redactURL keeps the scheme, host and path of rawURL and replaces every query
// value with redactedValue. Userinfo and the fragment are dropped entirely:
// both carry credentials on the Garmin login path.
//
// Source: _sanitize_exception_text and _QUERY_VALUE_RE in client.py (0.3.10),
// which redact query values because requests embeds the full URL — including
// ?ticket=ST-... — in its exception text.
func redactURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return redactedURLMarker
	}

	safe := url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Opaque: parsed.Opaque, Path: parsed.Path}
	out := safe.String()
	if parsed.RawQuery == "" {
		return out
	}
	return out + "?" + redactQuery(parsed.RawQuery)
}

// redactQuery replaces every value in a raw query string, preserving key order
// and duplicates so the shape of the request stays diagnosable.
func redactQuery(rawQuery string) string {
	pairs := strings.Split(rawQuery, "&")
	out := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if pair == "" {
			continue
		}
		key, _, found := strings.Cut(pair, "=")
		safeKey := sanitizeToken(key, MaxQueryKeyLen)
		if !found {
			out = append(out, safeKey)
			continue
		}
		out = append(out, safeKey+"="+redactedValue)
	}
	return strings.Join(out, "&")
}

// maxCauseDepth bounds how far a cause chain is descended. A chain that loops
// back on itself — or one a peer made pathologically deep — must terminate.
const maxCauseDepth = 8

// redactedCause describes err without ever rendering arbitrary text. Only shapes
// this package can reason about are described; anything else degrades to a
// transport failure category or to its Go type name, because a third-party error
// message may embed a cookie, an Authorization header or a response body.
func redactedCause(err error) string {
	return redactedCauseAt(err, maxCauseDepth)
}

// redactedCauseAt is redactedCause with an explicit remaining depth.
//
// Structured shapes are tested before the sentinel set, and a matched sentinel is
// rendered from the sentinel's own text rather than err.Error(). Both rules
// matter: fmt.Errorf lets a caller put a bearer token, a cookie or a password in
// the wrapper message, and that wrapper text must never reach a log line just
// because the chain happens to contain one of this package's sentinels.
func redactedCauseAt(err error, depth int) string {
	if err == nil {
		return ""
	}
	if depth <= 0 {
		return causeTypeName(err)
	}

	// A *Error is the richest shape and is tested first. errors.As matches an
	// *Error receiver without unwrapping, so a self-referential chain is bounded
	// by depth rather than looping inside errors.Is.
	if nested, ok := errors.AsType[*Error](err); ok {
		return nested.render(depth - 1)
	}

	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return renderURLError(urlErr, depth-1)
	}

	if text, ok := sentinelText(err); ok {
		return text
	}

	// Checked before networkCategory: context.DeadlineExceeded is also a
	// net.Error that reports a timeout.
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "context deadline exceeded"
	case errors.Is(err, context.Canceled):
		return "context canceled"
	}

	if category, ok := networkCategory(err); ok {
		return category
	}
	return causeTypeName(err)
}

// renderURLError reports the verb and the query-redacted URL of a *url.Error,
// then the redacted description of its own cause. The *url.Error's Error() text
// is never used: it embeds the raw URL, including ?ticket=ST-...
func renderURLError(urlErr *url.Error, depth int) string {
	return "url error " + sanitizeToken(urlErr.Op, MaxURLOpLen) + " " +
		redactURL(urlErr.URL) + " (" + redactedCauseAt(urlErr.Err, depth) + ")"
}

// sentinelText returns the canonical text of the first sentinel err matches. The
// text comes from the sentinel itself, never from err, because this package
// authored every sentinel string and authored none of the wrappers.
func sentinelText(err error) (string, bool) {
	for _, sentinel := range sentinels {
		if errors.Is(err, sentinel) {
			return sentinel.Error(), true
		}
	}
	return "", false
}

func causeTypeName(err error) string {
	return "cause of type " + fmt.Sprintf("%T", err)
}

// redactedResponse is the only shape a Response is ever rendered or serialized
// in. It reports the size and presence of the raw material, never its content.
type redactedResponse struct {
	Type        string `json:"type"`
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	BodyBytes   int    `json:"bodyBytes"`
	HeaderKeys  int    `json:"headerKeys"`
}

func (r Response) redacted() redactedResponse {
	return redactedResponse{
		Type:        "protocol.Response",
		Status:      r.Status(),
		ContentType: r.ContentType(),
		BodyBytes:   r.BodyLen(),
		HeaderKeys:  r.HeaderLen(),
	}
}

// String renders a Response without its body or header values.
func (r Response) String() string {
	red := r.redacted()
	return "protocol.Response{status:" + strconv.Itoa(red.Status) +
		" contentType:" + quoteLabel(red.ContentType) +
		" bodyBytes:" + strconv.Itoa(red.BodyBytes) +
		" headerKeys:" + strconv.Itoa(red.HeaderKeys) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (r Response) GoString() string { return r.String() }

// MarshalJSON serializes the redacted form, so a Response embedded in a log
// record cannot leak its body or headers.
func (r Response) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.redacted())
}

// LogValue implements slog.LogValuer, so structured logging is safe by default:
// every handler receives the redacted group instead of walking the value.
func (r Response) LogValue() slog.Value {
	red := r.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.Int("status", red.Status),
		slog.String("contentType", red.ContentType),
		slog.Int("bodyBytes", red.BodyBytes),
		slog.Int("headerKeys", red.HeaderKeys),
	)
}

// redactedClassification is the only shape a Classification is ever rendered or
// serialized in. Secret-bearing fields collapse to a presence flag.
type redactedClassification struct {
	Type                 string `json:"type"`
	Outcome              string `json:"outcome"`
	Status               int    `json:"status"`
	HasServiceTicket     bool   `json:"serviceTicketPresent"`
	MFAMethod            string `json:"mfaMethod,omitempty"`
	MFADeliveryUncertain bool   `json:"mfaDeliveryUncertain"`
	HasCSRFToken         bool   `json:"csrfTokenPresent"`
	HasPageTitle         bool   `json:"pageTitlePresent"`
	RetryAfterSeconds    int    `json:"retryAfterSeconds"`
	ResponseStatusType   string `json:"responseStatusType,omitempty"`
}

func (c Classification) redacted() redactedClassification {
	fields := c.f()
	return redactedClassification{
		Type:                 "protocol.Classification",
		Outcome:              fields.outcome.String(),
		Status:               fields.status,
		HasServiceTicket:     fields.serviceTicket != "",
		MFAMethod:            knownMFAMethod(fields.mfaMethod),
		MFADeliveryUncertain: fields.mfaDeliveryUncertain,
		HasCSRFToken:         fields.csrfToken != "",
		HasPageTitle:         fields.pageTitle != "",
		RetryAfterSeconds:    int(fields.retryAfter.Seconds()),
		ResponseStatusType:   knownResponseStatusType(fields.responseStatusType),
	}
}

// String renders a Classification without its ticket, CSRF token or page title.
func (c Classification) String() string {
	red := c.redacted()
	return "protocol.Classification{outcome:" + red.Outcome +
		" status:" + strconv.Itoa(red.Status) +
		" serviceTicket:" + presence(red.HasServiceTicket) +
		" mfaMethod:" + quoteLabel(red.MFAMethod) +
		" mfaDeliveryUncertain:" + strconv.FormatBool(red.MFADeliveryUncertain) +
		" csrfToken:" + presence(red.HasCSRFToken) +
		" pageTitle:" + presence(red.HasPageTitle) +
		" retryAfterSeconds:" + strconv.Itoa(red.RetryAfterSeconds) +
		" responseStatusType:" + quoteLabel(red.ResponseStatusType) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (c Classification) GoString() string { return c.String() }

// MarshalJSON serializes the redacted form, so a Classification embedded in a
// log record cannot leak its ticket, CSRF token or page title.
func (c Classification) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.redacted())
}

// LogValue implements slog.LogValuer, so structured logging is safe by default:
// every handler receives the redacted group instead of walking the value.
func (c Classification) LogValue() slog.Value {
	red := c.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.String("outcome", red.Outcome),
		slog.Int("status", red.Status),
		slog.Bool("serviceTicketPresent", red.HasServiceTicket),
		slog.String("mfaMethod", red.MFAMethod),
		slog.Bool("mfaDeliveryUncertain", red.MFADeliveryUncertain),
		slog.Bool("csrfTokenPresent", red.HasCSRFToken),
		slog.Bool("pageTitlePresent", red.HasPageTitle),
		slog.Int("retryAfterSeconds", red.RetryAfterSeconds),
		slog.String("responseStatusType", red.ResponseStatusType),
	)
}

func presence(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func quoteLabel(value string) string {
	if value == "" {
		return `""`
	}
	return `"` + value + `"`
}
