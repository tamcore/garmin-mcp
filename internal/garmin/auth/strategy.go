package auth

import (
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// StrategyName identifies one login transport. Only the constants below are
// strategies; any other value is not known and cannot be run.
type StrategyName string

// The login strategies, in the order the fallback chain tries them.
//
// Source: Client.login in python-garminconnect 0.3.10, which runs
// mobile+cffi, mobile+requests, widget+cffi, portal+cffi, portal+requests. The
// cffi and requests variants differ only in the curl_cffi TLS fingerprint, which
// Go's standard TLS stack cannot reproduce, so the three meaningful protocol
// flows are modeled once each. The pluggable transport is where a fingerprinting
// variant would attach, if the phase-0 gate ever calls for one.
const (
	// StrategyMobileIOS is the iOS app JSON login (upstream strategies 1 and 2).
	StrategyMobileIOS StrategyName = "mobile_ios"
	// StrategyWidget is the embedded SSO widget HTML login (upstream strategy 3).
	StrategyWidget StrategyName = "sso_widget"
	// StrategyPortal is the desktop portal JSON login (upstream strategies 4 and 5).
	StrategyPortal StrategyName = "portal"
)

// strategyOrder is the fallback order. Source: the strategies list in
// Client.login (0.3.10).
var strategyOrder = [...]StrategyName{StrategyMobileIOS, StrategyWidget, StrategyPortal}

// Strategies returns a fresh copy of the strategy fallback order.
func Strategies() []StrategyName {
	out := make([]StrategyName, len(strategyOrder))
	copy(out, strategyOrder[:])
	return out
}

// IsKnown reports whether s is one of the package's strategy constants.
func (s StrategyName) IsKnown() bool {
	for _, known := range strategyOrder {
		if s == known {
			return true
		}
	}
	return false
}

// String returns the label, or "unknown" for a value that is not a package
// constant, so a caller-supplied string can never reach a log line through this
// type.
func (s StrategyName) String() string {
	if s.IsKnown() {
		return string(s)
	}
	return labelUnknown
}

// PacingBounds returns the anti-WAF delay window this strategy must observe
// between its page GET and its credential POST. The third result is false for a
// strategy that performs no GET first and therefore paces nothing.
//
// Source: the random.uniform sleeps in _do_portal_web_login (10-20s) and
// _widget_web_login (3-8s) in client.py (0.3.10). The mobile JSON login posts
// straight to the API and sleeps nowhere.
func (s StrategyName) PacingBounds() (minDelay, maxDelay time.Duration, paced bool) {
	switch s {
	case StrategyWidget:
		return protocol.WidgetPacingMin, protocol.WidgetPacingMax, true
	case StrategyPortal:
		return protocol.PortalPacingMin, protocol.PortalPacingMax, true
	default:
		return 0, 0, false
	}
}

// loginOp is the sanitized operation label for the credential POST.
func (s StrategyName) loginOp() protocol.Op {
	switch s {
	case StrategyMobileIOS:
		return protocol.OpMobileLogin
	case StrategyWidget:
		return protocol.OpWidgetLogin
	case StrategyPortal:
		return protocol.OpPortalLogin
	default:
		return protocol.Op("")
	}
}

// loginEndpoint is the sanitized endpoint label for the credential POST.
func (s StrategyName) loginEndpoint() protocol.Endpoint {
	switch s {
	case StrategyMobileIOS:
		return protocol.EndpointMobileLogin
	case StrategyWidget:
		return protocol.EndpointWidgetSignIn
	case StrategyPortal:
		return protocol.EndpointPortalLogin
	default:
		return protocol.Endpoint("")
	}
}
