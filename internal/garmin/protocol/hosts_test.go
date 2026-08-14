package protocol

import (
	"strings"
	"testing"
)

func TestNewHostsBases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		domain         Domain
		wantSSO        string
		wantConnect    string
		wantConnectAPI string
		wantDIAuth     string
		wantIOSService string
	}{
		{
			name:           "global",
			domain:         DomainGlobal,
			wantSSO:        "https://sso.garmin.com",
			wantConnect:    "https://connect.garmin.com",
			wantConnectAPI: "https://connectapi.garmin.com",
			wantDIAuth:     "https://diauth.garmin.com",
			wantIOSService: "https://mobile.integration.garmin.com/gcm/ios",
		},
		{
			name:           "china",
			domain:         DomainChina,
			wantSSO:        "https://sso.garmin.cn",
			wantConnect:    "https://connect.garmin.cn",
			wantConnectAPI: "https://connectapi.garmin.cn",
			wantDIAuth:     "https://diauth.garmin.cn",
			wantIOSService: "https://mobile.integration.garmin.cn/gcm/ios",
		},
		{
			name:           "empty domain falls back to global",
			domain:         "",
			wantSSO:        "https://sso.garmin.com",
			wantConnect:    "https://connect.garmin.com",
			wantConnectAPI: "https://connectapi.garmin.com",
			wantDIAuth:     "https://diauth.garmin.com",
			wantIOSService: "https://mobile.integration.garmin.com/gcm/ios",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := NewHosts(tc.domain)
			assertEqual(t, "SSOBase", h.SSOBase(), tc.wantSSO)
			assertEqual(t, "ConnectBase", h.ConnectBase(), tc.wantConnect)
			assertEqual(t, "ConnectAPIBase", h.ConnectAPIBase(), tc.wantConnectAPI)
			assertEqual(t, "DIAuthBase", h.DIAuthBase(), tc.wantDIAuth)
			assertEqual(t, "IOSServiceURL", h.IOSServiceURL(), tc.wantIOSService)
		})
	}
}

// A non-allowlisted domain must never produce a credential-bearing URL that
// points at it. NewHosts falls back to the global region instead.
func TestNewHostsRejectsNonAllowlistedDomain(t *testing.T) {
	t.Parallel()

	hostile := []Domain{testHostileDomain, "garmin.com." + testHostileDomain, "sso.garmin.com", "GARMIN.COM"}

	for _, domain := range hostile {
		t.Run(string(domain), func(t *testing.T) {
			t.Parallel()

			h := NewHosts(domain)
			if got := h.Domain(); got != DomainGlobal {
				t.Fatalf("Domain() = %q, want %q", got, DomainGlobal)
			}
			urls := []string{
				h.SSOBase(), h.ConnectBase(), h.ConnectAPIBase(), h.DIAuthBase(),
				h.MobileIntegrationBase(), h.MobileLoginURL(), h.PortalLoginURL(),
				h.WidgetSignInURL(), h.WidgetRequestMFACodeURL(), h.DITokenURL(),
				h.SocialProfileURL(), h.IOSServiceURL(), h.PortalServiceURL(),
			}
			for _, u := range urls {
				for _, forbidden := range []string{testHostileDomain, "sso.sso.garmin.com"} {
					if strings.Contains(u, forbidden) {
						t.Fatalf("url %q points at %q", u, forbidden)
					}
				}
			}
		})
	}
}

func TestHostsEndpointURLs(t *testing.T) {
	t.Parallel()

	h := NewHosts(DomainGlobal)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"mobile login", h.MobileLoginURL(), testMobileLoginURL},
		{"portal login", h.PortalLoginURL(), "https://sso.garmin.com/portal/api/login"},
		{"portal signin page", h.PortalSignInPageURL(), "https://sso.garmin.com/portal/sso/en-US/sign-in"},
		{"widget embed", h.WidgetEmbedURL(), testSSOEmbedURL},
		{"widget signin", h.WidgetSignInURL(), "https://sso.garmin.com/sso/signin"},
		{"widget service url", h.WidgetServiceURL(), testSSOEmbedURL},
		{"mobile mfa verify", h.MobileMFAVerifyCodeURL(), "https://sso.garmin.com/mobile/api/mfa/verifyCode"},
		{"portal mfa verify", h.PortalMFAVerifyCodeURL(), "https://sso.garmin.com/portal/api/mfa/verifyCode"},
		{"widget mfa verify", h.WidgetVerifyMFAURL(), "https://sso.garmin.com/sso/verifyMFA/loginEnterMfaCode"},
		{"widget mfa request", h.WidgetRequestMFACodeURL(), "https://sso.garmin.com/sso/verifyMFA/mfaCode"},
		{"di token", h.DITokenURL(), "https://diauth.garmin.com/di-oauth2-service/oauth/token"},
		{"portal service", h.PortalServiceURL(), "https://connect.garmin.com/app"},
		{"social profile", h.SocialProfileURL(), "https://connectapi.garmin.com/userprofile-service/socialProfile"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertEqual(t, tc.name, tc.got, tc.want)
		})
	}
}

func TestHostsWithOverridesDoesNotMutateReceiver(t *testing.T) {
	t.Parallel()

	base := NewHosts(DomainGlobal)
	fake := base.WithOverrides(Overrides{
		SSO:               "http://127.0.0.1:1/sso-host",
		Connect:           "http://127.0.0.1:1/connect-host",
		ConnectAPI:        "http://127.0.0.1:1/api-host",
		DIAuth:            "http://127.0.0.1:1/diauth-host",
		MobileIntegration: "http://127.0.0.1:1/mobile-host",
	})

	assertEqual(t, "receiver SSO unchanged", base.SSOBase(), "https://sso.garmin.com")
	assertEqual(t, "override mobile login", fake.MobileLoginURL(), "http://127.0.0.1:1/sso-host/mobile/api/login")
	assertEqual(t, "override di token", fake.DITokenURL(), "http://127.0.0.1:1/diauth-host/di-oauth2-service/oauth/token")
	assertEqual(t, "override social profile", fake.SocialProfileURL(),
		"http://127.0.0.1:1/api-host/userprofile-service/socialProfile")
	assertEqual(t, "override ios service", fake.IOSServiceURL(), "http://127.0.0.1:1/mobile-host/gcm/ios")
	assertEqual(t, "override portal service", fake.PortalServiceURL(), "http://127.0.0.1:1/connect-host/app")
}

// Overrides is the one documented escape hatch for a non-Garmin host, and it
// keeps the allowlisted domain label so diagnostics stay honest.
func TestOverridesAreTheOnlyPathToAnArbitraryHost(t *testing.T) {
	t.Parallel()

	h := NewHosts(DomainChina).WithOverrides(Overrides{SSO: "http://example.test/sso/"})

	assertEqual(t, "overridden sso trimmed", h.MobileLoginURL(), "http://example.test/sso/mobile/api/login")
	assertEqual(t, "untouched diauth", h.DITokenURL(), "https://diauth.garmin.cn/di-oauth2-service/oauth/token")
	if got := h.Domain(); got != DomainChina {
		t.Fatalf("Domain() = %q, want %q", got, DomainChina)
	}
}

func TestHostsDomain(t *testing.T) {
	t.Parallel()

	if got := NewHosts("").Domain(); got != DomainGlobal {
		t.Fatalf("Domain() = %q, want %q", got, DomainGlobal)
	}
}

func assertEqual(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}
