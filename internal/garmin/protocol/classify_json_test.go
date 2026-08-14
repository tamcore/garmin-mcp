package protocol

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Shared literals for the classifier tests.
const (
	contentTypeJSON = "application/json"
	contentTypeHTML = "text/html"
)

// jsonResponse builds a 200 JSON response; non-200 cases spell out Response.
func jsonResponse(body string) Response {
	return Response{
		Status:      http.StatusOK,
		ContentType: contentTypeJSON + ";charset=UTF-8",
		Body:        []byte(body),
	}
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
			wantMFAMethod: "sms",
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
			response: Response{
				Status:      http.StatusTooManyRequests,
				ContentType: contentTypeJSON,
				Header:      http.Header{HeaderRetryAfter: []string{"120"}},
				Body:        []byte(`{}`),
			},
			wantOutcome:    OutcomeRateLimited,
			wantRetryAfter: 2 * time.Minute,
		},
		{
			name: "http 403 bot challenge with html body",
			response: Response{
				Status:      http.StatusForbidden,
				ContentType: contentTypeHTML,
				Body:        []byte("<html><title>Attention Required</title></html>"),
			},
			wantOutcome: OutcomeBotChallenge,
		},
		{
			name:        "http 401 without recognizable json",
			response:    Response{Status: http.StatusUnauthorized, ContentType: "text/plain", Body: []byte("Unauthorized")},
			wantOutcome: OutcomeInvalidCredentials,
		},
		{
			name: "http 503 is temporary",
			response: Response{
				Status:      http.StatusServiceUnavailable,
				ContentType: contentTypeHTML,
				Body:        []byte("<html>maintenance</html>"),
			},
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
			name: "content type sniffed from header when field empty",
			response: Response{
				Status: http.StatusOK,
				Header: http.Header{"Content-Type": []string{contentTypeJSON}},
				Body:   []byte(`{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"ST-fake-0002"}`),
			},
			wantOutcome: OutcomeSuccess,
			wantTicket:  "ST-fake-0002",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyJSONLogin(tc.response)
			if got.Outcome != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v", got.Outcome, tc.wantOutcome)
			}
			if got.ServiceTicket != tc.wantTicket {
				t.Fatalf("ServiceTicket = %q, want %q", got.ServiceTicket, tc.wantTicket)
			}
			if got.MFAMethod != tc.wantMFAMethod {
				t.Fatalf("MFAMethod = %q, want %q", got.MFAMethod, tc.wantMFAMethod)
			}
			if got.RetryAfter != tc.wantRetryAfter {
				t.Fatalf("RetryAfter = %v, want %v", got.RetryAfter, tc.wantRetryAfter)
			}
		})
	}
}

func TestClassifyJSONLoginRetryAfterHTTPDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	got := ClassifyJSONLogin(Response{
		Status: http.StatusTooManyRequests,
		Header: http.Header{HeaderRetryAfter: []string{"Fri, 02 Jan 2026 15:05:05 GMT"}},
		Now:    now,
	})

	if got.Outcome != OutcomeRateLimited {
		t.Fatalf("Outcome = %v, want %v", got.Outcome, OutcomeRateLimited)
	}
	if got.RetryAfter != time.Minute {
		t.Fatalf("RetryAfter = %v, want %v", got.RetryAfter, time.Minute)
	}
}

func TestClassificationErr(t *testing.T) {
	t.Parallel()

	cause := errors.New("synthetic cause")

	t.Run("success has no error", func(t *testing.T) {
		t.Parallel()
		c := ClassifyJSONLogin(jsonResponse(
			`{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"ST-fake-0003"}`))
		if err := c.Err("ios_login", EndpointMobileLogin, nil); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("failure carries operation endpoint status and cause", func(t *testing.T) {
		t.Parallel()
		c := ClassifyJSONLogin(Response{
			Status: http.StatusTooManyRequests,
			Header: http.Header{HeaderRetryAfter: []string{"15"}},
		})
		err := c.Err("ios_login", EndpointMobileLogin, cause)

		var pe *Error
		if !errors.As(err, &pe) {
			t.Fatalf("Err() = %T, want *protocol.Error", err)
		}
		if pe.Op != "ios_login" || pe.Endpoint != EndpointMobileLogin {
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
		err := c.Err("ios_login", EndpointMobileLogin, nil)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("errors.Is(ErrInvalidCredentials) = false for %v", err)
		}
		for _, forbidden := range []string{"S3cr3t", "SESSIONID", "password", "cookie"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("error %q leaked %q", err.Error(), forbidden)
			}
		}
	})
}

func TestEndpointNamesAreSanitized(t *testing.T) {
	t.Parallel()

	names := []string{
		EndpointMobileLogin,
		EndpointPortalLogin,
		EndpointPortalSignInPage,
		EndpointWidgetEmbed,
		EndpointWidgetSignIn,
		EndpointMobileMFAVerifyCode,
		EndpointPortalMFAVerifyCode,
		EndpointWidgetVerifyMFA,
		EndpointDIToken,
		EndpointSocialProfile,
	}

	for _, name := range names {
		if name == "" {
			t.Fatal("endpoint name must not be empty")
		}
		if strings.Contains(name, "://") || strings.Contains(name, "?") {
			t.Fatalf("endpoint name %q must be a host-free, query-free label", name)
		}
	}
}
