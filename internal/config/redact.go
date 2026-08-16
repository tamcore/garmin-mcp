package config

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
)

// redactedConfig is the only shape a Config is ever rendered or serialized in. It
// mirrors the operator-facing settings and replaces each secret with a presence
// marker, so printing effective configuration is safe by construction rather than
// by remembering to redact at every call site.
//
// This follows the pattern the protocol package uses for its response and
// classification types.
type redactedConfig struct {
	Type                   string   `json:"type"`
	Transport              string   `json:"transport"`
	BindAddress            string   `json:"bindAddress,omitempty"`
	PublicURL              string   `json:"publicURL,omitempty"`
	TrustedProxyCIDRs      []string `json:"trustedProxyCIDRs,omitempty"`
	AllowedOrigins         []string `json:"allowedOrigins,omitempty"`
	OAuthClientIDs         []string `json:"oauthClientIDs,omitempty"`
	SessionTimeout         string   `json:"sessionTimeout"`
	AllowInsecureHTTP      bool     `json:"allowInsecureHTTP"`
	TLSCertFile            string   `json:"tlsCertFile,omitempty"`
	TLSKeyFile             string   `json:"tlsKeyFile,omitempty"`
	DatabasePath           string   `json:"databasePath,omitempty"`
	StateDir               string   `json:"stateDir,omitempty"`
	PrincipalID            string   `json:"principalID,omitempty"`
	MasterKeyFile          string   `json:"masterKeyFile,omitempty"`
	MasterKey              string   `json:"masterKey"`
	GarminTokensFile       string   `json:"garminTokensFile,omitempty"`
	GarminTokens           string   `json:"garminTokens"`
	Region                 string   `json:"region"`
	EnableWriteTools       bool     `json:"enableWriteTools"`
	EnableDestructiveTools bool     `json:"enableDestructiveTools"`
	ToolAllowlistLen       int      `json:"toolAllowlistLen"`
	ToolDenylistLen        int      `json:"toolDenylistLen"`
	MaxRequestBytes        int64    `json:"maxRequestBytes"`
	MaxResponseBytes       int64    `json:"maxResponseBytes"`
	RequestTimeout         string   `json:"requestTimeout"`
	SafetyDelay            string   `json:"safetyDelay"`
	ReadRateLimit          int      `json:"readRateLimitPerMinute"`
	WriteRateLimit         int      `json:"writeRateLimitPerMinute"`
	LogLevel               string   `json:"logLevel"`
	LogFormat              string   `json:"logFormat"`
	ConfigFile             string   `json:"configFile,omitempty"`
}

// redacted projects c into its printable shape.
//
// Tool names are reported as counts rather than values: a configured tool name
// discloses which Garmin domains a deployment touches, and the logging policy
// treats an exact tool name as sensitive.
func (c Config) redacted() redactedConfig {
	return redactedConfig{
		Type:                   "config.Config",
		Transport:              string(c.Transport),
		BindAddress:            c.BindAddress,
		PublicURL:              c.PublicURL,
		TrustedProxyCIDRs:      copyStrings(c.TrustedProxyCIDRs),
		AllowedOrigins:         copyStrings(c.AllowedOrigins),
		OAuthClientIDs:         clientIDs(c.OAuthClients),
		SessionTimeout:         c.SessionTimeout.String(),
		AllowInsecureHTTP:      c.AllowInsecureHTTP,
		TLSCertFile:            c.TLSCertFile,
		TLSKeyFile:             c.TLSKeyFile,
		DatabasePath:           c.DatabasePath,
		StateDir:               c.StateDir,
		PrincipalID:            c.PrincipalID,
		MasterKeyFile:          c.MasterKeyPath,
		MasterKey:              c.MasterKey.String(),
		GarminTokensFile:       c.GarminTokensPath,
		GarminTokens:           c.GarminTokens.String(),
		Region:                 c.Region.String(),
		EnableWriteTools:       c.EnableWriteTools,
		EnableDestructiveTools: c.EnableDestructiveTools,
		ToolAllowlistLen:       len(c.ToolAllowlist),
		ToolDenylistLen:        len(c.ToolDenylist),
		MaxRequestBytes:        c.MaxRequestBytes,
		MaxResponseBytes:       c.MaxResponseBytes,
		RequestTimeout:         c.RequestTimeout.String(),
		SafetyDelay:            c.SafetyDelay.String(),
		ReadRateLimit:          c.ReadRateLimitPerMinute,
		WriteRateLimit:         c.WriteRateLimitPerMinute,
		LogLevel:               c.LogLevel,
		LogFormat:              c.LogFormat,
		ConfigFile:             c.ConfigFile,
	}
}

// String renders the effective configuration on one line, with every secret
// replaced by a marker. It satisfies %v, %+v, and %s.
func (c Config) String() string {
	return "config.Config{" + strings.Join(c.redacted().pairs(), " ") + "}"
}

