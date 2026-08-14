package testkit

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

func TestJSONFixturesClassifyAsIntended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantOutcome protocol.Outcome
	}{
		{name: "success", body: LoginSuccessJSON("ST-fake-2001"), wantOutcome: protocol.OutcomeSuccess},
		{name: "mfa required", body: LoginMFARequiredJSON("email"), wantOutcome: protocol.OutcomeMFARequired},
		{name: "invalid credentials", body: LoginInvalidCredentialsJSON(), wantOutcome: protocol.OutcomeInvalidCredentials},
		{name: "account locked", body: LoginAccountLockedJSON(), wantOutcome: protocol.OutcomeAccountLocked},
		{name: "captcha required", body: LoginCaptchaRequiredJSON(), wantOutcome: protocol.OutcomeBotChallenge},
		{name: "rate limited body", body: LoginRateLimitedBodyJSON(), wantOutcome: protocol.OutcomeRateLimited},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := protocol.NewResponseFromParts(http.StatusOK, ContentTypeJSON, nil, []byte(tc.body))
			got := protocol.ClassifyJSONLogin(resp)
			if got.Outcome() != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v", got.Outcome(), tc.wantOutcome)
			}
		})
	}
}

func TestHTMLFixturesClassifyAsIntended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantOutcome protocol.Outcome
	}{
		{name: "widget success", body: WidgetSuccessHTML("ST-fake-2002"), wantOutcome: protocol.OutcomeSuccess},
		{name: "widget totp mfa", body: WidgetMFAHTML(WidgetTitleTOTPMFA), wantOutcome: protocol.OutcomeMFARequired},
		{name: "widget email mfa", body: WidgetMFAHTML(WidgetTitleEmailMFA), wantOutcome: protocol.OutcomeMFARequired},
		{
			name:        "widget invalid credentials",
			body:        WidgetErrorHTML("Invalid username or password"),
			wantOutcome: protocol.OutcomeInvalidCredentials,
		},
		{name: "bot challenge", body: BotChallengeHTML(), wantOutcome: protocol.OutcomeBotChallenge},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := protocol.NewResponseFromParts(http.StatusOK, ContentTypeHTML, nil, []byte(tc.body))
			got := protocol.ClassifyWidgetLogin(resp)
			if got.Outcome() != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v (title %q)", got.Outcome(), tc.wantOutcome, got.PageTitle())
			}
		})
	}
}

// forbiddenInFixtures returns the patterns no fixture may contain, matched
// against lowercased text. Each entry names what it rules out so a failure is
// readable.
func forbiddenInFixtures() []struct {
	what    string
	pattern *regexp.Regexp
} {
	return []struct {
		what    string
		pattern *regexp.Regexp
	}{
		{"a real Garmin hostname", regexp.MustCompile(`garmin\.[a-z]{2,}`)},
		{"a real Garmin subdomain", regexp.MustCompile(`\b(sso|connect|connectapi|diauth|omt)\.[a-z0-9-]+\.[a-z]{2,}`)},
		{"a real mail domain", regexp.MustCompile(`@(gmail|googlemail|yahoo|outlook|hotmail|icloud)\.`)},
		{"a password field", regexp.MustCompile(`"pass(word|phrase|wordhash)"\s*:`)},
		{"a JWT header prefix", regexp.MustCompile(`eyj[a-z0-9_-]`)},
		{"a JWT-shaped token", regexp.MustCompile(`[a-z0-9_-]{8,}\.[a-z0-9_-]{8,}\.[a-z0-9_-]{8,}`)},
	}
}

func assertNoRealisticSecrets(t *testing.T, label, text string) {
	t.Helper()

	lower := strings.ToLower(text)
	for _, rule := range forbiddenInFixtures() {
		if match := rule.pattern.FindString(lower); match != "" {
			t.Fatalf("%s contains %s (%q)", label, rule.what, match)
		}
	}
}

func TestFixturesContainNoRealisticSecrets(t *testing.T) {
	t.Parallel()

	bodies := map[string]string{
		"LoginSuccessJSON":            LoginSuccessJSON("ST-fake-2003"),
		"LoginMFARequiredJSON":        LoginMFARequiredJSON("sms"),
		"LoginInvalidCredentialsJSON": LoginInvalidCredentialsJSON(),
		"LoginAccountLockedJSON":      LoginAccountLockedJSON(),
		"LoginCaptchaRequiredJSON":    LoginCaptchaRequiredJSON(),
		"LoginRateLimitedBodyJSON":    LoginRateLimitedBodyJSON(),
		"DITokenJSON":                 DITokenJSON("fake-access-2001", "fake-refresh-2001"),
		"SocialProfileJSON":           SocialProfileJSON("fake-display-2001"),
		"WidgetSignInPageHTML":        WidgetSignInPageHTML("fake-csrf-2001"),
		"WidgetSuccessHTML":           WidgetSuccessHTML("ST-fake-2004"),
		"WidgetMFAHTML/totp":          WidgetMFAHTML(WidgetTitleTOTPMFA),
		"WidgetMFAHTML/email":         WidgetMFAHTML(WidgetTitleEmailMFA),
		"WidgetErrorHTML":             WidgetErrorHTML("Account Locked"),
		"BotChallengeHTML":            BotChallengeHTML(),
		"RateLimited":                 RateLimited(30).Body,
	}

	for name, body := range bodies {
		assertNoRealisticSecrets(t, "fixture "+name+" ("+body+")", body)
	}
}

// TestFixtureSourceContainsNoRealisticSecrets scans fixtures.go itself, so a
// fixture added later is covered even if nobody lists it above.
func TestFixtureSourceContainsNoRealisticSecrets(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("fixtures.go")
	if err != nil {
		t.Fatalf("read fixtures.go: %v", err)
	}
	assertNoRealisticSecrets(t, "fixtures.go", string(source))
}

func TestDITokenFixtureCarriesInjectedValues(t *testing.T) {
	t.Parallel()

	body := DITokenJSON("fake-access-2002", "fake-refresh-2002")
	for _, want := range []string{"fake-access-2002", "fake-refresh-2002", "access_token", "refresh_token", "expires_in"} {
		if !strings.Contains(body, want) {
			t.Fatalf("DITokenJSON() = %q, missing %q", body, want)
		}
	}
}

func TestBehaviorHelpers(t *testing.T) {
	t.Parallel()

	if got := JSON(http.StatusOK, "{}"); got.ContentType != ContentTypeJSON || got.Status != http.StatusOK {
		t.Fatalf("JSON() = %+v", got)
	}
	if got := HTML(http.StatusOK, "<html></html>"); !strings.HasPrefix(got.ContentType, "text/html") {
		t.Fatalf("HTML() = %+v", got)
	}

	limited := RateLimited(30)
	if limited.Status != http.StatusTooManyRequests || limited.Header.Get(protocol.HeaderRetryAfter) != "30" {
		t.Fatalf("RateLimited() = %+v", limited)
	}

	base := JSON(http.StatusOK, "{}")
	delayed := base.WithDelay(time.Second)
	if base.Delay != 0 {
		t.Fatal("WithDelay mutated its receiver")
	}
	if delayed.Delay != time.Second {
		t.Fatalf("WithDelay() delay = %v", delayed.Delay)
	}
}
