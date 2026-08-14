// Package protocol holds the Garmin Connect wire identifiers (hosts, paths,
// client identities, user agents, pacing bounds) and the login response
// classifier.
//
// Every constant carries a source comment naming the upstream behavior it
// reproduces. The reference is python-garminconnect 0.3.8
// (commit e4e9748cf3fa62f997e77171addee3acc333232c), file garminconnect/client.py.
//
// The package performs no I/O and holds no mutable package-level state. All
// values are immutable: methods return new values instead of mutating their
// receiver.
package protocol

import "strings"

// Domain is the Garmin host suffix that selects an account region.
type Domain string

const (
	// DomainGlobal is the default region. Source: Client.__init__ default
	// argument domain="garmin.com".
	DomainGlobal Domain = "garmin.com"

	// DomainChina serves accounts in the Garmin China user database. Source:
	// the domain-aware host construction in Client.__init__, which notes that
	// CN users do not exist in the .com user database.
	DomainChina Domain = "garmin.cn"
)

// Normalize lowercases and trims d, defaulting to DomainGlobal when empty.
func (d Domain) Normalize() Domain {
	trimmed := strings.ToLower(strings.TrimSpace(string(d)))
	if trimmed == "" {
		return DomainGlobal
	}
	return Domain(trimmed)
}

// SSO and API paths. Source: the request URLs built in Client's login strategies.
const (
	// PathMobileLogin is the iOS/mobile JSON login API. Source: _do_mobile_login.
	PathMobileLogin = "/mobile/api/login"
	// PathPortalLogin is the desktop portal JSON login API. Source: _do_portal_web_login.
	PathPortalLogin = "/portal/api/login"
	// PathPortalSignInPage is the portal HTML page fetched for initial cookies.
	// Source: _do_portal_web_login step 1.
	PathPortalSignInPage = "/portal/sso/en-US/sign-in"
	// PathWidgetEmbed is the embedded SSO widget page. Source: _widget_web_login step 1.
	PathWidgetEmbed = "/sso/embed"
	// PathWidgetSignIn is the widget HTML sign-in form (GET for CSRF, POST for
	// credentials). Source: _widget_web_login steps 2 and 3.
	PathWidgetSignIn = "/sso/signin"
	// PathMobileMFAVerifyCode verifies an OTP for the mobile/iOS JSON flow.
	// Source: _complete_mfa, flow_path "mobile".
	PathMobileMFAVerifyCode = "/mobile/api/mfa/verifyCode"
	// PathPortalMFAVerifyCode verifies an OTP for the portal JSON flow, and is
	// also the cross-bucket fallback endpoint. Source: _complete_mfa.
	PathPortalMFAVerifyCode = "/portal/api/mfa/verifyCode"
	// PathWidgetVerifyMFA verifies an OTP for the widget HTML flow.
	// Source: _complete_mfa_widget.
	PathWidgetVerifyMFA = "/sso/verifyMFA/loginEnterMfaCode"
	// PathDIToken is the DI OAuth2 token endpoint on the diauth host.
	// Source: DI_TOKEN_URL / Client._di_token_url.
	PathDIToken = "/di-oauth2-service/oauth/token"
	// PathSocialProfile validates a candidate session on the API tier.
	// Source: Client._verify_token.
	PathSocialProfile = "/userprofile-service/socialProfile"
	// PathIOSService is the iOS CAS service path on the mobile integration host.
	// Source: Client._ios_service_url.
	PathIOSService = "/gcm/ios"
	// PathPortalService is the portal CAS service path on the connect host.
	// Source: Client._portal_service_url.
	PathPortalService = "/app"
)

// Sanitized endpoint labels for logs, metrics and errors. They never contain a
// host, credential or query string.
const (
	EndpointMobileLogin         = "sso.mobile.login"
	EndpointPortalLogin         = "sso.portal.login"
	EndpointPortalSignInPage    = "sso.portal.signin_page"
	EndpointWidgetEmbed         = "sso.widget.embed"
	EndpointWidgetSignIn        = "sso.widget.signin"
	EndpointMobileMFAVerifyCode = "sso.mobile.mfa.verify_code"
	EndpointPortalMFAVerifyCode = "sso.portal.mfa.verify_code"
	EndpointWidgetVerifyMFA     = "sso.widget.mfa.verify_code"
	EndpointDIToken             = "diauth.oauth.token"
	EndpointSocialProfile       = "connectapi.userprofile.social_profile"
)

// Overrides replaces base URLs, so tests can point every host at a local
// httptest server. An empty field keeps the domain-derived default.
type Overrides struct {
	SSO               string
	Connect           string
	ConnectAPI        string
	DIAuth            string
	MobileIntegration string
}

