package config

import "errors"

// Configuration failure sentinels. Every configuration error matches
// [ErrInvalidConfig] and, in addition, the sentinel that names why it failed, so
// a caller can distinguish "the operator forgot a setting" from "the operator
// asked for something unsafe" without matching on message text.
var (
	// ErrInvalidConfig matches every error this package reports for a rejected
	// configuration. Nothing may open a listener or read a key file after it.
	ErrInvalidConfig = errors.New("config: invalid configuration")

	// ErrMissingSetting reports a setting the selected transport requires.
	ErrMissingSetting = errors.New("config: required setting is missing")

	// ErrInapplicableSetting reports a setting that has no meaning for the
	// selected transport. It is rejected rather than ignored, because silently
	// dropping a security setting an operator believes is in force is worse
	// than refusing to start.
	ErrInapplicableSetting = errors.New("config: setting does not apply to the selected transport")

	// ErrInsecureSetting reports a combination that is understood but unsafe,
	// such as inline secret material in remote mode or a cleartext public
	// origin without the explicit development override.
	ErrInsecureSetting = errors.New("config: insecure setting combination")

	// ErrSecretConflict reports a secret supplied both inline and by file. The
	// pair is ambiguous, and guessing which one the operator meant could send a
	// stale key into production.
	ErrSecretConflict = errors.New("config: inline secret and secret file are both set")

	// ErrUnsupportedTransport reports a transport outside the supported set.
	ErrUnsupportedTransport = errors.New("config: unsupported transport")

	// ErrConfigFile reports a configuration file that could not be read or
	// parsed.
	ErrConfigFile = errors.New("config: configuration file cannot be read")
)

// FieldError reports one rejected setting. Field is the canonical setting key —
// the same spelling used by the flag, by the config-file key, and (upper-cased
// with the GARMIN_MCP_ prefix) by the environment variable — so the operator can
// find it whichever way it was set.
//
// Reason is authored by this package. A rejected value is never echoed into it:
// configuration input can be attacker-influenced and can carry secret material,
// and either would then reach an operator log line.
type FieldError struct {
	// Field is the canonical setting key, for example "bind-address".
	Field string
	// Reason states what was required, in this package's own words.
	Reason string
	// Err is the sentinel that classifies the failure.
	Err error
}

// newFieldError builds a *FieldError. reason must be a constant authored here,
// never caller-supplied text.
func newFieldError(field, reason string, err error) *FieldError {
	return &FieldError{Field: field, Reason: reason, Err: err}
}

// Error names the setting and the requirement, and nothing else.
func (e *FieldError) Error() string {
	return "config: " + e.Field + ": " + e.Reason
}

// Unwrap reports both ErrInvalidConfig and the classifying sentinel, so
// errors.Is matches either one.
func (e *FieldError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrInvalidConfig}
	}
	return []error{ErrInvalidConfig, e.Err}
}

// configFileError reports an unreadable or unparsable configuration file. The
// cause is reachable through errors.Is and errors.As but is never rendered: a
// parser error commonly quotes the offending line, and that line may hold the
// inline master key.
type configFileError struct {
	// Path is the file that could not be used.
	Path string
	// cause is the underlying read or parse failure.
	cause error
}

func (e *configFileError) Error() string {
	return "config: configuration file " + e.Path + " cannot be read or parsed"
}

// Unwrap reports the sentinels plus the hidden cause.
func (e *configFileError) Unwrap() []error {
	if e.cause == nil {
		return []error{ErrInvalidConfig, ErrConfigFile}
	}
	return []error{ErrInvalidConfig, ErrConfigFile, e.cause}
}
