package config

import (
	"errors"
	"slices"
	"strconv"
	"strings"
)

// Validate reports every problem in c at once, joined into a single error, so an
// operator fixing a deployment does not have to rediscover the next fault after
// each restart. Every reported error matches [ErrInvalidConfig].
//
// Validation is complete and purely lexical: it opens no listener, resolves no
// name, and reads none of the configured files. That ordering is the point —
// nothing may bind a port or read key material for a configuration that is going
// to be rejected.
//
// The network-facing half lives in validate_network.go.
func (c Config) Validate() error {
	errs := []error{
		c.validateRegion(),
		c.validateLimits(),
		c.validateLogging(),
		c.validatePrincipalID(),
	}
	errs = append(errs, c.validateSecrets()...)
	errs = append(errs, c.validatePaths()...)
	errs = append(errs, c.validateToolPolicy()...)

	switch c.Transport {
	case TransportStdio:
		errs = append(errs, c.validateStdio()...)
	case TransportStreamableHTTP:
		errs = append(errs, c.validateHTTP()...)
	default:
		errs = append(errs, newFieldError(keyTransport,
			"must be one of "+transportNames(), ErrUnsupportedTransport))
	}

	return errors.Join(errs...)
}

// validateRegion requires a region that came through the protocol package's
// domain validation. The zero ValidatedDomain is not a region and must not be
// read as a request for the default one.
func (c Config) validateRegion() error {
	if c.Region.IsValid() {
		return nil
	}
	_, cause := c.Region.Domain().Validate()
	return newFieldError(keyRegion, "must be a validated Garmin region", cause)
}

// validateStdio rejects every setting that only means something for a network
// listener. Ignoring one would leave an operator believing a security setting is
// in force when nothing reads it.
func (c Config) validateStdio() []error {
	inapplicable := []struct {
		key string
		set bool
	}{
		{key: keyPublicURL, set: c.PublicURL != ""},
		{key: keyTLSCertFile, set: c.TLSCertFile != ""},
		{key: keyTLSKeyFile, set: c.TLSKeyFile != ""},
		{key: keyTrustedProxyCIDRs, set: len(c.TrustedProxyCIDRs) > 0},
		{key: keyAllowInsecureHTTP, set: c.AllowInsecureHTTP},
		{key: keyAllowedOrigins, set: len(c.AllowedOrigins) > 0},
		{key: keyOAuthClients, set: len(c.OAuthClients) > 0},
	}

	var errs []error
	for _, candidate := range inapplicable {
		if candidate.set {
			errs = append(errs, newFieldError(candidate.key,
				"must not be set for the stdio transport", ErrInapplicableSetting))
		}
	}
	return errs
}

// validatePrincipalID keeps an unusable account binding a start-up failure.
//
// The rejected value is never echoed, and an email address is refused outright:
// a principal is an opaque internal identifier, and an email is login and
// display material that must never key isolation. The authoritative check is
// identity.NewPrincipal; this one runs first, so nothing opens a token store for
// a binding that is going to be refused.
func (c Config) validatePrincipalID() error {
	trimmed := strings.TrimSpace(c.PrincipalID)
	switch {
	case trimmed == "":
		return newFieldError(keyPrincipalID, "must not be blank", ErrMissingSetting)
	case trimmed != c.PrincipalID:
		return newFieldError(keyPrincipalID, "must not be padded with whitespace", ErrInvalidConfig)
	case len(trimmed) > MaxPrincipalIDLen:
		return newFieldError(keyPrincipalID,
			"must be at most "+strconv.Itoa(MaxPrincipalIDLen)+" bytes", ErrInvalidConfig)
	case strings.ContainsRune(trimmed, '@'):
		return newFieldError(keyPrincipalID,
			"must be an opaque identifier, not an email address", ErrInvalidConfig)
	case strings.ContainsFunc(trimmed, isControlRune):
		return newFieldError(keyPrincipalID, "must not contain a control character", ErrInvalidConfig)
	default:
		return nil
	}
}

// isControlRune reports whether r is a C0 or C1 control character, which has no
// place in an identifier that reaches a log line, a map key, or a file name.
func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// validateSecrets rejects an ambiguous inline-plus-file pair. Guessing which one
// the operator meant could put a stale key into production.
func (c Config) validateSecrets() []error {
	pairs := []struct {
		inlineKey string
		fileKey   string
		inline    Secret
		file      string
	}{
		{inlineKey: keyMasterKey, fileKey: keyMasterKeyFile, inline: c.MasterKey, file: c.MasterKeyPath},
		{inlineKey: keyGarminTokens, fileKey: keyGarminTokensFile, inline: c.GarminTokens, file: c.GarminTokensPath},
	}

	var errs []error
	for _, pair := range pairs {
		if pair.inline.IsSet() && pair.file != "" {
			errs = append(errs, newFieldError(pair.inlineKey,
				"conflicts with "+pair.fileKey+"; set exactly one", ErrSecretConflict))
		}
	}
	return errs
}

