package auth

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Query keys and form values shared by the login and MFA flows. Source: the
// params and data dictionaries in client.py (0.3.10).
const (
	queryKeyClientID = "clientId"
	queryKeyLocale   = "locale"
	queryKeyService  = "service"
	// formValueEmbed marks a widget request as coming from the embedded widget.
	formValueEmbed = "true"
)

// labelUnknown is what an unrecognized label renders as, so caller-supplied text
// can never reach a log line through a String method.
const labelUnknown = "unknown"

// stepResult is what one credential submission produced: the classified verdict
// plus everything an MFA continuation would need to resume the same SSO session.
type stepResult struct {
	session    *session
	class      protocol.Classification
	query      url.Values
	referer    string
	serviceURL string
}

// loginRequest is the JSON credential body of the mobile and portal APIs. It is
// built, marshaled and dropped inside one call, so no password reaches a field
// that outlives the request. Source: the json= payload in _do_mobile_login and
// _do_portal_web_login (client.py, 0.3.10).
type loginRequest struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	RememberMe   bool   `json:"rememberMe"`
	CaptchaToken string `json:"captchaToken"`
}

// jsonLoginSpec parameterizes the two JSON login flows, which differ only in
// client identity, service URL, user agent and whether a page GET precedes the
// credential POST.
type jsonLoginSpec struct {
	strategy     StrategyName
	clientID     string
	serviceURL   string
	loginURL     string
	userAgent    string
	pageURL      string
	pageOp       protocol.Op
	pageEndpoint protocol.Endpoint
}

// runStrategy performs one strategy's credential submission.
func (a *Authenticator) runStrategy(
	ctx context.Context,
	strategy StrategyName,
	creds Credentials,
) (stepResult, error) {
	switch strategy {
	case StrategyMobileIOS, StrategyPortal:
		return a.runJSONLogin(ctx, a.jsonSpec(strategy), creds)
	case StrategyWidget:
		return a.runWidgetLogin(ctx, creds)
	default:
		return stepResult{}, ErrNotConfigured
	}
}

// jsonSpec describes the mobile or portal JSON flow.
//
// Source: _do_mobile_login, which posts straight to /mobile/api/login with the
// iOS client identity, and _do_portal_web_login, which first GETs the portal
// sign-in page for cookies and then posts to /portal/api/login.
func (a *Authenticator) jsonSpec(strategy StrategyName) jsonLoginSpec {
	if strategy == StrategyMobileIOS {
		return jsonLoginSpec{
			strategy:   StrategyMobileIOS,
			clientID:   protocol.ClientIDIOS,
			serviceURL: a.hosts.IOSServiceURL(),
			loginURL:   a.hosts.MobileLoginURL(),
			userAgent:  protocol.UserAgentIOSLogin,
		}
	}
	return jsonLoginSpec{
		strategy:     StrategyPortal,
		clientID:     protocol.ClientIDPortal,
		serviceURL:   a.hosts.PortalServiceURL(),
		loginURL:     a.hosts.PortalLoginURL(),
		userAgent:    protocol.UserAgentDesktop,
		pageURL:      a.hosts.PortalSignInPageURL(),
		pageOp:       protocol.OpPortalLogin,
		pageEndpoint: protocol.EndpointPortalSignInPage,
	}
}

// runJSONLogin runs a JSON credential POST, preceded by a page GET and the
// anti-WAF delay when the flow calls for one.
func (a *Authenticator) runJSONLogin(
	ctx context.Context,
	spec jsonLoginSpec,
	creds Credentials,
) (stepResult, error) {
	sess, err := newSession(a.doer)
	if err != nil {
		return stepResult{}, err
	}

	referer, err := a.fetchLoginPage(ctx, sess, spec)
	if err != nil {
		return stepResult{}, err
	}

	query := url.Values{
		queryKeyClientID: {spec.clientID},
		queryKeyLocale:   {protocol.LoginLocale},
		queryKeyService:  {spec.serviceURL},
	}
	body := loginRequest{
		Username:   creds.Email(),
		Password:   creds.Password(),
		RememberMe: true,
	}

	raw, err := sess.postJSON(ctx, withQuery(spec.loginURL, query),
		jsonLoginHeaders(a.hosts.SSOBase(), spec.userAgent, referer), body)
	if err != nil {
		return stepResult{}, transportError(spec.strategy.loginOp(), spec.strategy.loginEndpoint(), err)
	}

	return stepResult{
		session:    sess,
		class:      protocol.ClassifyJSONLogin(a.tokens.response(raw)),
		query:      query,
		referer:    referer,
		serviceURL: spec.serviceURL,
	}, nil
}

// fetchLoginPage GETs the flow's sign-in page for initial cookies and then waits
// out the anti-WAF delay. It returns the Referer for the credential POST, or ""
// for a flow that posts straight to the API.
func (a *Authenticator) fetchLoginPage(
	ctx context.Context,
	sess *session,
	spec jsonLoginSpec,
) (string, error) {
	if spec.pageURL == "" {
		return "", nil
	}

	pageURL := withQuery(spec.pageURL, url.Values{
		queryKeyClientID: {spec.clientID},
		queryKeyService:  {spec.serviceURL},
	})

	raw, err := sess.get(ctx, pageURL, htmlPageHeaders(spec.userAgent, ""))
	if err != nil {
		return "", transportError(spec.pageOp, spec.pageEndpoint, err)
	}
	if err := statusError(spec.pageOp, spec.pageEndpoint, a.tokens.response(raw)); err != nil {
		return "", err
	}

	a.pace(spec.strategy)
	return pageURL, nil
}

