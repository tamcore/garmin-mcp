package protocol

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Shared literals for the classifier, label and redaction tests.
const (
	contentTypeJSON = "application/json"
	contentTypeHTML = "text/html"

	// testHostileDomain is a domain that must never reach URL construction.
	testHostileDomain = "attacker.example"
	// testCaseVariantDomain is an allowlisted domain in the wrong case, which is
	// valid input for ParseDomain and invalid everywhere else.
	testCaseVariantDomain = "GARMIN.COM"
	// testStatusTypeSuccessful is the responseStatus.type of a successful login.
	testStatusTypeSuccessful = "SUCCESSFUL"
	// Real Garmin URLs the URL builders and the redactor must produce.
	testSSOEmbedURL    = "https://sso.garmin.com/sso/embed"
	testMobileLoginURL = "https://sso.garmin.com/mobile/api/login"
	// Header and cookie names that must never appear in a rendered form.
	testHeaderCookie        = "Cookie"
	testHeaderSetCookie     = "Set-Cookie"
	testHeaderAuthorization = "Authorization"
	testCookieName          = "SESSIONID"
	// testHTTPVerbPost is the *url.Error Op a failed POST carries.
	testHTTPVerbPost = "Post"
)

// jsonResponse builds a 200 JSON response; non-200 cases spell out Response.
func jsonResponse(body string) Response {
	return NewResponseFromParts(http.StatusOK, contentTypeJSON+";charset=UTF-8", nil, []byte(body))
}

