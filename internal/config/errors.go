package config

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

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

// redactedPathMarker replaces a configuration file name that is not a plain,
// bounded name. The delimiters match the markers in secret.go.
const redactedPathMarker = "[redacted-path]"

// maxConfigFileNameLen bounds the rendered file name, so an absurdly long name
// cannot fill an operator's log line.
const maxConfigFileNameLen = 64

// The reasons a configuration file can be rejected. Each is authored here; none
// comes from a parser.
const (
	reasonMissing    = "does not exist"
	reasonUnreadable = "cannot be read"
	reasonUnparsable = "cannot be parsed"
	reasonForbidden  = "cannot be opened: permission denied"
)

// configFileError reports an unreadable or unparsable configuration file.
//
// Neither the underlying cause nor the full path is retained. A parser error
// quotes the offending scalar — "cannot decode !!str `...` as a !!int" is one real
// example — and that scalar may be the inline master key, so an error-chain walker
// or a %+v-style logger would render the secret even though the top-level text is
// clean. Only a sanitized file name and a reason authored by this package survive
// construction, and Unwrap reports only this package's sentinels.
type configFileError struct {
	// name is the sanitized base name of the file, or redactedPathMarker.
	name string
	// reason is one of the reason constants above.
	reason string
}

// newConfigFileError builds a *configFileError from the path the operator gave and
// the cause this package will not expose. cause is classified and then dropped.
func newConfigFileError(path string, cause error) *configFileError {
	return &configFileError{name: configFileName(path), reason: configFileReason(cause)}
}

// Error names the file and the reason, and nothing else.
func (e *configFileError) Error() string {
	return "config: configuration file " + e.name + " " + e.reason
}

// GoString satisfies %#v with the same sanitized rendering, which reflection would
// otherwise bypass.
func (e *configFileError) GoString() string {
	return "config.configFileError(" + e.Error() + ")"
}

// Unwrap reports only the sentinels this package documents. The cause is
// deliberately absent from the chain.
func (e *configFileError) Unwrap() []error {
	return []error{ErrInvalidConfig, ErrConfigFile}
}

// configFileReason classifies cause into one of this package's own reasons, so an
// operator still learns whether the file was missing, unreadable, or malformed
// without any parser text reaching the message.
func configFileReason(cause error) string {
	switch {
	case cause == nil:
		return reasonUnreadable
	case errors.Is(cause, fs.ErrNotExist):
		return reasonMissing
	case errors.Is(cause, fs.ErrPermission):
		return reasonForbidden
	}
	if _, ok := errors.AsType[viper.ConfigParseError](cause); ok {
		return reasonUnparsable
	}
	return reasonUnreadable
}

// configFileName reduces path to its base name, and only when that name is a
// plain, bounded one. The directory is dropped because it discloses the
// deployment's layout, and an unusual name is replaced entirely because the name
// is the last caller-supplied text in the message.
func configFileName(path string) string {
	name := filepath.Base(strings.TrimSpace(path))
	if name == "." || name == string(filepath.Separator) || len(name) > maxConfigFileNameLen {
		return redactedPathMarker
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return redactedPathMarker
		}
	}
	return name
}
