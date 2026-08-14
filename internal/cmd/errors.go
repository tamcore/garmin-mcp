package cmd

import "errors"

// ErrNotImplemented reports a command whose subsystem does not exist in this
// milestone. It is a real failure with a non-zero exit status, never a silent
// success: a caller that cannot tell "did nothing" from "did the work" would
// eventually believe an unimplemented capability is in service.
var ErrNotImplemented = errors.New("not implemented in this milestone")

// NotImplementedError names the command and the missing subsystem, and points at
// the authoritative status document instead of guessing a delivery date.
type NotImplementedError struct {
	// Command is the command path the operator invoked, for example
	// "garmin-mcp serve".
	Command string
	// Subsystem is the capability that does not exist yet.
	Subsystem string
}

// notImplemented builds a *NotImplementedError.
func notImplemented(command, subsystem string) error {
	return &NotImplementedError{Command: command, Subsystem: subsystem}
}

// Error states plainly that nothing was done.
func (e *NotImplementedError) Error() string {
	return e.Command + ": " + e.Subsystem + " is " + ErrNotImplemented.Error() +
		"; nothing was started or changed. See docs/implementation-status.md"
}

// Unwrap exposes the sentinel, so a caller can match with errors.Is.
func (e *NotImplementedError) Unwrap() error { return ErrNotImplemented }