// runWidgetLogin runs the embedded SSO widget HTML flow: embed GET for cookies,
// sign-in GET for the CSRF token, the anti-WAF delay, then the credential POST.
// Source: _widget_web_login (client.py, 0.3.10).
func (a *Authenticator) runWidgetLogin(ctx context.Context, creds Credentials) (stepResult, error) {
	sess, err := newSession(a.doer)
	if err != nil {
		return stepResult{}, err
	}

	embedURL := withQuery(a.hosts.WidgetEmbedURL(), widgetEmbedQuery(a.hosts))
	raw, err := sess.get(ctx, embedURL, htmlPageHeaders(protocol.UserAgentDesktop, ""))
	if err != nil {
		return stepResult{}, transportError(protocol.OpWidgetSignInPage, protocol.EndpointWidgetEmbed, err)
	}
	if err := statusError(protocol.OpWidgetSignInPage, protocol.EndpointWidgetEmbed,
		a.tokens.response(raw)); err != nil {
		return stepResult{}, err
	}

	signInQuery := widgetSignInQuery(a.hosts)
	signInURL := withQuery(a.hosts.WidgetSignInURL(), signInQuery)
	csrf, err := a.widgetCSRF(ctx, sess, signInURL, embedURL)
	if err != nil {
		return stepResult{}, err
	}

	a.pace(StrategyWidget)

	form := url.Values{
		"username": {creds.Email()},
		"password": {creds.Password()},
		"embed":    {formValueEmbed},
		"_csrf":    {csrf},
	}
	raw, err = sess.postForm(ctx, signInURL, htmlPageHeaders(protocol.UserAgentDesktop, signInURL), form)
	if err != nil {
		return stepResult{}, transportError(protocol.OpWidgetLogin, protocol.EndpointWidgetSignIn, err)
	}

	return stepResult{
		session:    sess,
		class:      protocol.ClassifyWidgetLogin(a.tokens.response(raw)),
		query:      signInQuery,
		referer:    signInURL,
		serviceURL: a.hosts.WidgetServiceURL(),
	}, nil
}

// widgetCSRF GETs the widget sign-in page and returns its _csrf form value.
func (a *Authenticator) widgetCSRF(
	ctx context.Context,
	sess *session,
	signInURL, embedURL string,
) (string, error) {
	raw, err := sess.get(ctx, signInURL, htmlPageHeaders(protocol.UserAgentDesktop, embedURL))
	if err != nil {
		return "", transportError(protocol.OpWidgetSignInPage, protocol.EndpointWidgetSignIn, err)
	}

	class := protocol.ClassifyWidgetSignInPage(a.tokens.response(raw))
	if err := class.Err(protocol.OpWidgetSignInPage, protocol.EndpointWidgetSignIn, nil); err != nil {
		return "", err
	}
	if class.CSRFToken() == "" {
		return "", ErrMissingCSRFToken
	}
	return class.CSRFToken(), nil
}

// pace waits out the strategy's anti-WAF delay between its page GET and its
// credential POST, using the injected sleeper and jitter so a test never waits.
func (a *Authenticator) pace(strategy StrategyName) {
	minDelay, maxDelay, paced := strategy.PacingBounds()
	if !paced {
		return
	}
	a.sleeper.Sleep(a.jitter(minDelay, maxDelay))
}

// widgetBase is the /sso base the widget query parameters point at.
func widgetBase(hosts protocol.Hosts) string {
	return strings.TrimSuffix(hosts.WidgetEmbedURL(), "/embed")
}

// widgetEmbedQuery is the embed page query. Source: embed_params in
// _widget_web_login.
func widgetEmbedQuery(hosts protocol.Hosts) url.Values {
	return url.Values{
		"id":          {"gauth-widget"},
		"embedWidget": {formValueEmbed},
		"gauthHost":   {widgetBase(hosts)},
	}
}

// widgetSignInQuery is the sign-in page query. Source: signin_params in
// _widget_web_login, which points every host, service and redirect parameter at
// the embed page.
func widgetSignInQuery(hosts protocol.Hosts) url.Values {
	embed := hosts.WidgetEmbedURL()
	return url.Values{
		"id":                              {"gauth-widget"},
		"embedWidget":                     {formValueEmbed},
		"gauthHost":                       {embed},
		queryKeyService:                   {embed},
		"source":                          {embed},
		"redirectAfterAccountLoginUrl":    {embed},
		"redirectAfterAccountCreationUrl": {embed},
	}
}

// jsonLoginHeaders are the headers the JSON login and MFA APIs expect. Source:
// login_headers in _do_mobile_login and post_headers in _do_portal_web_login.
func jsonLoginHeaders(origin, userAgent, referer string) http.Header {
	header := make(http.Header, 5)
	header.Set("User-Agent", userAgent)
	header.Set("Accept", "application/json, text/plain, */*")
	header.Set("Accept-Language", "en-US,en;q=0.9")
	header.Set("Origin", origin)
	if referer != "" {
		header.Set("Referer", referer)
	}
	return header
}

// htmlPageHeaders are the headers a browser-like page fetch sends. Source: the
// browser header set in _random_browser_headers and the widget GETs.
func htmlPageHeaders(userAgent, referer string) http.Header {
	header := make(http.Header, 4)
	header.Set("User-Agent", userAgent)
	header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		header.Set("Referer", referer)
	}
	return header
}
