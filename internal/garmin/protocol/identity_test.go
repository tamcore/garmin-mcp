package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestClientIdentityConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"ios sso client id", ClientIDIOS, "GCM_IOS_DARK"},
		{"portal sso client id", ClientIDPortal, "GarminConnect"},
		{
			"di grant type",
			DIGrantTypeServiceTicket,
			"https://connectapi.garmin.com/di-oauth2-service/oauth/grant/service_ticket",
		},
		{"di refresh grant type", DIGrantTypeRefreshToken, "refresh_token"},
		{"native api user agent", UserAgentNativeAPI, "GCM-Android-5.23"},
		{"login locale", LoginLocale, "en-US"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertEqual(t, tc.name, tc.got, tc.want)
		})
	}
}

func TestUserAgents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ua       string
		contains []string
	}{
		{name: "ios login ua", ua: UserAgentIOSLogin, contains: []string{"iPhone", "AppleWebKit", "Mobile/"}},
		{name: "desktop ua", ua: UserAgentDesktop, contains: []string{"Macintosh", "Chrome/", "Safari/"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, want := range tc.contains {
				if !strings.Contains(tc.ua, want) {
					t.Fatalf("%s = %q, missing %q", tc.name, tc.ua, want)
				}
			}
		})
	}
}

func TestDIClientIDsReturnsIndependentCopyInPriorityOrder(t *testing.T) {
	t.Parallel()

	want := []string{
		"GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2",
		"GARMIN_CONNECT_MOBILE_ANDROID_DI_2024Q4",
		"GARMIN_CONNECT_MOBILE_ANDROID_DI",
		"GARMIN_CONNECT_MOBILE_IOS_DI",
	}

	got := DIClientIDs()
	if len(got) != len(want) {
		t.Fatalf("DIClientIDs() length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		assertEqual(t, "candidate", got[i], want[i])
	}

	got[0] = "mutated"
	if DIClientIDs()[0] != want[0] {
		t.Fatal("DIClientIDs() exposed shared backing array")
	}
}

func TestPacingBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		low     time.Duration
		high    time.Duration
		wantLow time.Duration
		wantHi  time.Duration
	}{
		{
			name: "mobile and portal", low: PortalPacingMin, high: PortalPacingMax,
			wantLow: 10 * time.Second, wantHi: 20 * time.Second,
		},
		{name: "widget", low: WidgetPacingMin, high: WidgetPacingMax, wantLow: 3 * time.Second, wantHi: 8 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.low != tc.wantLow {
				t.Fatalf("min = %v, want %v", tc.low, tc.wantLow)
			}
			if tc.high != tc.wantHi {
				t.Fatalf("max = %v, want %v", tc.high, tc.wantHi)
			}
			if tc.low > tc.high {
				t.Fatalf("min %v greater than max %v", tc.low, tc.high)
			}
		})
	}
}

func TestNativeAPIHeadersIsACopy(t *testing.T) {
	t.Parallel()

	first := NativeAPIHeaders()
	if first.Get("User-Agent") != UserAgentNativeAPI {
		t.Fatalf("User-Agent = %q, want %q", first.Get("User-Agent"), UserAgentNativeAPI)
	}
	if first.Get("X-Garmin-User-Agent") == "" {
		t.Fatal("X-Garmin-User-Agent header missing")
	}

	first.Set("User-Agent", "mutated")
	if NativeAPIHeaders().Get("User-Agent") != UserAgentNativeAPI {
		t.Fatal("NativeAPIHeaders() exposed shared state")
	}
}

func TestBasicAuthHeaderUsesEmptySecret(t *testing.T) {
	t.Parallel()

	// base64("GARMIN_CONNECT_MOBILE_IOS_DI:") — client id with empty password.
	got := BasicAuthHeader("GARMIN_CONNECT_MOBILE_IOS_DI")
	const want = "Basic R0FSTUlOX0NPTk5FQ1RfTU9CSUxFX0lPU19ESTo="
	assertEqual(t, "BasicAuthHeader", got, want)
}
