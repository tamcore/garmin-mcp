package protocol

import "strings"

// Overrides replaces base URLs so tests can point every host at a local
// httptest server. It is the only way to make this package address a host that
// is not an allowlisted Garmin domain, and it is deliberately explicit: a caller
// has to name each base it redirects.
//
// Production code must leave this zero. An empty field keeps the domain-derived
// default.
type Overrides struct {
	SSO               string
	Connect           string
	ConnectAPI        string
	DIAuth            string
	MobileIntegration string
}

// Hosts builds absolute Garmin URLs for one domain. Construct it with NewHosts or
// NewHostsForValidatedDomain. The zero value is not usable: every URL it reports
// is empty.
type Hosts struct {
	domain            Domain
	sso               string
	connect           string
	connectAPI        string
	diAuth            string
	mobileIntegration string
}

// NewHosts derives the SSO, connect, connectapi, diauth and mobile-integration
// bases from domain. Source: the domain-aware host construction in
// Client.__init__ (client.py, 0.3.10).
//
// It is the strict constructor and it fails closed: only an allowlisted Domain is
// accepted, and anything else — the zero Domain, a case variant, a subdomain, an
// attacker-supplied host — is rejected with an error wrapping
// ErrUnsupportedDomain. Nothing is coerced. Silently substituting DomainGlobal
// would send a China account's credentials to the global region, so the caller
// has to see the rejection.
//
// Use ParseDomain for a caller-supplied string, NewHostsForValidatedDomain when
// the domain is already validated, and WithOverrides to redirect to a non-Garmin
// host in a test.
func NewHosts(domain Domain) (Hosts, error) {
	validated, err := domain.Validate()
	if err != nil {
		return Hosts{}, err
	}
	return NewHostsForValidatedDomain(validated), nil
}

// NewHostsForValidatedDomain derives the bases from a domain whose allowlist
// membership is already proven, so it cannot fail on a hostile host and needs no
// error return.
//
// A zero ValidatedDomain carries no region. It yields the zero Hosts, whose base
// URLs and endpoint URLs are all empty: an empty URL cannot be requested, so the
// failure is closed rather than aimed at a default region. Reaching that state is
// a programming error — a ValidatedDomain that was never obtained from ParseDomain
// or Domain.Validate — not caller input.
func NewHostsForValidatedDomain(validated ValidatedDomain) Hosts {
	if !validated.IsValid() {
		return Hosts{}
	}
	d := validated.Domain()
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
// The receiver is not modified. See Overrides: this is the test-only escape hatch
// to a non-Garmin host.
func (h Hosts) WithOverrides(o Overrides) Hosts {
	out := h
	out.sso = pick(o.SSO, h.sso)
	out.connect = pick(o.Connect, h.connect)
	out.connectAPI = pick(o.ConnectAPI, h.connectAPI)
	out.diAuth = pick(o.DIAuth, h.diAuth)
	out.mobileIntegration = pick(o.MobileIntegration, h.mobileIntegration)
	return out
}

// Domain reports the allowlisted domain the hosts were derived from. It is
// unaffected by WithOverrides, which replaces base URLs rather than the region.
func (h Hosts) Domain() Domain { return h.domain }

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

// WidgetRequestMFACodeURL asks Garmin to deliver an email or SMS OTP for a
// widget session. See PathWidgetRequestMFACode for the documented gap.
func (h Hosts) WidgetRequestMFACodeURL() string { return join(h.sso, PathWidgetRequestMFACode) }

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

// join builds an absolute URL. An empty base yields an empty URL rather than a
// bare path, so a zero Hosts cannot produce something a client would resolve
// against a host of its own choosing.
func join(base, path string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + path
}
