// Package config holds the single source of runtime settings for garmin-mcp.
//
// A [Config] is a value of plain fields. [Load] builds one with deterministic
// precedence — flags, then environment variables under the GARMIN_MCP_ prefix,
// then an optional configuration file, then the safe defaults in [Default] — and
// validates it completely before returning, so no listener is opened and no key
// file is read for a configuration that is going to be rejected.
//
// Credentials are absent by construction. There is no password field and no MFA
// field, on any of those layers: Garmin credentials belong only in the browser
// login form or the explicit TTY flow, never in a flag, an environment variable,
// a config file, or an MCP tool argument.
//
// Secret material that does belong here — the encryption master key and an
// imported Garmin token set — is held in a [Secret], which renders as a marker
// through every printing, serialization, and logging path. Each such setting has
// a "-file" companion holding a path instead, and supplying both is rejected.
// The companion's file is deliberately not read here: [Load] must not touch the
// filesystem beyond the configuration file itself.
package config

import (
	"reflect"
	"strings"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Transport selects how the MCP server talks to its client.
type Transport string

const (
	// TransportStdio serves one local principal over standard input and output.
	// Standard output is reserved exclusively for MCP frames.
	TransportStdio Transport = "stdio"

	// TransportStreamableHTTP serves remote, multi-user MCP over Streamable
	// HTTP. It requires a canonical public URL, a database, and master key
	// material.
	TransportStreamableHTTP Transport = "streamable-http"
)

// supportedTransports is the transport allowlist, in the order errors and help
// text report it.
var supportedTransports = [...]Transport{TransportStdio, TransportStreamableHTTP}

// Transports returns a fresh copy of the supported transports.
func Transports() []Transport {
	out := make([]Transport, len(supportedTransports))
	copy(out, supportedTransports[:])
	return out
}

// transportNames renders the allowlist for an error or a usage string.
func transportNames() string {
	names := make([]string, 0, len(supportedTransports))
	for _, transport := range supportedTransports {
		names = append(names, string(transport))
	}
	return strings.Join(names, ", ")
}

// ParseTransport validates a caller-supplied transport. Surrounding space is
// trimmed and case is folded; an empty value selects [TransportStdio], the
// documented default. Anything else is rejected with a [FieldError] that names
// the allowlist and never echoes the rejected input.
func ParseTransport(value string) (Transport, error) {
	candidate := Transport(strings.ToLower(strings.TrimSpace(value)))
	if candidate == "" {
		return TransportStdio, nil
	}
	for _, supported := range supportedTransports {
		if candidate == supported {
			return candidate, nil
		}
	}
	return "", newFieldError(keyTransport, "must be one of "+transportNames(), ErrUnsupportedTransport)
}

// Safe defaults. Every default is either inert or the most restrictive useful
// value: read-only tool tiers, a loopback bind address, and no insecure
// override.
const (
	// DefaultBindAddress keeps an accidentally started HTTP listener on the
	// loopback interface.
	DefaultBindAddress = "127.0.0.1:8180"
	// DefaultMaxRequestBytes bounds a decoded MCP request body.
	DefaultMaxRequestBytes int64 = 1 << 20
	// DefaultMaxResponseBytes bounds a Garmin response this server will read.
	DefaultMaxResponseBytes int64 = 8 << 20
	// DefaultRequestTimeout bounds one outbound Garmin call.
	DefaultRequestTimeout = 30 * time.Second
	// DefaultSessionTimeout closes an idle Streamable HTTP session.
	DefaultSessionTimeout = 30 * time.Minute
	// DefaultReadRateLimitPerMinute bounds read tools per principal.
	DefaultReadRateLimitPerMinute = 120
	// DefaultWriteRateLimitPerMinute bounds write tools per principal.
	DefaultWriteRateLimitPerMinute = 30
	// DefaultLogLevel is the slog level name used when none is configured.
	DefaultLogLevel = "info"
	// DefaultLogFormat is the log encoding used when none is configured.
	DefaultLogFormat = "text"
	// DefaultPrincipalID is the identifier the local stdio deployment binds when
	// the operator names none. Local stdio serves exactly one account, so a
	// stable opaque label is enough. It is a storage record key, never a Garmin
	// account selector: nothing resolves a Garmin account from it.
	DefaultPrincipalID = "local"
)

// Validation caps. A setting an operator can raise without bound is a denial of
// service waiting to happen, so each limit has a ceiling of its own.
const (
	// MaxRequestBytesCap is the largest accepted request-body bound.
	MaxRequestBytesCap int64 = 8 << 20
	// MaxResponseBytesCap is the largest accepted response-body bound.
	MaxResponseBytesCap int64 = 64 << 20
	// MinRequestTimeout is the smallest accepted outbound request timeout.
	MinRequestTimeout = time.Second
	// MaxRequestTimeout is the largest accepted outbound request timeout.
	MaxRequestTimeout = 10 * time.Minute
	// MinSessionTimeout is the smallest accepted idle-session bound.
	MinSessionTimeout = 30 * time.Second
	// MaxSessionTimeout is the largest accepted idle-session bound. A session
	// that outlives an access token is a session identifier doing the work of a
	// credential, which is exactly what the transport refuses to allow.
	MaxSessionTimeout = 24 * time.Hour
	// MaxRateLimitPerMinute is the largest accepted per-principal rate limit.
	MaxRateLimitPerMinute = 100_000
	// MaxToolNameLen bounds a configured tool name.
	MaxToolNameLen = 64
	// MaxPrincipalIDLen bounds the configured principal identifier. The
	// authority on a principal identifier is identity.NewPrincipal; this bound
	// exists so an unusable value is refused before anything opens a store.
	MaxPrincipalIDLen = 256
)

// Config is the complete runtime configuration. Every field is plain data, and
// the value is treated as immutable: helpers return a new Config rather than
// modifying the receiver.
//
// There is deliberately no password, MFA, email, or account-selector field.
type Config struct {
	// Transport selects the MCP transport.
	Transport Transport

	// BindAddress is the host:port the Streamable HTTP listener binds. It is
	// unused in stdio mode.
	BindAddress string
	// PublicURL is the canonical externally reachable origin, optionally with a
	// path prefix. It is the OAuth issuer and resource base, and it is never
	// derived from a request header.
	PublicURL string
	// TrustedProxyCIDRs lists the networks whose forwarded headers may be
	// trusted. Empty means no forwarded header is trusted.
	TrustedProxyCIDRs []string
	// AllowedOrigins is the browser Origin allowlist for the Streamable HTTP
	// endpoint. Empty denies every request that carries an Origin, which is CORS
	// denied by default; a standards-compliant non-browser client sends none.
	AllowedOrigins []string
	// AllowInsecureHTTP is the explicit development override that permits a
	// cleartext non-loopback origin. It defaults to false and must stay false
	// in production.
	AllowInsecureHTTP bool
	// TLSCertFile is the PEM certificate chain for the HTTPS listener.
	TLSCertFile string
	// TLSKeyFile is the PEM private key for the HTTPS listener.
	TLSKeyFile string

	// DatabasePath is the SQLite database holding principals, consents, and
	// encrypted token material.
	DatabasePath string
	// StateDir is the directory holding the encrypted token store and the
	// versioned key material. An empty value asks the caller to resolve the
	// per-user configuration directory, which Load deliberately does not do:
	// resolving it would read the environment outside the precedence rules.
	StateDir string
	// PrincipalID is the opaque identifier of the single account a local stdio
	// process is bound to. It is never an email address and never a tool
	// argument; [DefaultPrincipalID] applies when the operator names none.
	PrincipalID string
	// MasterKeyPath is the owner-only file holding the versioned encryption
	// master key. This is the supported way to supply the key.
	MasterKeyPath string
	// MasterKey is inline master key material. It is an explicitly insecure
	// compatibility override, permitted only in stdio mode.
	MasterKey Secret
	// GarminTokensPath is a mounted native Garmin DI token file to import.
	GarminTokensPath string
	// GarminTokens is an inline native Garmin DI token document. It is an
	// explicitly insecure compatibility override, permitted only in stdio mode.
	GarminTokens Secret

	// OAuthClients is the operator-registered OAuth client registry. A remote
	// deployment requires at least one entry; no vendor client is ever defaulted.
	OAuthClients []OAuthClient

	// Region is the validated Garmin account region. It can only be produced by
	// the protocol package's domain validation, so an unvalidated host cannot
	// reach URL construction.
	Region protocol.ValidatedDomain

	// EnableWriteTools enables the write tool tier. Remotely it is only half of
	// the gate: a granted OAuth write scope is also required.
	EnableWriteTools bool
	// EnableDestructiveTools enables the destructive tool tier. It requires the
	// write tier as well.
	EnableDestructiveTools bool
	// ToolAllowlist, when non-empty, restricts registration to these tool
	// names. It is intersected with scopes, never a substitute for them.
	ToolAllowlist []string
	// ToolDenylist removes tool names from registration.
	ToolDenylist []string

	// MaxRequestBytes bounds a decoded request body.
	MaxRequestBytes int64
	// MaxResponseBytes bounds a Garmin response body.
	MaxResponseBytes int64
	// RequestTimeout bounds one outbound Garmin call.
	RequestTimeout time.Duration
	// SessionTimeout closes an idle Streamable HTTP session. It bounds how long
	// a session identifier stays addressable, and it is unused in stdio mode.
	SessionTimeout time.Duration
	// ReadRateLimitPerMinute bounds read tool calls per principal per minute.
	ReadRateLimitPerMinute int
	// WriteRateLimitPerMinute bounds write tool calls per principal per minute.
	WriteRateLimitPerMinute int

	// LogLevel is the slog level name: debug, info, warn, or error.
	LogLevel string
	// LogFormat is the log encoding: text or json.
	LogFormat string

	// ConfigFile is the configuration file that was read, or "" when none was.
	ConfigFile string
}

// Default returns the safe defaults. The returned value shares no slice state
// with any other call, and it validates.
func Default() Config {
	return Config{
		Transport:               TransportStdio,
		BindAddress:             DefaultBindAddress,
		Region:                  defaultRegion(),
		MaxRequestBytes:         DefaultMaxRequestBytes,
		MaxResponseBytes:        DefaultMaxResponseBytes,
		RequestTimeout:          DefaultRequestTimeout,
		SessionTimeout:          DefaultSessionTimeout,
		ReadRateLimitPerMinute:  DefaultReadRateLimitPerMinute,
		WriteRateLimitPerMinute: DefaultWriteRateLimitPerMinute,
		LogLevel:                DefaultLogLevel,
		LogFormat:               DefaultLogFormat,
		PrincipalID:             DefaultPrincipalID,
	}
}

// defaultRegion returns the global Garmin region through the protocol package's
// validation. A failure yields the zero value, which [Config.Validate] rejects,
// so the default fails closed rather than guessing a region.
func defaultRegion() protocol.ValidatedDomain {
	region, err := protocol.ParseDomain("")
	if err != nil {
		return protocol.ValidatedDomain{}
	}
	return region
}

// Clone returns a copy that shares no slice backing array with the receiver, so
// a caller can adjust one without observing the change in the other.
func (c Config) Clone() Config {
	out := c
	out.TrustedProxyCIDRs = copyStrings(c.TrustedProxyCIDRs)
	out.AllowedOrigins = copyStrings(c.AllowedOrigins)
	out.OAuthClients = cloneClients(c.OAuthClients)
	out.ToolAllowlist = copyStrings(c.ToolAllowlist)
	out.ToolDenylist = copyStrings(c.ToolDenylist)
	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// fieldNames reports the exported field names of Config. It exists for the
// structural test that no credential field can be added.
func fieldNames() []string {
	typ := reflect.TypeFor[Config]()
	fields := reflect.VisibleFields(typ)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, field.Name)
	}
	return out
}
