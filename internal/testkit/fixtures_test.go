package testkit

import (
	"net/http"
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
			got := protocol.ClassifyJSONLogin(protocol.Response{
				Status:      http.StatusOK,
				ContentType: ContentTypeJSON,
				Body:        []byte(tc.body),
			})
			if got.Outcome != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v", got.Outcome, tc.wantOutcome)
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
			got := protocol.ClassifyWidgetLogin(protocol.Response{
				Status:      http.StatusOK,
				ContentType: ContentTypeHTML,
				Body:        []byte(tc.body),
			})
			if got.Outcome != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v (title %q)", got.Outcome, tc.wantOutcome, got.PageTitle)
			}
		})
	}
}

func TestFixturesContainNoRealisticSecrets(t *testing.T) {
	t.Parallel()

	bodies := []string{
		LoginSuccessJSON("ST-fake-2003"),
		LoginMFARequiredJSON("sms"),
		LoginInvalidCredentialsJSON(),
		LoginAccountLockedJSON(),
		LoginCaptchaRequiredJSON(),
		LoginRateLimitedBodyJSON(),
		DITokenJSON("fake-access-2001", "fake-refresh-2001"),
		SocialProfileJSON("fake-display-2001"),
		WidgetSignInPageHTML("fake-csrf-2001"),
		WidgetSuccessHTML("ST-fake-2004"),
		WidgetMFAHTML(WidgetTitleEmailMFA),
		WidgetErrorHTML("Account Locked"),
		BotChallengeHTML(),
	}

	for _, body := range bodies {
		lower := strings.ToLower(body)
		for _, forbidden := range []string{"garmin.com", "garmin.cn", "@gmail", `password":"`, "eyj"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("fixture %q contains %q", body, forbidden)
			}
		}
	}
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
