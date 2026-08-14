package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Secret-shaped material planted in the values under test. All of it is
// synthetic; none of it may reach a rendered or serialized form.
const (
	secretBody     = `{"password":"S3cr3t-Passw0rd","serviceTicketId":"ST-secret-0100"}`
	secretTicket   = "ST-secret-0100"
	secretCSRF     = "csrf-secret-0101"
	secretCookie   = testCookieName + "=cookie-secret-0102"
	secretBearer   = "Bearer bearer-secret-0103"
	secretTitle    = testHeaderSetCookie + " title-secret-0104"
	secretPassword = "S3cr3t-Passw0rd"
)

func secretLeaks() []string {
	return []string{
		secretBody, secretTicket, secretCSRF, secretCookie, secretBearer,
		secretTitle, secretPassword, "cookie-secret-0102", "bearer-secret-0103",
		"title-secret-0104", testHeaderSetCookie,
	}
}

func populatedResponse() Response {
	return NewResponseFromParts(http.StatusOK, contentTypeJSON, http.Header{
		testHeaderCookie:        []string{secretCookie},
		testHeaderAuthorization: []string{secretBearer},
		testHeaderSetCookie:     []string{secretCookie},
	}, []byte(secretBody)).WithNow(time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC))
}

func populatedClassification() Classification {
	return newClassification(classificationFields{
		outcome:              OutcomeSuccess,
		status:               http.StatusOK,
		serviceTicket:        secretTicket,
		mfaMethod:            mfaMethodSMS,
		mfaDeliveryUncertain: true,
		csrfToken:            secretCSRF,
		pageTitle:            secretTitle,
		retryAfter:           5 * time.Second,
		responseStatusType:   testStatusTypeSuccessful,
	})
}

func TestResponseFormattingIsRedacted(t *testing.T) {
	t.Parallel()

	resp := populatedResponse()
	renderings := map[string]string{
		"%v":       fmt.Sprintf("%v", resp),
		"%+v":      fmt.Sprintf("%+v", resp),
		"%#v":      fmt.Sprintf("%#v", resp),
		"%s":       resp.String(),
		"pointer":  fmt.Sprintf("%v", &resp),
		"String()": resp.String(),
	}

	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal(Response) error: %v", err)
	}
	renderings["json"] = string(encoded)

	for form, rendered := range renderings {
		assertNoSecrets(t, form, rendered)
		if !strings.Contains(rendered, "200") {
			t.Fatalf("%s rendering %q lost the status", form, rendered)
		}
	}
}

func TestClassificationFormattingIsRedacted(t *testing.T) {
	t.Parallel()

	c := populatedClassification()
	renderings := map[string]string{
		"%v":       fmt.Sprintf("%v", c),
		"%+v":      fmt.Sprintf("%+v", c),
		"%#v":      fmt.Sprintf("%#v", c),
		"%s":       c.String(),
		"pointer":  fmt.Sprintf("%v", &c),
		"String()": c.String(),
	}

	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal(Classification) error: %v", err)
	}
	renderings["json"] = string(encoded)

	for form, rendered := range renderings {
		assertNoSecrets(t, form, rendered)
		if !strings.Contains(rendered, OutcomeSuccess.String()) {
			t.Fatalf("%s rendering %q lost the outcome", form, rendered)
		}
	}
}

// A Classification nested in another struct must stay redacted, because that is
// how it reaches a log line in practice.
func TestClassificationStaysRedactedWhenNested(t *testing.T) {
	t.Parallel()

	type record struct {
		Attempt        int
		Classification Classification
		Response       Response
	}
	value := record{Attempt: 1, Classification: populatedClassification(), Response: populatedResponse()}

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(record) error: %v", err)
	}

	assertNoSecrets(t, "nested %+v", fmt.Sprintf("%+v", value))
	assertNoSecrets(t, "nested %#v", fmt.Sprintf("%#v", value))
	assertNoSecrets(t, "nested json", string(encoded))
}

// The redacted forms must still report presence, so an operator can tell an
// empty ticket from a withheld one.
func TestRedactedFormsReportPresenceNotContent(t *testing.T) {
	t.Parallel()

	withSecrets := populatedClassification().String()
	empty := newClassification(classificationFields{outcome: OutcomeUnknown}).String()

	if withSecrets == empty {
		t.Fatal("a populated Classification must not render identically to an empty one")
	}
	for _, want := range []string{"serviceTicket", "csrfToken", "pageTitle"} {
		if !strings.Contains(withSecrets, want) {
			t.Fatalf("rendering %q missing presence marker %q", withSecrets, want)
		}
	}
}

func TestRedactURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "query values redacted",
			in:   testSSOEmbedURL + "?ticket=ST-secret-0100&clientId=GCM_IOS_DARK",
			want: testSSOEmbedURL + "?ticket=<redacted>&clientId=<redacted>",
		},
		{
			name: "no query is unchanged",
			in:   testMobileLoginURL,
			want: testMobileLoginURL,
		},
		{
			name: "empty query marker dropped",
			in:   testSSOEmbedURL + "?",
			want: testSSOEmbedURL,
		},
		{
			name: "userinfo stripped",
			in:   "https://user:pa55word@sso.garmin.com/sso/embed?ticket=ST-secret-0100",
			want: testSSOEmbedURL + "?ticket=<redacted>",
		},
		{
			name: "fragment dropped",
			in:   testSSOEmbedURL + "#ticket=ST-secret-0100",
			want: testSSOEmbedURL,
		},
		{name: "unparsable url", in: "http://[::1]:namedport/x", want: "<redacted-url>"},
		{name: "empty url", in: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := redactURL(tc.in); got != tc.want {
				t.Fatalf("redactURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactedCauseNil(t *testing.T) {
	t.Parallel()

	if got := redactedCause(nil); got != "" {
		t.Fatalf("redactedCause(nil) = %q, want empty", got)
	}
}

func TestRedactedCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cause     error
		wantHas   []string
		wantLacks []string
	}{
		{
			name: "url error",
			cause: &url.Error{
				Op:  testHTTPVerbPost,
				URL: testSSOEmbedURL + "?ticket=ST-secret-0100",
				Err: errors.New(testHeaderCookie + ": " + secretCookie),
			},
			wantHas:   []string{testHTTPVerbPost, testSSOEmbedURL, "<redacted>"},
			wantLacks: []string{secretTicket, secretCookie, testHeaderCookie},
		},
		{
			name:      "arbitrary error yields type only",
			cause:     errors.New(testHeaderAuthorization + ": " + secretBearer),
			wantLacks: []string{secretBearer, testHeaderAuthorization, "bearer-secret-0103"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := redactedCause(tc.cause)
			if got == "" {
				t.Fatal("redactedCause() must describe a non-nil cause")
			}
			for _, want := range tc.wantHas {
				if !strings.Contains(got, want) {
					t.Fatalf("redactedCause() = %q, missing %q", got, want)
				}
			}
			for _, bad := range tc.wantLacks {
				if strings.Contains(got, bad) {
					t.Fatalf("redactedCause() = %q, leaked %q", got, bad)
				}
			}
		})
	}
}

func assertNoSecrets(t *testing.T, form, rendered string) {
	t.Helper()

	for _, bad := range secretLeaks() {
		if strings.Contains(rendered, bad) {
			t.Fatalf("%s rendering %q leaked %q", form, rendered, bad)
		}
	}
}
