package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
)

// PendingParams is the server-side material an MFA continuation needs. It is
// consumed by NewPending, which copies everything, so the caller may drop its
// value straight afterwards.
//
// It deliberately has no password and no code field: neither is ever kept.
type PendingParams struct {
	// Principal is the account the transaction belongs to. Every later
	// transition must present the same value.
	Principal string
	// Strategy is the login flow that reached the MFA challenge. The OTP must be
	// submitted to that flow's verify endpoint.
	Strategy StrategyName
	// MFAMethod is the delivery method Garmin named ("email", "sms", ...). It is
	// server-controlled text.
	MFAMethod string
	// MFADeliveryUncertain marks an MFA challenge scraped from HTML, where code
	// delivery is not confirmed.
	MFADeliveryUncertain bool
	// CSRFToken is the widget form token needed by the OTP POST. It is a
	// credential.
	CSRFToken string
	// Cookies is the SSO session the credential POST established. Every cookie
	// value is a credential.
	Cookies []*http.Cookie
	// Query is the login query the flow used, replayed on the verify call.
	Query url.Values
	// Referer is the page the OTP form was served from, replayed as a header.
	Referer string
	// ServiceURL is the CAS service URL the ticket must be issued for. It must
	// match the one used during the credential POST.
	ServiceURL string
}

// Pending is the immutable, server-side continuation state of one MFA login.
//
// It is secret-bearing: the cookies and the CSRF token are credentials. Like
// TokenSet it keeps them behind a pointer to unexported fields, so a reflective
// logger, a direct field print and a method-stripping alias cannot read them,
// and String, GoString, MarshalJSON and LogValue report shape only.
//
// It holds no password and no OTP. The state machine inside it starts in
// StateMFAPending.
type Pending struct {
	// secrets is a pointer on purpose; see TokenSet.secrets.
	secrets *pendingSecrets
}

type pendingSecrets struct {
	// principal is account-identifying data and csrfToken is a credential, so both
	// are sealed behind a pointer; see secretString. The cookies are already one
	// indirection away, because they are stored as pointers.
	principal            *secretString
	strategy             StrategyName
	machine              Machine
	mfaMethod            string
	mfaDeliveryUncertain bool
	csrfToken            *secretString
	cookies              []*http.Cookie
	query                url.Values
	referer              string
	serviceURL           string
}

// NewPending seals params into a Pending whose machine is already in
// StateMFAPending, reached through the permitted transitions. Cookies and query
// values are deep-copied, so the caller cannot mutate stored state afterwards.
func NewPending(params PendingParams) Pending {
	machine := NewMachine()
	if submitted, err := machine.SubmitCredentials(params.Strategy); err == nil {
		machine = submitted
	}
	if challenged, err := machine.RequireMFA(params.MFAMethod); err == nil {
		machine = challenged
	}

	return Pending{secrets: &pendingSecrets{
		principal:            sealSecret(params.Principal),
		strategy:             params.Strategy,
		machine:              machine,
		mfaMethod:            params.MFAMethod,
		mfaDeliveryUncertain: params.MFADeliveryUncertain,
		csrfToken:            sealSecret(params.CSRFToken),
		cookies:              copyCookies(params.Cookies),
		query:                copyValues(params.Query),
		referer:              params.Referer,
		serviceURL:           params.ServiceURL,
	}}
}

func (p Pending) s() pendingSecrets {
	if p.secrets == nil {
		return pendingSecrets{}
	}
	return *p.secrets
}

// Principal is the account the transaction belongs to.
func (p Pending) Principal() string { return revealSecret(p.s().principal) }

// Strategy is the login flow that reached the MFA challenge.
func (p Pending) Strategy() StrategyName { return p.s().strategy }

// State is the transaction's login state.
func (p Pending) State() State { return p.s().machine.State() }

// MFAMethod is the delivery method Garmin named.
func (p Pending) MFAMethod() string { return p.s().mfaMethod }

// MFADeliveryUncertain reports an MFA challenge whose code delivery is not
// confirmed.
func (p Pending) MFADeliveryUncertain() bool { return p.s().mfaDeliveryUncertain }

// CSRFToken is the widget form token. It is a credential: never log it.
func (p Pending) CSRFToken() string { return revealSecret(p.s().csrfToken) }

// Cookies returns a deep copy of the SSO session cookies. Every value is a
// credential.
func (p Pending) Cookies() []*http.Cookie { return copyCookies(p.s().cookies) }

