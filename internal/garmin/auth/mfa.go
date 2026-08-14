package auth

import (
	"context"
	"net/url"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// mfaVerifyRequest is the JSON body of the verifyCode APIs. The code lives in
// this value for the duration of one call and reaches no field that outlives it.
// Source: mfa_json in Client._complete_mfa (client.py, 0.3.10).
type mfaVerifyRequest struct {
	MFAMethod         string   `json:"mfaMethod"`
	Code              string   `json:"mfaVerificationCode"`
	RememberMyBrowser bool     `json:"rememberMyBrowser"`
	ReconsentList     []string `json:"reconsentList"`
	MFASetup          bool     `json:"mfaSetup"`
}

// mfaTarget is one verify endpoint, with the query and user agent that belong to
// its flow.
type mfaTarget struct {
	url       string
	query     url.Values
	endpoint  protocol.Endpoint
	userAgent string
}

// beginMFA stores the continuation state server-side and returns the capability
// the caller must present to CompleteMFA.
//
// Everything Garmin's OTP step needs — the SSO cookies, the CSRF value, the
// selected strategy, the login query, the Referer and the CAS service URL — is
// kept in the registry. Nothing goes to the client except the opaque capability.
//
// Documented gap: upstream 0.3.10 also asks Garmin to deliver an email or SMS
// code explicitly (PathWidgetRequestMFACode), driven by the widget page's inline
// JS variables (customerGuid, mfaMethod, locale, clientId, codeSentTo). That
// variable parsing is not implemented in the protocol package, so the delivery
// request is not made here either; MFADeliveryUncertain reports the uncertainty
// instead.
func (a *Authenticator) beginMFA(principal string, strategy StrategyName, step stepResult) (Result, error) {
	method := step.class.MFAMethod()
	if method == "" {
		method = protocol.MFAMethodEmail
	}

	pending := NewPending(PendingParams{
		Principal:            principal,
		Strategy:             strategy,
		MFAMethod:            method,
		MFADeliveryUncertain: step.class.MFADeliveryUncertain(),
		CSRFToken:            step.class.CSRFToken(),
		Cookies:              step.session.cookiesFor(a.hosts.SSOBase()),
		Query:                step.query,
		Referer:              step.referer,
		ServiceURL:           step.serviceURL,
	})

	transactionID, err := a.registry.Create(pending)
	if err != nil {
		return failedResult(strategy), err
	}
	return mfaPendingResult(strategy, transactionID, pending), nil
}

// CompleteMFA submits a one-time code for the transaction named by
// transactionID.
//
// The transaction must belong to principal, must not have expired, must still have
// attempts left, must not be in the middle of another completion and must not have
// been completed already; the registry enforces all five.
//
// The order of operations is the security property. Attempt takes the transaction's
// single completion lease, so a second submission of the same capability performs no
// external effect at all. The terminal success is then claimed — which re-checks the
// absolute TTL, because verification is a network call that can outlive the
// transaction — before the ticket is exchanged and before anything is saved. A
// completion that fails after the claim leaves no usable transaction, so the user
// restarts the login instead of retrying against a half-completed one. A wrong code
// releases the lease and leaves the transaction usable until its attempt budget runs
// out.
//
// code is not retained anywhere.
func (a *Authenticator) CompleteMFA(
	ctx context.Context,
	transactionID, principal, code string,
) (Result, error) {
	if principal == "" {
		return failedResult(""), ErrMissingPrincipal
	}
	if code == "" {
		return failedResult(""), ErrMissingMFACode
	}

	attempt, err := a.registry.Attempt(transactionID, principal)
	if err != nil {
		return failedResult(""), err
	}
	defer attempt.Release()

	pending := attempt.Pending()
	class, err := a.submitCode(ctx, pending, code)
	if err != nil {
		return failedResult(pending.Strategy()), err
	}

	if err := attempt.Claim(); err != nil {
		return failedResult(pending.Strategy()), err
	}
	if err := a.completeLogin(ctx, principal, class, pending.ServiceURL()); err != nil {
		// The capability is consumed, so this login cannot be resumed: the caller
		// must start a new one rather than replay the code.
		return failedResult(pending.Strategy()), err
	}
	return authenticatedResult(pending.Strategy()), nil
}

// submitCode seeds a fresh session with the pending SSO cookies and verifies the
// one-time code on the pending flow's endpoint.
func (a *Authenticator) submitCode(
	ctx context.Context,
	pending Pending,
	code string,
) (protocol.Classification, error) {
	sess, err := newSession(a.doer)
	if err != nil {
		return protocol.Classification{}, err
	}
	if err := sess.seed(a.hosts.SSOBase(), pending.Cookies()); err != nil {
		return protocol.Classification{}, err
	}
	return a.verifyCode(ctx, sess, pending, code)
}

// verifyCode submits the OTP to the verify endpoint of the pending flow.
func (a *Authenticator) verifyCode(
	ctx context.Context,
	sess *session,
	pending Pending,
	code string,
) (protocol.Classification, error) {
	if pending.Strategy() == StrategyWidget {
		return a.verifyWidgetCode(ctx, sess, pending, code)
	}
	return a.verifyJSONCode(ctx, sess, pending, code)
}

// verifyJSONCode submits the OTP to the mobile and portal verifyCode APIs in
// turn. The two share SSO cookies but sit in different rate-limit buckets, so the
// second is a real fallback rather than a retry. Source: the mfa_endpoints list
// in Client._complete_mfa.
func (a *Authenticator) verifyJSONCode(
	ctx context.Context,
	sess *session,
	pending Pending,
	code string,
) (protocol.Classification, error) {
	body := mfaVerifyRequest{
		MFAMethod:         pending.MFAMethod(),
		Code:              code,
		RememberMyBrowser: true,
		ReconsentList:     []string{},
	}

	var lastErr error
	for _, target := range a.mfaTargets(pending) {
		header := jsonLoginHeaders(a.hosts.SSOBase(), target.userAgent, pending.Referer())

		raw, err := sess.postJSON(ctx, withQuery(target.url, target.query), header, body)
		if err != nil {
			lastErr = transportError(protocol.OpVerifyMFA, target.endpoint, err)
			continue
		}

		class := protocol.ClassifyJSONLogin(a.tokens.response(raw))
		if class.Outcome() == protocol.OutcomeSuccess {
			return class, nil
		}
		lastErr = class.Err(protocol.OpVerifyMFA, target.endpoint, nil)
	}

	if lastErr == nil {
		lastErr = protocol.ErrUnknownResponse
	}
	return protocol.Classification{}, lastErr
}

// verifyWidgetCode submits the OTP to the widget's HTML verify form. Source:
// Client._complete_mfa_widget.
func (a *Authenticator) verifyWidgetCode(
	ctx context.Context,
	sess *session,
	pending Pending,
	code string,
) (protocol.Classification, error) {
	if pending.CSRFToken() == "" {
		return protocol.Classification{}, ErrMissingCSRFToken
	}

	form := url.Values{
		"mfa-code": {code},
		"embed":    {formValueEmbed},
		"_csrf":    {pending.CSRFToken()},
		"fromPage": {"setupEnterMfaCode"},
	}
	header := htmlPageHeaders(protocol.UserAgentDesktop, pending.Referer())

	raw, err := sess.postForm(ctx, withQuery(a.hosts.WidgetVerifyMFAURL(), pending.Query()), header, form)
	if err != nil {
		return protocol.Classification{}, transportError(protocol.OpVerifyMFA,
			protocol.EndpointWidgetVerifyMFA, err)
	}

	class := protocol.ClassifyWidgetLogin(a.tokens.response(raw))
	if class.Outcome() != protocol.OutcomeSuccess {
		return protocol.Classification{}, class.Err(protocol.OpVerifyMFA,
			protocol.EndpointWidgetVerifyMFA, nil)
	}
	return class, nil
}

// mfaTargets lists the verify endpoints to try, the pending flow's own first.
func (a *Authenticator) mfaTargets(pending Pending) []mfaTarget {
	mobile := mfaTarget{
		url: a.hosts.MobileMFAVerifyCodeURL(),
		query: url.Values{
			queryKeyClientID: {protocol.ClientIDIOS},
			queryKeyLocale:   {protocol.LoginLocale},
			queryKeyService:  {a.hosts.IOSServiceURL()},
		},
		endpoint:  protocol.EndpointMobileMFAVerifyCode,
		userAgent: protocol.UserAgentIOSLogin,
	}
	portal := mfaTarget{
		url: a.hosts.PortalMFAVerifyCodeURL(),
		query: url.Values{
			queryKeyClientID: {protocol.ClientIDPortal},
			queryKeyLocale:   {protocol.LoginLocale},
			queryKeyService:  {a.hosts.PortalServiceURL()},
		},
		endpoint:  protocol.EndpointPortalMFAVerifyCode,
		userAgent: protocol.UserAgentDesktop,
	}

	primary, alternate := portal, mobile
	if pending.Strategy() == StrategyMobileIOS {
		primary, alternate = mobile, portal
	}
	// Replay the query the credential POST used, so the verify call presents the
	// same client id and service URL the SSO session was created for.
	if stored := pending.Query(); len(stored) > 0 {
		primary.query = stored
	}
	return []mfaTarget{primary, alternate}
}
