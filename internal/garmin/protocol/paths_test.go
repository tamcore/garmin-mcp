package protocol

import (
	"strings"
	"testing"
)

func TestSSOPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"mobile login", PathMobileLogin, "/mobile/api/login"},
		{"portal login", PathPortalLogin, "/portal/api/login"},
		{"portal signin page", PathPortalSignInPage, "/portal/sso/en-US/sign-in"},
		{"widget embed", PathWidgetEmbed, "/sso/embed"},
		{"widget signin", PathWidgetSignIn, "/sso/signin"},
		{"mobile mfa verify", PathMobileMFAVerifyCode, "/mobile/api/mfa/verifyCode"},
		{"portal mfa verify", PathPortalMFAVerifyCode, "/portal/api/mfa/verifyCode"},
		{"widget mfa verify", PathWidgetVerifyMFA, "/sso/verifyMFA/loginEnterMfaCode"},
		{"widget mfa request", PathWidgetRequestMFACode, "/sso/verifyMFA/mfaCode"},
		{"di token", PathDIToken, "/di-oauth2-service/oauth/token"},
		{"social profile", PathSocialProfile, "/userprofile-service/socialProfile"},
		{"ios service", PathIOSService, "/gcm/ios"},
		{"portal service", PathPortalService, "/app"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertEqual(t, tc.name, tc.got, tc.want)
		})
	}
}

func TestEndpointLabelsAreSanitized(t *testing.T) {
	t.Parallel()

	labels := []Endpoint{
		EndpointMobileLogin,
		EndpointPortalLogin,
		EndpointPortalSignInPage,
		EndpointWidgetEmbed,
		EndpointWidgetSignIn,
		EndpointMobileMFAVerifyCode,
		EndpointPortalMFAVerifyCode,
		EndpointWidgetVerifyMFA,
		EndpointWidgetRequestMFACode,
		EndpointDIToken,
		EndpointSocialProfile,
	}

	seen := make(map[Endpoint]bool, len(labels))
	for _, label := range labels {
		assertSanitizedLabel(t, string(label))
		if !label.IsKnown() {
			t.Fatalf("endpoint %q must report IsKnown", label)
		}
		if seen[label] {
			t.Fatalf("endpoint label %q is duplicated", label)
		}
		seen[label] = true
	}
}

func TestOpLabelsAreSanitized(t *testing.T) {
	t.Parallel()

	labels := []Op{
		OpMobileLogin,
		OpPortalLogin,
		OpWidgetLogin,
		OpWidgetSignInPage,
		OpVerifyMFA,
		OpRequestMFACode,
		OpExchangeServiceTicket,
		OpRefreshToken,
		OpValidateSession,
	}

	seen := make(map[Op]bool, len(labels))
	for _, label := range labels {
		assertSanitizedLabel(t, string(label))
		if !label.IsKnown() {
			t.Fatalf("op %q must report IsKnown", label)
		}
		if seen[label] {
			t.Fatalf("op label %q is duplicated", label)
		}
		seen[label] = true
	}
}

// Anything that is not a package constant is not a label, so it can never reach
// a rendered error message.
func TestUnknownLabelsAreRejected(t *testing.T) {
	t.Parallel()

	hostile := []string{
		"",
		"https://sso.garmin.com/sso/embed?ticket=ST-secret",
		"Cookie: SESSIONID=abc",
		"sso.mobile.login\nCookie: x",
		"../../etc/passwd",
	}

	for _, value := range hostile {
		if Endpoint(value).IsKnown() {
			t.Fatalf("Endpoint(%q).IsKnown() = true, want false", value)
		}
		if Op(value).IsKnown() {
			t.Fatalf("Op(%q).IsKnown() = true, want false", value)
		}
	}
}

func assertSanitizedLabel(t *testing.T, label string) {
	t.Helper()

	if label == "" {
		t.Fatal("label must not be empty")
	}
	for _, forbidden := range []string{"://", "?", "&", " ", "\n", "\r", "\t", "=", ";"} {
		if strings.Contains(label, forbidden) {
			t.Fatalf("label %q must not contain %q", label, forbidden)
		}
	}
}