func TestClassifyJSONLogin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		response       Response
		wantOutcome    Outcome
		wantTicket     string
		wantMFAMethod  string
		wantRetryAfter time.Duration
	}{
		{
			name:        "successful login yields service ticket",
			response:    jsonResponse(`{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"ST-fake-0001"}`),
			wantOutcome: OutcomeSuccess,
			wantTicket:  "ST-fake-0001",
		},
		{
			name:        "successful login without ticket is unknown",
			response:    jsonResponse(`{"responseStatus":{"type":"SUCCESSFUL"}}`),
			wantOutcome: OutcomeUnknown,
		},
		{
			name: "mfa required keeps last used method",
			response: jsonResponse(
				`{"responseStatus":{"type":"MFA_REQUIRED"},"customerMfaInfo":{"mfaLastMethodUsed":"sms"}}`),
			wantOutcome:   OutcomeMFARequired,
			wantMFAMethod: mfaMethodSMS,
		},
		{
			name:          "mfa required defaults to email",
			response:      jsonResponse(`{"responseStatus":{"type":"MFA_REQUIRED"}}`),
			wantOutcome:   OutcomeMFARequired,
			wantMFAMethod: MFAMethodEmail,
		},
		{
			name:        "invalid username password",
			response:    jsonResponse(`{"responseStatus":{"type":"INVALID_USERNAME_PASSWORD"}}`),
			wantOutcome: OutcomeInvalidCredentials,
		},
		{
			name:        "invalid credentials variant",
			response:    jsonResponse(`{"responseStatus":{"type":"INVALID_CREDENTIALS"}}`),
			wantOutcome: OutcomeInvalidCredentials,
		},
		{
			name:        "account locked variant",
			response:    jsonResponse(`{"responseStatus":{"type":"ACCOUNT_LOCKED"}}`),
			wantOutcome: OutcomeAccountLocked,
		},
		{
			name:        "captcha required is a bot challenge",
			response:    jsonResponse(`{"responseStatus":{"type":"CAPTCHA_REQUIRED"}}`),
			wantOutcome: OutcomeBotChallenge,
		},
		{
			name:        "429 buried in json error body",
			response:    jsonResponse(`{"error":{"status-code":"429"}}`),
			wantOutcome: OutcomeRateLimited,
		},
		{
			name: "http 429 with delta-seconds retry-after",
			response: NewResponseFromParts(
				http.StatusTooManyRequests,
				contentTypeJSON,
				http.Header{HeaderRetryAfter: []string{"120"}},
				[]byte(`{}`),
			),
			wantOutcome:    OutcomeRateLimited,
			wantRetryAfter: 2 * time.Minute,
		},
		{
			name: "http 403 bot challenge with html body",
			response: NewResponseFromParts(
				http.StatusForbidden,
				contentTypeHTML,
				nil,
				[]byte("<html><title>Attention Required</title></html>"),
			),
			wantOutcome: OutcomeBotChallenge,
		},
		{
			// A bare 401 is a session, CSRF or protocol fault; only an explicit
			// credential rejection in the body may blame the password.
			name:        "http 401 without recognizable json is unknown",
			response:    NewResponseFromParts(http.StatusUnauthorized, "text/plain", nil, []byte("Unauthorized")),
			wantOutcome: OutcomeUnknown,
		},
		{
			name: "http 401 with explicit credential rejection",
			response: NewResponseFromParts(
				http.StatusUnauthorized,
				contentTypeJSON,
				nil,
				[]byte(`{"responseStatus":{"type":"INVALID_USERNAME_PASSWORD"}}`),
			),
			wantOutcome: OutcomeInvalidCredentials,
		},
		{
			name: "http 503 is temporary",
			response: NewResponseFromParts(
				http.StatusServiceUnavailable,
				contentTypeHTML,
				nil,
				[]byte("<html>maintenance</html>"),
			),
			wantOutcome: OutcomeTemporaryFailure,
		},
		{
			name:        "malformed json on 200 is unknown",
			response:    jsonResponse(`{"responseStatus":`),
			wantOutcome: OutcomeUnknown,
		},
		{
			name:        "empty body on 200 is unknown",
			response:    jsonResponse(``),
			wantOutcome: OutcomeUnknown,
		},
		{
			name:        "unrecognized response status type is unknown",
			response:    jsonResponse(`{"responseStatus":{"type":"SOMETHING_NEW"}}`),
			wantOutcome: OutcomeUnknown,
		},
		{
			// Substring matching used to read UNLOCKED as LOCKED and stop the
			// strategy chain on an account that is fine.
			name:        "account unlocked is not a lockout",
			response:    jsonResponse(`{"responseStatus":{"type":"ACCOUNT_UNLOCKED"}}`),
			wantOutcome: OutcomeUnknown,
		},
		{
			name:        "unlocked alone is not a lockout",
			response:    jsonResponse(`{"responseStatus":{"type":"UNLOCKED"}}`),
			wantOutcome: OutcomeUnknown,
		},
		{
			name:        "invalid token is not a credential rejection",
			response:    jsonResponse(`{"responseStatus":{"type":"INVALID_TOKEN"}}`),
			wantOutcome: OutcomeUnknown,
		},
		{
			name:        "invalid password variant still recognized",
			response:    jsonResponse(`{"responseStatus":{"type":"INVALID_PASSWORD"}}`),
			wantOutcome: OutcomeInvalidCredentials,
		},
		{
			name:        "locked token recognized inside a compound type",
			response:    jsonResponse(`{"responseStatus":{"type":"USER_ACCOUNT_LOCKED_OUT"}}`),
			wantOutcome: OutcomeAccountLocked,
		},
		{
			name: "content type sniffed from header when field empty",
			response: NewResponseFromParts(
				http.StatusOK,
				"",
				http.Header{"Content-Type": []string{contentTypeJSON}},
				[]byte(`{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"ST-fake-0002"}`),
			),
			wantOutcome: OutcomeSuccess,
			wantTicket:  "ST-fake-0002",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyJSONLogin(tc.response)
			if got.Outcome() != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v", got.Outcome(), tc.wantOutcome)
			}
			if got.ServiceTicket() != tc.wantTicket {
				t.Fatalf("ServiceTicket = %q, want %q", got.ServiceTicket(), tc.wantTicket)
			}
			if got.MFAMethod() != tc.wantMFAMethod {
				t.Fatalf("MFAMethod = %q, want %q", got.MFAMethod(), tc.wantMFAMethod)
			}
			if got.RetryAfter() != tc.wantRetryAfter {
				t.Fatalf("RetryAfter = %v, want %v", got.RetryAfter(), tc.wantRetryAfter)
			}
		})
	}
}

