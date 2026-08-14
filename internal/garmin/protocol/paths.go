package protocol

// SSO and API paths. Source: the request URLs built in Client's login strategies
// in python-garminconnect 0.3.10, file garminconnect/client.py.
const (
	// PathMobileLogin is the iOS/mobile JSON login API.
	// Source: Client._do_mobile_login, f"{self._sso}/mobile/api/login".
	PathMobileLogin = "/mobile/api/login"
	// PathPortalLogin is the desktop portal JSON login API.
	// Source: Client._do_portal_web_login, f"{self._sso}/portal/api/login".
	PathPortalLogin = "/portal/api/login"
	// PathPortalSignInPage is the portal HTML page fetched for initial cookies.
	// Source: Client._do_portal_web_login step 1, signin_url.
	PathPortalSignInPage = "/portal/sso/en-US/sign-in"
	// PathWidgetEmbed is the embedded SSO widget page.
	// Source: Client._widget_web_login, sso_embed = f"{sso_base}/embed".
	PathWidgetEmbed = "/sso/embed"
	// PathWidgetSignIn is the widget HTML sign-in form (GET for CSRF, POST for
	// credentials). Source: Client._widget_web_login steps 2 and 3.
	PathWidgetSignIn = "/sso/signin"
	// PathMobileMFAVerifyCode verifies an OTP for the mobile/iOS JSON flow.
	// Source: Client._complete_mfa, f"{self._sso}/{flow_path}/api/mfa/verifyCode"
	// with flow_path "mobile".
	PathMobileMFAVerifyCode = "/mobile/api/mfa/verifyCode"
	// PathPortalMFAVerifyCode verifies an OTP for the portal JSON flow, and is
	// also the cross-bucket fallback endpoint. Source: Client._complete_mfa,
	// alt_endpoint.
	PathPortalMFAVerifyCode = "/portal/api/mfa/verifyCode"
	// PathWidgetVerifyMFA verifies an OTP for the widget HTML flow.
	// Source: Client._complete_mfa_widget.
	PathWidgetVerifyMFA = "/sso/verifyMFA/loginEnterMfaCode"
	// PathWidgetRequestMFACode asks Garmin to deliver an email or SMS OTP for a
	// widget session, which the credential POST does not reliably trigger. New in
	// 0.3.10 ("explicitly request widget MFA code delivery", GH-386).
	// Source: Client._widget_request_mfa_code.
	//
	// Documented gap: the classifier work this endpoint belongs to is not
	// implemented here. Upstream parses the widget page's inline JS variables
	// (customerGuid, mfaMethod, locale, clientId, codeSentTo) via
	// _parse_widget_mfa_vars, requests delivery only for "email" and "sms" and
	// only when codeSentTo is empty, and uses those variables as the JSON body.
	// Only the wire constant is ported.
	PathWidgetRequestMFACode = "/sso/verifyMFA/mfaCode"
	// PathDIToken is the DI OAuth2 token endpoint on the diauth host.
	// Source: DI_TOKEN_URL / Client._di_token_url.
	PathDIToken = "/di-oauth2-service/oauth/token"
	// PathSocialProfile validates a candidate session on the API tier. Source:
	// Client._verify_token, connectapi("/userprofile-service/socialProfile").
	PathSocialProfile = "/userprofile-service/socialProfile"
	// PathIOSService is the iOS CAS service path on the mobile integration host.
	// Source: IOS_SERVICE_URL / Client._ios_service_url.
	PathIOSService = "/gcm/ios"
	// PathPortalService is the portal CAS service path on the connect host.
	// Source: PORTAL_SSO_SERVICE_URL / Client._portal_service_url.
	PathPortalService = "/app"
)

// Endpoint is a sanitized endpoint label for logs, metrics and errors. Only the
// Endpoint* constants below are labels: a value built from any other string is
// not recognized and renders as "unknown", so a URL, query string or header can
// never reach a rendered message through this type.
type Endpoint string