// Query returns a copy of the login query replayed on the verify call.
func (p Pending) Query() url.Values { return copyValues(p.s().query) }

// Referer is the page the OTP form was served from.
func (p Pending) Referer() string { return p.s().referer }

// ServiceURL is the CAS service URL the ticket must be issued for.
func (p Pending) ServiceURL() string { return p.s().serviceURL }

// storedBytes reports how many bytes of continuation state p holds. The registry
// bounds it, so one login cannot park an unbounded SSO response in memory. Cookie
// and query metadata counts, because it is stored too.
func (p Pending) storedBytes() int {
	secrets := p.s()

	total := len(revealSecret(secrets.principal)) + len(secrets.mfaMethod) +
		len(revealSecret(secrets.csrfToken)) + len(secrets.referer) +
		len(secrets.serviceURL) + len(secrets.strategy)
	for _, cookie := range secrets.cookies {
		total += len(cookie.Name) + len(cookie.Value) + len(cookie.Domain) + len(cookie.Path)
	}
	for key, values := range secrets.query {
		total += len(key)
		for _, value := range values {
			total += len(value)
		}
	}
	return total
}

// withMachine returns a copy of p carrying machine. The receiver is unchanged.
func (p Pending) withMachine(machine Machine) Pending {
	next := p.s()
	next.machine = machine
	return Pending{secrets: &next}
}

// machineValue returns the embedded state machine.
func (p Pending) machineValue() Machine { return p.s().machine }

func copyCookies(in []*http.Cookie) []*http.Cookie {
	if len(in) == 0 {
		return nil
	}
	out := make([]*http.Cookie, 0, len(in))
	for _, cookie := range in {
		if cookie == nil {
			continue
		}
		clone := *cookie
		out = append(out, &clone)
	}
	return out
}

func copyValues(in url.Values) url.Values {
	if len(in) == 0 {
		return nil
	}
	out := make(url.Values, len(in))
	for key, values := range in {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return out
}

// redactedPending is the only shape a Pending is ever rendered in. It reports
// the shape of the transaction, never a cookie or a token.
type redactedPending struct {
	Type          string `json:"type"`
	State         string `json:"state"`
	Strategy      string `json:"strategy"`
	MFAMethod     string `json:"mfaMethod,omitempty"`
	HasCSRFToken  bool   `json:"csrfTokenPresent"`
	CookieCount   int    `json:"cookieCount"`
	QueryKeyCount int    `json:"queryKeyCount"`
}

func (p Pending) redacted() redactedPending {
	secrets := p.s()
	return redactedPending{
		Type:          "auth.Pending",
		State:         secrets.machine.State().String(),
		Strategy:      secrets.strategy.String(),
		MFAMethod:     knownMFAMethod(secrets.mfaMethod),
		HasCSRFToken:  secrets.csrfToken != nil,
		CookieCount:   len(secrets.cookies),
		QueryKeyCount: len(secrets.query),
	}
}

// String renders a Pending without its cookies or CSRF token. The principal is
// omitted too: it is account-identifying data.
func (p Pending) String() string {
	red := p.redacted()
	return "auth.Pending{state:" + red.State +
		" strategy:" + red.Strategy +
		" mfaMethod:" + quoteLabel(red.MFAMethod) +
		" csrfToken:" + presence(red.HasCSRFToken) +
		" cookieCount:" + strconv.Itoa(red.CookieCount) +
		" queryKeyCount:" + strconv.Itoa(red.QueryKeyCount) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (p Pending) GoString() string { return p.String() }

// MarshalJSON serializes the redacted form.
func (p Pending) MarshalJSON() ([]byte, error) { return json.Marshal(p.redacted()) }

// LogValue implements slog.LogValuer.
func (p Pending) LogValue() slog.Value {
	red := p.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.String("state", red.State),
		slog.String("strategy", red.Strategy),
		slog.String("mfaMethod", red.MFAMethod),
		slog.Bool("csrfTokenPresent", red.HasCSRFToken),
		slog.Int("cookieCount", red.CookieCount),
		slog.Int("queryKeyCount", red.QueryKeyCount),
	)
}

// knownMFAMethod renders only the delivery methods this package recognizes.
// Garmin controls the value, so anything else is reported as "other" rather than
// echoed into a log line.
func knownMFAMethod(value string) string {
	switch value {
	case "":
		return ""
	case "email", "sms", "totp", "app":
		return value
	default:
		return "other"
	}
}
