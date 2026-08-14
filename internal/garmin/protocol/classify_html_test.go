package protocol

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func htmlResponse(status int, body string) Response {
	return NewResponseFromParts(status, contentTypeHTML+";charset=UTF-8", nil, []byte(body))
}

func widgetPage(title, extra string) string {
	return "<html><head><title>" + title + "</title></head><body>" + extra + "</body></html>"
}

func TestExtractCSRFToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		want   string
		wantOK bool
	}{
		{
			name:   "single space attribute form",
			body:   `<input type="hidden" name="_csrf" value="fake-csrf-0001" />`,
			want:   "fake-csrf-0001",
			wantOK: true,
		},
		{
			name:   "multiple whitespace between attributes",
			body:   "<input name=\"_csrf\"\n\t value=\"fake-csrf-0002\">",
			want:   "fake-csrf-0002",
			wantOK: true,
		},
		{
			name:   "absent token",
			body:   widgetPage("Sign In", `<input name="username" value="">`),
			want:   "",
			wantOK: false,
		},
		{
			name:   "empty document",
			body:   "",
			want:   "",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ExtractCSRFToken([]byte(tc.body))
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("ExtractCSRFToken() = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestExtractPageTitleSanitizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "plain title", body: widgetPage("Success", ""), want: "Success"},
		{
			name: "collapses whitespace",
			body: "<title>Enter  MFA\n code\tfor login</title>",
			want: "Enter MFA code for login",
		},
		{name: "strips control characters", body: "<title>Suc\x00ce\x07ss</title>", want: "Success"},
		{name: "absent title", body: "<html><body>no head</body></html>", want: ""},
		{
			name: "bounded length",
			body: "<title>" + strings.Repeat("x", MaxPageTitleLen+50) + "</title>",
			want: strings.Repeat("x", MaxPageTitleLen),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractPageTitle([]byte(tc.body)); got != tc.want {
				t.Fatalf("ExtractPageTitle() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractServiceTicket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		want   string
		wantOK bool
	}{
		{
			name:   "ticket in redirect url",
			body:   `<a href="https://sso.example.test/sso/embed?ticket=ST-fake-0001">continue</a>`,
			want:   "ST-fake-0001",
			wantOK: true,
		},
		{
			name:   "ticket followed by another parameter",
			body:   `<script>var u="/gcm/ios?ticket=ST-fake-0002&next=1";</script>`,
			want:   "ST-fake-0002",
			wantOK: true,
		},
		{
			name:   "non service ticket value ignored",
			body:   `<a href="?ticket=TGT-fake-0003">x</a>`,
			want:   "",
			wantOK: false,
		},
		{
			name:   "no ticket",
			body:   widgetPage("Success", "<p>done</p>"),
			want:   "",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ExtractServiceTicket([]byte(tc.body))
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("ExtractServiceTicket() = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestClassifyWidgetSignInPage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		response    Response
		wantOutcome Outcome
		wantCSRF    string
	}{
		{
			name: "csrf token present",
			response: htmlResponse(http.StatusOK,
				widgetPage("Sign In", `<input name="_csrf" value="fake-csrf-0004">`)),
			wantOutcome: OutcomeSuccess,
			wantCSRF:    "fake-csrf-0004",
		},
		{
			name:        "missing csrf token is unknown",
			response:    htmlResponse(http.StatusOK, widgetPage("Sign In", "")),
			wantOutcome: OutcomeUnknown,
		},
		{
			name:        "rate limited",
			response:    NewResponseFromParts(http.StatusTooManyRequests, "", http.Header{HeaderRetryAfter: []string{"5"}}, nil),
			wantOutcome: OutcomeRateLimited,
		},
		{
			name:        "widget page forbidden is a bot challenge",
			response:    htmlResponse(http.StatusForbidden, widgetPage("Just a moment...", "")),
			wantOutcome: OutcomeBotChallenge,
		},
		{
			name:        "bad gateway is temporary",
			response:    htmlResponse(http.StatusBadGateway, widgetPage("502 Bad Gateway", "")),
			wantOutcome: OutcomeTemporaryFailure,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyWidgetSignInPage(tc.response)
			if got.Outcome() != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v", got.Outcome(), tc.wantOutcome)
			}
			if got.CSRFToken() != tc.wantCSRF {
				t.Fatalf("CSRFToken = %q, want %q", got.CSRFToken(), tc.wantCSRF)
			}
		})
	}
}

func TestClassifyWidgetLoginTitleHeuristics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		response      Response
		wantOutcome   Outcome
		wantTicket    string
		wantUncertain bool
	}{
		{
			name:        "success extracts ticket",
			response:    htmlResponse(http.StatusOK, widgetPage("Success", `<a href="?ticket=ST-fake-0005">go</a>`)),
			wantOutcome: OutcomeSuccess,
			wantTicket:  "ST-fake-0005",
		},
		{
			name:        "success without ticket is unknown",
			response:    htmlResponse(http.StatusOK, widgetPage("Success", "")),
			wantOutcome: OutcomeUnknown,
		},
		{
			name: "totp mfa page",
			response: htmlResponse(http.StatusOK,
				widgetPage("Enter MFA code for login", `<input name="_csrf" value="fake-csrf-0006">`)),
			wantOutcome: OutcomeMFARequired,
		},
		{
			name: "email mfa page has uncertain delivery",
			response: htmlResponse(http.StatusOK,
				widgetPage("GARMIN Authentication Application", `<input name="_csrf" value="fake-csrf-0007">`)),
			wantOutcome:   OutcomeMFARequired,
			wantUncertain: true,
		},
		{
			name:        "locked account",
			response:    htmlResponse(http.StatusOK, widgetPage("Account Locked", "")),
			wantOutcome: OutcomeAccountLocked,
		},
		{
			name:        "invalid credentials title",
			response:    htmlResponse(http.StatusOK, widgetPage("Invalid username or password", "")),
			wantOutcome: OutcomeInvalidCredentials,
		},
		{
			name:        "incorrect credentials title",
			response:    htmlResponse(http.StatusOK, widgetPage("Incorrect email or password", "")),
			wantOutcome: OutcomeInvalidCredentials,
		},
		{
			name:        "account error title",
			response:    htmlResponse(http.StatusOK, widgetPage("Account Error", "")),
			wantOutcome: OutcomeInvalidCredentials,
		},
		{
			name:        "restricted child account",
			response:    htmlResponse(http.StatusOK, widgetPage("Unable to Sign In", "")),
			wantOutcome: OutcomeAccountRestricted,
		},
		{
			name:        "service unavailable title is temporary",
			response:    htmlResponse(http.StatusOK, widgetPage("503 Service Unavailable", "")),
			wantOutcome: OutcomeTemporaryFailure,
		},
		{
			name:        "cloudflare title is a bot challenge",
			response:    htmlResponse(http.StatusOK, widgetPage("Attention Required! | Cloudflare", "")),
			wantOutcome: OutcomeBotChallenge,
		},
		{
			name:        "unrecognized title is unknown",
			response:    htmlResponse(http.StatusOK, widgetPage("Garmin Connect", "")),
			wantOutcome: OutcomeUnknown,
		},
		{
			// "unlocked" must not match the "locked" hint by substring.
			name:        "unlocked title is not a lockout",
			response:    htmlResponse(http.StatusOK, widgetPage("Account Unlocked", "")),
			wantOutcome: OutcomeUnknown,
		},
		{
			name:        "invalidate title is not a credential rejection",
			response:    htmlResponse(http.StatusOK, widgetPage("Session invalidated", "")),
			wantOutcome: OutcomeUnknown,
		},
		{
			name:        "hyphenated locked title still matches",
			response:    htmlResponse(http.StatusOK, widgetPage("Account-Locked", "")),
			wantOutcome: OutcomeAccountLocked,
		},
		{
			name:        "digit hint needs a word boundary",
			response:    htmlResponse(http.StatusOK, widgetPage("Error 5030 occurred", "")),
			wantOutcome: OutcomeUnknown,
		},
		{
			name:        "missing title on 200 is unknown",
			response:    htmlResponse(http.StatusOK, "<html><body></body></html>"),
			wantOutcome: OutcomeUnknown,
		},
		{
			name: "http 429 wins over title",
			response: NewResponseFromParts(
				http.StatusTooManyRequests,
				contentTypeHTML,
				http.Header{HeaderRetryAfter: []string{"9"}},
				[]byte(widgetPage("Success", "?ticket=ST-fake-0008")),
			),
			wantOutcome: OutcomeRateLimited,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyWidgetLogin(tc.response)
			if got.Outcome() != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v (title %q)", got.Outcome(), tc.wantOutcome, got.PageTitle())
			}
			if got.ServiceTicket() != tc.wantTicket {
				t.Fatalf("ServiceTicket = %q, want %q", got.ServiceTicket(), tc.wantTicket)
			}
			if got.MFADeliveryUncertain() != tc.wantUncertain {
				t.Fatalf("MFADeliveryUncertain = %v, want %v", got.MFADeliveryUncertain(), tc.wantUncertain)
			}
		})
	}
}

func TestClassifyWidgetLoginKeepsCSRFAndRetryAfter(t *testing.T) {
	t.Parallel()

	got := ClassifyWidgetLogin(NewResponseFromParts(
		http.StatusTooManyRequests,
		contentTypeHTML,
		http.Header{HeaderRetryAfter: []string{"45"}},
		nil,
	))
	if got.RetryAfter() != 45*time.Second {
		t.Fatalf("RetryAfter = %v, want 45s", got.RetryAfter())
	}

	mfa := ClassifyWidgetLogin(htmlResponse(http.StatusOK,
		widgetPage("Enter MFA code for login", `<input name="_csrf" value="fake-csrf-0009">`)))
	if mfa.CSRFToken() != "fake-csrf-0009" {
		t.Fatalf("CSRFToken = %q, want %q", mfa.CSRFToken(), "fake-csrf-0009")
	}
	if mfa.PageTitle() != "Enter MFA code for login" {
		t.Fatalf("PageTitle = %q", mfa.PageTitle())
	}
}