// pairs renders the redacted settings as key:value fragments.
func (r redactedConfig) pairs() []string {
	return []string{
		"transport:" + quoteValue(r.Transport),
		"bindAddress:" + quoteValue(r.BindAddress),
		"publicURL:" + quoteValue(r.PublicURL),
		"trustedProxyCIDRs:" + strconv.Itoa(len(r.TrustedProxyCIDRs)),
		"allowedOrigins:" + strconv.Itoa(len(r.AllowedOrigins)),
		"oauthClients:" + strconv.Itoa(len(r.OAuthClientIDs)),
		"sessionTimeout:" + r.SessionTimeout,
		"allowInsecureHTTP:" + strconv.FormatBool(r.AllowInsecureHTTP),
		"tlsCertFile:" + quoteValue(r.TLSCertFile),
		"tlsKeyFile:" + quoteValue(r.TLSKeyFile),
		"databasePath:" + quoteValue(r.DatabasePath),
		"stateDir:" + quoteValue(r.StateDir),
		"principalID:" + quoteValue(r.PrincipalID),
		"masterKeyFile:" + quoteValue(r.MasterKeyFile),
		"masterKey:" + r.MasterKey,
		"garminTokensFile:" + quoteValue(r.GarminTokensFile),
		"garminTokens:" + r.GarminTokens,
		"region:" + quoteValue(r.Region),
		"enableWriteTools:" + strconv.FormatBool(r.EnableWriteTools),
		"enableDestructiveTools:" + strconv.FormatBool(r.EnableDestructiveTools),
		"toolAllowlistLen:" + strconv.Itoa(r.ToolAllowlistLen),
		"toolDenylistLen:" + strconv.Itoa(r.ToolDenylistLen),
		"maxRequestBytes:" + strconv.FormatInt(r.MaxRequestBytes, 10),
		"maxResponseBytes:" + strconv.FormatInt(r.MaxResponseBytes, 10),
		"requestTimeout:" + r.RequestTimeout,
		"safetyDelay:" + r.SafetyDelay,
		"readRateLimitPerMinute:" + strconv.Itoa(r.ReadRateLimit),
		"writeRateLimitPerMinute:" + strconv.Itoa(r.WriteRateLimit),
		"logLevel:" + quoteValue(r.LogLevel),
		"logFormat:" + quoteValue(r.LogFormat),
		"configFile:" + quoteValue(r.ConfigFile),
	}
}

// GoString satisfies %#v with the same redacted rendering, which reflection would
// otherwise bypass.
func (c Config) GoString() string { return c.String() }

// MarshalJSON serializes the redacted form, so a configuration dump cannot leak
// secret material.
func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.redacted())
}

// LogValue implements slog.LogValuer, so structured logging is safe by default:
// every handler receives the redacted group instead of walking the value.
func (c Config) LogValue() slog.Value {
	red := c.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.String("transport", red.Transport),
		slog.String("bindAddress", red.BindAddress),
		slog.String("publicURL", red.PublicURL),
		slog.Int("trustedProxyCIDRs", len(red.TrustedProxyCIDRs)),
		slog.Int("allowedOrigins", len(red.AllowedOrigins)),
		slog.Int("oauthClients", len(red.OAuthClientIDs)),
		slog.String("sessionTimeout", red.SessionTimeout),
		slog.Bool("allowInsecureHTTP", red.AllowInsecureHTTP),
		slog.String("databasePath", red.DatabasePath),
		slog.String("stateDir", red.StateDir),
		slog.String("principalID", red.PrincipalID),
		slog.String("masterKeyFile", red.MasterKeyFile),
		slog.String("masterKey", red.MasterKey),
		slog.String("garminTokensFile", red.GarminTokensFile),
		slog.String("garminTokens", red.GarminTokens),
		slog.String("region", red.Region),
		slog.Bool("enableWriteTools", red.EnableWriteTools),
		slog.Bool("enableDestructiveTools", red.EnableDestructiveTools),
		slog.String("requestTimeout", red.RequestTimeout),
		slog.String("safetyDelay", red.SafetyDelay),
		slog.String("logLevel", red.LogLevel),
		slog.String("logFormat", red.LogFormat),
		slog.String("configFile", red.ConfigFile),
	)
}

// clientIDs reports the registered client identifiers.
//
// An identifier is operator-chosen configuration, not a credential, and an
// operator diagnosing a rejected client needs to see which registrations are in
// force. Nothing else about a registration is rendered: a redirect URI names a
// third party, and the secret digest is material this package never prints.
func clientIDs(clients []OAuthClient) []string {
	if len(clients) == 0 {
		return nil
	}
	out := make([]string, 0, len(clients))
	for _, client := range clients {
		out = append(out, client.ID)
	}
	return out
}

func quoteValue(value string) string {
	if value == "" {
		return `""`
	}
	return `"` + value + `"`
}