// Sanitized endpoint labels. They never contain a host, credential or query
// string.
const (
	EndpointMobileLogin          Endpoint = "sso.mobile.login"
	EndpointPortalLogin          Endpoint = "sso.portal.login"
	EndpointPortalSignInPage     Endpoint = "sso.portal.signin_page"
	EndpointWidgetEmbed          Endpoint = "sso.widget.embed"
	EndpointWidgetSignIn         Endpoint = "sso.widget.signin"
	EndpointMobileMFAVerifyCode  Endpoint = "sso.mobile.mfa.verify_code"
	EndpointPortalMFAVerifyCode  Endpoint = "sso.portal.mfa.verify_code"
	EndpointWidgetVerifyMFA      Endpoint = "sso.widget.mfa.verify_code"
	EndpointWidgetRequestMFACode Endpoint = "sso.widget.mfa.request_code"
	EndpointDIToken              Endpoint = "diauth.oauth.token"
	EndpointSocialProfile        Endpoint = "connectapi.userprofile.social_profile"
)

var knownEndpoints = [...]Endpoint{
	EndpointMobileLogin,
	EndpointPortalLogin,
	EndpointPortalSignInPage,
	EndpointWidgetEmbed,
	EndpointWidgetSignIn,
	EndpointMobileMFAVerifyCode,
	EndpointPortalMFAVerifyCode,
	EndpointWidgetVerifyMFA,
	EndpointWidgetRequestMFACode,
	EndpointDIToken,
	EndpointSocialProfile,
}

// IsKnown reports whether e is one of the package's Endpoint constants.
func (e Endpoint) IsKnown() bool {
	for _, known := range knownEndpoints {
		if e == known {
			return true
		}
	}
	return false
}

// String returns the label, or "unknown" for a value that is not a package
// constant.
func (e Endpoint) String() string {
	if !e.IsKnown() {
		return labelUnknown
	}
	return string(e)
}

// Op is a sanitized label for the logical operation that failed. Like Endpoint,
// only the Op* constants below are labels.
type Op string

// Sanitized operation labels, one per step of the login and token lifecycle.
// Source: the strategy names and helper methods in Client.login.
const (
	// OpMobileLogin is the iOS/mobile JSON credential POST (strategies 1 and 2).
	OpMobileLogin Op = "mobile_login"
	// OpPortalLogin is the desktop portal JSON credential POST (strategies 4 and 5).
	OpPortalLogin Op = "portal_login"
	// OpWidgetLogin is the embedded widget credential POST (strategy 3).
	OpWidgetLogin Op = "widget_login"
	// OpWidgetSignInPage is a widget embed or sign-in GET, fetched for cookies
	// and the CSRF token.
	OpWidgetSignInPage Op = "widget_signin_page"
	// OpVerifyMFA submits an OTP to a verify endpoint.
	OpVerifyMFA Op = "verify_mfa"
	// OpRequestMFACode asks Garmin to deliver an email or SMS OTP.
	OpRequestMFACode Op = "request_mfa_code"
	// OpExchangeServiceTicket trades a CAS service ticket for a DI token.
	OpExchangeServiceTicket Op = "exchange_service_ticket"
	// OpRefreshToken refreshes an existing DI token.
	OpRefreshToken Op = "refresh_token"
	// OpValidateSession checks a candidate session against the API tier.
	OpValidateSession Op = "validate_session"
)

var knownOps = [...]Op{
	OpMobileLogin,
	OpPortalLogin,
	OpWidgetLogin,
	OpWidgetSignInPage,
	OpVerifyMFA,
	OpRequestMFACode,
	OpExchangeServiceTicket,
	OpRefreshToken,
	OpValidateSession,
}

// IsKnown reports whether o is one of the package's Op constants.
func (o Op) IsKnown() bool {
	for _, known := range knownOps {
		if o == known {
			return true
		}
	}
	return false
}

// String returns the label, or "unknown" for a value that is not a package
// constant.
func (o Op) String() string {
	if !o.IsKnown() {
		return labelUnknown
	}
	return string(o)
}
