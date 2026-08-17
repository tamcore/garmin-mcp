package protocol

import "time"

// Classification is the immutable verdict for one response. Only this package
// produces one, from a Classify* function.
//
// It is secret-bearing: the service ticket, the CSRF token and the page title
// carry credential and page material. None of it is reachable as a field: the
// fields are unexported and sit behind a pointer, so a reflective logger, a direct
// field print and a method-stripping alias (type Raw protocol.Classification) all
// see an address rather than the material. String, GoString, MarshalJSON and
// LogValue report presence rather than content; the accessors below hand the real
// values to a caller that asks for them deliberately.
//
// The zero value is inert: Outcome reports OutcomeUnknown and every other
// accessor its zero result.
type Classification struct {
	// fields is a pointer on purpose; see Response.parts. It is never mutated
	// after construction.
	fields *sealedFields
}

// sealedFields is the same deliberate extra indirection as sealedParts in
// response.go: it keeps a service ticket, a CSRF token and a page title out of
// reach of fmt's badVerb path on a method-stripping alias.
type sealedFields struct {
	inner *classificationFields
}

// classificationFields is the builder the classifiers fill in. It is copied into a
// Classification, so no caller ever shares its storage.
type classificationFields struct {
	outcome              Outcome
	status               int
	serviceTicket        string
	mfaMethod            string
	mfaDeliveryUncertain bool
	widgetMFA            WidgetMFARequest
	widgetMFAFound       bool
	csrfToken            string
	pageTitle            string
	retryAfter           time.Duration
	responseStatusType   string
}

// newClassification seals a builder into an immutable Classification, copying the
// fields so the caller's builder cannot be observed afterwards.
func newClassification(f classificationFields) Classification {
	return Classification{fields: &sealedFields{inner: &f}}
}

// f returns a copy of the verdict fields, or the zero fields for a zero value.
func (c Classification) f() classificationFields {
	if c.fields == nil || c.fields.inner == nil {
		return classificationFields{}
	}
	return *c.fields.inner
}

// withOutcome returns a copy of c whose outcome is replaced. It is unexported:
// only this package's MFA-verify-specific classifiers reinterpret a verdict, and
// every other field — status, ticket, title and the rest — is carried over
// unchanged.
func (c Classification) withOutcome(outcome Outcome) Classification {
	next := c.f()
	next.outcome = outcome
	return newClassification(next)
}

// Outcome is the classified meaning of the response.
func (c Classification) Outcome() Outcome { return c.f().outcome }

// Status echoes the HTTP status of the classified response.
func (c Classification) Status() int { return c.f().status }

// ServiceTicket is the extracted CAS service ticket on success, or "". It is a
// credential: never put it in a log line or an error message.
func (c Classification) ServiceTicket() string { return c.f().serviceTicket }

// WidgetMFA is what the widget page declared about delivering a code, and whether
// it declared anything at all. A page carrying no inline variables reports false,
// so a caller never builds a delivery request out of empty strings.
func (c Classification) WidgetMFA() (WidgetMFARequest, bool) {
	f := c.f()
	return f.widgetMFA, f.widgetMFAFound
}

// MFAMethod is the delivery method Garmin last used, on OutcomeMFARequired.
func (c Classification) MFAMethod() string { return c.f().mfaMethod }

// MFADeliveryUncertain marks an MFA challenge scraped from HTML, where OTP
// delivery is not confirmed. Upstream 0.3.10 removed the shelving this flag once
// drove and instead requests delivery explicitly via PathWidgetRequestMFACode; the
// flag is kept so a caller can decide.
func (c Classification) MFADeliveryUncertain() bool { return c.f().mfaDeliveryUncertain }

// CSRFToken is the _csrf form value found in an HTML page, or "". It is a
// credential: never put it in a log line or an error message.
func (c Classification) CSRFToken() string { return c.f().csrfToken }

// PageTitle is the sanitized, length-bounded HTML <title>. It is server-controlled
// text: sanitizing frees it of markup and control characters, but a caller must
// still not attribute meaning to its wording.
func (c Classification) PageTitle() string { return c.f().pageTitle }

// RetryAfter is the parsed Retry-After hint, or 0 when absent.
func (c Classification) RetryAfter() time.Duration { return c.f().retryAfter }

// ResponseStatusType is the sanitized responseStatus.type token, kept for
// diagnostics when the value is not recognized. Only recognized values reach a
// rendered form.
func (c Classification) ResponseStatusType() string { return c.f().responseStatusType }

// Err returns nil for OutcomeSuccess and a *Error otherwise, wrapping cause.
// OutcomeMFARequired is reported as an error too, so callers can match it with
// errors.Is(err, ErrMFARequired) and route to the OTP step.
//
// op and endpoint are typed labels: pass an Op* and an Endpoint* constant. Any
// other value renders as "unknown".
func (c Classification) Err(op Op, endpoint Endpoint, cause error) error {
	fields := c.f()
	if fields.outcome == OutcomeSuccess {
		return nil
	}
	return &Error{
		Op:         op,
		Endpoint:   endpoint,
		Status:     fields.status,
		Outcome:    fields.outcome,
		RetryAfter: fields.retryAfter,
		Err:        cause,
	}
}