// Hosts builds absolute Garmin URLs for one domain. The zero value is not
// usable; construct it with NewHosts.
type Hosts struct {
	domain            Domain
	sso               string
	connect           string
	connectAPI        string
	diAuth            string
	mobileIntegration string
}

// NewHosts derives the SSO, connect, connectapi, diauth and mobile-integration
// bases from domain. Source: the host construction in Client.__init__.
func NewHosts(domain Domain) Hosts {
	d := domain.Normalize()
	return Hosts{
		domain:            d,
		sso:               "https://sso." + string(d),
		connect:           "https://connect." + string(d),
		connectAPI:        "https://connectapi." + string(d),
		diAuth:            "https://diauth." + string(d),
		mobileIntegration: "https://mobile.integration." + string(d),
	}
}

// WithOverrides returns a copy of h with the non-empty override bases applied.
// The receiver is not modified.
func (h Hosts) WithOverrides(o Overrides) Hosts {
	out := h
	out.sso = pick(o.SSO, h.sso)
	out.connect = pick(o.Connect, h.connect)
	out.connectAPI = pick(o.ConnectAPI, h.connectAPI)
	out.diAuth = pick(o.DIAuth, h.diAuth)
	out.mobileIntegration = pick(o.MobileIntegration, h.mobileIntegration)
	return out
}

// Domain reports the normalized domain the hosts were derived from.
func (h Hosts) Domain() Domain { return h.domain.Normalize() }

// SSOBase is the single sign-on base URL, without a trailing slash.
func (h Hosts) SSOBase() string { return h.sso }

// ConnectBase is the web front-end base URL, without a trailing slash.
func (h Hosts) ConnectBase() string { return h.connect }

// ConnectAPIBase is the API tier base URL, without a trailing slash.
func (h Hosts) ConnectAPIBase() string { return h.connectAPI }

// DIAuthBase is the DI OAuth2 base URL, without a trailing slash.
func (h Hosts) DIAuthBase() string { return h.diAuth }

// MobileIntegrationBase is the CAS service host for the mobile app flows.
func (h Hosts) MobileIntegrationBase() string { return h.mobileIntegration }

// MobileLoginURL is the iOS JSON login endpoint. Credentials belong in the JSON
// body, never in the query string.
func (h Hosts) MobileLoginURL() string { return join(h.sso, PathMobileLogin) }

// PortalLoginURL is the portal JSON login endpoint.
func (h Hosts) PortalLoginURL() string { return join(h.sso, PathPortalLogin) }

// PortalSignInPageURL is the portal HTML page that seeds session cookies.
func (h Hosts) PortalSignInPageURL() string { return join(h.sso, PathPortalSignInPage) }

// WidgetEmbedURL is the embedded SSO widget page.
func (h Hosts) WidgetEmbedURL() string { return join(h.sso, PathWidgetEmbed) }

// WidgetSignInURL is the widget HTML sign-in form.
func (h Hosts) WidgetSignInURL() string { return join(h.sso, PathWidgetSignIn) }

// MobileMFAVerifyCodeURL verifies an OTP in the mobile/iOS JSON flow.
func (h Hosts) MobileMFAVerifyCodeURL() string { return join(h.sso, PathMobileMFAVerifyCode) }

// PortalMFAVerifyCodeURL verifies an OTP in the portal JSON flow.
func (h Hosts) PortalMFAVerifyCodeURL() string { return join(h.sso, PathPortalMFAVerifyCode) }

// WidgetVerifyMFAURL verifies an OTP in the widget HTML flow.
func (h Hosts) WidgetVerifyMFAURL() string { return join(h.sso, PathWidgetVerifyMFA) }

// DITokenURL is the DI OAuth2 token endpoint.
func (h Hosts) DITokenURL() string { return join(h.diAuth, PathDIToken) }

// SocialProfileURL validates a candidate session against the API tier.
func (h Hosts) SocialProfileURL() string { return join(h.connectAPI, PathSocialProfile) }

// IOSServiceURL is the CAS service URL presented by the iOS login flow.
func (h Hosts) IOSServiceURL() string { return join(h.mobileIntegration, PathIOSService) }

// PortalServiceURL is the CAS service URL presented by the portal login flow.
func (h Hosts) PortalServiceURL() string { return join(h.connect, PathPortalService) }

// WidgetServiceURL is the CAS service URL used by the widget flow, which is the
// embed page itself. Source: _widget_web_login's _establish_session call.
func (h Hosts) WidgetServiceURL() string { return h.WidgetEmbedURL() }

func pick(override, fallback string) string {
	if override == "" {
		return fallback
	}
	return strings.TrimRight(override, "/")
}

func join(base, path string) string {
	return strings.TrimRight(base, "/") + path
}
