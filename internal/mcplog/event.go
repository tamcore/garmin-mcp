package mcplog

import (
	"log/slog"
	"time"
)

// An Outcome is the coarse result of one tool call.
type Outcome string

// The outcomes. The set is closed: a call either ran, was refused by policy, was
// refused by the limiter, or failed.
const (
	OutcomeOK          Outcome = "ok"
	OutcomeDenied      Outcome = "denied"
	OutcomeRateLimited Outcome = "rate-limited"
	OutcomeError       Outcome = "error"
)

// String returns the label. An unrecognized outcome renders as "unknown" so a
// zero value cannot masquerade as a success.
func (o Outcome) String() string {
	switch o {
	case OutcomeOK, OutcomeDenied, OutcomeRateLimited, OutcomeError:
		return string(o)
	default:
		return "unknown"
	}
}

// level maps an outcome onto the severity an operator expects. A denial and a
// rate-limit rejection are warnings, because both are signals worth alerting on
// without being faults of the server.
func (o Outcome) level() slog.Level {
	switch o {
	case OutcomeDenied, OutcomeRateLimited:
		return slog.LevelWarn
	case OutcomeError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// A Status is the coarse status class of the call.
//
// It is deliberately not an HTTP status code and not a Garmin status code: the
// redaction rules permit a coarse status only, because an exact upstream code can
// disclose whether a particular record exists.
type Status string

// The status classes.
const (
	StatusSuccess       Status = "success"
	StatusClientError   Status = "client-error"
	StatusServerError   Status = "server-error"
	StatusUpstreamError Status = "upstream-error"
)

// String returns the label, or "none" when no status applies.
func (s Status) String() string {
	switch s {
	case StatusSuccess, StatusClientError, StatusServerError, StatusUpstreamError:
		return string(s)
	default:
		return "none"
	}
}

// A ToolEvent is everything that may be recorded about one tool call.
//
// The field set is the whole vocabulary, and it is closed on purpose. There is no
// free-form attribute channel, so there is no path by which a request body, a
// response body, a token, a cookie, a password, an MFA code, an email, a health
// metric, a coordinate, or a Garmin payload could reach a log line.
type ToolEvent struct {
	// RequestID correlates the log line with one JSON-RPC request.
	RequestID string

	// PrincipalID is the pseudonymous internal principal identifier. It is
	// personal data under short retention, never an email.
	PrincipalID string

	// ClientID names the OAuth client, or the MCP client implementation under
	// stdio.
	ClientID string

	// Category is the coarse tool domain, for example "activities" or
	// "womens-health". It is what is logged in place of the exact tool name.
	Category string

	// Tier is the coarse policy tier label.
	Tier string

	// ToolName is the exact tool name. It is emitted only when the operator set
	// Config.DebugToolNames, because a tool name can itself disclose a sensitive
	// domain.
	ToolName string

	// Outcome is the coarse result and selects the record's level.
	Outcome Outcome

	// Reason is caller-authored coarse text explaining a refusal.
	//
	// A caller must pass only text it authored itself — a policy or limiter
	// reason — never an upstream error string, which may embed a header or a
	// body. The policy package guarantees its reasons exclude the tool name.
	Reason string

	// Latency is how long the call took.
	Latency time.Duration

	// Status is the coarse status class.
	Status Status
}

// attrs renders the event. Empty fields are omitted rather than logged blank, so
// a record never implies a value the server does not have.
func (e ToolEvent) attrs(debugToolNames bool) []slog.Attr {
	attrs := make([]slog.Attr, 0, 9)
	attrs = appendNonEmpty(attrs, "requestId", e.RequestID)
	attrs = appendNonEmpty(attrs, "principalId", e.PrincipalID)
	attrs = appendNonEmpty(attrs, "clientId", e.ClientID)
	attrs = appendNonEmpty(attrs, "category", e.Category)
	attrs = appendNonEmpty(attrs, "tier", e.Tier)
	if debugToolNames {
		attrs = appendNonEmpty(attrs, "tool", e.ToolName)
	}
	attrs = append(attrs, slog.String("outcome", e.Outcome.String()))
	attrs = appendNonEmpty(attrs, "reason", e.Reason)
	attrs = append(attrs, slog.Int64("latencyMs", e.Latency.Milliseconds()))
	if e.Status != "" {
		attrs = append(attrs, slog.String("status", e.Status.String()))
	}
	return attrs
}

// A LifecycleEvent records a server transition. It carries operational facts
// only: nothing here is user data.
type LifecycleEvent struct {
	// Phase names the transition, for example "startup" or "shutdown".
	Phase string

	// Transport is "stdio" or "streamable-http".
	Transport string

	// Mode is the coarse deployment mode label.
	Mode string

	// ProtocolVersion is the MCP specification version the server was built
	// against.
	ProtocolVersion string

	// ToolCount is how many tools are registered.
	ToolCount int
}

// attrs renders the event, omitting empty fields.
func (e LifecycleEvent) attrs() []slog.Attr {
	attrs := make([]slog.Attr, 0, 5)
	attrs = appendNonEmpty(attrs, "phase", e.Phase)
	attrs = appendNonEmpty(attrs, "transport", e.Transport)
	attrs = appendNonEmpty(attrs, "mode", e.Mode)
	attrs = appendNonEmpty(attrs, "protocolVersion", e.ProtocolVersion)
	if e.ToolCount > 0 {
		attrs = append(attrs, slog.Int("toolCount", e.ToolCount))
	}
	return attrs
}

func appendNonEmpty(attrs []slog.Attr, key, value string) []slog.Attr {
	if value == "" {
		return attrs
	}
	return append(attrs, slog.String(key, value))
}
