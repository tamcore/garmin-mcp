package protocol

import (
	"encoding/base64"
	"net/http"
	"time"
)

// SSO client identities and locale. Source: the module-level constants in
// client.py (IOS_SSO_CLIENT_ID, PORTAL_SSO_CLIENT_ID) and the "locale" login
// parameter sent by every JSON login call.
const (
	// ClientIDIOS is the iOS app SSO client id used by login strategies 1 and 2.
	ClientIDIOS = "GCM_IOS_DARK"
	// ClientIDPortal is the desktop portal SSO client id used by strategies 4 and 5.
	ClientIDPortal = "GarminConnect"
	// LoginLocale is the locale query parameter sent with the JSON login APIs.
	LoginLocale = "en-US"
)

// User agents. Source: IOS_LOGIN_UA, DESKTOP_USER_AGENT, NATIVE_API_USER_AGENT
// and NATIVE_X_GARMIN_USER_AGENT in client.py. Standard Go TLS cannot reproduce
// the curl_cffi browser TLS fingerprint that upstream pairs with these strings;
// only the headers are ported.
const (
	// UserAgentIOSLogin is sent by the iOS mobile login flow.
	UserAgentIOSLogin = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) " +
		"AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148"

	// UserAgentDesktop is the static desktop fallback used by the portal and
	// widget HTML flows when no randomized browser header set is available.
	UserAgentDesktop = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	// UserAgentNativeAPI is sent to the DI auth host and the API tier.
	UserAgentNativeAPI = "GCM-Android-5.23"

	// UserAgentXGarmin is the X-Garmin-User-Agent companion header value.
	UserAgentXGarmin = "com.garmin.android.apps.connectmobile/5.23; ; " +
		"Google/sdk_gphone64_arm64/google; Android/33; Dalvik/2.1.0"
)

// DI OAuth2 grant types. Source: DI_GRANT_TYPE and the refresh call in
// Client._refresh_di_token.
const (
	// DIGrantTypeServiceTicket exchanges a CAS service ticket for a DI token.
	// The value is an opaque grant-type identifier: it always names the .com
	// host, including for garmin.cn accounts, and is never dialed.
	DIGrantTypeServiceTicket = "https://connectapi.garmin.com/di-oauth2-service/oauth/grant/service_ticket"

	// DIGrantTypeRefreshToken refreshes a DI token.
	DIGrantTypeRefreshToken = "refresh_token"
)

// MFAMethodEmail is the assumed delivery method when Garmin reports MFA without
// naming one. Source: the "email" default for customerMfaInfo.mfaLastMethodUsed.
const MFAMethodEmail = "email"

// Anti-WAF pacing bounds. Source: LOGIN_DELAY_MIN_S/LOGIN_DELAY_MAX_S and
// WIDGET_DELAY_MIN_S/WIDGET_DELAY_MAX_S. Cloudflare flags rapid GET-then-POST
// sequences as bot-like, so a randomized delay is part of the protocol, not
// decoration. The widget flow sits in a different rate-limit bucket and uses the
// shorter range.
const (
	PortalPacingMin = 10 * time.Second
	PortalPacingMax = 20 * time.Second
	WidgetPacingMin = 3 * time.Second
	WidgetPacingMax = 8 * time.Second
)

// diClientIDs are the DI OAuth2 client ids tried in order until one is accepted.
// Source: DI_CLIENT_IDS in client.py.
var diClientIDs = [...]string{
	"GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2",
	"GARMIN_CONNECT_MOBILE_ANDROID_DI_2024Q4",
	"GARMIN_CONNECT_MOBILE_ANDROID_DI",
	"GARMIN_CONNECT_MOBILE_IOS_DI",
}

// DIClientIDs returns a fresh copy of the candidate DI client ids in the order
// they must be tried.
func DIClientIDs() []string {
	out := make([]string, len(diClientIDs))
	copy(out, diClientIDs[:])
	return out
}

// NativeAPIHeaders returns a fresh header set identifying the native mobile app
// to the DI auth host and the API tier. Source: _native_headers in client.py.
func NativeAPIHeaders() http.Header {
	h := make(http.Header, 8)
	h.Set("User-Agent", UserAgentNativeAPI)
	h.Set("X-Garmin-User-Agent", UserAgentXGarmin)
	h.Set("X-Garmin-Paired-App-Version", "10861")
	h.Set("X-Garmin-Client-Platform", "Android")
	h.Set("X-App-Ver", "10861")
	h.Set("X-Lang", "en")
	h.Set("X-GCExperience", "GC5")
	h.Set("Accept-Language", "en-US,en;q=0.9")
	return h
}

// BasicAuthHeader builds the DI token endpoint Authorization value: the client
// id as the username with an empty password. Source: _build_basic_auth.
func BasicAuthHeader(clientID string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(clientID+":"))
}