func TestClassifyJSONLoginRetryAfterHTTPDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	got := ClassifyJSONLogin(NewResponseFromParts(
		http.StatusTooManyRequests,
		"",
		http.Header{HeaderRetryAfter: []string{"Fri, 02 Jan 2026 15:05:05 GMT"}},
		nil,
	).WithNow(now))

	if got.Outcome() != OutcomeRateLimited {
		t.Fatalf("Outcome = %v, want %v", got.Outcome(), OutcomeRateLimited)
	}
	if got.RetryAfter() != time.Minute {
		t.Fatalf("RetryAfter = %v, want %v", got.RetryAfter(), time.Minute)
	}
}

func TestClassificationErr(t *testing.T) {
	t.Parallel()

	cause := errors.New("synthetic cause")

	t.Run("success has no error", func(t *testing.T) {
		t.Parallel()
		c := ClassifyJSONLogin(jsonResponse(
			`{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"ST-fake-0003"}`))
		if err := c.Err(OpMobileLogin, EndpointMobileLogin, nil); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("failure carries operation endpoint status and cause", func(t *testing.T) {
		t.Parallel()
		c := ClassifyJSONLogin(NewResponseFromParts(
			http.StatusTooManyRequests,
			"",
			http.Header{HeaderRetryAfter: []string{"15"}},
			nil,
		))
		err := c.Err(OpMobileLogin, EndpointMobileLogin, cause)

		var pe *Error
		if !errors.As(err, &pe) {
			t.Fatalf("Err() = %T, want *protocol.Error", err)
		}
		if pe.Op != OpMobileLogin || pe.Endpoint != EndpointMobileLogin {
			t.Fatalf("Op/Endpoint = %q/%q", pe.Op, pe.Endpoint)
		}
		if pe.Status != http.StatusTooManyRequests || pe.RetryAfter != 15*time.Second {
			t.Fatalf("Status/RetryAfter = %d/%v", pe.Status, pe.RetryAfter)
		}
		if !errors.Is(err, ErrRateLimited) || !errors.Is(err, cause) {
			t.Fatal("error must match ErrRateLimited and wrap the cause")
		}
	})

	t.Run("body is never embedded in the error", func(t *testing.T) {
		t.Parallel()
		const body = `{"responseStatus":{"type":"INVALID_USERNAME_PASSWORD"},"password":"S3cr3t","cookie":"SESSIONID=abc"}`
		c := ClassifyJSONLogin(jsonResponse(body))
		err := c.Err(OpMobileLogin, EndpointMobileLogin, nil)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("errors.Is(ErrInvalidCredentials) = false for %v", err)
		}
		for _, forbidden := range []string{"S3cr3t", testCookieName, "password", "cookie"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("error %q leaked %q", err.Error(), forbidden)
			}
		}
	})
}

// The raw responseStatus.type token stays available for diagnostics even when the
// classifier does not recognize it; only the rendered forms fold it to "other".
func TestClassificationResponseStatusType(t *testing.T) {
	t.Parallel()

	known := ClassifyJSONLogin(jsonResponse(
		`{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"ST-fake-0010"}`))
	if got := known.ResponseStatusType(); got != testStatusTypeSuccessful {
		t.Fatalf("ResponseStatusType() = %q, want %s", got, testStatusTypeSuccessful)
	}

	unknown := ClassifyJSONLogin(jsonResponse(`{"responseStatus":{"type":"BRAND_NEW_STATE"}}`))
	if got := unknown.ResponseStatusType(); got != "BRAND_NEW_STATE" {
		t.Fatalf("ResponseStatusType() = %q, want the raw token", got)
	}
	if strings.Contains(unknown.String(), "BRAND_NEW_STATE") {
		t.Fatalf("rendering %q must fold an unrecognized token", unknown.String())
	}
}
