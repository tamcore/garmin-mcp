package config

import (
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// envPrefix is the documented environment prefix. fileSuffix names the companion
// setting that carries a path instead of inline secret material.
const (
	envPrefix  = "GARMIN_MCP_"
	fileSuffix = "-file"
)

// Canonical setting keys. One key spells the flag, the config-file key, and —
// upper-cased with the prefix — the environment variable, so an operator never
// has to translate between three vocabularies.
const (
	keyConfigFile             = "config"
	keyTransport              = "transport"
	keyBindAddress            = "bind-address"
	keyPublicURL              = "public-url"
	keyTrustedProxyCIDRs      = "trusted-proxy-cidrs"
	keyAllowInsecureHTTP      = "allow-insecure-http"
	keyTLSCertFile            = "tls-cert-file"
	keyTLSKeyFile             = "tls-key-file"
	keyDatabasePath           = "database-path"
	keyStateDir               = "state-dir"
	keyPrincipalID            = "principal-id"
	keyMasterKey              = "master-key"
	keyMasterKeyFile          = keyMasterKey + fileSuffix
	keyGarminTokens           = "garmin-tokens"
	keyGarminTokensFile       = keyGarminTokens + fileSuffix
	keyRegion                 = "region"
	keyEnableWriteTools       = "enable-write-tools"
	keyEnableDestructiveTools = "enable-destructive-tools"
	keyToolAllowlist          = "tool-allowlist"
	keyToolDenylist           = "tool-denylist"
	keyMaxRequestBytes        = "max-request-bytes"
	keyMaxResponseBytes       = "max-response-bytes"
	keyRequestTimeout         = "request-timeout"
	keyReadRateLimit          = "read-rate-limit"
	keyWriteRateLimit         = "write-rate-limit"
	keyLogLevel               = "log-level"
	keyLogFormat              = "log-format"
)

// settingKind selects how a setting is registered as a flag and read back.
type settingKind uint8

const (
	kindString settingKind = iota
	kindBool
	kindInt
	kindInt64
	kindDuration
	kindStringSlice
)

// setting describes one configuration knob on every layer at once.
type setting struct {
	// key is the canonical name.
	key string
	// flag is the command-line flag name, or "" for a setting that must not
	// appear on the command line.
	flag string
	// usage is the flag help text.
	usage string
	// kind selects the value type.
	kind settingKind
	// def is the safe default, matching kind.
	def any
	// secret marks inline secret material. A secret setting has no flag and has
	// a fileSuffix companion.
	secret bool
}

// settingTable is the single source of truth for defaults, flags, config-file
// keys, and environment variables.
//
// Two rules are structural rather than advisory, and both have tests: a setting
// marked secret carries no flag, because a command line is readable by every
// local process; and no setting names a password or an MFA code, because those
// are not configuration at all.
var settingTable = [...]setting{
	{
		key: keyConfigFile, flag: keyConfigFile, kind: kindString, def: "",
		usage: "path to a configuration file; unset reads no file",
	},
	{
		key: keyTransport, flag: keyTransport, kind: kindString, def: string(TransportStdio),
		usage: "MCP transport: stdio or streamable-http",
	},
	{
		key: keyBindAddress, flag: keyBindAddress, kind: kindString, def: DefaultBindAddress,
		usage: "host:port for the Streamable HTTP listener; unused in stdio mode",
	},
	{
		key: keyPublicURL, flag: keyPublicURL, kind: kindString, def: "",
		usage: "canonical public origin, required for streamable-http; never derived from a request header",
	},
	{
		key: keyTrustedProxyCIDRs, flag: keyTrustedProxyCIDRs, kind: kindStringSlice, def: []string{},
		usage: "networks whose forwarded headers may be trusted; empty trusts none",
	},
	{
		key: keyAllowInsecureHTTP, flag: keyAllowInsecureHTTP, kind: kindBool, def: false,
		usage: "development override permitting a cleartext non-loopback origin",
	},
	{
		key: keyTLSCertFile, flag: keyTLSCertFile, kind: kindString, def: "",
		usage: "PEM certificate chain for the HTTPS listener",
	},
	{
		key: keyTLSKeyFile, flag: keyTLSKeyFile, kind: kindString, def: "",
		usage: "PEM private key for the HTTPS listener",
	},
	{
		key: keyDatabasePath, flag: keyDatabasePath, kind: kindString, def: "",
		usage: "SQLite database path, required for streamable-http",
	},
	{
		key: keyStateDir, flag: keyStateDir, kind: kindString, def: "",
		usage: "directory holding the encrypted token store and key material; empty selects the per-user config directory",
	},
	{
		key: keyPrincipalID, flag: keyPrincipalID, kind: kindString, def: DefaultPrincipalID,
		usage: "opaque identifier of the single local account this process is bound to; never an email address",
	},
	{
		key: keyMasterKey, kind: kindString, def: "", secret: true,
		usage: "inline encryption master key; environment or config file only",
	},
	{
		key: keyMasterKeyFile, flag: keyMasterKeyFile, kind: kindString, def: "",
		usage: "owner-only file holding the encryption master key",
	},
	{
		key: keyGarminTokens, kind: kindString, def: "", secret: true,
		usage: "inline native Garmin DI token document; environment or config file only",
	},
	{
		key: keyGarminTokensFile, flag: keyGarminTokensFile, kind: kindString, def: "",
		usage: "native Garmin DI token file to import",
	},
	{
		key: keyRegion, flag: keyRegion, kind: kindString, def: "",
		usage: "Garmin account region: garmin.com or garmin.cn",
	},
	{
		key: keyEnableWriteTools, flag: keyEnableWriteTools, kind: kindBool, def: false,
		usage: "enable the write tool tier; remotely a granted write scope is also required",
	},
	{
		key: keyEnableDestructiveTools, flag: keyEnableDestructiveTools, kind: kindBool, def: false,
		usage: "enable the destructive tool tier; requires the write tier",
	},
	{
		key: keyToolAllowlist, flag: keyToolAllowlist, kind: kindStringSlice, def: []string{},
		usage: "restrict registration to these tool names; intersected with scopes",
	},
	{
		key: keyToolDenylist, flag: keyToolDenylist, kind: kindStringSlice, def: []string{},
		usage: "remove these tool names from registration",
	},
	{
		key: keyMaxRequestBytes, flag: keyMaxRequestBytes, kind: kindInt64, def: DefaultMaxRequestBytes,
		usage: "maximum accepted request body size in bytes",
	},
	{
		key: keyMaxResponseBytes, flag: keyMaxResponseBytes, kind: kindInt64, def: DefaultMaxResponseBytes,
		usage: "maximum Garmin response body size read in bytes",
	},
	{
		key: keyRequestTimeout, flag: keyRequestTimeout, kind: kindDuration, def: DefaultRequestTimeout,
		usage: "timeout for one outbound Garmin call",
	},
	{
		key: keyReadRateLimit, flag: keyReadRateLimit, kind: kindInt, def: DefaultReadRateLimitPerMinute,
		usage: "read tool calls allowed per principal per minute",
	},
	{
		key: keyWriteRateLimit, flag: keyWriteRateLimit, kind: kindInt, def: DefaultWriteRateLimitPerMinute,
		usage: "write tool calls allowed per principal per minute",
	},
	{
		key: keyLogLevel, flag: keyLogLevel, kind: kindString, def: DefaultLogLevel,
		usage: "log level: debug, info, warn, or error",
	},
	{
		key: keyLogFormat, flag: keyLogFormat, kind: kindString, def: DefaultLogFormat,
		usage: "log encoding: text or json",
	},
}

// settings returns a fresh copy of the setting table, so no caller can reshape
// the package's own view of its configuration surface.
func settings() []setting {
	out := make([]setting, len(settingTable))
	copy(out, settingTable[:])
	return out
}

// envName renders the environment variable for a canonical key.
func envName(key string) string {
	return envPrefix + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
}

// RegisterFlags declares every flag-visible setting on fs. Secret-bearing
// settings are skipped deliberately: inline secret material must arrive through
// the environment or a file, never on a command line that any local process can
// read.
//
// fs receives its own flag values, so two flag sets registered from here share
// no state.
func RegisterFlags(fs *pflag.FlagSet) {
	for _, s := range settings() {
		if s.flag == "" {
			continue
		}
		registerFlag(fs, s)
	}
}

// registerFlag declares one setting on fs.
func registerFlag(fs *pflag.FlagSet, s setting) {
	switch s.kind {
	case kindString:
		fs.String(s.flag, defaultOf[string](s), s.usage)
	case kindBool:
		fs.Bool(s.flag, defaultOf[bool](s), s.usage)
	case kindInt:
		fs.Int(s.flag, defaultOf[int](s), s.usage)
	case kindInt64:
		fs.Int64(s.flag, defaultOf[int64](s), s.usage)
	case kindDuration:
		fs.Duration(s.flag, defaultOf[time.Duration](s), s.usage)
	case kindStringSlice:
		fs.StringSlice(s.flag, defaultOf[[]string](s), s.usage)
	}
}

// defaultOf reports the typed default of s, or the zero value when the table
// entry does not match the kind.
func defaultOf[T any](s setting) T {
	if typed, ok := s.def.(T); ok {
		return typed
	}
	var zero T
	return zero
}
