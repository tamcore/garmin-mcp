package protocol

import (
	"errors"
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, err := NewHosts(tc.domain)
			if err != nil {
				t.Fatalf("NewHosts(%q) unexpected error: %v", tc.domain, err)
			}
			assertEqual(t, "SSOBase", h.SSOBase(), tc.wantSSO)
			assertEqual(t, "ConnectBase", h.ConnectBase(), tc.wantConnect)
			assertEqual(t, "ConnectAPIBase", h.ConnectAPIBase(), tc.wantConnectAPI)
			assertEqual(t, "DIAuthBase", h.DIAuthBase(), tc.wantDIAuth)
			assertEqual(t, "IOSServiceURL", h.IOSServiceURL(), tc.wantIOSService)
		})
	}
}

// A non-allowlisted domain must never produce a credential-bearing URL that
// points at it, and it must never be coerced into a region either: NewHosts
// rejects it.
func TestNewHostsRejectsNonAllowlistedDomain(t *testing.T) {
	t.Parallel()

	hostile := []Domain{
		"", testHostileDomain, "garmin.com." + testHostileDomain, "sso.garmin.com",
		testCaseVariantDomain, " garmin.com", "garmin.com.",
	}

	for _, domain := range hostile {
		t.Run(string(domain), func(t *testing.T) {
			t.Parallel()

			h, err := NewHosts(domain)
			if err == nil {
				t.Fatalf("NewHosts(%q) = %v, want an error", domain, h)
			}
			if !errors.Is(err, ErrUnsupportedDomain) {
				t.Fatalf("NewHosts(%q) error = %v, want ErrUnsupportedDomain", domain, err)
			}
			assertZeroHosts(t, h)
		})
	}
}

// A zero ValidatedDomain is a programming error. It must yield no URL at all,
// rather than the global region.
func TestNewHostsForValidatedDomainZeroValueYieldsNoURLs(t *testing.T) {
	t.Parallel()

	h := NewHostsForValidatedDomain(ValidatedDomain{})
	if got := h.Domain(); got != "" {
		t.Fatalf("Domain() = %q, want the zero Domain", got)
	}
	assertZeroHosts(t, h)
}

func TestNewHostsForValidatedDomainAcceptsParsedInput(t *testing.T) {
	t.Parallel()

	validated, err := ParseDomain(" GARMIN.CN ")
	if err != nil {
		t.Fatalf("ParseDomain unexpected error: %v", err)
	}

	h := NewHostsForValidatedDomain(validated)
	assertEqual(t, "SSOBase", h.SSOBase(), "https://sso.garmin.cn")
	if got := h.Domain(); got != DomainChina {
		t.Fatalf("Domain() = %q, want %q", got, DomainChina)
	}
}

// assertZeroHosts reports a failure unless every URL h can build is empty.
func assertZeroHosts(t *testing.T, h Hosts) {
	t.Helper()

	urls := map[string]string{
		"SSOBase":                 h.SSOBase(),
		"ConnectBase":             h.ConnectBase(),
		"ConnectAPIBase":          h.ConnectAPIBase(),
		"DIAuthBase":              h.DIAuthBase(),
		"MobileIntegrationBase":   h.MobileIntegrationBase(),
		"MobileLoginURL":          h.MobileLoginURL(),
		"PortalLoginURL":          h.PortalLoginURL(),
		"PortalSignInPageURL":     h.PortalSignInPageURL(),
		"WidgetEmbedURL":          h.WidgetEmbedURL(),
		"WidgetSignInURL":         h.WidgetSignInURL(),
		"WidgetServiceURL":        h.WidgetServiceURL(),
		"MobileMFAVerifyCodeURL":  h.MobileMFAVerifyCodeURL(),
		"PortalMFAVerifyCodeURL":  h.PortalMFAVerifyCodeURL(),
		"WidgetVerifyMFAURL":      h.WidgetVerifyMFAURL(),
		"WidgetRequestMFACodeURL": h.WidgetRequestMFACodeURL(),
		"DITokenURL":              h.DITokenURL(),
		"SocialProfileURL":        h.SocialProfileURL(),
		"IOSServiceURL":           h.IOSServiceURL(),
		"PortalServiceURL":        h.PortalServiceURL(),
	}
	for name, got := range urls {
		if got != "" {
			t.Fatalf("%s = %q, want empty for an unusable Hosts", name, got)
		}
	}
}

func TestHostsEndpointURLs(t *testing.T) {
	t.Parallel()

	h := mustHosts(t, DomainGlobal)

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

	base := mustHosts(t, DomainGlobal)
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

	h := mustHosts(t, DomainChina).WithOverrides(Overrides{SSO: "http://example.test/sso/"})

	assertEqual(t, "overridden sso trimmed", h.MobileLoginURL(), "http://example.test/sso/mobile/api/login")
	assertEqual(t, "untouched diauth", h.DITokenURL(), "https://diauth.garmin.cn/di-oauth2-service/oauth/token")
	if got := h.Domain(); got != DomainChina {
		t.Fatalf("Domain() = %q, want %q", got, DomainChina)
	}
}

func TestHostsDomain(t *testing.T) {
	t.Parallel()

	if got := mustHosts(t, DomainGlobal).Domain(); got != DomainGlobal {
		t.Fatalf("Domain() = %q, want %q", got, DomainGlobal)
	}
}

// mustHosts builds hosts for a domain the test knows is allowlisted.
func mustHosts(t *testing.T, domain Domain) Hosts {
	t.Helper()

	h, err := NewHosts(domain)
	if err != nil {
		t.Fatalf("NewHosts(%q) unexpected error: %v", domain, err)
	}
	return h
}

func assertEqual(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}

// An unvalidated Domain must not silently become the global region: a China
// account's credentials would then be sent to garmin.com.
func TestNewHostsFailsClosedOnUnvalidatedDomain(t *testing.T) {
	t.Parallel()

	for _, domain := range []Domain{"", testHostileDomain, DomainChina + "."} {
		h, err := NewHosts(domain)
		if err == nil {
			t.Fatalf("NewHosts(%q) accepted an unvalidated domain", domain)
		}
		if strings.Contains(h.SSOBase(), string(DomainGlobal)) {
			t.Fatalf("SSOBase() = %q; an unvalidated domain must not become the global region", h.SSOBase())
		}
	}
}
