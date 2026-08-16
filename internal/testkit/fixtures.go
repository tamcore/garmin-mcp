package testkit

import (
	"net/http"
	"strconv"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Response media types used by the scripted behaviors.
const (
	// ContentTypeJSON is the media type of the JSON login and token endpoints.
	ContentTypeJSON = "application/json"
	// ContentTypeHTML is the media type of the widget and portal HTML pages.
	ContentTypeHTML = "text/html;charset=UTF-8"
)

// Widget page titles the classifier keys on. They are reproduced here so a test
// can script the exact MFA variant it wants.
const (
	// FakeCustomerGUID is the account identifier the widget fixtures declare. It
	// is all zeroes on purpose: the real field names an account.
	FakeCustomerGUID = "00000000-0000-0000-0000-000000000000"
	// FakeWidgetClientID is the SSO client the widget fixtures are rendered for.
	FakeWidgetClientID = "GarminConnect"

	// WidgetTitleTOTPMFA is the authenticator-app MFA page title.
	WidgetTitleTOTPMFA = "Enter MFA code for login"
	// WidgetTitleEmailMFA is the email one-time-code page title, whose delivery
	// the scraped HTML cannot confirm.
	WidgetTitleEmailMFA = "GARMIN Authentication Application"
	// WidgetTitleSuccess precedes a service ticket.
	WidgetTitleSuccess = "Success"
)

// JSON returns a Behavior serving an application/json body.
func JSON(status int, body string) Behavior {
	return Behavior{Status: status, ContentType: ContentTypeJSON, Body: body}
}

// HTML returns a Behavior serving a text/html body.
func HTML(status int, body string) Behavior {
	return Behavior{Status: status, ContentType: ContentTypeHTML, Body: body}
}

// RateLimited returns a 429 Behavior with a delta-seconds Retry-After header.
func RateLimited(retryAfterSeconds int) Behavior {
	header := make(http.Header, 1)
	header.Set(protocol.HeaderRetryAfter, strconv.Itoa(retryAfterSeconds))
	return Behavior{
		Status:      http.StatusTooManyRequests,
		ContentType: ContentTypeJSON,
		Header:      header,
		Body:        `{"error":{"status-code":"429","message":"synthetic rate limit"}}`,
	}
}

// WithDelay returns a copy of b that stalls for d before responding, for timeout
// and cancellation tests. The receiver is not modified.
func (b Behavior) WithDelay(d time.Duration) Behavior {
	out := b
	out.Delay = d
	return out
}

// LoginSuccessJSON is a successful JSON login response carrying ticket.
func LoginSuccessJSON(ticket string) string {
	return `{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"` + ticket + `"}`
}

// LoginMFARequiredJSON is a JSON login response demanding an OTP delivered by
// method, for example "email" or "sms".
func LoginMFARequiredJSON(method string) string {
	return `{"responseStatus":{"type":"MFA_REQUIRED"},"customerMfaInfo":{"mfaLastMethodUsed":"` + method + `"}}`
}

// LoginInvalidCredentialsJSON is a definitive credential rejection.
func LoginInvalidCredentialsJSON() string {
	return `{"responseStatus":{"type":"INVALID_USERNAME_PASSWORD"}}`
}

// LoginAccountLockedJSON reports a locked account.
func LoginAccountLockedJSON() string {
	return `{"responseStatus":{"type":"ACCOUNT_LOCKED"}}`
}

// LoginCaptchaRequiredJSON reports a CAPTCHA bot challenge.
func LoginCaptchaRequiredJSON() string {
	return `{"responseStatus":{"type":"CAPTCHA_REQUIRED"}}`
}

// LoginRateLimitedBodyJSON reports a 429 inside an HTTP 200 body.
func LoginRateLimitedBodyJSON() string {
	return `{"error":{"status-code":"429","message":"synthetic rate limit"}}`
}

// DITokenJSON is a DI OAuth2 token response. Both tokens are opaque synthetic
// strings, deliberately not JWT-shaped.
func DITokenJSON(accessToken, refreshToken string) string {
	return `{"access_token":"` + accessToken +
		`","refresh_token":"` + refreshToken +
		`","token_type":"Bearer","expires_in":3600}`
}

// SocialProfileJSON is a minimal API-tier profile used to validate a session.
func SocialProfileJSON(displayName string) string {
	return `{"profileId":900001,"displayName":"` + displayName + `","fullName":"Fake Tester"}`
}

// WidgetSignInPageHTML is a widget sign-in page carrying a _csrf token.
func WidgetSignInPageHTML(csrf string) string {
	return widgetDocument("Sign In",
		`<form method="post"><input type="hidden" name="_csrf" value="`+csrf+`" />`+
			`<input name="username" value="" /></form>`)
}

// WidgetSuccessHTML is the widget page that carries a service ticket.
func WidgetSuccessHTML(ticket string) string {
	return widgetDocument(WidgetTitleSuccess,
		`<a href="/sso/embed?ticket=`+ticket+`">continue</a>`)
}

// WidgetMFAHTML is a widget MFA page with the given title and a _csrf token for
// the follow-up OTP POST.
func WidgetMFAHTML(title string) string {
	return widgetDocument(title,
		`<form method="post"><input type="hidden" name="_csrf" value="fake-csrf-mfa" />`+
			`<input name="mfa-code" value="" /></form>`)
}

// WidgetMFAVarsHTML is a widget MFA page that also declares the inline JS variables
// Garmin's real page carries, which is what drives the explicit code-delivery
// request.
//
// The customer GUID is an all-zero UUID: the field identifies an account, so no
// fixture carries a plausible one. codeSentTo empty means the sign-in POST did not
// already deliver a code.
func WidgetMFAVarsHTML(title, method, codeSentTo string) string {
	return widgetDocument(title,
		`<form method="post"><input type="hidden" name="_csrf" value="fake-csrf-mfa" />`+
			`<input name="mfa-code" value="" /></form>`+
			`<script>`+
			`var customerGuid = "`+FakeCustomerGUID+`";`+
			`var mfaMethod = "`+method+`";`+
			`var locale = "en_US";`+
			`var clientId = "`+FakeWidgetClientID+`";`+
			`var codeSentTo = "`+codeSentTo+`";`+
			`</script>`)
}

// WidgetErrorHTML is a widget page whose title states an account or credential
// problem, for example "Invalid username or password" or "Account Locked".
func WidgetErrorHTML(title string) string {
	return widgetDocument(title, `<p>synthetic error page</p>`)
}

// BotChallengeHTML is a synthetic WAF interstitial.
func BotChallengeHTML() string {
	return widgetDocument("Attention Required! | Cloudflare",
		`<p>Please enable cookies. Synthetic challenge page.</p>`)
}

func widgetDocument(title, body string) string {
	return `<!doctype html><html><head><title>` + title + `</title></head><body>` + body + `</body></html>`
}
