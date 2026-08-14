package config

import (
	"errors"
	"os"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// LoadOptions selects the sources [Load] reads. It carries no state of its own,
// so two concurrent loads share nothing.
type LoadOptions struct {
	// Flags is the parsed command-line flag set, normally the Cobra root
	// command's flags. Only a flag the operator actually changed takes
	// precedence; an untouched flag falls through to the lower layers. A nil
	// value skips the flag layer.
	Flags *pflag.FlagSet

	// ConfigFile is a fallback configuration file path, used when neither the
	// --config flag nor GARMIN_MCP_CONFIG names one. Empty reads no file.
	ConfigFile string
}

// Load builds the effective configuration and validates it.
//
// Precedence is deterministic and total: a changed flag beats an environment
// variable, which beats the configuration file, which beats the safe defaults in
// [Default]. The returned Config has already passed [Config.Validate], so a
// caller that receives no error may proceed to open listeners and files.
//
// The only file Load reads is the configuration file itself. The path in a
// "-file" secret setting is validated but deliberately not opened: reading key
// material belongs to the package that owns it, where ownership and mode can be
// enforced at the same time.
func Load(opts LoadOptions) (Config, error) {
	store, err := newStore(opts)
	if err != nil {
		return Config{}, err
	}

	cfg, err := fromStore(store)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// newStore wires the defaults, the environment bindings, the changed flags, and
// the configuration file, in that layering.
func newStore(opts LoadOptions) (*viper.Viper, error) {
	store := viper.New()
	for _, s := range settings() {
		store.SetDefault(s.key, s.def)
		if err := store.BindEnv(s.key, envName(s.key)); err != nil {
			return nil, errors.Join(ErrInvalidConfig, err)
		}
		bindFlag(store, opts.Flags, s)
	}

	file := resolveConfigFile(opts)
	if file == "" {
		return store, nil
	}
	store.SetConfigFile(file)
	if err := store.ReadInConfig(); err != nil {
		return nil, newConfigFileError(file, err)
	}
	return store, nil
}

// bindFlag binds one setting to its flag when the flag exists.
func bindFlag(store *viper.Viper, flags *pflag.FlagSet, s setting) {
	if flags == nil || s.flag == "" {
		return
	}
	flag := flags.Lookup(s.flag)
	if flag == nil {
		return
	}
	// BindPFlag only fails for a nil flag, which is excluded above.
	_ = store.BindPFlag(s.key, flag)
}

// resolveConfigFile picks the configuration file with the same precedence as
// every other setting: the changed flag, then the environment, then the caller's
// fallback.
func resolveConfigFile(opts LoadOptions) string {
	if opts.Flags != nil {
		if flag := opts.Flags.Lookup(keyConfigFile); flag != nil && flag.Changed {
			return flag.Value.String()
		}
	}
	if fromEnv := strings.TrimSpace(os.Getenv(envName(keyConfigFile))); fromEnv != "" {
		return fromEnv
	}
	return opts.ConfigFile
}

// fromStore converts the resolved settings into a Config. The two values that
// carry their own validated types — the transport and the Garmin region — are
// parsed here, so an unsupported value is reported before anything else reads the
// configuration.
func fromStore(store *viper.Viper) (Config, error) {
	transport, transportErr := ParseTransport(store.GetString(keyTransport))
	region, regionErr := protocol.ParseDomain(store.GetString(keyRegion))
	if err := errors.Join(transportErr, wrapRegionError(regionErr)); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Transport:  transport,
		Region:     region,
		ConfigFile: store.ConfigFileUsed(),
	}
	cfg = withNetwork(cfg, store)
	cfg = withState(cfg, store)
	cfg = withPolicy(cfg, store)
	cfg = withLimits(cfg, store)
	return cfg, nil
}

// wrapRegionError turns the protocol package's rejection into a FieldError that
// names the setting, while keeping protocol.ErrUnsupportedDomain matchable.
func wrapRegionError(err error) error {
	if err == nil {
		return nil
	}
	return newFieldError(keyRegion, "must be a supported Garmin region", err)
}

// withNetwork returns cfg with the listener and origin settings applied.
func withNetwork(cfg Config, store *viper.Viper) Config {
	out := cfg
	out.BindAddress = strings.TrimSpace(store.GetString(keyBindAddress))
	out.PublicURL = strings.TrimSpace(store.GetString(keyPublicURL))
	out.TrustedProxyCIDRs = stringList(store, keyTrustedProxyCIDRs)
	out.AllowInsecureHTTP = store.GetBool(keyAllowInsecureHTTP)
	out.TLSCertFile = strings.TrimSpace(store.GetString(keyTLSCertFile))
	out.TLSKeyFile = strings.TrimSpace(store.GetString(keyTLSKeyFile))
	return out
}

// withState returns cfg with the persistence and secret-material settings
// applied. A "-file" setting yields a path; the inline companion yields a
// [Secret].
func withState(cfg Config, store *viper.Viper) Config {
	out := cfg
	out.DatabasePath = strings.TrimSpace(store.GetString(keyDatabasePath))
	out.StateDir = strings.TrimSpace(store.GetString(keyStateDir))
	out.PrincipalID = strings.TrimSpace(store.GetString(keyPrincipalID))
	out.MasterKeyPath = strings.TrimSpace(store.GetString(keyMasterKeyFile))
	out.MasterKey = NewSecret(store.GetString(keyMasterKey))
	out.GarminTokensPath = strings.TrimSpace(store.GetString(keyGarminTokensFile))
	out.GarminTokens = NewSecret(store.GetString(keyGarminTokens))
	return out
}

// withPolicy returns cfg with the tool tier and name lists applied.
func withPolicy(cfg Config, store *viper.Viper) Config {
	out := cfg
	out.EnableWriteTools = store.GetBool(keyEnableWriteTools)
	out.EnableDestructiveTools = store.GetBool(keyEnableDestructiveTools)
	out.ToolAllowlist = stringList(store, keyToolAllowlist)
	out.ToolDenylist = stringList(store, keyToolDenylist)
	return out
}

// withLimits returns cfg with the request bounds and the logging settings
// applied.
func withLimits(cfg Config, store *viper.Viper) Config {
	out := cfg
	out.MaxRequestBytes = store.GetInt64(keyMaxRequestBytes)
	out.MaxResponseBytes = store.GetInt64(keyMaxResponseBytes)
	out.RequestTimeout = store.GetDuration(keyRequestTimeout)
	out.ReadRateLimitPerMinute = store.GetInt(keyReadRateLimit)
	out.WriteRateLimitPerMinute = store.GetInt(keyWriteRateLimit)
	out.LogLevel = strings.ToLower(strings.TrimSpace(store.GetString(keyLogLevel)))
	out.LogFormat = strings.ToLower(strings.TrimSpace(store.GetString(keyLogFormat)))
	return out
}

// stringList reads a list setting from any of its accepted spellings: a YAML
// sequence, a repeated flag, or a comma-separated environment variable. Empty
// entries are dropped, so a trailing comma cannot become a blank tool name.
func stringList(store *viper.Viper, key string) []string {
	if raw, ok := store.Get(key).(string); ok {
		return splitList(raw)
	}
	return normalizeList(store.GetStringSlice(key))
}

// splitList splits a comma-separated value.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return normalizeList(strings.Split(raw, ","))
}

// normalizeList trims each entry and drops the empty ones.
func normalizeList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, entry := range in {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