// validatePaths checks the shape of every configured path without touching the
// filesystem. Ownership, symlink, and mode checks belong to the package that
// opens the file, which has to repeat them at open time anyway.
func (c Config) validatePaths() []error {
	paths := []struct {
		key   string
		value string
	}{
		{key: keyDatabasePath, value: c.DatabasePath},
		{key: keyStateDir, value: c.StateDir},
		{key: keyMasterKeyFile, value: c.MasterKeyPath},
		{key: keyGarminTokensFile, value: c.GarminTokensPath},
		{key: keyTLSCertFile, value: c.TLSCertFile},
		{key: keyTLSKeyFile, value: c.TLSKeyFile},
	}

	var errs []error
	for _, candidate := range paths {
		if candidate.value == "" {
			continue
		}
		if err := checkPath(candidate.key, candidate.value); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// checkPath rejects a blank or traversing path. The rejected value is never
// echoed: a path can be attacker-influenced in a container environment.
func checkPath(key, value string) error {
	if strings.TrimSpace(value) == "" {
		return newFieldError(key, "must not be blank", ErrInvalidConfig)
	}
	// The raw segments are inspected, not the cleaned ones: cleaning resolves
	// "/var/lib/../../etc/shadow" to "/etc/shadow" and would hide the escape the
	// operator wrote.
	if slices.Contains(strings.Split(strings.ReplaceAll(value, `\`, "/"), "/"), "..") {
		return newFieldError(key, "must not contain a parent-directory segment", ErrInvalidConfig)
	}
	return nil
}

// validateToolPolicy checks the tier relationship and the two name lists. The
// tiers are an operator gate only; remotely each is intersected with a granted
// OAuth scope, and neither list may substitute for one.
func (c Config) validateToolPolicy() []error {
	var errs []error
	if c.EnableDestructiveTools && !c.EnableWriteTools {
		errs = append(errs, newFieldError(keyEnableDestructiveTools,
			"requires "+keyEnableWriteTools, ErrInvalidConfig))
	}
	errs = append(errs, checkToolNames(keyToolAllowlist, c.ToolAllowlist)...)
	errs = append(errs, checkToolNames(keyToolDenylist, c.ToolDenylist)...)
	errs = append(errs, c.checkToolListOverlap()...)
	return errs
}

// checkToolNames requires well-formed, unique MCP tool names, so a typo fails at
// startup instead of silently allowing or denying nothing.
func checkToolNames(key string, names []string) []error {
	seen := make(map[string]struct{}, len(names))
	var errs []error
	for _, name := range names {
		if !isToolName(name) {
			errs = append(errs, newFieldError(key,
				"must contain lower-case tool names of letters, digits, and underscores", ErrInvalidConfig))
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			errs = append(errs, newFieldError(key, "must not repeat a tool name", ErrInvalidConfig))
			continue
		}
		seen[name] = struct{}{}
	}
	return errs
}

// checkToolListOverlap rejects a name that is both allowed and denied, which has
// no defensible resolution.
func (c Config) checkToolListOverlap() []error {
	if len(c.ToolAllowlist) == 0 || len(c.ToolDenylist) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(c.ToolAllowlist))
	for _, name := range c.ToolAllowlist {
		allowed[name] = struct{}{}
	}
	for _, name := range c.ToolDenylist {
		if _, both := allowed[name]; both {
			return []error{newFieldError(keyToolDenylist,
				"must not repeat a name from "+keyToolAllowlist, ErrInvalidConfig)}
		}
	}
	return nil
}

// isToolName reports whether name matches the MCP tool-name shape this project
// registers: a lower-case letter followed by lower-case letters, digits, or
// underscores, bounded in length.
func isToolName(name string) bool {
	if name == "" || len(name) > MaxToolNameLen {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '_'):
		default:
			return false
		}
	}
	return true
}

// validateLimits keeps every bound positive and under its own ceiling, so an
// operator cannot disable a protection by raising it without limit.
func (c Config) validateLimits() error {
	bounds := []struct {
		key   string
		value int64
		max   int64
	}{
		{key: keyMaxRequestBytes, value: c.MaxRequestBytes, max: MaxRequestBytesCap},
		{key: keyMaxResponseBytes, value: c.MaxResponseBytes, max: MaxResponseBytesCap},
		{key: keyReadRateLimit, value: int64(c.ReadRateLimitPerMinute), max: MaxRateLimitPerMinute},
		{key: keyWriteRateLimit, value: int64(c.WriteRateLimitPerMinute), max: MaxRateLimitPerMinute},
	}

	var errs []error
	for _, bound := range bounds {
		if bound.value <= 0 || bound.value > bound.max {
			errs = append(errs, newFieldError(bound.key,
				"must be between 1 and "+strconv.FormatInt(bound.max, 10), ErrInvalidConfig))
		}
	}
	if c.RequestTimeout < MinRequestTimeout || c.RequestTimeout > MaxRequestTimeout {
		errs = append(errs, newFieldError(keyRequestTimeout,
			"must be between "+MinRequestTimeout.String()+" and "+MaxRequestTimeout.String(),
			ErrInvalidConfig))
	}
	return errors.Join(errs...)
}

// The accepted log encodings.
const (
	logFormatText = "text"
	logFormatJSON = "json"
)

// logLevels and logFormats are the accepted logging vocabularies.
var (
	logLevels  = [...]string{"debug", "info", "warn", "error"}
	logFormats = [...]string{logFormatText, logFormatJSON}
)

// validateLogging rejects an unknown level or encoding rather than falling back
// to a default, which could quietly raise or lower log verbosity.
func (c Config) validateLogging() error {
	var errs []error
	if !containsString(logLevels[:], c.LogLevel) {
		errs = append(errs, newFieldError(keyLogLevel,
			"must be one of "+strings.Join(logLevels[:], ", "), ErrInvalidConfig))
	}
	if !containsString(logFormats[:], c.LogFormat) {
		errs = append(errs, newFieldError(keyLogFormat,
			"must be one of "+strings.Join(logFormats[:], ", "), ErrInvalidConfig))
	}
	return errors.Join(errs...)
}

func containsString(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}
